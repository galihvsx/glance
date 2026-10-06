package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

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
	return fmt.Sprintf("10.211.%d.%d", os.Getpid()%250+1, n%250+1)
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
