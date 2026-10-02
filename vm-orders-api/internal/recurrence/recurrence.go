// Package recurrence is the date arithmetic for scheduled and repeat orders —
// CLAUDE.md §6.7.
//
// Every date here is an EXPECTED DELIVERY date: the day the customer is told
// the produce arrives, which is what they pick. The charging run for a date
// happens on its processing day, two days earlier, shortly before that day's
// cutoff — the same arithmetic §6.1 applies to a checkout order, read
// backwards:
//
//	processing day  = delivery - 2   (charged, reserved and processed here)
//	delivery day    = delivery - 1   (handed to the courier)
//	expected        = delivery       (what the customer chose)
//
// Dates are civil dates in IST. Inputs may arrive as IST midnight (from the
// timeline package) or UTC midnight (a DATE column read back through pgx);
// both name the same calendar day in IST, so everything is normalised through
// Day before any comparison.
package recurrence

import (
	"errors"
	"sort"
	"time"

	"github.com/vayal-mikrogreenz/vm-go-common/isttime"

	"github.com/vayal-mikrogreenz/vm-orders-api/internal/timeline"
)

// Frequency is how a schedule repeats.
type Frequency string

const (
	Once    Frequency = "once"
	Daily   Frequency = "daily"
	Weekly  Frequency = "weekly"
	Monthly Frequency = "monthly"
)

// MaxLeadDays is how far ahead the first delivery may be. Far enough for a
// month's planning; near enough that the catalogue a customer chose from is
// still roughly the one that will be there.
const MaxLeadDays = 60

// processingLeadDays is the gap between the processing day and the expected
// delivery date — §6.1's delivery_day + 1 and processing + 1.
const processingLeadDays = 2

// maxScan bounds every search. A weekly rule finds its next date within seven
// days and a monthly one within a month; this is a guard against a malformed
// rule looping, not a limit a valid one approaches.
const maxScan = 400

// Rule is one schedule's repeat pattern.
type Rule struct {
	Frequency Frequency
	// Weekdays for Weekly: 0 = Sunday .. 6 = Saturday.
	Weekdays []int
	// DayOfMonth for Monthly, 1-31. A day a month lacks falls on its last day.
	DayOfMonth int
	Start      time.Time
	// End is inclusive; the zero value repeats forever.
	End time.Time
}

// Day normalises t to midnight IST of the calendar day it names in IST.
func Day(t time.Time) time.Time {
	local := isttime.ToIST(t)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, isttime.Location())
}

// Validate reports a rule the schedule form should have refused.
func (r Rule) Validate() error {
	switch r.Frequency {
	case Once, Daily:
	case Weekly:
		if len(r.Weekdays) == 0 {
			return errors.New("choose at least one day of the week")
		}
		for _, day := range r.Weekdays {
			if day < 0 || day > 6 {
				return errors.New("weekdays must be 0 (Sunday) to 6 (Saturday)")
			}
		}
	case Monthly:
		if r.DayOfMonth < 1 || r.DayOfMonth > 31 {
			return errors.New("day of month must be 1 to 31")
		}
	default:
		return errors.New("frequency must be once, daily, weekly or monthly")
	}
	if r.Start.IsZero() {
		return errors.New("a start date is required")
	}
	if !r.End.IsZero() && Day(r.End).Before(Day(r.Start)) {
		return errors.New("the end date is before the start date")
	}
	return nil
}

// NextOnOrAfter returns the first delivery date of the rule on or after from,
// and false when the rule has none left (a one-off already past, or beyond
// End).
func (r Rule) NextOnOrAfter(from time.Time) (time.Time, bool) {
	start := Day(r.Start)
	day := Day(from)
	if day.Before(start) {
		day = start
	}

	if r.Frequency == Once {
		if day.Equal(start) {
			return start, r.withinEnd(start)
		}
		return time.Time{}, false
	}

	for i := 0; i < maxScan; i++ {
		if !r.withinEnd(day) {
			return time.Time{}, false
		}
		if r.matches(day) {
			return day, true
		}
		day = isttime.AddDays(day, 1)
	}
	return time.Time{}, false
}

// After returns the first delivery date strictly after day.
func (r Rule) After(day time.Time) (time.Time, bool) {
	return r.NextOnOrAfter(isttime.AddDays(Day(day), 1))
}

// Upcoming lists up to n delivery dates on or after from.
func (r Rule) Upcoming(from time.Time, n int) []time.Time {
	out := make([]time.Time, 0, n)
	next, ok := r.NextOnOrAfter(from)
	for ok && len(out) < n {
		out = append(out, next)
		next, ok = r.After(next)
	}
	return out
}

func (r Rule) withinEnd(day time.Time) bool {
	return r.End.IsZero() || !day.After(Day(r.End))
}

func (r Rule) matches(day time.Time) bool {
	switch r.Frequency {
	case Daily:
		return true
	case Weekly:
		weekday := int(day.Weekday())
		for _, wanted := range r.Weekdays {
			if wanted == weekday {
				return true
			}
		}
		return false
	case Monthly:
		return day.Day() == clampDay(day.Year(), day.Month(), r.DayOfMonth)
	}
	return false
}

// clampDay is dayOfMonth, or the month's last day when it has fewer.
func clampDay(year int, month time.Month, dayOfMonth int) int {
	last := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if dayOfMonth > last {
		return last
	}
	return dayOfMonth
}

// NormaliseWeekdays sorts and de-duplicates, so a stored rule reads the same
// however the form sent it.
func NormaliseWeekdays(days []int) []int {
	seen := map[int]bool{}
	out := make([]int, 0, len(days))
	for _, day := range days {
		if !seen[day] {
			seen[day] = true
			out = append(out, day)
		}
	}
	sort.Ints(out)
	return out
}

// ProcessingDate is the day a delivery date is charged, reserved and
// processed on.
func ProcessingDate(delivery time.Time) time.Time {
	return isttime.AddDays(Day(delivery), -processingLeadDays)
}

// ChargeAt is when the charging run attempts a delivery date: leadBefore
// ahead of its processing day's cutoff. Before the cutoff, so the order it
// writes is processed that day and draws on that day's stock, exactly as a
// checkout order placed then would.
func ChargeAt(delivery time.Time, cutoffHourIST int, leadBefore time.Duration) time.Time {
	return isttime.AtHourIST(ProcessingDate(delivery), cutoffHourIST).Add(-leadBefore)
}

// Locked reports whether a delivery date can no longer be changed by the
// customer — skipped, unskipped, or its items edited — because its charging
// run has started or passed.
func Locked(delivery, now time.Time, cutoffHourIST int, leadBefore time.Duration) bool {
	return !now.Before(ChargeAt(delivery, cutoffHourIST, leadBefore))
}

// EarliestStart is the first delivery date a new schedule may begin on: the
// day after the one an order placed now would arrive. The same day as a
// checkout order would just be a checkout order, and one day later leaves the
// charging run at least a day away — time for the customer to top up.
func EarliestStart(now time.Time, cutoffHourIST int) time.Time {
	return isttime.AddDays(Day(timeline.Compute(now, cutoffHourIST).ExpectedDeliveryDate), 1)
}

// LatestStart is the furthest-out first delivery a schedule may begin on.
func LatestStart(now time.Time) time.Time {
	return isttime.AddDays(Day(now), MaxLeadDays)
}

// Timeline returns the §6.1 schedule for an order charged on its processing
// day. Computed from the DATE rather than from the clock, so a run that lands
// a minute late still dates the order for the delivery the customer chose.
func Timeline(delivery, chargedAt time.Time, cutoffHourIST int) timeline.Schedule {
	processing := ProcessingDate(delivery)
	return timeline.Schedule{
		PlacedAt:             isttime.ToIST(chargedAt),
		ProcessingAt:         isttime.AtHourIST(processing, cutoffHourIST),
		DeliveryDay:          isttime.AddDays(processing, 1),
		ExpectedDeliveryDate: Day(delivery),
	}
}
