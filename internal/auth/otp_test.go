package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

// uniqueEmail returns an address unique to this test run across all
// packages (PID differs per test binary, sequence per call). Uniqueness
// per run also keeps the 1-hour rate-limit window from leaking between
// consecutive `go test` invocations.
func uniqueEmail(prefix string) string {
	return fmt.Sprintf("%s-%d-%d@example.com", prefix, os.Getpid(), testSeq.Add(1))
}

// uniqueIP returns an IP unique to this call (same uniqueness story as
// uniqueEmail).
func uniqueIP() string {
	n := testSeq.Add(1)
	return fmt.Sprintf("10.210.%d.%d", os.Getpid()%250+1, n%250+1)
}

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

func testConfig() *config.Config {
	return &config.Config{OTPPepper: "test-pepper-do-not-use-in-prod"}
}

func TestRequestOTPStoresHashedCode(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	cfg := testConfig()

	email := uniqueEmail("otp-hash")
	if err := RequestOTP(ctx, pool, cfg, email, uniqueIP()); err != nil {
		t.Fatalf("RequestOTP: %v", err)
	}

	var codeHash string
	var expiresAt time.Time
	var consumedAt *time.Time
	err := pool.QueryRow(ctx,
		`SELECT code_hash, expires_at, consumed_at FROM otp_codes WHERE email = $1`,
		email,
	).Scan(&codeHash, &expiresAt, &consumedAt)
	if err != nil {
		t.Fatalf("query otp_codes: %v", err)
	}

	// The stored hash must be SHA-256(pepper + code), hex-encoded — the plain
	// 6-digit code must never appear in the row.
	if len(codeHash) != 64 {
		t.Errorf("code_hash length = %d, want 64 hex chars", len(codeHash))
	}
	if _, err := hex.DecodeString(codeHash); err != nil {
		t.Errorf("code_hash is not hex: %v", err)
	}
	if consumedAt != nil {
		t.Errorf("consumed_at = %v, want NULL for a fresh code", consumedAt)
	}

	// 10-minute expiry (allow 60s of test clock skew).
	ttl := time.Until(expiresAt)
	if ttl < 9*time.Minute || ttl > 10*time.Minute {
		t.Errorf("expiry ttl = %v, want ~10 minutes", ttl)
	}

	// The plain code must not be recoverable from the hash without the code
	// itself — sanity check that hashing actually ran (hash of empty input
	// would be e3b0c442...).
	emptyHash := sha256.Sum256([]byte("test-pepper-do-not-use-in-prod"))
	if codeHash == hex.EncodeToString(emptyHash[:]) {
		t.Errorf("code_hash looks like the hash of an empty code")
	}

	// One outbox row with the email.otp event must exist in the same
	// transaction's aftermath — filtered to this test's recipient, since
	// other packages enqueue concurrently.
	var event string
	var payload string
	err = pool.QueryRow(ctx,
		`SELECT event, payload::text FROM outbox
		 WHERE event = 'email.otp' AND payload::text LIKE '%'||$1||'%'
		 ORDER BY id DESC LIMIT 1`,
		email,
	).Scan(&event, &payload)
	if err != nil {
		t.Fatalf("query outbox: %v", err)
	}
	if event != "email.otp" {
		t.Errorf("outbox event = %q, want %q", event, "email.otp")
	}
	if !strings.Contains(payload, email) {
		t.Errorf("outbox payload missing recipient: %s", payload)
	}
}

func TestRequestOTPRateLimitEmail(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	cfg := testConfig()

	email := uniqueEmail("otp-rl")
	ip := uniqueIP()
	for i := 0; i < 5; i++ {
		if err := RequestOTP(ctx, pool, cfg, email, ip); err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
	}
	if err := RequestOTP(ctx, pool, cfg, email, ip); err != ErrRateLimited {
		t.Fatalf("6th request in an hour: err = %v, want ErrRateLimited", err)
	}
}

func TestRequestOTPRateLimitIP(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	cfg := testConfig()

	// 20 requests from the same IP with distinct emails: the 21st must trip
	// the per-IP limit.
	ip := uniqueIP()
	for i := 0; i < 20; i++ {
		if err := RequestOTP(ctx, pool, cfg, uniqueEmail("otp-ip"), ip); err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
	}
	if err := RequestOTP(ctx, pool, cfg, uniqueEmail("otp-ip-final"), ip); err != ErrRateLimited {
		t.Fatalf("21st request from same IP: err = %v, want ErrRateLimited", err)
	}
}

func TestRequestOTPNoUserRowCreated(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	cfg := testConfig()

	email := uniqueEmail("otp-nouser")
	if err := RequestOTP(ctx, pool, cfg, email, uniqueIP()); err != nil {
		t.Fatalf("RequestOTP: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE email = $1`, email).Scan(&n); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if n != 0 {
		t.Errorf("users rows for unknown email = %d, want 0 (no enumeration, no provisioning at request time)", n)
	}
}
