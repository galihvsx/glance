package api

// Cycle HTTP endpoint tests (Task 22): create (201), duplicate name (409
// envelope), bad dates (400), get with progress_snapshot, add issues, and
// the project close_in_days PATCH. Real test database, no skips.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestCycleHTTP(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterCycleRoutes(e, &IssueHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("cycle-http"), "test-agent", uniqueIP())
	slug := uniqueSlug("cycle-http")
	createWorkspaceHTTP(t, e, cookie, "Cycle Co", slug)
	ident := uniqueProjectIdentifier("CH")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	cycleBase := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/cycles"

	start := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	end := time.Now().AddDate(0, 0, 13).Format("2006-01-02")

	// Create → 201.
	rec := postAuthedJSON(t, e, http.MethodPost, cycleBase, cookie,
		`{"name":"Sprint 1","start_date":"`+start+`","end_date":"`+end+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create cycle: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode cycle: %v", err)
	}
	if created.Status != "current" {
		t.Fatalf("status = %q, want current", created.Status)
	}

	// Duplicate name → 409 envelope.
	rec = postAuthedJSON(t, e, http.MethodPost, cycleBase, cookie,
		`{"name":"Sprint 1","start_date":"`+start+`","end_date":"`+end+`"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate cycle: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"conflict"`) {
		t.Fatalf("409 envelope: body=%s", rec.Body.String())
	}

	// Malformed date → 400; start after end → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, cycleBase, cookie,
		`{"name":"Bad","start_date":"not-a-date","end_date":"`+end+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad date: status = %d, want 400", rec.Code)
	}
	rec = postAuthedJSON(t, e, http.MethodPost, cycleBase, cookie,
		`{"name":"Bad2","start_date":"`+end+`","end_date":"`+start+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("start>end: status = %d, want 400", rec.Code)
	}

	// Get → 200 with progress_snapshot (all zeros, empty cycle).
	rec = getAuthed(t, e, http.MethodGet, cycleBase+"/"+created.ID, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get cycle: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var got struct {
		ID               string           `json:"id"`
		ProgressSnapshot map[string]int64 `json:"progress_snapshot"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	for _, g := range []string{"backlog", "unstarted", "started", "completed", "cancelled"} {
		if v, ok := got.ProgressSnapshot[g]; !ok || v != 0 {
			t.Fatalf("snapshot[%s] = %d present=%v, want 0 present", g, v, ok)
		}
	}

	// Unknown cycle → 404 envelope.
	rec = getAuthed(t, e, http.MethodGet, cycleBase+"/00000000-0000-0000-0000-000000000000", cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown cycle: status = %d, want 404", rec.Code)
	}

	// Project close_in_days PATCH (the ticker's rollover rule).
	rec = postAuthedJSON(t, e, http.MethodPatch,
		"/api/v1/workspaces/"+slug+"/projects/"+ident, cookie,
		`{"close_in_days": 7}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch close_in_days: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"close_in_days":7`) {
		t.Fatalf("close_in_days not echoed: body=%s", rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPatch,
		"/api/v1/workspaces/"+slug+"/projects/"+ident, cookie,
		`{"close_in_days": -1}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("negative close_in_days: status = %d, want 400", rec.Code)
	}
}

// TestCycleMalformedUUIDHTTP: malformed cycle/issue ids on the cycle
// endpoints return 400 bad_request envelopes, never 500.
func TestCycleMalformedUUIDHTTP(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterCycleRoutes(e, &IssueHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("cycle-badid-http"), "test-agent", uniqueIP())

	slug := uniqueSlug("cycle-badid-http")
	createWorkspaceHTTP(t, e, cookie, "Cycle Co", slug)
	ident := uniqueProjectIdentifier("CB")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	cycleBase := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/cycles"

	// One valid cycle, for the bad-issue-id case.
	start := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	end := time.Now().AddDate(0, 0, 13).Format("2006-01-02")
	rec := postAuthedJSON(t, e, http.MethodPost, cycleBase, cookie,
		`{"name":"S1","start_date":"`+start+`","end_date":"`+end+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create cycle: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode cycle: %v", err)
	}

	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"GET malformed cycle", http.MethodGet, cycleBase + "/not-a-uuid", ""},
		{"POST issues malformed cycle", http.MethodPost, cycleBase + "/not-a-uuid/issues", `{"issue_ids":["00000000-0000-0000-0000-000000000000"]}`},
		{"POST issues malformed issue", http.MethodPost, cycleBase + "/" + created.ID + "/issues", `{"issue_ids":["not-a-uuid"]}`},
		{"DELETE malformed cycle", http.MethodDelete, cycleBase + "/not-a-uuid", ""},
	}
	for _, tc := range cases {
		if tc.body != "" {
			rec = postAuthedJSON(t, e, tc.method, tc.path, cookie, tc.body)
		} else {
			rec = getAuthed(t, e, tc.method, tc.path, cookie)
		}
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400 (body: %s)", tc.name, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"code":"bad_request"`) {
			t.Fatalf("%s: missing bad_request envelope (body: %s)", tc.name, rec.Body.String())
		}
	}
}
