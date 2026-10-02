// Package isttime owns every date and time decision that a customer sees.
//
// CLAUDE.md rule 2: timestamps are stored as TIMESTAMPTZ in UTC, but all
// business-day logic — the 4pm cutoff, delivery days — is computed in
// Asia/Kolkata. The server's local timezone is never consulted, because a
// deploy to a UTC host would otherwise shift every customer's cutoff by five
// and a half hours without a single test failing.
//
// Everything here is a pure function of its inputs. Nothing reads the wall
// clock except Now and Today, which keeps the timeline calculator testable
// against fixed instants.
package isttime

import (
	"fmt"
	"time"

	// Embeds the IANA tz database in the binary. Without this,
	// LoadLocation("Asia/Kolkata") fails on a scratch or alpine container
	// that ships no zoneinfo — at runtime, in production, not at build time.
	_ "time/tzdata"
)

// ZoneName is the IANA identifier for Indian Standard Time (UTC+05:30).
const ZoneName = "Asia/Kolkata"

// ist is resolved once at package init. A failure here is unrecoverable and
// affects every date in the system, so panicking beats limping on with UTC.
var ist = func() *time.Location {
	loc, err := time.LoadLocation(ZoneName)
	if err != nil {
		panic(fmt.Sprintf("isttime: cannot load %s: %v", ZoneName, err))
	}
	return loc
}()

// Location returns the Asia/Kolkata location.
func Location() *time.Location { return ist }

// Now returns the current instant expressed in IST.
func Now() time.Time { return time.Now().In(ist) }

// ToIST re-expresses an instant in IST. The instant itself is unchanged; only
// its wall-clock representation differs.
func ToIST(t time.Time) time.Time { return t.In(ist) }

// StartOfDayIST returns midnight at the start of t's IST calendar day.
//
// This is the conversion from "instant" to "business date". A 2026-06-15
// 22:00 UTC timestamp belongs to the 2026-06-16 business day in India, and
// this function is what encodes that.
func StartOfDayIST(t time.Time) time.Time {
	l := t.In(ist)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, ist)
}

// Today returns midnight at the start of the current IST day.
//
// This is the value to compare daily_availability.available_on against
// (CLAUDE.md §5.2): a product is purchasable only on its own IST day.
func Today() time.Time { return StartOfDayIST(time.Now()) }

// AddDays shifts t by a whole number of calendar days, preserving the IST
// wall-clock time of day.
//
// Calendar arithmetic, not t.Add(24*time.Hour): the two agree in IST (which
// has no DST) but diverge in any zone that does, and this helper should stay
// correct if it is ever reused.
func AddDays(t time.Time, days int) time.Time {
	l := t.In(ist)
	return time.Date(l.Year(), l.Month(), l.Day()+days,
		l.Hour(), l.Minute(), l.Second(), l.Nanosecond(), ist)
}

// AtHourIST returns t's IST calendar day at the given hour, on the hour.
func AtHourIST(t time.Time, hour int) time.Time {
	l := t.In(ist)
	return time.Date(l.Year(), l.Month(), l.Day(), hour, 0, 0, 0, ist)
}

// SameDayIST reports whether two instants fall on the same IST calendar day.
func SameDayIST(a, b time.Time) bool {
	return StartOfDayIST(a).Equal(StartOfDayIST(b))
}

// FormatDate renders an IST date as "15 Jun 2026", the format CLAUDE.md §6.1
// specifies for the customer-facing expected delivery line.
func FormatDate(t time.Time) string { return t.In(ist).Format("02 Jan 2006") }

// ISODate is the wire format for a calendar date: "2026-06-15".
const ISODate = "2006-01-02"

// FormatISODate renders an IST calendar date for an API response or a query
// parameter.
func FormatISODate(t time.Time) string { return t.In(ist).Format(ISODate) }

// ParseISODate reads a "2026-06-15" calendar date as midnight IST.
//
// A date on the wire has no zone, and the zone is exactly what decides which
// orders fall inside a supplier's "today". Parsed in the server's location it
// would silently shift the boundary by hours on any host not set to IST —
// which is every deploy target we have.
func ParseISODate(s string) (time.Time, error) {
	parsed, err := time.ParseInLocation(ISODate, s, ist)
	if err != nil {
		return time.Time{}, fmt.Errorf("isttime: %q is not a YYYY-MM-DD date", s)
	}
	return parsed, nil
}

// validateCutoff guards the one input that could silently corrupt every
// order timeline. An out-of-range cutoff is a misconfiguration, and failing
// loudly at the first order beats shipping wrong delivery dates.
func validateCutoff(cutoffHour int) {
	if cutoffHour < 0 || cutoffHour > 23 {
		panic(fmt.Sprintf("isttime: cutoff hour %d out of range 0-23", cutoffHour))
	}
}

// IsBeforeCutoff reports whether t falls strictly before the cutoff hour on
// its own IST day.
//
// Strict: an order placed at exactly 16:00:00.000 is NOT before the cutoff
// and rolls to the next processing day. See the test table in
// isttime_test.go, which pins 15:59:59 and 16:00:00 to opposite sides.
func IsBeforeCutoff(t time.Time, cutoffHour int) bool {
	validateCutoff(cutoffHour)
	l := ToIST(t)
	return l.Before(AtHourIST(l, cutoffHour))
}

// Timeline is the fully-resolved schedule for a single order.
//
// CLAUDE.md §6.1: these values are computed once at order placement and
// stored on the order row. They are never recomputed, so that a change to
// the cutoff configuration cannot retroactively move the delivery date a
// customer has already been shown.
type Timeline struct {
	// PlacedAt is the instant the order was paid for, in IST.
	PlacedAt time.Time
	// ProcessingAt is the cutoff moment the order is processed at.
	ProcessingAt time.Time
	// DeliveryDay is a date: midnight IST at the start of the delivery day.
	DeliveryDay time.Time
	// ExpectedDeliveryDate is a date: midnight IST, shown to the customer.
	ExpectedDeliveryDate time.Time
}

// ComputeTimeline resolves an order's schedule from the instant it was
// placed, implementing CLAUDE.md §6.1 exactly:
//
//	placed before cutoff -> processing_date = today
//	placed at or after   -> processing_date = tomorrow
//	processing_at        = processing_date at cutoffHour:00 IST
//	delivery_day         = processing_date + 1 day
//	expected_delivery    = delivery_day + 1 day
//
// placedAt may be in any timezone; it is converted to IST first.
func ComputeTimeline(placedAt time.Time, cutoffHour int) Timeline {
	validateCutoff(cutoffHour)

	local := ToIST(placedAt)

	processingDate := StartOfDayIST(local)
	if !IsBeforeCutoff(local, cutoffHour) {
		processingDate = AddDays(processingDate, 1)
	}

	deliveryDay := AddDays(processingDate, 1)

	return Timeline{
		PlacedAt:             local,
		ProcessingAt:         AtHourIST(processingDate, cutoffHour),
		DeliveryDay:          deliveryDay,
		ExpectedDeliveryDate: AddDays(deliveryDay, 1),
	}
}

// Processed reports whether the "Order processed" milestone is complete as of
// now.
//
// CLAUDE.md §6.1 requires milestone completion to be derived from timestamps
// at read time rather than depending on a cron job, so that a stalled worker
// degrades side effects (emails) without ever showing a customer a stale
// timeline.
func (t Timeline) Processed(now time.Time) bool {
	return !now.Before(t.ProcessingAt)
}

// DeliveryDayReached reports whether the "Delivery day" milestone is complete
// as of now, i.e. now is at or past 00:00 IST on the delivery day.
func (t Timeline) DeliveryDayReached(now time.Time) bool {
	return !now.Before(t.DeliveryDay)
}
