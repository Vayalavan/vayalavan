package mail

import (
	"fmt"
	"strings"
	"time"

	"github.com/vayal-mikrogreenz/vm-go-common/isttime"
	"github.com/vayal-mikrogreenz/vm-go-common/money"
)

// Reasons a scheduled delivery was not made in full — CLAUDE.md §6.7.
const (
	ScheduleNoStock    = "no_stock"
	ScheduleLowBalance = "low_balance"
	SchedulePartial    = "partial"
)

// ScheduleNotice tells a customer what happened to one scheduled delivery
// when it was not simply delivered in full.
type ScheduleNotice struct {
	CustomerName string
	DeliveryDate time.Time
	Reason       string
	// Note lists the lines left out or reduced. Partial and no-stock only.
	Note string
	// What the delivery would have cost, and what the wallet held.
	// Low-balance only.
	Needed  money.Paise
	Balance money.Paise
	// Paused is set when this skip paused the schedule.
	Paused bool
	// OrderNumber of the partial delivery that WAS placed.
	OrderNumber  string
	SupportEmail string
}

// BuildScheduleNotice renders the notice as plain text, like every
// transactional email here.
func BuildScheduleNotice(n ScheduleNotice) Message {
	var b strings.Builder
	date := isttime.FormatDate(n.DeliveryDate)

	greeting := "Hello"
	if n.CustomerName != "" {
		greeting = "Hello " + n.CustomerName
	}
	fmt.Fprintf(&b, "%s,\n\n", greeting)

	var subject string
	switch n.Reason {
	case ScheduleLowBalance:
		subject = "Your scheduled delivery for " + date + " was skipped"
		fmt.Fprintf(&b, "We skipped your scheduled delivery for %s because your\n", date)
		fmt.Fprintf(&b, "Vayalavan wallet did not have enough in it.\n\n")
		fmt.Fprintf(&b, "%s\n", padRight("This delivery needed", money.FormatRupees(n.Needed)))
		fmt.Fprintf(&b, "%s\n\n", padRight("Your wallet held", money.FormatRupees(n.Balance)))
		fmt.Fprintf(&b, "Nothing was charged. Add money to your wallet before the next\n")
		fmt.Fprintf(&b, "delivery's cutoff and it will go ahead as planned.\n\n")
		if n.Paused {
			subject = "Your repeat order is paused — please top up your wallet"
			fmt.Fprintf(&b, "This is the third delivery in a row skipped for the same\n")
			fmt.Fprintf(&b, "reason, so we have PAUSED the schedule. Top up and resume it\n")
			fmt.Fprintf(&b, "from Scheduled orders whenever you are ready.\n\n")
		}
	case ScheduleNoStock:
		subject = "Your scheduled delivery for " + date + " was skipped"
		fmt.Fprintf(&b, "We skipped your scheduled delivery for %s because none of\n", date)
		fmt.Fprintf(&b, "its produce was in stock when it came to be packed.\n\n")
		if n.Note != "" {
			fmt.Fprintf(&b, "%s\n\n", n.Note)
		}
		fmt.Fprintf(&b, "Nothing was charged, and your next delivery is unaffected.\n\n")
	case SchedulePartial:
		subject = "Part of your delivery for " + date + " was not in stock"
		fmt.Fprintf(&b, "Your scheduled delivery for %s is on its way", date)
		if n.OrderNumber != "" {
			fmt.Fprintf(&b, " as order %s", n.OrderNumber)
		}
		fmt.Fprintf(&b, ",\nbut not everything on it was in stock today.\n\n")
		fmt.Fprintf(&b, "%s\n\n", n.Note)
		fmt.Fprintf(&b, "You were charged only for what we are sending — the order\n")
		fmt.Fprintf(&b, "confirmation lists exactly what that is.\n\n")
	default:
		subject = "About your scheduled delivery for " + date
	}

	fmt.Fprintf(&b, "Any issues? Write to us at %s\n", n.SupportEmail)

	return Message{Subject: subject, Body: b.String()}
}
