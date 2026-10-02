package api

import "log/slog"

// Small helpers so log call sites stay one line and attribute keys stay
// consistent across handlers.

func slog2(err error) slog.Attr { return slog.Any("error", err) }

func slogStr(key, value string) slog.Attr { return slog.String(key, value) }
