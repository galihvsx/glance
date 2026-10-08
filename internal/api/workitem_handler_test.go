package api

// Work-item HTTP endpoint tests (cycle 1, T2): the two spec §5 endpoints
// annotated "planned, NOT in v0.1.0" — GET /api/v1/work-items/{display-id}
// and GET /api/v1/search?q=. Real test database, no skips.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
)

func testWorkItemServer(t *testing.T, pool *pgxpool.Pool) *echo.Echo {
	t.Helper()
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterWorkItemRoutes(e, &IssueHandler{Pool: pool})
	return e
}

func createWorkItemHTTP(t *testing.T, e *echo.Echo, cookie *http.Cookie, base, body string) (id, displayID string) {
	t.Helper()
	rec := postAuthedJSON(t, e, http.MethodPost, base, cookie, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create issue: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID        string `json:"id"`
		DisplayID string `json:"display_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	return created.ID, created.DisplayID
}

func TestWorkItemByDisplayID(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testWorkItemServer(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("workitem"), "test-agent", uniqueIP())
	slug := uniqueSlug("workitem")
	createWorkspaceHTTP(t, e, cookie, "Workitem Co", slug)
	ident := uniqueProjectIdentifier("WI")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"

	uuid, displayID := createWorkItemHTTP(t, e, cookie, base, `{"name":"Ship it","priority":2}`)

	// GET by display-id → 200, same shape as GET by UUID.
	rec := getAuthed(t, e, http.MethodGet, "/api/v1/work-items/"+displayID, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get work-item %s: status = %d, want 200 (body: %s)", displayID, rec.Code, rec.Body.String())
	}
	var byDisplay, byUUID map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &byDisplay); err != nil {
		t.Fatalf("decode work-item: %v", err)
	}
	rec = getAuthed(t, e, http.MethodGet, base+"/"+uuid, cookie)
	if err := json.Unmarshal(rec.Body.Bytes(), &byUUID); err != nil {
		t.Fatalf("decode issue: %v", err)
	}
	for _, k := range []string{"id", "display_id", "name", "priority", "sequence_id", "state_id"} {
		if fmt.Sprint(byDisplay[k]) != fmt.Sprint(byUUID[k]) {
			t.Fatalf("field %s: display-id response %v != uuid response %v", k, byDisplay[k], byUUID[k])
		}
	}

	// Lowercase identifier normalizes and resolves.
	rec = getAuthed(t, e, http.MethodGet, "/api/v1/work-items/"+strings.ToLower(displayID), cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("lowercase display-id: status = %d, want 200", rec.Code)
	}

	// Unknown sequence and malformed IDs → 404 envelope, never 400/500.
	for _, bad := range []string{ident + "-999", "nope", ident + "-", "-5", ident + "-0", ident + "-abc", ident + "--1", "--"} {
		rec = getAuthed(t, e, http.MethodGet, "/api/v1/work-items/"+bad, cookie)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET /api/v1/work-items/%s: status = %d, want 404", bad, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `"code":"not_found"`) {
			t.Fatalf("GET /api/v1/work-items/%s: body %s lacks not_found envelope", bad, rec.Body.String())
		}
	}

	// Unauthenticated → 401.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/work-items/"+displayID, nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: status = %d, want 401", rec.Code)
	}
}

func TestWorkItemByDisplayIDTenancy(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testWorkItemServer(t, pool)

	cookie1 := loginTestUser(t, e, pool, uniqueEmail("wi-ten1"), "test-agent", uniqueIP())
	cookie2 := loginTestUser(t, e, pool, uniqueEmail("wi-ten2"), "test-agent", uniqueIP())

	// Same identifier in two workspaces; user1 is a member of both.
	ident := uniqueProjectIdentifier("AM")
	slug1 := uniqueSlug("wi-ten1")
	createWorkspaceHTTP(t, e, cookie1, "WS One", slug1)
	createProjectHTTP(t, e, cookie1, slug1, "Eng One", ident)
	base1 := "/api/v1/workspaces/" + slug1 + "/projects/" + ident + "/issues"
	_, disp1 := createWorkItemHTTP(t, e, cookie1, base1, `{"name":"one"}`)

	slug2 := uniqueSlug("wi-ten2")
	createWorkspaceHTTP(t, e, cookie1, "WS Two", slug2)
	createProjectHTTP(t, e, cookie1, slug2, "Eng Two", ident)
	base2 := "/api/v1/workspaces/" + slug2 + "/projects/" + ident + "/issues"
	_, disp2 := createWorkItemHTTP(t, e, cookie1, base2, `{"name":"two"}`)

	// Ambiguous identifier across member workspaces → 404 (no enumeration).
	for _, d := range []string{disp1, disp2} {
		rec := getAuthed(t, e, http.MethodGet, "/api/v1/work-items/"+d, cookie1)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("ambiguous %s: status = %d, want 404", d, rec.Code)
		}
	}

	// Issue in a workspace the caller is not a member of → 404.
	slug3 := uniqueSlug("wi-ten3")
	createWorkspaceHTTP(t, e, cookie2, "WS Three", slug3)
	ident3 := uniqueProjectIdentifier("NM")
	createProjectHTTP(t, e, cookie2, slug3, "Eng Three", ident3)
	base3 := "/api/v1/workspaces/" + slug3 + "/projects/" + ident3 + "/issues"
	_, disp3 := createWorkItemHTTP(t, e, cookie2, base3, `{"name":"three"}`)
	rec := getAuthed(t, e, http.MethodGet, "/api/v1/work-items/"+disp3, cookie1)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("non-member %s: status = %d, want 404", disp3, rec.Code)
	}
	if strings.Contains(rec.Body.String(), "three") {
		t.Fatal("non-member 404 leaks issue content")
	}
}

func TestGlobalSearch(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testWorkItemServer(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("gsearch"), "test-agent", uniqueIP())
	slug := uniqueSlug("gsearch")
	createWorkspaceHTTP(t, e, cookie, "Search Co", slug)
	ident := uniqueProjectIdentifier("GS")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"

	// A: title-heavy match (ranks first). B: description-only match.
	// C: no match.
	createWorkItemHTTP(t, e, cookie, base, `{"name":"rocket rocket rocket"}`)
	createWorkItemHTTP(t, e, cookie, base, `{"name":"unrelated","description":{"text":"needs more rocket fuel"}}`)
	createWorkItemHTTP(t, e, cookie, base, `{"name":"totally different"}`)

	// Issue matching in a workspace the caller cannot see must not leak.
	cookie2 := loginTestUser(t, e, pool, uniqueEmail("gsearch2"), "test-agent", uniqueIP())
	slug2 := uniqueSlug("gsearch2")
	createWorkspaceHTTP(t, e, cookie2, "Hidden Co", slug2)
	ident2 := uniqueProjectIdentifier("HD")
	createProjectHTTP(t, e, cookie2, slug2, "Hidden", ident2)
	createWorkItemHTTP(t, e, cookie2, "/api/v1/workspaces/"+slug2+"/projects/"+ident2+"/issues", `{"name":"rocket secret"}`)

	var res struct {
		Results []struct {
			Name              string `json:"name"`
			DisplayID         string `json:"display_id"`
			WorkspaceSlug     string `json:"workspace_slug"`
			ProjectIdentifier string `json:"project_identifier"`
		} `json:"results"`
		NextCursor string `json:"next_cursor"`
	}
	rec := getAuthed(t, e, http.MethodGet, "/api/v1/search?q=rocket", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("search: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode search: %v", err)
	}
	if len(res.Results) != 2 {
		t.Fatalf("search results = %d, want 2 (hidden workspace must not leak)", len(res.Results))
	}
	if res.Results[0].Name != "rocket rocket rocket" {
		t.Fatalf("first result = %q, want title-heavy match ranked first", res.Results[0].Name)
	}
	for _, r := range res.Results {
		if r.DisplayID == "" || r.WorkspaceSlug != slug || r.ProjectIdentifier != ident {
			t.Fatalf("result missing context: %+v", r)
		}
	}

	// q missing / blank → 400.
	for _, path := range []string{"/api/v1/search", "/api/v1/search?q=", "/api/v1/search?q=%20%20"} {
		rec = getAuthed(t, e, http.MethodGet, path, cookie)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("GET %s: status = %d, want 400", path, rec.Code)
		}
	}

	// per_page validation matches list conventions.
	for _, path := range []string{"/api/v1/search?q=rocket&per_page=0", "/api/v1/search?q=rocket&per_page=abc"} {
		rec = getAuthed(t, e, http.MethodGet, path, cookie)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("GET %s: status = %d, want 400", path, rec.Code)
		}
	}

	// Unauthenticated → 401.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/search?q=rocket", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated search: status = %d, want 401", rec.Code)
	}
}

func TestGlobalSearchCursorPagination(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testWorkItemServer(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("gspage"), "test-agent", uniqueIP())
	slug := uniqueSlug("gspage")
	createWorkspaceHTTP(t, e, cookie, "Page Co", slug)
	ident := uniqueProjectIdentifier("GP")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"

	for i := 0; i < 3; i++ {
		createWorkItemHTTP(t, e, cookie, base, fmt.Sprintf(`{"name":"pagination probe %d"}`, i))
	}

	var page1 struct {
		Results []struct {
			ID string `json:"id"`
		} `json:"results"`
		NextCursor string `json:"next_cursor"`
	}
	rec := getAuthed(t, e, http.MethodGet, "/api/v1/search?q=pagination&per_page=2", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("page 1: status = %d, want 200", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page1); err != nil {
		t.Fatalf("decode page 1: %v", err)
	}
	if len(page1.Results) != 2 || page1.NextCursor == "" {
		t.Fatalf("page 1: got %d results cursor=%q, want 2 + cursor", len(page1.Results), page1.NextCursor)
	}

	var page2 struct {
		Results []struct {
			ID string `json:"id"`
		} `json:"results"`
		NextCursor string `json:"next_cursor"`
	}
	rec = getAuthed(t, e, http.MethodGet, "/api/v1/search?q=pagination&per_page=2&cursor="+page1.NextCursor, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("page 2: status = %d, want 200", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page2); err != nil {
		t.Fatalf("decode page 2: %v", err)
	}
	if len(page2.Results) != 1 || page2.NextCursor != "" {
		t.Fatalf("page 2: got %d results cursor=%q, want 1 + no cursor", len(page2.Results), page2.NextCursor)
	}
	seen := map[string]bool{}
	for _, r := range page1.Results {
		seen[r.ID] = true
	}
	for _, r := range page2.Results {
		if seen[r.ID] {
			t.Fatalf("duplicate issue %s across pages", r.ID)
		}
	}

	// Garbage cursor → 400.
	rec = getAuthed(t, e, http.MethodGet, "/api/v1/search?q=pagination&cursor=garbage", cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("garbage cursor: status = %d, want 400", rec.Code)
	}
}
