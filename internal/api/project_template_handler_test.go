package api

// Project templates API tests (C17T1): HTTP authz (admin write / member
// read / guest 403 / outsider 404), template application via POST
// /projects with template_id, 400 on unknown template_id. Follows the
// workspace_handler_test patterns (testServer, loginTestUser,
// postAuthedJSON, getAuthed).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
)

func testProjectTemplateServer(t *testing.T, pool *pgxpool.Pool) *echo.Echo {
	t.Helper()
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterProjectTemplateRoutes(e, &ProjectTemplateHandler{Pool: pool})
	return e
}

func TestProjectTemplateAPI(t *testing.T) {
	pool := newTestPool(t)
	e := testProjectTemplateServer(t, pool)

	adminCookie := loginTestUser(t, e, pool, uniqueEmail("ptapi-admin"), "test-agent/1.0", uniqueIP())
	memberCookie := loginTestUser(t, e, pool, uniqueEmail("ptapi-member"), "test-agent/1.0", uniqueIP())
	guestCookie := loginTestUser(t, e, pool, uniqueEmail("ptapi-guest"), "test-agent/1.0", uniqueIP())
	outsiderCookie := loginTestUser(t, e, pool, uniqueEmail("ptapi-out"), "test-agent/1.0", uniqueIP())

	// Workspace + memberships.
	slug := uniqueSlug("ptapi")
	rec := postAuthedJSON(t, e, http.MethodPost, "/api/v1/workspaces",
		adminCookie, fmt.Sprintf(`{"name":"Acme","slug":%q}`, slug))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create workspace: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	memberID := authedUserID(t, e, memberCookie)
	guestID := authedUserID(t, e, guestCookie)
	base := "/api/v1/workspaces/" + slug + "/templates"
	for _, tc := range []struct {
		id   string
		role int
	}{
		{memberID, 15},
		{guestID, 5},
	} {
		rec := postAuthedJSON(t, e, http.MethodPost, "/api/v1/workspaces/"+slug+"/members",
			adminCookie, fmt.Sprintf(`{"user_id":%q,"role":%d}`, tc.id, tc.role))
		if rec.Code != http.StatusOK {
			t.Fatalf("add member %s: status = %d (body: %s)", tc.id, rec.Code, rec.Body.String())
		}
	}

	// Admin creates → 201.
	payload := `{"states":[{"name":"Icebox","group":"backlog","color":"#6b7280"}],"labels":[{"name":"bug"}],"modules":[{"name":"API"}],"estimate_scale":{"name":"Fib","points":[{"key":"1","value":1}]}}`
	rec = postAuthedJSON(t, e, http.MethodPost, base, adminCookie,
		fmt.Sprintf(`{"name":"Blueprint","description":"d","payload":%s}`, payload))
	if rec.Code != http.StatusCreated {
		t.Fatalf("admin create: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.ID == "" {
		t.Fatalf("decode create: %v (body: %s)", err, rec.Body.String())
	}

	// Duplicate name → 409.
	rec = postAuthedJSON(t, e, http.MethodPost, base, adminCookie,
		fmt.Sprintf(`{"name":"blueprint","payload":%s}`, payload))
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate name: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}

	// Bad payload → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, base, adminCookie,
		`{"name":"Bad","payload":{"states":[{"name":"A","group":"nope"}]}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad payload: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// Member reads → 200.
	rec = getAuthed(t, e, http.MethodGet, base, memberCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("member list: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var list struct {
		Templates []struct {
			ID string `json:"id"`
		} `json:"templates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list.Templates) != 1 {
		t.Fatalf("member list decode: %v (body: %s)", err, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, base+"/"+created.ID, memberCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("member get: status = %d, want 200", rec.Code)
	}

	// Member writes → 403.
	rec = postAuthedJSON(t, e, http.MethodPost, base, memberCookie, `{"name":"M","payload":{}}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member create: status = %d, want 403", rec.Code)
	}
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/"+created.ID, memberCookie, `{"name":"M2"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member patch: status = %d, want 403", rec.Code)
	}
	rec = postAuthedJSON(t, e, http.MethodDelete, base+"/"+created.ID, memberCookie, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member delete: status = %d, want 403", rec.Code)
	}

	// Guest reads → 403.
	rec = getAuthed(t, e, http.MethodGet, base, guestCookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest list: status = %d, want 403", rec.Code)
	}
	rec = getAuthed(t, e, http.MethodGet, base+"/"+created.ID, guestCookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest get: status = %d, want 403", rec.Code)
	}

	// Outsider → 404 on every path (tenancy boundary).
	rec = getAuthed(t, e, http.MethodGet, base, outsiderCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("outsider list: status = %d, want 404", rec.Code)
	}
	rec = postAuthedJSON(t, e, http.MethodPost, base, outsiderCookie, `{"name":"O","payload":{}}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("outsider create: status = %d, want 404", rec.Code)
	}

	// Unknown template id → 404.
	rec = getAuthed(t, e, http.MethodGet, base+"/00000000-0000-0000-0000-000000000000", adminCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id get: status = %d, want 404", rec.Code)
	}

	// Admin patch → 200.
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/"+created.ID, adminCookie, `{"description":"updated"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin patch: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	// Empty patch → 400.
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/"+created.ID, adminCookie, `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty patch: status = %d, want 400", rec.Code)
	}

	// Admin delete → 204, then 404.
	rec = postAuthedJSON(t, e, http.MethodDelete, base+"/"+created.ID, adminCookie, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("admin delete: status = %d, want 204", rec.Code)
	}
	rec = getAuthed(t, e, http.MethodGet, base+"/"+created.ID, adminCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete: status = %d, want 404", rec.Code)
	}
}

func TestProjectTemplateAPIApplyOnCreate(t *testing.T) {
	pool := newTestPool(t)
	e := testProjectTemplateServer(t, pool)

	adminCookie := loginTestUser(t, e, pool, uniqueEmail("ptapc-admin"), "test-agent/1.0", uniqueIP())
	slug := uniqueSlug("ptapc")
	rec := postAuthedJSON(t, e, http.MethodPost, "/api/v1/workspaces",
		adminCookie, fmt.Sprintf(`{"name":"Acme","slug":%q}`, slug))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create workspace: status = %d", rec.Code)
	}

	// Template via API.
	payload := `{"states":[{"name":"Icebox","group":"backlog"}],"labels":[{"name":"bug","color":"#ef4444"}],"modules":[{"name":"API"}]}`
	rec = postAuthedJSON(t, e, http.MethodPost, "/api/v1/workspaces/"+slug+"/templates",
		adminCookie, fmt.Sprintf(`{"name":"T","payload":%s}`, payload))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create template: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var tmpl struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &tmpl); err != nil {
		t.Fatalf("decode template: %v", err)
	}

	// POST /projects with template_id → 201, template states applied.
	ident := (strings.ToUpper(strings.ReplaceAll(uniqueSlug("ptp"), "-", "")) + "0000000000")[:10]
	rec = postAuthedJSON(t, e, http.MethodPost, "/api/v1/workspaces/"+slug+"/projects",
		adminCookie, fmt.Sprintf(`{"name":"Templated","identifier":%q,"template_id":%q}`, ident, tmpl.ID))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create project with template: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, "/api/v1/workspaces/"+slug+"/projects/"+ident+"/states", adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list states: status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Icebox") || strings.Contains(rec.Body.String(), "Backlog") {
		t.Fatalf("states body = %s, want Icebox and no default Backlog", rec.Body.String())
	}

	// Unknown template_id → 400, and no project row leaks.
	rec = postAuthedJSON(t, e, http.MethodPost, "/api/v1/workspaces/"+slug+"/projects",
		adminCookie, `{"name":"Bad","identifier":"BADTP001","template_id":"00000000-0000-0000-0000-000000000000"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown template_id: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, "/api/v1/workspaces/"+slug+"/projects/BADTP001", adminCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("failed-create project fetch: status = %d, want 404", rec.Code)
	}
}
