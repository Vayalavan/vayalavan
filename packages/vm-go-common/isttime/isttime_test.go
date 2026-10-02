package isttime

import (
	"testing"
	"time"
)

// cutoffHour mirrors the ORDER_CUTOFF_HOUR_IST=16 default.
const cutoffHour = 16

// istTime builds an instant from IST wall-clock components.
func istTime(year int, month time.Month, day, hour, min, sec int) time.Time {
	return time.Date(year, month, day, hour, min, sec, 0, ist)
}

// utcTime builds an instant from UTC wall-clock components, used to prove
// that inputs are converted rather than compared naively.
func utcTime(year int, month time.Month, day, hour, min, sec int) time.Time {
	return time.Date(year, month, day, hour, min, sec, 0, time.UTC)
}

func TestLocationIsIST(t *testing.T) {
	// IST is UTC+05:30 year-round with no DST. If the embedded tzdata were
	// missing or wrong, every date in the system would silently shift.
	_, offset := istTime(2026, time.June, 15, 12, 0, 0).Zone()
	if want := int((5*time.Hour + 30*time.Minute).Seconds()); offset != want {
		t.Errorf("IST offset = %d seconds, want %d", offset, want)
	}

	// Same check in January, to catch a zone that wrongly applies DST.
	_, winter := istTime(2026, time.January, 15, 12, 0, 0).Zone()
	if winter != offset {
		t.Errorf("IST offset differs between summer (%d) and winter (%d); IST has no DST",
			offset, winter)
	}
}

// TestComputeTimeline is the required test table from CLAUDE.md §6.1,
// asserting exact values. Every row of that table appears here verbatim.
func TestComputeTimeline(t *testing.T) {
	tests := []struct {
		name              string
		placedAt          time.Time
		wantProcessingAt  time.Time
		wantDeliveryDay   time.Time
		wantExpectedDeliv time.Time
	}{
		{
			name:              "morning, well before cutoff",
			placedAt:          istTime(2026, time.June, 15, 9, 0, 0),
			wantProcessingAt:  istTime(2026, time.June, 15, 16, 0, 0),
			wantDeliveryDay:   istTime(2026, time.June, 16, 0, 0, 0),
			wantExpectedDeliv: istTime(2026, time.June, 17, 0, 0, 0),
		},
		{
			name:              "one second before cutoff still makes today",
			placedAt:          istTime(2026, time.June, 15, 15, 59, 59),
			wantProcessingAt:  istTime(2026, time.June, 15, 16, 0, 0),
			wantDeliveryDay:   istTime(2026, time.June, 16, 0, 0, 0),
			wantExpectedDeliv: istTime(2026, time.June, 17, 0, 0, 0),
		},
		{
			name:              "exactly at cutoff rolls to next day",
			placedAt:          istTime(2026, time.June, 15, 16, 0, 0),
			wantProcessingAt:  istTime(2026, time.June, 16, 16, 0, 0),
			wantDeliveryDay:   istTime(2026, time.June, 17, 0, 0, 0),
			wantExpectedDeliv: istTime(2026, time.June, 18, 0, 0, 0),
		},
		{
			name:              "late evening rolls to next day",
			placedAt:          istTime(2026, time.June, 15, 23, 45, 0),
			wantProcessingAt:  istTime(2026, time.June, 16, 16, 0, 0),
			wantDeliveryDay:   istTime(2026, time.June, 17, 0, 0, 0),
			wantExpectedDeliv: istTime(2026, time.June, 18, 0, 0, 0),
		},
		{
			// 10:30 UTC is exactly 16:00 IST. A naive implementation that
			// compared the UTC hour against 16 would place this before the
			// cutoff and produce delivery dates a full day early.
			name:              "UTC input is converted, not compared naively",
			placedAt:          utcTime(2026, time.June, 15, 10, 30, 0),
			wantProcessingAt:  istTime(2026, time.June, 16, 16, 0, 0),
			wantDeliveryDay:   istTime(2026, time.June, 17, 0, 0, 0),
			wantExpectedDeliv: istTime(2026, time.June, 18, 0, 0, 0),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ComputeTimeline(tc.placedAt, cutoffHour)

			if !got.ProcessingAt.Equal(tc.wantProcessingAt) {
				t.Errorf("ProcessingAt = %s, want %s",
					got.ProcessingAt.Format(time.RFC3339),
					tc.wantProcessingAt.Format(time.RFC3339))
			}
			if !got.DeliveryDay.Equal(tc.wantDeliveryDay) {
				t.Errorf("DeliveryDay = %s, want %s",
					got.DeliveryDay.Format(time.RFC3339),
					tc.wantDeliveryDay.Format(time.RFC3339))
			}
			if !got.ExpectedDeliveryDate.Equal(tc.wantExpectedDeliv) {
				t.Errorf("ExpectedDeliveryDate = %s, want %s",
					got.ExpectedDeliveryDate.Format(time.RFC3339),
					tc.wantExpectedDeliv.Format(time.RFC3339))
			}
		})
	}
}

// TestComputeTimelineIsTimezoneIndependent asserts that the same instant
// expressed in three different zones yields byte-identical timelines.
func TestComputeTimelineIsTimezoneIndependent(t *testing.T) {
	// 2026-06-15 09:00 IST == 03:30 UTC == 23:30 on 06-14 US/Pacific (-7).
	instant := istTime(2026, time.June, 15, 9, 0, 0)

	pacific := time.FixedZone("PDT", -7*60*60)
	variants := map[string]time.Time{
		"IST":     instant,
		"UTC":     instant.In(time.UTC),
		"Pacific": instant.In(pacific),
	}

	want := ComputeTimeline(instant, cutoffHour)
	for name, variant := range variants {
		t.Run(name, func(t *testing.T) {
			got := ComputeTimeline(variant, cutoffHour)
			if !got.ProcessingAt.Equal(want.ProcessingAt) ||
				!got.DeliveryDay.Equal(want.DeliveryDay) ||
				!got.ExpectedDeliveryDate.Equal(want.ExpectedDeliveryDate) {
				t.Errorf("timeline from %s differs from IST baseline:\n got %+v\nwant %+v",
					name, got, want)
			}
		})
	}
}

// TestComputeTimelineCrossesBoundaries covers the rollovers that off-by-one
// date arithmetic gets wrong: month end, year end, and a leap day.
func TestComputeTimelineCrossesBoundaries(t *testing.T) {
	tests := []struct {
		name              string
		placedAt          time.Time
		wantProcessingAt  time.Time
		wantDeliveryDay   time.Time
		wantExpectedDeliv time.Time
	}{
		{
			name:              "month end rolls into next month",
			placedAt:          istTime(2026, time.June, 30, 17, 0, 0),
			wantProcessingAt:  istTime(2026, time.July, 1, 16, 0, 0),
			wantDeliveryDay:   istTime(2026, time.July, 2, 0, 0, 0),
			wantExpectedDeliv: istTime(2026, time.July, 3, 0, 0, 0),
		},
		{
			name:              "year end rolls into next year",
			placedAt:          istTime(2026, time.December, 31, 20, 0, 0),
			wantProcessingAt:  istTime(2027, time.January, 1, 16, 0, 0),
			wantDeliveryDay:   istTime(2027, time.January, 2, 0, 0, 0),
			wantExpectedDeliv: istTime(2027, time.January, 3, 0, 0, 0),
		},
		{
			name:              "leap day is a real day",
			placedAt:          istTime(2028, time.February, 28, 18, 0, 0),
			wantProcessingAt:  istTime(2028, time.February, 29, 16, 0, 0),
			wantDeliveryDay:   istTime(2028, time.March, 1, 0, 0, 0),
			wantExpectedDeliv: istTime(2028, time.March, 2, 0, 0, 0),
		},
		{
			name:              "midnight is before the cutoff",
			placedAt:          istTime(2026, time.June, 15, 0, 0, 0),
			wantProcessingAt:  istTime(2026, time.June, 15, 16, 0, 0),
			wantDeliveryDay:   istTime(2026, time.June, 16, 0, 0, 0),
			wantExpectedDeliv: istTime(2026, time.June, 17, 0, 0, 0),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ComputeTimeline(tc.placedAt, cutoffHour)
			if !got.ProcessingAt.Equal(tc.wantProcessingAt) {
				t.Errorf("ProcessingAt = %s, want %s",
					got.ProcessingAt.Format(time.RFC3339),
					tc.wantProcessingAt.Format(time.RFC3339))
			}
			if !got.DeliveryDay.Equal(tc.wantDeliveryDay) {
				t.Errorf("DeliveryDay = %s, want %s",
					got.DeliveryDay.Format(time.RFC3339),
					tc.wantDeliveryDay.Format(time.RFC3339))
			}
			if !got.ExpectedDeliveryDate.Equal(tc.wantExpectedDeliv) {
				t.Errorf("ExpectedDeliveryDate = %s, want %s",
					got.ExpectedDeliveryDate.Format(time.RFC3339),
					tc.wantExpectedDeliv.Format(time.RFC3339))
			}
		})
	}
}

func TestIsBeforeCutoff(t *testing.T) {
	tests := []struct {
		name string
		at   time.Time
		want bool
	}{
		{"midnight", istTime(2026, time.June, 15, 0, 0, 0), true},
		{"morning", istTime(2026, time.June, 15, 9, 0, 0), true},
		{"one second before", istTime(2026, time.June, 15, 15, 59, 59), true},
		{"exactly at cutoff", istTime(2026, time.June, 15, 16, 0, 0), false},
		{"one second after", istTime(2026, time.June, 15, 16, 0, 1), false},
		{"late evening", istTime(2026, time.June, 15, 23, 59, 59), false},
		// 10:29 UTC is 15:59 IST — before. 10:30 UTC is 16:00 IST — not.
		{"UTC just before cutoff", utcTime(2026, time.June, 15, 10, 29, 0), true},
		{"UTC exactly at cutoff", utcTime(2026, time.June, 15, 10, 30, 0), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsBeforeCutoff(tc.at, cutoffHour); got != tc.want {
				t.Errorf("IsBeforeCutoff(%s) = %v, want %v",
					tc.at.Format(time.RFC3339), got, tc.want)
			}
		})
	}
}

func TestValidateCutoffPanics(t *testing.T) {
	for _, hour := range []int{-1, 24, 100} {
		t.Run("out of range", func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("ComputeTimeline with cutoff %d did not panic", hour)
				}
			}()
			ComputeTimeline(Now(), hour)
		})
	}
}

func TestStartOfDayIST(t *testing.T) {
	tests := []struct {
		name string
		in   time.Time
		want time.Time
	}{
		{
			name: "midday IST",
			in:   istTime(2026, time.June, 15, 13, 45, 30),
			want: istTime(2026, time.June, 15, 0, 0, 0),
		},
		{
			name: "already midnight is unchanged",
			in:   istTime(2026, time.June, 15, 0, 0, 0),
			want: istTime(2026, time.June, 15, 0, 0, 0),
		},
		{
			// 22:00 UTC on the 15th is 03:30 IST on the 16th — the IST day
			// has already rolled over.
			name: "late UTC belongs to the next IST day",
			in:   utcTime(2026, time.June, 15, 22, 0, 0),
			want: istTime(2026, time.June, 16, 0, 0, 0),
		},
		{
			// 18:00 UTC on the 15th is 23:30 IST, still the 15th.
			name: "evening UTC is still the same IST day",
			in:   utcTime(2026, time.June, 15, 18, 0, 0),
			want: istTime(2026, time.June, 15, 0, 0, 0),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := StartOfDayIST(tc.in); !got.Equal(tc.want) {
				t.Errorf("StartOfDayIST(%s) = %s, want %s",
					tc.in.Format(time.RFC3339),
					got.Format(time.RFC3339),
					tc.want.Format(time.RFC3339))
			}
		})
	}
}

func TestAddDays(t *testing.T) {
	tests := []struct {
		name string
		in   time.Time
		days int
		want time.Time
	}{
		{"forward one", istTime(2026, time.June, 15, 10, 0, 0), 1, istTime(2026, time.June, 16, 10, 0, 0)},
		{"zero is identity", istTime(2026, time.June, 15, 10, 0, 0), 0, istTime(2026, time.June, 15, 10, 0, 0)},
		{"backward one", istTime(2026, time.June, 15, 10, 0, 0), -1, istTime(2026, time.June, 14, 10, 0, 0)},
		{"across month end", istTime(2026, time.June, 30, 10, 0, 0), 1, istTime(2026, time.July, 1, 10, 0, 0)},
		{"across year end", istTime(2026, time.December, 31, 10, 0, 0), 1, istTime(2027, time.January, 1, 10, 0, 0)},
		{"into a leap day", istTime(2028, time.February, 28, 10, 0, 0), 1, istTime(2028, time.February, 29, 10, 0, 0)},
		{"non-leap year skips Feb 29", istTime(2026, time.February, 28, 10, 0, 0), 1, istTime(2026, time.March, 1, 10, 0, 0)},
		{"preserves time of day", istTime(2026, time.June, 15, 23, 59, 59), 1, istTime(2026, time.June, 16, 23, 59, 59)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := AddDays(tc.in, tc.days); !got.Equal(tc.want) {
				t.Errorf("AddDays(%s, %d) = %s, want %s",
					tc.in.Format(time.RFC3339), tc.days,
					got.Format(time.RFC3339), tc.want.Format(time.RFC3339))
			}
		})
	}
}

func TestSameDayIST(t *testing.T) {
	tests := []struct {
		name string
		a, b time.Time
		want bool
	}{
		{
			name: "same day different hours",
			a:    istTime(2026, time.June, 15, 0, 0, 0),
			b:    istTime(2026, time.June, 15, 23, 59, 59),
			want: true,
		},
		{
			name: "one second apart across midnight",
			a:    istTime(2026, time.June, 15, 23, 59, 59),
			b:    istTime(2026, time.June, 16, 0, 0, 0),
			want: false,
		},
		{
			// 20:00 UTC on the 14th is 01:30 IST on the 15th.
			name: "UTC instant on the same IST day",
			a:    istTime(2026, time.June, 15, 9, 0, 0),
			b:    utcTime(2026, time.June, 14, 20, 0, 0),
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := SameDayIST(tc.a, tc.b); got != tc.want {
				t.Errorf("SameDayIST(%s, %s) = %v, want %v",
					tc.a.Format(time.RFC3339), tc.b.Format(time.RFC3339),
					got, tc.want)
			}
		})
	}
}

// TestTimelineMilestones covers the read-time milestone derivation that
// CLAUDE.md §6.1 requires instead of a cron-driven flag.
func TestTimelineMilestones(t *testing.T) {
	// Placed 09:00 on the 15th -> processed 16:00 on the 15th,
	// delivery day 16th.
	tl := ComputeTimeline(istTime(2026, time.June, 15, 9, 0, 0), cutoffHour)

	tests := []struct {
		name              string
		now               time.Time
		wantProcessed     bool
		wantDeliveryReach bool
	}{
		{"just after placement", istTime(2026, time.June, 15, 9, 1, 0), false, false},
		{"one second before processing", istTime(2026, time.June, 15, 15, 59, 59), false, false},
		{"exactly at processing", istTime(2026, time.June, 15, 16, 0, 0), true, false},
		{"evening of processing day", istTime(2026, time.June, 15, 22, 0, 0), true, false},
		{"exactly at delivery midnight", istTime(2026, time.June, 16, 0, 0, 0), true, true},
		{"during delivery day", istTime(2026, time.June, 16, 12, 0, 0), true, true},
		{"long after", istTime(2026, time.July, 1, 0, 0, 0), true, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tl.Processed(tc.now); got != tc.wantProcessed {
				t.Errorf("Processed(%s) = %v, want %v",
					tc.now.Format(time.RFC3339), got, tc.wantProcessed)
			}
			if got := tl.DeliveryDayReached(tc.now); got != tc.wantDeliveryReach {
				t.Errorf("DeliveryDayReached(%s) = %v, want %v",
					tc.now.Format(time.RFC3339), got, tc.wantDeliveryReach)
			}
		})
	}
}

func TestFormatDate(t *testing.T) {
	tests := []struct {
		name string
		in   time.Time
		want string
	}{
		{"june", istTime(2026, time.June, 17, 0, 0, 0), "17 Jun 2026"},
		{"single digit day is zero padded", istTime(2026, time.July, 3, 0, 0, 0), "03 Jul 2026"},
		{"january", istTime(2027, time.January, 2, 0, 0, 0), "02 Jan 2027"},
		// Formatted in IST, not the input's own zone: 20:00 UTC on the 16th
		// is already the 17th in India.
		{"UTC input renders as its IST date", utcTime(2026, time.June, 16, 20, 0, 0), "17 Jun 2026"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatDate(tc.in); got != tc.want {
				t.Errorf("FormatDate(%s) = %q, want %q",
					tc.in.Format(time.RFC3339), got, tc.want)
			}
		})
	}
}

func TestNowAndTodayUseIST(t *testing.T) {
	if got := Now().Location().String(); got != ZoneName {
		t.Errorf("Now() location = %q, want %q", got, ZoneName)
	}
	today := Today()
	if got := today.Location().String(); got != ZoneName {
		t.Errorf("Today() location = %q, want %q", got, ZoneName)
	}
	if h, m, s := today.Clock(); h != 0 || m != 0 || s != 0 {
		t.Errorf("Today() clock = %02d:%02d:%02d, want 00:00:00", h, m, s)
	}
	if !SameDayIST(today, Now()) {
		t.Error("Today() and Now() disagree on the current IST day")
	}
}
