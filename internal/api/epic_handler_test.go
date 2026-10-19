package api

// Epic HTTP endpoint tests (C71T0): full CRUD through the routes,
// archive/unarchive, the archived list filter, duplicate-name 409
// envelope, the delete guard (409 without ?force=true, 204 with it),
// issue add/remove idempotency, and initiative↔epic links.
// Real test database, no skips.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestEpicHTTP(t *testing.T) {
	pool := newTestPool(t)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterEpicRoutes(e, &IssueHandler{Pool: pool})
	RegisterInitiativeRoutes(e, &IssueHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("epic-http"), "test-agent", uniqueIP())
	slug := uniqueSlug("epic-http")
	createWorkspaceHTTP(t, e, cookie, "Epic Co", slug)
	ident := uniqueProjectIdentifier("EH")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	issueBase := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"
	epicBase := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/epics"

	// Create → 201.
	rec := postAuthedJSON(t, e, http.MethodPost, epicBase, cookie,
		`{"name":"Platform","description":"The long epic"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create epic: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		ProjectID   string `json:"project_id"`
		IssueCount  int64  `json:"issue_count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode epic: %v", err)
	}
	if created.Name != "Platform" || created.ProjectID == "" || created.IssueCount != 0 {
		t.Fatalf("created = %+v", created)
	}
	epicURL := epicBase + "/" + created.ID

	// Duplicate name → 409 envelope.
	rec = postAuthedJSON(t, e, http.MethodPost, epicBase, cookie, `{"name":"Platform"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate epic: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"conflict"`) {
		t.Fatalf("409 envelope: body=%s", rec.Body.String())
	}

	// Empty name → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, epicBase, cookie, `{"name":"  "}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty name: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// Get → 200 with progress; malformed id → 400.
	rec = getAuthed(t, e, http.MethodGet, epicURL, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get epic: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"progress":{"total":0`) {
		t.Fatalf("detail must carry progress: %s", rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, epicBase+"/not-a-uuid", cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed id: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// PATCH rename + clear description.
	rec = postAuthedJSON(t, e, http.MethodPatch, epicURL, cookie, `{"name":"Platform v2","description":""}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch epic: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"name":"Platform v2"`) {
		t.Fatalf("renamed body: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"description"`) {
		t.Fatalf("description should be cleared: %s", rec.Body.String())
	}

	// Archive → 200 with archived_at; hidden from default list; shown
	// with ?include_archived=true; unarchive restores.
	rec = postAuthedJSON(t, e, http.MethodPost, epicURL+"/archive", cookie, `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("archive: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"archived_at"`) {
		t.Fatalf("archive body: %s", rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, epicBase, cookie)
	var listed struct {
		Epics []struct {
			ID string `json:"id"`
		} `json:"epics"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Epics) != 0 {
		t.Fatalf("archived epic should be hidden: %+v", listed)
	}
	rec = getAuthed(t, e, http.MethodGet, epicBase+"?include_archived=true", cookie)
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list incl: %v", err)
	}
	if len(listed.Epics) != 1 || listed.Epics[0].ID != created.ID {
		t.Fatalf("include_archived list = %+v", listed)
	}
	rec = postAuthedJSON(t, e, http.MethodPost, epicURL+"/unarchive", cookie, `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("unarchive: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"archived_at"`) {
		t.Fatalf("unarchive body: %s", rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, epicBase, cookie)
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Epics) != 1 {
		t.Fatalf("unarchived epic should list: %+v", listed)
	}

	// Add issues → 200 (idempotent: twice is fine); list shows them.
	iss1 := createIssueHTTP(t, e, cookie, issueBase, "in epic")
	iss2 := createIssueHTTP(t, e, cookie, issueBase, "also in epic")
	for range 2 {
		rec = postAuthedJSON(t, e, http.MethodPost, epicURL+"/issues", cookie,
			`{"issue_ids":[`+`"`+iss1+`","`+iss2+`"]}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("add issues: status = %d (body: %s)", rec.Code, rec.Body.String())
		}
	}
	rec = getAuthed(t, e, http.MethodGet, epicURL+"/issues", cookie)
	var miListed struct {
		Issues []struct {
			ID        string `json:"id"`
			DisplayID string `json:"display_id"`
		} `json:"issues"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &miListed); err != nil {
		t.Fatalf("decode epic issues: %v", err)
	}
	if len(miListed.Issues) != 2 {
		t.Fatalf("epic issues = %+v", miListed)
	}

	// Issue detail carries epics: [{id, name}].
	rec = getAuthed(t, e, http.MethodGet, issueBase+"/"+iss1, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get issue: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var issueDetail struct {
		Epics []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"epics"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &issueDetail); err != nil {
		t.Fatalf("decode issue detail: %v", err)
	}
	if len(issueDetail.Epics) != 1 || issueDetail.Epics[0].ID != created.ID ||
		issueDetail.Epics[0].Name != "Platform v2" {
		t.Fatalf("issue epics = %+v", issueDetail)
	}

	// Remove one → 200; removing again is a silent no-op.
	for range 2 {
		rec = postAuthedJSON(t, e, http.MethodDelete, epicURL+"/issues", cookie,
			`{"issue_ids":["`+iss1+`"]}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("remove issues: status = %d (body: %s)", rec.Code, rec.Body.String())
		}
	}

	// Malformed issue id → 400; empty list → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, epicURL+"/issues", cookie, `{"issue_ids":["nope"]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad issue id: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPost, epicURL+"/issues", cookie, `{"issue_ids":[]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty add: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// Delete with issues still attached → 409; with ?force=true → 204.
	rec = getAuthed(t, e, http.MethodDelete, epicURL, cookie)
	if rec.Code != http.StatusConflict {
		t.Fatalf("guarded delete: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "force=true") {
		t.Fatalf("409 should mention ?force=true: %s", rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodDelete, epicURL+"?force=true", cookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("force delete: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, epicURL, cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get deleted: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}

	// Initiative↔epic links.
	rec = postAuthedJSON(t, e, http.MethodPost, "/api/v1/workspaces/"+slug+"/initiatives", cookie,
		`{"name":"Big Bet"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create initiative: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var initCreated struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &initCreated); err != nil {
		t.Fatalf("decode initiative: %v", err)
	}
	initEpics := "/api/v1/workspaces/" + slug + "/initiatives/" + initCreated.ID + "/epics"

	rec = postAuthedJSON(t, e, http.MethodPost, epicBase, cookie, `{"name":"Linked"}`)
	var linked struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &linked); err != nil {
		t.Fatalf("decode epic: %v", err)
	}
	// Link twice (idempotent) → 200 both times.
	for range 2 {
		rec = postAuthedJSON(t, e, http.MethodPost, initEpics, cookie,
			`{"epic_id":"`+linked.ID+`"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("link epic: status = %d (body: %s)", rec.Code, rec.Body.String())
		}
	}
	rec = getAuthed(t, e, http.MethodGet, initEpics, cookie)
	var epicsListed struct {
		Epics []struct {
			EpicID      string `json:"epic_id"`
			EpicName    string `json:"epic_name"`
			ProjectName string `json:"project_name"`
		} `json:"epics"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &epicsListed); err != nil {
		t.Fatalf("decode initiative epics: %v", err)
	}
	if len(epicsListed.Epics) != 1 || epicsListed.Epics[0].EpicName != "Linked" ||
		epicsListed.Epics[0].ProjectName != "Engineering" {
		t.Fatalf("initiative epics = %+v", epicsListed)
	}
	// Unlink twice (idempotent) → 200 both times.
	for range 2 {
		rec = getAuthed(t, e, http.MethodDelete, initEpics+"/"+linked.ID, cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("unlink epic: status = %d (body: %s)", rec.Code, rec.Body.String())
		}
	}
	rec = getAuthed(t, e, http.MethodGet, initEpics, cookie)
	if err := json.Unmarshal(rec.Body.Bytes(), &epicsListed); err != nil {
		t.Fatalf("decode after unlink: %v", err)
	}
	if len(epicsListed.Epics) != 0 {
		t.Fatalf("after unlink: %+v", epicsListed)
	}
	// Malformed epic id in body → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, initEpics, cookie, `{"epic_id":"nope"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad epic id: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	// Unknown initiative → 404.
	rec = postAuthedJSON(t, e, http.MethodPost,
		"/api/v1/workspaces/"+slug+"/initiatives/00000000-0000-4000-8000-000000000000/epics",
		cookie, `{"epic_id":"`+linked.ID+`"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing initiative: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
}
