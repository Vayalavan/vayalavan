package auth

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// bcrypt is deliberately slow, so tests use the cheapest legal cost.
const testCost = 4

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name     string
		password string
		wantErr  bool
	}{
		{"long passphrase", "correct horse battery staple", false},
		// Exactly 8 characters, and deliberately not a list entry — "abcd1234"
		// would fail, correctly, on the common-password check.
		{"exactly the minimum", "vk8mq2ph", false},
		{"mixed characters", "Tr0ub4dor&3", false},
		{"unicode counts as bytes", "पासवर्ड123", false},

		{"empty", "", true},
		{"one under the minimum", "abcd123", true},
		{"whitespace only", "        ", true},
		// bcrypt silently ignores input past 72 bytes, so anything longer
		// must be rejected rather than truncated.
		{"over the bcrypt input limit", strings.Repeat("a", 73), true},

		{"the most common password", "password", true},
		{"common password in caps", "PASSWORD", true},
		{"common password mixed case", "PaSsWoRd", true},
		{"common numeric", "12345678", true},
		{"common leetspeak", "p@ssw0rd", true},
		{"india-specific common", "bharat", true},
		{"platform name", "mikrogreenz", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePassword(tc.password)
			if tc.wantErr && err == nil {
				t.Errorf("ValidatePassword(%q) = nil, want an error", tc.password)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("ValidatePassword(%q) = %v, want nil", tc.password, err)
			}
			if tc.wantErr && err != nil && !errors.Is(err, ErrWeakPassword) {
				t.Errorf("error does not wrap ErrWeakPassword: %v", err)
			}
		})
	}
}

func TestHashAndVerifyPassword(t *testing.T) {
	const password = "correct horse battery staple"

	hash, err := HashPassword(password, testCost)
	if err != nil {
		t.Fatalf("HashPassword returned error: %v", err)
	}

	if hash == password {
		t.Fatal("password was stored in plaintext")
	}
	if !strings.HasPrefix(hash, "$2") {
		t.Errorf("hash %q is not a bcrypt hash", hash)
	}
	if !VerifyPassword(hash, password) {
		t.Error("VerifyPassword rejected the correct password")
	}
	if VerifyPassword(hash, password+"x") {
		t.Error("VerifyPassword accepted a wrong password")
	}
	if VerifyPassword(hash, "") {
		t.Error("VerifyPassword accepted an empty password")
	}

	// Distinct salts mean the same password hashes differently every time,
	// so a dump cannot be scanned for users who share a password.
	second, err := HashPassword(password, testCost)
	if err != nil {
		t.Fatalf("HashPassword returned error: %v", err)
	}
	if hash == second {
		t.Error("two hashes of the same password are identical — salt is not random")
	}
}

func TestVerifyPasswordRejectsGarbageHash(t *testing.T) {
	// A corrupted or empty hash column must fail closed, never open.
	for _, hash := range []string{"", "not-a-hash", "$2a$12$tooshort"} {
		if VerifyPassword(hash, "anything") {
			t.Errorf("VerifyPassword accepted garbage hash %q", hash)
		}
	}
}

func TestDummyHashIsUsable(t *testing.T) {
	// The constant exists to burn the same time a real comparison would. If
	// it were malformed, bcrypt would fail fast and reintroduce the timing
	// difference it is meant to hide.
	if VerifyPassword(DummyHash, "some guess") {
		t.Error("DummyHash matched a guess")
	}
	start := time.Now()
	WasteTimeComparing("some guess")
	if elapsed := time.Since(start); elapsed < time.Millisecond {
		t.Errorf("WasteTimeComparing returned in %v — too fast to mask timing", elapsed)
	}
}

func TestTokenMintAndVerify(t *testing.T) {
	minter := NewTokenMinter("test-secret-value", 15*time.Minute)
	userID := uuid.New()
	now := time.Now()

	token, err := minter.Mint(userID, RoleCustomer, "active", "", now)
	if err != nil {
		t.Fatalf("Mint returned error: %v", err)
	}

	claims, err := minter.Verify(token)
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if claims.Subject != userID.String() {
		t.Errorf("Subject = %q, want %q", claims.Subject, userID)
	}
	if claims.Role != RoleCustomer {
		t.Errorf("Role = %q, want %q", claims.Role, RoleCustomer)
	}
	if claims.Issuer != Issuer {
		t.Errorf("Issuer = %q, want %q", claims.Issuer, Issuer)
	}
}

func TestVerifyRejectsBadTokens(t *testing.T) {
	minter := NewTokenMinter("test-secret-value", 15*time.Minute)
	other := NewTokenMinter("a-completely-different-secret", 15*time.Minute)
	userID := uuid.New()
	now := time.Now()

	valid, err := minter.Mint(userID, RoleAdmin, "active", "", now)
	if err != nil {
		t.Fatalf("Mint returned error: %v", err)
	}

	t.Run("token signed with another secret", func(t *testing.T) {
		foreign, err := other.Mint(userID, RoleAdmin, "active", "", now)
		if err != nil {
			t.Fatalf("Mint returned error: %v", err)
		}
		if _, err := minter.Verify(foreign); err == nil {
			t.Error("accepted a token signed with a different secret")
		}
	})

	t.Run("expired token", func(t *testing.T) {
		expired, err := minter.Mint(userID, RoleAdmin, "active", "", now.Add(-2*time.Hour))
		if err != nil {
			t.Fatalf("Mint returned error: %v", err)
		}
		if _, err := minter.Verify(expired); !errors.Is(err, ErrExpiredToken) {
			t.Errorf("error = %v, want ErrExpiredToken", err)
		}
	})

	t.Run("tampered payload", func(t *testing.T) {
		parts := strings.Split(valid, ".")
		tampered := parts[0] + "." + parts[1] + "x." + parts[2]
		if _, err := minter.Verify(tampered); err == nil {
			t.Error("accepted a token whose payload was modified")
		}
	})

	t.Run("garbage", func(t *testing.T) {
		for _, token := range []string{"", "abc", "a.b.c"} {
			if _, err := minter.Verify(token); err == nil {
				t.Errorf("accepted garbage token %q", token)
			}
		}
	})
}

// TestVerifyRejectsAlgNone is the algorithm-confusion attack: an unsigned
// token declaring alg=none must never be accepted, or anyone can mint an
// admin session by hand.
func TestVerifyRejectsAlgNone(t *testing.T) {
	minter := NewTokenMinter("test-secret-value", 15*time.Minute)

	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   uuid.NewString(),
			Issuer:    Issuer,
			Audience:  jwt.ClaimStrings{Audience},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
		Role:   RoleAdmin,
		Status: "active",
	}

	unsigned, err := jwt.NewWithClaims(jwt.SigningMethodNone, claims).
		SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("building alg=none token: %v", err)
	}

	if _, err := minter.Verify(unsigned); err == nil {
		t.Fatal("SECURITY: accepted an unsigned alg=none token asserting the admin role")
	}
}

// TestVerifyRejectsWrongIssuerAndAudience guards against a token minted for a
// different environment or system being replayed here.
func TestVerifyRejectsWrongIssuerAndAudience(t *testing.T) {
	const secret = "test-secret-value"
	minter := NewTokenMinter(secret, 15*time.Minute)

	for _, tc := range []struct {
		name     string
		issuer   string
		audience string
	}{
		{"wrong issuer", "some-other-service", Audience},
		{"wrong audience", Issuer, "some-other-platform"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims := Claims{
				RegisteredClaims: jwt.RegisteredClaims{
					Subject:   uuid.NewString(),
					Issuer:    tc.issuer,
					Audience:  jwt.ClaimStrings{tc.audience},
					ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
				},
				Role: RoleAdmin,
			}
			token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).
				SignedString([]byte(secret))
			if err != nil {
				t.Fatalf("signing: %v", err)
			}
			if _, err := minter.Verify(token); err == nil {
				t.Errorf("accepted a token with %s", tc.name)
			}
		})
	}
}

func TestRefreshTokenHashing(t *testing.T) {
	plaintext, hash, err := NewRefreshToken()
	if err != nil {
		t.Fatalf("NewRefreshToken returned error: %v", err)
	}

	if plaintext == "" || hash == "" {
		t.Fatal("NewRefreshToken returned an empty value")
	}
	// What is stored must not be what is presented, or a database dump hands
	// over usable sessions.
	if plaintext == hash {
		t.Fatal("refresh token is stored in plaintext")
	}
	if len(hash) != 64 {
		t.Errorf("hash length = %d, want 64 hex characters (SHA-256)", len(hash))
	}
	// Lookup is by hash, so hashing must be deterministic.
	if HashRefreshToken(plaintext) != hash {
		t.Error("HashRefreshToken is not deterministic")
	}

	// Every token must be distinct; a collision would hand one user another's
	// session.
	seen := map[string]struct{}{}
	for i := 0; i < 500; i++ {
		token, _, err := NewRefreshToken()
		if err != nil {
			t.Fatalf("NewRefreshToken returned error: %v", err)
		}
		if _, dup := seen[token]; dup {
			t.Fatal("NewRefreshToken produced a duplicate")
		}
		seen[token] = struct{}{}
	}
}
