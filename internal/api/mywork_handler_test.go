package api

// GET /api/v1/workspaces/{slug}/my-issues tests (C6T3): the three filters,
// default filter, invalid filter/limit, and the non-member 404. Real test
// database, no skips.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestMyIssuesHTTP(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterAuthRoutes(e, testAuthHandler(pool))
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterMyWorkRoutes(e, &MyWorkHandler{Pool: pool})

	aliceEmail := uniqueEmail("mywork-alice")
	aliceCookie := loginTestUser(t, e, pool, aliceEmail, "test-agent", uniqueIP())
	slug := uniqueSlug("mywork")
	createWorkspaceHTTP(t, e, aliceCookie, "Acme", slug)
	ident := uniqueProjectIdentifier("MW")
	createProjectHTTP(t, e, aliceCookie, slug, "Engineering", ident)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident
	myBase := "/api/v1/workspaces/" + slug + "/my-issues"

	// Alice creates an issue; Bob (member) gets assigned + subscribes.
	bobEmail := uniqueEmail("mywork-bob")
	bobCookie := loginTestUser(t, e, pool, bobEmail, "test-agent", uniqueIP())
	var bobID string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM users WHERE email = $1`, bobEmail).Scan(&bobID); err != nil {
		t.Fatalf("bob id: %v", err)
	}
	var wsID string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM workspaces WHERE slug = $1`, slug).Scan(&wsID); err != nil {
		t.Fatalf("workspace id: %v", err)
	}
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1::uuid, $2::uuid, 15)`,
		wsID, bobID); err != nil {
		t.Fatalf("bob membership: %v", err)
	}

	rec := postAuthedJSON(t, e, http.MethodPost, base+"/issues", aliceCookie,
		`{"name":"bob task"}`)
	var iss struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &iss); err != nil {
		t.Fatalf("decode issue: %v", err)
	}
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`INSERT INTO issue_assignees (issue_id, user_id) VALUES ($1::uuid, $2::uuid)`,
		iss.ID, bobID); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO issue_subscribers (issue_id, user_id) VALUES ($1::uuid, $2::uuid)`,
		iss.ID, bobID); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	fetch := func(cookie *http.Cookie, path string) []map[string]any {
		t.Helper()
		rec := getAuthed(t, e, http.MethodGet, path, cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: status = %d, want 200 (body: %s)", path, rec.Code, rec.Body.String())
		}
		var body struct {
			Issues []map[string]any `json:"issues"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return body.Issues
	}

	// Bob / assigned → the issue, with display/state/project fields.
	items := fetch(bobCookie, myBase+"?filter=assigned")
	if len(items) != 1 {
		t.Fatalf("assigned = %d, want 1", len(items))
	}
	for _, k := range []string{"id", "display_id", "name", "priority", "state_name", "project_identifier", "updated_at"} {
		if _, ok := items[0][k]; !ok {
			t.Fatalf("item missing %q: %v", k, items[0])
		}
	}

	// Bob / watched → the issue; Bob / created → none.
	if items := fetch(bobCookie, myBase+"?filter=watched"); len(items) != 1 {
		t.Fatalf("watched = %d, want 1", len(items))
	}
	if items := fetch(bobCookie, myBase+"?filter=created"); len(items) != 0 {
		t.Fatalf("created = %d, want 0", len(items))
	}
	// Default filter = assigned.
	if items := fetch(bobCookie, myBase); len(items) != 1 {
		t.Fatalf("default = %d, want 1", len(items))
	}

	// Invalid filter → 400.
	rec = getAuthed(t, e, http.MethodGet, myBase+"?filter=bogus", bobCookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad filter: status = %d, want 400", rec.Code)
	}
	// Invalid limit → 400.
	rec = getAuthed(t, e, http.MethodGet, myBase+"?limit=abc", bobCookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad limit: status = %d, want 400", rec.Code)
	}

	// Outsider (no membership) → 404.
	strangerCookie := loginTestUser(t, e, pool, uniqueEmail("mywork-stranger"), "test-agent", uniqueIP())
	rec = getAuthed(t, e, http.MethodGet, myBase, strangerCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("outsider: status = %d, want 404", rec.Code)
	}
}
