package api

// C5T8: PATCH /issues/bulk HTTP tests. Happy path applies all four
// set-fields and returns {updated, issue_ids}; a bad id aborts the whole
// batch (404, nothing applied); >100 ids → 400; guests → 403. Real test
// database, no skips.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
)

type bulkSetTestCtx struct {
	e      *echo.Echo
	pool   *pgxpool.Pool
	cookie *http.Cookie
	slug   string
	ident  string
	base   string
	ids    []string
}

func setupBulkSetHTTP(t *testing.T) *bulkSetTestCtx {
	t.Helper()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testIssueServer(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("bulk-set-http"), "test-agent", uniqueIP())
	slug := uniqueSlug("bulk-set-http")
	createWorkspaceHTTP(t, e, cookie, "Bulk Co", slug)
	ident := uniqueProjectIdentifier("BS")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"

	var ids []string
	for _, name := range []string{"First", "Second"} {
		rec := postAuthedJSON(t, e, http.MethodPost, base, cookie,
			fmt.Sprintf(`{"name":%q}`, name))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create issue: status = %d (body: %s)", rec.Code, rec.Body.String())
		}
		var created struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
			t.Fatalf("decode created: %v", err)
		}
		ids = append(ids, created.ID)
	}
	return &bulkSetTestCtx{e: e, pool: pool, cookie: cookie, slug: slug, ident: ident, base: base, ids: ids}
}

func bulkSetStates(t *testing.T, c *bulkSetTestCtx) []struct {
	ID   string `json:"id"`
	Name string `json:"name"`
} {
	t.Helper()
	statesBase := "/api/v1/workspaces/" + c.slug + "/projects/" + c.ident
	rec := getAuthed(t, c.e, http.MethodGet, statesBase+"/states", c.cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list states: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var decoded struct {
		States []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"states"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode states: %v", err)
	}
	return decoded.States
}

func TestBulkSetHTTPAppliesAllFields(t *testing.T) {
	c := setupBulkSetHTTP(t)

	// Create a label and pick the "Todo" state for the set.
	statesBase := "/api/v1/workspaces/" + c.slug + "/projects/" + c.ident
	rec := postAuthedJSON(t, c.e, http.MethodPost, statesBase+"/labels", c.cookie,
		`{"name":"bulk-http-label"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create label: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var label struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &label); err != nil {
		t.Fatalf("decode label: %v", err)
	}
	var todoID string
	for _, s := range bulkSetStates(t, c) {
		if s.Name == "Todo" {
			todoID = s.ID
		}
	}
	if todoID == "" {
		t.Fatal("no Todo state")
	}
	assigneeID := authedUserID(t, c.e, c.cookie)

	idsJSON, _ := json.Marshal(c.ids)
	body := fmt.Sprintf(`{"issue_ids":%s,"set":{"state_id":%q,"priority":4,"label_ids":[%q],"assignee_id":%q}}`,
		idsJSON, todoID, label.ID, assigneeID)

	rec = postAuthedJSON(t, c.e, http.MethodPatch, c.base+"/bulk", c.cookie, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("bulk set: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		Updated  int      `json:"updated"`
		IssueIDs []string `json:"issue_ids"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode bulk response: %v", err)
	}
	if resp.Updated != 2 || len(resp.IssueIDs) != 2 {
		t.Fatalf("response = %+v, want updated=2 with both ids", resp)
	}

	// Verify every field landed on the first issue.
	rec = getAuthed(t, c.e, http.MethodGet, c.base+"/"+c.ids[0], c.cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get issue: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var got struct {
		StateID  string `json:"state_id"`
		Priority int    `json:"priority"`
		Labels   []struct {
			ID string `json:"id"`
		} `json:"labels"`
		Assignees []struct {
			ID string `json:"id"`
		} `json:"assignees"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode issue: %v", err)
	}
	if got.StateID != todoID || got.Priority != 4 {
		t.Fatalf("issue = state %q priority %d, want %q/4", got.StateID, got.Priority, todoID)
	}
	if len(got.Labels) != 1 || got.Labels[0].ID != label.ID {
		t.Fatalf("labels = %+v, want the one label", got.Labels)
	}
	if len(got.Assignees) != 1 || got.Assignees[0].ID != assigneeID {
		t.Fatalf("assignees = %+v, want the actor", got.Assignees)
	}
}

func TestBulkSetHTTPBadIDAbortsBatch(t *testing.T) {
	c := setupBulkSetHTTP(t)

	ids := append([]string{}, c.ids...)
	ids = append(ids, "00000000-0000-0000-0000-000000000000")
	idsJSON, _ := json.Marshal(ids)
	body := fmt.Sprintf(`{"issue_ids":%s,"set":{"priority":3}}`, idsJSON)
	rec := postAuthedJSON(t, c.e, http.MethodPatch, c.base+"/bulk", c.cookie, body)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bulk set with bad id: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}

	// The valid issue must be untouched: priority still 0.
	rec = getAuthed(t, c.e, http.MethodGet, c.base+"/"+c.ids[0], c.cookie)
	var got struct {
		Priority int `json:"priority"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode issue: %v", err)
	}
	if got.Priority != 0 {
		t.Fatalf("priority = %d after aborted batch, want 0", got.Priority)
	}
}

func TestBulkSetHTTPTooManyIDs400(t *testing.T) {
	c := setupBulkSetHTTP(t)

	ids := make([]string, 101)
	for i := range ids {
		ids[i] = c.ids[0]
	}
	idsJSON, _ := json.Marshal(ids)
	body := fmt.Sprintf(`{"issue_ids":%s,"set":{"priority":1}}`, idsJSON)
	rec := postAuthedJSON(t, c.e, http.MethodPatch, c.base+"/bulk", c.cookie, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("101 ids: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestBulkSetHTTPEmptyIDs400(t *testing.T) {
	c := setupBulkSetHTTP(t)

	rec := postAuthedJSON(t, c.e, http.MethodPatch, c.base+"/bulk", c.cookie,
		`{"issue_ids":[],"set":{"priority":1}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty ids: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestBulkSetHTTPEmptySet400(t *testing.T) {
	c := setupBulkSetHTTP(t)

	idsJSON, _ := json.Marshal(c.ids)
	body := fmt.Sprintf(`{"issue_ids":%s,"set":{}}`, idsJSON)
	rec := postAuthedJSON(t, c.e, http.MethodPatch, c.base+"/bulk", c.cookie, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty set: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestBulkSetHTTPGuest403(t *testing.T) {
	c := setupBulkSetHTTP(t)

	guestCookie := loginTestUser(t, c.e, c.pool, uniqueEmail("bulk-set-guest"), "test-agent", uniqueIP())
	guestID := authedUserID(t, c.e, guestCookie)
	membersPath := "/api/v1/workspaces/" + c.slug + "/members"
	rec := postAuthedJSON(t, c.e, http.MethodPost, membersPath, c.cookie,
		fmt.Sprintf(`{"user_id":%q,"role":5}`, guestID))
	if rec.Code != http.StatusOK {
		t.Fatalf("add guest: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	idsJSON, _ := json.Marshal(c.ids)
	body := fmt.Sprintf(`{"issue_ids":%s,"set":{"priority":1}}`, idsJSON)
	rec = postAuthedJSON(t, c.e, http.MethodPatch, c.base+"/bulk", guestCookie, body)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest bulk set: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestBulkSetHTTPClearAssignee(t *testing.T) {
	c := setupBulkSetHTTP(t)

	actorID := authedUserID(t, c.e, c.cookie)
	assigneesBase := "/api/v1/workspaces/" + c.slug + "/projects/" + c.ident + "/issues/" + c.ids[0] + "/assignees/" + actorID
	rec := postAuthedJSON(t, c.e, http.MethodPost, assigneesBase, c.cookie, `{}`)
	if rec.Code != http.StatusNoContent && rec.Code != http.StatusOK {
		t.Fatalf("assign: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	idsJSON, _ := json.Marshal(c.ids[:1])
	body := fmt.Sprintf(`{"issue_ids":%s,"set":{"assignee_id":null}}`, idsJSON)
	rec = postAuthedJSON(t, c.e, http.MethodPatch, c.base+"/bulk", c.cookie, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("bulk clear assignee: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	rec = getAuthed(t, c.e, http.MethodGet, c.base+"/"+c.ids[0], c.cookie)
	var got struct {
		Assignees []struct {
			ID string `json:"id"`
		} `json:"assignees"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode issue: %v", err)
	}
	if len(got.Assignees) != 0 {
		t.Fatalf("assignees = %+v, want cleared", got.Assignees)
	}
}
