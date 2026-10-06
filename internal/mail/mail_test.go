package mail

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"glance/internal/config"
	"glance/internal/store"
	"glance/migrations"
)

// testSeq hands out process-unique sequence numbers so parallel test
// packages sharing one database never collide on keys.
var testSeq atomic.Int64

// uniqueAddr returns a recipient address unique to this test run across all
// packages (PID differs per test binary, sequence per call).
func uniqueAddr(prefix string) string {
	return fmt.Sprintf("%s-%d-%d@example.com", prefix, os.Getpid(), testSeq.Add(1))
}

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

// migrateTestDB applies the real migrations. It deliberately does NOT
// truncate any table: test packages run in parallel against one shared
// database, so each test uses unique keys and filters its assertions to
// its own rows instead of assuming a clean slate.
func migrateTestDB(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if err := store.Migrate(ctx, pool, migrations.FS); err != nil {
		t.Fatalf("Migrate: %v", err)
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

// TestSMTPSenderBlackholedHostFailsFast pins the dial/session timeouts:
// 192.0.2.1 is TEST-NET-1 (RFC 5737) — guaranteed unroutable, so a
// blackholed SMTP host must fail fast instead of hanging the dispatch
// pass. Without the net.Dialer timeout this Send would block for minutes.
func TestSMTPSenderBlackholedHostFailsFast(t *testing.T) {
	s := &SMTPSender{
		cfg:         &config.Config{SMTPHost: "192.0.2.1", SMTPPort: "587", SMTPFrom: "noreply@example.com"},
		dialTimeout: 500 * time.Millisecond,
		opTimeout:   2 * time.Second,
	}
	start := time.Now()
	err := s.Send(context.Background(), Message{To: "a@example.com", Subject: "s", Body: "b"})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("Send to blackholed SMTP host succeeded, want dial error")
	}
	if elapsed > 10*time.Second {
		t.Fatalf("Send to blackholed host took %v, want fast failure (<10s)", elapsed)
	}
	t.Logf("blackholed host failed in %v: %v", elapsed, err)
}
