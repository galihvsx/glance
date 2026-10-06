package api

// Session management endpoint tests (Task 9): /me, logout, list/revoke
// sessions, and the RequireAuth middleware every Phase 2+ endpoint sits
// behind. All tests run against the real test database — no skips.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"glance/internal/auth"
)

// loginTestUser performs a full OTP login (request is skipped: the code is
// inserted directly) and returns the glance_session cookie. userAgent and
// ip are recorded on the session row so list-sessions assertions are
// meaningful.
func loginTestUser(t *testing.T, e *echo.Echo, pool *pgxpool.Pool, email, userAgent, ip string) *http.Cookie {
	t.Helper()
	insertVerifyCode(t, pool, email, "654321")
	body := fmt.Sprintf(`{"email":%q,"code":"654321"}`, email)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/otp/verify", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("User-Agent", userAgent)
	req.RemoteAddr = ip + ":1234"
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == auth.SessionCookieName {
			return ck
		}
	}
	t.Fatal("login: no session cookie in response")
	return nil
}

// sessionTokenHash returns the SHA-256 hex of a raw session token — the
// value stored in sessions.token_hash (the plain token never hits the DB).
func sessionTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// getAuthed issues an authenticated request with the session cookie.
func getAuthed(t *testing.T, e *echo.Echo, method, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func TestMeUnauthenticated401(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	// API failures are JSON, never redirects.
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Fatalf("Location = %q, want no redirect on API 401", loc)
	}
	if ct := rec.Header().Get(echo.HeaderContentType); !strings.Contains(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want JSON", ct)
	}
}

func TestMeGarbageToken401(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "not-a-real-token"})
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("garbage token: status = %d, want 401", rec.Code)
	}
}

func TestMeAuthenticated200(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)

	email := uniqueEmail("h-me-ok")
	cookie := loginTestUser(t, e, pool, email, "test-agent/1.0", uniqueIP())
	rec := getAuthed(t, e, http.MethodGet, "/api/v1/me", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode /me body: %v", err)
	}
	if body["email"] != strings.ToLower(email) {
		t.Errorf("email = %v, want %q", body["email"], strings.ToLower(email))
	}
	if _, ok := body["id"]; !ok {
		t.Errorf("/me body missing id: %v", body)
	}
	// The token hash must never leak into any API response.
	if strings.Contains(rec.Body.String(), "token_hash") {
		t.Errorf("/me body leaks token_hash: %s", rec.Body.String())
	}
}

func TestMeExpiredSession401(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)

	email := uniqueEmail("h-me-exp")
	cookie := loginTestUser(t, e, pool, email, "test-agent/1.0", uniqueIP())
	if _, err := pool.Exec(ctx,
		`UPDATE sessions SET expires_at = now() - INTERVAL '1 hour' WHERE token_hash = $1`,
		sessionTokenHash(cookie.Value)); err != nil {
		t.Fatalf("expire session: %v", err)
	}

	if rec := getAuthed(t, e, http.MethodGet, "/api/v1/me", cookie); rec.Code != http.StatusUnauthorized {
		t.Fatalf("expired session: status = %d, want 401", rec.Code)
	}
}

func TestMeRevokedSession401(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)

	email := uniqueEmail("h-me-rev")
	cookie := loginTestUser(t, e, pool, email, "test-agent/1.0", uniqueIP())
	if _, err := pool.Exec(ctx,
		`UPDATE sessions SET revoked_at = now() WHERE token_hash = $1`,
		sessionTokenHash(cookie.Value)); err != nil {
		t.Fatalf("revoke session: %v", err)
	}

	if rec := getAuthed(t, e, http.MethodGet, "/api/v1/me", cookie); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session: status = %d, want 401", rec.Code)
	}
}

func TestLogoutRevokesAndClearsCookie(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)

	email := uniqueEmail("h-logout")
	cookie := loginTestUser(t, e, pool, email, "test-agent/1.0", uniqueIP())

	rec := getAuthed(t, e, http.MethodPost, "/api/v1/auth/logout", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("logout: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	// The session row must be revoked.
	var revokedAt *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT revoked_at FROM sessions WHERE token_hash = $1`,
		sessionTokenHash(cookie.Value)).Scan(&revokedAt); err != nil {
		t.Fatalf("query session: %v", err)
	}
	if revokedAt == nil {
		t.Errorf("revoked_at is NULL after logout, want set")
	}

	// The response must clear the cookie (expired, same attributes).
	var cleared *http.Cookie
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == auth.SessionCookieName {
			cleared = ck
		}
	}
	if cleared == nil {
		t.Fatalf("no %q Set-Cookie in logout response", auth.SessionCookieName)
	}
	if cleared.MaxAge > 0 {
		t.Errorf("cleared cookie MaxAge = %d, want <= 0", cleared.MaxAge)
	}
	if cleared.Path != "/" || !cleared.HttpOnly || !cleared.Secure {
		t.Errorf("cleared cookie lost attributes: path=%q httponly=%v secure=%v",
			cleared.Path, cleared.HttpOnly, cleared.Secure)
	}

	// The old cookie is dead from here on.
	if rec2 := getAuthed(t, e, http.MethodGet, "/api/v1/me", cookie); rec2.Code != http.StatusUnauthorized {
		t.Fatalf("after logout: /me status = %d, want 401", rec2.Code)
	}
}

func TestLogoutUnauthenticated401(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("logout without session: status = %d, want 401", rec.Code)
	}
}

func TestListSessions(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)

	email := uniqueEmail("h-sess-list")
	cookieA := loginTestUser(t, e, pool, email, "agent-a/1.0", uniqueIP())
	_ = loginTestUser(t, e, pool, email, "agent-b/2.0", uniqueIP())

	rec := getAuthed(t, e, http.MethodGet, "/api/v1/auth/sessions", cookieA)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Sessions []map[string]any `json:"sessions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode sessions: %v", err)
	}
	if len(body.Sessions) != 2 {
		t.Fatalf("sessions = %d, want 2 (both test logins share one user)", len(body.Sessions))
	}
	for _, s := range body.Sessions {
		for _, key := range []string{"id", "created_at", "last_seen_at", "ip", "user_agent"} {
			if _, ok := s[key]; !ok {
				t.Errorf("session entry missing %q: %v", key, s)
			}
		}
		if _, ok := s["token_hash"]; ok {
			t.Errorf("session entry leaks token_hash: %v", s)
		}
	}
	// Both recorded user agents must show up.
	raw := rec.Body.String()
	if !strings.Contains(raw, "agent-a/1.0") || !strings.Contains(raw, "agent-b/2.0") {
		t.Errorf("sessions body missing recorded user agents: %s", raw)
	}
}

func TestListSessionsUnauthenticated401(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/sessions", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// sessionIDForToken resolves the session UUID for a raw token (test-only;
// production code never maps tokens to ids outside the auth package).
func sessionIDForToken(t *testing.T, pool *pgxpool.Pool, token string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`SELECT id FROM sessions WHERE token_hash = $1`,
		sessionTokenHash(token)).Scan(&id); err != nil {
		t.Fatalf("resolve session id: %v", err)
	}
	return id
}

func TestDeleteSessionRevokes(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)

	email := uniqueEmail("h-sess-del")
	cookieA := loginTestUser(t, e, pool, email, "agent-a/1.0", uniqueIP())
	cookieB := loginTestUser(t, e, pool, email, "agent-b/2.0", uniqueIP())
	idB := sessionIDForToken(t, pool, cookieB.Value)

	rec := getAuthed(t, e, http.MethodDelete, "/api/v1/auth/sessions/"+idB, cookieA)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	var revokedAt *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT revoked_at FROM sessions WHERE id = $1`, idB).Scan(&revokedAt); err != nil {
		t.Fatalf("query session: %v", err)
	}
	if revokedAt == nil {
		t.Errorf("session %s revoked_at is NULL after DELETE, want set", idB)
	}
	// B's cookie is dead, A's still works.
	if r := getAuthed(t, e, http.MethodGet, "/api/v1/me", cookieB); r.Code != http.StatusUnauthorized {
		t.Fatalf("deleted session /me: status = %d, want 401", r.Code)
	}
	if r := getAuthed(t, e, http.MethodGet, "/api/v1/me", cookieA); r.Code != http.StatusOK {
		t.Fatalf("surviving session /me: status = %d, want 200", r.Code)
	}
}

func TestDeleteSessionNotOwned404(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)

	emailA := uniqueEmail("h-sess-del-a")
	emailB := uniqueEmail("h-sess-del-b")
	cookieA := loginTestUser(t, e, pool, emailA, "agent-a/1.0", uniqueIP())
	cookieB := loginTestUser(t, e, pool, emailB, "agent-b/2.0", uniqueIP())
	idB := sessionIDForToken(t, pool, cookieB.Value)

	rec := getAuthed(t, e, http.MethodDelete, "/api/v1/auth/sessions/"+idB, cookieA)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete foreign session: status = %d, want 404", rec.Code)
	}
	// B's session must be untouched.
	var revokedAt *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT revoked_at FROM sessions WHERE id = $1`, idB).Scan(&revokedAt); err != nil {
		t.Fatalf("query session: %v", err)
	}
	if revokedAt != nil {
		t.Errorf("foreign session was revoked by another user")
	}
	if r := getAuthed(t, e, http.MethodGet, "/api/v1/me", cookieB); r.Code != http.StatusOK {
		t.Fatalf("victim session /me: status = %d, want 200", r.Code)
	}
}

func TestDeleteSessionMalformedID404(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)

	email := uniqueEmail("h-sess-del-bad")
	cookie := loginTestUser(t, e, pool, email, "agent-a/1.0", uniqueIP())
	rec := getAuthed(t, e, http.MethodDelete, "/api/v1/auth/sessions/not-a-uuid", cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("malformed id: status = %d, want 404 (never 500)", rec.Code)
	}
}

func TestDeleteCurrentSessionClearsCookie(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)

	email := uniqueEmail("h-sess-del-self")
	cookie := loginTestUser(t, e, pool, email, "agent-a/1.0", uniqueIP())
	id := sessionIDForToken(t, pool, cookie.Value)

	rec := getAuthed(t, e, http.MethodDelete, "/api/v1/auth/sessions/"+id, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete self: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var cleared *http.Cookie
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == auth.SessionCookieName {
			cleared = ck
		}
	}
	if cleared == nil || cleared.MaxAge > 0 {
		t.Errorf("deleting the current session must clear its cookie")
	}
	if r := getAuthed(t, e, http.MethodGet, "/api/v1/me", cookie); r.Code != http.StatusUnauthorized {
		t.Fatalf("after self-delete: /me status = %d, want 401", r.Code)
	}
}

func TestDeleteSessionUnauthenticated401(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/auth/sessions/00000000-0000-0000-0000-000000000000", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestLastSeenAtRefreshesWhenStale(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)

	email := uniqueEmail("h-sess-touch")
	cookie := loginTestUser(t, e, pool, email, "agent-a/1.0", uniqueIP())
	if _, err := pool.Exec(ctx,
		`UPDATE sessions SET last_seen_at = now() - INTERVAL '10 minutes' WHERE token_hash = $1`,
		sessionTokenHash(cookie.Value)); err != nil {
		t.Fatalf("age last_seen_at: %v", err)
	}

	before := time.Now()
	if rec := getAuthed(t, e, http.MethodGet, "/api/v1/me", cookie); rec.Code != http.StatusOK {
		t.Fatalf("/me: status = %d, want 200", rec.Code)
	}
	var lastSeen time.Time
	if err := pool.QueryRow(ctx,
		`SELECT last_seen_at FROM sessions WHERE token_hash = $1`,
		sessionTokenHash(cookie.Value)).Scan(&lastSeen); err != nil {
		t.Fatalf("query last_seen_at: %v", err)
	}
	if lastSeen.Before(before.Add(-time.Minute)) {
		t.Errorf("stale last_seen_at was not refreshed: %v", lastSeen)
	}
}

func TestLastSeenAtUntouchedWhenFresh(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)

	email := uniqueEmail("h-sess-notouch")
	cookie := loginTestUser(t, e, pool, email, "agent-a/1.0", uniqueIP())
	// xmin identifies the row's tuple version: any UPDATE — even one that
	// writes back the same value — allocates a new version with a new xmin.
	// Equal xmin before/after proves the hot path issued zero writes.
	var before time.Time
	var beforeXmin string
	if err := pool.QueryRow(ctx,
		`SELECT last_seen_at, xmin::text FROM sessions WHERE token_hash = $1`,
		sessionTokenHash(cookie.Value)).Scan(&before, &beforeXmin); err != nil {
		t.Fatalf("query last_seen_at: %v", err)
	}

	if rec := getAuthed(t, e, http.MethodGet, "/api/v1/me", cookie); rec.Code != http.StatusOK {
		t.Fatalf("/me: status = %d, want 200", rec.Code)
	}
	var after time.Time
	var afterXmin string
	if err := pool.QueryRow(ctx,
		`SELECT last_seen_at, xmin::text FROM sessions WHERE token_hash = $1`,
		sessionTokenHash(cookie.Value)).Scan(&after, &afterXmin); err != nil {
		t.Fatalf("query last_seen_at: %v", err)
	}
	if !after.Equal(before) {
		t.Errorf("fresh last_seen_at was rewritten (%v -> %v); the refresh must be conditional", before, after)
	}
	if afterXmin != beforeXmin {
		t.Errorf("fresh session row was physically rewritten (xmin %s -> %s); the hot path must issue zero writes", beforeXmin, afterXmin)
	}
}
