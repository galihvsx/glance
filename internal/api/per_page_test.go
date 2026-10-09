package api

// C2T8 bug bundle B: per_page is REJECTED (400) when out of range on
// every list endpoint — never silently clamped. The documented contract
// is "want 1-100"; fail-fast surfaces client bugs instead of returning
// fewer rows than asked for.

import (
	"net/http"
	"testing"

	"github.com/labstack/echo/v5"

	"glance/internal/config"
)

// setupPerPageProject builds a server with issue + intake routes and
// returns the session cookie, the issues base path, and the intake path.
func setupPerPageProject(t *testing.T, prefix string) (*echo.Echo, *http.Cookie, string, string) {
	t.Helper()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := echo.New()
	RegisterAuthRoutes(e, &AuthHandler{Pool: pool, Config: &config.Config{OTPPepper: "test-pepper-do-not-use-in-prod"}})
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterIntakeRoutes(e, &IssueHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail(prefix), "test-agent", uniqueIP())
	slug := uniqueSlug(prefix)
	createWorkspaceHTTP(t, e, cookie, "PerPage Co", slug)
	ident := uniqueProjectIdentifier("PP")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	project := "/api/v1/workspaces/" + slug + "/projects/" + ident
	return e, cookie, project + "/issues", project + "/intake"
}

func TestPerPageOutOfRangeRejected(t *testing.T) {
	e, cookie, base, intake := setupPerPageProject(t, "perpage")

	for _, path := range []string{
		base + "?per_page=0",
		base + "?per_page=101",
		base + "?per_page=1000000",
		base + "?per_page=abc",
		intake + "?per_page=0",
		intake + "?per_page=101",
	} {
		rec := getAuthed(t, e, http.MethodGet, path, cookie)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s: status = %d, want 400", path, rec.Code)
		}
	}

	// Boundary values still work.
	for _, path := range []string{
		base + "?per_page=1",
		base + "?per_page=100",
		intake + "?per_page=50",
	} {
		rec := getAuthed(t, e, http.MethodGet, path, cookie)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: status = %d, want 200 (body: %s)", path, rec.Code, rec.Body.String())
		}
	}
}

// TestSearchPerPageOutOfRangeRejected pins the same reject rule on the
// global search endpoint (C2T8 bug #6 consistency).
func TestSearchPerPageOutOfRangeRejected(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := echo.New()
	RegisterAuthRoutes(e, &AuthHandler{Pool: pool, Config: &config.Config{OTPPepper: "test-pepper-do-not-use-in-prod"}})
	RegisterWorkItemRoutes(e, &IssueHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("ppsearch"), "test-agent", uniqueIP())

	for _, perPage := range []string{"0", "101", "1000000", "abc"} {
		rec := getAuthed(t, e, http.MethodGet, "/api/v1/search?q=x&per_page="+perPage, cookie)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET /api/v1/search?per_page=%s: status = %d, want 400", perPage, rec.Code)
		}
	}
	rec := getAuthed(t, e, http.MethodGet, "/api/v1/search?q=x&per_page=100", cookie)
	if rec.Code != http.StatusOK {
		t.Errorf("GET /api/v1/search?per_page=100: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
}
