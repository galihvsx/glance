package api

// Workspace-scoped saved view HTTP endpoints (C113T0): create (201 /
// 409 duplicate / 400 bad name), list shape, shared-view visibility,
// non-owner PATCH 403, admin DELETE of a shared view, DELETE 204
// idempotent, 404 for unknown and non-member workspaces, and 401
// unauthenticated. Real test database, no skips. Mirrors
// issue_view_handler_test.go / issue_view_shared_handler_test.go.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
)

type wsViewHTTPFixture struct {
	pool      *pgxpool.Pool
	e         *echo.Echo
	cookieA   *http.Cookie
	cookieB   *http.Cookie
	cookieC   *http.Cookie
	slug      string
	viewsBase string
}

func setupWSViewHTTPFixture(t *testing.T) *wsViewHTTPFixture {
	t.Helper()
	pool := newTestPool(t)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterIssueViewRoutes(e, &IssueViewHandler{Pool: pool})

	fx := &wsViewHTTPFixture{pool: pool, e: e}
	fx.cookieA = loginTestUser(t, e, pool, uniqueEmail("wsv-a"), "test-agent", uniqueIP())
	fx.cookieB = loginTestUser(t, e, pool, uniqueEmail("wsv-b"), "test-agent", uniqueIP())
	fx.cookieC = loginTestUser(t, e, pool, uniqueEmail("wsv-c"), "test-agent", uniqueIP())

	fx.slug = uniqueSlug("wsv")
	createWorkspaceHTTP(t, e, fx.cookieA, "WS Views Co", fx.slug)
	// B is a plain member (15), C a second workspace admin (20);
	// A is the workspace creator (admin). cookieB/C start as
	// non-members until these rows land.
	userID := func(c *http.Cookie) string { return authedUserID(t, e, c) }
	for _, tc := range []struct {
		cookie *http.Cookie
		role   int
	}{{fx.cookieB, 15}, {fx.cookieC, 20}} {
		if _, err := pool.Exec(context.Background(),
			`INSERT INTO workspace_members (workspace_id, user_id, role)
			 SELECT w.id, $2::uuid, $3 FROM workspaces w
			 WHERE w.slug = $1
			 ON CONFLICT (workspace_id, user_id) DO UPDATE SET role = EXCLUDED.role`,
			fx.slug, userID(tc.cookie), tc.role); err != nil {
			t.Fatalf("add member: %v", err)
		}
	}
	fx.viewsBase = "/api/v1/workspaces/" + fx.slug + "/views"
	return fx
}

func postWSView(t *testing.T, fx *wsViewHTTPFixture, cookie *http.Cookie, body string) *httptest.ResponseRecorder {
	t.Helper()
	return postAuthedJSON(t, fx.e, http.MethodPost, fx.viewsBase, cookie, body)
}

func wsViewPayload(name string) string {
	return `{"name":"` + name + `","filters":{"pql":"state = \"Todo\""}}`
}

func TestWorkspaceIssueViewCreateListHTTP(t *testing.T) {
	fx := setupWSViewHTTPFixture(t)

	rec := postWSView(t, fx, fx.cookieA, wsViewPayload("My work"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create view: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if created["name"] != "My work" || created["id"] == "" {
		t.Fatalf("created = %v, want name=My work with id", created)
	}
	if created["is_default"] != false {
		t.Fatalf("created is_default = %v, want false", created["is_default"])
	}
	viewID := created["id"].(string)

	rec = getAuthed(t, fx.e, http.MethodGet, fx.viewsBase, fx.cookieA)
	if rec.Code != http.StatusOK {
		t.Fatalf("list views: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	views := decodeViews(t, rec)
	if len(views) != 1 || views[0]["id"] != viewID {
		t.Fatalf("views = %v, want the created view", views)
	}
	filters, ok := views[0]["filters"].(map[string]any)
	if !ok || filters["pql"] != `state = "Todo"` {
		t.Fatalf("filters = %v, want pql round-tripped", views[0]["filters"])
	}

	// Duplicate name (case-insensitive) → 409 conflict envelope.
	rec = postWSView(t, fx, fx.cookieA, wsViewPayload("my WORK"))
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate create: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}

	// Bad name → 400.
	rec = postWSView(t, fx, fx.cookieA, `{"name":"","filters":{"pql":"x"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("blank name: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// Bad filters (not an object) → 400.
	rec = postWSView(t, fx, fx.cookieA, `{"name":"ok","filters":[1,2]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("array filters: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestWorkspaceIssueViewPatchDeleteHTTP(t *testing.T) {
	fx := setupWSViewHTTPFixture(t)

	rec := postWSView(t, fx, fx.cookieA, wsViewPayload("Team work"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d", rec.Code)
	}
	id := decodeViews(t, getAuthed(t, fx.e, http.MethodGet, fx.viewsBase, fx.cookieA))[0]["id"].(string)

	// Owner renames + sets default → 200.
	rec = postAuthedJSON(t, fx.e, http.MethodPatch, fx.viewsBase+"/"+id, fx.cookieA,
		`{"name":"Team work 2","is_default":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner patch: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var updated map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated: %v", err)
	}
	if updated["name"] != "Team work 2" || updated["is_default"] != true {
		t.Fatalf("updated = %v, want renamed + default", updated)
	}

	// Owner shares → 200; member B's list shows it annotated.
	rec = postAuthedJSON(t, fx.e, http.MethodPatch, fx.viewsBase+"/"+id, fx.cookieA, `{"shared":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner share: status = %d, want 200", rec.Code)
	}
	views := decodeViews(t, getAuthed(t, fx.e, http.MethodGet, fx.viewsBase, fx.cookieB))
	if len(views) != 1 || views[0]["shared"] != true || views[0]["owner_name"] == nil {
		t.Fatalf("B's list = %v, want the shared view annotated", views)
	}

	// Non-owner member PATCH on a shared view → 403.
	rec = postAuthedJSON(t, fx.e, http.MethodPatch, fx.viewsBase+"/"+id, fx.cookieB, `{"name":"Hijacked"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-owner patch of shared view: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}

	// Non-owner member DELETE of a shared view → 403.
	rec = postAuthedJSON(t, fx.e, http.MethodDelete, fx.viewsBase+"/"+id, fx.cookieB, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member delete of shared view: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}

	// Workspace admin DELETE of a shared view → 204.
	rec = postAuthedJSON(t, fx.e, http.MethodDelete, fx.viewsBase+"/"+id, fx.cookieC, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("admin delete of shared view: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}

	// DELETE again → still 204 (idempotent).
	rec = postAuthedJSON(t, fx.e, http.MethodDelete, fx.viewsBase+"/"+id, fx.cookieA, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete again: status = %d, want 204", rec.Code)
	}
}

func TestWorkspaceIssueViewTenancyHTTP(t *testing.T) {
	fx := setupWSViewHTTPFixture(t)
	outsider := loginTestUser(t, fx.e, fx.pool, uniqueEmail("wsv-out"), "test-agent", uniqueIP())

	// Unknown workspace → 404.
	rec := getAuthed(t, fx.e, http.MethodGet, "/api/v1/workspaces/no-such-ws/views", fx.cookieA)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown workspace list: status = %d, want 404", rec.Code)
	}

	// Non-member workspace → the SAME 404 (no existence leak).
	rec = getAuthed(t, fx.e, http.MethodGet, fx.viewsBase, outsider)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("non-member list: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, fx.e, http.MethodPost, fx.viewsBase, outsider, wsViewPayload("x"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("non-member create: status = %d, want 404", rec.Code)
	}

	// Private view of another member: PATCH by a non-owner member → 404
	// (not 403 — the view is invisible to them).
	rec = postWSView(t, fx, fx.cookieA, wsViewPayload("Secret"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create private: status = %d", rec.Code)
	}
	id := decodeViews(t, getAuthed(t, fx.e, http.MethodGet, fx.viewsBase, fx.cookieA))[0]["id"].(string)
	rec = postAuthedJSON(t, fx.e, http.MethodPatch, fx.viewsBase+"/"+id, fx.cookieB, `{"name":"Hijacked"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("non-owner patch of private view: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	// ... and DELETE of another member's private view → 204 no-op.
	rec = postAuthedJSON(t, fx.e, http.MethodDelete, fx.viewsBase+"/"+id, fx.cookieB, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("member delete of private view: status = %d, want 204", rec.Code)
	}

	// Unauthenticated → 401.
	req := httptest.NewRequest(http.MethodGet, fx.viewsBase, nil)
	recorder := httptest.NewRecorder()
	fx.e.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated list: status = %d, want 401", recorder.Code)
	}
}
