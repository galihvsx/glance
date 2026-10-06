package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"glance/internal/config"
	"glance/internal/store"
	"glance/migrations"
)

// testSeq hands out process-unique sequence numbers so parallel test
// packages sharing one database never collide on keys.
var testSeq atomic.Int64

func uniqueEmail(prefix string) string {
	return fmt.Sprintf("%s-%d-%d@example.com", prefix, os.Getpid(), testSeq.Add(1))
}

func uniqueIP() string {
	n := testSeq.Add(1)
	// Unique per (process, call): the pid separates runs — the
	// rate_limits table persists between runs — and the sequence spreads
	// across two octets so it cannot wrap for 65k calls (a single %250
	// octet reuses IPs and makes rate-limit tests flaky).
	return fmt.Sprintf("10.211.%d.%d", (os.Getpid()+int(n>>8))%256, n&0xff)
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

func testServer(t *testing.T, pool *pgxpool.Pool) *echo.Echo {
	t.Helper()
	e := echo.New()
	h := &AuthHandler{
		Pool:   pool,
		Config: &config.Config{OTPPepper: "test-pepper-do-not-use-in-prod"},
	}
	RegisterAuthRoutes(e, h)
	return e
}

// postOTP issues a POST /api/v1/auth/otp/request from a unique client IP
// (Echo's RealIP reads RemoteAddr), so per-IP rate-limit counters never
// leak between tests or consecutive runs.
func postOTP(t *testing.T, e *echo.Echo, email string) *httptest.ResponseRecorder {
	t.Helper()
	body := fmt.Sprintf(`{"email":%q}`, email)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/otp/request", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.RemoteAddr = uniqueIP() + ":1234"
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func TestRequestOTPAlwaysOK(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)

	// Unknown email: must still be 200 {"ok":true} — no enumeration.
	email := uniqueEmail("nobody-knows-me")
	rec := postOTP(t, e, email)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if strings.TrimSpace(rec.Body.String()) != `{"ok":true}` {
		t.Fatalf("body = %q, want %q", rec.Body.String(), `{"ok":true}`)
	}

	// Unknown emails must not create user rows.
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM users WHERE email = $1`, email).Scan(&n); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if n != 0 {
		t.Errorf("users rows = %d, want 0 (no provisioning at request time)", n)
	}
}

func TestRequestOTPRateLimited(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)

	// Same email AND same IP for all six: the 6th must hit the 5/hr/email
	// limit. A fixed IP keeps the per-IP counter (20/hr) out of the way.
	email := uniqueEmail("ratelimit")
	ip := uniqueIP()
	for i := 0; i < 5; i++ {
		if rec := postOTPFrom(t, e, email, ip); rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i+1, rec.Code)
		}
	}
	rec := postOTPFrom(t, e, email, ip)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("6th request in an hour: status = %d, want 429", rec.Code)
	}
}

func postOTPFrom(t *testing.T, e *echo.Echo, email, ip string) *httptest.ResponseRecorder {
	t.Helper()
	body := fmt.Sprintf(`{"email":%q}`, email)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/otp/request", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.RemoteAddr = ip + ":1234"
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// postVerify issues a POST /api/v1/auth/otp/verify from a unique client IP
// (Echo's RealIP reads RemoteAddr), so per-IP rate-limit counters never
// leak between tests or consecutive runs.
func postVerify(t *testing.T, e *echo.Echo, email, code string) *httptest.ResponseRecorder {
	t.Helper()
	return postVerifyFrom(t, e, email, code, uniqueIP())
}

func postVerifyFrom(t *testing.T, e *echo.Echo, email, code, ip string) *httptest.ResponseRecorder {
	t.Helper()
	body := fmt.Sprintf(`{"email":%q,"code":%q}`, email, code)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/otp/verify", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.RemoteAddr = ip + ":1234"
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// insertVerifyCode stores a known code (the api package cannot reuse the
// auth package's test helper, so the hash is computed inline with the same
// pepper the test server uses).
func insertVerifyCode(t *testing.T, pool *pgxpool.Pool, email, code string) {
	t.Helper()
	sum := sha256.Sum256([]byte("test-pepper-do-not-use-in-prod" + code))
	_, err := pool.Exec(context.Background(),
		`INSERT INTO otp_codes (email, code_hash, expires_at) VALUES ($1, $2, $3)`,
		strings.ToLower(email), hex.EncodeToString(sum[:]), time.Now().Add(10*time.Minute))
	if err != nil {
		t.Fatalf("insert verify code: %v", err)
	}
}

func TestVerifyEndpointWrongCode401(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)

	email := uniqueEmail("h-verify-wrong")
	insertVerifyCode(t, pool, email, "123456")

	rec := postVerify(t, e, email, "000000")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong code: status = %d, want 401", rec.Code)
	}
	body := strings.TrimSpace(rec.Body.String())

	// Unknown email, expired and wrong code must be indistinguishable —
	// same status AND same body (no enumeration).
	rec2 := postVerify(t, e, uniqueEmail("h-verify-nobody"), "123456")
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("unknown email: status = %d, want 401", rec2.Code)
	}
	if strings.TrimSpace(rec2.Body.String()) != body {
		t.Fatalf("unknown email body = %q, wrong-code body = %q — enumeration leak", rec2.Body.String(), body)
	}
	if !strings.Contains(body, "invalid or expired code") {
		t.Fatalf("body = %q, want generic message", body)
	}
	if !strings.Contains(body, `"code":"unauthorized"`) {
		t.Fatalf("body = %q, want spec §5 envelope code unauthorized", body)
	}
}

// TestVerifyEndpointRateLimitedPerIP: 21 rapid wrong-code verifies from one
// IP in an hour must trip the 20/hr/IP budget with 429. Emails rotate so
// the tighter per-email budget (5/hr, Task 28) never trips first — this
// test isolates the IP budget.
func TestVerifyEndpointRateLimitedPerIP(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)

	ip := uniqueIP()

	for i := 0; i < 20; i++ {
		email := uniqueEmail("h-verify-rl-ip")
		insertVerifyCode(t, pool, email, "123456")
		if rec := postVerifyFrom(t, e, email, "000000", ip); rec.Code != http.StatusUnauthorized {
			t.Fatalf("verify %d: status = %d, want 401", i+1, rec.Code)
		}
	}
	email := uniqueEmail("h-verify-rl-ip")
	insertVerifyCode(t, pool, email, "123456")
	rec := postVerifyFrom(t, e, email, "000000", ip)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("21st verify from same IP in an hour: status = %d, want 429", rec.Code)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != `{"error":{"code":"rate_limited","message":"too many requests, try again later"}}` {
		t.Fatalf("429 body = %q, want the same message as the request endpoint", body)
	}
}

// TestVerifyEndpointRateLimitedPerEmail: the per-email budget (5/hr, Task
// 28 decision) must trip even when the attacker rotates IPs, breaking the
// code-burn loop: the attacker's whole hourly budget burns at most one
// code while the victim can request five.
func TestVerifyEndpointRateLimitedPerEmail(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)

	email := uniqueEmail("h-verify-rl-email")
	insertVerifyCode(t, pool, email, "123456")

	for i := 0; i < 5; i++ {
		if rec := postVerifyFrom(t, e, email, "000000", uniqueIP()); rec.Code != http.StatusUnauthorized {
			t.Fatalf("verify %d: status = %d, want 401", i+1, rec.Code)
		}
	}
	rec := postVerifyFrom(t, e, email, "000000", uniqueIP())
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("6th verify for same email in an hour: status = %d, want 429", rec.Code)
	}
}

func TestVerifyEndpointSuccessSetsCookie(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)

	email := uniqueEmail("h-verify-ok")
	insertVerifyCode(t, pool, email, "654321")

	rec := postVerify(t, e, email, "654321")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	var sc *http.Cookie
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == "glance_session" {
			sc = ck
		}
	}
	if sc == nil {
		t.Fatalf("no glance_session cookie in response")
	}
	if !sc.HttpOnly {
		t.Errorf("cookie HttpOnly = false, want true")
	}
	if !sc.Secure {
		t.Errorf("cookie Secure = false, want true")
	}
	if sc.SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie SameSite = %v, want Lax", sc.SameSite)
	}
	if sc.MaxAge != 2592000 {
		t.Errorf("cookie MaxAge = %d, want 2592000 (30 days)", sc.MaxAge)
	}
	if sc.Path != "/" {
		t.Errorf("cookie Path = %q, want /", sc.Path)
	}
	if len(sc.Value) != 64 {
		t.Fatalf("cookie value length = %d, want 64 hex chars", len(sc.Value))
	}

	// The cookie value's SHA-256 must match the stored session token_hash —
	// the plain token is never persisted.
	sum := sha256.Sum256([]byte(sc.Value))
	var tokenHash string
	err := pool.QueryRow(ctx,
		`SELECT s.token_hash FROM sessions s JOIN users u ON u.id = s.user_id WHERE u.email = $1`,
		strings.ToLower(email),
	).Scan(&tokenHash)
	if err != nil {
		t.Fatalf("query session: %v", err)
	}
	if tokenHash != hex.EncodeToString(sum[:]) {
		t.Errorf("session token_hash does not match SHA-256(cookie value)")
	}
}
