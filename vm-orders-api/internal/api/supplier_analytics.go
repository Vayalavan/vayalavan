package api

import (
	"net/http"
	"time"

	"github.com/vayal-mikrogreenz/vm-go-common/httpx"
	"github.com/vayal-mikrogreenz/vm-go-common/isttime"
	"github.com/vayal-mikrogreenz/vm-go-common/money"

	"github.com/vayal-mikrogreenz/vm-orders-api/internal/store"
)

// ---------------------------------------------------------------------------
// GET /supplier/analytics
// ---------------------------------------------------------------------------

// supplierBreakdownLimit bounds each ranked list, as on the admin dashboard.
//
// Five: a grower reads these from the top — the produce that earns, the pack
// size that sells — and a catalogue of forty rows buries exactly that. The
// total travels alongside so the screen can say "the top 5 of 31" instead of
// implying five is the whole catalogue.
const supplierBreakdownLimit = 5

type supplierDayRow struct {
	Date        string `json:"date"`
	Orders      int64  `json:"orders"`
	Units       int64  `json:"units"`
	Grams       int64  `json:"grams"`
	AmountPaise int64  `json:"amount_paise"`
	Amount      string `json:"amount_display"`
}

// supplierSizeCodeRow is one GRADE of one produce: "Pomegranate XL2".
//
// Named by both, because a grower's M2 pomegranate and M2 tomato are different
// crates and a row reading "M2" alone would be summing two of them.
type supplierSizeCodeRow struct {
	ProductName string `json:"product_name"`
	SizeCode    string `json:"size_code"`
	// The grower's own words for the grade — "250 g - 300 g". Empty when they
	// never filled it in.
	SizeMeta    string `json:"size_meta"`
	Units       int64  `json:"units"`
	Grams       int64  `json:"grams"`
	AmountPaise int64  `json:"amount_paise"`
	Amount      string `json:"amount_display"`
	Orders      int64  `json:"orders"`
}

type supplierPackRow struct {
	UnitLabel   string `json:"unit_label"`
	WeightGrams int32  `json:"weight_grams"`
	Units       int64  `json:"units"`
	Grams       int64  `json:"grams"`
	AmountPaise int64  `json:"amount_paise"`
	Amount      string `json:"amount_display"`
	Orders      int64  `json:"orders"`
}

// SupplierAnalytics is the grower's own trend screen.
//
// The sales screen answers "what am I owed" — a reconciliation document, one
// row per order, defaulting to all time. This answers a different question:
// what is selling, in what packs, and is it going up. Two screens rather than
// one because the second question wants a period and the first wants a
// lifetime, and a screen cannot default to both.
//
// Everything is scoped to the supplier on the request. There is no supplier_id
// parameter to tamper with (CLAUDE.md §7).
//
// What it deliberately does NOT return: anything about the customers. Cities,
// repeat rates and basket mix sit on the admin dashboard because they are ours
// to see; handing a third party a map of where our customers are is a
// different decision from showing a grower their own sales, and not one this
// endpoint should make quietly.
func (a *API) SupplierAnalytics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	supplierID, err := supplierFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	period, err := resolveTrendRange(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	from, to := period.From, period.To
	// The instants form, for the queries this screen shares with the sales
	// screen. Both describe the same window; only the shape differs, because
	// day-grouped queries need calendar dates and row filters need bounds.
	placedFrom, placedTo := period.Instants()

	// --- headline, and the settlement split ---------------------------------
	//
	// The same query the sales screen reconciles against, so the two screens
	// can never disagree about what a period earned.
	summary, err := a.queries.SupplierSalesSummary(ctx, store.SupplierSalesSummaryParams{
		SupplierID: supplierID, PlacedFrom: placedFrom, PlacedTo: placedTo,
	})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	// --- day by day ---------------------------------------------------------
	dailyRows, err := a.queries.SupplierAnalyticsDaily(ctx, store.SupplierAnalyticsDailyParams{
		SupplierID: supplierID, PlacedAt: from, PlacedAt_2: to,
	})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	daily := fillSupplierTrend(from, to, dailyRows)

	// --- what sold ----------------------------------------------------------
	byProduct, err := a.queries.SupplierSalesByProduct(ctx, store.SupplierSalesByProductParams{
		SupplierID: supplierID, PlacedFrom: placedFrom, PlacedTo: placedTo,
	})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	productTotal := len(byProduct)
	products := make([]supplierProductRow, 0, len(byProduct))
	for _, row := range byProduct {
		products = append(products, supplierProductRow{
			ProductName: row.ProductName,
			Grade:       row.Grade,
			Units:       row.Units,
			Grams:       row.Grams,
			AmountPaise: row.AmountPaise,
			Amount:      money.FormatRupees(money.Paise(row.AmountPaise)),
		})
	}

	packRows, err := a.queries.SupplierAnalyticsByPack(ctx, store.SupplierAnalyticsByPackParams{
		SupplierID: supplierID, PlacedFrom: placedFrom, PlacedTo: placedTo,
	})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	packTotal := len(packRows)
	packs := make([]supplierPackRow, 0, len(packRows))
	for _, row := range packRows {
		packs = append(packs, supplierPackRow{
			UnitLabel:   row.UnitLabel,
			WeightGrams: row.WeightGrams,
			Units:       row.Units,
			Grams:       row.Grams,
			AmountPaise: row.AmountPaise,
			Amount:      money.FormatRupees(money.Paise(row.AmountPaise)),
			Orders:      row.Orders,
		})
	}

	// --- which grades sell --------------------------------------------------
	//
	// Separate from "what sold": that one is per produce, and a grower who has
	// picked eleven grades of pomegranate needs to know which crates paid for
	// the picking. Ungraded lines are excluded by the query, so a grower who
	// does not grade gets an empty list and the clients hide the card.
	sizeCodeRows, err := a.queries.SupplierAnalyticsBySizeCode(
		ctx, store.SupplierAnalyticsBySizeCodeParams{
			SupplierID: supplierID, PlacedFrom: placedFrom, PlacedTo: placedTo,
		})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	sizeCodeTotal := len(sizeCodeRows)
	sizeCodes := make([]supplierSizeCodeRow, 0, len(sizeCodeRows))
	for _, row := range sizeCodeRows {
		sizeCodes = append(sizeCodes, supplierSizeCodeRow{
			ProductName: row.ProductName,
			SizeCode:    row.SizeCode,
			SizeMeta:    row.SizeMeta,
			Units:       row.Units,
			Grams:       row.Grams,
			AmountPaise: row.AmountPaise,
			Amount:      money.FormatRupees(money.Paise(row.AmountPaise)),
			Orders:      row.Orders,
		})
	}

	// Totals summed from the day rows rather than queried again: they are the
	// same set of lines, and a second aggregate could round differently and
	// leave the chart disagreeing with the figure above it.
	var units, grams int64
	for _, day := range daily {
		units += day.Units
		grams += day.Grams
	}

	var averagePaise int64
	if summary.OrderCount > 0 {
		averagePaise = summary.GrossPaise / summary.OrderCount
	}

	a.respond(ctx, w, http.StatusOK, map[string]any{
		"range": period.Describe(),
		"summary": map[string]any{
			"orders":              summary.OrderCount,
			"gross_paise":         summary.GrossPaise,
			"gross":               money.FormatRupees(money.Paise(summary.GrossPaise)),
			"pending_paise":       summary.PendingPaise,
			"pending":             money.FormatRupees(money.Paise(summary.PendingPaise)),
			"settled_paise":       summary.SettledPaise,
			"settled":             money.FormatRupees(money.Paise(summary.SettledPaise)),
			"average_order_paise": averagePaise,
			"average_order":       money.FormatRupees(money.Paise(averagePaise)),
			"units":               units,
			"grams":               grams,
		},
		"daily":                daily,
		"by_produce":           capRows(products, supplierBreakdownLimit),
		"by_produce_total":     productTotal,
		"by_produce_truncated": productTotal > supplierBreakdownLimit,
		"by_pack":              capRows(packs, supplierBreakdownLimit),
		"by_pack_total":        packTotal,
		"by_pack_truncated":    packTotal > supplierBreakdownLimit,
		// Graded lines only — see SupplierAnalyticsBySizeCode. Empty for a
		// grower who does not grade, and the clients render nothing.
		"by_size_code":           capRows(sizeCodes, supplierBreakdownLimit),
		"by_size_code_total":     sizeCodeTotal,
		"by_size_code_truncated": sizeCodeTotal > supplierBreakdownLimit,
	})
}

// fillSupplierTrend expands the query's sparse rows to every day in the window.
//
// Same reasoning as the admin dashboard's fillDailyTrend: the query returns
// only days that sold something, and a chart whose axis silently skips the
// quiet days describes a different week from the real one. Stepped in IST
// (CLAUDE.md rule 2).
func fillSupplierTrend(
	from, to time.Time, rows []store.SupplierAnalyticsDailyRow,
) []supplierDayRow {
	byDay := make(map[string]store.SupplierAnalyticsDailyRow, len(rows))
	for _, row := range rows {
		byDay[isttime.FormatISODate(row.Day)] = row
	}

	days := make([]supplierDayRow, 0, len(rows))
	for day := from; !day.After(to); day = isttime.AddDays(day, 1) {
		key := isttime.FormatISODate(day)
		row := byDay[key]
		days = append(days, supplierDayRow{
			Date:        key,
			Orders:      row.Orders,
			Units:       row.Units,
			Grams:       row.Grams,
			AmountPaise: row.AmountPaise,
			Amount:      money.FormatRupees(money.Paise(row.AmountPaise)),
		})
	}
	return days
}

// capRows keeps the top of an already-ranked list.
//
// Generic because the lists differ only in what a row holds; the ranking is
// the query's job and this must never reorder it. The limit is a parameter
// rather than the constant above because the admin dashboard uses this too,
// and its cap is its own decision even while the two happen to agree.
func capRows[T any](rows []T, limit int) []T {
	if len(rows) <= limit {
		return rows
	}
	return rows[:limit]
}
