package api

// Sub-issue parent HTTP endpoint tests (C5T4): set / clear through the
// routes, the 409 envelopes (self-parent, cross-project, cycle), 404 on
// an unknown parent id, and the ?include_children=1 detail contract.
// Real test database, no skips.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestIssueParentHTTP(t *testing.T) {
	_, e, cookie, issueBase := setupBulkProject(t, "subparent")

	parent := createIssueHTTP(t, e, cookie, issueBase, "Parent issue")
	child := createIssueHTTP(t, e, cookie, issueBase, "Child issue")
	parentBase := issueBase + "/" + child + "/parent"

	// Set → 200 with the new parent id.
	rec := postAuthedJSON(t, e, http.MethodPost, parentBase, cookie,
		`{"parent_issue_id":"`+parent+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("set parent: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var setRes struct {
		ParentIssueID *string `json:"parent_issue_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &setRes); err != nil {
		t.Fatalf("decode set parent: %v", err)
	}
	if setRes.ParentIssueID == nil || *setRes.ParentIssueID != parent {
		t.Fatalf("parent_issue_id = %v, want %s", setRes.ParentIssueID, parent)
	}

	// The detail endpoint now reports the parent.
	rec = postAuthedJSON(t, e, http.MethodGet, issueBase+"/"+child, cookie, ``)
	if rec.Code != http.StatusOK {
		t.Fatalf("get child: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"parent_id":"`+parent+`"`) {
		t.Fatalf("detail missing parent_id: %s", rec.Body.String())
	}
	// No children key without the flag.
	if strings.Contains(rec.Body.String(), `"children"`) {
		t.Fatalf("detail without flag must not include children: %s", rec.Body.String())
	}

	// ?include_children=1 on the PARENT returns the child summary.
	rec = postAuthedJSON(t, e, http.MethodGet, issueBase+"/"+parent+"?include_children=1", cookie, ``)
	if rec.Code != http.StatusOK {
		t.Fatalf("get parent +children: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var detail struct {
		Children []struct {
			UUID       string `json:"uuid"`
			Identifier string `json:"identifier"`
			Title      string `json:"title"`
			State      string `json:"state"`
		} `json:"children"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if len(detail.Children) != 1 {
		t.Fatalf("children = %d, want 1 (body: %s)", len(detail.Children), rec.Body.String())
	}
	c := detail.Children[0]
	if c.UUID != child || c.Title != "Child issue" || c.Identifier == "" || c.State == "" {
		t.Fatalf("child summary = %+v", c)
	}

	// Self-parent → 409.
	rec = postAuthedJSON(t, e, http.MethodPost, parentBase, cookie,
		`{"parent_issue_id":"`+child+`"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("self parent: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"conflict"`) {
		t.Fatalf("409 envelope: body=%s", rec.Body.String())
	}

	// Cycle: parent is the child's ancestor chain root; making the child
	// the parent of the root would close a loop.
	rec = postAuthedJSON(t, e, http.MethodPost, issueBase+"/"+parent+"/parent", cookie,
		`{"parent_issue_id":"`+child+`"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("cycle: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}

	// Unknown parent id → 404.
	rec = postAuthedJSON(t, e, http.MethodPost, parentBase, cookie,
		`{"parent_issue_id":"00000000-0000-0000-0000-000000000000"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown parent: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}

	// Cross-project parent → 409. A second project in the same workspace.
	slug := strings.Split(issueBase, "/")[4]
	ident2 := uniqueProjectIdentifier("SP")
	createProjectHTTP(t, e, cookie, slug, "Design", ident2)
	foreign := createIssueHTTP(t, e, cookie,
		"/api/v1/workspaces/"+slug+"/projects/"+ident2+"/issues", "Foreign parent")
	rec = postAuthedJSON(t, e, http.MethodPost, parentBase, cookie,
		`{"parent_issue_id":"`+foreign+`"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("cross-project: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}

	// Clear → 200, parent_id gone from detail.
	rec = postAuthedJSON(t, e, http.MethodDelete, parentBase, cookie, ``)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear parent: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodGet, issueBase+"/"+child, cookie, ``)
	if rec.Code != http.StatusOK {
		t.Fatalf("get child after clear: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"parent_id"`) {
		t.Fatalf("parent_id still present after clear: %s", rec.Body.String())
	}
	// And the parent's children list is empty again.
	rec = postAuthedJSON(t, e, http.MethodGet, issueBase+"/"+parent+"?include_children=1", cookie, ``)
	var detail2 struct {
		Children []any `json:"children"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &detail2); err != nil {
		t.Fatalf("decode detail2: %v", err)
	}
	if len(detail2.Children) != 0 {
		t.Fatalf("children after clear = %d, want 0", len(detail2.Children))
	}
}
