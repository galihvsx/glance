package api

// Automation HTTP endpoint tests (C11T1): rule CRUD through the
// project-nested routes (200 list / 201 create / 200 patch / 204
// delete), 400 on bad trigger/action, 404 on unknown rule, 403 for
// guests on read and write, and the spec §5 error envelope. Real test
// database, no skips.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"glance/internal/service"
)

func TestAutomationHTTP(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterAutomationRoutes(e, &AutomationHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("auto-http"), "test-agent", uniqueIP())
	slug := uniqueSlug("auto-http")
	createWorkspaceHTTP(t, e, cookie, "Auto Co", slug)
	ident := uniqueProjectIdentifier("AH")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	autoBase := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/automations"

	// List → empty wrapped shape.
	rec := getAuthed(t, e, http.MethodGet, autoBase, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var listed struct {
		Automations []struct {
			ID string `json:"id"`
		} `json:"automations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Automations) != 0 {
		t.Fatalf("automations = %d, want 0", len(listed.Automations))
	}

	// Create → 201 with the rule.
	rec = postAuthedJSON(t, e, http.MethodPost, autoBase, cookie,
		`{"name":"on start","trigger":{"type":"issue.state_changed","to_states":null,"from_states":null},"actions":[{"type":"add_comment","body":"hello"}]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if created.Name != "on start" || !created.Enabled || created.ID == "" {
		t.Fatalf("created = %+v, want on start/enabled/id", created)
	}

	// Bad trigger type → 400 envelope.
	rec = postAuthedJSON(t, e, http.MethodPost, autoBase, cookie,
		`{"name":"bad","trigger":{"type":"issue.created"},"actions":[{"type":"add_comment","body":"x"}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad trigger: status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"code":"bad_request"`) {
		t.Fatalf("400 envelope: body=%s", rec.Body.String())
	}

	// Unknown action type → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, autoBase, cookie,
		`{"name":"bad","trigger":{"type":"issue.state_changed"},"actions":[{"type":"teleport"}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad action: status = %d, want 400", rec.Code)
	}

	// PATCH disable → 200.
	rec = postAuthedJSON(t, e, http.MethodPatch, autoBase+"/"+created.ID, cookie, `{"enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var patched struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &patched); err != nil {
		t.Fatalf("decode patched: %v", err)
	}
	if patched.Enabled {
		t.Fatalf("patched enabled = true, want false")
	}

	// PATCH unknown rule → 404 envelope.
	rec = postAuthedJSON(t, e, http.MethodPatch, autoBase+"/00000000-0000-0000-0000-000000000000", cookie, `{"enabled":true}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("patch missing: status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"code":"not_found"`) {
		t.Fatalf("404 envelope: body=%s", rec.Body.String())
	}

	// Malformed rule id → 400 (never reaches a ::uuid cast).
	rec = postAuthedJSON(t, e, http.MethodPatch, autoBase+"/nope", cookie, `{"enabled":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("patch malformed id: status = %d, want 400", rec.Code)
	}

	// DELETE → 204; second delete → 404.
	rec = postAuthedJSON(t, e, http.MethodDelete, autoBase+"/"+created.ID, cookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d, want 204", rec.Code)
	}
	rec = postAuthedJSON(t, e, http.MethodDelete, autoBase+"/"+created.ID, cookie, ``)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete missing: status = %d, want 404", rec.Code)
	}
}

func TestAutomationHTTPGuestForbidden(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterAutomationRoutes(e, &AutomationHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("auto-gf"), "test-agent", uniqueIP())
	slug := uniqueSlug("auto-gf")
	createWorkspaceHTTP(t, e, cookie, "Auto Co", slug)
	ident := uniqueProjectIdentifier("AG")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	autoBase := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/automations"

	// Second user joins as guest (role 5).
	guestEmail := uniqueEmail("auto-gf-guest")
	guestCookie := loginTestUser(t, e, pool, guestEmail, "test-agent", uniqueIP())
	var adminID, guestID string
	ctx := context.Background()
	if err := pool.QueryRow(ctx,
		`SELECT m.user_id::text FROM workspace_members m
		 JOIN workspaces w ON w.id = m.workspace_id
		 WHERE w.slug = $1 AND m.role = 20`, slug).Scan(&adminID); err != nil {
		t.Fatalf("admin id: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM users WHERE email = $1`, guestEmail).Scan(&guestID); err != nil {
		t.Fatalf("guest id: %v", err)
	}
	if err := service.UpsertMember(ctx, pool, slug, adminID, guestID, service.RoleGuest); err != nil {
		t.Fatalf("UpsertMember guest: %v", err)
	}

	// Guest read → 403 (acceptance: member-scoped read).
	rec := getAuthed(t, e, http.MethodGet, autoBase, guestCookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest list: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	// Guest write → 403 (acceptance: non-maintainer write 403).
	rec = postAuthedJSON(t, e, http.MethodPost, autoBase, guestCookie,
		`{"name":"x","trigger":{"type":"issue.state_changed"},"actions":[{"type":"add_comment","body":"x"}]}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest create: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPatch, autoBase+"/00000000-0000-0000-0000-000000000000", guestCookie, `{"enabled":false}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest patch: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodDelete, autoBase+"/00000000-0000-0000-0000-000000000000", guestCookie, ``)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest delete: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
}
