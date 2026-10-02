// Package server runs an HTTP service with graceful shutdown.
package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/vayal-mikrogreenz/vm-go-common/config"
)

// readHeaderTimeout caps how long a client may take to send its headers.
// Without it, a handful of idle sockets can exhaust the server (Slowloris).
const readHeaderTimeout = 10 * time.Second

// Run starts the HTTP server and blocks until a shutdown signal arrives,
// then drains in-flight requests before returning.
//
// Graceful shutdown is not cosmetic here: a SIGTERM during a deploy must not
// kill a request that is midway through a payment verification or a stock
// reservation transaction. The server stops accepting new connections and
// gives existing ones cfg.ShutdownTimeout to finish.
//
// Returns nil on a clean shutdown, or an error if the listener failed or the
// drain timed out with requests still in flight.
func Run(ctx context.Context, cfg config.Base, handler http.Handler, logger *slog.Logger) error {
	// NotifyContext cancels ctx on the first SIGINT or SIGTERM. A second
	// signal is left to the default handler, so an operator can always force
	// the process down.
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srv := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Buffered so this goroutine can always exit, even if nobody reads the
	// channel because shutdown was signal-driven.
	serveErr := make(chan error, 1)
	go func() {
		logger.InfoContext(ctx, "http server listening",
			slog.String("addr", srv.Addr),
			slog.String("env", string(cfg.AppEnv)),
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case err := <-serveErr:
		// The listener died on its own — almost always a port already in use.
		return err
	case <-ctx.Done():
		logger.Info("shutdown signal received, draining connections",
			slog.Duration("timeout", cfg.ShutdownTimeout))
	}

	// Fresh context: ctx is already cancelled by the signal, and passing it
	// to Shutdown would abort the drain immediately.
	drainCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(drainCtx); err != nil {
		logger.Error("graceful shutdown timed out; forcing close",
			slog.Any("error", err))
		_ = srv.Close()
		return err
	}

	logger.Info("shutdown complete")
	return nil
}
