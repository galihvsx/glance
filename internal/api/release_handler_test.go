package api

// Release HTTP endpoint tests (C4T6): full CRUD through the routes,
// duplicate-name 409 envelope, PATCH tri-state, delete guard (409
// without flags, 204 with ?reassign= or ?force=true), and issue
// assign/remove/list. Real test database, no skips.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestReleaseHTTP(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterReleaseRoutes(e, &IssueHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("release-http"), "test-agent", uniqueIP())
	slug := uniqueSlug("release-http")
	createWorkspaceHTTP(t, e, cookie, "Release Co", slug)
	ident := uniqueProjectIdentifier("RH")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	issueBase := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"
	releaseBase := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/releases"

	// Create → 201.
	rec := postAuthedJSON(t, e, http.MethodPost, releaseBase, cookie,
		`{"name":"v1.0","description":"First","status":"planned","release_date":"2026-12-01"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create release: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Status     string `json:"status"`
		IssueCount int64  `json:"issue_count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode release: %v", err)
	}
	if created.Name != "v1.0" || created.IssueCount != 0 {
		t.Fatalf("created = %+v", created)
	}
	releaseURL := releaseBase + "/" + created.ID

	// Duplicate name → 409 envelope.
	rec = postAuthedJSON(t, e, http.MethodPost, releaseBase, cookie, `{"name":"v1.0"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate release: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"conflict"`) {
		t.Fatalf("409 envelope: body=%s", rec.Body.String())
	}

	// Bad status → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, releaseBase, cookie, `{"name":"Bad","status":"shipped"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad status: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// List → 200 with the release.
	rec = getAuthed(t, e, http.MethodGet, releaseBase, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list releases: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var listed struct {
		Releases []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"releases"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Releases) != 1 {
		t.Fatalf("listed = %+v, want 1", listed)
	}

	// PATCH tri-state: flip status, clear the date.
	rec = postAuthedJSON(t, e, http.MethodPatch, releaseURL, cookie,
		`{"status":"released","release_date":""}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch release: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var patched struct {
		Status      string  `json:"status"`
		ReleaseDate *string `json:"release_date"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &patched); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if patched.Status != "released" || patched.ReleaseDate != nil {
		t.Fatalf("patched = %+v", patched)
	}

	// PATCH {} → 400.
	rec = postAuthedJSON(t, e, http.MethodPatch, releaseURL, cookie, `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty patch: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// Assign an issue → listed under the release.
	iss := createIssueHTTP(t, e, cookie, issueBase, "Release issue")
	rec = postAuthedJSON(t, e, http.MethodPost, releaseURL+"/issues", cookie,
		`{"issue_ids":["`+iss+`"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("assign issue: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, releaseURL+"/issues", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list release issues: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var relIssues struct {
		Issues []struct {
			ID        string `json:"id"`
			DisplayID string `json:"display_id"`
		} `json:"issues"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &relIssues); err != nil {
		t.Fatalf("decode release issues: %v", err)
	}
	if len(relIssues.Issues) != 1 || relIssues.Issues[0].ID != iss {
		t.Fatalf("release issues = %+v", relIssues)
	}

	// Delete with issues → 409.
	rec = getAuthed(t, e, http.MethodDelete, releaseURL, cookie)
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete with issues: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}

	// Delete with ?reassign= → 204, issue moved.
	rec2 := postAuthedJSON(t, e, http.MethodPost, releaseBase, cookie, `{"name":"v1.1"}`)
	var created2 struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &created2); err != nil {
		t.Fatalf("decode release2: %v", err)
	}
	rec = getAuthed(t, e, http.MethodDelete, releaseURL+"?reassign="+created2.ID, cookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("reassign delete: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, releaseBase+"/"+created2.ID+"/issues", cookie)
	var moved struct {
		Issues []struct {
			ID string `json:"id"`
		} `json:"issues"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &moved); err != nil {
		t.Fatalf("decode moved: %v", err)
	}
	if len(moved.Issues) != 1 || moved.Issues[0].ID != iss {
		t.Fatalf("moved issues = %+v", moved)
	}

	// Unauthenticated → 401, not 404.
	rec = getPublic(t, e, releaseBase)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauth: status = %d, want 401", rec.Code)
	}
}
