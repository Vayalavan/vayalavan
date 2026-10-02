package api

import (
	"errors"
	"fmt"
	"testing"
)

// TestWebhookOutcome — what one processing attempt means for the event.
//
// Getting this wrong either way costs money: closing a temporary failure
// leaves a paid order unpaid until a human notices, and retrying a permanent
// one keeps Razorpay failing until it disables the whole webhook.
func TestWebhookOutcome(t *testing.T) {
	blip := errors.New("conn reset by peer")

	tests := []struct {
		name     string
		err      error
		attempts int
		want     webhookOutcome
	}{
		{"success", nil, 1, webhookDone},
		{"success on a retry", nil, 4, webhookDone},
		{"temporary, first attempt", blip, 1, webhookRetry},
		{"temporary, one short of the cap", blip, 9, webhookRetry},
		{"temporary, at the cap", blip, 10, webhookFailedForGood},
		{"temporary, past the cap", blip, 12, webhookFailedForGood},
		{"amount mismatch", permanent(errAmountMismatch), 1, webhookFailedForGood},
		{"permanent, wrapped again", fmt.Errorf("capture: %w", permanent(errAmountMismatch)), 1, webhookFailedForGood},
		{"bare amount mismatch is not marked permanent", errAmountMismatch, 1, webhookRetry},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := webhookOutcomeFor(tc.err, tc.attempts, 10); got != tc.want {
				t.Errorf("outcome = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestPermanentKeepsTheCause — marking an error permanent must not hide what
// it was, or the amount-mismatch checks downstream stop matching.
func TestPermanentKeepsTheCause(t *testing.T) {
	err := permanent(errAmountMismatch)
	if !errors.Is(err, errAmountMismatch) {
		t.Error("errors.Is lost the cause")
	}
	if err.Error() != errAmountMismatch.Error() {
		t.Errorf("message = %q", err.Error())
	}
}
