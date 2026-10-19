package api

// Parallel cycles HTTP tests (C78T0): the parallel_cycles toggle on the
// project PATCH path (member 15+ gate, payload echo) and the 409 overlap
// mapping on cycle create/update. Real test database, no skips.

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestParallelCyclesToggleHTTP pins the toggle on the existing project
// PATCH path: member flips it, the payload echoes it, a guest (5) gets
// 403.
func TestParallelCyclesToggleHTTP(t *testing.T) {
	pool := newTestPool(t)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("pc-toggle"), "test-agent", uniqueIP())
	slug := uniqueSlug("pc-toggle-ws")
	createWorkspaceHTTP(t, e, cookie, "Cycle Co", slug)
	ident := uniqueProjectIdentifier("PC")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	projectBase := "/api/v1/workspaces/" + slug + "/projects/" + ident

	// Detail payload exposes the default (false).
	rec := getAuthed(t, e, http.MethodGet, projectBase, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get project: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"parallel_cycles":false`) {
		t.Fatalf("default toggle not exposed: body=%s", rec.Body.String())
	}

	// Member flips it on: 200, echoed in the payload.
	rec = postAuthedJSON(t, e, http.MethodPatch, projectBase, cookie, `{"parallel_cycles":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch toggle on: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"parallel_cycles":true`) {
		t.Fatalf("toggle not echoed: body=%s", rec.Body.String())
	}

	// Guest (5) is read-only: 403, mirroring the existing
	// project-update gate.
	guest := loginTestUser(t, e, pool, uniqueEmail("pc-toggle-guest"), "test-agent", uniqueIP())
	var wsID, guestID string
	if err := pool.QueryRow(t.Context(), `SELECT id::text FROM workspaces WHERE slug = $1`, slug).Scan(&wsID); err != nil {
		t.Fatalf("workspace id: %v", err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT id::text FROM users WHERE email = $1`, guestEmailOf(t, pool, guest)).Scan(&guestID); err != nil {
		t.Fatalf("guest id: %v", err)
	}
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1::uuid, $2::uuid, 5)`, wsID, guestID); err != nil {
		t.Fatalf("add guest: %v", err)
	}
	rec = postAuthedJSON(t, e, http.MethodPatch, projectBase, guest, `{"parallel_cycles":false}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest toggle: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestCycleOverlap409HTTP pins the 409 mapping: with the toggle off, an
// overlapping create names the conflicting cycle in the message; the
// update path behaves the same; with the toggle on the create succeeds.
func TestCycleOverlap409HTTP(t *testing.T) {
	pool := newTestPool(t)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterCycleRoutes(e, &IssueHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("pc-409"), "test-agent", uniqueIP())
	slug := uniqueSlug("pc-409-ws")
	createWorkspaceHTTP(t, e, cookie, "Cycle Co", slug)
	ident := uniqueProjectIdentifier("PO")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	projectBase := "/api/v1/workspaces/" + slug + "/projects/" + ident
	cycleBase := projectBase + "/cycles"

	start := time.Now().AddDate(0, 0, -7).Format("2006-01-02")
	end := time.Now().AddDate(0, 0, 7).Format("2006-01-02")

	rec := postAuthedJSON(t, e, http.MethodPost, cycleBase, cookie,
		`{"name":"Sprint 12","start_date":"`+start+`","end_date":"`+end+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create cycle: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	// Overlapping create with the toggle off → 409, message names the
	// conflicting cycle.
	ostart := time.Now().AddDate(0, 0, -3).Format("2006-01-02")
	oend := time.Now().AddDate(0, 0, 10).Format("2006-01-02")
	rec = postAuthedJSON(t, e, http.MethodPost, cycleBase, cookie,
		`{"name":"Overlap","start_date":"`+ostart+`","end_date":"`+oend+`"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("overlapping create: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"code":"conflict"`) {
		t.Fatalf("409 envelope code: body=%s", body)
	}
	if !strings.Contains(body, `Sprint 12`) {
		t.Fatalf("409 message does not name the conflicting cycle: body=%s", body)
	}

	// Toggle on → the same create succeeds (201).
	rec = postAuthedJSON(t, e, http.MethodPatch, projectBase, cookie, `{"parallel_cycles":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("toggle on: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPost, cycleBase, cookie,
		`{"name":"Overlap","start_date":"`+ostart+`","end_date":"`+oend+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("overlapping create with toggle on: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
}
