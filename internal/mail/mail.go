// Package mail provides outbound email through a transactional outbox.
//
// Producers call Enqueue inside their own database transaction; the row
// becomes visible to the Dispatcher only when the caller commits. The
// Dispatcher (dispatcher.go) later claims due rows and delivers them through
// a Sender. When SMTP_HOST is unset, NewSender returns a LogSender that
// writes messages to stdout — the dev fallback.
package mail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/smtp"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"

	"glance/internal/config"
)

// Message is a single outbound email.
type Message struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// Sender delivers one email message.
type Sender interface {
	Send(ctx context.Context, msg Message) error
}

// NewSender picks the sender from config: SMTP when SMTP_HOST is set,
// otherwise the LogSender dev fallback.
func NewSender(cfg *config.Config) Sender {
	if cfg.SMTPHost == "" {
		return &LogSender{}
	}
	return &SMTPSender{cfg: cfg}
}

// Enqueue writes one outbox row inside the caller's transaction. The event
// is namespaced (e.g. "email.otp"); each dispatcher only claims its own
// namespace so the shared table never sees cross-dispatcher fights.
func Enqueue(ctx context.Context, tx pgx.Tx, event string, payload any) error {
	if event == "" {
		return errors.New("mail: event must not be empty")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("mail: marshal payload: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO outbox (event, payload) VALUES ($1, $2)`,
		event, json.RawMessage(data)); err != nil {
		return fmt.Errorf("mail: enqueue %q: %w", event, err)
	}
	return nil
}

// LogSender is the dev fallback: it writes the message to Out (os.Stdout
// when nil) instead of sending it.
type LogSender struct {
	Out io.Writer
}

func (s *LogSender) Send(_ context.Context, msg Message) error {
	out := s.Out
	if out == nil {
		out = os.Stdout
	}
	fmt.Fprintf(out, "[mail] to=%s subject=%q\n%s\n", msg.To, msg.Subject, msg.Body)
	return nil
}

// SMTPSender delivers mail through the SMTP server described by config.
// It uses STARTTLS when the server advertises it (port 587); implicit-TLS
// port 465 is not supported by net/smtp — operators should use 587.
type SMTPSender struct {
	cfg *config.Config
}

func (s *SMTPSender) Send(ctx context.Context, msg Message) error {
	if msg.To == "" {
		return errors.New("mail: missing recipient")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	port := s.cfg.SMTPPort
	if port == "" {
		port = "587"
	}
	from := s.cfg.SMTPFrom
	if from == "" {
		from = s.cfg.SMTPUser
	}
	var auth smtp.Auth
	if s.cfg.SMTPUser != "" {
		auth = smtp.PlainAuth("", s.cfg.SMTPUser, s.cfg.SMTPPassword, s.cfg.SMTPHost)
	}
	addr := net.JoinHostPort(s.cfg.SMTPHost, port)
	if err := smtp.SendMail(addr, auth, from, []string{msg.To}, buildMessage(from, msg)); err != nil {
		return fmt.Errorf("mail: smtp send to %s: %w", msg.To, err)
	}
	return nil
}

func buildMessage(from string, msg Message) []byte {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + msg.To + "\r\n")
	b.WriteString("Subject: " + msg.Subject + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(msg.Body)
	return []byte(b.String())
}
