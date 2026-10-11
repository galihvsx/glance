package api

// Project HTTP endpoint tests (Task 12, fix round 1): PATCH partial-update
// semantics — omitted fields stay untouched, explicit "" clears, {} is 400
// bad_request. Real test database, no skips.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
)

// projTestSeq hands out process-unique numbers for project identifiers.
var projTestSeq atomic.Int64

func uniqueProjectIdentifier(prefix string) string {
	return fmt.Sprintf("%s%05d%04d", prefix, os.Getpid()%100000, int(projTestSeq.Add(1))%10000)
}

func testProjectServer(t *testing.T, pool *pgxpool.Pool) *echo.Echo {
	t.Helper()
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	return e
}

func createProjectHTTP(t *testing.T, e *echo.Echo, cookie *http.Cookie, wsSlug, name, identifier string) {
	t.Helper()
	rec := postAuthedJSON(t, e, http.MethodPost,
		"/api/v1/workspaces/"+wsSlug+"/projects", cookie,
		fmt.Sprintf(`{"name":%q,"identifier":%q}`, name, identifier))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create project: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
}

func getProjectHTTP(t *testing.T, e *echo.Echo, cookie *http.Cookie, wsSlug, identifier string) (name, description string) {
	t.Helper()
	rec := getAuthed(t, e, http.MethodGet,
		"/api/v1/workspaces/"+wsSlug+"/projects/"+identifier, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get project: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var p struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode project: %v", err)
	}
	return p.Name, p.Description
}

func TestProjectPatchPartialSemantics(t *testing.T) {
	pool := newTestPool(t)
	e := testProjectServer(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("h-proj-patch"), "test-agent/1.0", uniqueIP())
	slug := uniqueSlug("proj-patch-http")
	createWorkspaceHTTP(t, e, cookie, "Patch Co", slug)
	ident := uniqueProjectIdentifier("HT")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident

	// Set a description first.
	rec := postAuthedJSON(t, e, http.MethodPatch, base, cookie, `{"description":"Core team work"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch description: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	// PATCH with only name: description must survive.
	rec = postAuthedJSON(t, e, http.MethodPatch, base, cookie, `{"name":"Engineering Two"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch name-only: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	name, desc := getProjectHTTP(t, e, cookie, slug, ident)
	if name != "Engineering Two" || desc != "Core team work" {
		t.Fatalf("after name-only patch: name=%q desc=%q, want name changed and desc intact", name, desc)
	}

	// PATCH with explicit "" clears the description (null vs empty distinct).
	rec = postAuthedJSON(t, e, http.MethodPatch, base, cookie, `{"description":""}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch clear description: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	name, desc = getProjectHTTP(t, e, cookie, slug, ident)
	if name != "Engineering Two" || desc != "" {
		t.Fatalf("after clear patch: name=%q desc=%q, want desc cleared and name intact", name, desc)
	}

	// PATCH with {} is 400 bad_request, not a silent no-op.
	rec = postAuthedJSON(t, e, http.MethodPatch, base, cookie, `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("patch empty body: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode 400: %v", err)
	}
	if env.Error.Code != "bad_request" || env.Error.Message != "nothing to update" {
		t.Fatalf("400 envelope = %+v, want code=bad_request message=nothing to update", env.Error)
	}
}

func decodeProjectIcon(t *testing.T, recBody []byte) *string {
	t.Helper()
	var p struct {
		Icon *string `json:"icon"`
	}
	if err := json.Unmarshal(recBody, &p); err != nil {
		t.Fatalf("decode project: %v", err)
	}
	return p.Icon
}

// TestProjectIconHTTP exercises the icon through the API: POST accepts an
// optional icon (invalid → 400), GET returns it, PATCH sets/clears it
// (invalid → 400).
func TestProjectIconHTTP(t *testing.T) {
	pool := newTestPool(t)
	e := testProjectServer(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("proj-icon-http"), "test-agent/1.0", uniqueIP())
	slug := uniqueSlug("proj-icon-http")
	createWorkspaceHTTP(t, e, cookie, "Icon Co", slug)

	// POST with a valid icon: 201, icon echoed.
	ident := uniqueProjectIdentifier("IC")
	rec := postAuthedJSON(t, e, http.MethodPost,
		"/api/v1/workspaces/"+slug+"/projects", cookie,
		fmt.Sprintf(`{"name":%q,"identifier":%q,"icon":"🚀"}`, "Rocket", ident))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create with icon: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	if icon := decodeProjectIcon(t, rec.Body.Bytes()); icon == nil || *icon != "🚀" {
		t.Fatalf("create icon = %v, want 🚀", icon)
	}

	// POST with an invalid icon: 400 bad_request.
	rec = postAuthedJSON(t, e, http.MethodPost,
		"/api/v1/workspaces/"+slug+"/projects", cookie,
		fmt.Sprintf(`{"name":%q,"identifier":%q,"icon":"not-an-emoji"}`, "Bad", uniqueProjectIdentifier("IB")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("create bad icon: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// GET returns the icon.
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident
	rec = getAuthed(t, e, http.MethodGet, base, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get project: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if icon := decodeProjectIcon(t, rec.Body.Bytes()); icon == nil || *icon != "🚀" {
		t.Fatalf("get icon = %v, want 🚀", icon)
	}

	// PATCH changes it.
	rec = postAuthedJSON(t, e, http.MethodPatch, base, cookie, `{"icon":"🎯"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch icon: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if icon := decodeProjectIcon(t, rec.Body.Bytes()); icon == nil || *icon != "🎯" {
		t.Fatalf("patch icon = %v, want 🎯", icon)
	}

	// PATCH with a ZWJ sequence: 400.
	rec = postAuthedJSON(t, e, http.MethodPatch, base, cookie, `{"icon":"👨‍👩‍👧"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("patch zwj icon: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// PATCH with explicit "": clears to NULL (icon omitted from payload).
	rec = postAuthedJSON(t, e, http.MethodPatch, base, cookie, `{"icon":""}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear icon: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if icon := decodeProjectIcon(t, rec.Body.Bytes()); icon != nil {
		t.Fatalf("clear icon = %q, want NULL", *icon)
	}

	// GET list includes the icon field contract (null here after clear).
	rec = getAuthed(t, e, http.MethodGet, "/api/v1/workspaces/"+slug+"/projects", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list projects: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var list struct {
		Projects []struct {
			Identifier string  `json:"identifier"`
			Icon       *string `json:"icon"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	found := false
	for _, p := range list.Projects {
		if p.Identifier == ident {
			found = true
			if p.Icon != nil {
				t.Fatalf("list icon = %q, want NULL after clear", *p.Icon)
			}
		}
	}
	if !found {
		t.Fatalf("project %s missing from list", ident)
	}
}
