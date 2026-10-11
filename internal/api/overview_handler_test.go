package api

// Project overview HTTP tests (C8T5): GET
// /api/v1/workspaces/{slug}/projects/{identifier}/overview returns the
// single-round-trip payload (project + role + analytics summary +
// states + activity + live cycle), guests get 403, outsiders 404, and
// projects without cycles report cycle: null. Real test database, no
// skips.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
)

func setupOverviewHTTP(t *testing.T) (*echo.Echo, *http.Cookie, string) {
	t.Helper()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterCycleRoutes(e, &IssueHandler{Pool: pool})
	RegisterActivityRoutes(e, &IssueHandler{Pool: pool})
	RegisterOverviewRoutes(e, &IssueHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("h-ovw"), "test-agent/1.0", uniqueIP())
	slug := uniqueSlug("overview-http")
	createWorkspaceHTTP(t, e, cookie, "Overview Co", slug)
	ident := uniqueProjectIdentifier("OVW")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident
	return e, cookie, base
}

type overviewBody struct {
	Project struct {
		ID          string `json:"id"`
		Identifier  string `json:"identifier"`
		Name        string `json:"name"`
		Description string `json:"description"`
	} `json:"project"`
	Role    int `json:"role"`
	Summary struct {
		ByState    map[string]int64 `json:"by_state"`
		ByPriority map[string]int64 `json:"by_priority"`
		Overdue    int64            `json:"overdue_count"`
	} `json:"summary"`
	States []struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Group string `json:"group"`
	} `json:"states"`
	Activity []activityRow `json:"activity"`
	Cycle    *struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Status    string `json:"status"`
		Completed int64  `json:"completed"`
		Total     int64  `json:"total"`
	} `json:"cycle"`
	OpenByPriority map[string]int64 `json:"open_by_priority"`
}

func getOverview(t *testing.T, e *echo.Echo, base string, cookie *http.Cookie) overviewBody {
	t.Helper()
	rec := getAuthed(t, e, http.MethodGet, base+"/overview", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("overview: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var body overviewBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode overview: %v", err)
	}
	return body
}

// stateIDByName fetches the project's states and returns the id of the
// named state.
func stateIDByName(t *testing.T, e *echo.Echo, base string, cookie *http.Cookie, name string) string {
	t.Helper()
	rec := getAuthed(t, e, http.MethodGet, base+"/states", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("states: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var body struct {
		States []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"states"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode states: %v", err)
	}
	for _, s := range body.States {
		if s.Name == name {
			return s.ID
		}
	}
	t.Fatalf("state %q not found", name)
	return ""
}

func TestOverviewHTTPFullShape(t *testing.T) {
	e, cookie, base := setupOverviewHTTP(t)

	// Set the markdown description via the existing PATCH endpoint.
	rec := postAuthedJSON(t, e, http.MethodPatch, base, cookie,
		`{"description":"# Ship it\n\n**Bold** plans for Q4."}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch description: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	issueA := createIssueHTTP(t, e, cookie, base+"/issues", "Overview issue A")
	issueB := createIssueHTTP(t, e, cookie, base+"/issues", "Overview issue B")
	// Mark A urgent so by_priority carries a known bucket.
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/issues/"+issueA, cookie, `{"priority":4}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch priority: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	// Live cycle: both issues in, B completed.
	today := time.Now().UTC().Format("2006-01-02")
	later := time.Now().UTC().AddDate(0, 0, 14).Format("2006-01-02")
	rec = postAuthedJSON(t, e, http.MethodPost, base+"/cycles", cookie,
		fmt.Sprintf(`{"name":"Sprint 1","start_date":%q,"end_date":%q}`, today, later))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create cycle: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode cycle: %v", err)
	}
	rec = postAuthedJSON(t, e, http.MethodPost, base+"/cycles/"+created.ID+"/issues", cookie,
		fmt.Sprintf(`{"issue_ids":[%q,%q]}`, issueA, issueB))
	if rec.Code != http.StatusOK {
		t.Fatalf("add cycle issues: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	doneID := stateIDByName(t, e, base, cookie, "Done")
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/issues/"+issueB, cookie,
		fmt.Sprintf(`{"state_id":%q}`, doneID))
	if rec.Code != http.StatusOK {
		t.Fatalf("complete issue: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	body := getOverview(t, e, base, cookie)

	if body.Project.Name != "Engineering" {
		t.Errorf("project.name = %q, want Engineering", body.Project.Name)
	}
	if body.Project.Description != "# Ship it\n\n**Bold** plans for Q4." {
		t.Errorf("project.description = %q, want the markdown set above", body.Project.Description)
	}
	if body.Role != 20 {
		t.Errorf("role = %d, want 20 (workspace creator is an admin)", body.Role)
	}
	if body.Summary.ByPriority["4"] != 1 {
		t.Errorf("by_priority[4] = %d, want 1", body.Summary.ByPriority["4"])
	}
	// A is urgent and still open; B was completed, so the open-only
	// split carries exactly one urgent issue.
	if body.OpenByPriority["4"] != 1 {
		t.Errorf("open_by_priority[4] = %d, want 1", body.OpenByPriority["4"])
	}
	var openTotal int64
	for _, n := range body.OpenByPriority {
		openTotal += n
	}
	if openTotal != 1 {
		t.Errorf("open_by_priority total = %d, want 1 (A only)", openTotal)
	}
	if len(body.States) != 5 {
		t.Errorf("len(states) = %d, want 5 default states", len(body.States))
	}
	seenCompleted := false
	for _, s := range body.States {
		if s.Group == "completed" {
			seenCompleted = true
		}
	}
	if !seenCompleted {
		t.Error("states carry no completed group: completion % cannot be derived")
	}
	if len(body.Activity) == 0 {
		t.Fatal("activity is empty, want the audit rows from the issue edits above")
	}
	for i := 1; i < len(body.Activity); i++ {
		if body.Activity[i-1].At < body.Activity[i].At {
			t.Fatalf("activity not newest-first at index %d", i)
		}
	}
	if body.Cycle == nil {
		t.Fatal("cycle is null, want the live cycle")
	}
	if body.Cycle.Name != "Sprint 1" {
		t.Errorf("cycle.name = %q, want Sprint 1", body.Cycle.Name)
	}
	if body.Cycle.Completed != 1 || body.Cycle.Total != 2 {
		t.Errorf("cycle completed/total = %d/%d, want 1/2", body.Cycle.Completed, body.Cycle.Total)
	}
}

func TestOverviewHTTPNoCycles(t *testing.T) {
	e, cookie, base := setupOverviewHTTP(t)
	body := getOverview(t, e, base, cookie)
	if body.Cycle != nil {
		t.Errorf("cycle = %+v, want null for a project without cycles", body.Cycle)
	}
	if body.Project.Description != "" {
		t.Errorf("description = %q, want empty default", body.Project.Description)
	}
}

func TestOverviewHTTPGuestForbidden(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterOverviewRoutes(e, &IssueHandler{Pool: pool})

	adminCookie := loginTestUser(t, e, pool, uniqueEmail("h-ovw-admin"), "test-agent/1.0", uniqueIP())
	guestCookie := loginTestUser(t, e, pool, uniqueEmail("h-ovw-guest"), "test-agent/1.0", uniqueIP())
	slug := uniqueSlug("ovw-guest")
	createWorkspaceHTTP(t, e, adminCookie, "Guest Corp", slug)
	ident := uniqueProjectIdentifier("OVG")
	createProjectHTTP(t, e, adminCookie, slug, "Engineering", ident)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident

	// Admin adds the guest (role 5).
	guestID := authedUserID(t, e, guestCookie)
	rec := postAuthedJSON(t, e, http.MethodPost, "/api/v1/workspaces/"+slug+"/members", adminCookie,
		fmt.Sprintf(`{"user_id":%q,"role":5}`, guestID))
	if rec.Code != http.StatusOK {
		t.Fatalf("add guest: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	rec = getAuthed(t, e, http.MethodGet, base+"/overview", guestCookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest overview: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, base+"/overview", adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin overview: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestOverviewHTTPNotFound(t *testing.T) {
	e, cookie, base := setupOverviewHTTP(t)
	// Malformed identifier (12-char ident + "XXX" = 15 chars > 12):
	// the service normalizes identifiers like every other entry point,
	// so a malformed identifier is a 400, not a 404.
	rec := getAuthed(t, e, http.MethodGet, base+"XXX/overview", cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed identifier overview: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	// Well-formed but nonexistent identifier: 404.
	slug := base[len("/api/v1/workspaces/"):]
	slug = slug[:strings.Index(slug, "/")]
	rec = getAuthed(t, e, http.MethodGet,
		"/api/v1/workspaces/"+slug+"/projects/ZZZ999999999/overview", cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown identifier overview: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
}
