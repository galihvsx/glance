package api

// Module HTTP endpoint tests (C3T1): full CRUD through the routes,
// duplicate-name 409 envelope, delete guard (409 without ?force=true,
// 204 with it), and issue add/remove/list. Real test database, no skips.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestModuleHTTP(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterModuleRoutes(e, &IssueHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("module-http"), "test-agent", uniqueIP())
	slug := uniqueSlug("module-http")
	createWorkspaceHTTP(t, e, cookie, "Module Co", slug)
	ident := uniqueProjectIdentifier("MH")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	issueBase := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"
	moduleBase := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/modules"

	// Create → 201.
	rec := postAuthedJSON(t, e, http.MethodPost, moduleBase, cookie,
		`{"name":"Platform","description":"The epic","status":"active","start_date":"2026-10-01","target_date":"2026-12-31"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create module: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Status      string `json:"status"`
		IssueCount  int64  `json:"issue_count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode module: %v", err)
	}
	if created.Name != "Platform" || created.IssueCount != 0 {
		t.Fatalf("created = %+v", created)
	}
	moduleURL := moduleBase + "/" + created.ID

	// Duplicate name → 409 envelope.
	rec = postAuthedJSON(t, e, http.MethodPost, moduleBase, cookie, `{"name":"Platform"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate module: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"conflict"`) {
		t.Fatalf("409 envelope: body=%s", rec.Body.String())
	}

	// Bad status → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, moduleBase, cookie, `{"name":"Bad","status":"nope"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad status: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// List → 200 with the module.
	rec = getAuthed(t, e, http.MethodGet, moduleBase, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list modules: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var listed struct {
		Modules []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"modules"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Modules) != 1 || listed.Modules[0].ID != created.ID {
		t.Fatalf("listed = %+v", listed)
	}

	// Get → 200; malformed id → 400.
	rec = getAuthed(t, e, http.MethodGet, moduleURL, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get module: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, moduleBase+"/not-a-uuid", cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed id: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// PATCH rename + clear description.
	rec = postAuthedJSON(t, e, http.MethodPatch, moduleURL, cookie, `{"name":"Platform v2","description":""}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch module: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"name":"Platform v2"`) {
		t.Fatalf("renamed body: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"description"`) {
		t.Fatalf("description should be cleared: %s", rec.Body.String())
	}

	// Add issues → 200; list shows them.
	iss1 := createIssueHTTP(t, e, cookie, issueBase, "in module")
	iss2 := createIssueHTTP(t, e, cookie, issueBase, "also in module")
	rec = postAuthedJSON(t, e, http.MethodPost, moduleURL+"/issues", cookie,
		`{"issue_ids":[`+`"`+iss1+`","`+iss2+`"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("add issues: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, moduleURL+"/issues", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list module issues: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var miListed struct {
		Issues []struct {
			ID        string `json:"id"`
			DisplayID string `json:"display_id"`
		} `json:"issues"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &miListed); err != nil {
		t.Fatalf("decode module issues: %v", err)
	}
	if len(miListed.Issues) != 2 || miListed.Issues[0].DisplayID == "" {
		t.Fatalf("module issues = %+v", miListed)
	}

	// Remove one issue → 200.
	rec = postAuthedJSON(t, e, http.MethodDelete, moduleURL+"/issues", cookie,
		`{"issue_ids":["`+iss1+`"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove issues: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	// Delete with issues still attached → 409; with ?force=true → 204.
	rec = getAuthed(t, e, http.MethodDelete, moduleURL, cookie)
	if rec.Code != http.StatusConflict {
		t.Fatalf("guarded delete: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "force=true") {
		t.Fatalf("409 should mention ?force=true: %s", rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodDelete, moduleURL+"?force=true", cookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("force delete: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, moduleURL, cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get deleted: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}

	// Unauthenticated → 401, not the SPA catch-all.
	req := httptest.NewRequest(http.MethodPost, moduleBase, strings.NewReader(`{"name":"Nope"}`))
	req.Header.Set("Content-Type", "application/json")
	recUnauth := httptest.NewRecorder()
	e.ServeHTTP(recUnauth, req)
	if recUnauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauthed create: status = %d, want 401", recUnauth.Code)
	}
}
