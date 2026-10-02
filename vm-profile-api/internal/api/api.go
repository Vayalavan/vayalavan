// Package api implements vm-profile-api's HTTP handlers.
//
// Two rules run through every handler here:
//
//   - Ownership is enforced in SQL (queries take the owner's id and filter on
//     it), not by an `if` after an unfiltered read. CLAUDE.md §7.
//   - Errors returned to clients never distinguish "no such account" from
//     "wrong password", so the API cannot be used to enumerate users.
package api

import (
	"context"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vayal-mikrogreenz/vm-go-common/httpx"
	"github.com/vayal-mikrogreenz/vm-go-common/serviceable"

	"github.com/vayal-mikrogreenz/vm-profile-api/internal/auth"
	"github.com/vayal-mikrogreenz/vm-profile-api/internal/mail"
	"github.com/vayal-mikrogreenz/vm-profile-api/internal/store"
)

// User and supplier lifecycle states (mirrors the CHECK constraints).
const (
	statusActive    = "active"
	statusPending   = "pending"
	statusSuspended = "suspended"
	statusApproved  = "approved"
	statusRejected  = "rejected"
)

// API carries the dependencies every handler needs.
type API struct {
	pool       *pgxpool.Pool
	queries    *store.Queries
	sessions   *SessionService
	bcryptCost int
	mailer     mail.Sender
	// appBaseURL is the supplier UI origin, used to build set-password links.
	appBaseURL string
	logger     *slog.Logger
}

// New builds the API.
func New(
	pool *pgxpool.Pool,
	sessions *SessionService,
	bcryptCost int,
	mailer mail.Sender,
	appBaseURL string,
	logger *slog.Logger,
) *API {
	return &API{
		pool:       pool,
		queries:    store.New(pool),
		sessions:   sessions,
		bcryptCost: bcryptCost,
		mailer:     mailer,
		appBaseURL: strings.TrimRight(appBaseURL, "/"),
		logger:     logger,
	}
}

// respond writes a JSON success response.
func (a *API) respond(ctx context.Context, w http.ResponseWriter, status int, body any) {
	httpx.WriteJSON(ctx, w, a.logger, status, body)
}

// fail writes an error in the platform's standard shape.
func (a *API) fail(ctx context.Context, w http.ResponseWriter, err error) {
	httpx.WriteError(ctx, w, a.logger, err)
}

// ---------------------------------------------------------------------------
// Input validation
//
// Deliberately conservative and shared, so two endpoints cannot disagree about
// what a valid phone number is.
// ---------------------------------------------------------------------------

var (
	// Pragmatic rather than RFC 5322-complete: something@something.tld with no
	// spaces. Real validation is sending a mail to it.
	emailPattern = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]{2,}$`)
	// Indian mobile numbers: 10 digits starting 6-9, after normalisation.
	phonePattern = regexp.MustCompile(`^[6-9][0-9]{9}$`)
	// Six digits, never leading zero.
	pincodePattern = regexp.MustCompile(`^[1-9][0-9]{5}$`)
	// IFSC: 4 letters, a 0, then 6 alphanumerics.
	ifscPattern = regexp.MustCompile(`^[A-Z]{4}0[A-Z0-9]{6}$`)
)

// validation accumulates field errors so a client sees every problem at once.
type validation struct {
	fields map[string]any
}

func newValidation() *validation { return &validation{fields: map[string]any{}} }

func (v *validation) add(field, message string) {
	v.fields[field] = message
}

// require checks a trimmed value is present and within length bounds.
func (v *validation) require(field, value string, min, max int) string {
	trimmed := strings.TrimSpace(value)
	switch {
	case trimmed == "":
		v.add(field, "is required")
	case len(trimmed) < min:
		v.add(field, "is too short")
	case len(trimmed) > max:
		v.add(field, "is too long")
	}
	return trimmed
}

func (v *validation) optional(value string, max int) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	if len(trimmed) > max {
		trimmed = trimmed[:max]
	}
	return &trimmed
}

// email normalises and validates an address.
func (v *validation) email(field, value string) string {
	// Lower-cased on the way in as well as being stored CITEXT: the column
	// makes lookups case-insensitive, this keeps what we display consistent.
	normalised := strings.ToLower(strings.TrimSpace(value))
	if normalised == "" {
		v.add(field, "is required")
		return ""
	}
	if !emailPattern.MatchString(normalised) {
		v.add(field, "is not a valid email address")
	}
	return normalised
}

// phone normalises Indian mobile numbers to bare 10 digits.
//
// Accepting "+91 98765 43210", "098765 43210" and "9876543210" as the same
// number matters: otherwise one person registers three times and none of the
// uniqueness constraints help.
func (v *validation) phone(field, value string) string {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, value)

	digits = strings.TrimPrefix(digits, "91")
	digits = strings.TrimPrefix(digits, "0")

	if digits == "" {
		v.add(field, "is required")
		return ""
	}
	if !phonePattern.MatchString(digits) {
		v.add(field, "must be a 10-digit Indian mobile number")
	}
	return digits
}

func (v *validation) pincode(field, value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		v.add(field, "is required")
		return ""
	}
	if !pincodePattern.MatchString(trimmed) {
		v.add(field, "must be a 6-digit PIN code")
	}
	return trimmed
}

// deliveryPincode is pincode plus the delivery area, for a CUSTOMER's address.
//
// Separate from pincode because a SUPPLIER's address is where the produce
// comes from, not where a parcel is sent: refusing to register a grower
// because we do not deliver to their village would be nonsense. suppliers.go
// keeps the plain six-digit check.
//
// Shape is reported before area, and only one of the two: "must be a 6-digit
// PIN code" and "must be in Bengaluru and Tamil Nadu" on the same field would
// make the customer fix one thing twice.
func (v *validation) deliveryPincode(field, value string) string {
	trimmed := v.pincode(field, value)
	if _, failed := v.fields[field]; failed {
		return trimmed
	}
	if !serviceable.Pincode(trimmed) {
		v.add(field, serviceable.PincodeFieldError)
	}
	return trimmed
}

func (v *validation) password(field, value string) string {
	if err := auth.ValidatePassword(value); err != nil {
		// The policy message is written for end users, so pass it through
		// after stripping the wrapping sentinel text.
		v.add(field, strings.TrimPrefix(err.Error(), "auth: password does not meet requirements: "))
	}
	return value
}

// err returns a 422 carrying every field problem, or nil when clean.
func (v *validation) err() error {
	if len(v.fields) == 0 {
		return nil
	}
	return httpx.Validation("Some fields need attention.", v.fields)
}
