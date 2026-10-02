package api

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vayal-mikrogreenz/vm-go-common/httpx"

	"github.com/vayal-mikrogreenz/vm-profile-api/internal/store"
)

type addressRequest struct {
	Label         string `json:"label"`
	RecipientName string `json:"recipient_name"`
	Phone         string `json:"phone"`
	Line1         string `json:"line1"`
	Line2         string `json:"line2"`
	Landmark      string `json:"landmark"`
	City          string `json:"city"`
	State         string `json:"state"`
	Pincode       string `json:"pincode"`
	IsDefault     bool   `json:"is_default"`
}

type addressResponse struct {
	ID            string  `json:"id"`
	Label         *string `json:"label"`
	RecipientName string  `json:"recipient_name"`
	Phone         string  `json:"phone"`
	Line1         string  `json:"line1"`
	Line2         *string `json:"line2"`
	Landmark      *string `json:"landmark"`
	City          string  `json:"city"`
	State         string  `json:"state"`
	Pincode       string  `json:"pincode"`
	IsDefault     bool    `json:"is_default"`
}

func toAddressResponse(a store.Address) addressResponse {
	return addressResponse{
		ID:            a.ID.String(),
		Label:         a.Label,
		RecipientName: a.RecipientName,
		Phone:         a.Phone,
		Line1:         a.Line1,
		Line2:         a.Line2,
		Landmark:      a.Landmark,
		City:          a.City,
		State:         a.State,
		Pincode:       a.Pincode,
		IsDefault:     a.IsDefault,
	}
}

// validateAddress runs the shared field rules for create and update.
func (a *API) validateAddress(req addressRequest) (store.CreateAddressParams, error) {
	v := newValidation()

	params := store.CreateAddressParams{
		Label:         v.optional(req.Label, 40),
		RecipientName: v.require("recipient_name", req.RecipientName, 1, 120),
		Phone:         v.phone("phone", req.Phone),
		Line1:         v.require("line1", req.Line1, 1, 200),
		Line2:         v.optional(req.Line2, 200),
		Landmark:      v.optional(req.Landmark, 120),
		City:          v.require("city", req.City, 1, 80),
		State:         v.require("state", req.State, 1, 80),
		// deliveryPincode, not pincode: this is where a parcel goes, so it
		// has to be inside the delivery area as well as six digits.
		Pincode:   v.deliveryPincode("pincode", req.Pincode),
		IsDefault: req.IsDefault,
	}
	return params, v.err()
}

// ListAddresses returns the caller's own addresses.
func (a *API) ListAddresses(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	actor, err := httpx.RequireActor(ctx)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	rows, err := a.queries.ListAddresses(ctx, actor.UserID)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	out := make([]addressResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, toAddressResponse(row))
	}
	a.respond(ctx, w, http.StatusOK, map[string]any{"addresses": out})
}

// CreateAddress adds an address for the caller.
func (a *API) CreateAddress(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	actor, err := httpx.RequireActor(ctx)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	var req addressRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		a.fail(ctx, w, err)
		return
	}

	params, err := a.validateAddress(req)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	params.UserID = actor.UserID

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := a.queries.WithTx(tx)

	count, err := q.CountLiveAddresses(ctx, actor.UserID)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	// The first address is always the default — a customer with exactly one
	// address and no default would face a checkout that cannot preselect.
	if count == 0 {
		params.IsDefault = true
	}

	// A partial unique index enforces one default per user, so an incoming
	// default must clear the incumbent inside the same transaction or the
	// insert violates it.
	if params.IsDefault {
		if err := q.ClearDefaultAddress(ctx, actor.UserID); err != nil {
			a.fail(ctx, w, httpx.Internal(err))
			return
		}
	}

	created, err := q.CreateAddress(ctx, params)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	a.respond(ctx, w, http.StatusCreated, toAddressResponse(created))
}

// GetAddress returns one of the caller's addresses.
func (a *API) GetAddress(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	actor, id, err := a.actorAndID(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	address, err := a.queries.GetAddress(ctx, store.GetAddressParams{
		ID:     id,
		UserID: actor.UserID,
	})
	if err != nil {
		a.fail(ctx, w, a.addressLookupError(err))
		return
	}
	a.respond(ctx, w, http.StatusOK, toAddressResponse(address))
}

// UpdateAddress edits one of the caller's addresses.
func (a *API) UpdateAddress(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	actor, id, err := a.actorAndID(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	var req addressRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		a.fail(ctx, w, err)
		return
	}
	params, err := a.validateAddress(req)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := a.queries.WithTx(tx)

	updated, err := q.UpdateAddress(ctx, store.UpdateAddressParams{
		ID:            id,
		UserID:        actor.UserID,
		Label:         params.Label,
		RecipientName: params.RecipientName,
		Phone:         params.Phone,
		Line1:         params.Line1,
		Line2:         params.Line2,
		Landmark:      params.Landmark,
		City:          params.City,
		State:         params.State,
		Pincode:       params.Pincode,
	})
	if err != nil {
		a.fail(ctx, w, a.addressLookupError(err))
		return
	}

	// Promotion to default is a separate step from the field update, so that
	// clearing the incumbent only happens when it is actually needed.
	if req.IsDefault && !updated.IsDefault {
		if err := q.ClearDefaultAddress(ctx, actor.UserID); err != nil {
			a.fail(ctx, w, httpx.Internal(err))
			return
		}
		if updated, err = q.SetDefaultAddress(ctx, store.SetDefaultAddressParams{
			ID:     id,
			UserID: actor.UserID,
		}); err != nil {
			a.fail(ctx, w, httpx.Internal(err))
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	a.respond(ctx, w, http.StatusOK, toAddressResponse(updated))
}

// SetDefaultAddress promotes one of the caller's addresses to default.
func (a *API) SetDefaultAddress(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	actor, id, err := a.actorAndID(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := a.queries.WithTx(tx)

	// Confirm ownership before mutating anything.
	if _, err := q.GetAddress(ctx, store.GetAddressParams{ID: id, UserID: actor.UserID}); err != nil {
		a.fail(ctx, w, a.addressLookupError(err))
		return
	}
	if err := q.ClearDefaultAddress(ctx, actor.UserID); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	updated, err := q.SetDefaultAddress(ctx, store.SetDefaultAddressParams{
		ID:     id,
		UserID: actor.UserID,
	})
	if err != nil {
		a.fail(ctx, w, a.addressLookupError(err))
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	a.respond(ctx, w, http.StatusOK, toAddressResponse(updated))
}

// DeleteAddress soft-deletes one of the caller's addresses.
//
// Soft delete only (CLAUDE.md §5.1): orders snapshot the address at placement,
// but a historical order screen still needs the row to exist.
func (a *API) DeleteAddress(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	actor, id, err := a.actorAndID(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := a.queries.WithTx(tx)

	deleted, err := q.SoftDeleteAddress(ctx, store.SoftDeleteAddressParams{
		ID:     id,
		UserID: actor.UserID,
	})
	if err != nil {
		a.fail(ctx, w, a.addressLookupError(err))
		return
	}

	// Deleting the default leaves the customer with addresses but no default,
	// which would break checkout preselection. Promote the newest survivor.
	if deleted.IsDefault {
		if err := q.PromoteNewestAddressToDefault(ctx, actor.UserID); err != nil {
			a.fail(ctx, w, httpx.Internal(err))
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// actorAndID extracts the caller and the {id} path parameter.
func (a *API) actorAndID(r *http.Request) (httpx.Actor, uuid.UUID, error) {
	actor, err := httpx.RequireActor(r.Context())
	if err != nil {
		return httpx.Actor{}, uuid.Nil, err
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		// A malformed id is reported as not-found rather than bad-request, so
		// probing ids cannot distinguish "wrong shape" from "not yours".
		return httpx.Actor{}, uuid.Nil, httpx.NotFound("Address not found.")
	}
	return actor, id, nil
}

// addressLookupError maps a miss to 404.
//
// The owner filter is in the query, so "belongs to someone else" arrives here
// as no-rows and becomes 404 — never 403, which would confirm the address
// exists.
func (a *API) addressLookupError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.NotFound("Address not found.")
	}
	return httpx.Internal(err)
}
