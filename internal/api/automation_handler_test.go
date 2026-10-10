package api

// Automation HTTP endpoint tests (C11T1): rule CRUD through the
// project-nested routes (200 list / 201 create / 200 patch / 204
// delete), 400 on bad trigger/action, 404 on unknown rule, 403 for
// guests on read and write, and the spec §5 error envelope. Real test
// database, no skips.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// startedStateIDHTTP returns a started-group state ID for the project
// via the real states list route (C12T2 helper).
func startedStateIDHTTP(t *testing.T, e *echo.Echo, cookie *http.Cookie, slug, ident string) string {
	t.Helper()
	rec := getAuthed(t, e, http.MethodGet,
		"/api/v1/workspaces/"+slug+"/projects/"+ident+"/states", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list states: status = %d, want 200", rec.Code)
	}
	var listed struct {
		States []struct {
			ID    string `json:"id"`
			Group string `json:"group"`
		} `json:"states"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode states: %v", err)
	}
	for _, s := range listed.States {
		if s.Group == "started" {
			return s.ID
		}
	}
	t.Fatal("no started-group state")
	return ""
}

func TestAutomationHTTP(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterAutomationRoutes(e, &AutomationHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("auto-http"), "test-agent", uniqueIP())
	slug := uniqueSlug("auto-http")
	createWorkspaceHTTP(t, e, cookie, "Auto Co", slug)
	ident := uniqueProjectIdentifier("AH")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	autoBase := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/automations"

	// List → empty wrapped shape.
	rec := getAuthed(t, e, http.MethodGet, autoBase, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var listed struct {
		Automations []struct {
			ID string `json:"id"`
		} `json:"automations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Automations) != 0 {
		t.Fatalf("automations = %d, want 0", len(listed.Automations))
	}

	// Create → 201 with the rule.
	rec = postAuthedJSON(t, e, http.MethodPost, autoBase, cookie,
		`{"name":"on start","trigger":{"type":"issue.state_changed","to_states":null,"from_states":null},"actions":[{"type":"add_comment","body":"hello"}]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if created.Name != "on start" || !created.Enabled || created.ID == "" {
		t.Fatalf("created = %+v, want on start/enabled/id", created)
	}

	// Bad trigger type → 400 envelope.
	rec = postAuthedJSON(t, e, http.MethodPost, autoBase, cookie,
		`{"name":"bad","trigger":{"type":"issue.deleted"},"actions":[{"type":"add_comment","body":"x"}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad trigger: status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"code":"bad_request"`) {
		t.Fatalf("400 envelope: body=%s", rec.Body.String())
	}

	// C12T2: issue.created trigger + set_priority/set_state actions
	// round-trip through the API.
	rec = postAuthedJSON(t, e, http.MethodPost, autoBase, cookie,
		`{"name":"on create","trigger":{"type":"issue.created"},"actions":[{"type":"set_priority","priority":3},{"type":"set_state","state_id":"`+startedStateIDHTTP(t, e, cookie, slug, ident)+`"}]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("created trigger: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}

	// C12T2: out-of-range priority and unknown state → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, autoBase, cookie,
		`{"name":"bad","trigger":{"type":"issue.created"},"actions":[{"type":"set_priority","priority":9}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad priority: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPost, autoBase, cookie,
		`{"name":"bad","trigger":{"type":"issue.created"},"actions":[{"type":"set_state","state_id":"00000000-0000-0000-0000-000000000000"}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad state: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// Unknown action type → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, autoBase, cookie,
		`{"name":"bad","trigger":{"type":"issue.state_changed"},"actions":[{"type":"teleport"}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad action: status = %d, want 400", rec.Code)
	}

	// PATCH disable → 200.
	rec = postAuthedJSON(t, e, http.MethodPatch, autoBase+"/"+created.ID, cookie, `{"enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var patched struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &patched); err != nil {
		t.Fatalf("decode patched: %v", err)
	}
	if patched.Enabled {
		t.Fatalf("patched enabled = true, want false")
	}

	// PATCH unknown rule → 404 envelope.
	rec = postAuthedJSON(t, e, http.MethodPatch, autoBase+"/00000000-0000-0000-0000-000000000000", cookie, `{"enabled":true}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("patch missing: status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"code":"not_found"`) {
		t.Fatalf("404 envelope: body=%s", rec.Body.String())
	}

	// Malformed rule id → 400 (never reaches a ::uuid cast).
	rec = postAuthedJSON(t, e, http.MethodPatch, autoBase+"/nope", cookie, `{"enabled":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("patch malformed id: status = %d, want 400", rec.Code)
	}

	// DELETE → 204; second delete → 404.
	rec = postAuthedJSON(t, e, http.MethodDelete, autoBase+"/"+created.ID, cookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d, want 204", rec.Code)
	}
	rec = postAuthedJSON(t, e, http.MethodDelete, autoBase+"/"+created.ID, cookie, ``)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete missing: status = %d, want 404", rec.Code)
	}
}

func TestAutomationHTTPGuestForbidden(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterAutomationRoutes(e, &AutomationHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("auto-gf"), "test-agent", uniqueIP())
	slug := uniqueSlug("auto-gf")
	createWorkspaceHTTP(t, e, cookie, "Auto Co", slug)
	ident := uniqueProjectIdentifier("AG")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	autoBase := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/automations"

	// Second user joins as guest (role 5).
	guestEmail := uniqueEmail("auto-gf-guest")
	guestCookie := loginTestUser(t, e, pool, guestEmail, "test-agent", uniqueIP())
	var adminID, guestID string
	ctx := context.Background()
	if err := pool.QueryRow(ctx,
		`SELECT m.user_id::text FROM workspace_members m
		 JOIN workspaces w ON w.id = m.workspace_id
		 WHERE w.slug = $1 AND m.role = 20`, slug).Scan(&adminID); err != nil {
		t.Fatalf("admin id: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM users WHERE email = $1`, guestEmail).Scan(&guestID); err != nil {
		t.Fatalf("guest id: %v", err)
	}
	if err := service.UpsertMember(ctx, pool, slug, adminID, guestID, service.RoleGuest); err != nil {
		t.Fatalf("UpsertMember guest: %v", err)
	}

	// Guest read → 403 (acceptance: member-scoped read).
	rec := getAuthed(t, e, http.MethodGet, autoBase, guestCookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest list: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	// Guest write → 403 (acceptance: non-maintainer write 403).
	rec = postAuthedJSON(t, e, http.MethodPost, autoBase, guestCookie,
		`{"name":"x","trigger":{"type":"issue.state_changed"},"actions":[{"type":"add_comment","body":"x"}]}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest create: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPatch, autoBase+"/00000000-0000-0000-0000-000000000000", guestCookie, `{"enabled":false}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest patch: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodDelete, autoBase+"/00000000-0000-0000-0000-000000000000", guestCookie, ``)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest delete: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
}

// Automation runs HTTP tests (C12T1): GET .../automations/runs through
// the real route — 200 with the wrapped shape, rule_id/issue_id
// filters, limit validation/clamping, 403 for guests, 404 for
// non-members, and the spec §5 error envelope.
func TestAutomationRunsHTTP(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterAutomationRoutes(e, &AutomationHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})

	ctx := context.Background()
	cookie := loginTestUser(t, e, pool, uniqueEmail("auto-runs"), "test-agent", uniqueIP())
	slug := uniqueSlug("auto-runs")
	createWorkspaceHTTP(t, e, cookie, "Auto Co", slug)
	ident := uniqueProjectIdentifier("AR")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	autoBase := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/automations"
	issueBase := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"

	var adminID string
	if err := pool.QueryRow(ctx,
		`SELECT m.user_id::text FROM workspace_members m
		 JOIN workspaces w ON w.id = m.workspace_id
		 WHERE w.slug = $1 AND m.role = 20`, slug).Scan(&adminID); err != nil {
		t.Fatalf("admin id: %v", err)
	}
	states, err := service.ListStates(ctx, pool, slug, ident, adminID)
	if err != nil {
		t.Fatalf("ListStates: %v", err)
	}
	var startedID string
	for _, s := range states {
		if s.Group == "started" {
			startedID = s.ID
		}
	}
	if startedID == "" {
		t.Fatal("no started state seeded")
	}

	// A rule that fires on any state change.
	rec := postAuthedJSON(t, e, http.MethodPost, autoBase, cookie,
		`{"name":"run log","trigger":{"type":"issue.state_changed","from_states":null,"to_states":null},"actions":[{"type":"add_comment","body":"fired"}]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create rule: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode rule: %v", err)
	}

	issueID := createIssueHTTP(t, e, cookie, issueBase, "runs e2e")
	rec = postAuthedJSON(t, e, http.MethodPatch, issueBase+"/"+issueID, cookie,
		`{"state_id":"`+startedID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("move issue: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	var runsResp struct {
		Runs []struct {
			ID             string  `json:"id"`
			RuleID         string  `json:"rule_id"`
			RuleName       string  `json:"rule_name"`
			IssueID        string  `json:"issue_id"`
			IssueDisplayID *string `json:"issue_display_id"`
			TriggerType    string  `json:"trigger_type"`
			FiredAt        string  `json:"fired_at"`
			Actions        []struct {
				Type  string  `json:"type"`
				OK    bool    `json:"ok"`
				Error *string `json:"error"`
			} `json:"actions"`
		} `json:"runs"`
	}
	get := func(path string, ck *http.Cookie) *httptest.ResponseRecorder {
		return getAuthed(t, e, http.MethodGet, path, ck)
	}
	decode := func(rec *httptest.ResponseRecorder) {
		t.Helper()
		runsResp.Runs = nil
		if err := json.Unmarshal(rec.Body.Bytes(), &runsResp); err != nil {
			t.Fatalf("decode runs: %v", err)
		}
	}

	// List → 200, exactly one run with the recorded outcome.
	rec = get(autoBase+"/runs", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("runs list: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	decode(rec)
	if len(runsResp.Runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runsResp.Runs))
	}
	run := runsResp.Runs[0]
	if run.RuleID != created.ID || run.RuleName != "run log" {
		t.Fatalf("run rule = %s/%q, want %s/\"run log\"", run.RuleID, run.RuleName, created.ID)
	}
	if run.IssueID != issueID || run.IssueDisplayID == nil || !strings.HasPrefix(*run.IssueDisplayID, ident+"-") {
		t.Fatalf("run issue = %s/%v, want %s/%s-N", run.IssueID, run.IssueDisplayID, issueID, ident)
	}
	if run.TriggerType != "issue.state_changed" || run.FiredAt == "" {
		t.Fatalf("run trigger/fired_at = %q/%q", run.TriggerType, run.FiredAt)
	}
	if len(run.Actions) != 1 || run.Actions[0].Type != "add_comment" || !run.Actions[0].OK {
		t.Fatalf("run actions = %+v, want one ok add_comment", run.Actions)
	}

	// Filters: matching rule_id / issue_id; non-matching → empty.
	rec = get(autoBase+"/runs?rule_id="+created.ID, cookie)
	decode(rec)
	if len(runsResp.Runs) != 1 {
		t.Fatalf("rule_id filter = %d, want 1", len(runsResp.Runs))
	}
	rec = get(autoBase+"/runs?rule_id=00000000-0000-0000-0000-000000000000", cookie)
	decode(rec)
	if len(runsResp.Runs) != 0 {
		t.Fatalf("rule_id no-match = %d, want 0", len(runsResp.Runs))
	}
	rec = get(autoBase+"/runs?issue_id="+issueID, cookie)
	decode(rec)
	if len(runsResp.Runs) != 1 {
		t.Fatalf("issue_id filter = %d, want 1", len(runsResp.Runs))
	}

	// Limit: 1 works; above max is clamped (200, not rejected);
	// 0 means "default" (same as absent); non-integer and negative
	// are 400 with the envelope.
	rec = get(autoBase+"/runs?limit=1", cookie)
	decode(rec)
	if len(runsResp.Runs) != 1 {
		t.Fatalf("limit=1 = %d, want 1", len(runsResp.Runs))
	}
	rec = get(autoBase+"/runs?limit=500", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("limit=500: status = %d, want 200 (clamped)", rec.Code)
	}
	rec = get(autoBase+"/runs?limit=0", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("limit=0: status = %d, want 200 (default)", rec.Code)
	}
	for _, bad := range []string{"limit=abc", "limit=-3"} {
		rec = get(autoBase+"/runs?"+bad, cookie)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400", bad, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `"code":"bad_request"`) {
			t.Fatalf("%s envelope: body=%s", bad, rec.Body.String())
		}
	}
	// Malformed filter UUIDs never reach a ::uuid cast: 400.
	for _, bad := range []string{"rule_id=nope", "issue_id=nope"} {
		rec = get(autoBase+"/runs?"+bad, cookie)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400", bad, rec.Code)
		}
	}

	// Guest read → 403.
	guestEmail := uniqueEmail("auto-runs-guest")
	guestCookie := loginTestUser(t, e, pool, guestEmail, "test-agent", uniqueIP())
	var guestID string
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM users WHERE email = $1`, guestEmail).Scan(&guestID); err != nil {
		t.Fatalf("guest id: %v", err)
	}
	if err := service.UpsertMember(ctx, pool, slug, adminID, guestID, service.RoleGuest); err != nil {
		t.Fatalf("UpsertMember guest: %v", err)
	}
	rec = get(autoBase+"/runs", guestCookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest runs: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}

	// Non-member (authenticated, no workspace membership) → 404.
	outCookie := loginTestUser(t, e, pool, uniqueEmail("auto-runs-out"), "test-agent", uniqueIP())
	rec = get(autoBase+"/runs", outCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("outsider runs: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"not_found"`) {
		t.Fatalf("404 envelope: body=%s", rec.Body.String())
	}
}
