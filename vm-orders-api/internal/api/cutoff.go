package api

import (
	"net/http"
	"time"

	"github.com/vayal-mikrogreenz/vm-go-common/isttime"

	"github.com/vayal-mikrogreenz/vm-orders-api/internal/timeline"
)

// Cutoff returns the authoritative order cutoff, computed server-side in IST.
//
// The browser must never decide this for itself (CLAUDE.md rule 2 and the
// client brief): a device clock can be wrong by hours, and a customer told
// they have time when they do not gets a delivery date we cannot honour.
//
// The client counts down against `server_now`, which lets it show a live timer
// without ever trusting its own clock for the DECISION — only for animating
// between polls.
func (a *API) Cutoff(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	now := time.Now()
	schedule := timeline.Compute(now, a.cutoffHour)

	// The next cutoff instant: today's if we are still before it, otherwise
	// tomorrow's — which is exactly what ProcessingAt already resolves to.
	cutoffAt := schedule.ProcessingAt
	beforeCutoff := isttime.IsBeforeCutoff(now, a.cutoffHour)

	a.respond(ctx, w, http.StatusOK, map[string]any{
		// Everything the client needs to render a countdown without guessing.
		"server_now":    now.In(isttime.Location()).Format(time.RFC3339),
		"cutoff_at":     cutoffAt.Format(time.RFC3339),
		"cutoff_hour":   a.cutoffHour,
		"before_cutoff": beforeCutoff,
		// Seconds remaining, so a client with a skewed clock still counts the
		// right duration.
		"seconds_until_cutoff": int64(cutoffAt.Sub(now).Seconds()),

		"processing_at":          schedule.ProcessingAt.Format(time.RFC3339),
		"delivery_day":           schedule.DeliveryDay.Format("2006-01-02"),
		"expected_delivery_date": schedule.ExpectedDeliveryDate.Format("2006-01-02"),
		"expected_delivery_text": schedule.ExpectedDeliveryText(),
		"courier_notice":         timeline.CourierNotice,
		"support_notice":         timeline.SupportNotice + a.supportMail,
	})
}
