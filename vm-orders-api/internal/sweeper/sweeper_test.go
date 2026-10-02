package sweeper

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"github.com/vayal-mikrogreenz/vm-orders-api/internal/razorpay"
	"github.com/vayal-mikrogreenz/vm-orders-api/internal/store"
)

// fakeRazorpay answers OrderPayments from a table and records who was asked.
type fakeRazorpay struct {
	answers map[string]fakeAnswer
	asked   []string
}

type fakeAnswer struct {
	payments []razorpay.Payment
	err      error
}

func (f *fakeRazorpay) OrderPayments(_ context.Context, orderID string) ([]razorpay.Payment, error) {
	f.asked = append(f.asked, orderID)
	answer := f.answers[orderID]
	return answer.payments, answer.err
}

// TestClearedOrders — only an order Razorpay says took no money may have its
// stock released; anything short of a clear answer keeps it.
func TestClearedOrders(t *testing.T) {
	paid := func(status string) fakeAnswer {
		return fakeAnswer{payments: []razorpay.Payment{{ID: "pay_1", Status: status}}}
	}

	tests := []struct {
		name   string
		answer fakeAnswer
		want   bool
	}{
		{"no attempts", fakeAnswer{}, true},
		{"only a failed attempt", paid("failed"), true},
		{"created, never completed", paid("created"), true},
		{"refunded", paid("refunded"), true},
		{"unknown to Razorpay", fakeAnswer{err: razorpay.ErrOrderNotFound}, true},
		{"captured, webhook late", paid("captured"), false},
		{"authorized, awaiting capture", paid("authorized"), false},
		{"failed then captured", fakeAnswer{payments: []razorpay.Payment{
			{ID: "pay_1", Status: "failed"}, {ID: "pay_2", Status: "captured"},
		}}, false},
		{"Razorpay unreachable", fakeAnswer{err: errors.New("dial tcp: timeout")}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			order := store.ListLapsedRazorpayOrdersRow{ID: uuid.New(), RazorpayOrderID: "order_A"}
			s := &Sweeper{
				payments: &fakeRazorpay{answers: map[string]fakeAnswer{"order_A": tc.answer}},
				logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
			}
			cleared := s.clearedOrders(context.Background(), []store.ListLapsedRazorpayOrdersRow{order})
			if got := len(cleared) == 1 && cleared[0] == order.ID; got != tc.want {
				t.Errorf("cleared = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestClearedOrdersStopsAtTheFirstError — once Razorpay fails, every later
// call would wait out the same timeout, so the pass stops asking. Orders
// already cleared stay cleared; the rest keep their stock until the next tick.
func TestClearedOrdersStopsAtTheFirstError(t *testing.T) {
	fake := &fakeRazorpay{answers: map[string]fakeAnswer{
		"order_1": {},
		"order_2": {err: errors.New("503 service unavailable")},
		"order_3": {},
	}}
	s := &Sweeper{payments: fake, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	first := uuid.New()
	cleared := s.clearedOrders(context.Background(), []store.ListLapsedRazorpayOrdersRow{
		{ID: first, RazorpayOrderID: "order_1"},
		{ID: uuid.New(), RazorpayOrderID: "order_2"},
		{ID: uuid.New(), RazorpayOrderID: "order_3"},
	})

	if len(cleared) != 1 || cleared[0] != first {
		t.Errorf("cleared = %v, want only the order checked before the failure", cleared)
	}
	if len(fake.asked) != 2 {
		t.Errorf("asked Razorpay %d times, want 2 (stop after the failure)", len(fake.asked))
	}
}
