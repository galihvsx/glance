package api

// Issue-link HTTP endpoint tests (C4T0): full CRUD through the routes,
// the 409 envelopes (self-link, cross-project, duplicate), 400 on bad
// kind / malformed ids, 404 on unknown delete, and the
// ?include_links=true Gantt contract on the issue list. Real test
// database, no skips.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestIssueLinkHTTP(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterIssueLinkRoutes(e, &IssueHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("link-http"), "test-agent", uniqueIP())
	slug := uniqueSlug("link-http")
	createWorkspaceHTTP(t, e, cookie, "Link Co", slug)
	ident := uniqueProjectIdentifier("LH")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	issueBase := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"
	issA := createIssueHTTP(t, e, cookie, issueBase, "A blocks B")
	issB := createIssueHTTP(t, e, cookie, issueBase, "B")
	linksBaseA := issueBase + "/" + issA + "/links"

	// Create → 201, default kind 'blocks'.
	rec := postAuthedJSON(t, e, http.MethodPost, linksBaseA, cookie,
		`{"target_issue_id":"`+issB+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create link: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID            string `json:"id"`
		IssueID       string `json:"issue_id"`
		TargetIssueID string `json:"target_issue_id"`
		Kind          string `json:"kind"`
		Direction     string `json:"direction"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode link: %v", err)
	}
	if created.Kind != "blocks" || created.IssueID != issA || created.TargetIssueID != issB {
		t.Fatalf("created = %+v", created)
	}

	// Duplicate edge → 409 envelope.
	rec = postAuthedJSON(t, e, http.MethodPost, linksBaseA, cookie,
		`{"target_issue_id":"`+issB+`","kind":"relates_to"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate link: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"conflict"`) {
		t.Fatalf("409 envelope: body=%s", rec.Body.String())
	}

	// Self-link → 409.
	rec = postAuthedJSON(t, e, http.MethodPost, linksBaseA, cookie,
		`{"target_issue_id":"`+issA+`"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("self link: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}

	// Bad kind → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, linksBaseA, cookie,
		`{"target_issue_id":"`+issB+`","kind":"parent_of"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad kind: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// Unknown target → 404.
	rec = postAuthedJSON(t, e, http.MethodPost, linksBaseA, cookie,
		`{"target_issue_id":"00000000-0000-0000-0000-000000000000"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown target: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}

	// Cross-project target → 409.
	ident2 := uniqueProjectIdentifier("LH2")
	createProjectHTTP(t, e, cookie, slug, "Other", ident2)
	issueBase2 := "/api/v1/workspaces/" + slug + "/projects/" + ident2 + "/issues"
	issX := createIssueHTTP(t, e, cookie, issueBase2, "foreign")
	rec = postAuthedJSON(t, e, http.MethodPost, linksBaseA, cookie,
		`{"target_issue_id":"`+issX+`"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("cross-project link: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"conflict"`) {
		t.Fatalf("409 envelope: body=%s", rec.Body.String())
	}

	// Malformed issue uuid in path → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, issueBase+"/not-a-uuid/links", cookie, `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed uuid: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// List → 200 with the outgoing edge; from B's side it's incoming.
	rec = getAuthed(t, e, http.MethodGet, linksBaseA, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list links: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var listed struct {
		Links []struct {
			ID        string `json:"id"`
			Kind      string `json:"kind"`
			Direction string `json:"direction"`
		} `json:"links"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Links) != 1 || listed.Links[0].ID != created.ID ||
		listed.Links[0].Direction != "outgoing" {
		t.Fatalf("listed = %+v", listed)
	}
	rec = getAuthed(t, e, http.MethodGet, issueBase+"/"+issB+"/links", cookie)
	if !strings.Contains(rec.Body.String(), `"direction":"incoming"`) {
		t.Fatalf("incoming direction missing: %s", rec.Body.String())
	}

	// Delete → 204; again → 404 envelope.
	rec = postAuthedJSON(t, e, http.MethodDelete, linksBaseA+"/"+created.ID, cookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete link: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodDelete, linksBaseA+"/"+created.ID, cookie, ``)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("re-delete: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"not_found"`) {
		t.Fatalf("404 envelope: body=%s", rec.Body.String())
	}
	// Malformed link id → 400.
	rec = postAuthedJSON(t, e, http.MethodDelete, linksBaseA+"/nope", cookie, ``)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed link id: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestIssueListIncludeLinksHTTP covers the Gantt consumption contract:
// ?include_links=true attaches the dependency edges for the listed
// issues (one query, no N+1); without the flag the envelope is
// byte-identical to the plain list.
func TestIssueListIncludeLinksHTTP(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterIssueLinkRoutes(e, &IssueHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("link-gantt"), "test-agent", uniqueIP())
	slug := uniqueSlug("link-gantt")
	createWorkspaceHTTP(t, e, cookie, "Link Co", slug)
	ident := uniqueProjectIdentifier("LG")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	issueBase := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"
	issA := createIssueHTTP(t, e, cookie, issueBase, "A")
	issB := createIssueHTTP(t, e, cookie, issueBase, "B")
	issC := createIssueHTTP(t, e, cookie, issueBase, "C")

	rec := postAuthedJSON(t, e, http.MethodPost, issueBase+"/"+issA+"/links", cookie,
		`{"target_issue_id":"`+issB+`","kind":"blocks"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create link: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	// Without the flag: no "links" key.
	rec = getAuthed(t, e, http.MethodGet, issueBase, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"links"`) {
		t.Fatalf("plain list must not contain links: %s", rec.Body.String())
	}

	// With the flag: the edge is attached.
	rec = getAuthed(t, e, http.MethodGet, issueBase+"?include_links=true", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list+links: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var withLinks struct {
		Results []struct {
			ID string `json:"id"`
		} `json:"results"`
		Links []struct {
			IssueID       string `json:"issue_id"`
			TargetIssueID string `json:"target_issue_id"`
			Kind          string `json:"kind"`
		} `json:"links"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &withLinks); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(withLinks.Results) != 3 {
		t.Fatalf("results = %d, want 3", len(withLinks.Results))
	}
	seenIDs := map[string]bool{}
	for _, r := range withLinks.Results {
		seenIDs[r.ID] = true
	}
	if !seenIDs[issA] || !seenIDs[issB] || !seenIDs[issC] {
		t.Fatalf("results missing issues: %+v", withLinks.Results)
	}
	if len(withLinks.Links) != 1 || withLinks.Links[0].IssueID != issA ||
		withLinks.Links[0].TargetIssueID != issB || withLinks.Links[0].Kind != "blocks" {
		t.Fatalf("links = %+v", withLinks.Links)
	}
}
