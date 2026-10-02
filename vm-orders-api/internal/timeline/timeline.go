// Package timeline computes an order's delivery schedule — CLAUDE.md §6.1.
//
// Two properties matter more than anything else here:
//
//   - The schedule is computed ONCE, at placement, and stored on the order. It
//     is never recomputed, so changing ORDER_CUTOFF_HOUR_IST tomorrow cannot
//     retroactively move a date a customer has already been shown.
//   - Milestone completion is DERIVED from timestamps at read time, not from a
//     flag a cron job sets. A stalled worker then degrades side effects
//     (emails) without ever showing a customer a stale timeline.
package timeline

import (
	"fmt"
	"time"

	"github.com/vayal-mikrogreenz/vm-go-common/isttime"
)

// Milestone names, as the customer sees them (CLAUDE.md §6.1).
const (
	MilestoneReceived  = "Order received"
	MilestoneProcessed = "Order processed"
	MilestoneDelivery  = "Delivery day"
)

// Schedule is an order's fully-resolved timeline.
//
// Every field is an instant or a date in IST. They are stored on the order row
// and read back, never recomputed.
type Schedule struct {
	// PlacedAt is when payment succeeded.
	PlacedAt time.Time
	// ProcessingAt is the cutoff moment the order is processed at.
	ProcessingAt time.Time
	// DeliveryDay is a date: midnight IST at the start of the delivery day.
	DeliveryDay time.Time
	// ExpectedDeliveryDate is a date: what the customer is told.
	ExpectedDeliveryDate time.Time
}

// Compute resolves the schedule from the instant an order was placed.
//
// Implements CLAUDE.md §6.1 exactly:
//
//	placed before cutoff -> processing_date = today
//	placed at or after   -> processing_date = tomorrow
//	processing_at        = processing_date at cutoffHour:00 IST
//	delivery_day         = processing_date + 1 day
//	expected_delivery    = delivery_day + 1 day
//
// placedAt may arrive in any timezone; it is converted to IST first. The
// comparison against the cutoff is strict: an order placed at exactly
// 16:00:00.000 has NOT beaten the cutoff.
func Compute(placedAt time.Time, cutoffHourIST int) Schedule {
	if cutoffHourIST < 0 || cutoffHourIST > 23 {
		// A misconfigured cutoff would mis-schedule every order placed. Fail
		// loudly at the first one rather than shipping wrong dates quietly.
		panic(fmt.Sprintf("timeline: cutoff hour %d out of range 0-23", cutoffHourIST))
	}

	local := isttime.ToIST(placedAt)

	processingDate := isttime.StartOfDayIST(local)
	if !isttime.IsBeforeCutoff(local, cutoffHourIST) {
		processingDate = isttime.AddDays(processingDate, 1)
	}

	deliveryDay := isttime.AddDays(processingDate, 1)

	return Schedule{
		PlacedAt:             local,
		ProcessingAt:         isttime.AtHourIST(processingDate, cutoffHourIST),
		DeliveryDay:          deliveryDay,
		ExpectedDeliveryDate: isttime.AddDays(deliveryDay, 1),
	}
}

// Milestone is one step of the customer-facing timeline.
type Milestone struct {
	Name string `json:"name"`
	At   string `json:"at"`
	// Completed is derived from `now` at read time — never stored.
	Completed bool `json:"completed"`
}

// Milestones renders the three-step timeline as of now.
//
// Exactly three steps (CLAUDE.md §6.1). There is deliberately no "dispatched"
// or "out for delivery": parcels go to third-party couriers and we have no
// tracking, so promising finer granularity than we can observe would be a lie.
func (s Schedule) Milestones(now time.Time) []Milestone {
	return s.MilestonesForStatus(now, "")
}

// MilestonesForStatus renders the timeline, honouring work already done.
//
// Milestone completion is normally derived from timestamps alone (CLAUDE.md
// §6.1) so the display never depends on a job having run. But an admin can
// process an order BEFORE its 4pm cutoff, and when they do the customer should
// see it — a timeline that still says "not yet processed" about a parcel that
// is packed is simply wrong.
//
// So the rule is: complete when the timestamp has passed OR when the order's
// status says it already happened. Time-based completion remains the floor, so
// nothing regresses if a status is ever missing.
func (s Schedule) MilestonesForStatus(now time.Time, status string) []Milestone {
	// A terminal order has no timeline. Not three greyed rows — none: a
	// timeline is a promise about what happens next, and an expired or
	// cancelled order has none to make. The status carries the outcome.
	//
	// This is the fix for a real bug. Completion used to be derived from the
	// clock alone, so an order that expired unpaid at 7pm still announced
	// "Order processed ✓" the next afternoon and "Delivery day ✓" the day
	// after — telling a customer who never paid that their food had been
	// packed and was arriving.
	// Empty slice, never nil: a nil slice marshals to JSON `null`, and the
	// clients call .map on it. `[]` is the contract CLAUDE.md §6.1 describes.
	if isTerminal(status) {
		return []Milestone{}
	}

	processed := s.Processed(now) || statusAtLeast(status, "processed")
	delivered := s.DeliveryDayReached(now)

	return []Milestone{
		{
			Name:      MilestoneReceived,
			At:        s.PlacedAt.Format(time.RFC3339),
			Completed: true,
		},
		{
			Name:      MilestoneProcessed,
			At:        s.ProcessingAt.Format(time.RFC3339),
			Completed: processed,
		},
		{
			Name:      MilestoneDelivery,
			At:        s.DeliveryDay.Format(dateFormat),
			Completed: delivered,
		},
	}
}

// isTerminal reports whether an order has stopped moving for good.
//
// These four are the statuses no clock can advance: the money never arrived,
// or it went back. Kept as a list rather than "not one of the live statuses"
// so an unknown or empty status still gets a timeline — a display must not go
// blank because a new status was added upstream.
func isTerminal(status string) bool {
	switch status {
	case "cancelled", "refunded", "expired", "payment_failed":
		return true
	default:
		return false
	}
}

// statusAtLeast reports whether an order has reached a fulfilment stage.
//
// Only the stages AFTER payment appear here; 'paid' has not been processed.
// Terminal states never reach this function — MilestonesForStatus returns
// early for them.
func statusAtLeast(status, stage string) bool {
	order := map[string]int{"processed": 1, "dispatched": 2}
	return order[status] >= order[stage] && order[stage] > 0
}

const dateFormat = "2006-01-02"

// Processed reports whether the order has passed its processing cutoff.
func (s Schedule) Processed(now time.Time) bool { return !now.Before(s.ProcessingAt) }

// DeliveryDayReached reports whether the delivery day has begun (00:00 IST).
func (s Schedule) DeliveryDayReached(now time.Time) bool { return !now.Before(s.DeliveryDay) }

// Customer-facing copy, fixed by CLAUDE.md §6.1 so every surface says the same
// thing.
const (
	// CourierNotice explains the absence of live tracking.
	CourierNotice = "We hand your order to professional courier partners after " +
		"processing, so live tracking isn't available. Dates shown are estimates."
	// SupportNotice is suffixed with the configured support address.
	SupportNotice = "Any issues? Write to us at "
)

// ExpectedDeliveryText renders the customer-facing delivery line, e.g.
// "Expected delivery: 17 Jun 2026".
func (s Schedule) ExpectedDeliveryText() string {
	return "Expected delivery: " + isttime.FormatDate(s.ExpectedDeliveryDate)
}
