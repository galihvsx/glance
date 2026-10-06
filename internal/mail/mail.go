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
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/smtp"
	"os"
	"strings"
	"time"

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

// smtpDialTimeout bounds the TCP connect; smtpOpTimeout bounds the whole
// SMTP session after connect. Without these, a blackholed SMTP_HOST hangs
// the dispatch pass and wedges the dispatcher (pool exhaustion).
const (
	smtpDialTimeout = 10 * time.Second
	smtpOpTimeout   = 30 * time.Second
)

// SMTPSender delivers mail through the SMTP server described by config.
// It uses STARTTLS when the server advertises it (port 587); implicit-TLS
// port 465 is not supported by net/smtp — operators should use 587.
//
// The dial and session timeouts are struct fields (zero = defaults) so
// tests can pin fast failure against a blackholed host.
type SMTPSender struct {
	cfg         *config.Config
	dialTimeout time.Duration
	opTimeout   time.Duration
}

func (s *SMTPSender) Send(ctx context.Context, msg Message) error {
	if msg.To == "" {
		return errors.New("mail: missing recipient")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	dialTimeout := s.dialTimeout
	if dialTimeout == 0 {
		dialTimeout = smtpDialTimeout
	}
	opTimeout := s.opTimeout
	if opTimeout == 0 {
		opTimeout = smtpOpTimeout
	}
	port := s.cfg.SMTPPort
	if port == "" {
		port = "587"
	}
	from := s.cfg.SMTPFrom
	if from == "" {
		from = s.cfg.SMTPUser
	}
	addr := net.JoinHostPort(s.cfg.SMTPHost, port)
	conn, err := (&net.Dialer{Timeout: dialTimeout}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("mail: smtp dial %s: %w", addr, err)
	}
	// One deadline for the whole session, set BEFORE the greeting read:
	// a server that accepts the TCP connection but never speaks must fail
	// the delivery instead of hanging the dispatch pass forever.
	if err := conn.SetDeadline(time.Now().Add(opTimeout)); err != nil {
		conn.Close()
		return fmt.Errorf("mail: set smtp deadline: %w", err)
	}
	c, err := smtp.NewClient(conn, s.cfg.SMTPHost)
	if err != nil {
		conn.Close()
		return fmt.Errorf("mail: smtp handshake with %s: %w", addr, err)
	}
	defer c.Quit()
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: s.cfg.SMTPHost}); err != nil {
			return fmt.Errorf("mail: smtp STARTTLS: %w", err)
		}
	}
	if s.cfg.SMTPUser != "" {
		auth := smtp.PlainAuth("", s.cfg.SMTPUser, s.cfg.SMTPPassword, s.cfg.SMTPHost)
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("mail: smtp auth: %w", err)
		}
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("mail: smtp MAIL FROM: %w", err)
	}
	if err := c.Rcpt(msg.To); err != nil {
		return fmt.Errorf("mail: smtp RCPT TO %s: %w", msg.To, err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("mail: smtp DATA: %w", err)
	}
	if _, err := w.Write(buildMessage(from, msg)); err != nil {
		w.Close()
		return fmt.Errorf("mail: smtp write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mail: smtp end data: %w", err)
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
