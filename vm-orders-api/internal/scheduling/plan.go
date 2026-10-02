// Package scheduling decides what a scheduled delivery can actually contain on
// the day it is charged — CLAUDE.md §6.7.
//
// Stock is declared fresh each morning and never carries over (§5.2), so a
// schedule made a week ago names produce that may not be listed today, or not
// in the quantity asked for. The rule the business chose is DELIVER WHAT IS
// THERE: every line is filled as far as today's grams allow, lines with
// nothing left are dropped, and the customer is told exactly which. Only when
// nothing at all can be filled is the date skipped.
//
// Pure: no I/O. The charging run fetches the catalogue once, and this is the
// part worth testing to the gram.
package scheduling

import (
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/vayal-mikrogreenz/vm-go-common/produce"

	"github.com/vayal-mikrogreenz/vm-orders-api/internal/catalogclient"
)

// Want is one line of a schedule: a pack, and how many of it.
type Want struct {
	UnitID uuid.UUID
	Qty    int32
	// The name the customer saw when they scheduled it, for the note when the
	// pack is not in today's catalogue at all.
	Name string
}

// Line is a line the delivery WILL contain, at today's price.
type Line struct {
	Unit   catalogclient.Unit
	Qty    int32
	Wanted int32
}

// Shortfall is a line that came up short, for the customer's note.
type Shortfall struct {
	Name   string
	Wanted int32
	Got    int32
}

// Plan is the delivery as it can be made today.
type Plan struct {
	Lines []Line
	Short []Shortfall
}

// Empty reports that nothing could be filled — the date is skipped.
func (p Plan) Empty() bool { return len(p.Lines) == 0 }

// GramsBySizeCode is what to reserve: grams per grade, the unit catalog locks.
func (p Plan) GramsBySizeCode() map[uuid.UUID]int32 {
	out := map[uuid.UUID]int32{}
	for _, line := range p.Lines {
		out[line.Unit.SizeCodeID] += line.Unit.WeightGrams * line.Qty
	}
	return out
}

// Build fills each want against remaining, the grams per size code still
// unallocated today. It DECREMENTS remaining for what it takes, so a run
// planning several schedules against one catalogue fetch cannot promise the
// same crate twice. The catalogue's reservation is still the authority; this
// keeps the plan honest enough that the reservation almost always agrees.
//
// Lines are filled in the order the customer listed them.
func Build(wants []Want, units map[uuid.UUID]catalogclient.Unit, remaining map[uuid.UUID]int32) Plan {
	var plan Plan
	for _, want := range wants {
		unit, listed := units[want.UnitID]
		if !listed || unit.WeightGrams <= 0 || want.Qty <= 0 {
			plan.Short = append(plan.Short, Shortfall{Name: want.Name, Wanted: want.Qty})
			continue
		}

		fits := remaining[unit.SizeCodeID] / unit.WeightGrams
		qty := want.Qty
		if fits < qty {
			qty = fits
		}
		if qty < 0 {
			qty = 0
		}

		name := displayName(unit)
		if qty < want.Qty {
			plan.Short = append(plan.Short, Shortfall{Name: name, Wanted: want.Qty, Got: qty})
		}
		if qty == 0 {
			continue
		}
		remaining[unit.SizeCodeID] -= unit.WeightGrams * qty
		plan.Lines = append(plan.Lines, Line{Unit: unit, Qty: qty, Wanted: want.Qty})
	}
	return plan
}

// Note is the sentence the customer reads about what was left out. Empty when
// everything was filled.
func (p Plan) Note() string {
	if len(p.Short) == 0 {
		return ""
	}
	parts := make([]string, 0, len(p.Short))
	for _, short := range p.Short {
		if short.Got == 0 {
			parts = append(parts, fmt.Sprintf("%s — not available", short.Name))
		} else {
			parts = append(parts, fmt.Sprintf("%s — %d of %d", short.Name, short.Got, short.Wanted))
		}
	}
	return "Left out or reduced: " + strings.Join(parts, "; ") + "."
}

// displayName is how a line is named everywhere (CLAUDE.md §5.3): the produce
// with its grade when there is a real one, then the pack.
func displayName(unit catalogclient.Unit) string {
	return produce.GradedName(unit.ProductName, unit.SizeCode) + " · " + unit.Label
}
