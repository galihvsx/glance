package api

// State + estimate-mutation HTTP endpoint tests (C6T2): state create
// (201 / 409 duplicate / 400 bad group), update (200 / 400 empty), delete
// (204 / 409 in-use / 204 with ?reassign_to moving issues), guest 403s,
// estimate delete (204 / 409 in-use), and add-points (200 / 400 dup).
// Real test database, no skips.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestStateHTTP(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterTaxonomyRoutes(e, &IssueHandler{Pool: pool})
	RegisterStateRoutes(e, &StateHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("state-http"), "test-agent", uniqueIP())

	slug := uniqueSlug("state-http")
	createWorkspaceHTTP(t, e, cookie, "State Co", slug)
	ident := uniqueProjectIdentifier("ST")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	projBase := "/api/v1/workspaces/" + slug + "/projects/" + ident

	// Create state → 201.
	rec := postAuthedJSON(t, e, http.MethodPost, projBase+"/states", cookie,
		`{"name":"Review","group":"started","color":"#a78bfa"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create state: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Group    string `json:"group"`
		Sequence int    `json:"sequence"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if created.Name != "Review" || created.Group != "started" {
		t.Fatalf("state = %+v, want Review/started", created)
	}

	// Duplicate name → 409 envelope.
	rec = postAuthedJSON(t, e, http.MethodPost, projBase+"/states", cookie,
		`{"name":"Review","group":"backlog"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate state: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"conflict"`) {
		t.Fatalf("409 envelope: body=%s", rec.Body.String())
	}

	// Bad group → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, projBase+"/states", cookie,
		`{"name":"Nope","group":"limbo"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad group: status = %d, want 400", rec.Code)
	}

	// Update → 200.
	rec = postAuthedJSON(t, e, http.MethodPatch, projBase+"/states/"+created.ID, cookie,
		`{"name":"In Review","color":"#10b981"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update state: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var updated struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated: %v", err)
	}
	if updated.Name != "In Review" || updated.Color != "#10b981" {
		t.Fatalf("updated = %+v, want In Review/#10b981", updated)
	}

	// Empty patch → 400.
	rec = postAuthedJSON(t, e, http.MethodPatch, projBase+"/states/"+created.ID, cookie, `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty patch: status = %d, want 400", rec.Code)
	}

	// Put an issue in the state, then delete → 409; with ?reassign_to → 204.
	rec = postAuthedJSON(t, e, http.MethodPost, projBase+"/issues", cookie, `{"name":"doomed"}`)
	var iss struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &iss); err != nil {
		t.Fatalf("decode issue: %v", err)
	}
	rec = postAuthedJSON(t, e, http.MethodPatch, projBase+"/issues/"+iss.ID, cookie,
		`{"state_id":"`+created.ID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("move issue: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodDelete, projBase+"/states/"+created.ID, cookie, ``)
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete in-use: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	// Reassign target: pick the seeded Backlog state.
	rec = getAuthed(t, e, http.MethodGet, projBase+"/states", cookie)
	var listed struct {
		States []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"states"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode states: %v", err)
	}
	var backlogID string
	for _, s := range listed.States {
		if s.Name == "Backlog" {
			backlogID = s.ID
		}
	}
	if backlogID == "" {
		t.Fatal("seeded Backlog state not found")
	}
	rec = postAuthedJSON(t, e, http.MethodDelete,
		projBase+"/states/"+created.ID+"?reassign_to="+backlogID, cookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete with reassign: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	// The issue moved.
	rec = getAuthed(t, e, http.MethodGet, projBase+"/issues/"+iss.ID, cookie)
	var detail struct {
		StateID string `json:"state_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode issue detail: %v", err)
	}
	if detail.StateID != backlogID {
		t.Fatalf("issue state_id = %s, want reassigned %s", detail.StateID, backlogID)
	}
}

func TestStateGuestForbiddenHTTP(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterStateRoutes(e, &StateHandler{Pool: pool})

	owner := loginTestUser(t, e, pool, uniqueEmail("state-owner"), "test-agent", uniqueIP())
	slug := uniqueSlug("state-guest")
	createWorkspaceHTTP(t, e, owner, "Guest Co", slug)
	ident := uniqueProjectIdentifier("SG")
	createProjectHTTP(t, e, owner, slug, "Engineering", ident)
	projBase := "/api/v1/workspaces/" + slug + "/projects/" + ident

	// Second user joins as guest (role 5) — direct membership insert.
	guestEmail := uniqueEmail("state-guest")
	guestCookie := loginTestUser(t, e, pool, guestEmail, "test-agent", uniqueIP())
	var guestID, wsID string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM users WHERE email = $1`, guestEmail).Scan(&guestID); err != nil {
		t.Fatalf("guest id: %v", err)
	}
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM workspaces WHERE slug = $1`, slug).Scan(&wsID); err != nil {
		t.Fatalf("workspace id: %v", err)
	}
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1::uuid, $2::uuid, 5)`,
		wsID, guestID); err != nil {
		t.Fatalf("guest membership: %v", err)
	}

	rec := postAuthedJSON(t, e, http.MethodPost, projBase+"/states", guestCookie,
		`{"name":"Sneaky","group":"backlog"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest create state: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestEstimateMutationsHTTP(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterTaxonomyRoutes(e, &IssueHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("est-http"), "test-agent", uniqueIP())
	slug := uniqueSlug("est-http")
	createWorkspaceHTTP(t, e, cookie, "Est Co", slug)
	ident := uniqueProjectIdentifier("ES")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	projBase := "/api/v1/workspaces/" + slug + "/projects/" + ident

	// Create a scale → 201.
	rec := postAuthedJSON(t, e, http.MethodPost, projBase+"/estimates", cookie,
		`{"name":"Fib","points":[{"key":"1","value":1},{"key":"2","value":2}]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create estimate: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var est struct {
		ID     string `json:"id"`
		Points []struct {
			ID  string `json:"id"`
			Key string `json:"key"`
		} `json:"points"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &est); err != nil {
		t.Fatalf("decode estimate: %v", err)
	}

	// Add points → 200, points appended.
	rec = postAuthedJSON(t, e, http.MethodPost, projBase+"/estimates/"+est.ID+"/points", cookie,
		`{"points":[{"key":"3","value":3}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("add points: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var withPoints struct {
		Points []struct {
			Key string `json:"key"`
		} `json:"points"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &withPoints); err != nil {
		t.Fatalf("decode estimate: %v", err)
	}
	if len(withPoints.Points) != 3 {
		t.Fatalf("points = %d, want 3", len(withPoints.Points))
	}

	// Duplicate key → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, projBase+"/estimates/"+est.ID+"/points", cookie,
		`{"points":[{"key":"3","value":5}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("dup point: status = %d, want 400", rec.Code)
	}

	// Point an issue at the scale, then delete → 409.
	rec = postAuthedJSON(t, e, http.MethodPost, projBase+"/issues", cookie, `{"name":"estd"}`)
	var iss struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &iss); err != nil {
		t.Fatalf("decode issue: %v", err)
	}
	rec = postAuthedJSON(t, e, http.MethodPatch, projBase+"/issues/"+iss.ID, cookie,
		`{"estimate_point_id":"`+est.Points[0].ID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("set estimate: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodDelete, projBase+"/estimates/"+est.ID, cookie, ``)
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete in-use scale: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}

	// Unused scale deletes → 204.
	rec = postAuthedJSON(t, e, http.MethodPost, projBase+"/estimates", cookie,
		`{"name":"Unused","points":[{"key":"S","value":1}]}`)
	var est2 struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &est2); err != nil {
		t.Fatalf("decode estimate 2: %v", err)
	}
	rec = postAuthedJSON(t, e, http.MethodDelete, projBase+"/estimates/"+est2.ID, cookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete unused scale: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
}
