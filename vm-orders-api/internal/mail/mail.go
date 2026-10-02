// Package mail sends transactional email for the orders domain.
package mail

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"mime/multipart"
	"net"
	netmail "net/mail"
	"net/smtp"
	"strings"
)

// Sender delivers a message. An interface so tests can assert what would have
// been sent without standing up an SMTP server.
type Sender interface {
	Send(ctx context.Context, msg Message) error
}

// Message is one outbound email.
type Message struct {
	// To may hold several comma-separated addresses. One field rather than a
	// slice because that is the shape env config and the outbox already carry,
	// and splitting it in one place beats every caller doing it differently.
	To      string
	Subject string
	Body    string
	// Attachments ride along as a multipart body. Empty for transactional mail,
	// which is plain text and must stay that way — an order confirmation with
	// an attachment looks like phishing.
	Attachments []Attachment
}

// Attachment is one file on a message.
type Attachment struct {
	Filename    string
	ContentType string
	Content     []byte
}

// Recipients splits the To field into addresses for the SMTP envelope.
//
// Blank entries are dropped rather than sent: an address list with a trailing
// comma is the most likely way this is mistyped in a .env file, and a relay
// rejects the whole message for one empty recipient. Each entry is reduced to
// a bare address for the same reason the sender is — see envelopeAddress.
func (m Message) Recipients() []string {
	out := make([]string, 0, 2)
	for _, address := range strings.Split(m.To, ",") {
		if trimmed := strings.TrimSpace(address); trimmed != "" {
			out = append(out, envelopeAddress(trimmed))
		}
	}
	return out
}

// envelopeAddress reduces `Name <someone@example.com>` to the address alone.
//
// SMTP's MAIL FROM and RCPT TO take an address, not a mailbox with a display
// name. MailHog tolerates the longer form, which is why this was not noticed
// locally; Gmail answers a display-name envelope with a syntax error and the
// message never leaves.
//
// Anything unparseable is passed through untouched: a relay rejecting a
// malformed address with its own error message is more useful than this
// silently rewriting it into something else.
func envelopeAddress(value string) string {
	parsed, err := netmail.ParseAddress(strings.TrimSpace(value))
	if err != nil {
		return strings.TrimSpace(value)
	}
	return parsed.Address
}

// Config describes the SMTP endpoint.
type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
}

// SMTPSender delivers over SMTP. Against MailHog locally, a real relay in
// production — the difference is entirely configuration.
type SMTPSender struct {
	cfg    Config
	logger *slog.Logger
}

// NewSMTPSender builds a sender.
func NewSMTPSender(cfg Config, logger *slog.Logger) *SMTPSender {
	return &SMTPSender{cfg: cfg, logger: logger}
}

// Send delivers the message.
func (s *SMTPSender) Send(ctx context.Context, msg Message) error {
	addr := net.JoinHostPort(s.cfg.Host, fmt.Sprint(s.cfg.Port))

	recipients := msg.Recipients()
	if len(recipients) == 0 {
		return fmt.Errorf("mail: %q has no recipients", msg.Subject)
	}

	raw, err := Render(s.cfg.From, msg)
	if err != nil {
		return err
	}

	// MailHog accepts unauthenticated mail; a real relay will not. Sending
	// nil auth when no username is configured is what makes both work.
	var auth smtp.Auth
	if s.cfg.Username != "" {
		auth = smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)
	}

	// The ENVELOPE sender must be a bare address. SMTP_FROM is written as
	// `Vayalavan <no-reply@…>` so the header reads well, and MailHog
	// accepts that in MAIL FROM — a real relay does not, and Gmail rejects the
	// message outright. The header keeps the display name; the envelope gets
	// the address out of it.
	if err := smtp.SendMail(addr, auth, envelopeAddress(s.cfg.From), recipients, raw); err != nil {
		return fmt.Errorf("mail: sending to %s: %w", msg.To, err)
	}

	s.logger.InfoContext(ctx, "email sent",
		slog.String("to", msg.To),
		slog.String("subject", msg.Subject),
	)
	return nil
}

// RecordingSender captures messages instead of sending them, for tests.
type RecordingSender struct {
	Sent []Message
}

// Send records the message.
func (r *RecordingSender) Send(_ context.Context, msg Message) error {
	r.Sent = append(r.Sent, msg)
	return nil
}

// Render builds the RFC 5322 message.
//
// Written by hand against mime/multipart rather than pulled from a mail
// library: the whole requirement is "plain text, sometimes with one CSV
// attached", the standard library does exactly that, and a dependency in the
// path of every order confirmation is a dependency to keep patched forever.
func Render(from string, msg Message) ([]byte, error) {
	var out bytes.Buffer

	fmt.Fprintf(&out, "From: %s\r\n", from)
	fmt.Fprintf(&out, "To: %s\r\n", strings.Join(msg.Recipients(), ", "))
	fmt.Fprintf(&out, "Subject: %s\r\n", msg.Subject)
	out.WriteString("MIME-Version: 1.0\r\n")

	if len(msg.Attachments) == 0 {
		out.WriteString("Content-Type: text/plain; charset=\"utf-8\"\r\n")
		out.WriteString("\r\n")
		out.WriteString(msg.Body)
		return out.Bytes(), nil
	}

	writer := multipart.NewWriter(&bytes.Buffer{})
	boundary := writer.Boundary()

	fmt.Fprintf(&out, "Content-Type: multipart/mixed; boundary=%q\r\n", boundary)
	out.WriteString("\r\n")

	fmt.Fprintf(&out, "--%s\r\n", boundary)
	out.WriteString("Content-Type: text/plain; charset=\"utf-8\"\r\n\r\n")
	out.WriteString(msg.Body)
	out.WriteString("\r\n")

	for _, file := range msg.Attachments {
		contentType := file.ContentType
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		fmt.Fprintf(&out, "--%s\r\n", boundary)
		fmt.Fprintf(&out, "Content-Type: %s\r\n", contentType)
		out.WriteString("Content-Transfer-Encoding: base64\r\n")
		fmt.Fprintf(&out, "Content-Disposition: attachment; filename=%q\r\n\r\n", file.Filename)

		// Base64 wrapped at 76 characters: some relays reject longer lines,
		// and a courier sheet for a busy day is not a short file.
		encoded := base64.StdEncoding.EncodeToString(file.Content)
		for len(encoded) > 76 {
			out.WriteString(encoded[:76])
			out.WriteString("\r\n")
			encoded = encoded[76:]
		}
		out.WriteString(encoded)
		out.WriteString("\r\n")
	}

	fmt.Fprintf(&out, "--%s--\r\n", boundary)
	return out.Bytes(), nil
}
