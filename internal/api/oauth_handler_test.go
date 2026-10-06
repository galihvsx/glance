package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"

	"glance/internal/config"
)

func TestOAuthLoginUnconfiguredProvider404(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool) // config has no OAuth credentials at all

	for _, path := range []string{
		"/api/v1/auth/oauth/google/login",
		"/api/v1/auth/oauth/github/login",
		"/api/v1/auth/oauth/google/callback?state=x&code=y",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404 (unconfigured provider)", path, rec.Code)
		}
	}
}

func TestOAuthLoginUnknownProvider404(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/twitter/login", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET unknown provider = %d, want 404", rec.Code)
	}
}

// TestOAuthLoginRedirectSetsStateCookie: a configured provider 302s to the
// provider with an unguessable state and stores the signed state in a
// single-use HttpOnly cookie.
func TestOAuthLoginRedirectSetsStateCookie(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := echo.New()
	h := &AuthHandler{Pool: pool, Config: &config.Config{
		OTPPepper:          "test-pepper",
		OAuthStateSecret:   "test-state-secret",
		AppURL:             "https://app.example",
		GoogleClientID:     "test-google-id",
		GoogleClientSecret: "test-google-secret",
	}}
	RegisterAuthRoutes(e, h)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/google/login", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("GET login = %d, want 302", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "accounts.google.com") {
		t.Errorf("redirect = %q, want accounts.google.com", loc)
	}
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	raw := u.Query().Get("state")
	if len(raw) < 32 {
		t.Errorf("state too short: %q", raw)
	}
	if !strings.Contains(loc, "redirect_uri="+url.QueryEscape("https://app.example/api/v1/auth/oauth/google/callback")) {
		t.Errorf("redirect_uri wrong in %q", loc)
	}

	var stateCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "glance_oauth_state" {
			stateCookie = c
		}
	}
	if stateCookie == nil {
		t.Fatal("glance_oauth_state cookie not set")
	}
	if !stateCookie.HttpOnly || !stateCookie.Secure || stateCookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("state cookie attributes wrong: %+v", stateCookie)
	}
	if !strings.Contains(stateCookie.Value, ".") || !strings.HasPrefix(stateCookie.Value, raw+".") {
		t.Error("state cookie must be the signed form of the raw state")
	}

	// Callback with a forged state (cookie valid, query state wrong) → 400.
	forged := httptest.NewRequest(http.MethodGet,
		"/api/v1/auth/oauth/google/callback?state=forged&code=x", nil)
	forged.AddCookie(stateCookie)
	forgedRec := httptest.NewRecorder()
	e.ServeHTTP(forgedRec, forged)
	if forgedRec.Code != http.StatusBadRequest {
		t.Errorf("forged state callback = %d, want 400", forgedRec.Code)
	}
	// The state cookie must be consumed (cleared) even on failure.
	cleared := false
	for _, c := range forgedRec.Result().Cookies() {
		if c.Name == "glance_oauth_state" && c.MaxAge <= 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("state cookie not cleared after failed callback (must be single-use)")
	}

	// Callback with no state cookie at all → 400.
	naked := httptest.NewRequest(http.MethodGet,
		"/api/v1/auth/oauth/google/callback?state="+raw+"&code=x", nil)
	nakedRec := httptest.NewRecorder()
	e.ServeHTTP(nakedRec, naked)
	if nakedRec.Code != http.StatusBadRequest {
		t.Errorf("missing-cookie callback = %d, want 400", nakedRec.Code)
	}
}
