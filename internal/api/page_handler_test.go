package api

// Page HTTP endpoint tests (C3T3): full CRUD through the routes, move
// tri-state (missing key vs explicit null vs uuid) + cycle guard, and
// revision list/restore. Real test database, no skips.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
)

func setupPageHTTP(t *testing.T, prefix string) (*echo.Echo, *http.Cookie, string) {
	t.Helper()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterPageRoutes(e, &IssueHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail(prefix), "test-agent", uniqueIP())
	slug := uniqueSlug(prefix)
	createWorkspaceHTTP(t, e, cookie, "Page Co", slug)
	ident := uniqueProjectIdentifier("PH")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	return e, cookie, "/api/v1/workspaces/" + slug + "/projects/" + ident + "/pages"
}

func createPageHTTP(t *testing.T, e *echo.Echo, cookie *http.Cookie, base, body string) string {
	t.Helper()
	rec := postAuthedJSON(t, e, http.MethodPost, base, cookie, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create page: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode page: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("created page has no id: %s", rec.Body.String())
	}
	return created.ID
}

func TestPageHTTP(t *testing.T) {
	e, cookie, pageBase := setupPageHTTP(t, "page-http")

	// Create root → 201.
	rootID := createPageHTTP(t, e, cookie, pageBase, `{"title":"Root","content":"# Hi"}`)

	// Empty title → 400 envelope.
	rec := postAuthedJSON(t, e, http.MethodPost, pageBase, cookie, `{"title":"  "}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty title: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"bad_request"`) {
		t.Fatalf("400 envelope: body=%s", rec.Body.String())
	}

	// Create child → 201, then list shows both (flat, root first).
	childID := createPageHTTP(t, e, cookie, pageBase, `{"title":"Child","parent_id":"`+rootID+`"}`)
	rec = getAuthed(t, e, http.MethodGet, pageBase, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list pages: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var list struct {
		Pages []struct {
			ID       string  `json:"id"`
			ParentID *string `json:"parent_id"`
			Title    string  `json:"title"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Pages) != 2 || list.Pages[0].ID != rootID || list.Pages[1].ID != childID {
		t.Fatalf("list = %+v, want [root child]", list.Pages)
	}

	// Get one → 200.
	rec = getAuthed(t, e, http.MethodGet, pageBase+"/"+childID, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get page: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	// Malformed id → 400.
	rec = getAuthed(t, e, http.MethodGet, pageBase+"/nope", cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed id: status = %d, want 400", rec.Code)
	}

	// PATCH title → 200; revision recorded.
	rec = postAuthedJSON(t, e, http.MethodPatch, pageBase+"/"+rootID, cookie, `{"title":"Root v2"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch page: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, pageBase+"/"+rootID+"/revisions", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list revisions: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var revs struct {
		Revisions []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"revisions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &revs); err != nil {
		t.Fatalf("decode revisions: %v", err)
	}
	if len(revs.Revisions) != 1 || revs.Revisions[0].Title != "Root" {
		t.Fatalf("revisions = %+v, want one pre-update snapshot", revs.Revisions)
	}

	// Restore → 200, title back to v1.
	revID := revs.Revisions[0].ID
	rec = postAuthedJSON(t, e, http.MethodPost, pageBase+"/"+rootID+"/restore", cookie,
		`{"revision_id":"`+revID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("restore: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"title":"Root"`) {
		t.Fatalf("restored body = %s, want title Root", rec.Body.String())
	}

	// Unknown revision → 404.
	rec = postAuthedJSON(t, e, http.MethodPost, pageBase+"/"+rootID+"/restore", cookie,
		`{"revision_id":"00000000-0000-4000-8000-000000000000"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown revision: status = %d, want 404", rec.Code)
	}

	// Move: cycle guard — reparent root under its own child → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, pageBase+"/"+rootID+"/move", cookie,
		`{"parent_id":"`+childID+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("cycle move: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "cycle") {
		t.Fatalf("cycle move body = %s, want cycle mention", rec.Body.String())
	}

	// Move: explicit null → root.
	rec = postAuthedJSON(t, e, http.MethodPost, pageBase+"/"+childID+"/move", cookie,
		`{"parent_id":null}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("move to root: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"parent_id":null`) && strings.Contains(rec.Body.String(), `"parent_id"`) {
		t.Fatalf("move to root body = %s, want null parent", rec.Body.String())
	}

	// Move: missing parent_id keeps parent (reorder only).
	rec = postAuthedJSON(t, e, http.MethodPost, pageBase+"/"+childID+"/move", cookie,
		`{"position":0}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("reorder: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	// Delete root → 204; child (now root) still there.
	rec = postAuthedJSON(t, e, http.MethodDelete, pageBase+"/"+rootID, cookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete page: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, pageBase+"/"+childID, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("child after root delete: status = %d, want 200 (child was moved out)", rec.Code)
	}

	// Unauthenticated → 401.
	req := httptest.NewRequest(http.MethodGet, pageBase, nil)
	rec2 := httptest.NewRecorder()
	e.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("unauth: status = %d, want 401", rec2.Code)
	}
}
