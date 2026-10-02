package mail

import (
	"strings"
	"testing"
	"time"

	"github.com/vayal-mikrogreenz/vm-go-common/isttime"
	"github.com/vayal-mikrogreenz/vm-go-common/money"
)

func TestScheduleNoticeLowBalanceStatesBothFigures(t *testing.T) {
	msg := BuildScheduleNotice(ScheduleNotice{
		CustomerName: "Priya",
		DeliveryDate: time.Date(2026, 10, 5, 0, 0, 0, 0, isttime.Location()),
		Reason:       ScheduleLowBalance,
		Needed:       money.Paise(105_000),
		Balance:      money.Paise(20_000),
		SupportEmail: "help@example.com",
	})
	for _, want := range []string{"05 Oct 2026", "₹1,050.00", "₹200.00", "Nothing was charged", "help@example.com"} {
		if !strings.Contains(msg.Body, want) && !strings.Contains(msg.Subject, want) {
			t.Errorf("notice missing %q:\n%s", want, msg.Body)
		}
	}
	if strings.Contains(msg.Body, "PAUSED") {
		t.Errorf("an unpaused skip must not say paused")
	}
}

func TestScheduleNoticePausedChangesTheSubject(t *testing.T) {
	msg := BuildScheduleNotice(ScheduleNotice{Reason: ScheduleLowBalance, Paused: true})
	if !strings.Contains(msg.Subject, "paused") || !strings.Contains(msg.Body, "PAUSED") {
		t.Fatalf("paused notice: %q\n%s", msg.Subject, msg.Body)
	}
}

func TestScheduleNoticePartialNamesTheOrderAndTheShortfall(t *testing.T) {
	msg := BuildScheduleNotice(ScheduleNotice{
		Reason: SchedulePartial, OrderNumber: "VM-261003-0007",
		Note: "Left out or reduced: Pomegranate (L) · 1 Kg Box — 1 of 2.",
	})
	if !strings.Contains(msg.Body, "VM-261003-0007") || !strings.Contains(msg.Body, "1 of 2") {
		t.Fatalf("partial notice:\n%s", msg.Body)
	}
}
