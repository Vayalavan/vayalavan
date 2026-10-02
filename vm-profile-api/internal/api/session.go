package api

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vayal-mikrogreenz/vm-go-common/httpx"

	"github.com/vayal-mikrogreenz/vm-profile-api/internal/auth"
	"github.com/vayal-mikrogreenz/vm-profile-api/internal/store"
)

// SessionService issues, rotates and revokes login sessions.
type SessionService struct {
	pool       *pgxpool.Pool
	queries    *store.Queries
	minter     *auth.TokenMinter
	refreshTTL time.Duration
	logger     *slog.Logger
}

// NewSessionService builds the service.
func NewSessionService(
	pool *pgxpool.Pool,
	queries *store.Queries,
	minter *auth.TokenMinter,
	refreshTTL time.Duration,
	logger *slog.Logger,
) *SessionService {
	return &SessionService{
		pool:       pool,
		queries:    queries,
		minter:     minter,
		refreshTTL: refreshTTL,
		logger:     logger,
	}
}

// Tokens is what a successful login or refresh returns.
type Tokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	// ExpiresIn is the access token's remaining life in seconds.
	ExpiresIn int `json:"expires_in"`
}

// errInvalidRefresh is returned for every refresh failure.
//
// One indistinguishable error for "unknown", "expired" and "revoked": telling
// a caller which one it was would let them probe for valid token values.
var errInvalidRefresh = httpx.Unauthorized("Your session has expired. Please sign in again.")

// Issue creates a new session for a user.
func (s *SessionService) Issue(
	ctx context.Context,
	q *store.Queries,
	user store.User,
	userAgent string,
) (Tokens, error) {
	now := time.Now()

	access, err := s.minter.Mint(user.ID, user.Role, user.Status,
		s.supplierIDFor(ctx, q, user), now)
	if err != nil {
		return Tokens{}, httpx.Internal(err)
	}

	plaintext, hash, err := auth.NewRefreshToken()
	if err != nil {
		return Tokens{}, httpx.Internal(err)
	}

	if _, err := q.CreateRefreshToken(ctx, store.CreateRefreshTokenParams{
		UserID:    user.ID,
		TokenHash: hash,
		ExpiresAt: now.Add(s.refreshTTL),
		UserAgent: nullableString(userAgent),
	}); err != nil {
		return Tokens{}, httpx.Internal(err)
	}

	return Tokens{
		AccessToken:  access,
		RefreshToken: plaintext,
		TokenType:    "Bearer",
		ExpiresIn:    int(s.minter.TTL().Seconds()),
	}, nil
}

// refreshReplayGrace is how long after rotation a token may be presented again
// without being treated as theft.
//
// Short on purpose: long enough to cover a lost response, a reload mid-request
// or a duplicated client bootstrap; far too short to be useful to someone
// replaying a token stolen from a log or a backup.
const refreshReplayGrace = 30 * time.Second

// Rotate exchanges a refresh token for a fresh pair, invalidating the old one.
//
// Rotation means a stolen refresh token is only useful until the legitimate
// client next refreshes. Reuse detection is what turns that into an alarm:
// if a token that has ALREADY been rotated is presented again, either the
// attacker or the real user is replaying a token the other has since
// exchanged. We cannot tell which, so every session for that user is revoked
// and both are forced to sign in again. That is the intended, safe outcome.
func (s *SessionService) Rotate(
	ctx context.Context,
	presented, userAgent string,
) (Tokens, store.User, error) {
	if presented == "" {
		return Tokens{}, store.User{}, errInvalidRefresh
	}
	hash := auth.HashRefreshToken(presented)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Tokens{}, store.User{}, httpx.Internal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once committed

	q := s.queries.WithTx(tx)

	existing, err := q.GetRefreshTokenByHash(ctx, hash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Never issued, or already deleted by the expiry sweeper.
			return Tokens{}, store.User{}, errInvalidRefresh
		}
		return Tokens{}, store.User{}, httpx.Internal(err)
	}

	// --- replay grace window -------------------------------------------------
	//
	// A rotation the client never received looks EXACTLY like a stolen token
	// being replayed: in both cases an already-rotated token arrives again.
	// Treating every such case as theft signed people out for a dropped
	// packet, a reload during the request, or a duplicated bootstrap — a
	// security control firing almost entirely on legitimate users.
	//
	// So: if the presented token was rotated moments ago and its successor is
	// still healthy, this is the lost-response case. Rotate from the successor
	// and hand the client a working pair. The chain stays linear and the
	// successor is consumed, so this cannot be repeated indefinitely.
	//
	// Outside the window, reuse is still treated as theft and still revokes
	// every session. An attacker would need the token within seconds of the
	// real client's own rotation, which is a far better trade than signing
	// users out whenever the network hiccups.
	if existing.RevokedAt != nil && existing.ReplacedBy != nil &&
		time.Since(*existing.RevokedAt) <= refreshReplayGrace {

		successor, succErr := q.GetRefreshTokenByID(ctx, *existing.ReplacedBy)
		if succErr == nil && successor.RevokedAt == nil &&
			time.Now().Before(successor.ExpiresAt) {

			s.logger.InfoContext(ctx, "refresh replay within grace window — reissuing",
				slog.String("user_id", existing.UserID.String()),
				slog.String("token_id", existing.ID.String()),
			)
			// Continue the rotation below against the successor, which is the
			// token the client would have used had it received the response.
			existing = successor
		}
	}

	// --- reuse detection -----------------------------------------------------
	if existing.RevokedAt != nil {
		s.logger.WarnContext(ctx, "refresh token reuse detected — revoking all sessions",
			slog.String("user_id", existing.UserID.String()),
			slog.String("token_id", existing.ID.String()),
			slog.Time("originally_revoked_at", *existing.RevokedAt),
		)

		revoked, revokeErr := q.RevokeAllUserRefreshTokens(ctx, existing.UserID)
		if revokeErr != nil {
			return Tokens{}, store.User{}, httpx.Internal(revokeErr)
		}
		if err := tx.Commit(ctx); err != nil {
			return Tokens{}, store.User{}, httpx.Internal(err)
		}

		s.logger.WarnContext(ctx, "sessions revoked after reuse",
			slog.String("user_id", existing.UserID.String()),
			slog.Int64("sessions_revoked", revoked),
		)
		return Tokens{}, store.User{}, errInvalidRefresh
	}

	if time.Now().After(existing.ExpiresAt) {
		return Tokens{}, store.User{}, errInvalidRefresh
	}

	user, err := q.GetUserByID(ctx, existing.UserID)
	if err != nil {
		return Tokens{}, store.User{}, httpx.Internal(err)
	}
	// A suspended account must not be able to refresh its way to a new access
	// token; otherwise suspension would not take effect until the refresh
	// token itself expired, up to 30 days later.
	if user.Status != statusActive {
		return Tokens{}, store.User{}, httpx.Forbidden("This account is not active.")
	}

	now := time.Now()

	access, err := s.minter.Mint(user.ID, user.Role, user.Status,
		s.supplierIDFor(ctx, q, user), now)
	if err != nil {
		return Tokens{}, store.User{}, httpx.Internal(err)
	}

	plaintext, newHash, err := auth.NewRefreshToken()
	if err != nil {
		return Tokens{}, store.User{}, httpx.Internal(err)
	}

	created, err := q.CreateRefreshToken(ctx, store.CreateRefreshTokenParams{
		UserID:    user.ID,
		TokenHash: newHash,
		ExpiresAt: now.Add(s.refreshTTL),
		UserAgent: nullableString(userAgent),
	})
	if err != nil {
		return Tokens{}, store.User{}, httpx.Internal(err)
	}

	// Revoke the presented token, recording its successor so a later reuse can
	// be traced to the exact chain it came from.
	if _, err := q.RevokeRefreshToken(ctx, store.RevokeRefreshTokenParams{
		ID:         existing.ID,
		ReplacedBy: &created.ID,
	}); err != nil {
		return Tokens{}, store.User{}, httpx.Internal(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Tokens{}, store.User{}, httpx.Internal(err)
	}

	return Tokens{
		AccessToken:  access,
		RefreshToken: plaintext,
		TokenType:    "Bearer",
		ExpiresIn:    int(s.minter.TTL().Seconds()),
	}, user, nil
}

// Revoke invalidates a single refresh token (logout).
//
// Idempotent and deliberately silent about whether the token existed: logout
// must not double as a token-validity oracle.
func (s *SessionService) Revoke(ctx context.Context, presented string) error {
	if presented == "" {
		return nil
	}

	existing, err := s.queries.GetRefreshTokenByHash(ctx, auth.HashRefreshToken(presented))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return httpx.Internal(err)
	}
	if existing.RevokedAt != nil {
		return nil
	}

	if _, err := s.queries.RevokeRefreshToken(ctx, store.RevokeRefreshTokenParams{
		ID:         existing.ID,
		ReplacedBy: nil,
	}); err != nil {
		return httpx.Internal(err)
	}
	return nil
}

// supplierIDFor resolves a supplier account's own supplier id, for the token
// claim. Best-effort: a supplier row that does not exist yet yields an empty
// claim, and catalog requests will be rejected for want of it — which is the
// correct outcome, not a login failure.
func (s *SessionService) supplierIDFor(
	ctx context.Context, q *store.Queries, user store.User,
) string {
	if user.Role != auth.RoleSupplier {
		return ""
	}
	supplier, err := q.GetSupplierByUserID(ctx, user.ID)
	if err != nil {
		return ""
	}
	return supplier.ID.String()
}

func nullableString(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}
