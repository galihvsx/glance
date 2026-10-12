package api

// Duplicate merge HTTP tests (C25T3): POST
// /api/v1/workspaces/{slug}/projects/{identifier}/issues/{uuid}/merge
// with {"into_uuid": "<uuid>"} folds the duplicate into the surviving
// issue. 200 with {"merged": bool, "already_merged": bool, "issue":
// {...}, "into_display_id": "ENG-45"}; re-merging into the same target
// is a 200 no-op (already_merged). 400 on self-merge, cross-project
// target and merge-into-descendant; 404 on unknown issue/target; 403
// for guests. All tests run against the real test database — no skips.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestIssueHTTPMergeHappyPath(t *testing.T) {
	pool := newTestPool(t)
	e := testIssueServer(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("issue-merge-http"), "test-agent", uniqueIP())
	slug := uniqueSlug("issue-merge-http")
	createWorkspaceHTTP(t, e, cookie, "Issue Merge Co", slug)
	ident := uniqueProjectIdentifier("MG")
	createProjectHTTP(t, e, cookie, slug, "Merge", ident)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"

	source := createIssueHTTP(t, e, cookie, base, "Crash on login duplicate")
	target := createIssueHTTP(t, e, cookie, base, "Crash on login")

	rec := postAuthedJSON(t, e, http.MethodPost, base+"/"+source+"/merge", cookie,
		`{"into_uuid":"`+target+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("merge: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var res struct {
		Merged        bool   `json:"merged"`
		AlreadyMerged bool   `json:"already_merged"`
		IntoDisplayID string `json:"into_display_id"`
		Issue         struct {
			ID        string `json:"id"`
			DisplayID string `json:"display_id"`
			StateID   string `json:"state_id"`
		} `json:"issue"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode merge result: %v", err)
	}
	if !res.Merged || res.AlreadyMerged {
		t.Fatalf("result = %+v, want merged", res)
	}
	if res.Issue.ID != source {
		t.Fatalf("issue.id = %q, want %q", res.Issue.ID, source)
	}
	if res.IntoDisplayID == "" {
		t.Fatal("into_display_id is empty")
	}

	// The source now sits in a cancelled-group state.
	var group string
	if err := pool.QueryRow(context.Background(),
		`SELECT s."group" FROM issues i JOIN states s ON s.id = i.state_id WHERE i.id = $1::uuid`,
		source).Scan(&group); err != nil {
		t.Fatalf("state group: %v", err)
	}
	if group != "cancelled" {
		t.Fatalf("source state group = %q, want cancelled", group)
	}

	// Re-merging into the same target is a 200 no-op with
	// already_merged=true.
	rec = postAuthedJSON(t, e, http.MethodPost, base+"/"+source+"/merge", cookie,
		`{"into_uuid":"`+target+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("re-merge: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var noop struct {
		Merged        bool `json:"merged"`
		AlreadyMerged bool `json:"already_merged"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &noop); err != nil {
		t.Fatalf("decode re-merge result: %v", err)
	}
	if noop.Merged || !noop.AlreadyMerged {
		t.Fatalf("re-merge = %+v, want already_merged", noop)
	}
}

func TestIssueHTTPMergeGuards(t *testing.T) {
	pool := newTestPool(t)
	e := testIssueServer(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("issue-merge-http"), "test-agent", uniqueIP())
	slug := uniqueSlug("issue-merge-guards")
	createWorkspaceHTTP(t, e, cookie, "Issue Merge Guards Co", slug)
	ident := uniqueProjectIdentifier("MH")
	createProjectHTTP(t, e, cookie, slug, "Merge Guards", ident)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"

	loner := createIssueHTTP(t, e, cookie, base, "Loner")
	other := createIssueHTTP(t, e, cookie, base, "Other")
	mergeURL := func(id string) string { return base + "/" + id + "/merge" }

	// Self-merge → 400.
	rec := postAuthedJSON(t, e, http.MethodPost, mergeURL(loner), cookie,
		`{"into_uuid":"`+loner+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("self-merge: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// Missing into_uuid → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, mergeURL(loner), cookie, `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty into_uuid: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// Unknown target → 404.
	rec = postAuthedJSON(t, e, http.MethodPost, mergeURL(loner), cookie,
		`{"into_uuid":"00000000-0000-0000-0000-000000000000"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown target: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}

	// Unknown source → 404.
	rec = postAuthedJSON(t, e, http.MethodPost,
		base+"/00000000-0000-0000-0000-000000000000/merge", cookie,
		`{"into_uuid":"`+other+`"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown source: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}

	// Cross-project target → 400.
	otherIdent := uniqueProjectIdentifier("MX")
	createProjectHTTP(t, e, cookie, slug, "Merge X", otherIdent)
	foreign := createIssueHTTP(t, e, cookie,
		"/api/v1/workspaces/"+slug+"/projects/"+otherIdent+"/issues", "Foreign")
	rec = postAuthedJSON(t, e, http.MethodPost, mergeURL(loner), cookie,
		`{"into_uuid":"`+foreign+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("cross-project: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// Guest (role 5 < 15) → 403.
	guestCookie := loginTestUser(t, e, pool, uniqueEmail("issue-merge-guest"), "test-agent", uniqueIP())
	guestID := authedUserID(t, e, guestCookie)
	rec = postAuthedJSON(t, e, http.MethodPost,
		"/api/v1/workspaces/"+slug+"/members",
		cookie,
		fmt.Sprintf(`{"user_id":%q,"role":5}`, guestID))
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("add guest: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPost, mergeURL(loner), guestCookie,
		`{"into_uuid":"`+other+`"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
}
