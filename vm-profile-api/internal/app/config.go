// Package app wires vm-profile-api's configuration and HTTP routing.
package app

import (
	"time"

	"github.com/vayal-mikrogreenz/vm-go-common/config"
)

// ServiceName identifies this service in logs and health responses.
const ServiceName = "vm-profile-api"

// DefaultPort is this service's assigned port from the platform port map.
const DefaultPort = 8081

// EnvPrefix namespaces this service's variables in the single root .env
// (PROFILE_PORT, PROFILE_DATABASE_URL). See .env.example.
const EnvPrefix = "PROFILE"

// Schema is the only Postgres schema this service may touch (CLAUDE.md §3).
const Schema = "profile"

// SMTP holds outbound mail settings, shared with vm-orders-api via the root
// .env — both send transactional mail through the same relay.
type SMTP struct {
	Host     string
	Port     int
	User     string
	Password string
	From     string
}

// Config is the fully-resolved configuration for vm-profile-api.
type Config struct {
	Base          config.Base
	Database      config.Database
	InternalToken string

	// JWTSecret signs access tokens and is shared with the gateway, which
	// verifies them. A mismatch fails every authenticated request.
	JWTSecret string
	// AccessTokenTTL bounds how long a stolen access token stays useful.
	AccessTokenTTL time.Duration
	// RefreshTokenTTL bounds how long a session can be silently renewed.
	RefreshTokenTTL time.Duration
	// BcryptCost is the password hashing work factor (CLAUDE.md §5.1: 12).
	BcryptCost int

	SMTP SMTP

	// SupplierAppURL is the supplier UI origin, used to build the
	// set-password links emailed to admin-created suppliers. Config rather
	// than a constant so a staging deployment links to staging.
	SupplierAppURL string

	// EventRetention is how long supplier_events_outbox rows are kept for
	// replay after CDC has read them (OUTBOX_RETENTION_DAYS, CLAUDE.md §5.4).
	EventRetention time.Duration
}

// LoadConfig reads configuration from the environment, reporting every
// problem at once rather than failing on the first.
func LoadConfig() (Config, error) {
	l := config.New()

	cfg := Config{
		Base:          config.LoadBase(l, ServiceName, EnvPrefix, DefaultPort),
		Database:      config.LoadDatabase(l, EnvPrefix),
		InternalToken: config.InternalToken(l),

		JWTSecret:       l.RequireString("JWT_SECRET"),
		AccessTokenTTL:  time.Duration(l.Int("JWT_ACCESS_TTL_MIN", 15)) * time.Minute,
		RefreshTokenTTL: time.Duration(l.Int("JWT_REFRESH_TTL_DAYS", 30)) * 24 * time.Hour,
		BcryptCost:      l.IntInRange("BCRYPT_COST", 12, 10, 15),

		SMTP: SMTP{
			Host:     l.RequireString("SMTP_HOST"),
			Port:     l.Int("SMTP_PORT", 1025),
			User:     l.String("SMTP_USER", ""),
			Password: l.String("SMTP_PASSWORD", ""),
			From:     l.RequireString("SMTP_FROM"),
		},

		SupplierAppURL: l.RequireString("SUPPLIER_APP_URL"),
		EventRetention: time.Duration(
			l.IntInRange("OUTBOX_RETENTION_DAYS", 7, 1, 365)) * 24 * time.Hour,
	}

	if err := l.Err(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
