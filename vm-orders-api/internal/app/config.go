// Package app wires vm-orders-api's configuration and HTTP routing.
package app

import (
	"time"

	"github.com/vayal-mikrogreenz/vm-go-common/config"
	"github.com/vayal-mikrogreenz/vm-go-common/money"

	"github.com/vayal-mikrogreenz/vm-orders-api/internal/api"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/pricing"
)

// ServiceName identifies this service in logs and health responses.
const ServiceName = "vm-orders-api"

// DefaultPort is this service's assigned port from the platform port map.
const DefaultPort = 8083

// EnvPrefix namespaces this service's variables in the single root .env
// (ORDERS_PORT, ORDERS_DATABASE_URL). See .env.example.
const EnvPrefix = "ORDERS"

// Schema is the only Postgres schema this service may touch (CLAUDE.md §3).
const Schema = "orders"

// Pricing holds the money rules from CLAUDE.md §6.2.
//
// Both values are integers by construction — basis points and paise — so no
// float ever enters a fee calculation (CLAUDE.md rule 1).
type Pricing struct {
	// PlatformFeeBPS is the platform fee in basis points. 300 = 3.00%.
	PlatformFeeBPS int64
	// DeliveryFeePaise is the flat delivery charge. 1500 = Rs. 15.
	DeliveryFeePaise money.Paise
	// SupplierCommissionBPS is deducted from the supplier's payout, unlike
	// PlatformFeeBPS which is added to the customer's total. See
	// internal/pricing/rates.go for the whole commercial model.
	SupplierCommissionBPS int64
	// DeliveryMarginPaise is our share of DeliveryFeePaise; the remainder is
	// the courier's cost.
	DeliveryMarginPaise money.Paise
}

// Fulfilment holds the timing rules from CLAUDE.md §6.1 and §6.3.
type Fulfilment struct {
	// CutoffHourIST is the daily order cutoff, in IST. Range-checked at
	// load: a value outside 0-23 would mis-schedule every order placed.
	//
	// One hour, three behaviours, on purpose: it decides each order's
	// processing date (CLAUDE.md §6.1), and it is also the earliest the day's
	// courier sheet is emailed. Moving the cutoff moves the report with it,
	// which is what someone changing it would expect — the sheet exists to
	// describe what the cutoff decided.
	CutoffHourIST int
	// DispatchHourIST is when, on an order's delivery day, it may be marked
	// dispatched automatically. IST, 24-hour clock, range-checked at load.
	//
	// Its own setting rather than the cutoff, because it describes a different
	// physical event: the cutoff is an afternoon deadline for orders, this is
	// the morning the courier collects. Defaults to 0 — dispatch as soon as
	// the delivery day begins — which is the behaviour that shipped. Set it to
	// the collection hour and the customer stops being told their food is on
	// its way while the box is still on our floor.
	DispatchHourIST int
	// ReservationTTL is how long held stock survives without payment before
	// the sweeper releases it.
	ReservationTTL time.Duration
	// EventRetention is how long order_events_outbox rows are kept for
	// replay after CDC has read them (OUTBOX_RETENTION_DAYS, CLAUDE.md §5.4).
	EventRetention time.Duration
	// ProcessorInterval is how often the job looks for orders whose cutoff
	// has passed. It is a poll, not a schedule — see the Processor type in
	// internal/api for why — so this is the worst-case lag between an
	// order's 4pm cutoff and it actually being processed.
	//
	// The same tick also dispatches orders whose delivery day has arrived, so
	// this one interval paces both automatic transitions.
	ProcessorInterval time.Duration
}

// Razorpay holds payment provider credentials.
//
// KeySecret and WebhookSecret are distinct secrets serving distinct purposes:
// the first signs the browser callback, the second signs webhook bodies.
// Both are required — an empty secret would make signature verification
// trivially forgeable rather than merely broken.
// Wallet bounds the prepaid wallet — CLAUDE.md §6.7.
type Wallet struct {
	MinTopupPaise   money.Paise
	MaxTopupPaise   money.Paise
	MaxBalancePaise money.Paise
}

// Schedules configures the scheduled-delivery charging run — CLAUDE.md §6.7.
type Schedules struct {
	// ChargeLead is how long before the processing day's cutoff a delivery is
	// charged. Before, not at: an order written at the cutoff would be dated
	// for the next day's processing.
	ChargeLead time.Duration
	// LowBalancePauseAfter pauses a schedule after this many deliveries in a
	// row were skipped for want of balance.
	LowBalancePauseAfter int
	// RunInterval is how often the run looks for due schedules.
	RunInterval time.Duration
}

type Razorpay struct {
	KeyID         string
	KeySecret     string
	WebhookSecret string
	// WebhookMaxAttempts is how many deliveries of one event may fail for a
	// temporary reason before it is closed as failed and left to a human.
	// Zero means the default.
	WebhookMaxAttempts int
}

// SMTP holds outbound mail settings for the transactional email worker.
type SMTP struct {
	Host     string
	Port     int
	User     string
	Password string
	From     string
}

// DailyReport configures the emailed courier sheet.
//
// It carries its OWN transport rather than reusing SMTP above, because the two
// go to different places on purpose: transactional mail points at MailHog in
// development so an order confirmation never escapes a laptop, while this
// report is meant to reach two real inboxes. Sharing one relay would force a
// choice between mailing the team from a dev machine and not being able to
// test the report at all.
//
// An empty To switches the job off — the right default for a developer's
// machine, where nobody wants a courier sheet every afternoon because they
// left the stack running.
type DailyReport struct {
	To      string
	Subject string
	From    string
	// Password is a Gmail app password, not an account password. It is read
	// from the environment like every other secret and never has a default
	// (CLAUDE.md rule 3, rule 4).
	Password string
	Host     string
	Port     int
}

// Config is the fully-resolved configuration for vm-orders-api.
type Config struct {
	Base          config.Base
	Database      config.Database
	InternalToken string

	Pricing    Pricing
	Fulfilment Fulfilment
	Razorpay   Razorpay
	Wallet     Wallet
	Schedules  Schedules
	SMTP       SMTP
	Report     DailyReport

	// CatalogAPIURL is where stock is reserved (CLAUDE.md §6.3).
	CatalogAPIURL string
	// ProfileAPIURL is where the delivery address is verified.
	ProfileAPIURL string

	// SupportEmail appears on the order timeline and in every transactional
	// email (CLAUDE.md §6.1, §9).
	SupportEmail string
}

// LoadConfig reads configuration from the environment, reporting every
// problem at once rather than failing on the first.
func LoadConfig() (Config, error) {
	l := config.New()

	cfg := Config{
		Base:          config.LoadBase(l, ServiceName, EnvPrefix, DefaultPort),
		Database:      config.LoadDatabase(l, EnvPrefix),
		InternalToken: config.InternalToken(l),

		Pricing: Pricing{
			PlatformFeeBPS: l.Int64("PLATFORM_FEE_BPS", pricing.DefaultPlatformFeeBPS),
			DeliveryFeePaise: money.Paise(l.Int64("DELIVERY_FEE_PAISE",
				int64(pricing.DefaultDeliveryFeePaise))),
			SupplierCommissionBPS: l.Int64("SUPPLIER_COMMISSION_BPS",
				pricing.DefaultSupplierCommissionBPS),
			DeliveryMarginPaise: money.Paise(l.Int64("DELIVERY_MARGIN_PAISE",
				int64(pricing.DefaultDeliveryMarginPaise))),
		},

		Fulfilment: Fulfilment{
			CutoffHourIST:   l.IntInRange("ORDER_CUTOFF_HOUR_IST", 16, 0, 23),
			DispatchHourIST: l.IntInRange("DISPATCH_HOUR_IST", 0, 0, 23),
			ReservationTTL: time.Duration(
				l.Int("RESERVATION_TTL_MINUTES", 15)) * time.Minute,
			EventRetention: time.Duration(
				l.IntInRange("OUTBOX_RETENTION_DAYS", 7, 1, 365)) * 24 * time.Hour,
			// Bounded rather than a plain Int: 0 would turn the ticker into a
			// hot loop hammering Postgres as fast as the connection allows,
			// and anything past an hour would delay processing so far beyond
			// the cutoff that the feature stops meaning anything. A typo
			// should stop the deploy, not degrade it silently (rule 3).
			ProcessorInterval: time.Duration(
				l.IntInRange("ORDER_PROCESSOR_INTERVAL_SECONDS", 60, 5, 3600)) * time.Second,
		},

		Razorpay: Razorpay{
			KeyID:         l.RequireString("RAZORPAY_KEY_ID"),
			KeySecret:     l.RequireString("RAZORPAY_KEY_SECRET"),
			WebhookSecret: l.RequireString("RAZORPAY_WEBHOOK_SECRET"),
			WebhookMaxAttempts: l.IntInRange(
				"WEBHOOK_MAX_ATTEMPTS", api.DefaultWebhookMaxAttempts, 1, 100),
		},

		Wallet: Wallet{
			MinTopupPaise:   money.Paise(l.Int64("WALLET_MIN_TOPUP_PAISE", 10_000)),
			MaxTopupPaise:   money.Paise(l.Int64("WALLET_MAX_TOPUP_PAISE", 1_000_000)),
			MaxBalancePaise: money.Paise(l.Int64("WALLET_MAX_BALANCE_PAISE", 2_000_000)),
		},

		Schedules: Schedules{
			ChargeLead: time.Duration(
				l.IntInRange("SCHEDULE_CHARGE_LEAD_MINUTES", 30, 5, 180)) * time.Minute,
			LowBalancePauseAfter: l.IntInRange("SCHEDULE_LOW_BALANCE_PAUSE_AFTER", 3, 1, 30),
			RunInterval: time.Duration(
				l.IntInRange("SCHEDULE_RUN_INTERVAL_SECONDS", 60, 10, 900)) * time.Second,
		},

		SMTP: SMTP{
			Host:     l.RequireString("SMTP_HOST"),
			Port:     l.Int("SMTP_PORT", 1025),
			User:     l.String("SMTP_USER", ""),
			Password: l.String("SMTP_PASSWORD", ""),
			From:     l.RequireString("SMTP_FROM"),
		},

		Report: DailyReport{
			To:       l.String("MAIL_TO", ""),
			Subject:  l.String("MAIL_SUBJECT", "Vayal Daily Orders Report"),
			From:     l.String("MAIL_FROM", ""),
			Password: l.String("MAIL_APP_PASSWORD", ""),
			// Gmail's submission endpoint. 587 is STARTTLS, which is what the
			// standard library's SendMail negotiates; 465 is implicit TLS and
			// will NOT work here.
			Host: l.String("MAIL_SMTP_HOST", "smtp.gmail.com"),
			Port: l.Int("MAIL_SMTP_PORT", 587),
		},

		CatalogAPIURL: l.RequireString("CATALOG_API_URL"),
		ProfileAPIURL: l.RequireString("PROFILE_API_URL"),

		SupportEmail: l.RequireString("SUPPORT_EMAIL"),
	}

	if err := l.Err(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
