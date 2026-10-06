package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"glance/internal/config"
)

func TestTokenBearerAuthEndToEnd(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := tokenTestServerFull(t, pool)

	email := uniqueEmail("tok-e2e")
	cookie := loginTestUser(t, e, pool, email, "test-agent/1.0", uniqueIP())

	// Mint a token via session auth.
	plaintext := createTokenViaAPI(t, e, cookie, "ci", []string{"read", "write"})

	// Use it on a real protected route.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces", nil)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /workspaces with bearer: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	// And on /me — tokens authenticate as their owner everywhere.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /me with bearer: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var me struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil {
		t.Fatalf("decode /me: %v", err)
	}
	if me.Email != email {
		t.Fatalf("/me email = %q, want %q", me.Email, email)
	}
}

func TestTokenRevokedGives401(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := tokenTestServerFull(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("tok-rev"), "test-agent/1.0", uniqueIP())
	id, plaintext := createTokenViaAPIWithID(t, e, cookie, "ci", []string{"read", "write"})

	// Revoke it.
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/tokens/"+id, nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent && rec.Code != http.StatusOK {
		t.Fatalf("revoke: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	// Bearer on a protected route → 401, not 403.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/workspaces", nil)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked bearer: status = %d, want 401 (body: %s)", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "unauthorized")
}

func TestTokenWrongScopeGives403(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := tokenTestServerFull(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("tok-scope"), "test-agent/1.0", uniqueIP())
	plaintext := createTokenViaAPI(t, e, cookie, "readonly", []string{"read"})

	// Reads are fine.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces", nil)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("read-scoped GET: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	// Mutations need write → 403.
	body := fmt.Sprintf(`{"name":%q,"slug":%q}`, "Scope Test", "scope-"+uniqueSlugSuffix())
	req = httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("read-scoped POST: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "forbidden")
}

func TestTokenCannotManageTokens(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := tokenTestServerFull(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("tok-mgmt"), "test-agent/1.0", uniqueIP())
	plaintext := createTokenViaAPI(t, e, cookie, "ci", []string{"read", "write"})

	// A token must not mint new tokens — management is session-only.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tokens", strings.NewReader(`{"name":"evil","scopes":["read"]}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bearer minting token: status = %d, want 401 (body: %s)", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/tokens", nil)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bearer listing tokens: status = %d, want 401 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestTokenListOmitsSecrets(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := tokenTestServerFull(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("tok-list"), "test-agent/1.0", uniqueIP())
	plaintext := createTokenViaAPI(t, e, cookie, "ci", []string{"read"})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tokens", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, secret := range []string{plaintext, "token_hash", `"token"`} {
		if strings.Contains(body, secret) {
			t.Fatalf("token list leaks secret %q", secret)
		}
	}
	var list struct {
		Tokens []struct {
			ID     string   `json:"id"`
			Name   string   `json:"name"`
			Scopes []string `json:"scopes"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Tokens) != 1 || list.Tokens[0].Name != "ci" {
		t.Fatalf("list = %+v, want one token named ci", list.Tokens)
	}
}

func TestUnknownBearerTokenGives401(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := tokenTestServerFull(t, pool)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces", nil)
	req.Header.Set("Authorization", "Bearer gl_thisisnotarealtoken0000000000000000")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown bearer: status = %d, want 401 (body: %s)", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "unauthorized")
}

func TestCreateTokenValidation(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := tokenTestServerFull(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("tok-val"), "test-agent/1.0", uniqueIP())

	for _, tc := range []struct {
		name string
		body string
	}{
		{"unknown scope", `{"name":"x","scopes":["admin"]}`},
		{"empty scopes", `{"name":"x","scopes":[]}`},
		{"empty name", `{"name":"","scopes":["read"]}`},
		{"bad json", `{"name":`},
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tokens", strings.NewReader(tc.body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400 (body: %s)", tc.name, rec.Code, rec.Body.String())
		}
		assertErrorCode(t, rec, "bad_request")
	}
}

// tokenTestServerFull wires auth + token + workspace routes — everything
// the token HTTP tests exercise.
func tokenTestServerFull(t *testing.T, pool *pgxpool.Pool) *echo.Echo {
	t.Helper()
	e := echo.New()
	ah := &AuthHandler{Pool: pool, Config: &config.Config{OTPPepper: "test-pepper-do-not-use-in-prod"}}
	RegisterAuthRoutes(e, ah)
	th := &TokenHandler{Pool: pool}
	RegisterTokenRoutes(e, th)
	wh := &WorkspaceHandler{Pool: pool}
	RegisterWorkspaceRoutes(e, wh)
	return e
}

// createTokenViaAPI mints a token through the session-authed endpoint and
// returns the plaintext (shown once in the 201 response).
func createTokenViaAPI(t *testing.T, e *echo.Echo, cookie *http.Cookie, name string, scopes []string) string {
	t.Helper()
	_, plaintext := createTokenViaAPIWithID(t, e, cookie, name, scopes)
	return plaintext
}

func createTokenViaAPIWithID(t *testing.T, e *echo.Echo, cookie *http.Cookie, name string, scopes []string) (string, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"name": name, "scopes": scopes})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tokens", strings.NewReader(string(body)))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create token: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created token: %v", err)
	}
	if created.ID == "" || !strings.HasPrefix(created.Token, "gl_") {
		t.Fatalf("created = %+v, want id and gl_-prefixed token", created)
	}
	return created.ID, created.Token
}

// assertErrorCode pins only the machine-readable code of the spec §5
// envelope — the human message is covered by assertErrorMessage elsewhere.
func assertErrorCode(t *testing.T, rec *httptest.ResponseRecorder, wantCode string) {
	t.Helper()
	var decoded struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	if decoded.Error.Code != wantCode {
		t.Fatalf("error code = %q, want %q", decoded.Error.Code, wantCode)
	}
}

func uniqueSlugSuffix() string {
	return fmt.Sprintf("s%d", testSeq.Add(1))
}
