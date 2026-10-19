package api

// Project-label HTTP endpoint tests (C76T0): workspace-mounted CRUD
// (201 / 409 on duplicate / 400 on bad color / 204 delete), per-project
// assignment (204, duplicate-assign 409, idempotent unassign), guest
// 403 on writes, and the ?label= filter on the projects list
// (repeatable + comma-separated, malformed → 400 with a clear
// message). Real test database, no skips.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
)

func setupProjectLabelHTTP(t *testing.T, pool *pgxpool.Pool) (*echo.Echo, *http.Cookie, string, string) {
	t.Helper()
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("pl-http"), "test-agent", uniqueIP())
	slug := uniqueSlug("pl-http")
	createWorkspaceHTTP(t, e, cookie, "PL Co", slug)
	ident := uniqueProjectIdentifier("PL")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	return e, cookie, slug, ident
}

func createProjectLabelHTTP(t *testing.T, e *echo.Echo, cookie *http.Cookie, slug, body string) string {
	t.Helper()
	rec := postAuthedJSON(t, e, http.MethodPost,
		"/api/v1/workspaces/"+slug+"/project-labels", cookie, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create project label: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var l struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
		t.Fatalf("decode project label: %v", err)
	}
	return l.ID
}

func TestProjectLabelHTTP(t *testing.T) {
	pool := newTestPool(t)
	e, cookie, slug, ident := setupProjectLabelHTTP(t, pool)
	base := "/api/v1/workspaces/" + slug + "/project-labels"
	projBase := "/api/v1/workspaces/" + slug + "/projects/" + ident

	// Create → 201 with the label JSON.
	rec := postAuthedJSON(t, e, http.MethodPost, base, cookie,
		`{"name":"platform","color":"#3b82f6"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Name != "platform" || created.Color != "#3b82f6" {
		t.Fatalf("created = %+v", created)
	}

	// Duplicate name → 409 envelope.
	rec = postAuthedJSON(t, e, http.MethodPost, base, cookie, `{"name":"platform"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"conflict"`) {
		t.Fatalf("409 envelope: body=%s", rec.Body.String())
	}

	// Bad color → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, base, cookie,
		`{"name":"nope","color":"red"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad color: status = %d, want 400", rec.Code)
	}

	// Blank name → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, base, cookie, `{"name":"  "}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("blank name: status = %d, want 400", rec.Code)
	}

	// List → wrapped shape, never null.
	rec = getAuthed(t, e, http.MethodGet, base, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d", rec.Code)
	}
	var listed struct {
		Labels []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"project_labels"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Labels) != 1 {
		t.Fatalf("labels = %d, want 1", len(listed.Labels))
	}

	// Get → 200; foreign id → 404.
	rec = getAuthed(t, e, http.MethodGet, base+"/"+created.ID, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: status = %d", rec.Code)
	}
	rec = getAuthed(t, e, http.MethodGet, base+"/00000000-0000-0000-0000-000000000000", cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get unknown: status = %d, want 404", rec.Code)
	}

	// PATCH partial: rename only.
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/"+created.ID, cookie,
		`{"name":"platform-x"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var patched struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &patched); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if patched.Name != "platform-x" || patched.Color != "#3b82f6" {
		t.Fatalf("patched = %+v", patched)
	}

	// Empty patch → 400.
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/"+created.ID, cookie, `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty patch: status = %d, want 400", rec.Code)
	}

	// Assign → 204; duplicate → 409; unassign → 204 twice.
	rec = postAuthedJSON(t, e, http.MethodPost,
		projBase+"/project-labels/"+created.ID, cookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("assign: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPost,
		projBase+"/project-labels/"+created.ID, cookie, ``)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate assign: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodDelete,
		projBase+"/project-labels/"+created.ID, cookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("unassign: status = %d, want 204", rec.Code)
	}
	rec = postAuthedJSON(t, e, http.MethodDelete,
		projBase+"/project-labels/"+created.ID, cookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("unassign idempotent: status = %d, want 204", rec.Code)
	}

	// Assign: unknown project → 404; unknown label → 404.
	rec = postAuthedJSON(t, e, http.MethodPost,
		"/api/v1/workspaces/"+slug+"/projects/NOPE/project-labels/"+created.ID, cookie, ``)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("assign unknown project: status = %d, want 404", rec.Code)
	}
	rec = postAuthedJSON(t, e, http.MethodPost,
		projBase+"/project-labels/00000000-0000-0000-0000-000000000000", cookie, ``)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("assign unknown label: status = %d, want 404", rec.Code)
	}

	// DELETE label → 204; get after → 404.
	rec = postAuthedJSON(t, e, http.MethodDelete, base+"/"+created.ID, cookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d, want 204", rec.Code)
	}
	rec = getAuthed(t, e, http.MethodGet, base+"/"+created.ID, cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete: status = %d, want 404", rec.Code)
	}
}

// TestProjectLabelGuestHTTP: a guest reads the taxonomy and the
// project chips but gets 403 on every write.
func TestProjectLabelGuestHTTP(t *testing.T) {
	pool := newTestPool(t)
	e, cookie, slug, ident := setupProjectLabelHTTP(t, pool)
	base := "/api/v1/workspaces/" + slug + "/project-labels"
	projBase := "/api/v1/workspaces/" + slug + "/projects/" + ident

	labelID := createProjectLabelHTTP(t, e, cookie, slug, `{"name":"platform"}`)

	// Second user → guest member of the workspace.
	guestEmail := uniqueEmail("pl-guest")
	guestCookie := loginTestUser(t, e, pool, guestEmail, "test-agent", uniqueIP())
	var wsID, guestID string
	ctx := context.Background()
	if err := pool.QueryRow(ctx, `SELECT id::text FROM workspaces WHERE slug = $1`, slug).Scan(&wsID); err != nil {
		t.Fatalf("ws id: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users WHERE email = $1`, guestEmail).Scan(&guestID); err != nil {
		t.Fatalf("guest id: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1::uuid, $2::uuid, 5)`,
		wsID, guestID); err != nil {
		t.Fatalf("add guest: %v", err)
	}

	// Guest reads: taxonomy list + project payload chips.
	rec := getAuthed(t, e, http.MethodGet, base, guestCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("guest list: status = %d", rec.Code)
	}
	rec = getAuthed(t, e, http.MethodGet,
		"/api/v1/workspaces/"+slug+"/projects/"+ident, guestCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("guest project: status = %d", rec.Code)
	}

	// Guest writes: all 403.
	for _, tc := range []struct {
		method, path, body string
	}{
		{http.MethodPost, base, `{"name":"nope"}`},
		{http.MethodPatch, base + "/" + labelID, `{"name":"nope"}`},
		{http.MethodDelete, base + "/" + labelID, ``},
		{http.MethodPost, projBase + "/project-labels/" + labelID, ``},
		{http.MethodDelete, projBase + "/project-labels/" + labelID, ``},
	} {
		rec := postAuthedJSON(t, e, tc.method, tc.path, guestCookie, tc.body)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("guest %s %s: status = %d, want 403 (body: %s)",
				tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
}

// TestProjectListLabelParam: ?label= on the projects list —
// repeatable + comma-separated, malformed → 400 with a clear message,
// unknown → 400, chips [] never null on payloads.
func TestProjectListLabelParam(t *testing.T) {
	pool := newTestPool(t)
	e, cookie, slug, identA := setupProjectLabelHTTP(t, pool)
	listBase := "/api/v1/workspaces/" + slug + "/projects"

	identB := uniqueProjectIdentifier("PL")
	createProjectHTTP(t, e, cookie, slug, "Backend", identB)

	teamID := createProjectLabelHTTP(t, e, cookie, slug, `{"name":"team-a"}`)
	prioID := createProjectLabelHTTP(t, e, cookie, slug, `{"name":"p0"}`)

	assign := func(ident, labelID string, want int) {
		t.Helper()
		rec := postAuthedJSON(t, e, http.MethodPost,
			"/api/v1/workspaces/"+slug+"/projects/"+ident+"/project-labels/"+labelID, cookie, ``)
		if rec.Code != want {
			t.Fatalf("assign: status = %d, want %d", rec.Code, want)
		}
	}
	assign(identA, teamID, http.StatusNoContent)
	assign(identB, prioID, http.StatusNoContent)
	assign(identB, teamID, http.StatusNoContent)

	decodeList := func(recBody string) []map[string]any {
		t.Helper()
		var list struct {
			Projects []struct {
				Identifier string           `json:"identifier"`
				Labels     []map[string]any `json:"labels"`
			} `json:"projects"`
		}
		if err := json.Unmarshal([]byte(recBody), &list); err != nil {
			t.Fatalf("decode projects: %v", err)
		}
		out := make([]map[string]any, 0, len(list.Projects))
		for _, p := range list.Projects {
			if p.Labels == nil {
				t.Fatalf("project %s labels = null, want []", p.Identifier)
			}
			out = append(out, map[string]any{"identifier": p.Identifier, "labels": p.Labels})
		}
		return out
	}

	// Unfiltered: both projects, labels [] never null.
	rec := getAuthed(t, e, http.MethodGet, listBase, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d", rec.Code)
	}
	if got := decodeList(rec.Body.String()); len(got) != 2 {
		t.Fatalf("unfiltered = %d, want 2", len(got))
	}

	// Single label → its projects.
	rec = getAuthed(t, e, http.MethodGet, listBase+"?label="+teamID, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("filter team: status = %d", rec.Code)
	}
	if got := decodeList(rec.Body.String()); len(got) != 2 {
		t.Fatalf("filter team = %d, want 2", len(got))
	}

	// Repeatable + comma-separated → OR union.
	rec = getAuthed(t, e, http.MethodGet,
		listBase+"?label="+teamID+"&label="+prioID+","+teamID, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("filter multi: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if got := decodeList(rec.Body.String()); len(got) != 2 {
		t.Fatalf("filter multi = %d, want 2", len(got))
	}

	// Malformed → 400 with a clear message naming the value.
	rec = getAuthed(t, e, http.MethodGet, listBase+"?label=nope", cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed label: status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `nope`) {
		t.Fatalf("400 message must name the bad value: body=%s", rec.Body.String())
	}

	// Unknown UUID → 400 with a clear message.
	rec = getAuthed(t, e, http.MethodGet,
		listBase+"?label=00000000-0000-0000-0000-000000000000", cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown label: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// Empty ?label= behaves as unfiltered.
	rec = getAuthed(t, e, http.MethodGet, listBase+"?label=", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("empty label: status = %d, want 200", rec.Code)
	}
	if got := decodeList(rec.Body.String()); len(got) != 2 {
		t.Fatalf("empty label = %d, want 2", len(got))
	}
}
