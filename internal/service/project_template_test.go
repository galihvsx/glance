package service

// Project templates tests (C17T1): CRUD authz (admin write / member read /
// guest 403 / outsider 404), payload validation, template application in
// the project-creation transaction, 400 on unknown template id, and
// label reuse. Runs against the real test database — no skips. Reuses the
// harness from workspace_test.go (newTestPool, migrateTestDB,
// createTestUser, createTestWorkspace, uniqueTestEmail, uniqueTestSlug,
// UpsertMember).

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

const fullTemplatePayload = `{
	"states": [
		{"name": "Icebox", "group": "backlog", "color": "#6b7280"},
		{"name": "Doing", "group": "started", "color": "#f59e0b"},
		{"name": "Shipped", "group": "completed", "color": "#22c55e"}
	],
	"labels": [
		{"name": "bug", "color": "#ef4444"},
		{"name": "feature", "color": "#3b82f6"}
	],
	"modules": [
		{"name": "API", "description": "backend surface"}
	],
	"estimate_scale": {
		"name": "Fibonacci",
		"points": [
			{"key": "1", "value": 1, "description": "trivial"},
			{"key": "2", "value": 2}
		]
	}
}`

func createTestProjectTemplate(t *testing.T, pool *pgxpool.Pool, wsSlug, actorID, name, payload string) *ProjectTemplate {
	t.Helper()
	if payload == "" {
		payload = `{}`
	}
	tmpl, err := CreateProjectTemplate(context.Background(), pool, wsSlug, actorID,
		ProjectTemplateInput{Name: name, Description: "d", Payload: json.RawMessage(payload)})
	if err != nil {
		t.Fatalf("CreateProjectTemplate: %v", err)
	}
	return tmpl
}

func TestProjectTemplateCRUD(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	admin := createTestUser(t, pool, uniqueTestEmail("pt-admin"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-pt"), admin)

	// Create.
	tmpl := createTestProjectTemplate(t, pool, ws.Slug, admin, "Web team", fullTemplatePayload)
	if tmpl.ID == "" || tmpl.WorkspaceID != ws.ID || tmpl.Name != "Web team" {
		t.Fatalf("template = %+v, want workspace-scoped 'Web team'", tmpl)
	}
	if tmpl.CreatedBy == nil || *tmpl.CreatedBy != admin {
		t.Fatalf("created_by = %v, want %s", tmpl.CreatedBy, admin)
	}

	// Get.
	got, err := GetProjectTemplate(ctx, pool, ws.Slug, tmpl.ID, admin)
	if err != nil {
		t.Fatalf("GetProjectTemplate: %v", err)
	}
	if got.ID != tmpl.ID || string(got.Payload) == "" {
		t.Fatalf("get = %+v, want full template", got)
	}

	// List.
	list, err := ListProjectTemplates(ctx, pool, ws.Slug, admin)
	if err != nil {
		t.Fatalf("ListProjectTemplates: %v", err)
	}
	if len(list) != 1 || list[0].ID != tmpl.ID {
		t.Fatalf("list = %d templates, want 1", len(list))
	}

	// Duplicate name (case-insensitive) → conflict.
	if _, err := CreateProjectTemplate(ctx, pool, ws.Slug, admin,
		ProjectTemplateInput{Name: "web TEAM", Payload: json.RawMessage(`{}`)}); !errors.Is(err, ErrProjectTemplateConflict) {
		t.Fatalf("duplicate name err = %v, want ErrProjectTemplateConflict", err)
	}

	// Patch name + description.
	patched, err := UpdateProjectTemplate(ctx, pool, ws.Slug, tmpl.ID, admin,
		ProjectTemplatePatch{Name: strPtr("Mobile team"), Description: strPtr("new desc")})
	if err != nil {
		t.Fatalf("UpdateProjectTemplate: %v", err)
	}
	if patched.Name != "Mobile team" || patched.Description != "new desc" {
		t.Fatalf("patched = %+v", patched)
	}

	// Empty patch → nothing to update.
	if _, err := UpdateProjectTemplate(ctx, pool, ws.Slug, tmpl.ID, admin,
		ProjectTemplatePatch{}); !errors.Is(err, ErrNothingToUpdate) {
		t.Fatalf("empty patch err = %v, want ErrNothingToUpdate", err)
	}

	// Delete.
	if err := DeleteProjectTemplate(ctx, pool, ws.Slug, tmpl.ID, admin); err != nil {
		t.Fatalf("DeleteProjectTemplate: %v", err)
	}
	if _, err := GetProjectTemplate(ctx, pool, ws.Slug, tmpl.ID, admin); !errors.Is(err, ErrProjectTemplateNotFound) {
		t.Fatalf("get after delete err = %v, want ErrProjectTemplateNotFound", err)
	}
	// Delete again → not found (idempotent-ish: missing is an error).
	if err := DeleteProjectTemplate(ctx, pool, ws.Slug, tmpl.ID, admin); !errors.Is(err, ErrProjectTemplateNotFound) {
		t.Fatalf("second delete err = %v, want ErrProjectTemplateNotFound", err)
	}
}

func TestProjectTemplateAuthz(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	admin := createTestUser(t, pool, uniqueTestEmail("pt-az-admin"))
	member := createTestUser(t, pool, uniqueTestEmail("pt-az-member"))
	guest := createTestUser(t, pool, uniqueTestEmail("pt-az-guest"))
	outsider := createTestUser(t, pool, uniqueTestEmail("pt-az-out"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-pt-az"), admin)
	addTestMember(t, pool, ws.Slug, admin, member, RoleMember)
	addTestMember(t, pool, ws.Slug, admin, guest, RoleGuest)

	tmpl := createTestProjectTemplate(t, pool, ws.Slug, admin, "Blueprint", fullTemplatePayload)

	// Member reads fine.
	if _, err := ListProjectTemplates(ctx, pool, ws.Slug, member); err != nil {
		t.Fatalf("member list: %v", err)
	}
	if _, err := GetProjectTemplate(ctx, pool, ws.Slug, tmpl.ID, member); err != nil {
		t.Fatalf("member get: %v", err)
	}

	// Member writes → forbidden.
	in := ProjectTemplateInput{Name: "x", Payload: json.RawMessage(`{}`)}
	if _, err := CreateProjectTemplate(ctx, pool, ws.Slug, member, in); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member create err = %v, want ErrForbidden", err)
	}
	if _, err := UpdateProjectTemplate(ctx, pool, ws.Slug, tmpl.ID, member,
		ProjectTemplatePatch{Name: strPtr("y")}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member patch err = %v, want ErrForbidden", err)
	}
	if err := DeleteProjectTemplate(ctx, pool, ws.Slug, tmpl.ID, member); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member delete err = %v, want ErrForbidden", err)
	}

	// Guest reads → forbidden.
	if _, err := ListProjectTemplates(ctx, pool, ws.Slug, guest); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest list err = %v, want ErrForbidden", err)
	}
	if _, err := GetProjectTemplate(ctx, pool, ws.Slug, tmpl.ID, guest); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest get err = %v, want ErrForbidden", err)
	}
	// Guest writes → forbidden too (not 404: the workspace exists).
	if _, err := CreateProjectTemplate(ctx, pool, ws.Slug, guest, in); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest create err = %v, want ErrForbidden", err)
	}

	// Outsider → workspace not found on every path.
	if _, err := ListProjectTemplates(ctx, pool, ws.Slug, outsider); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider list err = %v, want ErrNotFound", err)
	}
	if _, err := CreateProjectTemplate(ctx, pool, ws.Slug, outsider, in); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider create err = %v, want ErrNotFound", err)
	}
	// Bad slug → not found as well.
	if _, err := ListProjectTemplates(ctx, pool, "no-such-ws", admin); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bad slug list err = %v, want ErrNotFound", err)
	}
}

func TestProjectTemplatePayloadValidation(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	admin := createTestUser(t, pool, uniqueTestEmail("pt-val-admin"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-pt-val"), admin)

	cases := map[string]string{
		"malformed json":      `{"states": [}`,
		"unknown top field":   `{"bogus": 1}`,
		"unknown state field": `{"states": [{"name": "A", "group": "backlog", "nope": 1}]}`,
		"empty state name":    `{"states": [{"name": " ", "group": "backlog"}]}`,
		"bad state group":     `{"states": [{"name": "A", "group": "flying"}]}`,
		"bad state color":     `{"states": [{"name": "A", "group": "backlog", "color": "red"}]}`,
		"dup state names":     `{"states": [{"name": "A", "group": "backlog"}, {"name": "a", "group": "started"}]}`,
		"empty label name":    `{"labels": [{"name": ""}]}`,
		"bad label color":     `{"labels": [{"name": "x", "color": "#gggggg"}]}`,
		"dup label names":     `{"labels": [{"name": "x"}, {"name": "X"}]}`,
		"empty module name":   `{"modules": [{"name": " "}]}`,
		"dup module names":    `{"modules": [{"name": "A"}, {"name": "a"}]}`,
		"empty scale name":    `{"estimate_scale": {"name": "", "points": []}}`,
		"empty point key":     `{"estimate_scale": {"name": "S", "points": [{"key": "", "value": 1}]}}`,
		"dup point keys":      `{"estimate_scale": {"name": "S", "points": [{"key": "1", "value": 1}, {"key": "1", "value": 2}]}}`,
	}
	for name, payload := range cases {
		if _, err := CreateProjectTemplate(ctx, pool, ws.Slug, admin,
			ProjectTemplateInput{Name: "v-" + strings.ReplaceAll(name, " ", "-"), Payload: json.RawMessage(payload)}); !errors.Is(err, ErrInvalidProjectTemplate) {
			t.Errorf("%s: err = %v, want ErrInvalidProjectTemplate", name, err)
		}
	}

	// Empty name → name required, not payload-shaped.
	if _, err := CreateProjectTemplate(ctx, pool, ws.Slug, admin,
		ProjectTemplateInput{Name: "  ", Payload: json.RawMessage(`{}`)}); !errors.Is(err, ErrNameRequired) {
		t.Errorf("empty name err = %v, want ErrNameRequired", err)
	}

	// Minimal/empty payloads are fine.
	for _, payload := range []string{``, `{}`, `{"labels": []}`} {
		if _, err := CreateProjectTemplate(ctx, pool, ws.Slug, admin,
			ProjectTemplateInput{Name: "ok-" + uniqueTestIdentifier(), Payload: json.RawMessage(payload)}); err != nil {
			t.Errorf("payload %q: err = %v, want nil", payload, err)
		}
	}

	// Patch with a bad payload → 400-shaped.
	tmpl := createTestProjectTemplate(t, pool, ws.Slug, admin, "patchme", `{}`)
	if _, err := UpdateProjectTemplate(ctx, pool, ws.Slug, tmpl.ID, admin,
		ProjectTemplatePatch{Payload: json.RawMessage(`{"states": [{"name": "A", "group": "nope"}]}`)}); !errors.Is(err, ErrInvalidProjectTemplate) {
		t.Errorf("patch bad payload err = %v, want ErrInvalidProjectTemplate", err)
	}
}

func TestCreateProjectWithTemplate(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	admin := createTestUser(t, pool, uniqueTestEmail("pt-apply-admin"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-pt-apply"), admin)
	tmpl := createTestProjectTemplate(t, pool, ws.Slug, admin, "Full", fullTemplatePayload)

	p, err := CreateProjectWithTemplate(ctx, pool, ws.Slug, admin, "Templated", uniqueTestIdentifier(), &tmpl.ID)
	if err != nil {
		t.Fatalf("CreateProjectWithTemplate: %v", err)
	}

	// States: the template's, not the defaults.
	states, err := ListStates(ctx, pool, ws.Slug, p.Identifier, admin)
	if err != nil {
		t.Fatalf("ListStates: %v", err)
	}
	if len(states) != 3 {
		t.Fatalf("states = %d, want 3 template states", len(states))
	}
	wantStates := []struct{ name, group string }{
		{"Icebox", "backlog"}, {"Doing", "started"}, {"Shipped", "completed"},
	}
	for i, w := range wantStates {
		if states[i].Name != w.name || states[i].Group != w.group {
			t.Fatalf("state[%d] = %s/%s, want %s/%s", i, states[i].Name, states[i].Group, w.name, w.group)
		}
	}

	// Labels: workspace-scoped, both created.
	labels, err := ListLabels(ctx, pool, ws.Slug, p.Identifier, admin)
	if err != nil {
		t.Fatalf("ListLabels: %v", err)
	}
	labelNames := map[string]string{}
	for _, l := range labels {
		labelNames[l.Name] = l.Color
	}
	if labelNames["bug"] != "#ef4444" || labelNames["feature"] != "#3b82f6" {
		t.Fatalf("labels = %v, want bug+feature with template colors", labelNames)
	}

	// Modules.
	modules, err := ListModules(ctx, pool, ws.Slug, p.Identifier, admin)
	if err != nil {
		t.Fatalf("ListModules: %v", err)
	}
	if len(modules) != 1 || modules[0].Name != "API" {
		t.Fatalf("modules = %+v, want [API]", modules)
	}

	// Estimate scale + points.
	estimates, err := ListEstimates(ctx, pool, ws.Slug, p.Identifier, admin)
	if err != nil {
		t.Fatalf("ListEstimates: %v", err)
	}
	if len(estimates) != 1 || estimates[0].Name != "Fibonacci" {
		t.Fatalf("estimates = %+v, want [Fibonacci]", estimates)
	}
	if len(estimates[0].Points) != 2 {
		t.Fatalf("points = %d, want 2", len(estimates[0].Points))
	}
}

func TestCreateProjectWithTemplateUnknownID(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	admin := createTestUser(t, pool, uniqueTestEmail("pt-unk-admin"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-pt-unk"), admin)

	bogus := "00000000-0000-0000-0000-000000000000"
	if _, err := CreateProjectWithTemplate(ctx, pool, ws.Slug, admin, "Nope", uniqueTestIdentifier(), &bogus); !errors.Is(err, ErrUnknownTemplateID) {
		t.Fatalf("err = %v, want ErrUnknownTemplateID", err)
	}

	// Cross-workspace template id is also unknown here.
	other := createTestUser(t, pool, uniqueTestEmail("pt-unk-other"))
	ws2 := createTestWorkspace(t, pool, "Other", uniqueTestSlug("acme-pt-unk2"), other)
	foreign := createTestProjectTemplate(t, pool, ws2.Slug, other, "Foreign", `{}`)
	if _, err := CreateProjectWithTemplate(ctx, pool, ws.Slug, admin, "Nope2", uniqueTestIdentifier(), &foreign.ID); !errors.Is(err, ErrUnknownTemplateID) {
		t.Fatalf("foreign template err = %v, want ErrUnknownTemplateID", err)
	}

	// The failed attempts left no project rows behind (all-or-nothing tx).
	var n int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM projects WHERE workspace_id = $1::uuid`, ws.ID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("projects = %d after failed template applies, want 0", n)
	}
}

func TestCreateProjectWithTemplateReusesExistingLabels(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	admin := createTestUser(t, pool, uniqueTestEmail("pt-reuse-admin"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-pt-reuse"), admin)

	// Pre-existing "bug" label in the workspace; the template must reuse
	// it instead of failing on the UNIQUE(workspace_id, name) constraint.
	p0 := createTestProject(t, pool, ws.Slug, admin, "Seed", uniqueTestIdentifier())
	if _, err := CreateLabel(ctx, pool, ws.Slug, p0.Identifier, admin, LabelInput{Name: "bug", Color: strPtr("#000000")}); err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}

	tmpl := createTestProjectTemplate(t, pool, ws.Slug, admin, "Reuse", fullTemplatePayload)
	p, err := CreateProjectWithTemplate(ctx, pool, ws.Slug, admin, "Reused", uniqueTestIdentifier(), &tmpl.ID)
	if err != nil {
		t.Fatalf("CreateProjectWithTemplate: %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM labels WHERE workspace_id = $1::uuid AND name = 'bug'`, ws.ID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("bug labels = %d, want 1 (reused, not duplicated)", n)
	}
	// The existing label keeps its original color — the template does not
	// overwrite it.
	var color string
	if err := pool.QueryRow(ctx,
		`SELECT color FROM labels WHERE workspace_id = $1::uuid AND name = 'bug'`, ws.ID).Scan(&color); err != nil {
		t.Fatalf("color: %v", err)
	}
	if color != "#000000" {
		t.Fatalf("bug color = %s, want original #000000", color)
	}

	// Template states still replaced the defaults.
	states, err := ListStates(ctx, pool, ws.Slug, p.Identifier, admin)
	if err != nil {
		t.Fatalf("ListStates: %v", err)
	}
	if len(states) != 3 {
		t.Fatalf("states = %d, want 3 template states", len(states))
	}
}

func TestCreateProjectWithoutTemplateUnchanged(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	admin := createTestUser(t, pool, uniqueTestEmail("pt-plain-admin"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-pt-plain"), admin)

	// nil template → exactly the old behavior: default states, no labels,
	// no modules, no estimates.
	p, err := CreateProjectWithTemplate(ctx, pool, ws.Slug, admin, "Plain", uniqueTestIdentifier(), nil)
	if err != nil {
		t.Fatalf("CreateProjectWithTemplate: %v", err)
	}
	states, err := ListStates(ctx, pool, ws.Slug, p.Identifier, admin)
	if err != nil {
		t.Fatalf("ListStates: %v", err)
	}
	if len(states) != len(wantDefaultStates) {
		t.Fatalf("states = %d, want %d defaults", len(states), len(wantDefaultStates))
	}
	labels, _ := ListLabels(ctx, pool, ws.Slug, p.Identifier, admin)
	modules, _ := ListModules(ctx, pool, ws.Slug, p.Identifier, admin)
	estimates, _ := ListEstimates(ctx, pool, ws.Slug, p.Identifier, admin)
	if len(labels) != 0 || len(modules) != 0 || len(estimates) != 0 {
		t.Fatalf("labels=%d modules=%d estimates=%d, want all 0", len(labels), len(modules), len(estimates))
	}
}

func TestCreateProjectWithLabelsOnlyTemplate(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	admin := createTestUser(t, pool, uniqueTestEmail("pt-lo-admin"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-pt-lo"), admin)

	// A template with no states keeps the default seed but still applies
	// labels/modules/estimates.
	tmpl := createTestProjectTemplate(t, pool, ws.Slug, admin, "LabelsOnly",
		`{"labels": [{"name": "docs", "color": "#10b981"}], "modules": [{"name": "Guide"}]}`)
	p, err := CreateProjectWithTemplate(ctx, pool, ws.Slug, admin, "LO", uniqueTestIdentifier(), &tmpl.ID)
	if err != nil {
		t.Fatalf("CreateProjectWithTemplate: %v", err)
	}
	states, err := ListStates(ctx, pool, ws.Slug, p.Identifier, admin)
	if err != nil {
		t.Fatalf("ListStates: %v", err)
	}
	if len(states) != len(wantDefaultStates) {
		t.Fatalf("states = %d, want %d defaults", len(states), len(wantDefaultStates))
	}
	labels, err := ListLabels(ctx, pool, ws.Slug, p.Identifier, admin)
	if err != nil {
		t.Fatalf("ListLabels: %v", err)
	}
	if len(labels) != 1 || labels[0].Name != "docs" {
		t.Fatalf("labels = %+v, want [docs]", labels)
	}
	modules, err := ListModules(ctx, pool, ws.Slug, p.Identifier, admin)
	if err != nil {
		t.Fatalf("ListModules: %v", err)
	}
	if len(modules) != 1 || modules[0].Name != "Guide" {
		t.Fatalf("modules = %+v, want [Guide]", modules)
	}
}
