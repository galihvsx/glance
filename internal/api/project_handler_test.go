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
	migrateTestDB(t, pool)
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
