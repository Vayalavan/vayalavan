package api

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/vayal-mikrogreenz/vm-go-common/isttime"

	"github.com/vayal-mikrogreenz/vm-orders-api/internal/mail"

	"github.com/vayal-mikrogreenz/vm-orders-api/internal/store"
)

// TestFillDailyTrendCoversEveryDay — the query returns only days that traded;
// the chart must still show the quiet ones, in order, as zeros.
func TestFillDailyTrendCoversEveryDay(t *testing.T) {
	from, err := isttime.ParseISODate("2026-06-15")
	if err != nil {
		t.Fatalf("parse from: %v", err)
	}
	to, err := isttime.ParseISODate("2026-06-19")
	if err != nil {
		t.Fatalf("parse to: %v", err)
	}
	middle, err := isttime.ParseISODate("2026-06-17")
	if err != nil {
		t.Fatalf("parse middle: %v", err)
	}

	// One trading day in the middle of a five-day window; everything else was
	// quiet. Deliberately not the first or last day — an off-by-one in the
	// step would otherwise be invisible.
	got := fillDailyTrend(from, to, []store.AnalyticsDailyTrendRow{
		{Day: middle, Orders: 4, PaidOrders: 3, GmvPaise: 125000},
	})

	wantDates := []string{
		"2026-06-15", "2026-06-16", "2026-06-17", "2026-06-18", "2026-06-19",
	}
	if len(got) != len(wantDates) {
		t.Fatalf("got %d days, want %d", len(got), len(wantDates))
	}
	for i, want := range wantDates {
		if got[i].Date != want {
			t.Errorf("day %d: got %q, want %q", i, got[i].Date, want)
		}
	}

	traded := got[2]
	if traded.Orders != 4 || traded.PaidOrders != 3 || traded.GmvPaise != 125000 {
		t.Errorf("trading day: got %+v", traded)
	}
	if traded.Gmv != "₹1,250.00" {
		t.Errorf("trading day display: got %q, want ₹1,250.00", traded.Gmv)
	}

	for _, i := range []int{0, 1, 3, 4} {
		if got[i].Orders != 0 || got[i].GmvPaise != 0 {
			t.Errorf("quiet day %s should be zero, got %+v", got[i].Date, got[i])
		}
		if got[i].Gmv != "₹0.00" {
			t.Errorf("quiet day %s display: got %q, want ₹0.00", got[i].Date, got[i].Gmv)
		}
	}
}

// TestFillDailyTrendSingleDay — "today" is a one-day window, and must produce
// exactly one point rather than an empty chart.
func TestFillDailyTrendSingleDay(t *testing.T) {
	day, err := isttime.ParseISODate("2026-08-18")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	got := fillDailyTrend(day, day, nil)
	if len(got) != 1 {
		t.Fatalf("got %d days, want 1", len(got))
	}
	if got[0].Date != "2026-08-18" || got[0].Orders != 0 {
		t.Errorf("got %+v", got[0])
	}
}

// TestCentiPer — averages in hundredths, and the empty period does not divide
// by zero.
func TestCentiPer(t *testing.T) {
	tests := []struct {
		name   string
		total  int64
		orders int64
		want   int64
	}{
		{"no orders", 0, 0, 0},
		{"no orders but units", 7, 0, 0},
		{"exactly one pack each", 12, 12, 100},
		{"two and a half packs", 5, 2, 250},
		{"truncates rather than rounds", 10, 3, 333},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := centiPer(tc.total, tc.orders); got != tc.want {
				t.Errorf("centiPer(%d, %d) = %d, want %d",
					tc.total, tc.orders, got, tc.want)
			}
		})
	}
}

// TestFillSupplierTrendCoversEveryDay — the grower's chart has the same
// obligation as the admin's: a day nobody bought on is a zero, not a gap.
func TestFillSupplierTrendCoversEveryDay(t *testing.T) {
	from, err := isttime.ParseISODate("2026-06-15")
	if err != nil {
		t.Fatalf("parse from: %v", err)
	}
	to, err := isttime.ParseISODate("2026-06-17")
	if err != nil {
		t.Fatalf("parse to: %v", err)
	}

	got := fillSupplierTrend(from, to, []store.SupplierAnalyticsDailyRow{
		{Day: to, Orders: 2, Units: 5, Grams: 15000, AmountPaise: 87500},
	})

	if len(got) != 3 {
		t.Fatalf("got %d days, want 3", len(got))
	}
	if got[0].Date != "2026-06-15" || got[0].AmountPaise != 0 || got[0].Amount != "₹0.00" {
		t.Errorf("quiet day: got %+v", got[0])
	}
	if got[2].Units != 5 || got[2].Amount != "₹875.00" {
		t.Errorf("trading day: got %+v", got[2])
	}
}

// TestDailyReportWaitsForTheCutoff — nothing is emailed before the configured
// hour, and the hour is ORDER_CUTOFF_HOUR_IST rather than a second setting.
//
// The queries are nil on purpose: before the cutoff the job must return
// without touching the database at all, so a nil pointer here is the assertion.
func TestDailyReportWaitsForTheCutoff(t *testing.T) {
	sender := &mail.RecordingSender{}
	report := NewDailyReport(nil, sender, DailyReportConfig{
		Recipients: "ops@example.com",
		Subject:    "Vayal Daily Orders Report",
		CutoffHour: 16,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ist := isttime.Location()
	for _, at := range []time.Time{
		time.Date(2026, 8, 19, 0, 0, 0, 0, ist),
		time.Date(2026, 8, 19, 9, 30, 0, 0, ist),
		time.Date(2026, 8, 19, 15, 59, 59, 0, ist),
		// Same instant, expressed in UTC: the hour must be read in IST, not in
		// whatever zone the server happens to run in (CLAUDE.md rule 2).
		time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC),
	} {
		report.Run(context.Background(), at)
	}

	if len(sender.Sent) != 0 {
		t.Errorf("sent %d messages before the cutoff, want none", len(sender.Sent))
	}
}

// TestDailyReportDisabledWithoutRecipients — no MAIL_TO, no job. A developer's
// machine must not mail the team a courier sheet because the stack was left
// running.
func TestDailyReportDisabledWithoutRecipients(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	if NewDailyReport(nil, &mail.RecordingSender{}, DailyReportConfig{}, logger) != nil {
		t.Error("an empty recipient list should switch the job off")
	}
	if NewDailyReport(nil, &mail.RecordingSender{}, DailyReportConfig{
		Recipients: "   ",
	}, logger) != nil {
		t.Error("whitespace is not a recipient list")
	}
	// And a nil job is safe to call, because that is how the processor calls it.
	var absent *DailyReport
	absent.Run(context.Background(), time.Now())
}

// TestDispatchDue — the automatic dispatch waits for DISPATCH_HOUR_IST, and
// reads the hour in IST rather than in whatever zone the server runs in.
func TestDispatchDue(t *testing.T) {
	ist := isttime.Location()

	tests := []struct {
		name string
		now  time.Time
		hour int
		want bool
	}{
		// The default: dispatch the moment the delivery day begins.
		{"midnight, hour 0", time.Date(2026, 8, 19, 0, 0, 0, 0, ist), 0, true},
		{"afternoon, hour 0", time.Date(2026, 8, 19, 15, 0, 0, 0, ist), 0, true},

		// A 9am courier collection.
		{"before collection", time.Date(2026, 8, 19, 8, 59, 59, 0, ist), 9, false},
		{"on the hour", time.Date(2026, 8, 19, 9, 0, 0, 0, ist), 9, true},
		{"after collection", time.Date(2026, 8, 19, 23, 30, 0, 0, ist), 9, true},

		// 03:30 UTC is 09:00 IST. Read in UTC this would be false, and every
		// order would wait until 14:30 IST — the bug rule 2 exists to prevent.
		{"utc instant, in IST it is 9am", time.Date(2026, 8, 19, 3, 30, 0, 0, time.UTC), 9, true},
		// And 03:00 UTC is 08:30 IST, which is genuinely too early.
		{"utc instant, in IST it is 8:30am", time.Date(2026, 8, 19, 3, 0, 0, 0, time.UTC), 9, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := dispatchDue(tc.now, tc.hour); got != tc.want {
				t.Errorf("dispatchDue(%s, %d) = %v, want %v",
					tc.now.Format(time.RFC3339), tc.hour, got, tc.want)
			}
		})
	}
}
