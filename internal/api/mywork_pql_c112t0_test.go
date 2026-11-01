package api

// GET /my-issues ?pql= HTTP tests (C112T0): the endpoint contract —
// the predicate ANDs with the filter, a probe failure 400s with
// message + position (the C91 contract), blank ?pql= degrades to the
// unfiltered filter list, and limit keeps its 1..200 behavior.
// Multi-match set semantics and the taxonomy-leak rule live in the
// service tests (mywork_pql_c112t0_test.go), where the private-project
// fixture is direct; this file pins the HTTP envelope.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestMyIssuesHTTPPQL(t *testing.T) {
	pool := newTestPool(t)
	e := testServer(t, pool)
	RegisterAuthRoutes(e, testAuthHandler(pool))
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterMyWorkRoutes(e, &MyWorkHandler{Pool: pool})

	aliceCookie := loginTestUser(t, e, pool, uniqueEmail("mw-pql-alice"), "test-agent", uniqueIP())
	slug := uniqueSlug("mw-pql")
	createWorkspaceHTTP(t, e, aliceCookie, "Acme", slug)
	ident := uniqueProjectIdentifier("MP")
	createProjectHTTP(t, e, aliceCookie, slug, "Engineering", ident)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident
	myBase := "/api/v1/workspaces/" + slug + "/my-issues"

	bobEmail := uniqueEmail("mw-pql-bob")
	bobCookie := loginTestUser(t, e, pool, bobEmail, "test-agent", uniqueIP())
	ctx := context.Background()
	var bobID, wsID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users WHERE email = $1`, bobEmail).Scan(&bobID); err != nil {
		t.Fatalf("bob id: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM workspaces WHERE slug = $1`, slug).Scan(&wsID); err != nil {
		t.Fatalf("workspace id: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1::uuid, $2::uuid, 15)`,
		wsID, bobID); err != nil {
		t.Fatalf("bob membership: %v", err)
	}

	create := func(cookie *http.Cookie, name string) string {
		t.Helper()
		rec := postAuthedJSON(t, e, http.MethodPost, base+"/issues", cookie,
			`{"name":"`+name+`"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %q: status = %d (body: %s)", name, rec.Code, rec.Body.String())
		}
		var iss struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &iss); err != nil {
			t.Fatalf("decode issue: %v", err)
		}
		return iss.ID
	}
	setState := func(issueID, stateName string) {
		t.Helper()
		if _, err := pool.Exec(ctx,
			`UPDATE issues SET state_id = (SELECT s.id FROM states s JOIN projects p ON p.id = s.project_id
			 WHERE p.identifier = $2 AND s.name = $3) WHERE id = $1::uuid`,
			issueID, ident, stateName); err != nil {
			t.Fatalf("set state %q: %v", stateName, err)
		}
	}
	assign := func(issueID string) {
		t.Helper()
		if _, err := pool.Exec(ctx,
			`INSERT INTO issue_assignees (issue_id, user_id) VALUES ($1::uuid, $2::uuid)`,
			issueID, bobID); err != nil {
			t.Fatalf("assign: %v", err)
		}
	}
	fetchNames := func(cookie *http.Cookie, path string) []string {
		t.Helper()
		rec := getAuthed(t, e, http.MethodGet, path, cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: status = %d, want 200 (body: %s)", path, rec.Code, rec.Body.String())
		}
		var body struct {
			Issues []struct {
				Name string `json:"name"`
			} `json:"issues"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		names := make([]string, 0, len(body.Issues))
		for _, it := range body.Issues {
			names = append(names, it.Name)
		}
		return names
	}
	wantNames := func(got []string, want ...string) {
		t.Helper()
		joined := strings.Join(got, "|")
		for _, w := range want {
			found := false
			for _, g := range got {
				if g == w {
					found = true
				}
			}
			if !found {
				t.Fatalf("got %v, want it to contain %q", got, want)
			}
		}
		if len(got) != len(want) {
			t.Fatalf("got %v (%d rows), want exactly %v", got, len(got), want)
		}
		_ = joined
	}

	alpha := create(aliceCookie, "alpha")
	beta := create(aliceCookie, "beta")
	gamma := create(bobCookie, "gamma")
	setState(alpha, "Todo")
	setState(beta, "Done")
	setState(gamma, "Todo")
	assign(alpha)
	assign(beta)

	// PQL ANDs with the assigned filter: only the assigned Todo issue.
	wantNames(fetchNames(bobCookie, myBase+"?filter=assigned&pql="+url.QueryEscape(`state = Todo`)), "alpha")
	// Same predicate under created: only Bob's own Todo issue.
	wantNames(fetchNames(bobCookie, myBase+"?filter=created&pql="+url.QueryEscape(`state = Todo`)), "gamma")
	// Blank pql degrades to the unfiltered assigned list.
	wantNames(fetchNames(bobCookie, myBase+"?filter=assigned&pql="), "alpha", "beta")
	// limit keeps its 1..200 behavior under PQL.
	if got := fetchNames(bobCookie, myBase+"?filter=assigned&pql="+url.QueryEscape(`state in (Todo, Done)`)+"&limit=1"); len(got) != 1 {
		t.Fatalf("pql + limit=1: got %d rows, want 1", len(got))
	}

	// Probe failure: unknown name 400s with message + position.
	rec := getAuthed(t, e, http.MethodGet, myBase+"?filter=assigned&pql="+url.QueryEscape(`state = NoSuchState`), bobCookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown state: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	msg, pos, hasPos := pqlErrorBody(t, rec.Body.String())
	if !strings.Contains(msg, `unknown state "NoSuchState"`) {
		t.Fatalf("error message = %q, want unknown-state text", msg)
	}
	if !hasPos || pos <= 0 {
		t.Fatalf("error position = %v (hasPos=%v), want a positive column", pos, hasPos)
	}
	// Parse failure likewise 400s with position.
	rec = getAuthed(t, e, http.MethodGet, myBase+"?filter=assigned&pql="+url.QueryEscape(`state == Todo`), bobCookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("parse error: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	if _, pos, hasPos := pqlErrorBody(t, rec.Body.String()); !hasPos || pos <= 0 {
		t.Fatalf("parse error position = %v (hasPos=%v), want a positive column", pos, hasPos)
	}
}
