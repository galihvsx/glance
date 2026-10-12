package api

// Convert issue <-> sub-issue HTTP tests (C24T1): POST
// /api/v1/workspaces/{slug}/projects/{identifier}/issues/{uuid}/convert
// with {"parent_uuid": "<uuid>"} demotes to a sub-issue and
// {"parent_uuid": null} promotes back to a standalone issue. 200 with
// {"converted": bool, "issue": {...}}; 400 on children-block, depth-1
// and cross-project parents; 404 on unknown issue/parent; 403 for
// guests. All tests run against the real test database — no skips.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestIssueHTTPConvertDemote(t *testing.T) {
	pool := newTestPool(t)
	e := testIssueServer(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("issue-convert-http"), "test-agent", uniqueIP())
	slug := uniqueSlug("issue-convert-http")
	createWorkspaceHTTP(t, e, cookie, "Issue Convert Co", slug)
	ident := uniqueProjectIdentifier("CV")
	createProjectHTTP(t, e, cookie, slug, "Convert", ident)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"

	parent := createIssueHTTP(t, e, cookie, base, "Convert parent")
	child := createIssueHTTP(t, e, cookie, base, "Convert child")

	rec := postAuthedJSON(t, e, http.MethodPost, base+"/"+child+"/convert", cookie,
		`{"parent_uuid":"`+parent+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("convert demote: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var res struct {
		Converted bool `json:"converted"`
		Issue     struct {
			ID        string  `json:"id"`
			DisplayID string  `json:"display_id"`
			ParentID  *string `json:"parent_id"`
		} `json:"issue"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode convert result: %v", err)
	}
	if !res.Converted {
		t.Fatal("converted = false, want true")
	}
	if res.Issue.ID != child {
		t.Fatalf("issue.id = %q, want %q", res.Issue.ID, child)
	}
	if res.Issue.ParentID == nil || *res.Issue.ParentID != parent {
		t.Fatalf("issue.parent_id = %v, want %s", res.Issue.ParentID, parent)
	}

	// Demoting again to the same parent is a 200 no-op.
	rec = postAuthedJSON(t, e, http.MethodPost, base+"/"+child+"/convert", cookie,
		`{"parent_uuid":"`+parent+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("convert demote no-op: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var noop struct {
		Converted bool `json:"converted"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &noop); err != nil {
		t.Fatalf("decode no-op result: %v", err)
	}
	if noop.Converted {
		t.Fatal("converted = true on idempotent re-demote, want false")
	}

	// Promote back with null.
	rec = postAuthedJSON(t, e, http.MethodPost, base+"/"+child+"/convert", cookie,
		`{"parent_uuid":null}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("convert promote: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var promoted struct {
		Converted bool `json:"converted"`
		Issue     struct {
			ParentID *string `json:"parent_id"`
		} `json:"issue"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &promoted); err != nil {
		t.Fatalf("decode promote result: %v", err)
	}
	if !promoted.Converted {
		t.Fatal("converted = false on promote, want true")
	}
	if promoted.Issue.ParentID != nil {
		t.Fatalf("issue.parent_id = %v after promote, want nil", promoted.Issue.ParentID)
	}
}

func TestIssueHTTPConvertErrors(t *testing.T) {
	pool := newTestPool(t)
	e := testIssueServer(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("issue-convert-err"), "test-agent", uniqueIP())
	slug := uniqueSlug("issue-convert-err")
	createWorkspaceHTTP(t, e, cookie, "Issue Convert Err Co", slug)
	ident := uniqueProjectIdentifier("CE")
	createProjectHTTP(t, e, cookie, slug, "Convert Err", ident)
	otherIdent := uniqueProjectIdentifier("CE")
	createProjectHTTP(t, e, cookie, slug, "Other", otherIdent)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"
	convertURL := func(id string) string { return base + "/" + id + "/convert" }

	parent := createIssueHTTP(t, e, cookie, base, "Err parent")
	child := createIssueHTTP(t, e, cookie, base, "Err child")
	grandchild := createIssueHTTP(t, e, cookie, base, "Err grandchild")
	// grandchild is a sub-issue of child: child cannot be converted
	// while it has children (400).
	rec := postAuthedJSON(t, e, http.MethodPost, base+"/"+grandchild+"/parent", cookie,
		`{"parent_issue_id":"`+child+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup parent: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPost, convertURL(child), cookie,
		`{"parent_uuid":"`+parent+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("children-block: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// Depth-1: a parent that is itself a sub-issue → 400.
	mid := createIssueHTTP(t, e, cookie, base, "Err mid")
	rec = postAuthedJSON(t, e, http.MethodPost, base+"/"+mid+"/parent", cookie,
		`{"parent_issue_id":"`+parent+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup mid parent: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	loner := createIssueHTTP(t, e, cookie, base, "Err loner")
	rec = postAuthedJSON(t, e, http.MethodPost, convertURL(loner), cookie,
		`{"parent_uuid":"`+mid+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("depth-1: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// Cross-project parent → 400.
	foreign := createIssueHTTP(t, e, cookie, "/api/v1/workspaces/"+slug+"/projects/"+otherIdent+"/issues", "Foreign parent")
	rec = postAuthedJSON(t, e, http.MethodPost, convertURL(loner), cookie,
		`{"parent_uuid":"`+foreign+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("cross-project: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// Unknown parent → 404.
	rec = postAuthedJSON(t, e, http.MethodPost, convertURL(loner), cookie,
		`{"parent_uuid":"00000000-0000-0000-0000-000000000000"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown parent: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}

	// Unknown issue → 404.
	rec = postAuthedJSON(t, e, http.MethodPost,
		convertURL("00000000-0000-0000-0000-000000000000"), cookie,
		`{"parent_uuid":null}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown issue: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}

	// Self-parent → 409.
	rec = postAuthedJSON(t, e, http.MethodPost, convertURL(loner), cookie,
		`{"parent_uuid":"`+loner+`"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("self-parent: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}

	// Guest (role 5 < 15) → 403.
	guestCookie := loginTestUser(t, e, pool, uniqueEmail("issue-convert-guest"), "test-agent", uniqueIP())
	guestID := authedUserID(t, e, guestCookie)
	rec = postAuthedJSON(t, e, http.MethodPost,
		"/api/v1/workspaces/"+slug+"/members",
		cookie,
		fmt.Sprintf(`{"user_id":%q,"role":5}`, guestID))
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("add guest: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPost, convertURL(loner), guestCookie,
		`{"parent_uuid":"`+parent+`"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestIssueHTTPConvertPromoteNoOp(t *testing.T) {
	pool := newTestPool(t)
	e := testIssueServer(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("issue-convert-noop"), "test-agent", uniqueIP())
	slug := uniqueSlug("issue-convert-noop")
	createWorkspaceHTTP(t, e, cookie, "Issue Convert Noop Co", slug)
	ident := uniqueProjectIdentifier("CN")
	createProjectHTTP(t, e, cookie, slug, "Convert Noop", ident)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"

	iss := createIssueHTTP(t, e, cookie, base, "Noop standalone")
	rec := postAuthedJSON(t, e, http.MethodPost, base+"/"+iss+"/convert", cookie,
		`{"parent_uuid":null}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("promote no-op: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var res struct {
		Converted bool `json:"converted"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Converted {
		t.Fatal("converted = true on already-standalone issue, want false")
	}
}
