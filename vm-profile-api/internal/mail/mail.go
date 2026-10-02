// Package mail sends transactional email.
package mail

import (
	"context"
	"fmt"
	"log/slog"
	"net"
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
	To      string
	Subject string
	Body    string
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

	var body strings.Builder
	fmt.Fprintf(&body, "From: %s\r\n", s.cfg.From)
	fmt.Fprintf(&body, "To: %s\r\n", msg.To)
	fmt.Fprintf(&body, "Subject: %s\r\n", msg.Subject)
	body.WriteString("MIME-Version: 1.0\r\n")
	body.WriteString("Content-Type: text/plain; charset=\"utf-8\"\r\n")
	body.WriteString("\r\n")
	body.WriteString(msg.Body)

	// MailHog accepts unauthenticated mail; a real relay will not. Sending
	// nil auth when no username is configured is what makes both work.
	var auth smtp.Auth
	if s.cfg.Username != "" {
		auth = smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)
	}

	if err := smtp.SendMail(addr, auth, s.cfg.From, []string{msg.To}, []byte(body.String())); err != nil {
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
