package api

// Initiative HTTP endpoint tests (C27T0): CRUD through the routes,
// duplicate-name 409 envelope, guest write 403, tenancy 404s, membership
// add/remove idempotency, archived filtering, and the rollup endpoint.
// Real test database, no skips.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"glance/internal/service"
)

func TestInitiativeHTTP(t *testing.T) {
	pool := newTestPool(t)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterInitiativeRoutes(e, &IssueHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("init-http"), "test-agent", uniqueIP())
	slug := uniqueSlug("init-http")
	createWorkspaceHTTP(t, e, cookie, "Init Co", slug)
	ident := uniqueProjectIdentifier("IH")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	base := "/api/v1/workspaces/" + slug + "/initiatives"

	// Create → 201.
	rec := postAuthedJSON(t, e, http.MethodPost, base, cookie,
		`{"name":"Overhaul","description":"The program","status":"active"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Status      string `json:"status"`
		WorkspaceID string `json:"workspace_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Name != "Overhaul" || created.Status != "active" || created.WorkspaceID == "" {
		t.Fatalf("created = %+v", created)
	}
	initURL := base + "/" + created.ID

	// Duplicate name → 409 envelope.
	rec = postAuthedJSON(t, e, http.MethodPost, base, cookie, `{"name":"Overhaul"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"conflict"`) {
		t.Fatalf("409 envelope: body=%s", rec.Body.String())
	}

	// Get → 200; malformed id → 400.
	rec = getAuthed(t, e, http.MethodGet, initURL, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, base+"/not-a-uuid", cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed id: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// PATCH name → 200; bad status → 400.
	rec = postAuthedJSON(t, e, http.MethodPatch, initURL, cookie, `{"status":"completed"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPatch, initURL, cookie, `{"status":"bogus"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad status: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// Membership: add project (idempotent), list, rollup, remove.
	var projectID string
	if err := pool.QueryRow(context.Background(),
		`SELECT p.id::text FROM projects p JOIN workspaces w ON w.id = p.workspace_id
		  WHERE w.slug = $1 AND p.identifier = $2`, slug, ident).Scan(&projectID); err != nil {
		t.Fatalf("project id: %v", err)
	}
	rec = postAuthedJSON(t, e, http.MethodPost, initURL+"/projects", cookie,
		`{"project_id":"`+projectID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("add project: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	// Repeat add → still 200.
	rec = postAuthedJSON(t, e, http.MethodPost, initURL+"/projects", cookie,
		`{"project_id":"`+projectID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("repeat add project: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, initURL+"/projects", cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), projectID) {
		t.Fatalf("list projects: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	// Rollup → 200 with the aggregate shape.
	rec = getAuthed(t, e, http.MethodGet, initURL+"/rollup", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("rollup: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var roll struct {
		Total         int64   `json:"total"`
		DoneCount     int64   `json:"done_count"`
		CompletionPct float64 `json:"completion_pct"`
		Projects      []struct {
			ProjectID   string `json:"project_id"`
			ProjectName string `json:"project_name"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &roll); err != nil {
		t.Fatalf("decode rollup: %v", err)
	}
	if len(roll.Projects) != 1 || roll.Projects[0].ProjectName != "Engineering" {
		t.Fatalf("rollup = %+v", roll)
	}

	// Remove → 200; delete initiative → 204; get after delete → 404.
	rec = postAuthedJSON(t, e, http.MethodDelete, initURL+"/projects/"+projectID, cookie, ``)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove project: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodDelete, initURL, cookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, initURL, cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestInitiativeHTTPTenancyAndRoles(t *testing.T) {
	pool := newTestPool(t)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterInitiativeRoutes(e, &IssueHandler{Pool: pool})

	adminCookie := loginTestUser(t, e, pool, uniqueEmail("init-admin"), "test-agent", uniqueIP())
	slug := uniqueSlug("init-tenancy")
	createWorkspaceHTTP(t, e, adminCookie, "Init Co", slug)
	base := "/api/v1/workspaces/" + slug + "/initiatives"

	// Guest member: reads fine, writes 403.
	guestEmail := uniqueEmail("init-guest")
	guestCookie := loginTestUser(t, e, pool, guestEmail, "test-agent", uniqueIP())
	ctx := context.Background()
	var adminID, guestID string
	if err := pool.QueryRow(ctx,
		`SELECT m.user_id::text FROM workspace_members m JOIN workspaces w ON w.id = m.workspace_id
		  WHERE w.slug = $1 AND m.role = 20`, slug).Scan(&adminID); err != nil {
		t.Fatalf("admin id: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users WHERE email = $1`, guestEmail).Scan(&guestID); err != nil {
		t.Fatalf("guest id: %v", err)
	}
	if err := service.UpsertMember(ctx, pool, slug, adminID, guestID, service.RoleGuest); err != nil {
		t.Fatalf("UpsertMember: %v", err)
	}

	rec := postAuthedJSON(t, e, http.MethodPost, base, adminCookie, `{"name":"A"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("admin create: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	rec = postAuthedJSON(t, e, http.MethodPost, base, guestCookie, `{"name":"B"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest create: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, base, guestCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("guest list: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodDelete, base+"/"+created.ID, guestCookie, ``)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest delete: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, base+"/"+created.ID+"/rollup", guestCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("guest rollup: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	// Non-member (bad slug space) → 404, never 403.
	otherCookie := loginTestUser(t, e, pool, uniqueEmail("init-outsider"), "test-agent", uniqueIP())
	rec = getAuthed(t, e, http.MethodGet, base+"/"+created.ID, otherCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("outsider get: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPost, base, otherCookie, `{"name":"X"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("outsider create: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, "/api/v1/workspaces/does-not-exist/initiatives", adminCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bad slug: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}

	// Archived filtering.
	rec = postAuthedJSON(t, e, http.MethodPost, base, adminCookie, `{"name":"Old","status":"archived"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create archived: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, base, adminCookie)
	if rec.Code != http.StatusOK || strings.Count(rec.Body.String(), `"id"`) != 1 {
		t.Fatalf("default list: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, base+"?include_archived=1", adminCookie)
	if rec.Code != http.StatusOK || strings.Count(rec.Body.String(), `"id"`) != 2 {
		t.Fatalf("include_archived list: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
}
