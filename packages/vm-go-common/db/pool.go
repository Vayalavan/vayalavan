// Package db builds the pgx connection pool every Go service uses.
package db

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vayal-mikrogreenz/vm-go-common/config"
)

// connectTimeout bounds the initial connection attempt so a wrong
// DATABASE_URL surfaces at boot instead of hanging startup indefinitely.
const connectTimeout = 10 * time.Second

// NewPool opens and verifies a connection pool.
//
// It pings before returning: a pool is lazy, so without this a bad password
// would first surface as a failed customer request rather than a failed
// deploy.
func NewPool(ctx context.Context, cfg config.Database, logger *slog.Logger) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		// Never wrap the URL itself into the error — it contains the password.
		return nil, fmt.Errorf("db: DATABASE_URL is not a valid connection string: %w", err)
	}

	poolCfg.MaxConns = cfg.MaxConns
	// Keep one warm connection so the first request after an idle period does
	// not pay TCP and TLS setup.
	poolCfg.MinConns = 1
	// Recycle connections well inside typical cloud idle timeouts, so the
	// pool never hands out a connection a middlebox has already dropped.
	poolCfg.MaxConnLifetime = 30 * time.Minute
	poolCfg.MaxConnIdleTime = 5 * time.Minute
	poolCfg.HealthCheckPeriod = 1 * time.Minute
	poolCfg.ConnConfig.ConnectTimeout = connectTimeout

	// Server-side caps on how long a single statement may run.
	//
	// Without these one bad query holds a pooled connection indefinitely: the
	// HTTP request times out and the client gives up, but Postgres keeps
	// executing and the connection never returns to the pool. Enough of those
	// and the pool is exhausted while the database looks idle — the service is
	// down for a reason nothing in the application logs explains.
	//
	// Set on the connection rather than per query so it covers every path,
	// including ones added later that forget.
	//
	// lock_timeout is deliberately much shorter than statement_timeout: the
	// checkout transaction takes `SELECT … FOR UPDATE` on daily_availability
	// (CLAUDE.md §6.3), and waiting minutes for a lock is never the right
	// answer — failing fast lets the customer retry against fresh stock.
	if poolCfg.ConnConfig.RuntimeParams == nil {
		poolCfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	poolCfg.ConnConfig.RuntimeParams["statement_timeout"] = strconv.Itoa(
		int(cfg.StatementTimeout / time.Millisecond))
	poolCfg.ConnConfig.RuntimeParams["lock_timeout"] = strconv.Itoa(
		int(cfg.LockTimeout / time.Millisecond))
	// Bounds a transaction left open by a client that vanished mid-request;
	// an idle transaction holds its locks and blocks the stock rows.
	poolCfg.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] =
		strconv.Itoa(int(cfg.IdleTxTimeout / time.Millisecond))

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("db: creating pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: cannot reach database: %w", err)
	}

	logger.InfoContext(ctx, "database pool ready",
		slog.String("host", poolCfg.ConnConfig.Host),
		slog.String("database", poolCfg.ConnConfig.Database),
		slog.String("user", poolCfg.ConnConfig.User),
		slog.Int("max_conns", int(poolCfg.MaxConns)),
	)

	return pool, nil
}

// Check returns a readiness probe for the pool, for httpx.Readyz.
func Check(pool *pgxpool.Pool) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		return pool.Ping(ctx)
	}
}
