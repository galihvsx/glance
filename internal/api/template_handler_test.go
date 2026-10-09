package api

// Issue template HTTP endpoint tests (C7T0): full CRUD through the
// routes, duplicate-name 409 envelope, guest read 200 / guest mutate 403,
// apply happy path (201 with the created issue, labels attached), and a
// stale label → 404 with the honest envelope. Real test database, no
// skips.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestTemplateHTTP(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterTaxonomyRoutes(e, &IssueHandler{Pool: pool})
	RegisterTemplateRoutes(e, &IssueHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("template-http"), "test-agent/1.0", uniqueIP())
	guestCookie := loginTestUser(t, e, pool, uniqueEmail("template-http-guest"), "test-agent/1.0", uniqueIP())
	slug := uniqueSlug("template-http")
	createWorkspaceHTTP(t, e, cookie, "Template Co", slug)
	ident := uniqueProjectIdentifier("TH")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	projBase := "/api/v1/workspaces/" + slug + "/projects/" + ident
	tmplBase := projBase + "/templates"

	// Admin adds the guest (role 5).
	guestID := authedUserID(t, e, guestCookie)
	rec := postAuthedJSON(t, e, http.MethodPost, "/api/v1/workspaces/"+slug+"/members", cookie,
		fmt.Sprintf(`{"user_id":%q,"role":5}`, guestID))
	if rec.Code != http.StatusOK {
		t.Fatalf("add guest: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	// A label to reference from template_data.
	rec = postAuthedJSON(t, e, http.MethodPost, projBase+"/labels", cookie, `{"name":"http-bug"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create label: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var label struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &label); err != nil {
		t.Fatalf("decode label: %v", err)
	}

	// Create → 201.
	rec = postAuthedJSON(t, e, http.MethodPost, tmplBase, cookie,
		`{"name":"Bug report","description":"Standard form","template_data":{"name":"New bug","priority":2,"label_ids":["`+label.ID+`"]}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create template: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		Description  string `json:"description"`
		TemplateData struct {
			Name     *string  `json:"name"`
			Priority *int     `json:"priority"`
			LabelIDs []string `json:"label_ids"`
		} `json:"template_data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode template: %v", err)
	}
	if created.Name != "Bug report" || created.Description != "Standard form" {
		t.Fatalf("created = %+v", created)
	}
	if created.TemplateData.Name == nil || *created.TemplateData.Name != "New bug" ||
		created.TemplateData.Priority == nil || *created.TemplateData.Priority != 2 ||
		len(created.TemplateData.LabelIDs) != 1 {
		t.Fatalf("template_data = %+v", created.TemplateData)
	}
	tmplURL := tmplBase + "/" + created.ID

	// Duplicate name → 409 envelope.
	rec = postAuthedJSON(t, e, http.MethodPost, tmplBase, cookie, `{"name":"Bug report"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate template: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"conflict"`) {
		t.Fatalf("409 envelope: body=%s", rec.Body.String())
	}

	// Bad template_data → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, tmplBase, cookie, `{"name":"Bad","template_data":{"priority":9}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad template_data: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// List → 200 with the template; guest reads fine too.
	rec = getAuthed(t, e, http.MethodGet, tmplBase, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list templates: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var listed struct {
		Templates []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"templates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Templates) != 1 || listed.Templates[0].Name != "Bug report" {
		t.Fatalf("listed = %+v", listed)
	}
	rec = getAuthed(t, e, http.MethodGet, tmplBase, guestCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("guest list: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, tmplURL, guestCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("guest get: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	// Guest mutations → 403 across the board.
	rec = postAuthedJSON(t, e, http.MethodPost, tmplBase, guestCookie, `{"name":"Guest one"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest create: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPatch, tmplURL, guestCookie, `{"name":"Guest edit"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest patch: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodDelete, tmplURL, guestCookie, ``)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest delete: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPost, tmplURL+"/apply", guestCookie, `{}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest apply: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}

	// Malformed id → 400.
	rec = getAuthed(t, e, http.MethodGet, tmplBase+"/nope", cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed id: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// PATCH: rename + replace template_data wholesale (label kept).
	rec = postAuthedJSON(t, e, http.MethodPatch, tmplURL, cookie,
		`{"name":"Bug report v2","template_data":{"priority":3,"label_ids":["`+label.ID+`"]}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch template: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var patched struct {
		Name         string `json:"name"`
		TemplateData struct {
			Name     *string  `json:"name"`
			Priority *int     `json:"priority"`
			LabelIDs []string `json:"label_ids"`
		} `json:"template_data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &patched); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if patched.Name != "Bug report v2" || patched.TemplateData.Name != nil ||
		patched.TemplateData.Priority == nil || *patched.TemplateData.Priority != 3 ||
		len(patched.TemplateData.LabelIDs) != 1 || patched.TemplateData.LabelIDs[0] != label.ID {
		t.Fatalf("patched = %+v", patched)
	}

	// PATCH {} → 400 nothing to update.
	rec = postAuthedJSON(t, e, http.MethodPatch, tmplURL, cookie, `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty patch: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// Apply → 201 with the created issue: template defaults + label.
	rec = postAuthedJSON(t, e, http.MethodPost, tmplURL+"/apply", cookie, `{}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("apply template: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var applied struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Priority  int    `json:"priority"`
		DisplayID string `json:"display_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &applied); err != nil {
		t.Fatalf("decode applied issue: %v", err)
	}
	if applied.Name != "Bug report v2" || applied.Priority != 3 || applied.DisplayID == "" {
		t.Fatalf("applied = %+v", applied)
	}
	// The label rode along: the issue detail lists it.
	rec = getAuthed(t, e, http.MethodGet, projBase+"/issues/"+applied.ID, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get applied issue: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), label.ID) {
		t.Fatalf("applied issue missing label %s: %s", label.ID, rec.Body.String())
	}

	// Apply with overrides → the override wins.
	rec = postAuthedJSON(t, e, http.MethodPost, tmplURL+"/apply", cookie,
		`{"name":"Override title","priority":0,"label_ids":[]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("apply with overrides: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var applied2 struct {
		Name     string `json:"name"`
		Priority int    `json:"priority"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &applied2); err != nil {
		t.Fatalf("decode applied2: %v", err)
	}
	if applied2.Name != "Override title" || applied2.Priority != 0 {
		t.Fatalf("applied2 = %+v", applied2)
	}

	// Apply with a stale label override → 404 honest envelope.
	rec = postAuthedJSON(t, e, http.MethodPost, tmplURL+"/apply", cookie,
		`{"label_ids":["11111111-2222-3333-4444-555555555555"]}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("stale label apply: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"not_found"`) ||
		!strings.Contains(rec.Body.String(), "missing label") {
		t.Fatalf("stale envelope: body=%s", rec.Body.String())
	}

	// Delete → 204; the template is gone.
	rec = postAuthedJSON(t, e, http.MethodDelete, tmplURL, cookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete template: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, tmplURL, cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
}
