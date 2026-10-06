package api

// Intake HTTP endpoint tests (Task 20): route wiring for the inbox and
// the four triage actions — create with intake:true → 201, GET /intake
// lists it pending, accept → 200 accepted, snooze with a past date →
// 400 envelope, duplicate without target → 400. Real test database,
// no skips.

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
)

func testIntakeServer(t *testing.T, pool *pgxpool.Pool) *echo.Echo {
	t.Helper()
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterIntakeRoutes(e, &IssueHandler{Pool: pool})
	return e
}

func TestIntakeHTTPTriage(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testIntakeServer(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("intake-http"), "test-agent", uniqueIP())
	slug := uniqueSlug("intake-http")
	createWorkspaceHTTP(t, e, cookie, "Intake Co", slug)
	ident := uniqueProjectIdentifier("IN")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	issuesBase := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"
	intakeBase := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/intake"

	// Create with intake:true → 201.
	rec := postAuthedJSON(t, e, http.MethodPost, issuesBase, cookie, `{"name":"Triage me","intake":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create intake issue: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}

	// Inbox lists it as pending.
	rec = getAuthed(t, e, http.MethodGet, intakeBase, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get intake: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var inbox struct {
		Intake struct {
			IsDefault bool `json:"is_default"`
		} `json:"intake"`
		Items []struct {
			IssueID    string `json:"issue_id"`
			Status     int    `json:"status"`
			StatusName string `json:"status_name"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &inbox); err != nil {
		t.Fatalf("decode inbox: %v", err)
	}
	if !inbox.Intake.IsDefault {
		t.Errorf("intake.is_default = false, want true")
	}
	if len(inbox.Items) != 1 || inbox.Items[0].IssueID != created.ID || inbox.Items[0].StatusName != "pending" {
		t.Fatalf("inbox items = %+v, want the one pending issue", inbox.Items)
	}

	// Accept → 200, accepted.
	rec = postAuthedJSON(t, e, http.MethodPost, intakeBase+"/issues/"+created.ID+"/accept", cookie, `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("accept: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var accepted struct {
		StatusName string `json:"status_name"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &accepted); err != nil {
		t.Fatalf("decode accepted: %v", err)
	}
	if accepted.StatusName != "accepted" {
		t.Errorf("status_name = %q, want accepted", accepted.StatusName)
	}

	// Inbox is empty after triage.
	rec = getAuthed(t, e, http.MethodGet, intakeBase, cookie)
	var after struct {
		Items []any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &after); err != nil {
		t.Fatalf("decode inbox after: %v", err)
	}
	if len(after.Items) != 0 {
		t.Errorf("inbox items after accept = %d, want 0", len(after.Items))
	}
}

func TestIntakeHTTPSnoozeValidation(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testIntakeServer(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("intake-snooze"), "test-agent", uniqueIP())
	slug := uniqueSlug("intake-snooze")
	createWorkspaceHTTP(t, e, cookie, "Snooze Co", slug)
	ident := uniqueProjectIdentifier("SN")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	issuesBase := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"
	intakeBase := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/intake"

	rec := postAuthedJSON(t, e, http.MethodPost, issuesBase, cookie, `{"name":"Later","intake":true}`)
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}

	// Past snoozed_till → 400 with the spec §5 envelope.
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	rec = postAuthedJSON(t, e, http.MethodPost, intakeBase+"/issues/"+created.ID+"/snooze", cookie,
		`{"snoozed_till":"`+past+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("snooze past: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Error.Code != "bad_request" {
		t.Errorf("error.code = %q, want bad_request", env.Error.Code)
	}

	// Missing snoozed_till → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, intakeBase+"/issues/"+created.ID+"/snooze", cookie, `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("snooze missing date: status = %d, want 400", rec.Code)
	}

	// Future date → 200 snoozed.
	future := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	rec = postAuthedJSON(t, e, http.MethodPost, intakeBase+"/issues/"+created.ID+"/snooze", cookie,
		`{"snoozed_till":"`+future+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("snooze future: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	// Duplicate without target → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, intakeBase+"/issues/"+created.ID+"/duplicate", cookie, `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("duplicate missing target: status = %d, want 400", rec.Code)
	}
}
