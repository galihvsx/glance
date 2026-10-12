package api

// Move-issue HTTP tests (C24T0): POST
// /api/v1/workspaces/{slug}/projects/{identifier}/issues/{uuid}/move with
// {"target_project_identifier": "..."} → 200 with the move report;
// 400 on same-project / intake / bad body; 403 for guests; 404 for
// unknown issues and non-members. All tests run against the real test
// database — no skips.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestIssueHTTPMove(t *testing.T) {
	pool := newTestPool(t)
	e := testIssueServer(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("issue-move-http"), "test-agent", uniqueIP())
	slug := uniqueSlug("issue-move-http")
	createWorkspaceHTTP(t, e, cookie, "Issue Move Co", slug)
	srcIdent := uniqueProjectIdentifier("MV")
	dstIdent := uniqueProjectIdentifier("MV")
	createProjectHTTP(t, e, cookie, slug, "Source", srcIdent)
	createProjectHTTP(t, e, cookie, slug, "Target", dstIdent)
	srcBase := "/api/v1/workspaces/" + slug + "/projects/" + srcIdent + "/issues"

	rec := postAuthedJSON(t, e, http.MethodPost, srcBase, cookie, `{"name":"Movable","priority":2}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create issue: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID        string `json:"id"`
		DisplayID string `json:"display_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}

	// Move → 200 with the full report shape.
	rec = postAuthedJSON(t, e, http.MethodPost, srcBase+"/"+created.ID+"/move", cookie,
		`{"target_project_identifier":"`+dstIdent+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("move issue: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var res struct {
		Issue struct {
			ID         string `json:"id"`
			DisplayID  string `json:"display_id"`
			ProjectID  string `json:"project_id"`
			StateID    string `json:"state_id"`
			SequenceID int    `json:"sequence_id"`
		} `json:"issue"`
		NewDisplayID       string   `json:"new_display_id"`
		StateRemapped      bool     `json:"state_remapped"`
		LabelsDropped      []string `json:"labels_dropped"`
		EstimateDropped    bool     `json:"estimate_dropped"`
		MembershipsDropped struct {
			Cycles   int `json:"cycles"`
			Modules  int `json:"modules"`
			Releases int `json:"releases"`
		} `json:"memberships_dropped"`
		CustomValuesDropped int `json:"custom_values_dropped"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode move result: %v", err)
	}
	if res.Issue.ID != created.ID {
		t.Fatalf("issue.id = %q, want %q (UUID preserved)", res.Issue.ID, created.ID)
	}
	if res.NewDisplayID != dstIdent+"-1" || res.Issue.DisplayID != dstIdent+"-1" {
		t.Fatalf("new_display_id = %q / issue.display_id = %q, want %q", res.NewDisplayID, res.Issue.DisplayID, dstIdent+"-1")
	}
	if res.LabelsDropped == nil {
		t.Fatal("labels_dropped must serialize as [] not null")
	}

	// The moved issue is gone from the source project…
	rec = postAuthedJSON(t, e, http.MethodGet, srcBase+"/"+created.ID, cookie, ``)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get moved issue in source: status = %d, want 404", rec.Code)
	}
	// …and readable in the target project.
	rec = postAuthedJSON(t, e, http.MethodGet,
		"/api/v1/workspaces/"+slug+"/projects/"+dstIdent+"/issues/"+created.ID, cookie, ``)
	if rec.Code != http.StatusOK {
		t.Fatalf("get moved issue in target: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestIssueHTTPMoveErrors(t *testing.T) {
	pool := newTestPool(t)
	e := testIssueServer(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("issue-move-err"), "test-agent", uniqueIP())
	slug := uniqueSlug("issue-move-err")
	createWorkspaceHTTP(t, e, cookie, "Issue Move Err Co", slug)
	srcIdent := uniqueProjectIdentifier("ME")
	dstIdent := uniqueProjectIdentifier("ME")
	createProjectHTTP(t, e, cookie, slug, "Source", srcIdent)
	createProjectHTTP(t, e, cookie, slug, "Target", dstIdent)
	srcBase := "/api/v1/workspaces/" + slug + "/projects/" + srcIdent + "/issues"
	moveURL := func(id string) string { return srcBase + "/" + id + "/move" }

	rec := postAuthedJSON(t, e, http.MethodPost, srcBase, cookie, `{"name":"Err case"}`)
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}

	// Unknown issue → 404.
	rec = postAuthedJSON(t, e, http.MethodPost, moveURL("00000000-0000-0000-0000-000000000000"), cookie,
		`{"target_project_identifier":"`+dstIdent+`"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown issue: status = %d, want 404", rec.Code)
	}
	// Same project → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, moveURL(created.ID), cookie,
		`{"target_project_identifier":"`+srcIdent+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("same project: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	// Unknown target project → 404.
	rec = postAuthedJSON(t, e, http.MethodPost, moveURL(created.ID), cookie,
		`{"target_project_identifier":"NOPE404"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown target: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	// Missing target identifier → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, moveURL(created.ID), cookie, `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing target: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	// Malformed target identifier → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, moveURL(created.ID), cookie,
		`{"target_project_identifier":"bad ident!"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed target: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	// Intake issue → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, srcBase, cookie, `{"name":"Intake item","intake":true}`)
	var intakeCreated struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &intakeCreated); err != nil {
		t.Fatalf("decode intake created: %v", err)
	}
	rec = postAuthedJSON(t, e, http.MethodPost, moveURL(intakeCreated.ID), cookie,
		`{"target_project_identifier":"`+dstIdent+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("intake issue: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	// Guest (role 5 < 15) → 403.
	guestCookie := loginTestUser(t, e, pool, uniqueEmail("issue-move-guest"), "test-agent", uniqueIP())
	guestID := authedUserID(t, e, guestCookie)
	rec = postAuthedJSON(t, e, http.MethodPost, "/api/v1/workspaces/"+slug+"/members", cookie,
		fmt.Sprintf(`{"user_id":%q,"role":5}`, guestID))
	if rec.Code != http.StatusOK {
		t.Fatalf("add guest: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPost, moveURL(created.ID), guestCookie,
		`{"target_project_identifier":"`+dstIdent+`"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	// Non-member → 404 (tenancy boundary).
	outsiderCookie := loginTestUser(t, e, pool, uniqueEmail("issue-move-outsider"), "test-agent", uniqueIP())
	rec = postAuthedJSON(t, e, http.MethodPost, moveURL(created.ID), outsiderCookie,
		`{"target_project_identifier":"`+dstIdent+`"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("non-member: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
}
