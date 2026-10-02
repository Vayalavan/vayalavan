// Package app wires vm-events-consumer-api's configuration and HTTP routing.
package app

import (
	"time"

	"github.com/vayal-mikrogreenz/vm-go-common/config"
)

// ServiceName identifies this service in logs and health responses.
const ServiceName = "vm-events-consumer-api"

// DefaultPort is this service's slot in the platform port map.
const DefaultPort = 8084

// EnvPrefix namespaces this service's variables in the root .env
// (EVENTS_CONSUMER_PORT, EVENTS_CONSUMER_DATABASE_URL).
const EnvPrefix = "EVENTS_CONSUMER"

// Schema is the only Postgres schema this service may write (CLAUDE.md §3,
// §5.4). It reads the others only as a replication stream.
const Schema = "analytics"

// CDC configures the logical-replication stream.
type CDC struct {
	// ReplicationURL connects as the REPLICATION role, with
	// replication=database. A different role from Database.URL on purpose:
	// the stream's role can read the WAL and nothing else, and the writer can
	// write the analytics schema and nothing else.
	ReplicationURL string
	SlotName       string
	Publication    string
	// StatusInterval is how often the consumer reports its position to
	// Postgres while idle, which also keeps the connection alive.
	StatusInterval time.Duration
}

// Config is the fully-resolved configuration.
type Config struct {
	Base          config.Base
	Database      config.Database
	InternalToken string
	CDC           CDC
	// ProcessedRetention is how long event ids are kept for dedupe. Longer
	// than any replay the source can produce (OUTBOX_RETENTION_DAYS).
	ProcessedRetention time.Duration
}

// LoadConfig reads configuration from the environment, reporting every
// problem at once rather than failing on the first.
func LoadConfig() (Config, error) {
	l := config.New()

	cfg := Config{
		Base:          config.LoadBase(l, ServiceName, EnvPrefix, DefaultPort),
		Database:      config.LoadDatabase(l, EnvPrefix),
		InternalToken: config.InternalToken(l),
		CDC: CDC{
			ReplicationURL: l.RequireString(EnvPrefix + "_REPLICATION_URL"),
			SlotName:       l.RequireString("CDC_SLOT_NAME"),
			Publication:    l.RequireString("CDC_PUBLICATION"),
			StatusInterval: time.Duration(
				l.IntInRange("CDC_STATUS_INTERVAL_SECONDS", 10, 1, 300)) * time.Second,
		},
		ProcessedRetention: 30 * 24 * time.Hour,
	}

	if err := l.Err(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
