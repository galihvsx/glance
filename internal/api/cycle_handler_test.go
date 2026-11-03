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

// TestCycleRemoveIssuesHTTP (C132T0): DELETE .../cycles/{cycleID}/issues
// — 200 + {"removed": n} shape, 400s, 404s, guest 403 mirroring the add
// endpoint, and DELETE /:cycleID itself untouched (still 204).
func TestCycleRemoveIssuesHTTP(t *testing.T) {
	pool := newTestPool(t)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterCycleRoutes(e, &IssueHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("cycle-rm-http"), "test-agent", uniqueIP())
	slug := uniqueSlug("cycle-rm-http")
	createWorkspaceHTTP(t, e, cookie, "Cycle Co", slug)
	ident := uniqueProjectIdentifier("CR")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	issueBase := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"
	cycleBase := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/cycles"

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
	cycleURL := cycleBase + "/" + created.ID
	memberCount := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(t.Context(),
			`SELECT COUNT(*) FROM cycle_issues WHERE cycle_id = $1::uuid`, created.ID).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}

	iss1 := createIssueHTTP(t, e, cookie, issueBase, "in cycle one")
	iss2 := createIssueHTTP(t, e, cookie, issueBase, "in cycle two")
	nonMember := createIssueHTTP(t, e, cookie, issueBase, "never added")

	// Add both via the existing POST route, then remove → 200 + shape.
	rec = postAuthedJSON(t, e, http.MethodPost, cycleURL+"/issues", cookie,
		`{"issue_ids":["`+iss1+`","`+iss2+`"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("add issues: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if memberCount() != 2 {
		t.Fatalf("setup count = %d, want 2", memberCount())
	}
	rec = postAuthedJSON(t, e, http.MethodDelete, cycleURL+"/issues", cookie,
		`{"issue_ids":["`+iss1+`"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove issue: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var removed struct {
		Removed int `json:"removed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &removed); err != nil {
		t.Fatalf("decode removed: %v", err)
	}
	if removed.Removed != 1 {
		t.Fatalf("removed = %d, want 1 (body: %s)", removed.Removed, rec.Body.String())
	}
	if memberCount() != 1 {
		t.Fatalf("after remove count = %d, want 1", memberCount())
	}

	// Removing a non-member is a 200 no-op, still echoing the count.
	rec = postAuthedJSON(t, e, http.MethodDelete, cycleURL+"/issues", cookie,
		`{"issue_ids":["`+nonMember+`"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove non-member: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if memberCount() != 1 {
		t.Fatalf("after non-member remove count = %d, want 1", memberCount())
	}

	// 400s: empty ids, malformed issue id, malformed cycle id.
	for _, tc := range []struct {
		name string
		path string
		body string
	}{
		{"empty ids", cycleURL + "/issues", `{"issue_ids":[]}`},
		{"malformed issue", cycleURL + "/issues", `{"issue_ids":["not-a-uuid"]}`},
		{"malformed cycle", cycleBase + "/not-a-uuid/issues", `{"issue_ids":["` + iss2 + `"]}`},
	} {
		rec = postAuthedJSON(t, e, http.MethodDelete, tc.path, cookie, tc.body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400 (body: %s)", tc.name, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"code":"bad_request"`) {
			t.Fatalf("%s: missing bad_request envelope (body: %s)", tc.name, rec.Body.String())
		}
	}
	// 404s: unknown cycle, unknown issue, foreign-project issue.
	ident2 := uniqueProjectIdentifier("CR")
	createProjectHTTP(t, e, cookie, slug, "Engineering 2", ident2)
	foreign := createIssueHTTP(t, e, cookie,
		"/api/v1/workspaces/"+slug+"/projects/"+ident2+"/issues", "foreign")
	for _, tc := range []struct {
		name string
		path string
		body string
	}{
		{"unknown cycle", cycleBase + "/00000000-0000-0000-0000-000000000000/issues", `{"issue_ids":["` + iss2 + `"]}`},
		{"unknown issue", cycleURL + "/issues", `{"issue_ids":["00000000-0000-4000-8000-000000000000"]}`},
		{"foreign issue", cycleURL + "/issues", `{"issue_ids":["` + foreign + `"]}`},
	} {
		rec = postAuthedJSON(t, e, http.MethodDelete, tc.path, cookie, tc.body)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want 404 (body: %s)", tc.name, rec.Code, rec.Body.String())
		}
	}
	if memberCount() != 1 {
		t.Fatalf("after error cases count = %d, want 1 (i2 untouched)", memberCount())
	}

	// Guest (5) gets 403, mirroring the add endpoint's Member (15)+ gate.
	guest := loginTestUser(t, e, pool, uniqueEmail("cycle-rm-guest"), "test-agent", uniqueIP())
	var wsID, guestID string
	if err := pool.QueryRow(t.Context(), `SELECT id::text FROM workspaces WHERE slug = $1`, slug).Scan(&wsID); err != nil {
		t.Fatalf("workspace id: %v", err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT id::text FROM users WHERE email = $1`, guestEmailOf(t, pool, guest)).Scan(&guestID); err != nil {
		t.Fatalf("guest id: %v", err)
	}
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1::uuid, $2::uuid, 5)`, wsID, guestID); err != nil {
		t.Fatalf("add guest: %v", err)
	}
	rec = postAuthedJSON(t, e, http.MethodDelete, cycleURL+"/issues", guest,
		`{"issue_ids":["`+iss2+`"]}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest remove: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}

	// Remove the last member, then DELETE /:cycleID itself still works
	// (204) — the new issues route did not shadow delete-cycle.
	rec = postAuthedJSON(t, e, http.MethodDelete, cycleURL+"/issues", cookie,
		`{"issue_ids":["`+iss2+`"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove last: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodDelete, cycleURL, cookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete cycle: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
}
