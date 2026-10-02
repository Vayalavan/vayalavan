package timeline

import (
	"testing"
	"time"

	"github.com/vayal-mikrogreenz/vm-go-common/isttime"
)

// cutoffHour mirrors the ORDER_CUTOFF_HOUR_IST=16 default.
const cutoffHour = 16

func ist(y int, m time.Month, d, hh, mm, ss int) time.Time {
	return time.Date(y, m, d, hh, mm, ss, 0, isttime.Location())
}

func utc(y int, m time.Month, d, hh, mm, ss int) time.Time {
	return time.Date(y, m, d, hh, mm, ss, 0, time.UTC)
}

// TestComputeMatchesTheSpecTable is the required table from CLAUDE.md §6.1,
// asserted exactly. Every row of that table appears here verbatim.
//
//	| Placed at (IST)       | processing_at        | delivery_day | expected_delivery |
//	| 2026-06-15 09:00      | 2026-06-15 16:00     | 2026-06-16   | 2026-06-17        |
//	| 2026-06-15 15:59:59   | 2026-06-15 16:00     | 2026-06-16   | 2026-06-17        |
//	| 2026-06-15 16:00:00   | 2026-06-16 16:00     | 2026-06-17   | 2026-06-18        |
//	| 2026-06-15 23:45      | 2026-06-16 16:00     | 2026-06-17   | 2026-06-18        |
//	| 2026-06-15 10:30 UTC  | 2026-06-16 16:00 IST | 2026-06-17   | 2026-06-18        |
func TestComputeMatchesTheSpecTable(t *testing.T) {
	tests := []struct {
		name              string
		placedAt          time.Time
		wantProcessingAt  time.Time
		wantDeliveryDay   time.Time
		wantExpectedDeliv time.Time
	}{
		{
			name:              "09:00 — well before the cutoff",
			placedAt:          ist(2026, time.June, 15, 9, 0, 0),
			wantProcessingAt:  ist(2026, time.June, 15, 16, 0, 0),
			wantDeliveryDay:   ist(2026, time.June, 16, 0, 0, 0),
			wantExpectedDeliv: ist(2026, time.June, 17, 0, 0, 0),
		},
		{
			name:              "15:59:59 — one second inside the cutoff",
			placedAt:          ist(2026, time.June, 15, 15, 59, 59),
			wantProcessingAt:  ist(2026, time.June, 15, 16, 0, 0),
			wantDeliveryDay:   ist(2026, time.June, 16, 0, 0, 0),
			wantExpectedDeliv: ist(2026, time.June, 17, 0, 0, 0),
		},
		{
			// The boundary. "Before" is strict, so exactly 16:00:00 rolls over.
			name:              "16:00:00 — exactly at the cutoff rolls to tomorrow",
			placedAt:          ist(2026, time.June, 15, 16, 0, 0),
			wantProcessingAt:  ist(2026, time.June, 16, 16, 0, 0),
			wantDeliveryDay:   ist(2026, time.June, 17, 0, 0, 0),
			wantExpectedDeliv: ist(2026, time.June, 18, 0, 0, 0),
		},
		{
			name:              "23:45 — late evening",
			placedAt:          ist(2026, time.June, 15, 23, 45, 0),
			wantProcessingAt:  ist(2026, time.June, 16, 16, 0, 0),
			wantDeliveryDay:   ist(2026, time.June, 17, 0, 0, 0),
			wantExpectedDeliv: ist(2026, time.June, 18, 0, 0, 0),
		},
		{
			// 10:30 UTC IS 16:00 IST. A naive implementation comparing the UTC
			// hour against 16 would call this "before the cutoff" and promise
			// delivery a full day early.
			name:              "10:30 UTC — converted, not compared naively",
			placedAt:          utc(2026, time.June, 15, 10, 30, 0),
			wantProcessingAt:  ist(2026, time.June, 16, 16, 0, 0),
			wantDeliveryDay:   ist(2026, time.June, 17, 0, 0, 0),
			wantExpectedDeliv: ist(2026, time.June, 18, 0, 0, 0),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Compute(tc.placedAt, cutoffHour)

			assertTime(t, "ProcessingAt", got.ProcessingAt, tc.wantProcessingAt)
			assertTime(t, "DeliveryDay", got.DeliveryDay, tc.wantDeliveryDay)
			assertTime(t, "ExpectedDeliveryDate", got.ExpectedDeliveryDate, tc.wantExpectedDeliv)
		})
	}
}

func assertTime(t *testing.T, field string, got, want time.Time) {
	t.Helper()
	if !got.Equal(want) {
		t.Errorf("%s = %s, want %s",
			field,
			got.In(isttime.Location()).Format(time.RFC3339),
			want.In(isttime.Location()).Format(time.RFC3339))
	}
}

// TestComputeIsTimezoneIndependent — the same instant in three zones must
// produce byte-identical schedules. IST has no DST, so this also pins that no
// offset arithmetic sneaks in.
func TestComputeIsTimezoneIndependent(t *testing.T) {
	instant := ist(2026, time.June, 15, 9, 0, 0)
	pacific := time.FixedZone("PDT", -7*60*60)
	kathmandu := time.FixedZone("NPT", 5*60*60+45*60) // deliberately odd offset

	want := Compute(instant, cutoffHour)

	for name, variant := range map[string]time.Time{
		"IST":       instant,
		"UTC":       instant.In(time.UTC),
		"Pacific":   instant.In(pacific),
		"Kathmandu": instant.In(kathmandu),
	} {
		t.Run(name, func(t *testing.T) {
			got := Compute(variant, cutoffHour)
			assertTime(t, "ProcessingAt", got.ProcessingAt, want.ProcessingAt)
			assertTime(t, "DeliveryDay", got.DeliveryDay, want.DeliveryDay)
			assertTime(t, "ExpectedDeliveryDate", got.ExpectedDeliveryDate, want.ExpectedDeliveryDate)
		})
	}
}

// TestComputeIsDSTFree checks the schedule across dates where northern and
// southern hemisphere DST transitions happen. IST observes none, so a
// midday-placed order must always process at exactly 16:00 IST.
func TestComputeIsDSTFree(t *testing.T) {
	// Dates chosen to straddle US, EU and AU DST switchovers.
	dstDates := []struct {
		name string
		date time.Time
	}{
		{"US spring forward", ist(2026, time.March, 8, 9, 0, 0)},
		{"EU spring forward", ist(2026, time.March, 29, 9, 0, 0)},
		{"US fall back", ist(2026, time.November, 1, 9, 0, 0)},
		{"EU fall back", ist(2026, time.October, 25, 9, 0, 0)},
		{"AU spring forward", ist(2026, time.October, 4, 9, 0, 0)},
		{"midwinter", ist(2026, time.January, 15, 9, 0, 0)},
		{"midsummer", ist(2026, time.July, 15, 9, 0, 0)},
	}

	for _, tc := range dstDates {
		t.Run(tc.name, func(t *testing.T) {
			got := Compute(tc.date, cutoffHour)

			// Same calendar day, 16:00 sharp, every time.
			hour, minute, second := got.ProcessingAt.Clock()
			if hour != cutoffHour || minute != 0 || second != 0 {
				t.Errorf("ProcessingAt clock = %02d:%02d:%02d, want %02d:00:00",
					hour, minute, second, cutoffHour)
			}
			if !isttime.SameDayIST(got.ProcessingAt, tc.date) {
				t.Errorf("ProcessingAt fell on a different day than placement")
			}
			// Delivery is always exactly one day after processing, two before
			// expected — no offset ever creeps in.
			if got.DeliveryDay.Sub(isttime.StartOfDayIST(got.ProcessingAt)) != 24*time.Hour {
				t.Errorf("DeliveryDay is not exactly one day after the processing day")
			}
			if got.ExpectedDeliveryDate.Sub(got.DeliveryDay) != 24*time.Hour {
				t.Errorf("ExpectedDeliveryDate is not exactly one day after DeliveryDay")
			}
		})
	}
}

// TestComputeCrossesBoundaries covers the rollovers off-by-one date arithmetic
// gets wrong, including a leap day.
func TestComputeCrossesBoundaries(t *testing.T) {
	tests := []struct {
		name              string
		placedAt          time.Time
		wantProcessingAt  time.Time
		wantDeliveryDay   time.Time
		wantExpectedDeliv time.Time
	}{
		{
			name:              "month end",
			placedAt:          ist(2026, time.June, 30, 17, 0, 0),
			wantProcessingAt:  ist(2026, time.July, 1, 16, 0, 0),
			wantDeliveryDay:   ist(2026, time.July, 2, 0, 0, 0),
			wantExpectedDeliv: ist(2026, time.July, 3, 0, 0, 0),
		},
		{
			name:              "year end",
			placedAt:          ist(2026, time.December, 31, 20, 0, 0),
			wantProcessingAt:  ist(2027, time.January, 1, 16, 0, 0),
			wantDeliveryDay:   ist(2027, time.January, 2, 0, 0, 0),
			wantExpectedDeliv: ist(2027, time.January, 3, 0, 0, 0),
		},
		{
			// 2028 is a leap year: 29 February exists and must be used.
			name:              "into a leap day",
			placedAt:          ist(2028, time.February, 28, 18, 0, 0),
			wantProcessingAt:  ist(2028, time.February, 29, 16, 0, 0),
			wantDeliveryDay:   ist(2028, time.March, 1, 0, 0, 0),
			wantExpectedDeliv: ist(2028, time.March, 2, 0, 0, 0),
		},
		{
			// Placed ON the leap day.
			name:              "on the leap day",
			placedAt:          ist(2028, time.February, 29, 9, 0, 0),
			wantProcessingAt:  ist(2028, time.February, 29, 16, 0, 0),
			wantDeliveryDay:   ist(2028, time.March, 1, 0, 0, 0),
			wantExpectedDeliv: ist(2028, time.March, 2, 0, 0, 0),
		},
		{
			// 2027 is NOT a leap year: 28 Feb + 1 is 1 March.
			name:              "non-leap year skips 29 February",
			placedAt:          ist(2027, time.February, 28, 18, 0, 0),
			wantProcessingAt:  ist(2027, time.March, 1, 16, 0, 0),
			wantDeliveryDay:   ist(2027, time.March, 2, 0, 0, 0),
			wantExpectedDeliv: ist(2027, time.March, 3, 0, 0, 0),
		},
		{
			name:              "midnight is before the cutoff",
			placedAt:          ist(2026, time.June, 15, 0, 0, 0),
			wantProcessingAt:  ist(2026, time.June, 15, 16, 0, 0),
			wantDeliveryDay:   ist(2026, time.June, 16, 0, 0, 0),
			wantExpectedDeliv: ist(2026, time.June, 17, 0, 0, 0),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Compute(tc.placedAt, cutoffHour)
			assertTime(t, "ProcessingAt", got.ProcessingAt, tc.wantProcessingAt)
			assertTime(t, "DeliveryDay", got.DeliveryDay, tc.wantDeliveryDay)
			assertTime(t, "ExpectedDeliveryDate", got.ExpectedDeliveryDate, tc.wantExpectedDeliv)
		})
	}
}

// TestMilestonesAreDerivedAtReadTime — CLAUDE.md §6.1 requires completion to
// come from timestamps, not a cron-set flag, so a stalled worker never shows a
// customer a stale timeline.
func TestMilestonesAreDerivedAtReadTime(t *testing.T) {
	// Placed 09:00 on the 15th -> processed 16:00 the 15th, delivery the 16th.
	schedule := Compute(ist(2026, time.June, 15, 9, 0, 0), cutoffHour)

	tests := []struct {
		name              string
		now               time.Time
		wantProcessed     bool
		wantDeliveryStart bool
	}{
		{"just after placement", ist(2026, time.June, 15, 9, 1, 0), false, false},
		{"one second before processing", ist(2026, time.June, 15, 15, 59, 59), false, false},
		{"exactly at processing", ist(2026, time.June, 15, 16, 0, 0), true, false},
		{"evening of the processing day", ist(2026, time.June, 15, 22, 0, 0), true, false},
		{"exactly at delivery midnight", ist(2026, time.June, 16, 0, 0, 0), true, true},
		{"during the delivery day", ist(2026, time.June, 16, 12, 0, 0), true, true},
		{"long after", ist(2026, time.July, 1, 0, 0, 0), true, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			milestones := schedule.Milestones(tc.now)

			if len(milestones) != 3 {
				t.Fatalf("got %d milestones, want exactly 3", len(milestones))
			}
			// "Order received" is complete the moment the order exists.
			if !milestones[0].Completed {
				t.Error("'Order received' is not complete")
			}
			if milestones[1].Completed != tc.wantProcessed {
				t.Errorf("'Order processed' completed = %v, want %v",
					milestones[1].Completed, tc.wantProcessed)
			}
			if milestones[2].Completed != tc.wantDeliveryStart {
				t.Errorf("'Delivery day' completed = %v, want %v",
					milestones[2].Completed, tc.wantDeliveryStart)
			}
		})
	}
}

func TestMilestoneNames(t *testing.T) {
	schedule := Compute(ist(2026, time.June, 15, 9, 0, 0), cutoffHour)
	milestones := schedule.Milestones(ist(2026, time.June, 15, 9, 1, 0))

	want := []string{MilestoneReceived, MilestoneProcessed, MilestoneDelivery}
	for i, name := range want {
		if milestones[i].Name != name {
			t.Errorf("milestone %d = %q, want %q", i, milestones[i].Name, name)
		}
	}
}

func TestExpectedDeliveryText(t *testing.T) {
	schedule := Compute(ist(2026, time.June, 15, 9, 0, 0), cutoffHour)
	// CLAUDE.md §6.1 specifies DD MMM YYYY.
	if got, want := schedule.ExpectedDeliveryText(), "Expected delivery: 17 Jun 2026"; got != want {
		t.Errorf("ExpectedDeliveryText() = %q, want %q", got, want)
	}
}

func TestComputePanicsOnBadCutoff(t *testing.T) {
	for _, hour := range []int{-1, 24, 99} {
		t.Run("out of range", func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("Compute with cutoff %d did not panic", hour)
				}
			}()
			Compute(time.Now(), hour)
		})
	}
}

// TestMilestonesHonourEarlyProcessing — an admin can pack an order before its
// 4pm cutoff, and when they do the customer must see it. A timeline still
// saying "not yet processed" about a parcel that is packed is simply wrong.
func TestMilestonesHonourEarlyProcessing(t *testing.T) {
	// Placed 9am, so the cutoff is 4pm the SAME day and has not passed yet.
	placed := ist(2026, time.June, 15, 9, 0, 0)
	schedule := Compute(placed, 16)
	now := ist(2026, time.June, 15, 10, 0, 0) // an hour later, well before 4pm

	// Time alone says not processed.
	byTime := schedule.MilestonesForStatus(now, "paid")
	if byTime[1].Completed {
		t.Error("'Order processed' completed at 10am for a paid order — the " +
			"cutoff is 4pm and nothing has happened yet")
	}

	// An admin processing early completes it.
	early := schedule.MilestonesForStatus(now, "processed")
	if !early[1].Completed {
		t.Error("'Order processed' still incomplete after an admin processed it")
	}

	// Dispatched implies processed.
	dispatched := schedule.MilestonesForStatus(now, "dispatched")
	if !dispatched[1].Completed {
		t.Error("'Order processed' incomplete for a dispatched order")
	}

	// The delivery day is NOT reached early just because it was packed early —
	// the parcel still has to travel.
	if early[2].Completed {
		t.Error("'Delivery day' completed early; only the calendar completes that")
	}
}

// TestTimeStillCompletesWithoutAStatus — status is an override, never a
// requirement. If it is missing or unknown, the timestamp rule still applies,
// so the display never depends on a job having run (CLAUDE.md §6.1).
func TestTimeStillCompletesWithoutAStatus(t *testing.T) {
	placed := ist(2026, time.June, 15, 9, 0, 0)
	schedule := Compute(placed, 16)
	afterCutoff := ist(2026, time.June, 15, 16, 30, 0)

	for _, status := range []string{"", "paid", "something-unknown"} {
		milestones := schedule.MilestonesForStatus(afterCutoff, status)
		if !milestones[1].Completed {
			t.Errorf("status %q: 'Order processed' incomplete after the cutoff passed",
				status)
		}
	}
}

// TestTerminalStatusesDoNotCompleteFulfilment — a cancelled order has not been
// processed, and must never render as though it were.
//
// Checked at three moments, and the LAST two are the ones that matter. The
// original test only looked before the cutoff, where the time-based rule is
// false anyway — so it passed while an expired order on a customer's screen
// showed "Order processed ✓" and "Delivery day ✓", which is what a customer
// who never paid was actually being told.
func TestTerminalStatusesDoNotCompleteFulfilment(t *testing.T) {
	placed := ist(2026, time.June, 15, 9, 0, 0)
	schedule := Compute(placed, 16)

	moments := []struct {
		name string
		now  time.Time
	}{
		{"before the cutoff", ist(2026, time.June, 15, 10, 0, 0)},
		{"after the cutoff", ist(2026, time.June, 15, 16, 30, 0)},
		{"after the delivery day", ist(2026, time.June, 17, 9, 0, 0)},
	}

	for _, status := range []string{"cancelled", "refunded", "expired", "payment_failed"} {
		for _, moment := range moments {
			t.Run(status+"/"+moment.name, func(t *testing.T) {
				milestones := schedule.MilestonesForStatus(moment.now, status)

				// No fulfilment timeline at all: a timeline is a promise about
				// what happens next, and a terminal order has none to make.
				// Both clients hide the block when the list is empty.
				if len(milestones) != 0 {
					t.Fatalf("status %q at %s: got %d milestones, want none: %+v",
						status, moment.name, len(milestones), milestones)
				}
			})
		}
	}
}

// TestLiveStatusesKeepTheirTimeline — the fix above must not take the timeline
// away from orders that are still going somewhere.
func TestLiveStatusesKeepTheirTimeline(t *testing.T) {
	placed := ist(2026, time.June, 15, 9, 0, 0)
	schedule := Compute(placed, 16)
	afterDelivery := ist(2026, time.June, 17, 9, 0, 0)

	for _, status := range []string{"", "pending_payment", "paid", "processed", "dispatched"} {
		milestones := schedule.MilestonesForStatus(afterDelivery, status)
		if len(milestones) != 3 {
			t.Fatalf("status %q: got %d milestones, want 3", status, len(milestones))
		}
		for i, milestone := range milestones {
			if !milestone.Completed {
				t.Errorf("status %q: milestone %d (%s) should be complete once the "+
					"delivery day has passed", status, i, milestone.Name)
			}
		}
	}
}
