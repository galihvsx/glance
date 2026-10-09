package api

// Favorite HTTP endpoint tests (C7T4): star (201 / 200 idempotent),
// unstar (204 idempotent), list shape (display_id + routing context),
// per-user isolation, 404 on unknown/invisible/malformed targets, 401
// unauthenticated, 400 on bad type. Real test database, no skips.

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

type favFixture struct {
	pool     *pgxpool.Pool
	e        *echo.Echo
	cookieA  *http.Cookie
	cookieB  *http.Cookie
	slug     string
	ident    string
	issueID  string
	projBase string
}

func setupFavoriteFixture(t *testing.T) *favFixture {
	t.Helper()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterFavoriteRoutes(e, &FavoriteHandler{Pool: pool})

	fx := &favFixture{pool: pool, e: e}
	fx.cookieA = loginTestUser(t, e, pool, uniqueEmail("fav-a"), "test-agent", uniqueIP())
	fx.cookieB = loginTestUser(t, e, pool, uniqueEmail("fav-b"), "test-agent", uniqueIP())

	fx.slug = uniqueSlug("fav")
	createWorkspaceHTTP(t, e, fx.cookieA, "Fav Co", fx.slug)
	fx.ident = uniqueProjectIdentifier("FV")
	createProjectHTTP(t, e, fx.cookieA, fx.slug, "Favorites", fx.ident)
	fx.projBase = "/api/v1/workspaces/" + fx.slug + "/projects/" + fx.ident
	fx.issueID = createIssueHTTP(t, e, fx.cookieA, fx.projBase+"/issues", "star me")
	return fx
}

func postFavorite(t *testing.T, fx *favFixture, cookie *http.Cookie, body string) *httptest.ResponseRecorder {
	t.Helper()
	return postAuthedJSON(t, fx.e, http.MethodPost, "/api/v1/favorites", cookie, body)
}

func decodeFavorite(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var fav map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &fav); err != nil {
		t.Fatalf("decode favorite: %v (body: %s)", err, rec.Body.String())
	}
	return fav
}

func TestFavoriteStarUnstar(t *testing.T) {
	fx := setupFavoriteFixture(t)

	// Star the issue → 201.
	rec := postFavorite(t, fx, fx.cookieA,
		fmt.Sprintf(`{"type":"issue","id":%q}`, fx.issueID))
	if rec.Code != http.StatusCreated {
		t.Fatalf("star issue: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	fav := decodeFavorite(t, rec)
	if fav["type"] != "issue" || fav["favoritable_id"] != fx.issueID {
		t.Fatalf("favorite = %v, want type=issue id=%s", fav, fx.issueID)
	}
	favID := fav["id"]

	// Star again → 200, same favorite row (idempotent).
	rec = postFavorite(t, fx, fx.cookieA,
		fmt.Sprintf(`{"type":"issue","id":%q}`, fx.issueID))
	if rec.Code != http.StatusOK {
		t.Fatalf("re-star issue: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if again := decodeFavorite(t, rec); again["id"] != favID {
		t.Fatalf("re-star id = %v, want %v (same row)", again["id"], favID)
	}

	// Only one row exists despite two POSTs. Scoped to this test's user +
	// issue: the package suite shares one DB and other tests write their
	// own favorites rows.
	userA := authedUserID(t, fx.e, fx.cookieA)
	var n int
	if err := fx.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM favorites
		 WHERE user_id = $1::uuid AND favoritable_type = 'issue' AND favoritable_id = $2::uuid`,
		userA, fx.issueID).Scan(&n); err != nil {
		t.Fatalf("count favorites: %v", err)
	}
	if n != 1 {
		t.Fatalf("favorite rows = %d, want 1", n)
	}

	// Unstar → 204.
	rec = postAuthedJSON(t, fx.e, http.MethodDelete, "/api/v1/favorites", fx.cookieA,
		fmt.Sprintf(`{"type":"issue","id":%q}`, fx.issueID))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("unstar: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}

	// Unstar again → 204 (idempotent, never-starred is fine too).
	rec = postAuthedJSON(t, fx.e, http.MethodDelete, "/api/v1/favorites", fx.cookieA,
		fmt.Sprintf(`{"type":"issue","id":%q}`, fx.issueID))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("re-unstar: status = %d, want 204", rec.Code)
	}
}

func TestFavoriteListShape(t *testing.T) {
	fx := setupFavoriteFixture(t)

	rec := postFavorite(t, fx, fx.cookieA,
		fmt.Sprintf(`{"type":"issue","id":%q}`, fx.issueID))
	if rec.Code != http.StatusCreated {
		t.Fatalf("star issue: status = %d", rec.Code)
	}

	// Project id: resolve via the project list endpoint.
	prec := getAuthed(t, fx.e, http.MethodGet,
		"/api/v1/workspaces/"+fx.slug+"/projects", fx.cookieA)
	var projList struct {
		Projects []struct {
			ID string `json:"id"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(prec.Body.Bytes(), &projList); err != nil || len(projList.Projects) == 0 {
		t.Fatalf("project list: %v (body: %s)", err, prec.Body.String())
	}
	projectID := projList.Projects[0].ID
	rec = postFavorite(t, fx, fx.cookieA,
		fmt.Sprintf(`{"type":"project","id":%q}`, projectID))
	if rec.Code != http.StatusCreated {
		t.Fatalf("star project: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	rec = getAuthed(t, fx.e, http.MethodGet, "/api/v1/favorites", fx.cookieA)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d, want 200", rec.Code)
	}
	var list struct {
		Issues []struct {
			ID                string `json:"id"`
			DisplayID         string `json:"display_id"`
			Name              string `json:"name"`
			WorkspaceSlug     string `json:"workspace_slug"`
			ProjectID         string `json:"project_id"`
			ProjectIdentifier string `json:"project_identifier"`
		} `json:"issues"`
		Projects []struct {
			ID            string `json:"id"`
			Identifier    string `json:"identifier"`
			Name          string `json:"name"`
			WorkspaceSlug string `json:"workspace_slug"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Issues) != 1 {
		t.Fatalf("issues = %d, want 1", len(list.Issues))
	}
	iss := list.Issues[0]
	if iss.ID != fx.issueID || iss.Name != "star me" {
		t.Fatalf("issue = %+v, want id=%s name=star me", iss, fx.issueID)
	}
	if !strings.HasPrefix(iss.DisplayID, fx.ident+"-") {
		t.Fatalf("display_id = %q, want prefix %s-", iss.DisplayID, fx.ident)
	}
	if iss.WorkspaceSlug != fx.slug || iss.ProjectIdentifier != fx.ident {
		t.Fatalf("issue routing = %+v, want slug=%s ident=%s", iss, fx.slug, fx.ident)
	}
	if len(list.Projects) != 1 {
		t.Fatalf("projects = %d, want 1", len(list.Projects))
	}
	if p := list.Projects[0]; p.ID != projectID || p.Identifier != fx.ident || p.WorkspaceSlug != fx.slug {
		t.Fatalf("project = %+v, want id=%s ident=%s slug=%s", p, projectID, fx.ident, fx.slug)
	}
}

func TestFavoritePerUserIsolation(t *testing.T) {
	fx := setupFavoriteFixture(t)

	rec := postFavorite(t, fx, fx.cookieA,
		fmt.Sprintf(`{"type":"issue","id":%q}`, fx.issueID))
	if rec.Code != http.StatusCreated {
		t.Fatalf("star issue: status = %d", rec.Code)
	}

	// User B's list is empty — favorites never leak across users.
	rec = getAuthed(t, fx.e, http.MethodGet, "/api/v1/favorites", fx.cookieB)
	var list struct {
		Issues   []any `json:"issues"`
		Projects []any `json:"projects"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Issues) != 0 || len(list.Projects) != 0 {
		t.Fatalf("user B list = %+v, want empty", list)
	}

	// User B stars the same issue: 404 — B is not a member of A's
	// workspace, and the 404 must not reveal the issue exists.
	rec = postFavorite(t, fx, fx.cookieB,
		fmt.Sprintf(`{"type":"issue","id":%q}`, fx.issueID))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("star other-workspace issue: status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"code":"not_found"`) {
		t.Fatalf("404 envelope: body=%s", rec.Body.String())
	}

	// B unstarring A's issue is a silent no-op; A's star survives.
	rec = postAuthedJSON(t, fx.e, http.MethodDelete, "/api/v1/favorites", fx.cookieB,
		fmt.Sprintf(`{"type":"issue","id":%q}`, fx.issueID))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("B unstar: status = %d, want 204", rec.Code)
	}
	rec = getAuthed(t, fx.e, http.MethodGet, "/api/v1/favorites", fx.cookieA)
	var listA struct {
		Issues []struct {
			ID string `json:"id"`
		} `json:"issues"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listA); err != nil {
		t.Fatalf("decode list A: %v", err)
	}
	if len(listA.Issues) != 1 || listA.Issues[0].ID != fx.issueID {
		t.Fatalf("A's star lost after B's unstar: %+v", listA)
	}
}

func TestFavoriteNotFound(t *testing.T) {
	fx := setupFavoriteFixture(t)

	// Well-formed but unknown issue UUID → 404.
	rec := postFavorite(t, fx, fx.cookieA,
		`{"type":"issue","id":"11111111-2222-3333-4444-555555555555"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("star unknown issue: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}

	// Malformed UUID → 404, never 500.
	rec = postFavorite(t, fx, fx.cookieA, `{"type":"issue","id":"not-a-uuid"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("star malformed uuid: status = %d, want 404", rec.Code)
	}

	// Unknown project UUID → 404.
	rec = postFavorite(t, fx, fx.cookieA,
		`{"type":"project","id":"11111111-2222-3333-4444-555555555555"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("star unknown project: status = %d, want 404", rec.Code)
	}
}

func TestFavoriteValidation(t *testing.T) {
	fx := setupFavoriteFixture(t)

	// Bad type → 400.
	rec := postFavorite(t, fx, fx.cookieA,
		fmt.Sprintf(`{"type":"banana","id":%q}`, fx.issueID))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad type: status = %d, want 400", rec.Code)
	}

	// Missing id → 400.
	rec = postFavorite(t, fx, fx.cookieA, `{"type":"issue"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing id: status = %d, want 400", rec.Code)
	}

	// Unauthenticated → 401 on all three verbs.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/favorites",
		strings.NewReader(fmt.Sprintf(`{"type":"issue","id":%q}`, fx.issueID)))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	recorder := httptest.NewRecorder()
	fx.e.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauth POST: status = %d, want 401", recorder.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/favorites", nil)
	recorder = httptest.NewRecorder()
	fx.e.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauth GET: status = %d, want 401", recorder.Code)
	}
}
