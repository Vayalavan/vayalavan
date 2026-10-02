package httpx

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

// Headers the gateway uses to forward the authenticated identity to internal
// services, after it has verified the JWT.
//
// Internal services trust these headers ONLY because InternalAuth has already
// proved the caller holds INTERNAL_SERVICE_TOKEN, i.e. is the gateway. If
// these services were ever reachable directly, spoofing X-User-Role: admin
// would be trivial — which is why CLAUDE.md rule 5 (only the gateway is
// public) is a security control, not a topology preference.
const (
	UserIDHeader   = "X-User-Id"
	UserRoleHeader = "X-User-Role"
	// SupplierIDHeader carries a supplier account's own supplier id, taken
	// from the verified token. Services scope every supplier-owned query to
	// it, so it must never be read from client input.
	SupplierIDHeader = "X-Supplier-Id"
)

// Roles recognised across the platform (CLAUDE.md §7).
const (
	RoleCustomer = "customer"
	RoleSupplier = "supplier"
	RoleAdmin    = "admin"
	// RoleAnalyst sees the admin console's Analytics section and nothing
	// else. No Go service grants it anything; it exists so a header carrying
	// it is recognised rather than treated as unknown.
	RoleAnalyst = "analyst"
)

// Actor is the authenticated caller behind a request.
type Actor struct {
	UserID uuid.UUID
	Role   string
	// SupplierID is set only for supplier accounts.
	SupplierID uuid.UUID
}

// RequireSupplierID returns the caller's supplier id, or an error.
//
// A supplier token without one means the account has no supplier row — a
// broken state that must fail closed rather than fall through to a query
// scoped by the zero UUID, which would match nothing or, worse, something.
func (a Actor) RequireSupplierID() (uuid.UUID, error) {
	if a.Role != RoleSupplier || a.SupplierID == uuid.Nil {
		return uuid.Nil, Forbidden("This account is not linked to a supplier.")
	}
	return a.SupplierID, nil
}

// IsAdmin reports whether the actor is an administrator.
func (a Actor) IsAdmin() bool { return a.Role == RoleAdmin }

type actorKey struct{}

// WithActor returns a context carrying the authenticated caller.
func WithActor(ctx context.Context, actor Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, actor)
}

// ActorFrom returns the authenticated caller from the context.
func ActorFrom(ctx context.Context) (Actor, bool) {
	actor, ok := ctx.Value(actorKey{}).(Actor)
	return actor, ok
}

// RequireActor returns the caller, or an Unauthorized error.
//
// Handlers call this rather than reading the context directly, so a missing
// identity can never be silently treated as "some zero-valued user".
func RequireActor(ctx context.Context) (Actor, error) {
	actor, ok := ActorFrom(ctx)
	if !ok {
		return Actor{}, Unauthorized("Authentication is required.")
	}
	return actor, nil
}

// ActorContext parses the gateway's identity headers onto the context.
//
// Absent or malformed headers mean an anonymous request, not an error: public
// routes (register, login, supplier application) run through this same
// middleware. Enforcement is the job of RequireActor and RequireRole.
func ActorContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawID := r.Header.Get(UserIDHeader)
		role := r.Header.Get(UserRoleHeader)

		if rawID != "" && role != "" {
			if id, err := uuid.Parse(rawID); err == nil {
				actor := Actor{UserID: id, Role: role}
				if supplierID, err := uuid.Parse(r.Header.Get(SupplierIDHeader)); err == nil {
					actor.SupplierID = supplierID
				}
				r = r.WithContext(WithActor(r.Context(), actor))
			}
		}
		next.ServeHTTP(w, r)
	})
}

// RequireRole returns middleware admitting only the listed roles.
//
// Defence in depth: the gateway already guards these routes, but CLAUDE.md §7
// requires ownership and role checks in the service layer too, so a routing
// mistake at the edge cannot expose an admin endpoint.
func RequireRole(roles ...string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(roles))
	for _, r := range roles {
		allowed[r] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			actor, ok := ActorFrom(r.Context())
			if !ok {
				WriteError(r.Context(), w, LoggerFrom(r.Context()),
					Unauthorized("Authentication is required."))
				return
			}
			if _, permitted := allowed[actor.Role]; !permitted {
				WriteError(r.Context(), w, LoggerFrom(r.Context()),
					Forbidden("You do not have access to this resource."))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
