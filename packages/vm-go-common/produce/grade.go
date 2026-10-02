// Package produce holds the naming rules a line of produce is described by.
//
// A supplier grades produce into separate crates at separate prices
// (CLAUDE.md §5.2), so "Pomegranate · 1 Kg Box" is not a complete description
// of anything: two lines of an order, a cart or a courier sheet can read
// identically and be different goods. Every surface that names a line has to
// say which grade, and every surface has to hide the same two cases — which is
// why the rule lives here rather than as a copy per service.
//
// The web UIs apply the same rule in @vayal/ui-kit's `gradedName`, and each
// mobile app carries its own copy with a test asserting it agrees.
package produce

import "strings"

// DefaultSizeCode is the implicit grade of an ungraded listing
// (vm-catalog-api migration 00009).
//
// Never shown to a customer: a grower who does not grade has exactly one code,
// and printing it offers a choice of one. It should not normally be
// snapshotted onto an order either — CLAUDE.md §5.3 leaves the size code NULL
// on an ungraded line rather than inventing a grade the customer never chose —
// but the check costs nothing and covers rows written before that rule.
const DefaultSizeCode = "STD"

// GradeLabel returns the grade worth showing, or "" when there is none.
//
// Empty, whitespace and the implicit default all come back empty, so a caller
// can branch on the result instead of repeating the rule.
func GradeLabel(sizeCode string) string {
	trimmed := strings.TrimSpace(sizeCode)
	if trimmed == "" || strings.EqualFold(trimmed, DefaultSizeCode) {
		return ""
	}
	return trimmed
}

// GradedName is the produce plus its grade: "Pomegranate (XL2)", or
// "Pomegranate" when the listing is ungraded.
//
// For places with one string to work with — an alert, a subject line, a
// spreadsheet cell. Where two elements fit, prefer the name with GradeLabel
// beside it.
func GradedName(productName, sizeCode string) string {
	grade := GradeLabel(sizeCode)
	if grade == "" {
		return productName
	}
	return productName + " (" + grade + ")"
}
