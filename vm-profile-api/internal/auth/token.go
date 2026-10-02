package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Roles recognised across the platform (CLAUDE.md §7).
const (
	RoleCustomer = "customer"
	RoleSupplier = "supplier"
	RoleAdmin    = "admin"
	// RoleAnalyst reads analytics and nothing else (migration 00004).
	RoleAnalyst = "analyst"
)

// Issuer identifies the service that mints access tokens. Verified by the
// gateway so a token from some other system is rejected outright.
const Issuer = "vm-profile-api"

// Audience scopes tokens to this platform.
const Audience = "vayal-mikrogreenz"

// Claims is the access-token payload.
//
// Role is embedded so the gateway can authorise a request without a round
// trip to this service on every call. The cost is that a role change does not
// take effect until the access token expires — acceptable at a 15-minute TTL,
// and the reason that TTL is short.
type Claims struct {
	jwt.RegisteredClaims
	Role   string `json:"role"`
	Status string `json:"status"`
	// SupplierID is set only for supplier accounts. Carrying it in the token
	// means vm-catalog-api can scope every query to the owning supplier
	// without a round trip to vm-profile-api on each request.
	SupplierID string `json:"supplier_id,omitempty"`
}

// Errors returned when a token cannot be accepted.
var (
	ErrInvalidToken = errors.New("auth: token is invalid")
	ErrExpiredToken = errors.New("auth: token has expired")
)

// TokenMinter issues and verifies access tokens.
type TokenMinter struct {
	secret []byte
	ttl    time.Duration
}

// NewTokenMinter builds a minter. The secret is shared with the gateway,
// which verifies what this mints.
func NewTokenMinter(secret string, ttl time.Duration) *TokenMinter {
	return &TokenMinter{secret: []byte(secret), ttl: ttl}
}

// TTL returns the access-token lifetime, for the expires_in field.
func (m *TokenMinter) TTL() time.Duration { return m.ttl }

// Mint issues a signed access token for a user.
func (m *TokenMinter) Mint(userID uuid.UUID, role, status, supplierID string, now time.Time) (string, error) {
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			Issuer:    Issuer,
			Audience:  jwt.ClaimStrings{Audience},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(m.ttl)),
			ID:        uuid.NewString(),
		},
		Role:       role,
		Status:     status,
		SupplierID: supplierID,
	}

	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
	if err != nil {
		return "", fmt.Errorf("auth: signing access token: %w", err)
	}
	return signed, nil
}

// Verify parses and validates an access token.
//
// The algorithm is pinned to HS256. Without pinning, a token with alg=none —
// or one signed with the public half of an asymmetric pair — could be
// accepted: the classic JWT algorithm-confusion attack.
func (m *TokenMinter) Verify(token string) (*Claims, error) {
	parsed, err := jwt.ParseWithClaims(token, &Claims{},
		func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("%w: unexpected signing method %v",
					ErrInvalidToken, t.Header["alg"])
			}
			return m.secret, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(Issuer),
		jwt.WithAudience(Audience),
	)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrExpiredToken
		}
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

	claims, ok := parsed.Claims.(*Claims)
	if !ok || !parsed.Valid {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

// refreshTokenBytes is the entropy in a refresh token. 32 bytes (256 bits)
// makes guessing infeasible regardless of how many are outstanding.
const refreshTokenBytes = 32

// NewRefreshToken returns a new opaque refresh token and its storage hash.
//
// The plaintext is returned to the client exactly once and never stored; only
// the hash is persisted, so a database dump yields no usable sessions.
//
// Opaque and random rather than a second JWT: refresh tokens must be
// revocable, and revoking a stateless token requires the very database lookup
// a JWT is meant to avoid.
func NewRefreshToken() (plaintext, hash string, err error) {
	buf := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("auth: generating refresh token: %w", err)
	}
	plaintext = base64.RawURLEncoding.EncodeToString(buf)
	return plaintext, HashRefreshToken(plaintext), nil
}

// HashRefreshToken returns the storage hash for a refresh token.
//
// Plain SHA-256, deliberately not bcrypt: the input is 256 bits of our own
// randomness, not a user-chosen secret, so there is nothing to brute-force
// and no need for a slow KDF. It also has to be a deterministic hash, since
// lookup is by hash.
func HashRefreshToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// NewOpaqueToken returns a random token and its hash, for password-set links.
func NewOpaqueToken() (plaintext, hash string, err error) {
	return NewRefreshToken()
}
