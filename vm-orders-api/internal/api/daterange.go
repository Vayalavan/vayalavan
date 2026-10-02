package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vayal-mikrogreenz/vm-go-common/httpx"
	"github.com/vayal-mikrogreenz/vm-go-common/isttime"
)

// dashboardRange is a resolved, validated reporting period.
//
// One vocabulary for every reporting screen on the platform — the admin
// dashboard, the fulfilment queues, and the supplier's own sales — so that
// "past 7 days" cannot come to mean two different weeks depending on which
// screen asked.
type dashboardRange struct {
	From time.Time
	To   time.Time
	// All is the unbounded window: every order ever. Only screens that opt in
	// may ask for it (see resolveSalesRange); From and To are unset when it
	// is true, and a caller that ignores it would report from the zero time.
	All   bool
	Key   string
	Label string
}

// Instants converts the window to the half-open pair the supplier sales
// queries take: midnight IST at the start of From, and midnight IST at the
// start of the day AFTER To, so the last day counts whole.
//
// Bounds, not dates, because those queries filter on `placed_at`, which is a
// TIMESTAMPTZ. Converting here keeps the timezone rule in Go — a
// `AT TIME ZONE 'Asia/Kolkata'` in the SQL would be a second copy of it, in a
// place no Go test covers (CLAUDE.md rule 2).
//
// All time returns two nils, which the queries read as no filter.
func (d dashboardRange) Instants() (*time.Time, *time.Time) {
	if d.All {
		return nil, nil
	}
	from := isttime.StartOfDayIST(d.From)
	to := isttime.StartOfDayIST(isttime.AddDays(d.To, 1))
	return &from, &to
}

// Describe is the window as a screen should label it.
func (d dashboardRange) Describe() map[string]any {
	if d.All {
		return map[string]any{"key": d.Key, "label": d.Label, "all_time": true}
	}
	return map[string]any{
		"key":      d.Key,
		"label":    d.Label,
		"all_time": false,
		"from":     isttime.FormatISODate(d.From),
		"to":       isttime.FormatISODate(d.To),
	}
}

// maxDashboardRangeDays bounds a custom range.
//
// Not a performance limit — the query is a single aggregate — but a guard
// against a typo like 2026 vs 2016 turning one screen into a full table scan
// of every order ever placed.
const maxDashboardRangeDays = 400

func parseISTDate(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, httpx.BadRequest("a date is required.")
	}
	parsed, err := isttime.ParseISODate(raw)
	if err != nil {
		return time.Time{}, httpx.BadRequest("date must be in YYYY-MM-DD format.")
	}
	return parsed, nil
}

// resolveDashboardRange turns ?range= (and ?from=/?to=) into IST dates for the
// admin screens, which default to today and have no all-time option: they are
// looking at what is happening now.
func resolveDashboardRange(r *http.Request) (dashboardRange, error) {
	return resolveRange(r, "today", false)
}

// resolveSalesRange is the supplier sales variant.
//
// It defaults to ALL TIME, unlike the admin screens. That screen exists to
// reconcile against manual NEFT settlement (CLAUDE.md §6, supplier payouts),
// so its lifetime totals are the figure a grower checks their bank against;
// defaulting to today would silently change what "gross" means for everyone
// already using it.
func resolveSalesRange(r *http.Request) (dashboardRange, error) {
	return resolveRange(r, "all", true)
}

// resolveQueueRange is the admin ORDERS list variant.
//
// Allows all time, unlike the dashboard tiles: that screen is a queue an
// operator browses and exports, not a figure for a period, and "show me
// everything" is a reasonable thing to ask of a list.
//
// The BULK actions on the same screen deliberately do NOT use this — see
// AdminBulkDispatch. "Dispatch all processed" across all time is a very
// different act from dispatching what is on screen today, and it should not
// become available just because the list defaults to showing everything.
func resolveQueueRange(r *http.Request) (dashboardRange, error) {
	return resolveRange(r, "today", true)
}

// resolveTrendRange is the analytics variant, for screens whose whole purpose
// is a line over time.
//
// Defaults to the past week rather than today. "Today" is the right default
// for the tiles an operator checks every morning, but an analytics screen
// opening on a single day is a chart with one point on it — the default should
// be the shape the screen exists to show. All-time is not offered: a grower's
// first year would compress a useful week into three pixels.
func resolveTrendRange(r *http.Request) (dashboardRange, error) {
	return resolveRange(r, "7d", false)
}

// resolveRange is the shared resolver.
//
// Every named period ENDS today and counts backwards inclusive of today, which
// is what "past 7 days" means to someone running a shop: this week so far,
// including this morning. The alternative — seven days ending yesterday —
// hides the day they most want to see.
//
// All arithmetic is in Asia/Kolkata and the client's clock is never consulted
// (CLAUDE.md rule 2). A client may ask for a period; it may not assert what
// today is.
func resolveRange(r *http.Request, defaultKey string, allowAll bool) (dashboardRange, error) {
	today := isttime.Today()
	key := strings.TrimSpace(r.URL.Query().Get("range"))
	if key == "" {
		key = defaultKey
	}

	back := func(days int, label string) (dashboardRange, error) {
		return dashboardRange{
			From: isttime.AddDays(today, -(days - 1)), To: today,
			Key: key, Label: label,
		}, nil
	}

	switch key {
	case "all":
		if !allowAll {
			return dashboardRange{}, httpx.BadRequest(rangeKeyError(allowAll))
		}
		return dashboardRange{All: true, Key: key, Label: "All time"}, nil
	case "today":
		return dashboardRange{From: today, To: today, Key: key, Label: "Today"}, nil
	case "2d":
		return back(2, "Past 2 days")
	case "3d":
		return back(3, "Past 3 days")
	case "7d":
		return back(7, "Past 7 days")
	case "1m":
		return back(30, "Past month")
	case "custom":
		from, err := parseISTDate(r.URL.Query().Get("from"))
		if err != nil {
			return dashboardRange{}, httpx.BadRequest(
				"from must be a date in YYYY-MM-DD form.")
		}
		to, err := parseISTDate(r.URL.Query().Get("to"))
		if err != nil {
			return dashboardRange{}, httpx.BadRequest(
				"to must be a date in YYYY-MM-DD form.")
		}
		if to.Before(from) {
			return dashboardRange{}, httpx.BadRequest(
				"The end of the range cannot be before its start.")
		}
		// A future end date is not an error worth refusing — it simply has no
		// orders in it — but clamping keeps the label honest.
		if to.After(today) {
			to = today
		}
		if to.Sub(from) > time.Duration(maxDashboardRangeDays)*24*time.Hour {
			return dashboardRange{}, httpx.BadRequest(
				"That range is too long. Choose a period under " +
					strconv.Itoa(maxDashboardRangeDays) + " days.")
		}
		return dashboardRange{
			From: from, To: to, Key: key,
			Label: isttime.FormatDate(from) + " to " + isttime.FormatDate(to),
		}, nil
	default:
		return dashboardRange{}, httpx.BadRequest(rangeKeyError(allowAll))
	}
}

func rangeKeyError(allowAll bool) string {
	keys := "today, 2d, 3d, 7d, 1m, custom"
	if allowAll {
		keys = "all, " + keys
	}
	return "range must be one of: " + keys + "."
}
