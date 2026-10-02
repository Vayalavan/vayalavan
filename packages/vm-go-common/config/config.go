// Package config loads service configuration from the process environment.
//
// CLAUDE.md rule 3: everything configurable comes from env vars, and a
// service must fail fast with a clear message when a required one is missing.
// "Fail fast" here means before the listener opens, and "clear" means every
// problem at once — a loader that dies on the first missing variable turns a
// misconfigured deploy into a five-round guessing game.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Loader accumulates configuration problems instead of failing on the first
// one. Read values eagerly, then call Err exactly once.
//
//	l := config.New()
//	cfg := Config{
//	    Base: config.LoadBase(l, "vm-orders-api", 8083),
//	    KeyID: l.RequireString("RAZORPAY_KEY_ID"),
//	}
//	if err := l.Err(); err != nil { ... }
//
// Values returned before Err is checked are zero-valued when absent, which is
// safe precisely because Err aborts startup.
type Loader struct {
	problems []string
}

// New returns an empty Loader.
func New() *Loader { return &Loader{} }

// Err returns a single error describing every problem found, or nil.
func (l *Loader) Err() error {
	if len(l.problems) == 0 {
		return nil
	}
	return fmt.Errorf("invalid configuration:\n  - %s",
		strings.Join(l.problems, "\n  - "))
}

func (l *Loader) reject(format string, args ...any) {
	l.problems = append(l.problems, fmt.Sprintf(format, args...))
}

// lookup treats an empty or whitespace-only value as absent. An env var set
// to "" in a compose file or CI secret is almost always an accident, and
// silently accepting it produces a confusing failure much later.
func (l *Loader) lookup(key string) (string, bool) {
	v := strings.TrimSpace(os.Getenv(key))
	return v, v != ""
}

// RequireString returns the value of key, recording a problem if it is unset.
func (l *Loader) RequireString(key string) string {
	v, ok := l.lookup(key)
	if !ok {
		l.reject("%s is required but not set", key)
		return ""
	}
	return v
}

// String returns the value of key, or fallback when unset.
func (l *Loader) String(key, fallback string) string {
	if v, ok := l.lookup(key); ok {
		return v
	}
	return fallback
}

// RequireInt returns the integer value of key, recording a problem if it is
// unset or unparseable.
func (l *Loader) RequireInt(key string) int {
	v, ok := l.lookup(key)
	if !ok {
		l.reject("%s is required but not set", key)
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		l.reject("%s must be an integer, got %q", key, v)
		return 0
	}
	return n
}

// Int returns the integer value of key, or fallback when unset. A value that
// is set but unparseable is always a problem — falling back would mask a typo
// in a port number or fee rate.
func (l *Loader) Int(key string, fallback int) int {
	v, ok := l.lookup(key)
	if !ok {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		l.reject("%s must be an integer, got %q", key, v)
		return fallback
	}
	return n
}

// IntInRange behaves like Int but also rejects values outside [min, max].
//
// Used for the settings where an out-of-range value is silently destructive
// rather than immediately fatal — ORDER_CUTOFF_HOUR_IST=25 would otherwise
// mis-schedule every order in the system.
func (l *Loader) IntInRange(key string, fallback, min, max int) int {
	n := l.Int(key, fallback)
	if n < min || n > max {
		l.reject("%s must be between %d and %d, got %d", key, min, max, n)
	}
	return n
}

// Int64 returns the int64 value of key, or fallback when unset.
//
// Distinct from Int because every money and rate value in this system is
// int64 paise or basis points (CLAUDE.md rule 1).
func (l *Loader) Int64(key string, fallback int64) int64 {
	v, ok := l.lookup(key)
	if !ok {
		return fallback
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		l.reject("%s must be an integer, got %q", key, v)
		return fallback
	}
	return n
}

// Bool returns the boolean value of key, or fallback when unset. Accepts the
// forms strconv.ParseBool does: 1, t, true, 0, f, false (any case).
func (l *Loader) Bool(key string, fallback bool) bool {
	v, ok := l.lookup(key)
	if !ok {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		l.reject("%s must be a boolean, got %q", key, v)
		return fallback
	}
	return b
}

// Duration returns the duration value of key (e.g. "15m", "30s"), or fallback
// when unset.
func (l *Loader) Duration(key string, fallback time.Duration) time.Duration {
	v, ok := l.lookup(key)
	if !ok {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		l.reject("%s must be a duration such as \"30s\" or \"15m\", got %q", key, v)
		return fallback
	}
	return d
}

// CSV returns the value of key split on commas with empty entries dropped,
// or fallback when unset. Used for list-shaped settings such as
// CORS_ALLOWED_ORIGINS.
func (l *Loader) CSV(key string, fallback []string) []string {
	v, ok := l.lookup(key)
	if !ok {
		return fallback
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return fallback
	}
	return out
}

// OneOf returns the value of key, rejecting anything outside allowed.
func (l *Loader) OneOf(key, fallback string, allowed ...string) string {
	v := l.String(key, fallback)
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	l.reject("%s must be one of [%s], got %q", key, strings.Join(allowed, ", "), v)
	return fallback
}

// Environment names the deployment environment.
type Environment string

const (
	EnvDevelopment Environment = "development"
	EnvStaging     Environment = "staging"
	EnvProduction  Environment = "production"
)

// IsProduction reports whether this is the production environment. Used to
// gate development-only affordances such as verbose error details.
func (e Environment) IsProduction() bool { return e == EnvProduction }

// Base is the configuration every service in the platform shares.
type Base struct {
	// ServiceName identifies the service in logs. Set in code, not env: it is
	// a property of the binary, not of the deployment.
	ServiceName string
	AppEnv      Environment
	LogLevel    string
	Port        int
	// ShutdownTimeout bounds how long graceful shutdown waits for in-flight
	// requests before forcing the process down.
	ShutdownTimeout time.Duration
}

// Addr returns the listen address for the configured port.
func (b Base) Addr() string { return fmt.Sprintf(":%d", b.Port) }

// LoadBase reads the settings common to all services.
//
// envPrefix namespaces this service's own variables within the single root
// .env — "PROFILE" reads PROFILE_PORT. Genuinely shared settings (APP_ENV,
// LOG_LEVEL) stay unprefixed, because seven processes reading one file
// cannot each own a bare PORT.
//
// defaultPort is the service's slot in the platform port map.
func LoadBase(l *Loader, serviceName, envPrefix string, defaultPort int) Base {
	return Base{
		ServiceName: serviceName,
		AppEnv: Environment(l.OneOf("APP_ENV", string(EnvDevelopment),
			string(EnvDevelopment), string(EnvStaging), string(EnvProduction))),
		LogLevel:        l.OneOf("LOG_LEVEL", "info", "debug", "info", "warn", "error"),
		Port:            l.Int(envPrefix+"_PORT", defaultPort),
		ShutdownTimeout: l.Duration("SHUTDOWN_TIMEOUT", 15*time.Second),
	}
}

// Database holds connection settings for a service's own Postgres schema.
type Database struct {
	URL      string
	MaxConns int32
	// StatementTimeout caps a single query. Generous enough for the CSV import
	// commit, short enough that a runaway query frees its connection.
	StatementTimeout time.Duration
	// LockTimeout caps waiting for a row lock. Much shorter: the checkout
	// transaction locks stock rows, and queueing behind them is worse for a
	// customer than being told to try again.
	LockTimeout time.Duration
	// IdleTxTimeout kills a transaction left open with no work in flight,
	// which would otherwise hold its locks indefinitely.
	IdleTxTimeout time.Duration
}

// LoadDatabase reads <PREFIX>_DATABASE_URL and shared pool sizing.
//
// Required with no fallback: a default pointing at localhost is exactly the
// silent misconfiguration CLAUDE.md rule 3 forbids, because it fails at query
// time rather than at boot.
func LoadDatabase(l *Loader, envPrefix string) Database {
	return Database{
		URL:      l.RequireString(envPrefix + "_DATABASE_URL"),
		MaxConns: int32(l.Int("DB_MAX_CONNS", 10)),
		StatementTimeout: time.Duration(
			l.Int("DB_STATEMENT_TIMEOUT_MS", 15000)) * time.Millisecond,
		LockTimeout: time.Duration(
			l.Int("DB_LOCK_TIMEOUT_MS", 5000)) * time.Millisecond,
		IdleTxTimeout: time.Duration(
			l.Int("DB_IDLE_TX_TIMEOUT_MS", 30000)) * time.Millisecond,
	}
}

// InternalToken reads the shared secret that authenticates service-to-service
// calls from the gateway (CLAUDE.md rule 5). Required everywhere, because a
// Go service that starts without it is publicly callable if ever exposed.
func InternalToken(l *Loader) string {
	return l.RequireString("INTERNAL_SERVICE_TOKEN")
}
