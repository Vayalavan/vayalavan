// Package auth holds password hashing, token minting and the rules that
// govern both.
package auth

import (
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode"

	"golang.org/x/crypto/bcrypt"
)

// MinPasswordLength is the floor from the requirements. Deliberately a floor
// and not a complexity rule: length beats character-class gymnastics, which
// mostly teach people to write "Passw0rd!".
const MinPasswordLength = 8

// maxPasswordLength guards bcrypt's hard 72-byte input limit. Beyond it
// bcrypt silently ignores the tail, so "same first 72 bytes" would mean "same
// password" — better to reject than to quietly truncate.
const maxPasswordLength = 72

//go:embed common_passwords.txt
var commonPasswordList string

// commonPasswords is built once, lazily, into a set for O(1) lookup.
var commonPasswords = sync.OnceValue(func() map[string]struct{} {
	lines := strings.Split(commonPasswordList, "\n")
	set := make(map[string]struct{}, len(lines))
	for _, line := range lines {
		if entry := strings.TrimSpace(strings.ToLower(line)); entry != "" &&
			!strings.HasPrefix(entry, "#") {
			set[entry] = struct{}{}
		}
	}
	return set
})

// ErrWeakPassword is returned by ValidatePassword. Callers surface its message
// to the user, so it must stay free of internal detail.
var ErrWeakPassword = errors.New("auth: password does not meet requirements")

// ValidatePassword enforces the password policy.
//
// Checked before hashing, so a rejected password never reaches storage.
func ValidatePassword(password string) error {
	if len(password) < MinPasswordLength {
		return fmt.Errorf("%w: must be at least %d characters",
			ErrWeakPassword, MinPasswordLength)
	}
	if len(password) > maxPasswordLength {
		return fmt.Errorf("%w: must be at most %d characters",
			ErrWeakPassword, maxPasswordLength)
	}
	// Whitespace-only passwords clear the length bar while being trivially
	// guessable.
	if strings.TrimFunc(password, unicode.IsSpace) == "" {
		return fmt.Errorf("%w: cannot be only whitespace", ErrWeakPassword)
	}
	// Case-insensitive: "PASSWORD" is no stronger than "password".
	if _, common := commonPasswords()[strings.ToLower(password)]; common {
		return fmt.Errorf("%w: this password is too common — pick something less guessable",
			ErrWeakPassword)
	}
	return nil
}

// HashPassword hashes a password with bcrypt at the configured cost.
//
// Cost comes from config (CLAUDE.md §5.1 specifies 12) rather than being
// hardcoded, so it can be raised as hardware gets faster without a code
// change.
func HashPassword(password string, cost int) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return "", fmt.Errorf("auth: hashing password: %w", err)
	}
	return string(hash), nil
}

// VerifyPassword reports whether the password matches the stored hash.
//
// bcrypt's comparison is constant-time with respect to the hash, so this does
// not leak how much of a guess was correct.
func VerifyPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// DummyHash is a valid bcrypt hash of a random value, used to keep failed
// logins as slow as successful ones.
//
// Without this, "unknown email" returns immediately while "wrong password"
// spends ~100ms in bcrypt — a timing difference that lets an attacker
// enumerate which addresses have accounts.
const DummyHash = "$2a$12$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"

// WasteTimeComparing performs a throwaway bcrypt comparison so that a login
// for a non-existent user costs the same as one for a real user.
func WasteTimeComparing(password string) {
	_ = bcrypt.CompareHashAndPassword([]byte(DummyHash), []byte(password))
}
