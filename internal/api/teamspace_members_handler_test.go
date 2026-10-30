package api

// Teamspace members HTTP endpoints (C90T0): POST/GET/DELETE
// /api/v1/workspaces/{slug}/teamspaces/{teamspaceID}/members through
// the routes — Admin-only add/remove, member/admin reads, the 400 for
// a non-workspace-member target, idempotent re-add/re-remove, and the
// members endpoint answering 404 once the teamspace itself is deleted
// (memberships cascade with it). Real test database, no skips.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestTeamspaceMembersHTTP(t *testing.T) {
	pool := newTestPool(t)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterTeamspaceRoutes(e, &TeamspaceHandler{Pool: pool})

	adminCookie := loginTestUser(t, e, pool, uniqueEmail("tsm-admin"), "test-agent/1.0", uniqueIP())
	slug := uniqueSlug("tsm-http")
	createWorkspaceHTTP(t, e, adminCookie, "Members Co", slug)
	base := "/api/v1/workspaces/" + slug + "/teamspaces"

	// Create → 201, creator auto-member (member_count 1).
	rec := postAuthedJSON(t, e, http.MethodPost, base, adminCookie, `{"name":"Platform"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID          string `json:"id"`
		MemberCount int64  `json:"member_count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.ID == "" || created.MemberCount != 1 {
		t.Fatalf("created = %+v, want id + member_count 1", created)
	}
	membersURL := base + "/" + created.ID + "/members"

	// A workspace member added AFTER the teamspace was created is not
	// yet a teamspace member: the teamspaces list hides it from them.
	memberCookie := loginTestUser(t, e, pool, uniqueEmail("tsm-member"), "test-agent/1.0", uniqueIP())
	memberID := authedUserID(t, e, memberCookie)
	rec = postAuthedJSON(t, e, http.MethodPost, "/api/v1/workspaces/"+slug+"/members", adminCookie,
		`{"user_id":"`+memberID+`","role":15}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("workspace add member: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, base, memberCookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"teamspaces":[]`) {
		t.Fatalf("member teamspace list before add: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	// Non-admin add → 403.
	rec = postAuthedJSON(t, e, http.MethodPost, membersURL, memberCookie, `{"user_id":"`+memberID+`"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member add: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}

	// Admin add → 200 with the member row; duplicate add → still 200.
	rec = postAuthedJSON(t, e, http.MethodPost, membersURL, adminCookie, `{"user_id":"`+memberID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin add: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var added struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &added); err != nil || added.ID != memberID {
		t.Fatalf("added = %s, want id %s", rec.Body.String(), memberID)
	}
	rec = postAuthedJSON(t, e, http.MethodPost, membersURL, adminCookie, `{"user_id":"`+memberID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("duplicate add: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	// Member can now read the roster (2 entries) and sees the
	// teamspace in their list.
	rec = getAuthed(t, e, http.MethodGet, membersURL, memberCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("member list roster: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var roster struct {
		Members []struct {
			ID string `json:"id"`
		} `json:"members"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &roster); err != nil || len(roster.Members) != 2 {
		t.Fatalf("roster = %s, want 2 members", rec.Body.String())
	}

	// A registered user who is NOT a workspace member → honest 400,
	// the same whether or not the id exists at all.
	outsiderCookie := loginTestUser(t, e, pool, uniqueEmail("tsm-outsider"), "test-agent/1.0", uniqueIP())
	outsiderID := authedUserID(t, e, outsiderCookie)
	rec = postAuthedJSON(t, e, http.MethodPost, membersURL, adminCookie, `{"user_id":"`+outsiderID+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("add non-workspace-member: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	// And the outsider (not even a workspace member) gets 404, never
	// the roster.
	rec = getAuthed(t, e, http.MethodGet, membersURL, outsiderCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("outsider list roster: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}

	// Remove → 200; roster back to 1; re-remove → still 200.
	rec = postAuthedJSON(t, e, http.MethodDelete, membersURL+"/"+memberID, adminCookie, ``)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, membersURL, adminCookie)
	if err := json.Unmarshal(rec.Body.Bytes(), &roster); err != nil || len(roster.Members) != 1 {
		t.Fatalf("roster after remove = %s, want 1 member", rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodDelete, membersURL+"/"+memberID, adminCookie, ``)
	if rec.Code != http.StatusOK {
		t.Fatalf("re-remove non-member: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	// Delete the teamspace → its memberships cascade, and the members
	// endpoint now reports the teamspace itself as not found.
	rec = postAuthedJSON(t, e, http.MethodDelete, base+"/"+created.ID, adminCookie, ``)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete teamspace: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, membersURL, adminCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("members after teamspace delete: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
}
