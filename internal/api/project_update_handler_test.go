package api

// Project update HTTP endpoint tests (C34T0): CRUD through the routes,
// newest-first ordering, status/body validation, the authz matrix
// (author vs other member vs admin vs viewer vs guest vs non-member),
// and unknown-update 404s. Real test database, no skips.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
)

func setupProjectUpdateHTTP(t *testing.T) (e *echo.Echo, pool *pgxpool.Pool, adminCookie *http.Cookie, updatesBase, slug string) {
	t.Helper()
	pool = newTestPool(t)
	e = testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterProjectUpdateRoutes(e, &IssueHandler{Pool: pool})

	adminCookie = loginTestUser(t, e, pool, uniqueEmail("pu-admin"), "test-agent", uniqueIP())
	slug = uniqueSlug("project-update")
	createWorkspaceHTTP(t, e, adminCookie, "ProjUpd Co", slug)
	ident := uniqueProjectIdentifier("PU")
	createProjectHTTP(t, e, adminCookie, slug, "Engineering", ident)
	updatesBase = "/api/v1/workspaces/" + slug + "/projects/" + ident + "/updates"
	return e, pool, adminCookie, updatesBase, slug
}

type projectUpdateJSON struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Author    *struct {
		ID    string  `json:"id"`
		Name  *string `json:"name"`
		Email string  `json:"email"`
	} `json:"author"`
	Status    string `json:"status"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func postProjectUpdate(t *testing.T, e *echo.Echo, cookie *http.Cookie, base, body string) *httptest.ResponseRecorder {
	t.Helper()
	return postAuthedJSON(t, e, http.MethodPost, base, cookie, body)
}

func decodeProjectUpdate(t *testing.T, rec *httptest.ResponseRecorder) projectUpdateJSON {
	t.Helper()
	var u projectUpdateJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &u); err != nil {
		t.Fatalf("decode update: %v (body: %s)", err, rec.Body.String())
	}
	return u
}

func listProjectUpdatesHTTP(t *testing.T, e *echo.Echo, cookie *http.Cookie, base string) []projectUpdateJSON {
	t.Helper()
	rec := getAuthed(t, e, http.MethodGet, base, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list updates: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var list struct {
		Updates []projectUpdateJSON `json:"updates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	return list.Updates
}

func TestProjectUpdateCRUD(t *testing.T) {
	e, _, adminCookie, base, _ := setupProjectUpdateHTTP(t)

	// Create → 201, fields round-trip, author attributed.
	rec := postProjectUpdate(t, e, adminCookie, base, `{"status":"on_track","body":"Kicked off"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create update: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	created := decodeProjectUpdate(t, rec)
	if created.ID == "" || created.ProjectID == "" {
		t.Fatalf("created update = %+v, want id and project_id set", created)
	}
	if created.Status != "on_track" || created.Body != "Kicked off" {
		t.Fatalf("created update = %+v, want on_track / Kicked off", created)
	}
	if created.Author == nil || created.Author.Email == "" {
		t.Fatalf("created update has no author: %s", rec.Body.String())
	}

	// A second update lists first (newest first).
	rec = postProjectUpdate(t, e, adminCookie, base, `{"status":"at_risk","body":"Blocked"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create second update: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	second := decodeProjectUpdate(t, rec)
	list := listProjectUpdatesHTTP(t, e, adminCookie, base)
	if len(list) != 2 || list[0].ID != second.ID || list[1].ID != created.ID {
		t.Fatalf("list = %+v, want [second, created] newest first", list)
	}

	// PATCH → 200, status + body replaced.
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/"+created.ID, adminCookie,
		`{"status":"off_track","body":"Escalated"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch update: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if got := decodeProjectUpdate(t, rec); got.Status != "off_track" || got.Body != "Escalated" {
		t.Fatalf("patched update = %+v, want off_track / Escalated", got)
	}

	// DELETE → 204; second delete → 404.
	rec = postAuthedJSON(t, e, http.MethodDelete, base+"/"+created.ID, adminCookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete update: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodDelete, base+"/"+created.ID, adminCookie, ``)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("second delete: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}

	// List holds only the second update.
	list = listProjectUpdatesHTTP(t, e, adminCookie, base)
	if len(list) != 1 || list[0].ID != second.ID {
		t.Fatalf("list after delete = %+v, want [second]", list)
	}
}

func TestProjectUpdateValidation(t *testing.T) {
	e, _, adminCookie, base, _ := setupProjectUpdateHTTP(t)

	// Invalid status → 400; empty / whitespace body → 400.
	for _, tc := range []struct {
		name string
		body string
	}{
		{"bad status", `{"status":"green","body":"x"}`},
		{"empty status", `{"status":"","body":"x"}`},
		{"empty body", `{"status":"on_track","body":""}`},
		{"whitespace body", `{"status":"on_track","body":"   "}`},
		{"missing body", `{"status":"on_track"}`},
		{"missing status", `{"body":"x"}`},
	} {
		rec := postProjectUpdate(t, e, adminCookie, base, tc.body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400 (body: %s)", tc.name, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"code":"bad_request"`) {
			t.Fatalf("%s: 400 envelope: body=%s", tc.name, rec.Body.String())
		}
	}

	// Create one valid update, then PATCH validation.
	rec := postProjectUpdate(t, e, adminCookie, base, `{"status":"on_track","body":"OK"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	u := decodeProjectUpdate(t, rec)

	for _, tc := range []struct {
		name string
		body string
	}{
		{"patch bad status", `{"status":"nope"}`},
		{"patch empty body", `{"body":"  "}`},
		{"patch no keys", `{}`},
	} {
		rec := postAuthedJSON(t, e, http.MethodPatch, base+"/"+u.ID, adminCookie, tc.body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400 (body: %s)", tc.name, rec.Code, rec.Body.String())
		}
	}

	// Malformed update id on PATCH/DELETE → 400; well-formed but
	// unknown → 404.
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/nope", adminCookie, `{"body":"x"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad update id patch: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodDelete, base+"/nope", adminCookie, ``)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad update id delete: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	missing := "00000000-0000-0000-0000-000000000000"
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/"+missing, adminCookie, `{"body":"x"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown update patch: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodDelete, base+"/"+missing, adminCookie, ``)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown update delete: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestProjectUpdateAuthzMatrix(t *testing.T) {
	e, pool, adminCookie, base, slug := setupProjectUpdateHTTP(t)

	memberCookie := addWorkspaceMemberWithRole(t, e, pool, slug, "pu-member", 15)
	otherCookie := addWorkspaceMemberWithRole(t, e, pool, slug, "pu-other", 15)
	viewerCookie := addWorkspaceMemberWithRole(t, e, pool, slug, "pu-viewer", 10)
	guestCookie := addWorkspaceMemberWithRole(t, e, pool, slug, "pu-guest", 5)
	outsiderCookie := loginTestUser(t, e, pool, uniqueEmail("pu-outsider"), "test-agent", uniqueIP())

	// Member writes → 201.
	rec := postProjectUpdate(t, e, memberCookie, base, `{"status":"on_track","body":"Member update"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("member create: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	memberUpdate := decodeProjectUpdate(t, rec)

	// Other member: read OK, but touching someone else's update is 403.
	rec = getAuthed(t, e, http.MethodGet, base, otherCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("other member list: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/"+memberUpdate.ID, otherCookie, `{"body":"Hijack"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("other member patch: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodDelete, base+"/"+memberUpdate.ID, otherCookie, ``)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("other member delete: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}

	// Author edits their own → 200.
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/"+memberUpdate.ID, memberCookie, `{"body":"Edited by author"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("author patch: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	// Admin edits and deletes someone else's update → 200 / 204.
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/"+memberUpdate.ID, adminCookie, `{"body":"Edited by admin"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin patch other's: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodDelete, base+"/"+memberUpdate.ID, adminCookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("admin delete other's: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}

	// Viewer: read OK, write 403.
	rec = getAuthed(t, e, http.MethodGet, base, viewerCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("viewer list: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postProjectUpdate(t, e, viewerCookie, base, `{"status":"on_track","body":"Nope"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer create: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}

	// Guest: read 403, write 403.
	rec = getAuthed(t, e, http.MethodGet, base, guestCookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest list: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postProjectUpdate(t, e, guestCookie, base, `{"status":"on_track","body":"Nope"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest create: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}

	// Non-member: 404 workspace-not-found (tenancy boundary).
	rec = getAuthed(t, e, http.MethodGet, base, outsiderCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("outsider list: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
}
