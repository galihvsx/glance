package api

// HTTP shape for ?include_subtasks=1 (C122T0): the subtasks key is
// present ONLY with the flag; with it, every row carries an array
// (empty for childless issues) of rows with EXACTLY the contract
// fields id, display_id, title, state_id, priority, assignee_ids,
// sort_order. Flag off: the key appears nowhere in the body.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestIssueListIncludeSubtasksHTTP(t *testing.T) {
	pool := newTestPool(t)
	e := testServer(t, pool)
	RegisterAuthRoutes(e, testAuthHandler(pool))
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("issue-subtasks-list"), "test-agent", uniqueIP())

	slug := uniqueSlug("issue-subtasks-list")
	createWorkspaceHTTP(t, e, cookie, "Subtask Co", slug)
	ident := uniqueProjectIdentifier("SL")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident

	createIssue := func(body string) string {
		rec := postAuthedJSON(t, e, http.MethodPost, base+"/issues", cookie, body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create issue: status = %d (body: %s)", rec.Code, rec.Body.String())
		}
		var issue struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &issue); err != nil {
			t.Fatalf("decode issue: %v", err)
		}
		return issue.ID
	}

	parentID := createIssue(`{"name":"Parent work"}`)
	childID := createIssue(`{"name":"Child work","parent_id":"` + parentID + `","priority":4,"sort_order":7}`)
	lonerID := createIssue(`{"name":"Loner work"}`)
	_ = childID
	_ = lonerID

	list := func(q string) []map[string]json.RawMessage {
		rec := getAuthed(t, e, http.MethodGet, base+"/issues"+q, cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("list %s: status = %d", q, rec.Code)
		}
		var body struct {
			Results []map[string]json.RawMessage `json:"results"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode list %s: %v", q, err)
		}
		return body.Results
	}
	byID := func(rows []map[string]json.RawMessage) map[string]map[string]json.RawMessage {
		out := make(map[string]map[string]json.RawMessage, len(rows))
		for _, r := range rows {
			var id string
			if err := json.Unmarshal(r["id"], &id); err != nil {
				t.Fatalf("decode row id: %v", err)
			}
			out[id] = r
		}
		return out
	}

	// Flag on: parent carries exactly one subtask row with the
	// contract fields and nothing else; loner carries [].
	rows := byID(list("?include_subtasks=1&per_page=100"))
	parent, ok := rows[parentID]
	if !ok {
		t.Fatal("parent missing from flag-on list")
	}
	var kids []map[string]json.RawMessage
	if err := json.Unmarshal(parent["subtasks"], &kids); err != nil {
		t.Fatalf("decode parent subtasks: %v", err)
	}
	if len(kids) != 1 {
		t.Fatalf("parent subtasks = %d rows, want 1", len(kids))
	}
	wantKeys := map[string]bool{
		"id": true, "display_id": true, "title": true, "state_id": true,
		"priority": true, "assignee_ids": true, "sort_order": true,
	}
	for k := range kids[0] {
		if !wantKeys[k] {
			t.Fatalf("subtask row carries extra key %q", k)
		}
	}
	for k := range wantKeys {
		if _, ok := kids[0][k]; !ok {
			t.Fatalf("subtask row missing key %q", k)
		}
	}
	var title string
	if err := json.Unmarshal(kids[0]["title"], &title); err != nil || title != "Child work" {
		t.Fatalf("subtask title = %q, want Child work", title)
	}
	var priority string
	if err := json.Unmarshal(kids[0]["priority"], &priority); err != nil || priority != "urgent" {
		t.Fatalf("subtask priority = %q, want \"urgent\" (int 4)", priority)
	}
	var sortOrder float64
	if err := json.Unmarshal(kids[0]["sort_order"], &sortOrder); err != nil || sortOrder != 7 {
		t.Fatalf("subtask sort_order = %v, want 7", sortOrder)
	}
	var assignees []string
	if err := json.Unmarshal(kids[0]["assignee_ids"], &assignees); err != nil || assignees == nil {
		t.Fatalf("subtask assignee_ids = %v, want [] (never null)", assignees)
	}
	loner, ok := rows[lonerID]
	if !ok {
		t.Fatal("loner missing from flag-on list")
	}
	if string(loner["subtasks"]) != "[]" {
		t.Fatalf("loner subtasks = %s, want []", loner["subtasks"])
	}

	// Flag off: the key appears nowhere in the body.
	rec := getAuthed(t, e, http.MethodGet, base+"/issues?per_page=100", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list without flag: status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "subtasks") {
		t.Fatalf("subtasks leaked without the flag: %s", rec.Body.String())
	}
}
