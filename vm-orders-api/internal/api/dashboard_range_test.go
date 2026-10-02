package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vayal-mikrogreenz/vm-go-common/isttime"
)

func rangeFor(t *testing.T, query string) (dashboardRange, error) {
	t.Helper()
	return resolveDashboardRange(httptest.NewRequest(http.MethodGet, "/admin/dashboard"+query, nil))
}

// TestNamedRangesEndToday — every preset counts backwards INCLUDING today.
// "Past 7 days" ending yesterday would hide the day an operator most wants.
func TestNamedRangesEndToday(t *testing.T) {
	today := isttime.Today()

	tests := []struct {
		query     string
		wantDays  int
		wantLabel string
	}{
		{"", 1, "Today"},
		{"?range=today", 1, "Today"},
		{"?range=2d", 2, "Past 2 days"},
		{"?range=7d", 7, "Past 7 days"},
		{"?range=1m", 30, "Past month"},
	}

	for _, tc := range tests {
		t.Run(tc.wantLabel, func(t *testing.T) {
			got, err := rangeFor(t, tc.query)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !got.To.Equal(today) {
				t.Errorf("To = %v, want today %v", got.To, today)
			}
			days := int(got.To.Sub(got.From).Hours()/24) + 1
			if days != tc.wantDays {
				t.Errorf("span = %d days, want %d", days, tc.wantDays)
			}
			if got.Label != tc.wantLabel {
				t.Errorf("label = %q, want %q", got.Label, tc.wantLabel)
			}
		})
	}
}

func TestCustomRange(t *testing.T) {
	got, err := rangeFor(t, "?range=custom&from=2026-06-01&to=2026-06-15")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.From.Format("2006-01-02") != "2026-06-01" {
		t.Errorf("from = %v", got.From)
	}
	if got.To.Format("2006-01-02") != "2026-06-15" {
		t.Errorf("to = %v", got.To)
	}
	// Parsed in IST, not UTC — parsing as UTC midnight would shift the
	// boundary 5.5 hours and move orders between days.
	if _, offset := got.From.Zone(); offset != 19800 {
		t.Errorf("from parsed at offset %d, want +05:30 (19800)", offset)
	}
}

// TestCustomRangeIsClampedToToday — a future end date is not worth refusing
// (it simply contains no orders), but it must not make the label lie.
func TestCustomRangeIsClampedToToday(t *testing.T) {
	future := isttime.AddDays(isttime.Today(), 30).Format("2006-01-02")
	got, err := rangeFor(t, "?range=custom&from=2026-01-01&to="+future)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.To.After(isttime.Today()) {
		t.Errorf("To = %v, want clamped to today", got.To)
	}
}

func TestInvalidRangesAreRefused(t *testing.T) {
	tests := []struct {
		name  string
		query string
	}{
		{"unknown preset", "?range=last-fortnight"},
		{"custom with no dates", "?range=custom"},
		{"custom missing the end", "?range=custom&from=2026-06-01"},
		{"custom missing the start", "?range=custom&to=2026-06-01"},
		{"malformed date", "?range=custom&from=01-06-2026&to=2026-06-15"},
		{"end before start", "?range=custom&from=2026-06-15&to=2026-06-01"},
		// The typo that turns one screen into a scan of every order ever.
		{"absurdly long range", "?range=custom&from=1990-01-01&to=2026-06-15"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := rangeFor(t, tc.query); err == nil {
				t.Error("expected the range to be refused, but it was accepted")
			}
		})
	}
}

// TestRangeIgnoresClientSuppliedToday — a client may ask for a PERIOD, never
// assert what today is (CLAUDE.md rule 2). A skewed or crafted clock must not
// be able to shift the reporting window.
func TestRangeIgnoresClientSuppliedToday(t *testing.T) {
	got, err := rangeFor(t, "?range=7d&today=1999-01-01&date=1999-01-01")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.To.Equal(isttime.Today()) {
		t.Errorf("To = %v, want server's today %v", got.To, isttime.Today())
	}
}

func TestSingleDayRangeIsInclusive(t *testing.T) {
	got, err := rangeFor(t, "?range=custom&from=2026-06-10&to=2026-06-10")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.From.Equal(got.To) {
		t.Errorf("from %v != to %v for a single day", got.From, got.To)
	}
	// BETWEEN is inclusive at both ends, so one day is from == to, not a
	// zero-width window that would report nothing.
	if got.To.Sub(got.From) != 0 {
		t.Errorf("span = %v, want 0", got.To.Sub(got.From))
	}
}

var _ = time.Now

func salesRangeFor(t *testing.T, query string) (dashboardRange, error) {
	t.Helper()
	return resolveSalesRange(httptest.NewRequest(http.MethodGet, "/supplier/sales"+query, nil))
}

// The supplier screen reconciles lifetime payouts against a bank statement, so
// an unfiltered request must keep meaning "everything" — the day the filter
// shipped must not change what "gross" means for anyone already using it.
func TestSalesRangeDefaultsToAllTime(t *testing.T) {
	got, err := salesRangeFor(t, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.All {
		t.Fatalf("default range = %+v, want all time", got)
	}
	from, to := got.Instants()
	if from != nil || to != nil {
		t.Errorf("all-time bounds = (%v, %v), want both nil so SQL applies no filter", from, to)
	}
}

// All time is the supplier screen's option alone. The admin dashboard's
// figures are "what is happening now" and have no unbounded reading.
func TestAllTimeIsRefusedOnAdminScreens(t *testing.T) {
	if _, err := rangeFor(t, "?range=all"); err == nil {
		t.Error("expected range=all to be refused for the admin dashboard")
	}
}

func TestSalesRangePresets(t *testing.T) {
	today := isttime.Today()

	tests := []struct {
		query     string
		wantDays  int
		wantLabel string
	}{
		{"?range=today", 1, "Today"},
		{"?range=2d", 2, "Past 2 days"},
		{"?range=3d", 3, "Past 3 days"},
		{"?range=7d", 7, "Past 7 days"},
		{"?range=1m", 30, "Past month"},
	}

	for _, tc := range tests {
		t.Run(tc.wantLabel, func(t *testing.T) {
			got, err := salesRangeFor(t, tc.query)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.All {
				t.Fatal("a named preset must not resolve to all time")
			}
			if !got.To.Equal(today) {
				t.Errorf("To = %v, want today %v", got.To, today)
			}
			if days := int(got.To.Sub(got.From).Hours()/24) + 1; days != tc.wantDays {
				t.Errorf("span = %d days, want %d", days, tc.wantDays)
			}
			if got.Label != tc.wantLabel {
				t.Errorf("label = %q, want %q", got.Label, tc.wantLabel)
			}
		})
	}
}

// The SQL bound is half-open, and the upper end is the point every off-by-one
// hides in: an order placed at 23:30 IST on the last day of the range belongs
// inside it.
func TestInstantsCoverTheWholeLastDay(t *testing.T) {
	got, err := salesRangeFor(t, "?range=custom&from=2026-06-10&to=2026-06-10")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	from, to := got.Instants()
	if from == nil || to == nil {
		t.Fatal("a custom range must have both bounds")
	}

	lateOnTheLastDay := time.Date(2026, 6, 10, 23, 30, 0, 0, isttime.Location())
	if lateOnTheLastDay.Before(*from) || !lateOnTheLastDay.Before(*to) {
		t.Errorf("23:30 IST on the last day fell outside [%v, %v)", *from, *to)
	}

	firstMoment := time.Date(2026, 6, 10, 0, 0, 0, 0, isttime.Location())
	if firstMoment.Before(*from) {
		t.Errorf("midnight IST on the first day fell before the lower bound %v", *from)
	}

	// And the moment the next day starts must NOT be included, or two adjacent
	// ranges would both claim the same orders.
	nextDay := time.Date(2026, 6, 11, 0, 0, 0, 0, isttime.Location())
	if nextDay.Before(*to) {
		t.Errorf("midnight IST on the following day fell inside the window")
	}
}

// TestQueueRangeAllowsAllTime — the admin ORDERS list may ask for everything.
//
// It is a queue an operator browses and exports, not a figure for a period.
// The dashboard tiles and the bulk actions must NOT gain the option: a tile
// showing all-time GMV is not what "today" means on that screen, and
// "dispatch all processed" with no period is every order ever.
func TestQueueRangeAllowsAllTime(t *testing.T) {
	queue, err := resolveQueueRange(
		httptest.NewRequest(http.MethodGet, "/admin/orders?range=all", nil))
	if err != nil {
		t.Fatalf("queue range: %v", err)
	}
	if !queue.All {
		t.Error("range=all did not resolve to an unbounded window")
	}
	// Unset on purpose — a caller reading these without checking All would
	// filter from the zero time.
	if !queue.From.IsZero() || !queue.To.IsZero() {
		t.Errorf("all-time should leave the dates unset, got %v to %v", queue.From, queue.To)
	}

	// Omitted, it still defaults to today: an API caller that says nothing
	// gets the same narrow answer it always did.
	def, err := resolveQueueRange(httptest.NewRequest(http.MethodGet, "/admin/orders", nil))
	if err != nil {
		t.Fatalf("default: %v", err)
	}
	if def.All || def.Key != "today" {
		t.Errorf("default = %+v, want today", def)
	}

	// And the screens that must not offer it still refuse.
	for _, resolve := range []func(*http.Request) (dashboardRange, error){
		resolveDashboardRange, resolveTrendRange,
	} {
		if _, err := resolve(
			httptest.NewRequest(http.MethodGet, "/x?range=all", nil)); err == nil {
			t.Error("a resolver that must refuse all-time accepted it")
		}
	}
}

// TestScopeLabelNamesTheBatch — what the audit row and the response call a
// bulk action.
//
// A selection is scoped by its ids and has no period; "all" names the period
// it covered, all time included, so an audit entry read months later says
// which orders were in the batch.
func TestScopeLabelNamesTheBatch(t *testing.T) {
	tests := []struct {
		all    bool
		period string
		want   string
	}{
		{true, "All time", "all eligible in All time"},
		{true, "Today", "all eligible in Today"},
		{false, "selection", "selected"},
	}

	for _, tc := range tests {
		if got := scopeLabel(tc.all, tc.period); got != tc.want {
			t.Errorf("scopeLabel(%v, %q) = %q, want %q", tc.all, tc.period, got, tc.want)
		}
	}
}
