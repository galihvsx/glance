package api

// `project` PQL field HTTP tests (C114T0): on /my-issues the field
// filters the workspace list by project key or name, and on a
// project-scoped list endpoint any `project` comparison 400s with
// message + position. Resolution semantics and the leak rule live
// in the service tests (project_pql_c114t0_test.go); this file pins
// the HTTP envelope.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestProjectPQLHTTP(t *testing.T) {
	pool := newTestPool(t)
	e := testServer(t, pool)
	RegisterAuthRoutes(e, testAuthHandler(pool))
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterMyWorkRoutes(e, &MyWorkHandler{Pool: pool})

	aliceCookie := loginTestUser(t, e, pool, uniqueEmail("pj-pql-alice"), "test-agent", uniqueIP())
	slug := uniqueSlug("pj-pql")
	createWorkspaceHTTP(t, e, aliceCookie, "Acme", slug)
	identA := uniqueProjectIdentifier("PA")
	identB := uniqueProjectIdentifier("PB")
	createProjectHTTP(t, e, aliceCookie, slug, "Alpha", identA)
	createProjectHTTP(t, e, aliceCookie, slug, "Beta", identB)
	baseA := "/api/v1/workspaces/" + slug + "/projects/" + identA
	myBase := "/api/v1/workspaces/" + slug + "/my-issues"

	bobEmail := uniqueEmail("pj-pql-bob")
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

	create := func(cookie *http.Cookie, base, name string) string {
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
	assign := func(issueID string) {
		t.Helper()
		if _, err := pool.Exec(ctx,
			`INSERT INTO issue_assignees (issue_id, user_id) VALUES ($1::uuid, $2::uuid)`,
			issueID, bobID); err != nil {
			t.Fatalf("assign: %v", err)
		}
	}
	fetchMyNames := func(path string) []string {
		t.Helper()
		rec := getAuthed(t, e, http.MethodGet, path, bobCookie)
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

	alpha1 := create(aliceCookie, baseA, "alpha1")
	beta1 := create(aliceCookie, "/api/v1/workspaces/"+slug+"/projects/"+identB, "beta1")
	assign(alpha1)
	assign(beta1)

	// Workspace scope: by identifier and by name, each narrows to
	// that project's assigned issue.
	got := fetchMyNames(myBase + "?filter=assigned&pql=" + url.QueryEscape(`project = `+identA))
	if len(got) != 1 || got[0] != "alpha1" {
		t.Fatalf("project = ident: got %v, want [alpha1]", got)
	}
	got = fetchMyNames(myBase + "?filter=assigned&pql=" + url.QueryEscape(`project = Beta`))
	if len(got) != 1 || got[0] != "beta1" {
		t.Fatalf("project = name: got %v, want [beta1]", got)
	}
	// Unknown project 400s with message + position.
	rec := getAuthed(t, e, http.MethodGet, myBase+"?filter=assigned&pql="+url.QueryEscape(`project = NoSuchProject`), bobCookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown project: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	msg, pos, hasPos := pqlErrorBody(t, rec.Body.String())
	if !strings.Contains(msg, `unknown project "NoSuchProject"`) {
		t.Fatalf("error message = %q, want unknown-project text", msg)
	}
	if !hasPos || pos <= 0 {
		t.Fatalf("error position = %v (hasPos=%v), want a positive column", pos, hasPos)
	}

	// Project scope: any `project` comparison 400s with message +
	// position, for = / != / in alike.
	for _, q := range []string{
		`project = ` + identA,
		`project != ` + identA,
		`project in (` + identA + `, ` + identB + `)`,
	} {
		rec := getAuthed(t, e, http.MethodGet, baseA+"/issues?pql="+url.QueryEscape(q), aliceCookie)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("project scope %q: status = %d, want 400 (body: %s)", q, rec.Code, rec.Body.String())
		}
		msg, pos, hasPos := pqlErrorBody(t, rec.Body.String())
		if !strings.Contains(msg, "only available on workspace-scoped lists") {
			t.Fatalf("project scope %q: message = %q, want workspace-scoped text", q, msg)
		}
		if !hasPos || pos <= 0 {
			t.Fatalf("project scope %q: position = %v (hasPos=%v), want a positive column", q, pos, hasPos)
		}
	}
}
