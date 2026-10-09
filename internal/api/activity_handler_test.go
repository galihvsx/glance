package api

// Activity feed HTTP tests (C5T7): the /activity endpoint returns the
// seeded audit rows as the newest-first feed, guests get 403, and bad
// limits get 400. Real test database, no skips.

import (
	"encoding/json"
	"fmt"
	"net/http"
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
