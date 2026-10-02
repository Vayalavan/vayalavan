package mail_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/vayal-mikrogreenz/vm-orders-api/internal/mail"
)

// TestRecipientsSplitsAndTrims — MAIL_TO is a comma-separated list typed into
// a .env file, so it arrives with spaces and, sooner or later, a trailing
// comma. One empty address makes a relay reject the whole message.
func TestRecipientsSplitsAndTrims(t *testing.T) {
	tests := []struct {
		name string
		to   string
		want []string
	}{
		{"one", "a@example.com", []string{"a@example.com"}},
		{"two", "a@example.com,b@example.com", []string{"a@example.com", "b@example.com"}},
		{"spaces", " a@example.com , b@example.com ", []string{"a@example.com", "b@example.com"}},
		{"trailing comma", "a@example.com,", []string{"a@example.com"}},
		{"empty", "", nil},
		{"only commas", ",,", nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mail.Message{To: tc.to}.Recipients()
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i, want := range tc.want {
				if got[i] != want {
					t.Errorf("address %d: got %q, want %q", i, got[i], want)
				}
			}
		})
	}
}

// TestRenderPlainTextHasNoMultipart — an order confirmation must stay a plain
// message. A transactional email dressed up as multipart looks like phishing.
func TestRenderPlainText(t *testing.T) {
	raw, err := mail.Render("no-reply@vayal.test", mail.Message{
		To: "customer@example.com", Subject: "Order confirmed", Body: "Thank you.",
	})
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	out := string(raw)

	if strings.Contains(out, "multipart") {
		t.Error("a message with no attachments must not be multipart")
	}
	for _, want := range []string{
		"From: no-reply@vayal.test\r\n",
		"To: customer@example.com\r\n",
		"Subject: Order confirmed\r\n",
		"Content-Type: text/plain; charset=\"utf-8\"\r\n",
		"Thank you.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// TestRenderAttachment — the CSV must survive the trip: base64, wrapped, and
// decodable back to exactly what went in.
func TestRenderAttachment(t *testing.T) {
	// Long enough to force several base64 lines, and with a comma and a quote
	// so a naive header would break.
	csv := "order_number,recipient_name\r\n" +
		strings.Repeat("VM-260819-0001,\"Priya, R\"\r\n", 12)

	raw, err := mail.Render("reports@vayal.test", mail.Message{
		To:      "ops@example.com, owner@example.com",
		Subject: "Vayal Daily Orders Report — 2026-08-19",
		Body:    "Orders: 12",
		Attachments: []mail.Attachment{{
			Filename:    "vayal-orders-20260819.csv",
			ContentType: "text/csv; charset=utf-8",
			Content:     []byte(csv),
		}},
	})
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	out := string(raw)

	if !strings.Contains(out, "Content-Type: multipart/mixed; boundary=") {
		t.Fatalf("expected a multipart message, got:\n%s", out)
	}
	if !strings.Contains(out, `Content-Disposition: attachment; filename="vayal-orders-20260819.csv"`) {
		t.Error("the attachment is not named")
	}
	if !strings.Contains(out, "To: ops@example.com, owner@example.com\r\n") {
		t.Error("both recipients should appear in the header")
	}
	if !strings.Contains(out, "Orders: 12") {
		t.Error("the covering note is missing")
	}

	// Every base64 line within the limit relays accept.
	body := out[strings.Index(out, "Content-Transfer-Encoding: base64"):]
	for _, line := range strings.Split(body, "\r\n") {
		if len(line) > 76 && !strings.HasPrefix(line, "Content-") && !strings.HasPrefix(line, "--") {
			t.Errorf("base64 line is %d chars, over the 76-char limit: %q", len(line), line)
		}
	}

	// And the file comes back out byte for byte.
	start := strings.Index(body, "\r\n\r\n") + 4
	end := strings.Index(body[start:], "\r\n--")
	encoded := strings.ReplaceAll(body[start:start+end], "\r\n", "")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decoding the attachment: %v", err)
	}
	if string(decoded) != csv {
		t.Errorf("the attachment did not survive the round trip:\ngot  %q\nwant %q",
			string(decoded), csv)
	}
}

// TestEnvelopeAddressStripsDisplayNames — SMTP's MAIL FROM and RCPT TO take a
// bare address.
//
// SMTP_FROM is written as `Vayalavan <no-reply@…>` so the header reads
// well. MailHog accepts that shape in the envelope too, which is why this went
// unnoticed locally — Gmail answers it with a syntax error and the message
// never leaves.
func TestRecipientsReduceToBareAddresses(t *testing.T) {
	tests := []struct {
		name string
		to   string
		want []string
	}{
		{"bare", "a@example.com", []string{"a@example.com"}},
		{"display name", "Vayalavan <a@example.com>", []string{"a@example.com"}},
		{
			"mixed list",
			"Ops <ops@example.com>, owner@example.com",
			[]string{"ops@example.com", "owner@example.com"},
		},
		// Left alone rather than rewritten: the relay's own rejection message
		// is more useful than a silent guess.
		{"unparseable", "not-an-address", []string{"not-an-address"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mail.Message{To: tc.to}.Recipients()
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("address %d: got %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestRenderKeepsTheDisplayNameInTheHeader — the envelope is stripped, the
// header is not: a customer should see "Vayalavan", not a bare address.
func TestRenderKeepsTheDisplayNameInTheHeader(t *testing.T) {
	raw, err := mail.Render("Vayalavan <no-reply@vayal.test>", mail.Message{
		To: "Priya <priya@example.com>", Subject: "Order confirmed", Body: "Thank you.",
	})
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	out := string(raw)

	if !strings.Contains(out, "From: Vayalavan <no-reply@vayal.test>\r\n") {
		t.Errorf("the sender's display name was lost from the header:\n%s", out)
	}
	if !strings.Contains(out, "To: priya@example.com\r\n") {
		t.Errorf("recipient header: got:\n%s", out)
	}
}
