package api

// Issue HTTP endpoint tests (Task 14): create → 201 with derived display_id,
// get → 200, partial PATCH (tri-state description clear and parent_id
// clear through the JSON layer), delete → 204 then 404, and the spec §5
// error envelope on a missing issue. Real test database, no skips.

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

	cookie := loginTestUser(t, e, pool, uniqueEmail("issue-http"), "test-agent", uniqueIP())
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

	// PATCH explicit null parent_id clears it (tri-state through JSON):
	// create a parent, create a child with parent_id, clear it, and the
	// key must disappear from the JSON (omitempty) — i.e. the column is
	// SQL NULL, not a dangling reference.
	rec = postAuthedJSON(t, e, http.MethodPost, base, cookie, `{"name":"Parent issue"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create parent issue: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var parent struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &parent); err != nil {
		t.Fatalf("decode parent: %v", err)
	}
	rec = postAuthedJSON(t, e, http.MethodPost, base, cookie,
		`{"name":"Child issue","parent_id":"`+parent.ID+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create child issue: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var child struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &child); err != nil {
		t.Fatalf("decode child: %v", err)
	}
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/"+child.ID, cookie, `{"parent_id":null}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear parent_id: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, base+"/"+child.ID, cookie)
	var childMap map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &childMap); err != nil {
		t.Fatalf("decode child after clear: %v", err)
	}
	if _, present := childMap["parent_id"]; present {
		t.Fatalf("parent_id still present after explicit null: %v", childMap["parent_id"])
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

// TestIssueHTTPList covers the list endpoint (Task 15): the
// {results, next_cursor} envelope, sparse fieldsets in JSON, cursor
// pagination through HTTP, and 400 envelopes for bad params.
func TestIssueHTTPList(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testIssueServer(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("issue-list-http"), "test-agent", uniqueIP())
	slug := uniqueSlug("issue-list-http")
	createWorkspaceHTTP(t, e, cookie, "List Co", slug)
	ident := uniqueProjectIdentifier("LI")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"

	postAuthedJSON(t, e, http.MethodPost, base, cookie,
		`{"name":"first","description":{"type":"doc","content":[]}}`)
	postAuthedJSON(t, e, http.MethodPost, base, cookie, `{"name":"second"}`)
	postAuthedJSON(t, e, http.MethodPost, base, cookie, `{"name":"third"}`)

	// Default list: 200, envelope shape, description omitted.
	rec := getAuthed(t, e, http.MethodGet, base+"?per_page=2", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var page struct {
		Results []struct {
			ID        string `json:"id"`
			DisplayID string `json:"display_id"`
			Name      string `json:"name"`
		} `json:"results"`
		NextCursor string `json:"next_cursor"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(page.Results) != 2 || page.NextCursor == "" {
		t.Fatalf("page = %+v, want 2 results + next_cursor", page)
	}
	if page.Results[0].Name != "third" || page.Results[1].Name != "second" {
		t.Fatalf("order = %q,%q, want third,second (-updated_at)",
			page.Results[0].Name, page.Results[1].Name)
	}
	if page.Results[0].DisplayID != ident+"-3" {
		t.Fatalf("display_id = %q, want %s-3", page.Results[0].DisplayID, ident)
	}
	if strings.Contains(rec.Body.String(), `"description"`) {
		t.Fatal("description must be omitted from the list by default")
	}

	// Follow the cursor: the last page has no next_cursor.
	rec = getAuthed(t, e, http.MethodGet, base+"?per_page=2&cursor="+page.NextCursor, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list page 2: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var page2 struct {
		Results    []map[string]any `json:"results"`
		NextCursor string           `json:"next_cursor"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page2); err != nil {
		t.Fatalf("decode page 2: %v", err)
	}
	if len(page2.Results) != 1 || page2.NextCursor != "" {
		t.Fatalf("page2 = %+v, want 1 result, no next_cursor", page2)
	}

	// fields=description includes it.
	rec = getAuthed(t, e, http.MethodGet, base+"?fields=description&q=first", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list fields: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"description":{"type":"doc"`) {
		t.Fatalf("fields=description: body missing description: %s", rec.Body.String())
	}

	// Bad params → 400 with the error envelope.
	for _, path := range []string{
		base + "?order_by=bogus",
		base + "?per_page=0",
		base + "?cursor=not-a-cursor",
		base + "?updated_after=yesterday",
	} {
		rec = getAuthed(t, e, http.MethodGet, path, cookie)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("GET %s: status = %d, want 400 (body: %s)", path, rec.Code, rec.Body.String())
		}
		var env struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Error.Code == "" {
			t.Fatalf("GET %s: body is not the error envelope: %s", path, rec.Body.String())
		}
	}
}
