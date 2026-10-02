package mail

import (
	"fmt"
	"strings"
	"time"

	"github.com/vayal-mikrogreenz/vm-go-common/isttime"
	"github.com/vayal-mikrogreenz/vm-go-common/money"
	"github.com/vayal-mikrogreenz/vm-go-common/produce"
)

// OrderLine is one item on the confirmation.
type OrderLine struct {
	ProductName string
	UnitLabel   string
	Grade       string
	// SizeCode is the GRADE the pack was bought at — "XL2". Empty on an
	// ungraded listing. Without it a receipt for two grades of one produce has
	// two identical-looking lines at different prices, which is the first
	// thing a customer would write in about.
	SizeCode  string
	Qty       int32
	LineTotal money.Paise
}

// Milestone is one step of the three-step timeline (CLAUDE.md §6.1).
type Milestone struct {
	Name string
	When string
	Done bool
}

// OrderConfirmation is everything the email needs.
type OrderConfirmation struct {
	CustomerName string
	OrderNumber  string
	PlacedAt     time.Time

	Lines       []OrderLine
	Subtotal    money.Paise
	PlatformFee money.Paise
	DeliveryFee money.Paise
	Total       money.Paise

	Milestones           []Milestone
	ExpectedDeliveryDate time.Time

	DeliveryAddress []string
	SupportEmail    string
}

// BuildOrderConfirmation renders the confirmation email.
//
// Plain text rather than HTML: it renders identically in every client, cannot
// be mangled by a Gmail clipper, and is what actually reaches an Indian
// customer reading on a mid-range phone. The content is fixed by CLAUDE.md
// §6.1 — the three milestones, the expected delivery date, the
// no-live-tracking note and the support address are all required.
func BuildOrderConfirmation(c OrderConfirmation) Message {
	var b strings.Builder

	greeting := "Hello"
	if c.CustomerName != "" {
		greeting = "Hello " + c.CustomerName
	}
	fmt.Fprintf(&b, "%s,\n\n", greeting)
	fmt.Fprintf(&b, "Thank you for your order. We've received your payment and your\n")
	fmt.Fprintf(&b, "produce is being prepared.\n\n")
	fmt.Fprintf(&b, "Order %s\n", c.OrderNumber)
	fmt.Fprintf(&b, "Placed %s\n\n", c.PlacedAt.In(isttime.Location()).Format("02 Jan 2006, 3:04 PM"))

	// --- items ---------------------------------------------------------------
	b.WriteString(divider)
	b.WriteString("YOUR ORDER\n")
	b.WriteString(divider)
	for _, line := range c.Lines {
		// The size code first, because it is what distinguishes two lines of
		// the same produce; the product's grade after it, as before.
		name := produce.GradedName(line.ProductName, line.SizeCode)
		if line.Grade != "" {
			name += " (Grade " + line.Grade + ")"
		}
		// Left-aligned name, right-aligned amount, so the column of figures
		// reads cleanly in a monospace mail client.
		left := fmt.Sprintf("%s x%d - %s", name, line.Qty, line.UnitLabel)
		fmt.Fprintf(&b, "%s\n", padRight(left, money.FormatRupees(line.LineTotal)))
	}

	// --- money ---------------------------------------------------------------
	b.WriteString("\n")
	b.WriteString(divider)
	b.WriteString("PAYMENT\n")
	b.WriteString(divider)
	fmt.Fprintf(&b, "%s\n", padRight("Subtotal", money.FormatRupees(c.Subtotal)))
	// Named exactly as the checkout screen names it, so the receipt and the
	// page a customer just saw agree (CLAUDE.md §6.2).
	fmt.Fprintf(&b, "%s\n", padRight("Platform fee (3%)", money.FormatRupees(c.PlatformFee)))
	fmt.Fprintf(&b, "%s\n", padRight("Delivery charge", money.FormatRupees(c.DeliveryFee)))
	fmt.Fprintf(&b, "%s\n", padRight("TOTAL PAID", money.FormatRupees(c.Total)))

	// --- timeline ------------------------------------------------------------
	b.WriteString("\n")
	b.WriteString(divider)
	b.WriteString("WHAT HAPPENS NEXT\n")
	b.WriteString(divider)
	for _, milestone := range c.Milestones {
		marker := "[ ]"
		if milestone.Done {
			marker = "[x]"
		}
		fmt.Fprintf(&b, "%s %s\n    %s\n", marker, milestone.Name, milestone.When)
	}

	fmt.Fprintf(&b, "\nExpected delivery: %s\n\n",
		isttime.FormatDate(c.ExpectedDeliveryDate))

	// Required copy, verbatim from CLAUDE.md §6.1. Setting the expectation in
	// the confirmation is what stops a "where is my parcel" email on day two.
	b.WriteString(
		"We hand your order to professional courier partners after processing,\n" +
			"so live tracking isn't available. Dates shown are estimates.\n\n")

	// --- address -------------------------------------------------------------
	if len(c.DeliveryAddress) > 0 {
		b.WriteString(divider)
		b.WriteString("DELIVERING TO\n")
		b.WriteString(divider)
		for _, line := range c.DeliveryAddress {
			if strings.TrimSpace(line) != "" {
				fmt.Fprintf(&b, "%s\n", line)
			}
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "Any issues? Write to us at %s\n\n", c.SupportEmail)
	b.WriteString("— Vayalavan\n")

	return Message{
		To: "", // set by the dispatcher, which knows the recipient
		// The order number in the subject makes the customer's own inbox
		// search work when they contact support.
		Subject: fmt.Sprintf("Order %s confirmed — Vayalavan", c.OrderNumber),
		Body:    b.String(),
	}
}

const divider = "------------------------------------------------------------\n"

// lineWidth matches the divider, so amounts align against it.
const lineWidth = 60

// padRight right-aligns `right` against lineWidth, truncating a long label
// rather than wrapping it into a ragged second line.
func padRight(left, right string) string {
	maxLeft := lineWidth - len([]rune(right)) - 1
	runes := []rune(left)
	if len(runes) > maxLeft && maxLeft > 3 {
		left = string(runes[:maxLeft-3]) + "..."
	}
	gap := lineWidth - len([]rune(left)) - len([]rune(right))
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}
