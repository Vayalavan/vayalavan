package projector

import (
	"context"
	"log/slog"
	"time"

	"github.com/vayal-mikrogreenz/vm-events-consumer-api/internal/store"
)

// RunPruner deletes processed_events ids older than retention, hourly, until
// ctx is cancelled. The ids only need to outlive any replay the source can
// produce, and that is bounded by OUTBOX_RETENTION_DAYS on the source side.
func (p *Projector) RunPruner(ctx context.Context, retention time.Duration) {
	queries := store.New(p.pool)
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pruned, err := queries.PruneProcessedEvents(ctx, time.Now().Add(-retention))
			if err != nil {
				p.logger.WarnContext(ctx, "pruning processed events failed", slog.Any("error", err))
				continue
			}
			if pruned > 0 {
				p.logger.InfoContext(ctx, "pruned processed events", slog.Int64("rows", pruned))
			}
		}
	}
}
