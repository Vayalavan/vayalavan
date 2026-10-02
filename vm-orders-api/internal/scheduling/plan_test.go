package scheduling

import (
	"testing"

	"github.com/google/uuid"

	"github.com/vayal-mikrogreenz/vm-orders-api/internal/catalogclient"
)

var (
	gradeL = uuid.MustParse("00000000-0000-0000-0000-00000000000a")
	gradeM = uuid.MustParse("00000000-0000-0000-0000-00000000000b")
	pack1L = uuid.MustParse("00000000-0000-0000-0000-000000000001") // 1 kg of L
	pack2L = uuid.MustParse("00000000-0000-0000-0000-000000000002") // 2 kg of L
	pack1M = uuid.MustParse("00000000-0000-0000-0000-000000000003") // 1 kg of M
	gone   = uuid.MustParse("00000000-0000-0000-0000-000000000009") // not listed today
)

func units() map[uuid.UUID]catalogclient.Unit {
	return map[uuid.UUID]catalogclient.Unit{
		pack1L: {ID: pack1L, SizeCodeID: gradeL, SizeCode: "L", ProductName: "Pomegranate", Label: "1 Kg Box", WeightGrams: 1000},
		pack2L: {ID: pack2L, SizeCodeID: gradeL, SizeCode: "L", ProductName: "Pomegranate", Label: "2 Kg Box", WeightGrams: 2000},
		pack1M: {ID: pack1M, SizeCodeID: gradeM, SizeCode: "M", ProductName: "Pomegranate", Label: "1 Kg Box", WeightGrams: 1000},
	}
}

func TestBuildFillsEverythingWhenStockAllows(t *testing.T) {
	remaining := map[uuid.UUID]int32{gradeL: 10_000, gradeM: 5_000}
	plan := Build([]Want{{UnitID: pack1L, Qty: 2}, {UnitID: pack1M, Qty: 3}}, units(), remaining)

	if len(plan.Lines) != 2 || len(plan.Short) != 0 {
		t.Fatalf("got %+v", plan)
	}
	if remaining[gradeL] != 8_000 || remaining[gradeM] != 2_000 {
		t.Fatalf("remaining not decremented: %+v", remaining)
	}
	if plan.Note() != "" {
		t.Fatalf("expected no note, got %q", plan.Note())
	}
}

func TestBuildReducesALineToWhatFits(t *testing.T) {
	remaining := map[uuid.UUID]int32{gradeL: 2_500}
	plan := Build([]Want{{UnitID: pack1L, Qty: 4}}, units(), remaining)

	if len(plan.Lines) != 1 || plan.Lines[0].Qty != 2 {
		t.Fatalf("want 2 of 4, got %+v", plan.Lines)
	}
	if remaining[gradeL] != 500 {
		t.Fatalf("remaining = %d, want 500", remaining[gradeL])
	}
	want := "Left out or reduced: Pomegranate (L) · 1 Kg Box — 2 of 4."
	if plan.Note() != want {
		t.Fatalf("note = %q, want %q", plan.Note(), want)
	}
}

// Two lines on one grade share its grams. The first listed is filled first,
// and the second gets what is left — never the same gram twice.
func TestBuildSharesAGradeAcrossLines(t *testing.T) {
	remaining := map[uuid.UUID]int32{gradeL: 3_000}
	plan := Build([]Want{{UnitID: pack2L, Qty: 1}, {UnitID: pack1L, Qty: 2}}, units(), remaining)

	if len(plan.Lines) != 2 || plan.Lines[0].Qty != 1 || plan.Lines[1].Qty != 1 {
		t.Fatalf("got %+v", plan.Lines)
	}
	if remaining[gradeL] != 0 {
		t.Fatalf("remaining = %d, want 0", remaining[gradeL])
	}
	grams := plan.GramsBySizeCode()
	if grams[gradeL] != 3_000 {
		t.Fatalf("reserve %d g of L, want 3000", grams[gradeL])
	}
}

// Selling out one grade leaves its sibling untouched (CLAUDE.md §5.2).
func TestBuildGradesDoNotShare(t *testing.T) {
	remaining := map[uuid.UUID]int32{gradeL: 0, gradeM: 4_000}
	plan := Build([]Want{{UnitID: pack1L, Qty: 1}, {UnitID: pack1M, Qty: 1}}, units(), remaining)

	if len(plan.Lines) != 1 || plan.Lines[0].Unit.ID != pack1M {
		t.Fatalf("only M should be filled, got %+v", plan.Lines)
	}
	if remaining[gradeM] != 3_000 {
		t.Fatalf("M remaining = %d", remaining[gradeM])
	}
}

func TestBuildDropsAPackNotListedToday(t *testing.T) {
	remaining := map[uuid.UUID]int32{gradeL: 5_000}
	plan := Build([]Want{
		{UnitID: gone, Qty: 1, Name: "Alphonso Mango · 10 Kg box"},
		{UnitID: pack1L, Qty: 1},
	}, units(), remaining)

	if len(plan.Lines) != 1 {
		t.Fatalf("got %+v", plan.Lines)
	}
	want := "Left out or reduced: Alphonso Mango · 10 Kg box — not available."
	if plan.Note() != want {
		t.Fatalf("note = %q", plan.Note())
	}
}

// Nothing in stock is the one case the date is skipped rather than partly
// delivered.
func TestBuildEmptyWhenNothingFits(t *testing.T) {
	remaining := map[uuid.UUID]int32{gradeL: 999}
	plan := Build([]Want{{UnitID: pack1L, Qty: 1}, {UnitID: gone, Qty: 2, Name: "x"}}, units(), remaining)
	if !plan.Empty() {
		t.Fatalf("expected empty plan, got %+v", plan.Lines)
	}
	if remaining[gradeL] != 999 {
		t.Fatalf("an empty plan must not consume grams: %d", remaining[gradeL])
	}
}
