package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vayal-mikrogreenz/vm-go-common/httpx"

	"github.com/vayal-mikrogreenz/vm-profile-api/internal/auth"
	"github.com/vayal-mikrogreenz/vm-profile-api/internal/store"
)

// pgUniqueViolation is the SQLSTATE for a unique constraint breach.
const pgUniqueViolation = "23505"

// isUniqueViolation reports whether err is a unique constraint breach.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation
}

// errBadCredentials is the single response for every failed login.
//
// One message for "no such account" and "wrong password" alike: distinct
// errors would turn the login form into an account-enumeration oracle.
var errBadCredentials = httpx.Unauthorized("Email, phone or password is incorrect.")

// ---------------------------------------------------------------------------
// POST /auth/register
// ---------------------------------------------------------------------------

type registerRequest struct {
	Name     string `json:"name"`
	Phone    string `json:"phone"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type authResponse struct {
	User   userResponse `json:"user"`
	Tokens Tokens       `json:"tokens"`
}

type userResponse struct {
	ID            string  `json:"id"`
	Email         *string `json:"email"`
	Phone         *string `json:"phone"`
	Role          string  `json:"role"`
	Status        string  `json:"status"`
	EmailVerified bool    `json:"email_verified"`
	Name          *string `json:"name,omitempty"`
}

func toUserResponse(u store.User, name *string) userResponse {
	return userResponse{
		ID:            u.ID.String(),
		Email:         u.Email,
		Phone:         u.Phone,
		Role:          u.Role,
		Status:        u.Status,
		EmailVerified: u.EmailVerified,
		Name:          name,
	}
}

// Register creates a customer account and signs them straight in.
func (a *API) Register(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req registerRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		a.fail(ctx, w, err)
		return
	}

	v := newValidation()
	name := v.require("name", req.Name, 1, 120)
	email := v.email("email", req.Email)
	phone := v.phone("phone", req.Phone)
	v.password("password", req.Password)
	if err := v.err(); err != nil {
		a.fail(ctx, w, err)
		return
	}

	hash, err := auth.HashPassword(req.Password, a.bcryptCost)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	// The user row and its profile must appear together — a user without a
	// profile would break GET /me for an account that looks registered.
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := a.queries.WithTx(tx)

	user, err := q.CreateUser(ctx, store.CreateUserParams{
		Email:         &email,
		Phone:         &phone,
		PasswordHash:  &hash,
		Role:          auth.RoleCustomer,
		Status:        statusActive,
		EmailVerified: false,
	})
	if err != nil {
		if isUniqueViolation(err) {
			// Registration unavoidably reveals that an identifier is taken —
			// the alternative is silently doing nothing, which is worse UX
			// and still probeable. Kept vague about WHICH field.
			a.fail(ctx, w, httpx.Conflict("ACCOUNT_EXISTS",
				"An account with this email or phone number already exists."))
			return
		}
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	if _, err := q.CreateCustomerProfile(ctx, store.CreateCustomerProfileParams{
		UserID: user.ID,
		Name:   name,
	}); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	tokens, err := a.sessions.Issue(ctx, q, user, r.UserAgent())
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	a.respond(ctx, w, http.StatusCreated, authResponse{
		User:   toUserResponse(user, &name),
		Tokens: tokens,
	})
}

// ---------------------------------------------------------------------------
// POST /auth/login
// ---------------------------------------------------------------------------

type loginRequest struct {
	// Identifier is an email address or a phone number — customers remember
	// one or the other, not which one they signed up with.
	Identifier string `json:"identifier"`
	Password   string `json:"password"`
}

// Login authenticates and starts a session.
func (a *API) Login(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req loginRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		a.fail(ctx, w, err)
		return
	}

	identifier := strings.TrimSpace(req.Identifier)
	if identifier == "" || req.Password == "" {
		a.fail(ctx, w, errBadCredentials)
		return
	}

	user, err := a.lookupByIdentifier(ctx, identifier)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Spend the same time a real bcrypt comparison would, so response
			// timing does not reveal whether the account exists.
			auth.WasteTimeComparing(req.Password)
			a.fail(ctx, w, errBadCredentials)
			return
		}
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	// An admin-created supplier has no password until it uses its set-password
	// link. Treated as a normal failure so the state is not observable.
	if user.PasswordHash == nil || !auth.VerifyPassword(*user.PasswordHash, req.Password) {
		auth.WasteTimeComparing(req.Password)
		a.fail(ctx, w, errBadCredentials)
		return
	}

	// Only after the password is confirmed do we reveal account state — a
	// suspended account is a fact about a real user, so proving knowledge of
	// the password first is the right order.
	switch user.Status {
	case statusSuspended:
		a.fail(ctx, w, httpx.Forbidden("This account has been suspended. Contact support."))
		return
	case statusPending:
		a.fail(ctx, w, httpx.Forbidden("This account is awaiting approval."))
		return
	}

	tokens, err := a.sessions.Issue(ctx, a.queries, user, r.UserAgent())
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	a.respond(ctx, w, http.StatusOK, authResponse{
		User:   toUserResponse(user, a.displayName(ctx, user)),
		Tokens: tokens,
	})
}

// lookupByIdentifier finds a user by email or phone.
func (a *API) lookupByIdentifier(ctx context.Context, identifier string) (store.User, error) {
	if strings.Contains(identifier, "@") {
		// email is a nullable column, so the generated query takes *string.
		email := strings.ToLower(identifier)
		return a.queries.GetUserByEmail(ctx, &email)
	}

	v := newValidation()
	phone := v.phone("identifier", identifier)
	if len(v.fields) > 0 {
		// Not a usable phone number and not an email — no account can match.
		return store.User{}, pgx.ErrNoRows
	}
	return a.queries.GetUserByPhone(ctx, &phone)
}

// displayName resolves a friendly name for the response, best-effort.
func (a *API) displayName(ctx context.Context, user store.User) *string {
	switch user.Role {
	case auth.RoleCustomer:
		if profile, err := a.queries.GetCustomerProfile(ctx, user.ID); err == nil {
			return &profile.Name
		}
	case auth.RoleSupplier:
		if supplier, err := a.queries.GetSupplierByUserID(ctx, user.ID); err == nil {
			return &supplier.BusinessName
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// POST /auth/refresh
// ---------------------------------------------------------------------------

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// Refresh rotates a refresh token into a fresh token pair.
func (a *API) Refresh(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req refreshRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		a.fail(ctx, w, err)
		return
	}

	tokens, user, err := a.sessions.Rotate(ctx, strings.TrimSpace(req.RefreshToken), r.UserAgent())
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	a.respond(ctx, w, http.StatusOK, authResponse{
		User:   toUserResponse(user, a.displayName(ctx, user)),
		Tokens: tokens,
	})
}

// ---------------------------------------------------------------------------
// POST /auth/logout
// ---------------------------------------------------------------------------

// Logout revokes the presented refresh token.
//
// Always 204, whether or not the token was valid: logout must not double as
// a token-validity oracle, and a client signing out should never see an error
// it cannot act on.
func (a *API) Logout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req refreshRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		a.fail(ctx, w, err)
		return
	}

	if err := a.sessions.Revoke(ctx, strings.TrimSpace(req.RefreshToken)); err != nil {
		a.fail(ctx, w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// GET /me
// ---------------------------------------------------------------------------

type meResponse struct {
	User     userResponse      `json:"user"`
	Supplier *supplierResponse `json:"supplier,omitempty"`
}

// Me returns the authenticated caller's own record.
func (a *API) Me(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	actor, err := httpx.RequireActor(ctx)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	user, err := a.queries.GetUserByID(ctx, actor.UserID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// The token is valid but its subject is gone — treat as signed out.
			a.fail(ctx, w, httpx.Unauthorized("Your session is no longer valid."))
			return
		}
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	resp := meResponse{User: toUserResponse(user, a.displayName(ctx, user))}

	if user.Role == auth.RoleSupplier {
		supplier, err := a.queries.GetSupplierByUserID(ctx, user.ID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			a.fail(ctx, w, httpx.Internal(err))
			return
		}
		if err == nil {
			// Own record: bank details still masked. Only the admin payout
			// screen sees them in full (CLAUDE.md §5.1).
			masked := toSupplierResponse(supplier, false)
			resp.Supplier = &masked
		}
	}

	a.respond(ctx, w, http.StatusOK, resp)
}

// ---------------------------------------------------------------------------
// POST /auth/set-password — completes admin-created supplier onboarding
// ---------------------------------------------------------------------------

type setPasswordRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// SetPassword consumes a one-time link token and sets the account password.
func (a *API) SetPassword(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req setPasswordRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		a.fail(ctx, w, err)
		return
	}

	v := newValidation()
	v.password("password", req.Password)
	if err := v.err(); err != nil {
		a.fail(ctx, w, err)
		return
	}

	invalid := httpx.Unauthorized("This link is invalid or has expired.")

	if strings.TrimSpace(req.Token) == "" {
		a.fail(ctx, w, invalid)
		return
	}

	record, err := a.queries.GetPasswordSetTokenByHash(ctx, auth.HashRefreshToken(req.Token))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			a.fail(ctx, w, invalid)
			return
		}
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	// One-time and time-limited: a link that stays valid forever is a
	// password reset anyone who reads the mailbox later can perform.
	if record.UsedAt != nil || time.Now().After(record.ExpiresAt) {
		a.fail(ctx, w, invalid)
		return
	}

	hash, err := auth.HashPassword(req.Password, a.bcryptCost)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := a.queries.WithTx(tx)

	// Marking used inside the transaction, filtered on used_at IS NULL, makes
	// two concurrent submissions of the same link resolve to exactly one
	// winner.
	if _, err := q.UsePasswordSetToken(ctx, record.ID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			a.fail(ctx, w, invalid)
			return
		}
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	user, err := q.SetUserPassword(ctx, store.SetUserPasswordParams{
		ID:           record.UserID,
		PasswordHash: &hash,
	})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	// Setting a password proves control of the mailbox the link went to.
	if user.Status == statusPending {
		if user, err = q.SetUserStatus(ctx, store.SetUserStatusParams{
			ID:     user.ID,
			Status: statusActive,
		}); err != nil {
			a.fail(ctx, w, httpx.Internal(err))
			return
		}
	}

	tokens, err := a.sessions.Issue(ctx, q, user, r.UserAgent())
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	a.respond(ctx, w, http.StatusOK, authResponse{
		User:   toUserResponse(user, a.displayName(ctx, user)),
		Tokens: tokens,
	})
}
