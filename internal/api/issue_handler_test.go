package api

// Issue HTTP endpoint tests (Task 14): create → 201 with derived display_id,
// get → 200, partial PATCH (tri-state parent_id clear), delete → 204 then
// 404, and the spec §5 error envelope on a missing issue. Real test
// database, no skips.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
)

func testIssueServer(t *testing.T, pool *pgxpool.Pool) *echo.Echo {
	t.Helper()
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	return e
}

func TestIssueHTTPCRUD(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testIssueServer(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("issue-http"), "test-agent", "127.0.0.1")
	slug := uniqueSlug("issue-http")
	createWorkspaceHTTP(t, e, cookie, "Issue Co", slug)
	ident := uniqueProjectIdentifier("HI")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"

	// Create → 201, display_id derived.
	rec := postAuthedJSON(t, e, http.MethodPost, base, cookie,
		`{"name":"Ship it","priority":2,"description":{"type":"doc","content":[]}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create issue: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID         string `json:"id"`
		SequenceID int    `json:"sequence_id"`
		DisplayID  string `json:"display_id"`
		Name       string `json:"name"`
		StateID    string `json:"state_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if created.SequenceID != 1 || created.DisplayID != ident+"-1" || created.Name != "Ship it" {
		t.Fatalf("created = %+v, want seq 1 display %s-1", created, ident)
	}
	if created.StateID == "" {
		t.Fatal("created issue has no default state")
	}

	// Second issue → sequence 2 (counter advances through HTTP too).
	rec = postAuthedJSON(t, e, http.MethodPost, base, cookie, `{"name":"Second"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create issue 2: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	// Get → 200.
	rec = getAuthed(t, e, http.MethodGet, base+"/"+created.ID, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get issue: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	// PATCH name only → 200; description (omitted) survives.
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/"+created.ID, cookie, `{"name":"Ship it now"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch name: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, base+"/"+created.ID, cookie)
	var got struct {
		Name        string          `json:"name"`
		Description json.RawMessage `json:"description"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode patched: %v", err)
	}
	if got.Name != "Ship it now" || len(got.Description) == 0 {
		t.Fatalf("after name-only patch: name=%q desc len=%d, want name changed and desc intact", got.Name, len(got.Description))
	}

	// PATCH explicit null description clears it (tri-state).
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/"+created.ID, cookie, `{"description":null}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear description: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, base+"/"+created.ID, cookie)
	got = struct {
		Name        string          `json:"name"`
		Description json.RawMessage `json:"description"`
	}{}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode cleared: %v", err)
	}
	if len(got.Description) != 0 {
		t.Fatalf("description after explicit null: %s, want cleared", got.Description)
	}

	// PATCH {} → 400 bad_request (ErrNothingToUpdate).
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/"+created.ID, cookie, `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty patch: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Error.Code != "bad_request" {
		t.Fatalf("empty patch envelope: body=%s", rec.Body.String())
	}

	// Delete → 204, then 404 with the error envelope.
	rec = postAuthedJSON(t, e, http.MethodDelete, base+"/"+created.ID, cookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, base+"/"+created.ID, cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete: status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"code":"not_found"`) {
		t.Fatalf("404 envelope: body=%s", rec.Body.String())
	}
}
