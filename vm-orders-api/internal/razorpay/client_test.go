package razorpay

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestOrderPayments — the sweeper's question to Razorpay. A wrong answer
// either sells a paying customer's produce or never frees abandoned stock.
func TestOrderPayments(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantStatus []string
		wantErr    error
		wantAnyErr bool
	}{
		{
			name:   "payments listed",
			status: http.StatusOK,
			body: `{"entity":"collection","count":2,"items":[
				{"id":"pay_1","amount":11800,"status":"failed"},
				{"id":"pay_2","amount":11800,"status":"captured"}]}`,
			wantStatus: []string{"failed", "captured"},
		},
		{
			name:       "no attempts",
			status:     http.StatusOK,
			body:       `{"entity":"collection","count":0,"items":[]}`,
			wantStatus: []string{},
		},
		{
			name:    "unknown order",
			status:  http.StatusNotFound,
			body:    `{"error":{"description":"The id provided does not exist"}}`,
			wantErr: ErrOrderNotFound,
		},
		{
			name:       "provider failure",
			status:     http.StatusServiceUnavailable,
			body:       `{"error":{"description":"unavailable"}}`,
			wantAnyErr: true,
		},
		{
			name:       "bad credentials are an error, not 'nothing paid'",
			status:     http.StatusUnauthorized,
			body:       `{"error":{"description":"Authentication failed"}}`,
			wantAnyErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath string
			var gotAuth bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				user, pass, ok := r.BasicAuth()
				gotAuth = ok && user == "key_id" && pass == "key_secret"
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			client := NewClient("key_id", "key_secret").WithEndpoint(server.URL + "/v1/orders")
			payments, err := client.OrderPayments(context.Background(), "order_ABC")

			if gotPath != "/v1/orders/order_ABC/payments" {
				t.Errorf("path = %q", gotPath)
			}
			if !gotAuth {
				t.Error("request did not carry the key id and secret")
			}
			switch {
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			case tc.wantAnyErr:
				if err == nil || errors.Is(err, ErrOrderNotFound) {
					t.Fatalf("err = %v, want a provider error", err)
				}
				return
			case err != nil:
				t.Fatalf("unexpected error: %v", err)
			}
			if len(payments) != len(tc.wantStatus) {
				t.Fatalf("got %d payments, want %d", len(payments), len(tc.wantStatus))
			}
			for i, status := range tc.wantStatus {
				if payments[i].Status != status {
					t.Errorf("payment %d status = %q, want %q", i, payments[i].Status, status)
				}
			}
		})
	}
}
