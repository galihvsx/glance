package mail

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"glance/internal/config"
	"glance/internal/store"
	"glance/migrations"
)

// newTestPool opens a real pool against TEST_DATABASE_URL. The database is
// available in this environment, so an unset URL is a hard failure —
// these tests must never silently skip.
func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Fatal("TEST_DATABASE_URL must be set; refusing to skip")
	}
	pool, err := store.NewPool(context.Background(), &config.Config{DatabaseURL: url})
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// migrateTestDB applies the real migrations and empties the outbox so each
// test starts from a known state.
func migrateTestDB(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if err := store.Migrate(ctx, pool, migrations.FS); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE outbox`); err != nil {
		t.Fatalf("truncate outbox: %v", err)
	}
}

func TestLogSenderWhenNoSMTP(t *testing.T) {
	cfg := &config.Config{} // SMTPHost empty -> dev fallback
	s := NewSender(cfg)
	ls, ok := s.(*LogSender)
	if !ok {
		t.Fatalf("NewSender with empty SMTP_HOST = %T, want *LogSender", s)
	}

	var buf bytes.Buffer
	ls.Out = &buf
	msg := Message{To: "dev@example.com", Subject: "hello", Body: "world"}
	if err := ls.Send(context.Background(), msg); err != nil {
		t.Fatalf("LogSender.Send: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "dev@example.com") {
		t.Errorf("log output missing recipient: %q", out)
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("log output missing subject: %q", out)
	}
}

func TestNewSenderWithSMTP(t *testing.T) {
	cfg := &config.Config{SMTPHost: "smtp.example.com", SMTPPort: "587"}
	s := NewSender(cfg)
	if _, ok := s.(*SMTPSender); !ok {
		t.Fatalf("NewSender with SMTP_HOST = %T, want *SMTPSender", s)
	}
}
