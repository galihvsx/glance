package api

// Activity feed HTTP tests (C5T7): the /activity endpoint returns the
// seeded audit rows as the newest-first feed, guests get 403, and bad
// limits get 400. Real test database, no skips.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
)

func setupActivityHTTP(t *testing.T) (*echo.Echo, *http.Cookie, string) {
	t.Helper()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterActivityRoutes(e, &IssueHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("h-activity"), "test-agent/1.0", uniqueIP())
	slug := uniqueSlug("activity-http")
	createWorkspaceHTTP(t, e, cookie, "Activity Co", slug)
	ident := uniqueProjectIdentifier("ACT")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident
	return e, cookie, base
}

type activityRow struct {
	At              string  `json:"at"`
	Actor           string  `json:"actor"`
	IssueUUID       string  `json:"issue_uuid"`
	IssueIdentifier string  `json:"issue_identifier"`
	Field           string  `json:"field"`
	Old             *string `json:"old"`
	New             *string `json:"new"`
}

func TestActivityHTTPFeed(t *testing.T) {
	e, cookie, base := setupActivityHTTP(t)

	issueID := createIssueHTTP(t, e, cookie, base+"/issues", "Feed issue")
	rec := postAuthedJSON(t, e, http.MethodPatch, base+"/issues/"+issueID, cookie, `{"priority":3}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch priority: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	rec = getAuthed(t, e, http.MethodGet, base+"/activity", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("activity: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Activity []activityRow `json:"activity"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode activity: %v (body: %s)", err, rec.Body.String())
	}
	if len(body.Activity) != 2 {
		t.Fatalf("activity rows = %d, want 2 (_created + priority)", len(body.Activity))
	}
	// Newest first: the priority change, then the creation.
	first := body.Activity[0]
	if first.Field != "priority" || first.IssueUUID != issueID {
		t.Fatalf("first row = %+v, want the priority change", first)
	}
	if first.Old == nil || *first.Old != "0" || first.New == nil || *first.New != "3" {
		t.Fatalf("priority old/new wrong: %+v", first)
	}
	if !strings.HasSuffix(first.IssueIdentifier, "-1") {
		t.Fatalf("issue_identifier = %q, want suffix -1", first.IssueIdentifier)
	}
	if first.At == "" || first.Actor == "" {
		t.Fatalf("row missing at/actor: %+v", first)
	}
	second := body.Activity[1]
	if second.Field != "_created" || second.Old != nil {
		t.Fatalf("second row = %+v, want _created with NULL old", second)
	}
}

func TestActivityHTTPLimit(t *testing.T) {
	e, cookie, base := setupActivityHTTP(t)
	createIssueHTTP(t, e, cookie, base+"/issues", "Limit issue")

	rec := getAuthed(t, e, http.MethodGet, base+"/activity?limit=1", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("limit=1: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Activity []activityRow `json:"activity"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Activity) != 1 {
		t.Fatalf("limit=1 → %d rows, want 1", len(body.Activity))
	}

	for _, bad := range []string{"0", "201", "abc", "-5"} {
		rec := getAuthed(t, e, http.MethodGet, base+"/activity?limit="+bad, cookie)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("limit=%s: status = %d, want 400 (body: %s)", bad, rec.Code, rec.Body.String())
		}
	}
}

func TestActivityHTTPGuestForbidden(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterActivityRoutes(e, &IssueHandler{Pool: pool})

	adminCookie := loginTestUser(t, e, pool, uniqueEmail("h-act-admin"), "test-agent/1.0", uniqueIP())
	guestCookie := loginTestUser(t, e, pool, uniqueEmail("h-act-guest"), "test-agent/1.0", uniqueIP())
	slug := uniqueSlug("act-guest")
	createWorkspaceHTTP(t, e, adminCookie, "Act Corp", slug)
	ident := uniqueProjectIdentifier("ACG")
	createProjectHTTP(t, e, adminCookie, slug, "Engineering", ident)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident

	// Admin adds the guest (role 5).
	guestID := authedUserID(t, e, guestCookie)
	rec := postAuthedJSON(t, e, http.MethodPost, "/api/v1/workspaces/"+slug+"/members", adminCookie,
		fmt.Sprintf(`{"user_id":%q,"role":5}`, guestID))
	if rec.Code != http.StatusOK {
		t.Fatalf("add guest: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	rec = getAuthed(t, e, http.MethodGet, base+"/activity", guestCookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest activity: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	// The admin still reads fine.
	rec = getAuthed(t, e, http.MethodGet, base+"/activity", adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin activity: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
}

// Workspace activity feed HTTP tests (C17T4): the /activity endpoint on
// the workspace path aggregates every project's audit rows, paginates
// with the opaque before cursor, filters by types, and keeps the
// tenancy boundary (guest 403, bad slug 404, bad cursor 400). Real test
// database, no skips.

type workspaceActivityRow struct {
	At                string  `json:"at"`
	Actor             string  `json:"actor"`
	IssueUUID         string  `json:"issue_uuid"`
	IssueIdentifier   string  `json:"issue_identifier"`
	Field             string  `json:"field"`
	Old               *string `json:"old"`
	New               *string `json:"new"`
	ProjectIdentifier string  `json:"project_identifier"`
	ProjectName       string  `json:"project_name"`
}

type workspaceActivityBody struct {
	Activity []workspaceActivityRow `json:"activity"`
	Next     *string                `json:"next_cursor"`
}

// setupWorkspaceActivityHTTP seeds two projects in one workspace, each
// with one issue (the second issue gets a priority change so the feed
// has rows in both projects), and returns the workspace-scoped base.
func setupWorkspaceActivityHTTP(t *testing.T) (*echo.Echo, *http.Cookie, string) {
	t.Helper()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterActivityRoutes(e, &IssueHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("h-wsact"), "test-agent/1.0", uniqueIP())
	slug := uniqueSlug("wsact-http")
	createWorkspaceHTTP(t, e, cookie, "WS Act Co", slug)
	wsBase := "/api/v1/workspaces/" + slug

	ident1 := uniqueProjectIdentifier("WA1")
	createProjectHTTP(t, e, cookie, slug, "Eng", ident1)
	issue1 := createIssueHTTP(t, e, cookie, wsBase+"/projects/"+ident1+"/issues", "First issue")
	ident2 := uniqueProjectIdentifier("WA2")
	createProjectHTTP(t, e, cookie, slug, "Design", ident2)
	issue2 := createIssueHTTP(t, e, cookie, wsBase+"/projects/"+ident2+"/issues", "Second issue")

	// A priority change on the second issue gives the feed a row in the
	// second project too.
	rec := postAuthedJSON(t, e, http.MethodPatch, wsBase+"/projects/"+ident2+"/issues/"+issue2, cookie, `{"priority":3}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch priority: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	_ = issue1
	return e, cookie, wsBase
}

func decodeWorkspaceActivity(t *testing.T, rec *httptest.ResponseRecorder) workspaceActivityBody {
	t.Helper()
	var body workspaceActivityBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode workspace activity: %v (body: %s)", err, rec.Body.String())
	}
	return body
}

func TestWorkspaceActivityHTTPFeed(t *testing.T) {
	e, cookie, wsBase := setupWorkspaceActivityHTTP(t)

	rec := getAuthed(t, e, http.MethodGet, wsBase+"/activity", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("activity: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	body := decodeWorkspaceActivity(t, rec)
	// 2 issues × _created + 1 priority change = 3 rows.
	if len(body.Activity) != 3 {
		t.Fatalf("rows = %d, want 3", len(body.Activity))
	}
	if body.Next != nil {
		t.Fatalf("next_cursor = %q, want null (exhausted)", *body.Next)
	}
	// Newest first: the priority change, then the two creations.
	if body.Activity[0].Field != "priority" {
		t.Fatalf("first row field = %q, want priority", body.Activity[0].Field)
	}
	// Every row carries the owning project badge.
	seen := map[string]string{}
	for _, r := range body.Activity {
		if r.ProjectIdentifier == "" || r.ProjectName == "" {
			t.Fatalf("row missing project badge: %+v", r)
		}
		seen[r.ProjectName] = r.ProjectIdentifier
	}
	if len(seen) != 2 || seen["Eng"] == "" || seen["Design"] == "" {
		t.Fatalf("badge projects = %v, want Eng + Design", seen)
	}
}

func TestWorkspaceActivityHTTPPagination(t *testing.T) {
	e, cookie, wsBase := setupWorkspaceActivityHTTP(t)

	rec := getAuthed(t, e, http.MethodGet, wsBase+"/activity?limit=1", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("page 1: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	p1 := decodeWorkspaceActivity(t, rec)
	if len(p1.Activity) != 1 || p1.Next == nil {
		t.Fatalf("page 1: %d rows cursor %v, want 1 row + cursor", len(p1.Activity), p1.Next)
	}

	rec = getAuthed(t, e, http.MethodGet, wsBase+"/activity?limit=1&before="+url.QueryEscape(*p1.Next), cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("page 2: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	p2 := decodeWorkspaceActivity(t, rec)
	if len(p2.Activity) != 1 {
		t.Fatalf("page 2: %d rows, want 1", len(p2.Activity))
	}
	if p2.Activity[0].IssueUUID == p1.Activity[0].IssueUUID && p2.Activity[0].Field == p1.Activity[0].Field {
		t.Fatalf("page 2 repeats page 1's row")
	}

	// Type filter composes with pagination: only the priority row.
	rec = getAuthed(t, e, http.MethodGet, wsBase+"/activity?types=priority", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("types=priority: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	filtered := decodeWorkspaceActivity(t, rec)
	if len(filtered.Activity) != 1 || filtered.Activity[0].Field != "priority" {
		t.Fatalf("types=priority: %d rows, want the single priority row", len(filtered.Activity))
	}
	if filtered.Next != nil {
		t.Fatalf("types=priority: next_cursor set on a 1-row filtered feed")
	}

	// Bad cursor is 400; bad limit is 400.
	rec = getAuthed(t, e, http.MethodGet, wsBase+"/activity?before=garbage", cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad cursor: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, wsBase+"/activity?limit=0", cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("limit=0: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestWorkspaceActivityHTTPTenancy(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterActivityRoutes(e, &IssueHandler{Pool: pool})

	adminCookie := loginTestUser(t, e, pool, uniqueEmail("h-wsact-admin"), "test-agent/1.0", uniqueIP())
	guestCookie := loginTestUser(t, e, pool, uniqueEmail("h-wsact-guest"), "test-agent/1.0", uniqueIP())
	strangerCookie := loginTestUser(t, e, pool, uniqueEmail("h-wsact-stranger"), "test-agent/1.0", uniqueIP())
	slug := uniqueSlug("wsact-tenant")
	createWorkspaceHTTP(t, e, adminCookie, "Tenant Co", slug)
	ident := uniqueProjectIdentifier("WTN")
	createProjectHTTP(t, e, adminCookie, slug, "Eng", ident)
	createIssueHTTP(t, e, adminCookie, "/api/v1/workspaces/"+slug+"/projects/"+ident+"/issues", "Tenant issue")

	// Admin adds the guest (role 5).
	guestID := authedUserID(t, e, guestCookie)
	rec := postAuthedJSON(t, e, http.MethodPost, "/api/v1/workspaces/"+slug+"/members", adminCookie,
		fmt.Sprintf(`{"user_id":%q,"role":5}`, guestID))
	if rec.Code != http.StatusOK {
		t.Fatalf("add guest: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	url := "/api/v1/workspaces/" + slug + "/activity"
	rec = getAuthed(t, e, http.MethodGet, url, guestCookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, url, strangerCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("stranger: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, "/api/v1/workspaces/no-such-ws/activity", adminCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bad slug: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	// The admin still reads fine.
	rec = getAuthed(t, e, http.MethodGet, url, adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
}
