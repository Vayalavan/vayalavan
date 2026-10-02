package api

import (
	"log/slog"

	"github.com/google/uuid"
)

// Small helpers so log call sites stay one line and attribute keys stay
// consistent across handlers.

func slogErr(err error) slog.Attr { return slog.Any("error", err) }

func slogSupplier(id uuid.UUID) slog.Attr { return slog.String("supplier_id", id.String()) }

func slogAdmin(id uuid.UUID) slog.Attr { return slog.String("admin_user_id", id.String()) }
