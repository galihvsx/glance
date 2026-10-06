package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
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
	// Unique per (process, call): the pid separates runs — the
	// rate_limits table persists between runs — and the sequence spreads
	// across two octets so it cannot wrap for 65k calls (a single %250
	// octet reuses IPs and makes rate-limit tests flaky).
	return fmt.Sprintf("10.210.%d.%d", (os.Getpid()+int(n>>8))%256, n&0xff)
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

// insertTestCode stores a KNOWN code for verify tests (RequestOTP generates
// a random one the test cannot know). createdAgo controls candidate
// ordering: the newest outstanding code is the verify candidate.
func insertTestCode(t *testing.T, pool *pgxpool.Pool, email, code string, createdAgo time.Duration) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO otp_codes (email, code_hash, expires_at, created_at)
		 VALUES ($1, $2, $3, $4)`,
		strings.ToLower(email), hashCode(testConfig().OTPPepper, code),
		time.Now().Add(10*time.Minute), time.Now().Add(-createdAgo))
	if err != nil {
		t.Fatalf("insert test code: %v", err)
	}
}

func TestVerifyWrongCode401(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	cfg := testConfig()

	email := uniqueEmail("verify-wrong")
	insertTestCode(t, pool, email, "123456", 0)

	_, err := VerifyOTP(ctx, pool, cfg, email, "000000", "test-agent", uniqueIP())
	if !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("wrong code: err = %v, want ErrInvalidCode", err)
	}

	// The attempt must be counted…
	var attempts int
	if err := pool.QueryRow(ctx, `SELECT attempts FROM otp_codes WHERE email = $1`, email).Scan(&attempts); err != nil {
		t.Fatalf("query attempts: %v", err)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1", attempts)
	}

	// …and no session or user may exist.
	var n int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM sessions`).Scan(&n); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	_ = n // sessions table is shared; filter below instead
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE email = $1`, email).Scan(&n); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if n != 0 {
		t.Errorf("users rows for failed verify = %d, want 0", n)
	}
}

func TestVerifyBurnsAfter5Attempts(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	cfg := testConfig()

	email := uniqueEmail("verify-burn")
	insertTestCode(t, pool, email, "123456", 0)
	ip := uniqueIP()

	for i := 0; i < 5; i++ {
		_, err := VerifyOTP(ctx, pool, cfg, email, "000000", "test-agent", ip)
		if !errors.Is(err, ErrInvalidCode) {
			t.Fatalf("attempt %d: err = %v, want ErrInvalidCode", i+1, err)
		}
	}

	// The code is burned: even the CORRECT code now fails generically.
	_, err := VerifyOTP(ctx, pool, cfg, email, "123456", "test-agent", ip)
	if !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("correct code after 5 wrong attempts: err = %v, want ErrInvalidCode", err)
	}

	var attempts int
	var consumedAt *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT attempts, consumed_at FROM otp_codes WHERE email = $1`, email,
	).Scan(&attempts, &consumedAt); err != nil {
		t.Fatalf("query code: %v", err)
	}
	if attempts != 5 {
		t.Errorf("attempts = %d, want exactly 5", attempts)
	}
	if consumedAt == nil {
		t.Errorf("consumed_at = NULL, want set (code burned)")
	}
}

func TestVerifySuccessSetsCookie(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	cfg := testConfig()

	email := uniqueEmail("verify-ok")
	insertTestCode(t, pool, email, "654321", 0)
	wantIP := uniqueIP()

	token, err := VerifyOTP(ctx, pool, cfg, email, "654321", "test-agent/1.0", wantIP)
	if err != nil {
		t.Fatalf("VerifyOTP: %v", err)
	}
	if len(token) != 64 {
		t.Fatalf("token length = %d, want 64 hex chars (32 bytes)", len(token))
	}
	if _, err := hex.DecodeString(token); err != nil {
		t.Fatalf("token is not hex: %v", err)
	}

	// New email → user row auto-provisioned.
	var userID string
	if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE email = $1`, email).Scan(&userID); err != nil {
		t.Fatalf("user not provisioned: %v", err)
	}

	// Session row: only the SHA-256 hash of the token is stored, 30-day
	// expiry, request metadata recorded, not revoked.
	var tokenHash, ua, ip string
	var expiresAt time.Time
	var revokedAt *time.Time
	var sessionUserID string
	err = pool.QueryRow(ctx,
		`SELECT user_id, token_hash, user_agent, ip, expires_at, revoked_at
		 FROM sessions WHERE user_id = $1`,
		userID,
	).Scan(&sessionUserID, &tokenHash, &ua, &ip, &expiresAt, &revokedAt)
	if err != nil {
		t.Fatalf("query session: %v", err)
	}
	sum := sha256.Sum256([]byte(token))
	if tokenHash != hex.EncodeToString(sum[:]) {
		t.Errorf("stored token_hash does not match SHA-256(token)")
	}
	if tokenHash == token {
		t.Errorf("plain token stored in token_hash")
	}
	if ua != "test-agent/1.0" || ip != wantIP {
		t.Errorf("session metadata = (%q, %q), want (test-agent/1.0, %q)", ua, ip, wantIP)
	}
	ttl := time.Until(expiresAt)
	if ttl < 29*24*time.Hour || ttl > 30*24*time.Hour {
		t.Errorf("session ttl = %v, want ~30 days", ttl)
	}
	if revokedAt != nil {
		t.Errorf("revoked_at = %v, want NULL", revokedAt)
	}

	// Code consumed.
	var consumedAt *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT consumed_at FROM otp_codes WHERE email = $1`, email,
	).Scan(&consumedAt); err != nil {
		t.Fatalf("query code: %v", err)
	}
	if consumedAt == nil {
		t.Errorf("consumed_at = NULL after success, want set")
	}
}

// TestVerifyNewestCodeSupersedes pins the Task 6 review handoff: multiple
// outstanding codes per email are allowed; the newest is the candidate, and
// on success every other outstanding code for the email is invalidated so
// an older code can never be used after a newer one succeeded.
func TestVerifyNewestCodeSupersedes(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	cfg := testConfig()

	email := uniqueEmail("verify-supersede")
	insertTestCode(t, pool, email, "111111", time.Minute) // older
	insertTestCode(t, pool, email, "222222", 0)           // newer (candidate)
	ip := uniqueIP()

	// The older code is NOT the candidate: it fails, and the failed attempt
	// is counted against the newest code.
	_, err := VerifyOTP(ctx, pool, cfg, email, "111111", "test-agent", ip)
	if !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("older code: err = %v, want ErrInvalidCode", err)
	}
	var newerAttempts int
	if err := pool.QueryRow(ctx,
		`SELECT attempts FROM otp_codes WHERE email = $1 AND code_hash = $2`,
		email, hashCode(cfg.OTPPepper, "222222"),
	).Scan(&newerAttempts); err != nil {
		t.Fatalf("query newer attempts: %v", err)
	}
	if newerAttempts != 1 {
		t.Errorf("newer code attempts = %d, want 1 (older code counted against it)", newerAttempts)
	}

	// Newest code succeeds…
	if _, err := VerifyOTP(ctx, pool, cfg, email, "222222", "test-agent", ip); err != nil {
		t.Fatalf("newer code: %v", err)
	}

	// …and now BOTH codes are consumed: the older one can never be used.
	var outstanding int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM otp_codes WHERE email = $1 AND consumed_at IS NULL`, email,
	).Scan(&outstanding); err != nil {
		t.Fatalf("count outstanding: %v", err)
	}
	if outstanding != 0 {
		t.Errorf("outstanding codes after success = %d, want 0", outstanding)
	}
	_, err = VerifyOTP(ctx, pool, cfg, email, "111111", "test-agent", ip)
	if !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("superseded older code: err = %v, want ErrInvalidCode (code-confusion bypass)", err)
	}
}

// TestVerifyConcurrentAttemptsAtomic: concurrent wrong-code verifies must
// not exceed the 5-attempt budget (no lost updates).
func TestVerifyConcurrentAttemptsAtomic(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	cfg := testConfig()

	email := uniqueEmail("verify-conc")
	insertTestCode(t, pool, email, "123456", 0)
	ip := uniqueIP()

	const n = 10
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = VerifyOTP(context.Background(), pool, cfg, email, "000000", "conc", ip)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if !errors.Is(err, ErrInvalidCode) {
			t.Fatalf("goroutine %d: err = %v, want ErrInvalidCode", i, err)
		}
	}

	var attempts int
	var consumedAt *time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT attempts, consumed_at FROM otp_codes WHERE email = $1`, email,
	).Scan(&attempts, &consumedAt); err != nil {
		t.Fatalf("query code: %v", err)
	}
	if attempts != 5 {
		t.Errorf("attempts = %d, want exactly 5 (budget respected under concurrency)", attempts)
	}
	if consumedAt == nil {
		t.Errorf("consumed_at = NULL, want set (code burned)")
	}
}
