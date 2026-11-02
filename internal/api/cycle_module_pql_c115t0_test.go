package api

// `cycle` / `module` PQL field HTTP tests (C115T0): on a project
// list endpoint the fields filter by cycle/module name, a cycle
// predicate ANDs with the ?cycle= scope param (foreign-cycle
// predicate → empty, cross module predicate → filtered), and on
// /my-issues they filter the workspace list; unknown names 400
// with message + position. Resolution semantics, set semantics,
// and the taxonomy-leak rule live in the service tests
// (pql_cycle_module_c115t0_test.go,
// mywork_pql_cycle_module_c115t0_test.go); this file pins the
// HTTP envelope.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"glance/internal/service"
)

func TestCycleModulePQLHTTP(t *testing.T) {
	pool := newTestPool(t)
	e := testServer(t, pool)
	RegisterAuthRoutes(e, testAuthHandler(pool))
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterMyWorkRoutes(e, &MyWorkHandler{Pool: pool})

	aliceEmail := uniqueEmail("cm-pql-alice")
	aliceCookie := loginTestUser(t, e, pool, aliceEmail, "test-agent", uniqueIP())
	slug := uniqueSlug("cm-pql")
	createWorkspaceHTTP(t, e, aliceCookie, "Acme", slug)
	ident := uniqueProjectIdentifier("CM")
	createProjectHTTP(t, e, aliceCookie, slug, "Engineering", ident)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident
	myBase := "/api/v1/workspaces/" + slug + "/my-issues"

	bobEmail := uniqueEmail("cm-pql-bob")
	bobCookie := loginTestUser(t, e, pool, bobEmail, "test-agent", uniqueIP())
	ctx := context.Background()
	var aliceID, bobID, wsID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users WHERE email = $1`, aliceEmail).Scan(&aliceID); err != nil {
		t.Fatalf("alice id: %v", err)
	}
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
	assign := func(issueID string) {
		t.Helper()
		if _, err := pool.Exec(ctx,
			`INSERT INTO issue_assignees (issue_id, user_id) VALUES ($1::uuid, $2::uuid)`,
			issueID, bobID); err != nil {
			t.Fatalf("assign: %v", err)
		}
	}
	fetchProjectNames := func(path string) []string {
		t.Helper()
		rec := getAuthed(t, e, http.MethodGet, path, aliceCookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: status = %d, want 200 (body: %s)", path, rec.Code, rec.Body.String())
		}
		var body struct {
			Results []struct {
				Name string `json:"name"`
			} `json:"results"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		names := make([]string, 0, len(body.Results))
		for _, it := range body.Results {
			names = append(names, it.Name)
		}
		return names
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
	wantExactly := func(got []string, want ...string) {
		t.Helper()
		// Order-insensitive exact comparison.
		sortStrings := func(xs []string) []string {
			out := append([]string(nil), xs...)
			for i := range out {
				for j := i + 1; j < len(out); j++ {
					if out[j] < out[i] {
						out[i], out[j] = out[j], out[i]
					}
				}
			}
			return out
		}
		if strings.Join(sortStrings(got), "|") != strings.Join(sortStrings(want), "|") {
			t.Fatalf("got %v, want exactly %v", got, want)
		}
	}

	now := time.Now()
	cycle, err := service.CreateCycle(ctx, pool, slug, ident, aliceID, service.CycleInput{
		Name: "Sprint 3", StartDate: now.AddDate(0, 0, -30), EndDate: now.AddDate(0, 0, -16),
	})
	if err != nil {
		t.Fatalf("CreateCycle: %v", err)
	}
	otherCycle, err := service.CreateCycle(ctx, pool, slug, ident, aliceID, service.CycleInput{
		Name: "Sprint 4", StartDate: now.AddDate(0, 0, -15), EndDate: now.AddDate(0, 0, -1),
	})
	if err != nil {
		t.Fatalf("CreateCycle other: %v", err)
	}
	module, err := service.CreateModule(ctx, pool, slug, ident, aliceID, service.ModuleInput{Name: "Auth"})
	if err != nil {
		t.Fatalf("CreateModule: %v", err)
	}

	alpha := create(aliceCookie, "alpha")
	beta := create(aliceCookie, "beta")
	gamma := create(aliceCookie, "gamma")
	delta := create(aliceCookie, "delta")
	if err := service.AddCycleIssues(ctx, pool, slug, ident, aliceID, cycle.ID, []string{alpha, delta}); err != nil {
		t.Fatalf("AddCycleIssues: %v", err)
	}
	if err := service.AddCycleIssues(ctx, pool, slug, ident, aliceID, otherCycle.ID, []string{beta}); err != nil {
		t.Fatalf("AddCycleIssues other: %v", err)
	}
	if err := service.AddModuleIssues(ctx, pool, slug, ident, aliceID, module.ID, []string{beta, delta}); err != nil {
		t.Fatalf("AddModuleIssues: %v", err)
	}
	for _, id := range []string{alpha, beta, gamma, delta} {
		assign(id)
	}

	// Project list endpoint: each field narrows by name.
	wantExactly(fetchProjectNames(base+"/issues?pql="+url.QueryEscape(`cycle = "Sprint 3"`)), "alpha", "delta")
	wantExactly(fetchProjectNames(base+"/issues?pql="+url.QueryEscape(`module = Auth`)), "beta", "delta")
	wantExactly(fetchProjectNames(base+"/issues?pql="+url.QueryEscape(`cycle = none`)), "gamma")
	// Scope AND over HTTP: a foreign-cycle predicate under a cycle
	// scope yields empty; a cross module predicate filters correctly.
	wantExactly(fetchProjectNames(base + "/issues?cycle=" + cycle.ID + "&pql=" + url.QueryEscape(`cycle = "Sprint 4"`)))
	wantExactly(fetchProjectNames(base+"/issues?cycle="+cycle.ID+"&pql="+url.QueryEscape(`module = Auth`)), "delta")
	// Unknown name 400s with message + position on the project list.
	rec := getAuthed(t, e, http.MethodGet, base+"/issues?pql="+url.QueryEscape(`cycle = NoSuchCycle`), aliceCookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown cycle: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	msg, pos, hasPos := pqlErrorBody(t, rec.Body.String())
	if !strings.Contains(msg, `unknown cycle "NoSuchCycle"`) {
		t.Fatalf("error message = %q, want unknown-cycle text", msg)
	}
	if !hasPos || pos <= 0 {
		t.Fatalf("error position = %v (hasPos=%v), want a positive column", pos, hasPos)
	}

	// /my-issues: workspace scope filters the assigned list.
	wantExactly(fetchMyNames(myBase+"?filter=assigned&pql="+url.QueryEscape(`cycle = "Sprint 3"`)), "alpha", "delta")
	wantExactly(fetchMyNames(myBase+"?filter=assigned&pql="+url.QueryEscape(`module = Auth`)), "beta", "delta")
	wantExactly(fetchMyNames(myBase+"?filter=assigned&pql="+url.QueryEscape(`module = none`)), "alpha", "gamma")
	rec = getAuthed(t, e, http.MethodGet, myBase+"?filter=assigned&pql="+url.QueryEscape(`module = NoSuchModule`), bobCookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown module: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	msg, pos, hasPos = pqlErrorBody(t, rec.Body.String())
	if !strings.Contains(msg, `unknown module "NoSuchModule"`) {
		t.Fatalf("error message = %q, want unknown-module text", msg)
	}
	if !hasPos || pos <= 0 {
		t.Fatalf("error position = %v (hasPos=%v), want a positive column", pos, hasPos)
	}
}
