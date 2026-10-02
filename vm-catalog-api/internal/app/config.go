// Package app wires vm-catalog-api's configuration and HTTP routing.
package app

import (
	"time"

	"github.com/vayal-mikrogreenz/vm-go-common/config"

	"github.com/vayal-mikrogreenz/vm-catalog-api/internal/storage"
)

// ServiceName identifies this service in logs and health responses.
const ServiceName = "vm-catalog-api"

// DefaultPort is this service's assigned port from the platform port map.
const DefaultPort = 8082

// EnvPrefix namespaces this service's variables in the single root .env
// (CATALOG_PORT, CATALOG_DATABASE_URL). See .env.example.
const EnvPrefix = "CATALOG"

// Schema is the only Postgres schema this service may touch (CLAUDE.md §3).
const Schema = "catalog"

// Limits caps the untrusted-input paths into this service.
type Limits struct {
	// MaxImageBytes bounds a single product image (CLAUDE.md §6.6: 5 MB).
	MaxImageBytes int64
	// MaxVideoBytes bounds a single product video. An order of magnitude
	// larger than an image because even a ten-second clip off a phone is
	// tens of megabytes, and a limit a supplier trips on every attempt is a
	// feature they stop using.
	MaxVideoBytes int64
	// MaxMediaPerSizeCode bounds ONE grade's gallery. Per size code, not per
	// product, because that is where a gallery lives — a product graded M/L/XL
	// legitimately carries three sets of photographs.
	MaxMediaPerSizeCode int
	// MaxSizeCodesPerProduct bounds the grade tree. A grower has a handful of
	// grades; hundreds means a runaway client, and every one is a row we
	// delete and re-insert on every save.
	MaxSizeCodesPerProduct int
	// MaxCSVBytes bounds a supplier import upload (CLAUDE.md §6.5: 5 MB).
	MaxCSVBytes int64
	// MaxCSVRows bounds rows per import (CLAUDE.md §6.5: 2000).
	MaxCSVRows int
}

// Config is the fully-resolved configuration for vm-catalog-api.
type Config struct {
	Base          config.Base
	Database      config.Database
	InternalToken string
	Storage       storage.Config
	Limits        Limits

	// ProfileAPIURL is where the approved-supplier allow-list comes from.
	ProfileAPIURL string

	// DefaultMarkupBPS is the markup a NEW product starts on, in basis points
	// (500 = 5%). Env-driven like every other rate on the platform
	// (CLAUDE.md rule 3), so changing the commercial default is one variable
	// rather than a deploy.
	//
	// It applies at creation only. Once a product exists its markup is the
	// admin's to set, and lowering this default must not silently reprice
	// produce someone already priced deliberately.
	DefaultMarkupBPS int32

	// EventRetention is how long product_events_outbox rows are kept for
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
		EventRetention: time.Duration(
			l.IntInRange("OUTBOX_RETENTION_DAYS", 7, 1, 365)) * 24 * time.Hour,

		Storage: storage.Config{
			Provider: l.OneOf("STORAGE_PROVIDER", "minio", "minio", "s3"),
			// The single address used both to sign URLs and to fetch them
			// from the browser. There is deliberately no second "public"
			// endpoint — see internal/storage/storage.go.
			Endpoint:       l.RequireString("S3_ENDPOINT"),
			Region:         l.String("S3_REGION", "us-east-1"),
			Bucket:         l.RequireString("S3_BUCKET"),
			AccessKey:      l.RequireString("S3_ACCESS_KEY"),
			SecretKey:      l.RequireString("S3_SECRET_KEY"),
			ForcePathStyle: l.Bool("S3_FORCE_PATH_STYLE", true),
			PresignPutTTL:  l.Duration("S3_PRESIGN_PUT_TTL", 15*time.Minute),
			PresignGetTTL:  l.Duration("S3_PRESIGN_GET_TTL", time.Hour),
		},

		ProfileAPIURL: l.RequireString("PROFILE_API_URL"),

		Limits: Limits{
			MaxImageBytes:          l.Int64("MAX_IMAGE_BYTES", 5*1024*1024),
			MaxVideoBytes:          l.Int64("MAX_VIDEO_BYTES", 50*1024*1024),
			MaxMediaPerSizeCode:    l.Int("MAX_MEDIA_PER_SIZE_CODE", 8),
			MaxSizeCodesPerProduct: l.Int("MAX_SIZE_CODES_PER_PRODUCT", 12),
			MaxCSVBytes:            l.Int64("MAX_CSV_BYTES", 5*1024*1024),
			MaxCSVRows:             l.Int("MAX_CSV_ROWS", 2000),
		},

		// Range-checked at load, so a nonsense rate fails the deploy rather
		// than pricing the storefront (CLAUDE.md rule 3).
		DefaultMarkupBPS: int32(l.IntInRange("PRODUCT_MARKUP_DEFAULT_BPS", 500, 0, 10000)),
	}

	if err := l.Err(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
