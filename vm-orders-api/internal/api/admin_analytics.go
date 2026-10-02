package api

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/vayal-mikrogreenz/vm-go-common/httpx"
	"github.com/vayal-mikrogreenz/vm-go-common/isttime"
	"github.com/vayal-mikrogreenz/vm-go-common/money"

	"github.com/vayal-mikrogreenz/vm-orders-api/internal/store"
)

// ---------------------------------------------------------------------------
// GET /admin/analytics
// ---------------------------------------------------------------------------

// analyticsBreakdownLimit bounds each ranked list.
//
// Five, because a ranked list is read from the top and the tail is answered
// better by the screen the row links to than by more rows here. The total is
// returned alongside, so the UI can say "the top 5 of 31" rather than implying
// five is all there is.
const (
	analyticsBreakdownLimit  = 5
	analyticsDestinationRows = 8
)

type analyticsSupplierRow struct {
	SupplierID   string `json:"supplier_id"`
	BusinessName string `json:"business_name"`
	SalesPaise   int64  `json:"sales_paise"`
	Sales        string `json:"sales_display"`
	// PayablePaise is what the growers' payout rows pay them for this period;
	// MarginPaise is the difference — commission plus markup, as it actually
	// landed rather than as a rate says it should have.
	PayablePaise int64  `json:"payable_paise"`
	Payable      string `json:"payable_display"`
	MarginPaise  int64  `json:"margin_paise"`
	Margin       string `json:"margin_display"`
	Units        int64  `json:"units"`
	Grams        int64  `json:"grams"`
	Orders       int64  `json:"orders"`
}

type analyticsProduceRow struct {
	ProductName string  `json:"product_name"`
	Grade       *string `json:"grade"`
	// The GRADE this row sold out of — "XL2". Empty on an ungraded listing and
	// on orders placed before size codes existed. Without it the row sums
	// crates that are separate goods at separate prices (CLAUDE.md §5.2).
	SizeCode    string `json:"size_code"`
	SalesPaise  int64  `json:"sales_paise"`
	Sales       string `json:"sales_display"`
	MarkupPaise int64  `json:"markup_paise"`
	Markup      string `json:"markup_display"`
	Units       int64  `json:"units"`
	Grams       int64  `json:"grams"`
	Orders      int64  `json:"orders"`
}

// analyticsSizeCodeRow is one GRADE of one produce, across every supplier.
//
// Named by both, because one grower's M2 pomegranate and another's M2 tomato
// are different goods and a row reading "M2" alone would sum them.
type analyticsSizeCodeRow struct {
	ProductName string `json:"product_name"`
	SizeCode    string `json:"size_code"`
	// The grower's own words for the grade — "250 g - 300 g". The latest
	// wording wins where two suppliers word it differently.
	SizeMeta string `json:"size_meta"`
	// How many suppliers sold this grade in the period. One is a single
	// grower's habit; five is a market.
	Suppliers   int64  `json:"suppliers"`
	SalesPaise  int64  `json:"sales_paise"`
	Sales       string `json:"sales_display"`
	MarkupPaise int64  `json:"markup_paise"`
	Markup      string `json:"markup_display"`
	Units       int64  `json:"units"`
	Grams       int64  `json:"grams"`
	Orders      int64  `json:"orders"`
}

type analyticsDayRow struct {
	Date       string `json:"date"`
	Orders     int64  `json:"orders"`
	PaidOrders int64  `json:"paid_orders"`
	GmvPaise   int64  `json:"gmv_paise"`
	Gmv        string `json:"gmv_display"`
}

type analyticsOutcomeRow struct {
	Status     string `json:"status"`
	Orders     int64  `json:"orders"`
	ValuePaise int64  `json:"value_paise"`
	Value      string `json:"value_display"`
}

type analyticsDestinationRow struct {
	City     string `json:"city"`
	Pincode  string `json:"pincode"`
	Orders   int64  `json:"orders"`
	GmvPaise int64  `json:"gmv_paise"`
	Gmv      string `json:"gmv_display"`
}

// AdminAnalytics is the dashboard's breakdowns: where the money came from,
// what was sold, and how the period moved day to day.
//
// A SEPARATE endpoint from /admin/dashboard, not extra keys on it, for two
// reasons. The tiles refresh every minute and must stay cheap; these are seven
// aggregates over order_items and belong on their own cadence. And a failure
// here — a slow group-by, a profile lookup timing out — must not take the
// headline figures off the screen with it.
//
// The period is resolved by the SAME resolver the dashboard uses, so both
// screens can never disagree about what "past 7 days" means, and it is always
// resolved in IST from the server's clock (CLAUDE.md rule 2).
func (a *API) AdminAnalytics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if _, err := adminFromRequest(r); err != nil {
		a.fail(ctx, w, err)
		return
	}

	period, err := resolveDashboardRange(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	from, to := period.From, period.To

	// --- revenue by supplier -------------------------------------------------
	supplierRows, err := a.queries.AnalyticsRevenueBySupplier(ctx,
		store.AnalyticsRevenueBySupplierParams{PlacedAt: from, PlacedAt_2: to})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	ids := make([]uuid.UUID, 0, len(supplierRows))
	for _, row := range supplierRows {
		ids = append(ids, row.SupplierID)
	}
	// Best-effort, exactly as on the settlement screen: an unreachable
	// profile-api leaves rows labelled by id rather than blanking the money.
	names := a.supplierNames(ctx, ids)

	suppliers := make([]analyticsSupplierRow, 0, len(supplierRows))
	for _, row := range supplierRows {
		// A payout larger than the sales it settles would mean a data problem,
		// not a negative margin. Report zero rather than a figure that cannot
		// be true — the same guard the dashboard's commission figure uses.
		margin := row.SalesPaise - row.PayablePaise
		if margin < 0 {
			margin = 0
		}
		name := names[row.SupplierID]
		if name == "" {
			name = "Supplier " + row.SupplierID.String()[:8]
		}
		suppliers = append(suppliers, analyticsSupplierRow{
			SupplierID:   row.SupplierID.String(),
			BusinessName: name,
			SalesPaise:   row.SalesPaise,
			Sales:        money.FormatRupees(money.Paise(row.SalesPaise)),
			PayablePaise: row.PayablePaise,
			Payable:      money.FormatRupees(money.Paise(row.PayablePaise)),
			MarginPaise:  margin,
			Margin:       money.FormatRupees(money.Paise(margin)),
			Units:        row.Units,
			Grams:        row.Grams,
			Orders:       row.Orders,
		})
	}

	// --- revenue by produce --------------------------------------------------
	produceRows, err := a.queries.AnalyticsRevenueByProduce(ctx,
		store.AnalyticsRevenueByProduceParams{PlacedAt: from, PlacedAt_2: to})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	produce := make([]analyticsProduceRow, 0, len(produceRows))
	for _, row := range produceRows {
		produce = append(produce, analyticsProduceRow{
			ProductName: row.ProductName,
			Grade:       row.Grade,
			SizeCode:    row.SizeCode,
			SalesPaise:  row.SalesPaise,
			Sales:       money.FormatRupees(money.Paise(row.SalesPaise)),
			MarkupPaise: row.MarkupPaise,
			Markup:      money.FormatRupees(money.Paise(row.MarkupPaise)),
			Units:       row.Units,
			Grams:       row.Grams,
			Orders:      row.Orders,
		})
	}

	// --- revenue by grade ----------------------------------------------------
	//
	// Separate from the produce breakdown above rather than a drill-down of
	// it: that one answers "what sells", this one answers "which crate", and
	// a grower deciding what to pick next season needs the second.
	sizeCodeRows, err := a.queries.AnalyticsRevenueBySizeCode(ctx,
		store.AnalyticsRevenueBySizeCodeParams{PlacedAt: from, PlacedAt_2: to})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	sizeCodes := make([]analyticsSizeCodeRow, 0, len(sizeCodeRows))
	for _, row := range sizeCodeRows {
		sizeCodes = append(sizeCodes, analyticsSizeCodeRow{
			ProductName: row.ProductName,
			SizeCode:    deref(row.SizeCode),
			SizeMeta:    row.SizeMeta,
			Suppliers:   row.Suppliers,
			SalesPaise:  row.SalesPaise,
			Sales:       money.FormatRupees(money.Paise(row.SalesPaise)),
			MarkupPaise: row.MarkupPaise,
			Markup:      money.FormatRupees(money.Paise(row.MarkupPaise)),
			Units:       row.Units,
			Grams:       row.Grams,
			Orders:      row.Orders,
		})
	}

	// --- the last seven days, whatever period is selected --------------------
	//
	// Deliberately NOT scoped to the picker. Every other figure on this screen
	// moves with the range, which makes it impossible to answer "how is the
	// week going" without changing what everything else is showing. This one
	// line always covers today and the six days before it, in IST, so it is
	// the same week no matter what the rest of the page is set to.
	weekTo := isttime.Today()
	weekFrom := isttime.AddDays(weekTo, -6)
	weekRows, err := a.queries.AnalyticsDailyTrend(ctx,
		store.AnalyticsDailyTrendParams{PlacedAt: weekFrom, PlacedAt_2: weekTo})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	week := fillDailyTrend(weekFrom, weekTo, weekRows)

	// --- what became of the orders placed ------------------------------------
	outcomeRows, err := a.queries.AnalyticsOrderOutcomes(ctx,
		store.AnalyticsOrderOutcomesParams{PlacedAt: from, PlacedAt_2: to})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	outcomes := make([]analyticsOutcomeRow, 0, len(outcomeRows))
	var placedOrders int64
	for _, row := range outcomeRows {
		placedOrders += row.Orders
		outcomes = append(outcomes, analyticsOutcomeRow{
			Status:     row.Status,
			Orders:     row.Orders,
			ValuePaise: row.ValuePaise,
			Value:      money.FormatRupees(money.Paise(row.ValuePaise)),
		})
	}

	// --- basket, customers, cutoff -------------------------------------------
	basket, err := a.queries.AnalyticsBasketAndCustomers(ctx,
		store.AnalyticsBasketAndCustomersParams{PlacedAt: from, PlacedAt_2: to})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	cutoff, err := a.queries.AnalyticsCutoffSplit(ctx,
		store.AnalyticsCutoffSplitParams{
			PlacedAt: from, PlacedAt_2: to,
			// The configured hour, never a literal 16 — see §6.1 and the
			// query's own note.
			Column3: int32(a.cutoffHour),
		})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	destinationRows, err := a.queries.AnalyticsTopDestinations(ctx,
		store.AnalyticsTopDestinationsParams{
			PlacedAt: from, PlacedAt_2: to, Limit: analyticsDestinationRows,
		})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	destinations := make([]analyticsDestinationRow, 0, len(destinationRows))
	for _, row := range destinationRows {
		destinations = append(destinations, analyticsDestinationRow{
			City:     row.City,
			Pincode:  row.Pincode,
			Orders:   row.Orders,
			GmvPaise: row.GmvPaise,
			Gmv:      money.FormatRupees(money.Paise(row.GmvPaise)),
		})
	}

	// Averages are computed here rather than in SQL so the divide-by-zero case
	// has one obvious answer: a period with no paid orders has no average
	// basket, and reporting ₹0.00 would read as "we sold nothing at ₹0" rather
	// than "there is nothing to average".
	var aovPaise int64
	if basket.PaidOrders > 0 {
		aovPaise = basket.GmvPaise / basket.PaidOrders
	}
	newCustomers := basket.Customers - basket.ReturningCustomers
	if newCustomers < 0 {
		newCustomers = 0
	}

	a.respond(ctx, w, http.StatusOK, map[string]any{
		"range": map[string]any{
			"key":   period.Key,
			"label": period.Label,
			"from":  isttime.FormatISODate(from),
			"to":    isttime.FormatISODate(to),
			"days":  int(to.Sub(from).Hours()/24) + 1,
		},
		// Ranked lists, capped. `truncated` tells the UI whether its "and N
		// more" line is honest.
		"by_supplier":           capSuppliers(suppliers),
		"by_supplier_truncated": len(suppliers) > analyticsBreakdownLimit,
		"by_supplier_total":     len(suppliers),
		"by_produce":            capProduce(produce),
		"by_produce_truncated":  len(produce) > analyticsBreakdownLimit,
		"by_produce_total":      len(produce),
		// Graded lines only — a shop with no graded produce gets an empty list
		// and the dashboard renders nothing rather than an empty card.
		"by_size_code":           capRows(sizeCodes, analyticsBreakdownLimit),
		"by_size_code_truncated": len(sizeCodes) > analyticsBreakdownLimit,
		"by_size_code_total":     len(sizeCodes),
		// Fixed seven-day window, independent of `range` above. The dates are
		// returned with it so the screen never has to work out which week this
		// is — and never computes a date of its own (CLAUDE.md rule 2).
		"last_7_days": map[string]any{
			"from": isttime.FormatISODate(weekFrom),
			"to":   isttime.FormatISODate(weekTo),
			"days": week,
		},
		"outcomes":      outcomes,
		"orders_placed": placedOrders,
		"basket": map[string]any{
			"paid_orders":         basket.PaidOrders,
			"gmv_paise":           basket.GmvPaise,
			"gmv":                 money.FormatRupees(money.Paise(basket.GmvPaise)),
			"average_order_paise": aovPaise,
			"average_order":       money.FormatRupees(money.Paise(aovPaise)),
			"units":               basket.Units,
			// Packs per order, ×100 so the UI can show one decimal without
			// anyone parsing a float out of a string.
			"units_per_order_centi": centiPer(basket.Units, basket.PaidOrders),
		},
		"customers": map[string]any{
			"total":     basket.Customers,
			"new":       newCustomers,
			"returning": basket.ReturningCustomers,
		},
		// Which side of the 4pm cutoff the day's orders landed on: everything
		// after it waits for tomorrow's processing run (§6.1), so a period
		// that is mostly "after" is a period of later deliveries.
		"cutoff": map[string]any{
			"hour_ist": a.cutoffHour,
			"before":   cutoff.BeforeCutoff,
			"after":    cutoff.AfterCutoff,
		},
		"destinations": destinations,
	})
}

// fillDailyTrend expands the query's sparse rows to every day in the window.
//
// The query returns only days that had an order. A day with no orders must
// still appear, as a zero: a week that reads as five bars because two days
// sold nothing describes a different week from the real one — and a chart
// whose x-axis silently skips days is worse than no chart.
//
// Days are stepped in IST (CLAUDE.md rule 2), so the 30-minute offset and any
// DST elsewhere cannot slide a bar onto the wrong day.
func fillDailyTrend(
	from, to time.Time, rows []store.AnalyticsDailyTrendRow,
) []analyticsDayRow {
	byDay := make(map[string]store.AnalyticsDailyTrendRow, len(rows))
	for _, row := range rows {
		byDay[isttime.FormatISODate(row.Day)] = row
	}

	trend := make([]analyticsDayRow, 0, len(rows))
	for day := from; !day.After(to); day = isttime.AddDays(day, 1) {
		key := isttime.FormatISODate(day)
		row := byDay[key]
		trend = append(trend, analyticsDayRow{
			Date:       key,
			Orders:     row.Orders,
			PaidOrders: row.PaidOrders,
			GmvPaise:   row.GmvPaise,
			Gmv:        money.FormatRupees(money.Paise(row.GmvPaise)),
		})
	}
	return trend
}

// centiPer is a per-order average in hundredths, guarding the empty period.
func centiPer(total, orders int64) int64 {
	if orders <= 0 {
		return 0
	}
	return total * 100 / orders
}

func capSuppliers(rows []analyticsSupplierRow) []analyticsSupplierRow {
	if len(rows) <= analyticsBreakdownLimit {
		return rows
	}
	return rows[:analyticsBreakdownLimit]
}

func capProduce(rows []analyticsProduceRow) []analyticsProduceRow {
	if len(rows) <= analyticsBreakdownLimit {
		return rows
	}
	return rows[:analyticsBreakdownLimit]
}
