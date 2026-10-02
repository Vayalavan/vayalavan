package recurrence

import (
	"testing"
	"time"

	"github.com/vayal-mikrogreenz/vm-go-common/isttime"
)

func date(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := isttime.ParseISODate(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return Day(d)
}

func iso(d time.Time) string { return isttime.FormatISODate(d) }

func istAt(t *testing.T, s string) time.Time {
	t.Helper()
	at, err := time.ParseInLocation("2006-01-02 15:04:05", s, isttime.Location())
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func TestUpcoming(t *testing.T) {
	tests := []struct {
		name string
		rule Rule
		from string
		n    int
		want []string
	}{
		{
			name: "once, on its date",
			rule: Rule{Frequency: Once, Start: date(t, "2026-10-05")},
			from: "2026-10-01", n: 3,
			want: []string{"2026-10-05"},
		},
		{
			name: "once, already past",
			rule: Rule{Frequency: Once, Start: date(t, "2026-10-05")},
			from: "2026-10-06", n: 3,
			want: []string{},
		},
		{
			name: "daily from start",
			rule: Rule{Frequency: Daily, Start: date(t, "2026-10-05")},
			from: "2026-10-01", n: 3,
			want: []string{"2026-10-05", "2026-10-06", "2026-10-07"},
		},
		{
			name: "daily stops at an inclusive end date",
			rule: Rule{Frequency: Daily, Start: date(t, "2026-10-05"), End: date(t, "2026-10-06")},
			from: "2026-10-05", n: 5,
			want: []string{"2026-10-05", "2026-10-06"},
		},
		{
			// 2026-10-05 is a Monday.
			name: "weekly on Monday and Thursday",
			rule: Rule{Frequency: Weekly, Weekdays: []int{1, 4}, Start: date(t, "2026-10-05")},
			from: "2026-10-05", n: 4,
			want: []string{"2026-10-05", "2026-10-08", "2026-10-12", "2026-10-15"},
		},
		{
			name: "weekly starting mid-week skips to the first matching day",
			rule: Rule{Frequency: Weekly, Weekdays: []int{0}, Start: date(t, "2026-10-06")},
			from: "2026-10-06", n: 2,
			want: []string{"2026-10-11", "2026-10-18"},
		},
		{
			name: "monthly on the 31st falls on the last day of shorter months",
			rule: Rule{Frequency: Monthly, DayOfMonth: 31, Start: date(t, "2027-01-01")},
			from: "2027-01-01", n: 4,
			want: []string{"2027-01-31", "2027-02-28", "2027-03-31", "2027-04-30"},
		},
		{
			name: "monthly in a leap February",
			rule: Rule{Frequency: Monthly, DayOfMonth: 30, Start: date(t, "2028-02-01")},
			from: "2028-02-01", n: 2,
			want: []string{"2028-02-29", "2028-03-30"},
		},
		{
			name: "monthly on the 15th, starting after it",
			rule: Rule{Frequency: Monthly, DayOfMonth: 15, Start: date(t, "2026-10-16")},
			from: "2026-10-16", n: 2,
			want: []string{"2026-11-15", "2026-12-15"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.rule.Upcoming(date(t, tt.from), tt.n)
			gotISO := make([]string, 0, len(got))
			for _, d := range got {
				gotISO = append(gotISO, iso(d))
			}
			if len(gotISO) != len(tt.want) {
				t.Fatalf("got %v, want %v", gotISO, tt.want)
			}
			for i := range gotISO {
				if gotISO[i] != tt.want[i] {
					t.Fatalf("got %v, want %v", gotISO, tt.want)
				}
			}
		})
	}
}

// A DATE read back through pgx is UTC midnight; the timeline package hands out
// IST midnight. Both must name the same day, or a stored next_delivery_date
// would be compared a day off.
func TestDayNormalisesUTCAndISTMidnight(t *testing.T) {
	utc := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	ist := time.Date(2026, 10, 5, 0, 0, 0, 0, isttime.Location())
	if !Day(utc).Equal(Day(ist)) {
		t.Fatalf("Day(utc)=%v, Day(ist)=%v", Day(utc), Day(ist))
	}
	if iso(Day(utc)) != "2026-10-05" {
		t.Fatalf("got %s", iso(Day(utc)))
	}
}

func TestChargeAtAndLocked(t *testing.T) {
	const cutoff = 16
	lead := 30 * time.Minute
	delivery := date(t, "2026-10-10")

	// Processing day is two days before the expected delivery, charged at
	// 15:30 IST — before that day's 16:00 cutoff.
	want := istAt(t, "2026-10-08 15:30:00")
	if got := ChargeAt(delivery, cutoff, lead); !got.Equal(want) {
		t.Fatalf("ChargeAt = %v, want %v", got, want)
	}

	tests := []struct {
		now    string
		locked bool
	}{
		{"2026-10-08 15:29:59", false},
		{"2026-10-08 15:30:00", true},
		{"2026-10-09 09:00:00", true},
		{"2026-10-07 23:00:00", false},
	}
	for _, tt := range tests {
		if got := Locked(delivery, istAt(t, tt.now), cutoff, lead); got != tt.locked {
			t.Errorf("Locked at %s = %v, want %v", tt.now, got, tt.locked)
		}
	}
}

func TestEarliestStart(t *testing.T) {
	const cutoff = 16
	tests := []struct {
		now  string
		want string
	}{
		// Before the cutoff an order placed now arrives on the 17th, so a
		// schedule may start on the 18th.
		{"2026-06-15 09:00:00", "2026-06-18"},
		{"2026-06-15 15:59:59", "2026-06-18"},
		// At and after the cutoff everything moves a day.
		{"2026-06-15 16:00:00", "2026-06-19"},
		{"2026-06-15 23:45:00", "2026-06-19"},
	}
	for _, tt := range tests {
		if got := iso(EarliestStart(istAt(t, tt.now), cutoff)); got != tt.want {
			t.Errorf("EarliestStart(%s) = %s, want %s", tt.now, got, tt.want)
		}
	}
}

// The earliest start's charging run must always be in the future, or the
// first delivery of a new schedule could be locked before it was ever made.
func TestEarliestStartIsNeverAlreadyLocked(t *testing.T) {
	const cutoff = 16
	lead := 30 * time.Minute
	start := istAt(t, "2026-06-15 00:00:00")
	for minutes := 0; minutes < 24*60; minutes += 7 {
		now := start.Add(time.Duration(minutes) * time.Minute)
		first := EarliestStart(now, cutoff)
		if Locked(first, now, cutoff, lead) {
			t.Fatalf("at %v the earliest start %s is already locked", now, iso(first))
		}
	}
}

// The order a run writes must carry the timeline §6.1 would have given a
// checkout order placed at the same moment — computed from the date, so a
// late run still dates it for the delivery the customer chose.
func TestTimelineMatchesCheckoutArithmetic(t *testing.T) {
	const cutoff = 16
	delivery := date(t, "2026-06-17")
	charged := istAt(t, "2026-06-15 15:30:00")
	got := Timeline(delivery, charged, cutoff)

	if want := istAt(t, "2026-06-15 16:00:00"); !got.ProcessingAt.Equal(want) {
		t.Errorf("ProcessingAt = %v, want %v", got.ProcessingAt, want)
	}
	if iso(got.DeliveryDay) != "2026-06-16" {
		t.Errorf("DeliveryDay = %s", iso(got.DeliveryDay))
	}
	if iso(got.ExpectedDeliveryDate) != "2026-06-17" {
		t.Errorf("ExpectedDeliveryDate = %s", iso(got.ExpectedDeliveryDate))
	}
}

func TestValidate(t *testing.T) {
	start := date(t, "2026-10-05")
	bad := []Rule{
		{Frequency: "fortnightly", Start: start},
		{Frequency: Weekly, Start: start},
		{Frequency: Weekly, Weekdays: []int{7}, Start: start},
		{Frequency: Monthly, DayOfMonth: 0, Start: start},
		{Frequency: Daily},
		{Frequency: Daily, Start: start, End: date(t, "2026-10-04")},
	}
	for i, rule := range bad {
		if rule.Validate() == nil {
			t.Errorf("rule %d validated but should not have: %+v", i, rule)
		}
	}
	if err := (Rule{Frequency: Weekly, Weekdays: []int{0, 6}, Start: start}).Validate(); err != nil {
		t.Errorf("valid weekly rule rejected: %v", err)
	}
}
