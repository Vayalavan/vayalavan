package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vayal-mikrogreenz/vm-go-common/httpx"
	"github.com/vayal-mikrogreenz/vm-go-common/isttime"
	"github.com/vayal-mikrogreenz/vm-go-common/money"
	"github.com/vayal-mikrogreenz/vm-go-common/produce"

	"github.com/vayal-mikrogreenz/vm-orders-api/internal/catalogclient"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/pricing"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/recurrence"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/store"
)

// Scheduled and repeat orders — CLAUDE.md §6.7.
//
// A schedule is made from the cart at checkout: the customer chooses a first
// delivery DATE (never a time) and whether it repeats daily, weekly or
// monthly. Nothing is charged or reserved here. Each date is charged from the
// wallet by the charging run (scheduler.go), at that day's prices, against
// that day's stock.

const (
	scheduleActive    = "active"
	schedulePaused    = "paused"
	scheduleCancelled = "cancelled"
	scheduleCompleted = "completed"

	occurrencePlaced            = "placed"
	occurrenceSkippedByCustomer = "skipped_by_customer"
	occurrenceSkippedNoStock    = "skipped_no_stock"
	occurrenceSkippedLowBalance = "skipped_low_balance"
	occurrenceMissed            = "missed"

	// How many upcoming dates the detail view lists.
	upcomingDates = 8
	// How far past the start a repeat may be set to end.
	maxScheduleSpan = 366
)

func ruleOf(s store.Schedule) recurrence.Rule {
	weekdays := make([]int, 0, len(s.Weekdays))
	for _, day := range s.Weekdays {
		weekdays = append(weekdays, int(day))
	}
	rule := recurrence.Rule{
		Frequency: recurrence.Frequency(s.Frequency),
		Weekdays:  weekdays,
		Start:     s.StartDate,
	}
	if s.DayOfMonth != nil {
		rule.DayOfMonth = int(*s.DayOfMonth)
	}
	if s.EndDate != nil {
		rule.End = *s.EndDate
	}
	return rule
}

var weekdayShort = []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}

// ruleSummary is the schedule in words, for a card: "Every Mon & Thu".
func ruleSummary(rule recurrence.Rule) string {
	switch rule.Frequency {
	case recurrence.Once:
		return "One-off, " + isttime.FormatDate(rule.Start)
	case recurrence.Daily:
		return "Every day"
	case recurrence.Weekly:
		names := make([]string, 0, len(rule.Weekdays))
		for _, day := range rule.Weekdays {
			names = append(names, weekdayShort[day])
		}
		if len(names) == 7 {
			return "Every day"
		}
		if len(names) > 1 {
			return "Every " + strings.Join(names[:len(names)-1], ", ") + " & " + names[len(names)-1]
		}
		return "Every " + strings.Join(names, "")
	case recurrence.Monthly:
		if rule.DayOfMonth >= 29 {
			return fmt.Sprintf("Monthly on the %s (or the month's last day)", ordinal(rule.DayOfMonth))
		}
		return "Monthly on the " + ordinal(rule.DayOfMonth)
	}
	return string(rule.Frequency)
}

func ordinal(n int) string {
	suffix := "th"
	if n%100 < 11 || n%100 > 13 {
		switch n % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return fmt.Sprintf("%d%s", n, suffix)
}

// firstUnlockedDate is the earliest delivery date whose charging run has not
// started — the first date a resumed or edited schedule can still affect.
func (a *API) firstUnlockedDate(now time.Time) time.Time {
	day := recurrence.Day(now)
	for i := 0; i < 5; i++ {
		if !recurrence.Locked(day, now, a.cutoffHour, a.schedules.ChargeLead) {
			return day
		}
		day = isttime.AddDays(day, 1)
	}
	return day
}

// ---------------------------------------------------------------------------
// Responses
// ---------------------------------------------------------------------------

type scheduleItemResponse struct {
	ProductID      string `json:"product_id"`
	ProductUnitID  string `json:"product_unit_id"`
	ProductName    string `json:"product_name"`
	UnitLabel      string `json:"unit_label"`
	SizeCode       string `json:"size_code,omitempty"`
	Qty            int32  `json:"qty"`
	AvailableToday bool   `json:"available_today"`
	// Today's price, as an estimate. Omitted when the pack is not listed.
	UnitPrice string `json:"unit_price_display,omitempty"`
	LineTotal string `json:"line_total_display,omitempty"`
}

type upcomingDateResponse struct {
	Date    string `json:"date"`
	Display string `json:"display"`
	Skipped bool   `json:"skipped"`
	// Locked: its charging run has started, so it can no longer be skipped.
	Locked   bool   `json:"locked"`
	ChargeAt string `json:"charge_at"`
}

type occurrenceResponse struct {
	Date        string  `json:"date"`
	Display     string  `json:"display"`
	Status      string  `json:"status"`
	OrderID     *string `json:"order_id,omitempty"`
	OrderNumber *string `json:"order_number,omitempty"`
	Total       string  `json:"total_display,omitempty"`
	Note        *string `json:"note,omitempty"`
}

type scheduleResponse struct {
	ID         string  `json:"id"`
	Status     string  `json:"status"`
	Frequency  string  `json:"frequency"`
	Weekdays   []int   `json:"weekdays"`
	DayOfMonth *int16  `json:"day_of_month,omitempty"`
	Summary    string  `json:"summary"`
	StartDate  string  `json:"start_date"`
	EndDate    *string `json:"end_date,omitempty"`

	NextDeliveryDate    *string `json:"next_delivery_date,omitempty"`
	NextDeliveryDisplay *string `json:"next_delivery_display,omitempty"`
	// When the next date is charged — the last moment to top up or skip it.
	NextChargeAt *string `json:"next_charge_at,omitempty"`

	// One delivery at TODAY's prices. An estimate: each date is charged at
	// its own day's prices, for what is in stock then.
	EstimatePaise   int64  `json:"estimate_paise"`
	Estimate        string `json:"estimate_display"`
	LowBalanceSkips int32  `json:"low_balance_skips"`

	Address addressSnapshot        `json:"address"`
	Items   []scheduleItemResponse `json:"items"`

	Upcoming []upcomingDateResponse `json:"upcoming,omitempty"`
	History  []occurrenceResponse   `json:"history,omitempty"`

	CreatedAt string `json:"created_at"`
}

func (a *API) toScheduleResponse(
	s store.Schedule, items []store.ScheduleItem, units map[uuid.UUID]catalogclient.Unit,
) scheduleResponse {
	rule := ruleOf(s)
	var address addressSnapshot
	_ = json.Unmarshal(s.AddressSnapshot, &address)

	weekdays := rule.Weekdays
	if weekdays == nil {
		weekdays = []int{}
	}
	out := scheduleResponse{
		ID:              s.ID.String(),
		Status:          s.Status,
		Frequency:       s.Frequency,
		Weekdays:        weekdays,
		DayOfMonth:      s.DayOfMonth,
		Summary:         ruleSummary(rule),
		StartDate:       isttime.FormatISODate(s.StartDate),
		LowBalanceSkips: s.LowBalanceSkips,
		Address:         address,
		Items:           make([]scheduleItemResponse, 0, len(items)),
		CreatedAt:       s.CreatedAt.Format(time.RFC3339),
	}
	if s.EndDate != nil {
		end := isttime.FormatISODate(*s.EndDate)
		out.EndDate = &end
	}
	if s.NextDeliveryDate != nil && s.Status != scheduleCancelled && s.Status != scheduleCompleted {
		next := isttime.FormatISODate(*s.NextDeliveryDate)
		display := isttime.FormatDate(*s.NextDeliveryDate)
		chargeAt := recurrence.ChargeAt(*s.NextDeliveryDate, a.cutoffHour, a.schedules.ChargeLead).
			Format(time.RFC3339)
		out.NextDeliveryDate, out.NextDeliveryDisplay, out.NextChargeAt = &next, &display, &chargeAt
	}

	lines := []pricing.Line{}
	for _, item := range items {
		entry := scheduleItemResponse{
			ProductID:     item.ProductID.String(),
			ProductUnitID: item.ProductUnitID.String(),
			ProductName:   item.ProductNameSnapshot,
			UnitLabel:     item.UnitLabelSnapshot,
			SizeCode:      produce.GradeLabel(deref(item.SizeCodeSnapshot)),
			Qty:           item.Qty,
		}
		if unit, listed := units[item.ProductUnitID]; listed {
			entry.AvailableToday = unit.Purchasable
			entry.UnitPrice = money.FormatRupees(unit.PricePaise)
			entry.LineTotal = money.FormatRupees(unit.PricePaise.Mul(int64(item.Qty)))
			lines = append(lines, pricing.Line{
				SupplierID: unit.SupplierID, UnitPricePaise: unit.PricePaise, Qty: int64(item.Qty),
			})
		}
		out.Items = append(out.Items, entry)
	}
	estimate := pricing.Compute(lines, a.pricing).TotalPaise
	out.EstimatePaise = estimate.Int64()
	out.Estimate = money.FormatRupees(estimate)
	return out
}

// todaysUnits is the catalogue for pricing estimates. A failure degrades to
// no estimate rather than failing the page: the schedule itself does not
// depend on it.
func (a *API) todaysUnits(ctx context.Context) map[uuid.UUID]catalogclient.Unit {
	today, err := a.catalog.TodaysCatalogue(ctx)
	if err != nil {
		a.logger.WarnContext(ctx, "could not price schedule estimates", slog2(err))
		return map[uuid.UUID]catalogclient.Unit{}
	}
	return today.Units
}

func (a *API) walletBalance(ctx context.Context, customerID uuid.UUID) money.Paise {
	wallet, err := a.queries.EnsureWallet(ctx, customerID)
	if err != nil {
		return 0
	}
	return money.Paise(wallet.BalancePaise)
}

// ---------------------------------------------------------------------------
// GET /schedules/options
// ---------------------------------------------------------------------------

// ScheduleOptions tells the date picker which first dates it may offer.
func (a *API) ScheduleOptions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customerID, err := customerFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	now := time.Now()
	earliest := recurrence.EarliestStart(now, a.cutoffHour)
	balance := a.walletBalance(ctx, customerID)
	a.respond(ctx, w, http.StatusOK, map[string]any{
		"earliest_start":         isttime.FormatISODate(earliest),
		"earliest_start_display": isttime.FormatDate(earliest),
		"latest_start":           isttime.FormatISODate(recurrence.LatestStart(now)),
		"max_end":                isttime.FormatISODate(isttime.AddDays(earliest, maxScheduleSpan)),
		"charge_lead_minutes":    int(a.schedules.ChargeLead / time.Minute),
		"cutoff_hour_ist":        a.cutoffHour,
		"wallet_balance_paise":   balance.Int64(),
		"wallet_balance_display": money.FormatRupees(balance),
	})
}

// ---------------------------------------------------------------------------
// POST /schedules
// ---------------------------------------------------------------------------

type createScheduleRequest struct {
	AddressID  string `json:"address_id"`
	Frequency  string `json:"frequency"`
	Weekdays   []int  `json:"weekdays"`
	DayOfMonth int    `json:"day_of_month"`
	StartDate  string `json:"start_date"`
	EndDate    string `json:"end_date"`
}

// CreateSchedule turns the cart into a schedule and empties the cart. No money
// moves and no stock is held.
func (a *API) CreateSchedule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customerID, err := customerFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	var req createScheduleRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		a.fail(ctx, w, err)
		return
	}

	now := time.Now()
	rule, fieldErrs := a.parseRule(req, now)
	if len(fieldErrs) > 0 {
		a.fail(ctx, w, httpx.Validation("Please check the schedule.", fieldErrs))
		return
	}

	addressID, err := uuid.Parse(req.AddressID)
	if err != nil {
		a.fail(ctx, w, httpx.Validation("A delivery address is required.",
			map[string]any{"address_id": "must be a UUID"}))
		return
	}
	address, err := a.fetchAddress(ctx, customerID, addressID)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}

	cart, items, units, err := a.cartUnits(ctx, customerID)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	if len(items) == 0 {
		a.fail(ctx, w, httpx.Conflict("CART_EMPTY", "Your cart is empty."))
		return
	}

	next, ok := rule.NextOnOrAfter(rule.Start)
	if !ok {
		a.fail(ctx, w, httpx.Validation("That schedule has no delivery dates.",
			map[string]any{"end_date": "leaves no dates"}))
		return
	}

	addressJSON, _ := json.Marshal(address)
	weekdays := make([]int16, 0, len(rule.Weekdays))
	for _, day := range rule.Weekdays {
		weekdays = append(weekdays, int16(day))
	}
	var dayOfMonth *int16
	if rule.Frequency == recurrence.Monthly {
		day := int16(rule.DayOfMonth)
		dayOfMonth = &day
	}
	var endDate *time.Time
	if !rule.End.IsZero() {
		end := rule.End
		endDate = &end
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := a.queries.WithTx(tx)

	schedule, err := q.CreateSchedule(ctx, store.CreateScheduleParams{
		CustomerID:       customerID,
		AddressID:        addressID,
		AddressSnapshot:  addressJSON,
		Frequency:        string(rule.Frequency),
		Weekdays:         weekdays,
		DayOfMonth:       dayOfMonth,
		StartDate:        rule.Start,
		EndDate:          endDate,
		NextDeliveryDate: &next,
	})
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	// Merged by pack, because a schedule holds one line per pack.
	qtyByUnit := map[uuid.UUID]int32{}
	order := []uuid.UUID{}
	for _, item := range items {
		if _, seen := qtyByUnit[item.ProductUnitID]; !seen {
			order = append(order, item.ProductUnitID)
		}
		qtyByUnit[item.ProductUnitID] += item.Qty
	}
	for _, unitID := range order {
		unit, listed := units[unitID]
		if !listed {
			a.fail(ctx, w, httpx.Conflict("ITEM_UNAVAILABLE",
				"An item in your cart is no longer listed. Please review your cart."))
			return
		}
		qty := qtyByUnit[unitID]
		if qty > 99 {
			qty = 99
		}
		var sizeCode *string
		if grade := produce.GradeLabel(unit.SizeCode); grade != "" {
			sizeCode = &grade
		}
		if _, err := q.CreateScheduleItem(ctx, store.CreateScheduleItemParams{
			ScheduleID:          schedule.ID,
			ProductID:           unit.ProductID,
			ProductUnitID:       unit.ID,
			Qty:                 qty,
			ProductNameSnapshot: unit.ProductName,
			UnitLabelSnapshot:   unit.Label,
			SizeCodeSnapshot:    sizeCode,
		}); err != nil {
			a.fail(ctx, w, httpx.Internal(err))
			return
		}
	}
	if _, err := q.ClearCart(ctx, cart.ID); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	if err := tx.Commit(ctx); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	a.respondWithSchedule(ctx, w, customerID, schedule.ID, http.StatusCreated)
}

// parseRule validates the request against the schedule rules and returns
// field errors keyed as the form names them.
func (a *API) parseRule(req createScheduleRequest, now time.Time) (recurrence.Rule, map[string]any) {
	errs := map[string]any{}
	rule := recurrence.Rule{
		Frequency:  recurrence.Frequency(req.Frequency),
		Weekdays:   recurrence.NormaliseWeekdays(req.Weekdays),
		DayOfMonth: req.DayOfMonth,
	}

	start, err := isttime.ParseISODate(req.StartDate)
	if err != nil {
		errs["start_date"] = "must be a date, YYYY-MM-DD"
		return rule, errs
	}
	rule.Start = recurrence.Day(start)

	earliest := recurrence.EarliestStart(now, a.cutoffHour)
	latest := recurrence.LatestStart(now)
	if rule.Start.Before(earliest) {
		errs["start_date"] = "the earliest delivery you can schedule is " + isttime.FormatDate(earliest)
	} else if rule.Start.After(latest) {
		errs["start_date"] = "the first delivery must be within the next " +
			fmt.Sprint(recurrence.MaxLeadDays) + " days"
	}

	if strings.TrimSpace(req.EndDate) != "" {
		if rule.Frequency == recurrence.Once {
			errs["end_date"] = "a one-off delivery has no end date"
		} else if end, err := isttime.ParseISODate(req.EndDate); err != nil {
			errs["end_date"] = "must be a date, YYYY-MM-DD"
		} else {
			rule.End = recurrence.Day(end)
			if rule.End.After(isttime.AddDays(rule.Start, maxScheduleSpan)) {
				errs["end_date"] = "a repeat can run for up to a year"
			}
		}
	}

	if err := rule.Validate(); err != nil {
		switch rule.Frequency {
		case recurrence.Weekly:
			errs["weekdays"] = err.Error()
		case recurrence.Monthly:
			errs["day_of_month"] = err.Error()
		default:
			if _, taken := errs["end_date"]; !taken {
				errs["frequency"] = err.Error()
			}
		}
	}
	// Only the pattern the frequency uses is stored.
	if rule.Frequency != recurrence.Weekly {
		rule.Weekdays = nil
	}
	if rule.Frequency != recurrence.Monthly {
		rule.DayOfMonth = 0
	}
	return rule, errs
}

// ---------------------------------------------------------------------------
// GET /schedules, GET /schedules/{id}
// ---------------------------------------------------------------------------

// ListSchedules returns the customer's live schedules with estimates, and the
// wallet balance so the list can warn when it will not cover the next one.
func (a *API) ListSchedules(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customerID, err := customerFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	schedules, err := a.queries.ListSchedulesForCustomer(ctx, customerID)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	ids := make([]uuid.UUID, 0, len(schedules))
	for _, s := range schedules {
		ids = append(ids, s.ID)
	}
	allItems, err := a.queries.ListScheduleItemsForSchedules(ctx, ids)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	byID := map[uuid.UUID][]store.ScheduleItem{}
	for _, item := range allItems {
		byID[item.ScheduleID] = append(byID[item.ScheduleID], item)
	}

	units := a.todaysUnits(ctx)
	out := make([]scheduleResponse, 0, len(schedules))
	for _, s := range schedules {
		out = append(out, a.toScheduleResponse(s, byID[s.ID], units))
	}
	balance := a.walletBalance(ctx, customerID)
	a.respond(ctx, w, http.StatusOK, map[string]any{
		"schedules":              out,
		"wallet_balance_paise":   balance.Int64(),
		"wallet_balance_display": money.FormatRupees(balance),
	})
}

// GetSchedule returns one schedule with its upcoming dates and history.
func (a *API) GetSchedule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customerID, err := customerFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	scheduleID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		a.fail(ctx, w, httpx.NotFound("Schedule not found."))
		return
	}
	a.respondWithSchedule(ctx, w, customerID, scheduleID, http.StatusOK)
}

func (a *API) respondWithSchedule(
	ctx context.Context, w http.ResponseWriter, customerID, scheduleID uuid.UUID, status int,
) {
	schedule, err := a.queries.GetScheduleForCustomer(ctx, store.GetScheduleForCustomerParams{
		ID: scheduleID, CustomerID: customerID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			a.fail(ctx, w, httpx.NotFound("Schedule not found."))
			return
		}
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	items, err := a.queries.ListScheduleItems(ctx, schedule.ID)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	response := a.toScheduleResponse(schedule, items, a.todaysUnits(ctx))

	now := time.Now()
	if schedule.Status == scheduleActive || schedule.Status == schedulePaused {
		from := a.firstUnlockedDate(now)
		if schedule.NextDeliveryDate != nil && recurrence.Day(*schedule.NextDeliveryDate).Before(from) {
			from = recurrence.Day(*schedule.NextDeliveryDate)
		}
		skips, err := a.queries.ListFutureSkips(ctx, store.ListFutureSkipsParams{
			ScheduleID: schedule.ID, DeliveryDate: from,
		})
		if err != nil {
			a.fail(ctx, w, httpx.Internal(err))
			return
		}
		skipped := map[string]bool{}
		for _, day := range skips {
			skipped[isttime.FormatISODate(recurrence.Day(day))] = true
		}
		for _, day := range ruleOf(schedule).Upcoming(from, upcomingDates) {
			key := isttime.FormatISODate(day)
			response.Upcoming = append(response.Upcoming, upcomingDateResponse{
				Date:     key,
				Display:  day.Format("Mon, 02 Jan"),
				Skipped:  skipped[key],
				Locked:   recurrence.Locked(day, now, a.cutoffHour, a.schedules.ChargeLead),
				ChargeAt: recurrence.ChargeAt(day, a.cutoffHour, a.schedules.ChargeLead).Format(time.RFC3339),
			})
		}
	}

	history, err := a.queries.ListOccurrencesForSchedule(ctx, schedule.ID)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	for _, occurrence := range history {
		// A future skip is shown on the upcoming list, not as history.
		if occurrence.Status == occurrenceSkippedByCustomer &&
			!recurrence.Locked(occurrence.DeliveryDate, now, a.cutoffHour, a.schedules.ChargeLead) {
			continue
		}
		entry := occurrenceResponse{
			Date:        isttime.FormatISODate(occurrence.DeliveryDate),
			Display:     isttime.FormatDate(occurrence.DeliveryDate),
			Status:      occurrence.Status,
			OrderNumber: occurrence.OrderNumber,
			Note:        occurrence.Note,
		}
		if occurrence.OrderID != nil {
			id := occurrence.OrderID.String()
			entry.OrderID = &id
		}
		if occurrence.TotalPaise != nil {
			entry.Total = money.FormatRupees(money.Paise(*occurrence.TotalPaise))
		}
		response.History = append(response.History, entry)
	}

	a.respond(ctx, w, status, response)
}

// ---------------------------------------------------------------------------
// PATCH /schedules/{id}
// ---------------------------------------------------------------------------

type updateScheduleRequest struct {
	AddressID *string `json:"address_id"`
	Items     []struct {
		ProductUnitID string `json:"product_unit_id"`
		Qty           int32  `json:"qty"`
	} `json:"items"`
}

// UpdateSchedule changes quantities (0 removes a line) or the address. Adding
// produce is done by making a new schedule from the cart; changing the repeat
// pattern by cancelling and making a new one — both are rare, and a pattern
// edit that silently re-dated skips would be worse than asking.
//
// An edit made after a date's charging run has started affects the dates
// after it; the one being charged uses what it read.
func (a *API) UpdateSchedule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customerID, err := customerFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	schedule, ok := a.loadOwnSchedule(ctx, w, r, customerID)
	if !ok {
		return
	}
	if schedule.Status == scheduleCancelled || schedule.Status == scheduleCompleted {
		a.fail(ctx, w, httpx.Conflict("SCHEDULE_CLOSED", "This schedule has ended."))
		return
	}
	var req updateScheduleRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		a.fail(ctx, w, err)
		return
	}

	var address *addressSnapshot
	var addressID uuid.UUID
	if req.AddressID != nil {
		addressID, err = uuid.Parse(*req.AddressID)
		if err != nil {
			a.fail(ctx, w, httpx.Validation("Choose a delivery address.",
				map[string]any{"address_id": "must be a UUID"}))
			return
		}
		fetched, err := a.fetchAddress(ctx, customerID, addressID)
		if err != nil {
			a.fail(ctx, w, err)
			return
		}
		address = &fetched
	}

	tx, err := a.pool.Begin(ctx)
	if err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := a.queries.WithTx(tx)

	if _, err := q.LockSchedule(ctx, schedule.ID); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}

	if address != nil {
		addressJSON, _ := json.Marshal(address)
		if _, err := q.UpdateScheduleAddress(ctx, store.UpdateScheduleAddressParams{
			ID: schedule.ID, AddressID: addressID, AddressSnapshot: addressJSON,
		}); err != nil {
			a.fail(ctx, w, httpx.Internal(err))
			return
		}
	}

	if req.Items != nil {
		existing, err := q.ListScheduleItems(ctx, schedule.ID)
		if err != nil {
			a.fail(ctx, w, httpx.Internal(err))
			return
		}
		wanted := map[uuid.UUID]int32{}
		for _, item := range req.Items {
			unitID, err := uuid.Parse(item.ProductUnitID)
			if err != nil || item.Qty < 0 || item.Qty > 99 {
				a.fail(ctx, w, httpx.Validation("Quantities must be 0 to 99.",
					map[string]any{"items": "invalid line"}))
				return
			}
			wanted[unitID] = item.Qty
		}
		kept := make([]store.ScheduleItem, 0, len(existing))
		for _, item := range existing {
			if qty, named := wanted[item.ProductUnitID]; named {
				item.Qty = qty
			}
			if item.Qty > 0 {
				kept = append(kept, item)
			}
		}
		if len(kept) == 0 {
			a.fail(ctx, w, httpx.Validation(
				"A schedule needs at least one item. Cancel it instead to stop deliveries.",
				map[string]any{"items": "empty"}))
			return
		}
		if err := q.DeleteScheduleItems(ctx, schedule.ID); err != nil {
			a.fail(ctx, w, httpx.Internal(err))
			return
		}
		for _, item := range kept {
			if _, err := q.CreateScheduleItem(ctx, store.CreateScheduleItemParams{
				ScheduleID:          schedule.ID,
				ProductID:           item.ProductID,
				ProductUnitID:       item.ProductUnitID,
				Qty:                 item.Qty,
				ProductNameSnapshot: item.ProductNameSnapshot,
				UnitLabelSnapshot:   item.UnitLabelSnapshot,
				SizeCodeSnapshot:    item.SizeCodeSnapshot,
			}); err != nil {
				a.fail(ctx, w, httpx.Internal(err))
				return
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	a.respondWithSchedule(ctx, w, customerID, schedule.ID, http.StatusOK)
}

func (a *API) loadOwnSchedule(
	ctx context.Context, w http.ResponseWriter, r *http.Request, customerID uuid.UUID,
) (store.Schedule, bool) {
	scheduleID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		a.fail(ctx, w, httpx.NotFound("Schedule not found."))
		return store.Schedule{}, false
	}
	schedule, err := a.queries.GetScheduleForCustomer(ctx, store.GetScheduleForCustomerParams{
		ID: scheduleID, CustomerID: customerID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			a.fail(ctx, w, httpx.NotFound("Schedule not found."))
			return store.Schedule{}, false
		}
		a.fail(ctx, w, httpx.Internal(err))
		return store.Schedule{}, false
	}
	return schedule, true
}

// ---------------------------------------------------------------------------
// POST /schedules/{id}/pause | /resume | /cancel
// ---------------------------------------------------------------------------

// PauseSchedule stops charging until resumed. Dates pass while paused.
func (a *API) PauseSchedule(w http.ResponseWriter, r *http.Request) {
	a.changeScheduleStatus(w, r, schedulePaused)
}

// ResumeSchedule restarts from the first date whose charging run has not
// started, and clears the low-balance count.
func (a *API) ResumeSchedule(w http.ResponseWriter, r *http.Request) {
	a.changeScheduleStatus(w, r, scheduleActive)
}

// CancelSchedule ends a schedule for good. Orders already placed are not
// touched: they are ordinary orders, cancelled through support like any other.
func (a *API) CancelSchedule(w http.ResponseWriter, r *http.Request) {
	a.changeScheduleStatus(w, r, scheduleCancelled)
}

func (a *API) changeScheduleStatus(w http.ResponseWriter, r *http.Request, to string) {
	ctx := r.Context()
	customerID, err := customerFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	schedule, ok := a.loadOwnSchedule(ctx, w, r, customerID)
	if !ok {
		return
	}

	allowed := map[string][]string{
		schedulePaused:    {scheduleActive},
		scheduleActive:    {schedulePaused},
		scheduleCancelled: {scheduleActive, schedulePaused},
	}
	permitted := false
	for _, from := range allowed[to] {
		if schedule.Status == from {
			permitted = true
		}
	}
	if !permitted {
		a.fail(ctx, w, httpx.Conflict("SCHEDULE_STATE",
			fmt.Sprintf("This schedule is %s.", schedule.Status)))
		return
	}

	next := schedule.NextDeliveryDate
	skips := schedule.LowBalanceSkips
	status := to
	switch to {
	case scheduleActive:
		skips = 0
		first, found := ruleOf(schedule).NextOnOrAfter(a.firstUnlockedDate(time.Now()))
		if !found {
			status, next = scheduleCompleted, nil
		} else {
			next = &first
		}
	case scheduleCancelled:
		next = nil
	}

	if _, err := a.queries.SetScheduleStatus(ctx, store.SetScheduleStatusParams{
		ID: schedule.ID, Status: status, NextDeliveryDate: next, LowBalanceSkips: skips,
	}); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	if to == scheduleCancelled {
		a.respond(ctx, w, http.StatusOK, map[string]any{"id": schedule.ID.String(), "status": status})
		return
	}
	a.respondWithSchedule(ctx, w, customerID, schedule.ID, http.StatusOK)
}

// ---------------------------------------------------------------------------
// POST /schedules/{id}/skip, DELETE /schedules/{id}/skip/{date}
// ---------------------------------------------------------------------------

type skipRequest struct {
	DeliveryDate string `json:"delivery_date"`
}

// SkipDelivery skips one date. Allowed until that date's charging run starts.
func (a *API) SkipDelivery(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customerID, err := customerFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	schedule, ok := a.loadOwnSchedule(ctx, w, r, customerID)
	if !ok {
		return
	}
	var req skipRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		a.fail(ctx, w, err)
		return
	}
	day, ok := a.skippableDate(ctx, w, schedule, req.DeliveryDate)
	if !ok {
		return
	}

	if _, err := a.queries.CreateOccurrence(ctx, store.CreateOccurrenceParams{
		ScheduleID: schedule.ID, DeliveryDate: day, Status: occurrenceSkippedByCustomer,
	}); err != nil && !isUniqueViolation(err) {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	a.respondWithSchedule(ctx, w, customerID, schedule.ID, http.StatusOK)
}

// UnskipDelivery puts a skipped date back, on the same terms.
func (a *API) UnskipDelivery(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customerID, err := customerFromRequest(r)
	if err != nil {
		a.fail(ctx, w, err)
		return
	}
	schedule, ok := a.loadOwnSchedule(ctx, w, r, customerID)
	if !ok {
		return
	}
	day, ok := a.skippableDate(ctx, w, schedule, chi.URLParam(r, "date"))
	if !ok {
		return
	}
	if _, err := a.queries.DeleteCustomerSkip(ctx, store.DeleteCustomerSkipParams{
		ScheduleID: schedule.ID, DeliveryDate: day,
	}); err != nil {
		a.fail(ctx, w, httpx.Internal(err))
		return
	}
	a.respondWithSchedule(ctx, w, customerID, schedule.ID, http.StatusOK)
}

// skippableDate parses a date and checks it is one of the schedule's own
// dates whose charging run has not yet started.
func (a *API) skippableDate(
	ctx context.Context, w http.ResponseWriter, schedule store.Schedule, raw string,
) (time.Time, bool) {
	if schedule.Status != scheduleActive && schedule.Status != schedulePaused {
		a.fail(ctx, w, httpx.Conflict("SCHEDULE_CLOSED", "This schedule has ended."))
		return time.Time{}, false
	}
	parsed, err := isttime.ParseISODate(raw)
	if err != nil {
		a.fail(ctx, w, httpx.Validation("Choose a delivery date.",
			map[string]any{"delivery_date": "must be a date, YYYY-MM-DD"}))
		return time.Time{}, false
	}
	day := recurrence.Day(parsed)
	if match, found := ruleOf(schedule).NextOnOrAfter(day); !found || !match.Equal(day) {
		a.fail(ctx, w, httpx.Validation("That is not one of this schedule's delivery dates.",
			map[string]any{"delivery_date": "not a scheduled date"}))
		return time.Time{}, false
	}
	if recurrence.Locked(day, time.Now(), a.cutoffHour, a.schedules.ChargeLead) {
		a.fail(ctx, w, httpx.Conflict("DELIVERY_LOCKED",
			"That delivery is already being packed and can no longer be changed."))
		return time.Time{}, false
	}
	return day, true
}
