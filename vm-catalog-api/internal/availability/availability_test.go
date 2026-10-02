package availability

import (
	"errors"
	"testing"
	"time"

	"github.com/vayal-mikrogreenz/vm-go-common/isttime"
)

// ist builds an instant from IST wall-clock components.
func ist(year int, month time.Month, day, hour, min, sec int) time.Time {
	return time.Date(year, month, day, hour, min, sec, 0, isttime.Location())
}

// utc builds an instant from UTC components, to prove conversion happens.
func utc(year int, month time.Month, day, hour, min, sec int) time.Time {
	return time.Date(year, month, day, hour, min, sec, 0, time.UTC)
}

// date is a business day (midnight IST).
func date(year int, month time.Month, day int) time.Time {
	return ist(year, month, day, 0, 0, 0)
}

func openSheet(on time.Time, total, reserved, sold int32) Sheet {
	return Sheet{
		AvailableOn:   on,
		TotalGrams:    total,
		ReservedGrams: reserved,
		SoldGrams:     sold,
		Status:        StatusOpen,
	}
}

// TestSellableAcrossTheDayBoundary is the requirement in full: purchasability
// flips at midnight IST, not at midnight UTC and not at midnight wherever the
// server runs.
func TestSellableAcrossTheDayBoundary(t *testing.T) {
	// Stock declared for 15 June.
	sheet := openSheet(date(2026, time.June, 15), 40000, 0, 0)

	tests := []struct {
		name string
		now  time.Time
		want bool
	}{
		{"one second into the day", ist(2026, time.June, 15, 0, 0, 1), true},
		{"midday", ist(2026, time.June, 15, 12, 0, 0), true},
		{"one second before midnight", ist(2026, time.June, 15, 23, 59, 59), true},
		// The flip. Same stock, one second later, no longer sellable.
		{"midnight exactly is the NEXT day", ist(2026, time.June, 16, 0, 0, 0), false},
		{"one second after midnight", ist(2026, time.June, 16, 0, 0, 1), false},
		{"the day before", ist(2026, time.June, 14, 23, 59, 59), false},

		// 18:29:59 UTC is 23:59:59 IST on the 15th — still sellable.
		// 18:30:00 UTC is 00:00:00 IST on the 16th — no longer sellable.
		// A naive UTC-based comparison gets both of these wrong.
		{"UTC instant still inside the IST day", utc(2026, time.June, 15, 18, 29, 59), true},
		{"UTC instant just past the IST day", utc(2026, time.June, 15, 18, 30, 0), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sheet.SellableOn(BusinessDay(tc.now))
			if got != tc.want {
				t.Errorf("SellableOn(%s) = %v, want %v",
					tc.now.In(isttime.Location()).Format(time.RFC3339), got, tc.want)
			}
		})
	}
}

// TestYesterdaysStockIsNotSellableToday — availability does not carry over
// (CLAUDE.md §5.2). A supplier who forgets to declare has nothing on sale,
// rather than silently reselling yesterday's numbers.
func TestYesterdaysStockIsNotSellableToday(t *testing.T) {
	today := date(2026, time.June, 15)
	yesterday := date(2026, time.June, 14)
	tomorrow := date(2026, time.June, 16)

	// Plenty of stock, wide open — but declared for the wrong day.
	stale := openSheet(yesterday, 100000, 0, 0)
	if stale.SellableOn(today) {
		t.Error("yesterday's declaration is sellable today — availability must not carry over")
	}
	if stale.RemainingGrams() != 100000 {
		t.Errorf("RemainingGrams = %d, want 100000 (the grams exist; the DAY is wrong)",
			stale.RemainingGrams())
	}

	// Tomorrow's declaration is not sellable early either.
	future := openSheet(tomorrow, 100000, 0, 0)
	if future.SellableOn(today) {
		t.Error("tomorrow's declaration is sellable today")
	}

	// Today's is.
	if !openSheet(today, 100000, 0, 0).SellableOn(today) {
		t.Error("today's declaration is not sellable today")
	}
}

func TestSellableRequiresOpenAndRemaining(t *testing.T) {
	today := date(2026, time.June, 15)

	tests := []struct {
		name  string
		sheet Sheet
		want  bool
	}{
		{"open with stock", openSheet(today, 10000, 0, 0), true},
		{"open, partly reserved", openSheet(today, 10000, 4000, 0), true},
		{"open, partly sold", openSheet(today, 10000, 0, 4000), true},
		{"open, one gram left", openSheet(today, 10000, 5000, 4999), true},

		{"fully reserved", openSheet(today, 10000, 10000, 0), false},
		{"fully sold", openSheet(today, 10000, 0, 10000), false},
		{"reserved plus sold exhausts it", openSheet(today, 10000, 6000, 4000), false},
		{"nothing declared", openSheet(today, 0, 0, 0), false},
		{
			name: "closed early by the supplier",
			sheet: Sheet{
				AvailableOn: today, TotalGrams: 10000, Status: StatusClosed,
			},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.sheet.SellableOn(today); got != tc.want {
				t.Errorf("SellableOn = %v, want %v (remaining %d, status %q)",
					got, tc.want, tc.sheet.RemainingGrams(), tc.sheet.Status)
			}
		})
	}
}

func TestRemainingGramsNeverGoesNegative(t *testing.T) {
	// Should be impossible — the DB CHECK forbids it — but if a row ever did
	// go negative, it must read as sold out, not wrap into "purchasable".
	broken := openSheet(date(2026, time.June, 15), 1000, 800, 500)
	if got := broken.RemainingGrams(); got != 0 {
		t.Errorf("RemainingGrams = %d, want 0 for an over-committed row", got)
	}
	if broken.SellableOn(date(2026, time.June, 15)) {
		t.Error("an over-committed row reported itself sellable")
	}
}

// TestUnitPurchasable — a product can have stock while a large pack cannot be
// filled from it.
func TestUnitPurchasable(t *testing.T) {
	tests := []struct {
		name      string
		remaining int32
		unit      int32
		want      bool
	}{
		{"exactly enough", 3000, 3000, true},
		{"more than enough", 40000, 3000, true},
		{"one gram short", 2999, 3000, false},
		{"nothing left", 0, 1000, false},
		// The case the per-unit flag exists for: 3 kg left, so the 1 kg pack
		// sells and the 5 kg pack does not.
		{"small pack fits", 3000, 1000, true},
		{"large pack does not", 3000, 5000, false},
		{"zero-weight unit is never purchasable", 10000, 0, false},
		{"negative weight is never purchasable", 10000, -5, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := UnitPurchasable(tc.remaining, tc.unit); got != tc.want {
				t.Errorf("UnitPurchasable(%d, %d) = %v, want %v",
					tc.remaining, tc.unit, got, tc.want)
			}
		})
	}
}

// TestValidateNewTotal is the "reducing below committed stock is rejected"
// requirement.
func TestValidateNewTotal(t *testing.T) {
	today := date(2026, time.June, 15)

	tests := []struct {
		name      string
		sheet     Sheet
		requested int32
		wantErr   bool
	}{
		{"increase is always fine", openSheet(today, 10000, 3000, 2000), 50000, false},
		{"no change", openSheet(today, 10000, 3000, 2000), 10000, false},
		{"reduce, still above committed", openSheet(today, 10000, 3000, 2000), 6000, false},
		{"reduce to exactly committed", openSheet(today, 10000, 3000, 2000), 5000, false},

		// One gram below what customers hold.
		{"one gram below committed", openSheet(today, 10000, 3000, 2000), 4999, true},
		{"reduce to zero with stock committed", openSheet(today, 10000, 3000, 2000), 0, true},
		{"reduce below sold alone", openSheet(today, 10000, 0, 8000), 7999, true},
		{"reduce below reserved alone", openSheet(today, 10000, 8000, 0), 7999, true},

		{"zero is fine when nothing is committed", openSheet(today, 10000, 0, 0), 0, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateNewTotal(tc.sheet, tc.requested)

			if !tc.wantErr {
				if err != nil {
					t.Fatalf("ValidateNewTotal(%d) = %v, want nil", tc.requested, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateNewTotal(%d) = nil, want a ReductionError", tc.requested)
			}

			var reduction ReductionError
			if !errors.As(err, &reduction) {
				t.Fatalf("error is %T, want ReductionError", err)
			}
			// The supplier needs to be told the floor, not just "invalid".
			if reduction.CommittedGrams != tc.sheet.CommittedGrams() {
				t.Errorf("CommittedGrams = %d, want %d",
					reduction.CommittedGrams, tc.sheet.CommittedGrams())
			}
		})
	}
}

// TestStockHintNeverLeaksNumbers — the hint is a nudge, not a stock readout.
func TestStockHintNeverLeaksNumbers(t *testing.T) {
	tests := []struct {
		name      string
		remaining int32
		smallest  int32
		want      string
	}{
		{"sold out says nothing", 0, 1000, HintNone},
		{"exactly one pack left", 1000, 1000, HintFew},
		{"three packs left", 3000, 1000, HintFew},
		{"four packs left is not few", 4000, 1000, HintNone},
		{"plenty", 40000, 1000, HintNone},
		// Scaled to pack size: 2 kg is nearly gone for a 1 kg pack, and
		// plentiful for a 100 g one.
		{"2kg with a 1kg pack is few", 2000, 1000, HintFew},
		{"2kg with a 100g pack is plenty", 2000, 100, HintNone},
		{"unknown pack size says nothing", 5000, 0, HintNone},
		// Grams remain, but not enough for even the smallest pack: the card is
		// showing as sold out, so nudging at it would be a false invitation.
		{"less than one pack says nothing", 999, 1000, HintNone},
		{"a fifth of the only pack says nothing", 1000, 5000, HintNone},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := StockHint(tc.remaining, tc.smallest)
			if got != tc.want {
				t.Errorf("StockHint(%d, %d) = %q, want %q",
					tc.remaining, tc.smallest, got, tc.want)
			}
			// Whatever it returns must never contain the actual number.
			if got != HintNone && got != HintFew {
				t.Errorf("unexpected hint %q — hints must be a fixed coarse set", got)
			}
		})
	}
}

// TestBusinessDayIsAlwaysIST guards requirement 4: the day is computed
// server-side in IST, whatever zone the instant arrives in.
func TestBusinessDayIsAlwaysIST(t *testing.T) {
	// One instant, three representations.
	instant := ist(2026, time.June, 15, 9, 0, 0)
	pacific := time.FixedZone("PDT", -7*60*60)

	want := date(2026, time.June, 15)
	for name, variant := range map[string]time.Time{
		"IST":     instant,
		"UTC":     instant.In(time.UTC),
		"Pacific": instant.In(pacific),
	} {
		t.Run(name, func(t *testing.T) {
			if got := BusinessDay(variant); !got.Equal(want) {
				t.Errorf("BusinessDay from %s = %s, want %s",
					name, got.Format(time.RFC3339), want.Format(time.RFC3339))
			}
		})
	}

	// 20:00 UTC on the 15th is 01:30 IST on the 16th — a different business day.
	lateUTC := utc(2026, time.June, 15, 20, 0, 0)
	if got := BusinessDay(lateUTC); !got.Equal(date(2026, time.June, 16)) {
		t.Errorf("BusinessDay(20:00 UTC 15 Jun) = %s, want 16 Jun IST",
			got.Format(time.RFC3339))
	}
}

// TestSellableGrams pins the rule the supplier screen got wrong: a closed
// product has stock on the books but none a customer can buy.
func TestSellableGrams(t *testing.T) {
	tests := []struct {
		name         string
		sheet        Sheet
		wantSellable int32
		// RemainingGrams must stay status-blind, so reopening restores the
		// declaration intact rather than resurrecting a zero.
		wantRemaining int32
	}{
		{
			name:          "open with stock",
			sheet:         Sheet{TotalGrams: 50000, Status: StatusOpen},
			wantSellable:  50000,
			wantRemaining: 50000,
		},
		{
			name:          "closed with stock sells nothing",
			sheet:         Sheet{TotalGrams: 50000, Status: StatusClosed},
			wantSellable:  0,
			wantRemaining: 50000,
		},
		{
			name:          "closed after partial sales still sells nothing",
			sheet:         Sheet{TotalGrams: 50000, SoldGrams: 25000, Status: StatusClosed},
			wantSellable:  0,
			wantRemaining: 25000,
		},
		{
			name:          "open but fully sold",
			sheet:         Sheet{TotalGrams: 50000, SoldGrams: 50000, Status: StatusOpen},
			wantSellable:  0,
			wantRemaining: 0,
		},
		{
			name:          "open with grams held by reservations",
			sheet:         Sheet{TotalGrams: 50000, ReservedGrams: 20000, Status: StatusOpen},
			wantSellable:  30000,
			wantRemaining: 30000,
		},
		{
			name:          "closed and oversold clamps at zero, never negative",
			sheet:         Sheet{TotalGrams: 1000, SoldGrams: 5000, Status: StatusClosed},
			wantSellable:  0,
			wantRemaining: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.sheet.SellableGrams(); got != tc.wantSellable {
				t.Errorf("SellableGrams() = %d, want %d", got, tc.wantSellable)
			}
			if got := tc.sheet.RemainingGrams(); got != tc.wantRemaining {
				t.Errorf("RemainingGrams() = %d, want %d", got, tc.wantRemaining)
			}
		})
	}
}

// TestClosedIsNeverSellable ties the two together: whenever SellableOn says no
// because of status, SellableGrams must agree by reporting zero. They are read
// by different callers (customer catalog vs supplier sheet) and must not drift.
func TestClosedIsNeverSellable(t *testing.T) {
	day := Today()
	closed := Sheet{
		AvailableOn: day, TotalGrams: 50000, Status: StatusClosed,
	}
	if closed.SellableOn(day) {
		t.Error("a closed sheet reported itself sellable")
	}
	if got := closed.SellableGrams(); got != 0 {
		t.Errorf("SellableGrams() = %d for a closed sheet, want 0", got)
	}
}
