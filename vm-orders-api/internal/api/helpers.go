package api

import (
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5/pgconn"
)

const pgUniqueViolation = "23505"

// isUniqueViolation reports whether err is a unique constraint breach. For
// order creation this is how a concurrent request with the same
// Idempotency-Key announces itself.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation
}

func slog2(err error) slog.Attr { return slog.Any("error", err) }
