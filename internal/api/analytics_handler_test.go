package api

// Analytics HTTP endpoint tests (C4T3): summary shape, trends
// validation, cycle burndown routing, and the 401/404 envelope
// behavior. Real test database, no skips.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
)

func setupAnalyticsHTTP(t *testing.T, e *echo.Echo, cookie *http.Cookie) (slug, ident, analyticsBase string) {
	t.Helper()
	slug = uniqueSlug("analytics-http")
	createWorkspaceHTTP(t, e, cookie, "Analytics Co", slug)
	ident = uniqueProjectIdentifier("ANL")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	analyticsBase = "/api/v1/workspaces/" + slug + "/projects/" + ident + "/analytics"
	return slug, ident, analyticsBase
}

func TestAnalyticsHTTPSummary(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterAnalyticsRoutes(e, &IssueHandler{Pool: pool})
	cookie := loginTestUser(t, e, pool, uniqueEmail("analytics-http"), "test-agent", uniqueIP())
	_, _, analyticsBase := setupAnalyticsHTTP(t, e, cookie)

	rec := getAuthed(t, e, http.MethodGet, analyticsBase+"/summary", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("summary: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var s struct {
		ByState       map[string]int64 `json:"by_state"`
		ByPriority    map[string]int64 `json:"by_priority"`
		ByLabel       map[string]int64 `json:"by_label"`
		OverdueCount  int64            `json:"overdue_count"`
		EstimateTotal int64            `json:"estimate_total"`
		EstimateDone  int64            `json:"estimate_done"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatalf("decode summary: %v (body: %s)", err, rec.Body.String())
	}
	// Fresh project: no issues, but the shape must be complete.
	if s.ByState == nil || s.ByPriority == nil || s.ByLabel == nil {
		t.Fatalf("summary maps must be non-nil: %s", rec.Body.String())
	}
	if len(s.ByState) != 0 || s.OverdueCount != 0 || s.EstimateTotal != 0 {
		t.Fatalf("fresh project should be empty: %s", rec.Body.String())
	}
}

func TestAnalyticsHTTPTrends(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterAnalyticsRoutes(e, &IssueHandler{Pool: pool})
	cookie := loginTestUser(t, e, pool, uniqueEmail("analytics-http"), "test-agent", uniqueIP())
	_, _, analyticsBase := setupAnalyticsHTTP(t, e, cookie)

	// Default window: 30 days.
	rec := getAuthed(t, e, http.MethodGet, analyticsBase+"/trends", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("trends: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var tr struct {
		Days []struct {
			Date    string `json:"date"`
			Created int64  `json:"created"`
			Closed  int64  `json:"closed"`
		} `json:"days"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &tr); err != nil {
		t.Fatalf("decode trends: %v (body: %s)", err, rec.Body.String())
	}
	if len(tr.Days) != 30 {
		t.Fatalf("len(days) = %d, want 30", len(tr.Days))
	}

	// Explicit window.
	rec = getAuthed(t, e, http.MethodGet, analyticsBase+"/trends?days=7", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("trends?days=7: status = %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &tr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(tr.Days) != 7 {
		t.Fatalf("len(days) = %d, want 7", len(tr.Days))
	}

	// Cap: 500 → 90.
	rec = getAuthed(t, e, http.MethodGet, analyticsBase+"/trends?days=500", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("trends?days=500: status = %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &tr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(tr.Days) != 90 {
		t.Fatalf("len(days) = %d, want 90 (capped)", len(tr.Days))
	}

	// Malformed → 400 envelope.
	for _, bad := range []string{"abc", "0", "-3"} {
		rec = getAuthed(t, e, http.MethodGet, analyticsBase+"/trends?days="+bad, cookie)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("trends?days=%s: status = %d, want 400", bad, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `"code":"bad_request"`) {
			t.Fatalf("400 envelope: body=%s", rec.Body.String())
		}
	}
}

func TestAnalyticsHTTPCycleBurndown(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterCycleRoutes(e, &IssueHandler{Pool: pool})
	RegisterAnalyticsRoutes(e, &IssueHandler{Pool: pool})
	cookie := loginTestUser(t, e, pool, uniqueEmail("analytics-http"), "test-agent", uniqueIP())
	slug, ident, analyticsBase := setupAnalyticsHTTP(t, e, cookie)
	cycleBase := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/cycles"

	// Create a cycle via HTTP.
	rec := postAuthedJSON(t, e, http.MethodPost, cycleBase, cookie,
		`{"name":"S1","start_date":"2026-09-01","end_date":"2026-09-07"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create cycle: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode cycle: %v", err)
	}

	rec = getAuthed(t, e, http.MethodGet, analyticsBase+"/cycle/"+created.ID+"/burndown", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("burndown: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var bd struct {
		TotalScope int `json:"total_scope"`
		Days       []struct {
			Date              string `json:"date"`
			Remaining         *int   `json:"remaining"`
			RemainingEstimate *int64 `json:"remaining_estimate"`
		} `json:"days"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &bd); err != nil {
		t.Fatalf("decode burndown: %v (body: %s)", err, rec.Body.String())
	}
	if len(bd.Days) != 7 {
		t.Fatalf("len(days) = %d, want 7", len(bd.Days))
	}

	// Unknown cycle → 404; malformed → 400.
	rec = getAuthed(t, e, http.MethodGet, analyticsBase+"/cycle/00000000-0000-0000-0000-000000000000/burndown", cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown cycle: status = %d, want 404", rec.Code)
	}
	rec = getAuthed(t, e, http.MethodGet, analyticsBase+"/cycle/nope/burndown", cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed cycle: status = %d, want 400", rec.Code)
	}
}

func TestAnalyticsHTTPAuth(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterAnalyticsRoutes(e, &IssueHandler{Pool: pool})
	cookie := loginTestUser(t, e, pool, uniqueEmail("analytics-http"), "test-agent", uniqueIP())
	_, _, analyticsBase := setupAnalyticsHTTP(t, e, cookie)

	// Unauthenticated → 401.
	rec := authedReq(t, e, http.MethodGet, analyticsBase+"/summary", nil, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauth summary: status = %d, want 401", rec.Code)
	}

	// Outsider (authenticated, never a member) → 404, not a leak.
	outsider := loginTestUser(t, e, pool, uniqueEmail("analytics-out-http"), "test-agent", uniqueIP())
	rec = getAuthed(t, e, http.MethodGet, analyticsBase+"/summary", outsider)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("outsider summary: status = %d, want 404", rec.Code)
	}
	rec = getAuthed(t, e, http.MethodGet, analyticsBase+"/trends", outsider)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("outsider trends: status = %d, want 404", rec.Code)
	}
}
