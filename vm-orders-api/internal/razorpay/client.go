package razorpay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/vayal-mikrogreenz/vm-go-common/money"
)

const (
	ordersEndpoint   = "https://api.razorpay.com/v1/orders"
	paymentsEndpoint = "https://api.razorpay.com/v1/payments"
)

// Client calls the Razorpay Orders API (CLAUDE.md §6.4 step 1).
type Client struct {
	keyID     string
	keySecret string
	http      *http.Client
	// endpoint is overridable so tests never reach the real API.
	endpoint string
	// payments is the Payments API base, for refunds. Overridable likewise.
	payments string
}

// NewClient builds a client. Credentials come from env and are never logged.
func NewClient(keyID, keySecret string) *Client {
	return &Client{
		keyID:     keyID,
		keySecret: keySecret,
		http:      &http.Client{Timeout: 15 * time.Second},
		endpoint:  ordersEndpoint,
		payments:  paymentsEndpoint,
	}
}

// WithEndpoint overrides the API base, for tests.
func (c *Client) WithEndpoint(endpoint string) *Client {
	clone := *c
	clone.endpoint = endpoint
	return &clone
}

// WithPaymentsEndpoint overrides the Payments API base, for tests.
func (c *Client) WithPaymentsEndpoint(endpoint string) *Client {
	clone := *c
	clone.payments = endpoint
	return &clone
}

// Order is the provider order a browser Checkout is opened against.
type Order struct {
	ID       string `json:"id"`
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
	Receipt  string `json:"receipt"`
	Status   string `json:"status"`
}

// CreateOrder registers an order with Razorpay.
//
// amount is integer paise, which is exactly what Razorpay expects — no
// conversion, and therefore no rounding, happens here (CLAUDE.md rule 1).
func (c *Client) CreateOrder(
	ctx context.Context, amount money.Paise, receipt string,
) (Order, error) {
	body, err := json.Marshal(map[string]any{
		"amount":   amount.Int64(),
		"currency": "INR",
		// The human order number, so a Razorpay dashboard row can be traced
		// back to ours without a lookup table.
		"receipt": receipt,
	})
	if err != nil {
		return Order{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return Order{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.keyID, c.keySecret)

	resp, err := c.http.Do(req)
	if err != nil {
		return Order{}, fmt.Errorf("razorpay: creating order: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		// The provider's error text can name our key id; keep it out of the
		// customer-facing path by returning a generic error and letting the
		// caller log the detail.
		var detail struct {
			Error struct {
				Description string `json:"description"`
			} `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&detail)
		return Order{}, fmt.Errorf("razorpay: order creation returned %d: %s",
			resp.StatusCode, detail.Error.Description)
	}

	var order Order
	if err := json.NewDecoder(resp.Body).Decode(&order); err != nil {
		return Order{}, fmt.Errorf("razorpay: decoding order: %w", err)
	}
	return order, nil
}

// ErrOrderNotFound means Razorpay has no order with that id — typically a
// test-mode order looked up with live keys. Nothing can have been paid on it.
var ErrOrderNotFound = errors.New("razorpay: order not found")

// Payment is one payment attempt against a Razorpay order.
type Payment struct {
	ID     string `json:"id"`
	Amount int64  `json:"amount"`
	// created, authorized, captured, refunded or failed.
	Status string `json:"status"`
}

// MoneyTaken reports whether the customer has parted with money on this
// attempt: captured, or authorized and awaiting capture. A refunded payment
// has been given back, and a failed one never left the customer.
func (p Payment) MoneyTaken() bool {
	return p.Status == "captured" || p.Status == "authorized"
}

// OrderPayments lists every payment attempt made against one Razorpay order.
//
// The reservation sweeper asks this before releasing a lapsed hold, so a
// customer whose capture webhook is late — a Razorpay outage, a backlog — does
// not have their produce sold to someone else while the notification is still
// on its way.
func (c *Client) OrderPayments(ctx context.Context, orderID string) ([]Payment, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.endpoint+"/"+url.PathEscape(orderID)+"/payments", nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.keyID, c.keySecret)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("razorpay: listing order payments: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrOrderNotFound
	}
	if resp.StatusCode >= 300 {
		var detail struct {
			Error struct {
				Description string `json:"description"`
			} `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&detail)
		return nil, fmt.Errorf("razorpay: order payments returned %d: %s",
			resp.StatusCode, detail.Error.Description)
	}

	var collection struct {
		Items []Payment `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&collection); err != nil {
		return nil, fmt.Errorf("razorpay: decoding order payments: %w", err)
	}
	return collection.Items, nil
}

// Refund is a refund Razorpay has accepted.
type Refund struct {
	ID        string `json:"id"`
	PaymentID string `json:"payment_id"`
	Amount    int64  `json:"amount"`
	Status    string `json:"status"`
}

// RefundPayment sends amount back against one captured payment — the wallet's
// admin refund (CLAUDE.md §6.7). A refund can only ever go back to the card or
// account the payment came from, which is what keeps the wallet closed-loop.
//
// receipt is our own refund id, so a retried call after a timeout is
// recognisable on the dashboard rather than looking like a second refund.
func (c *Client) RefundPayment(
	ctx context.Context, paymentID string, amount money.Paise, receipt string,
) (Refund, error) {
	body, err := json.Marshal(map[string]any{
		"amount":  amount.Int64(),
		"receipt": receipt,
		"speed":   "normal",
	})
	if err != nil {
		return Refund{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.payments+"/"+url.PathEscape(paymentID)+"/refund", bytes.NewReader(body))
	if err != nil {
		return Refund{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.keyID, c.keySecret)

	resp, err := c.http.Do(req)
	if err != nil {
		return Refund{}, fmt.Errorf("razorpay: refunding payment: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		var detail struct {
			Error struct {
				Description string `json:"description"`
			} `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&detail)
		return Refund{}, fmt.Errorf("razorpay: refund returned %d: %s",
			resp.StatusCode, detail.Error.Description)
	}

	var refund Refund
	if err := json.NewDecoder(resp.Body).Decode(&refund); err != nil {
		return Refund{}, fmt.Errorf("razorpay: decoding refund: %w", err)
	}
	return refund, nil
}
