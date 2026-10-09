package api

// Saved view HTTP endpoint tests (C9T2): create (201 / 409 duplicate /
// 400 bad name), list shape, per-user isolation, project scoping (404 on
// unknown/invisible project, 400 on malformed ids), rename + default via
// PATCH, delete (204 idempotent), and 401 unauthenticated. Real test
// database, no skips.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
)

type viewHTTPFixture struct {
	pool      *pgxpool.Pool
	e         *echo.Echo
	cookieA   *http.Cookie
	cookieB   *http.Cookie
	projectID string
	viewsBase string
}

func setupViewHTTPFixture(t *testing.T) *viewHTTPFixture {
	t.Helper()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueViewRoutes(e, &IssueViewHandler{Pool: pool})

	fx := &viewHTTPFixture{pool: pool, e: e}
	fx.cookieA = loginTestUser(t, e, pool, uniqueEmail("vw-a"), "test-agent", uniqueIP())
	fx.cookieB = loginTestUser(t, e, pool, uniqueEmail("vw-b"), "test-agent", uniqueIP())

	slug := uniqueSlug("vw")
	createWorkspaceHTTP(t, e, fx.cookieA, "Views Co", slug)
	ident := uniqueProjectIdentifier("VW")
	createProjectHTTP(t, e, fx.cookieA, slug, "Views", ident)
	if err := pool.QueryRow(context.Background(),
		`SELECT p.id::text FROM projects p
		 JOIN workspaces w ON w.id = p.workspace_id
		 WHERE w.slug = $1 AND p.identifier = $2`,
		slug, ident).Scan(&fx.projectID); err != nil {
		t.Fatalf("project id: %v", err)
	}
	fx.viewsBase = "/api/v1/projects/" + fx.projectID + "/views"
	return fx
}

func postView(t *testing.T, fx *viewHTTPFixture, cookie *http.Cookie, body string) *httptest.ResponseRecorder {
	t.Helper()
	return postAuthedJSON(t, fx.e, http.MethodPost, fx.viewsBase, cookie, body)
}

func getViews(t *testing.T, fx *viewHTTPFixture, cookie *http.Cookie, base string) *httptest.ResponseRecorder {
	t.Helper()
	return getAuthed(t, fx.e, http.MethodGet, base, cookie)
}

func decodeViews(t *testing.T, rec *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	var payload struct {
		Views []map[string]any `json:"views"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode views: %v (body: %s)", err, rec.Body.String())
	}
	return payload.Views
}

func viewPayload(name string) string {
	return fmt.Sprintf(`{"name":%q,"filters":{"q":"bug","priorities":[1,3]},"display":{"groupBy":"priority"}}`, name)
}

func TestIssueViewCreateListHTTP(t *testing.T) {
	fx := setupViewHTTPFixture(t)

	// Create → 201.
	rec := postView(t, fx, fx.cookieA, viewPayload("My bugs"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create view: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if created["name"] != "My bugs" || created["id"] == "" {
		t.Fatalf("created = %v, want name=My bugs with id", created)
	}
	if created["is_default"] != false {
		t.Fatalf("created is_default = %v, want false", created["is_default"])
	}
	viewID := created["id"].(string)

	// List → 200 {views:[...]} with the filters round-tripped.
	rec = getViews(t, fx, fx.cookieA, fx.viewsBase)
	if rec.Code != http.StatusOK {
		t.Fatalf("list views: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	views := decodeViews(t, rec)
	if len(views) != 1 || views[0]["id"] != viewID {
		t.Fatalf("views = %v, want the created view", views)
	}
	filters, ok := views[0]["filters"].(map[string]any)
	if !ok || filters["q"] != "bug" {
		t.Fatalf("filters = %v, want q=bug", views[0]["filters"])
	}
	if views[0]["display"] == nil {
		t.Fatalf("display missing from list item")
	}
}

func TestIssueViewDuplicateNameHTTP(t *testing.T) {
	fx := setupViewHTTPFixture(t)
	rec := postView(t, fx, fx.cookieA, viewPayload("My bugs"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201", rec.Code)
	}
	// Duplicate (case-insensitive) → 409 with the conflict envelope.
	rec = postView(t, fx, fx.cookieA, viewPayload("MY BUGS"))
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if env.Error.Code != ErrCodeConflict {
		t.Fatalf("error code = %q, want conflict", env.Error.Code)
	}
}

func TestIssueViewBadInputHTTP(t *testing.T) {
	fx := setupViewHTTPFixture(t)

	// Blank name → 400.
	rec := postView(t, fx, fx.cookieA, viewPayload("   "))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("blank name: status = %d, want 400", rec.Code)
	}
	// Missing filters → 400.
	rec = postView(t, fx, fx.cookieA, `{"name":"ok"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing filters: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	// Non-object filters → 400.
	rec = postView(t, fx, fx.cookieA, `{"name":"ok","filters":[1]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("array filters: status = %d, want 400", rec.Code)
	}
	// Malformed project id → 400.
	rec = postAuthedJSON(t, fx.e, http.MethodGet, "/api/v1/projects/nope/views", fx.cookieA, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed project id: status = %d, want 400", rec.Code)
	}
	// Unknown project id → 404.
	rec = getViews(t, fx, fx.cookieA, "/api/v1/projects/00000000-0000-0000-0000-000000000000/views")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown project: status = %d, want 404", rec.Code)
	}
	// Unauthenticated → 401.
	req := httptest.NewRequest(http.MethodPost, fx.viewsBase, strings.NewReader(viewPayload("x")))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	recorder := httptest.NewRecorder()
	fx.e.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: status = %d, want 401 (body: %s)", recorder.Code, recorder.Body.String())
	}
}

func TestIssueViewPatchDeleteHTTP(t *testing.T) {
	fx := setupViewHTTPFixture(t)
	rec := postView(t, fx, fx.cookieA, viewPayload("Alpha"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d", rec.Code)
	}
	id := decodeViews(t, getViews(t, fx, fx.cookieA, fx.viewsBase))[0]["id"].(string)

	// PATCH rename → 200.
	rec = postAuthedJSON(t, fx.e, http.MethodPatch, fx.viewsBase+"/"+id, fx.cookieA,
		`{"name":"Beta"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch rename: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var renamed map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &renamed); err != nil {
		t.Fatalf("decode renamed: %v", err)
	}
	if renamed["name"] != "Beta" {
		t.Fatalf("renamed = %v", renamed)
	}

	// PATCH {} → 400 (nothing to update).
	rec = postAuthedJSON(t, fx.e, http.MethodPatch, fx.viewsBase+"/"+id, fx.cookieA, `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty patch: status = %d, want 400", rec.Code)
	}

	// PATCH set default → 200 with is_default true.
	rec = postAuthedJSON(t, fx.e, http.MethodPatch, fx.viewsBase+"/"+id, fx.cookieA,
		`{"is_default":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch default: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	// PATCH unknown view → 404.
	rec = postAuthedJSON(t, fx.e, http.MethodPatch, fx.viewsBase+"/00000000-0000-0000-0000-000000000000",
		fx.cookieA, `{"name":"x"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("patch unknown view: status = %d, want 404", rec.Code)
	}

	// DELETE → 204; DELETE again → 204 (idempotent).
	rec = postAuthedJSON(t, fx.e, http.MethodDelete, fx.viewsBase+"/"+id, fx.cookieA, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d, want 204", rec.Code)
	}
	rec = postAuthedJSON(t, fx.e, http.MethodDelete, fx.viewsBase+"/"+id, fx.cookieA, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("re-delete: status = %d, want 204", rec.Code)
	}
	if views := decodeViews(t, getViews(t, fx, fx.cookieA, fx.viewsBase)); len(views) != 0 {
		t.Fatalf("views after delete = %d, want 0", len(views))
	}
}

func TestIssueViewUserIsolationHTTP(t *testing.T) {
	fx := setupViewHTTPFixture(t)

	// cookieB is NOT a member of the workspace: even listing is 404
	// (tenancy boundary — no existence leak).
	rec := getViews(t, fx, fx.cookieB, fx.viewsBase)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("non-member list: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}

	// Make cookieB a member (directly: the HTTP member-add path is
	// covered by workspace tests), then verify they still cannot see
	// cookieA's views.
	rec = postView(t, fx, fx.cookieA, viewPayload("Mine"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d", rec.Code)
	}
	if _, err := fx.pool.Exec(context.Background(),
		`INSERT INTO workspace_members (workspace_id, user_id, role)
		 SELECT w.id, $2::uuid, 15 FROM workspaces w
		 JOIN projects p ON p.workspace_id = w.id
		 WHERE p.id = $1::uuid
		 ON CONFLICT DO NOTHING`,
		fx.projectID, authedUserID(t, fx.e, fx.cookieB)); err != nil {
		t.Fatalf("add member: %v", err)
	}

	// B's list is empty — A's views are invisible.
	if views := decodeViews(t, getViews(t, fx, fx.cookieB, fx.viewsBase)); len(views) != 0 {
		t.Fatalf("member B sees %d views, want 0", len(views))
	}
	// B can create their own view with the same name.
	rec = postView(t, fx, fx.cookieB, viewPayload("Mine"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("B create same name: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestIssueViewDefaultExclusivityHTTP(t *testing.T) {
	fx := setupViewHTTPFixture(t)
	var ids []string
	for _, name := range []string{"One", "Two"} {
		rec := postView(t, fx, fx.cookieA, viewPayload(name))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %q: status = %d", name, rec.Code)
		}
		var v map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatalf("decode: %v", err)
		}
		ids = append(ids, v["id"].(string))
	}
	// Set One default, then Two default: exactly one default survives.
	for _, id := range ids {
		rec := postAuthedJSON(t, fx.e, http.MethodPatch, fx.viewsBase+"/"+id, fx.cookieA,
			`{"is_default":true}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("set default: status = %d", rec.Code)
		}
	}
	views := decodeViews(t, getViews(t, fx, fx.cookieA, fx.viewsBase))
	defaults := 0
	for _, v := range views {
		if v["is_default"] == true {
			defaults++
			if v["id"] != ids[1] {
				t.Fatalf("default = %v, want Two", v["name"])
			}
		}
	}
	if defaults != 1 {
		t.Fatalf("defaults = %d, want 1", defaults)
	}
}

func TestIssueViewErrorEnvelopeShape(t *testing.T) {
	fx := setupViewHTTPFixture(t)
	// Every error path answers with the spec §5 envelope.
	rec := getViews(t, fx, fx.cookieA, "/api/v1/projects/00000000-0000-0000-0000-000000000000/views")
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Error.Code != "not_found" || !strings.Contains(env.Error.Message, "project") {
		t.Fatalf("envelope = %+v, want not_found/project", env.Error)
	}
}
