package api

// PATCH /api/v1/auth/me tests (C6T7): display-name update — 200 with the
// refreshed user, 400 on blank/oversized names, 401 without a session.
// Real test database, no skips.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"glance/internal/config"
)

func testAuthHandler(pool *pgxpool.Pool) *AuthHandler {
	return &AuthHandler{
		Pool:   pool,
		Config: &config.Config{OTPPepper: "test-pepper-do-not-use-in-prod"},
	}
}

func TestUpdateMe(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterAuthRoutes(e, testAuthHandler(pool))

	cookie := loginTestUser(t, e, pool, uniqueEmail("me-update"), "test-agent", uniqueIP())

	// Happy path → 200 with the updated name.
	rec := postAuthedJSON(t, e, http.MethodPatch, "/api/v1/auth/me", cookie,
		`{"name":"  Galih  "}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update name: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var me struct {
		Name  *string `json:"name"`
		Email string  `json:"email"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	if me.Name == nil || *me.Name != "Galih" {
		t.Fatalf("name = %v, want Galih (trimmed)", me.Name)
	}

	// Blank → 400.
	rec = postAuthedJSON(t, e, http.MethodPatch, "/api/v1/auth/me", cookie,
		`{"name":"   "}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("blank name: status = %d, want 400", rec.Code)
	}

	// Oversized → 400.
	rec = postAuthedJSON(t, e, http.MethodPatch, "/api/v1/auth/me", cookie,
		`{"name":"`+strings.Repeat("x", 101)+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("long name: status = %d, want 400", rec.Code)
	}

	// No session → 401.
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/auth/me",
		strings.NewReader(`{"name":"Nobody"}`))
	rec2 := httptest.NewRecorder()
	e.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: status = %d, want 401", rec2.Code)
	}
}

// TestListSessionsMarksCurrent: the caller's own session carries
// current=true; a second session does not.
func TestListSessionsMarksCurrent(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterAuthRoutes(e, testAuthHandler(pool))

	email := uniqueEmail("sess-current")
	cookie1 := loginTestUser(t, e, pool, email, "test-agent/1", uniqueIP())
	_ = loginTestUser(t, e, pool, email, "test-agent/2", uniqueIP())

	rec := getAuthed(t, e, http.MethodGet, "/api/v1/auth/sessions", cookie1)
	if rec.Code != http.StatusOK {
		t.Fatalf("list sessions: status = %d, want 200", rec.Code)
	}
	var listed struct {
		Sessions []struct {
			ID      string `json:"id"`
			Current bool   `json:"current"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode sessions: %v", err)
	}
	if len(listed.Sessions) != 2 {
		t.Fatalf("sessions = %d, want 2", len(listed.Sessions))
	}
	current := 0
	for _, s := range listed.Sessions {
		if s.Current {
			current++
		}
	}
	if current != 1 {
		t.Fatalf("current flags = %d, want exactly 1", current)
	}
}
