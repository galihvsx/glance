package api

// Issue project-structure tests (C19T3): the ?include_project_structure=1
// attachment on GET issue (cycle/module/release membership) and the
// tri-state release_id on PATCH issue. Real test database, no skips.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestIssueProjectStructureInclude(t *testing.T) {
	pool := newTestPool(t)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterCycleRoutes(e, &IssueHandler{Pool: pool})
	RegisterModuleRoutes(e, &IssueHandler{Pool: pool})
	RegisterReleaseRoutes(e, &IssueHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("issue-structure"), "test-agent", uniqueIP())

	slug := uniqueSlug("issue-structure")
	createWorkspaceHTTP(t, e, cookie, "Structure Co", slug)
	ident := uniqueProjectIdentifier("ST")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident

	// Issue.
	rec := postAuthedJSON(t, e, http.MethodPost, base+"/issues", cookie, `{"name":"Structured work"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create issue: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var issue struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &issue); err != nil {
		t.Fatalf("decode issue: %v", err)
	}

	// Cycle + module + release.
	start := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	end := time.Now().AddDate(0, 0, 13).Format("2006-01-02")
	rec = postAuthedJSON(t, e, http.MethodPost, base+"/cycles", cookie,
		`{"name":"Cycle 19","start_date":"`+start+`","end_date":"`+end+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create cycle: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var cycle struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &cycle); err != nil {
		t.Fatalf("decode cycle: %v", err)
	}
	rec = postAuthedJSON(t, e, http.MethodPost, base+"/modules", cookie, `{"name":"Command menu"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create module: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var module struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &module); err != nil {
		t.Fatalf("decode module: %v", err)
	}
	rec = postAuthedJSON(t, e, http.MethodPost, base+"/releases", cookie, `{"name":"v0.4.0","status":"planned"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create release: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var release struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &release); err != nil {
		t.Fatalf("decode release: %v", err)
	}

	// Membership: cycle + module via their endpoints, release via PATCH.
	rec = postAuthedJSON(t, e, http.MethodPost, base+"/cycles/"+cycle.ID+"/issues", cookie,
		`{"issue_ids":["`+issue.ID+`"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("add to cycle: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPost, base+"/modules/"+module.ID+"/issues", cookie,
		`{"issue_ids":["`+issue.ID+`"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("add to module: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/issues/"+issue.ID, cookie,
		`{"release_id":"`+release.ID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch release_id: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	// GET with the flag → all three memberships present.
	rec = getAuthed(t, e, http.MethodGet, base+"/issues/"+issue.ID+"?include_project_structure=1", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get with include: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var got struct {
		ProjectStructure *struct {
			Cycle *struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"cycle"`
			Modules []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"modules"`
			Release *struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"release"`
		} `json:"project_structure"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode include: %v", err)
	}
	ps := got.ProjectStructure
	if ps == nil {
		t.Fatal("project_structure missing from response")
	}
	if ps.Cycle == nil || ps.Cycle.ID != cycle.ID || ps.Cycle.Name != "Cycle 19" {
		t.Fatalf("cycle = %+v, want id %s name Cycle 19", ps.Cycle, cycle.ID)
	}
	if len(ps.Modules) != 1 || ps.Modules[0].ID != module.ID || ps.Modules[0].Name != "Command menu" {
		t.Fatalf("modules = %+v, want one Command menu", ps.Modules)
	}
	if ps.Release == nil || ps.Release.ID != release.ID || ps.Release.Name != "v0.4.0" {
		t.Fatalf("release = %+v, want id %s name v0.4.0", ps.Release, release.ID)
	}

	// Backward compatibility: without the flag the key is absent.
	rec = getAuthed(t, e, http.MethodGet, base+"/issues/"+issue.ID, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get without include: status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "project_structure") {
		t.Fatalf("project_structure leaked without the flag: %s", rec.Body.String())
	}

	// Clear the release with explicit null → include shows no release.
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/issues/"+issue.ID, cookie, `{"release_id":null}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear release: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, base+"/issues/"+issue.ID+"?include_project_structure=1", cookie)
	var cleared struct {
		ProjectStructure *struct {
			Release *struct {
				ID string `json:"id"`
			} `json:"release"`
		} `json:"project_structure"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &cleared); err != nil {
		t.Fatalf("decode cleared: %v", err)
	}
	if cleared.ProjectStructure == nil || cleared.ProjectStructure.Release != nil {
		t.Fatalf("release after clear = %+v, want nil", cleared.ProjectStructure)
	}

	// Malformed id → 400; well-formed but unknown → 404.
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/issues/"+issue.ID, cookie, `{"release_id":"nope"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed release_id: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/issues/"+issue.ID, cookie,
		`{"release_id":"00000000-0000-0000-0000-000000000000"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown release_id: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}

	// Unknown issue with the flag → 404 (tenancy intact).
	rec = getAuthed(t, e, http.MethodGet,
		base+"/issues/00000000-0000-0000-0000-000000000000?include_project_structure=1", cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown issue include: status = %d, want 404", rec.Code)
	}
}
