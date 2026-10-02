// Package grams formats a weight the way a customer would say it.
//
// Availability is tracked in grams at the product level (CLAUDE.md §5.2), but
// nobody buying vegetables says "10000 g". Two services now quote a weight
// back to the same customer — vm-catalog-api when a reservation cannot be
// granted, vm-orders-api when a cart asks for more than is left — and they
// have to phrase it identically, or the cart and the checkout error appear to
// disagree about how much is there.
package grams

import "fmt"

// Format renders a gram count as kilos above a kilo and grams below, with no
// trailing ".0": "750 g", "5 kg", "1.5 kg".
//
// Integer arithmetic, half-up at one decimal. Nothing here is money, but this
// codebase has one rule about how quantities are divided (CLAUDE.md rule 1),
// and a second style for "only a label" is how the first one erodes.
func Format(g int32) string {
	if g < 0 {
		g = 0
	}
	if g < 1000 {
		return fmt.Sprintf("%d g", g)
	}

	kg := g / 1000
	tenths := (g%1000 + 50) / 100
	if tenths == 10 {
		kg++
		tenths = 0
	}
	if tenths == 0 {
		return fmt.Sprintf("%d kg", kg)
	}
	return fmt.Sprintf("%d.%d kg", kg, tenths)
}
