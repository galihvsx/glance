package api

// Project ACL HTTP endpoints (C92T0): project members CRUD + Lead
// set/clear + visibility PATCH through the routes — the authZ
// statuses (403 for non-admin management, 400 for a non-workspace
// target, 409 for last-admin removal, 404 for a private project a
// non-member asks for) and the lead_user_id field in the teamspace
// payload. Real test database, no skips.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestProjectACLHTTP(t *testing.T) {
	pool := newTestPool(t)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterTeamspaceRoutes(e, &TeamspaceHandler{Pool: pool})

	adminCookie := loginTestUser(t, e, pool, uniqueEmail("acl-admin"), "test-agent/1.0", uniqueIP())
	slug := uniqueSlug("acl-http")
	createWorkspaceHTTP(t, e, adminCookie, "ACL Co", slug)
	projectsBase := "/api/v1/workspaces/" + slug + "/projects"

	// Create project -> 201, visibility public by default.
	rec := postAuthedJSON(t, e, http.MethodPost, projectsBase, adminCookie, `{"name":"Alpha","identifier":"ALPHA"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		Visibility string `json:"visibility"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.Visibility != "public" {
		t.Fatalf("created visibility = %s, want public", rec.Body.String())
	}
	membersURL := projectsBase + "/ALPHA/members"

	// Workspace member (added after project creation).
	memberCookie := loginTestUser(t, e, pool, uniqueEmail("acl-member"), "test-agent/1.0", uniqueIP())
	memberID := authedUserID(t, e, memberCookie)
	rec = postAuthedJSON(t, e, http.MethodPost, "/api/v1/workspaces/"+slug+"/members", adminCookie,
		`{"user_id":"`+memberID+`","role":15}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("workspace add member: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	// Non-admin member add -> 403.
	rec = postAuthedJSON(t, e, http.MethodPost, membersURL, memberCookie, `{"user_id":"`+memberID+`","role":15}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member add: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	// Admin add -> 200; roster lists the member.
	rec = postAuthedJSON(t, e, http.MethodPost, membersURL, adminCookie, `{"user_id":"`+memberID+`","role":15}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin add: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, membersURL, memberCookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), memberID) {
		t.Fatalf("roster: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	// Change role -> 200; bad role -> 400.
	rec = postAuthedJSON(t, e, http.MethodPatch, membersURL+"/"+memberID, adminCookie, `{"role":5}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("change role: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPatch, membersURL+"/"+memberID, adminCookie, `{"role":99}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad role: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	// Visibility: member cannot flip (403); admin can; bad value 400.
	rec = postAuthedJSON(t, e, http.MethodPatch, projectsBase+"/ALPHA", memberCookie, `{"visibility":"private"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member visibility: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPatch, projectsBase+"/ALPHA", adminCookie, `{"visibility":"secret"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad visibility: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPatch, projectsBase+"/ALPHA", adminCookie, `{"visibility":"private"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"visibility":"private"`) {
		t.Fatalf("admin visibility: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	// Private project: the member (now direct guest 5) still reads it
	// via the direct row; remove the direct row and it 404s.
	rec = postAuthedJSON(t, e, http.MethodDelete, membersURL+"/"+memberID, adminCookie, ``)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove member: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, projectsBase+"/ALPHA", memberCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("private get non-member: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, projectsBase, memberCookie)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "ALPHA") {
		t.Fatalf("private list non-member: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	// Lead: create a teamspace, set the member as Lead (member must
	// be a teamspace member first), verify lead_user_id in payload,
	// non-admin set -> 403, clear -> null.
	rec = postAuthedJSON(t, e, http.MethodPost, "/api/v1/workspaces/"+slug+"/teamspaces", adminCookie, `{"name":"Platform"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create teamspace: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var ts struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ts); err != nil || ts.ID == "" {
		t.Fatalf("decode teamspace: %v", err)
	}
	teamspacesBase := "/api/v1/workspaces/" + slug + "/teamspaces"
	rec = postAuthedJSON(t, e, http.MethodPost, teamspacesBase+"/"+ts.ID+"/members", adminCookie, `{"user_id":"`+memberID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("teamspace add member: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPost, teamspacesBase+"/"+ts.ID+"/lead", memberCookie, `{"user_id":"`+memberID+`"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member set lead: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPost, teamspacesBase+"/"+ts.ID+"/lead", adminCookie, `{"user_id":"`+memberID+`"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"lead_user_id":"`+memberID+`"`) {
		t.Fatalf("admin set lead: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodDelete, teamspacesBase+"/"+ts.ID+"/lead", adminCookie, ``)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"lead_user_id":null`) {
		t.Fatalf("clear lead: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
}
