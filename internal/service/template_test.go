package service

// Issue templates (C7T0): CRUD + member scoping, apply happy path with
// ref validation, and stale-ref 404s. Real test database, no skips.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func setupTemplateTest(t *testing.T) (context.Context, *pgxpool.Pool, string, string, string) {
	t.Helper()
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	actor := createTestUser(t, pool, uniqueTestEmail("template"))
	slug := uniqueTestSlug("template-ws")
	createTestWorkspace(t, pool, "Template Co", slug, actor)
	ident := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Eng", ident)
	return ctx, pool, slug, ident, actor
}

// addGuestMember adds a role-5 guest to the workspace.
func addGuestMember(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, admin string) string {
	t.Helper()
	guest := createTestUser(t, pool, uniqueTestEmail("template-guest"))
	if err := UpsertMember(ctx, pool, slug, admin, guest, RoleGuest); err != nil {
		t.Fatalf("UpsertMember guest: %v", err)
	}
	return guest
}

func templateDataJSON(t *testing.T, doc string) json.RawMessage {
	t.Helper()
	return json.RawMessage(doc)
}

func TestTemplateCRUD(t *testing.T) {
	ctx, pool, slug, ident, actor := setupTemplateTest(t)

	desc := "Standard bug report"
	tmpl, err := CreateTemplate(ctx, pool, slug, ident, actor, TemplateInput{
		Name:         "Bug report",
		Description:  &desc,
		TemplateData: templateDataJSON(t, `{"name":"New bug","priority":2,"label_ids":[]}`),
	})
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	if tmpl.Name != "Bug report" || tmpl.Description != desc {
		t.Fatalf("got %+v", tmpl)
	}
	if tmpl.TemplateData.Name == nil || *tmpl.TemplateData.Name != "New bug" {
		t.Fatalf("template_data.name = %+v", tmpl.TemplateData)
	}
	if tmpl.TemplateData.Priority == nil || *tmpl.TemplateData.Priority != 2 {
		t.Fatalf("template_data.priority = %+v", tmpl.TemplateData)
	}
	if tmpl.CreatedBy == nil || *tmpl.CreatedBy != actor {
		t.Fatalf("created_by = %+v", tmpl.CreatedBy)
	}

	// Empty template_data → '{}'.
	tmpl2, err := CreateTemplate(ctx, pool, slug, ident, actor, TemplateInput{Name: "Feature request"})
	if err != nil {
		t.Fatalf("CreateTemplate empty data: %v", err)
	}
	if tmpl2.TemplateData.Name != nil || tmpl2.TemplateData.Priority != nil {
		t.Fatalf("empty data = %+v, want zero TemplateData", tmpl2.TemplateData)
	}

	// Duplicate name → conflict.
	if _, err := CreateTemplate(ctx, pool, slug, ident, actor, TemplateInput{Name: "Bug report"}); !errors.Is(err, ErrTemplateConflict) {
		t.Fatalf("dup name: err = %v, want ErrTemplateConflict", err)
	}

	// Empty name → 400.
	if _, err := CreateTemplate(ctx, pool, slug, ident, actor, TemplateInput{Name: "  "}); !errors.Is(err, ErrNameRequired) {
		t.Fatalf("empty name: err = %v, want ErrNameRequired", err)
	}

	// Bad priority in template_data → 400.
	if _, err := CreateTemplate(ctx, pool, slug, ident, actor, TemplateInput{
		Name:         "Bad",
		TemplateData: templateDataJSON(t, `{"priority":9}`),
	}); !errors.Is(err, ErrInvalidTemplate) {
		t.Fatalf("bad priority: err = %v, want ErrInvalidTemplate", err)
	}

	// Garbage template_data → 400.
	if _, err := CreateTemplate(ctx, pool, slug, ident, actor, TemplateInput{
		Name:         "Bad2",
		TemplateData: templateDataJSON(t, `{"priority":`),
	}); !errors.Is(err, ErrInvalidTemplate) {
		t.Fatalf("garbage data: err = %v, want ErrInvalidTemplate", err)
	}

	// List → both templates, oldest first.
	list, err := ListTemplates(ctx, pool, slug, ident, actor)
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	if len(list) != 2 || list[0].ID != tmpl.ID || list[1].ID != tmpl2.ID {
		t.Fatalf("list = %d templates, want 2 in creation order", len(list))
	}

	// Get → the template.
	got, err := GetTemplate(ctx, pool, slug, ident, actor, tmpl.ID)
	if err != nil {
		t.Fatalf("GetTemplate: %v", err)
	}
	if got.Name != "Bug report" {
		t.Fatalf("got name = %q", got.Name)
	}

	// Get unknown id → 404.
	if _, err := GetTemplate(ctx, pool, slug, ident, actor, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrTemplateNotFound) {
		t.Fatalf("unknown id: err = %v, want ErrTemplateNotFound", err)
	}

	// Get malformed id → 400.
	if _, err := GetTemplate(ctx, pool, slug, ident, actor, "nope"); !errors.Is(err, ErrInvalidTemplateID) {
		t.Fatalf("malformed id: err = %v, want ErrInvalidTemplateID", err)
	}

	// PATCH: rename + replace template_data wholesale.
	newName := "Bug report v2"
	patched, err := UpdateTemplate(ctx, pool, slug, ident, actor, tmpl.ID, TemplatePatch{
		Name:         &newName,
		TemplateData: templateDataJSON(t, `{"priority":3}`),
	})
	if err != nil {
		t.Fatalf("UpdateTemplate: %v", err)
	}
	if patched.Name != "Bug report v2" {
		t.Fatalf("patched name = %q", patched.Name)
	}
	if patched.TemplateData.Name != nil {
		t.Fatalf("template_data not replaced: %+v", patched.TemplateData)
	}
	if patched.TemplateData.Priority == nil || *patched.TemplateData.Priority != 3 {
		t.Fatalf("patched priority = %+v", patched.TemplateData.Priority)
	}

	// PATCH to a taken name → conflict.
	if _, err := UpdateTemplate(ctx, pool, slug, ident, actor, tmpl.ID, TemplatePatch{Name: &tmpl2.Name}); !errors.Is(err, ErrTemplateConflict) {
		t.Fatalf("patch dup name: err = %v, want ErrTemplateConflict", err)
	}

	// PATCH with empty name → 400.
	empty := "  "
	if _, err := UpdateTemplate(ctx, pool, slug, ident, actor, tmpl.ID, TemplatePatch{Name: &empty}); !errors.Is(err, ErrNameRequired) {
		t.Fatalf("patch empty name: err = %v, want ErrNameRequired", err)
	}

	// PATCH with nothing → 400.
	if _, err := UpdateTemplate(ctx, pool, slug, ident, actor, tmpl.ID, TemplatePatch{}); !errors.Is(err, ErrNothingToUpdate) {
		t.Fatalf("empty patch: err = %v, want ErrNothingToUpdate", err)
	}

	// PATCH bad template_data → 400.
	if _, err := UpdateTemplate(ctx, pool, slug, ident, actor, tmpl.ID, TemplatePatch{
		TemplateData: templateDataJSON(t, `{"priority":-1}`),
	}); !errors.Is(err, ErrInvalidTemplate) {
		t.Fatalf("patch bad data: err = %v, want ErrInvalidTemplate", err)
	}

	// Delete → gone.
	if err := DeleteTemplate(ctx, pool, slug, ident, actor, tmpl.ID); err != nil {
		t.Fatalf("DeleteTemplate: %v", err)
	}
	if _, err := GetTemplate(ctx, pool, slug, ident, actor, tmpl.ID); !errors.Is(err, ErrTemplateNotFound) {
		t.Fatalf("after delete: err = %v, want ErrTemplateNotFound", err)
	}
	// Delete again → 404.
	if err := DeleteTemplate(ctx, pool, slug, ident, actor, tmpl.ID); !errors.Is(err, ErrTemplateNotFound) {
		t.Fatalf("delete again: err = %v, want ErrTemplateNotFound", err)
	}
}

func TestTemplateScoping(t *testing.T) {
	ctx, pool, slug, ident, admin := setupTemplateTest(t)
	guest := addGuestMember(t, ctx, pool, slug, admin)
	outside := createTestUser(t, pool, uniqueTestEmail("template-outside"))

	tmpl, err := CreateTemplate(ctx, pool, slug, ident, admin, TemplateInput{
		Name:         "Scoped",
		TemplateData: templateDataJSON(t, `{"priority":1}`),
	})
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}

	// Guests may read.
	if _, err := ListTemplates(ctx, pool, slug, ident, guest); err != nil {
		t.Fatalf("guest list: %v", err)
	}
	if _, err := GetTemplate(ctx, pool, slug, ident, guest, tmpl.ID); err != nil {
		t.Fatalf("guest get: %v", err)
	}

	// Guests may not mutate: create, patch, delete, apply.
	if _, err := CreateTemplate(ctx, pool, slug, ident, guest, TemplateInput{Name: "x"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest create: err = %v, want ErrForbidden", err)
	}
	newName := "x"
	if _, err := UpdateTemplate(ctx, pool, slug, ident, guest, tmpl.ID, TemplatePatch{Name: &newName}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest patch: err = %v, want ErrForbidden", err)
	}
	if err := DeleteTemplate(ctx, pool, slug, ident, guest, tmpl.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest delete: err = %v, want ErrForbidden", err)
	}
	if _, err := ApplyTemplate(ctx, pool, slug, ident, guest, tmpl.ID, TemplateOverrides{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest apply: err = %v, want ErrForbidden", err)
	}

	// Non-members get the tenancy 404 (never a forbidden).
	if _, err := ListTemplates(ctx, pool, slug, ident, outside); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-member list: err = %v, want ErrNotFound", err)
	}
	if _, err := CreateTemplate(ctx, pool, slug, ident, outside, TemplateInput{Name: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-member create: err = %v, want ErrNotFound", err)
	}

	// Bad workspace slug → workspace 404; bad project → project 404.
	if _, err := ListTemplates(ctx, pool, "no-such-ws", ident, admin); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bad slug: err = %v, want ErrNotFound", err)
	}
	if _, err := GetTemplate(ctx, pool, slug, "NOPE", admin, tmpl.ID); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("bad project: err = %v, want ErrProjectNotFound", err)
	}

	// A template of another project is invisible (404), not leaked.
	ident2 := uniqueTestIdentifier()
	createTestProject(t, pool, slug, admin, "Ops", ident2)
	if _, err := GetTemplate(ctx, pool, slug, ident2, admin, tmpl.ID); !errors.Is(err, ErrTemplateNotFound) {
		t.Fatalf("cross-project get: err = %v, want ErrTemplateNotFound", err)
	}
}

// seedTemplateRefs creates a label, an estimate point, and a second
// state for apply tests.
func seedTemplateRefs(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, ident, actor string) (labelID, pointID, stateID string) {
	t.Helper()
	label, err := CreateLabel(ctx, pool, slug, ident, actor, LabelInput{Name: "template-bug"})
	if err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}
	est, err := CreateEstimate(ctx, pool, slug, ident, actor, EstimateInput{
		Name:   "T-shirt",
		Points: []EstimatePointInput{{Key: "S", Value: 1}, {Key: "M", Value: 2}},
	})
	if err != nil {
		t.Fatalf("CreateEstimate: %v", err)
	}
	if len(est.Points) != 2 {
		t.Fatalf("points = %d, want 2", len(est.Points))
	}
	state, err := CreateState(ctx, pool, slug, ident, actor, StateInput{Name: "Template Progress " + uniqueTestSlug("st"), Group: "started", Color: "#000000"})
	if err != nil {
		t.Fatalf("CreateState: %v", err)
	}
	return label.ID, est.Points[0].ID, state.ID
}

func TestTemplateApply(t *testing.T) {
	ctx, pool, slug, ident, actor := setupTemplateTest(t)
	labelID, pointID, stateID := seedTemplateRefs(t, ctx, pool, slug, ident, actor)

	tmpl, err := CreateTemplate(ctx, pool, slug, ident, actor, TemplateInput{
		Name: "Bug flow",
		TemplateData: templateDataJSON(t, `{"name":"Investigate bug","description":"Steps to reproduce","priority":2,`+
			`"state_id":"`+stateID+`","estimate_point_id":"`+pointID+`","label_ids":["`+labelID+`"]}`),
	})
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}

	// Happy path: no overrides — the issue inherits every default.
	iss, err := ApplyTemplate(ctx, pool, slug, ident, actor, tmpl.ID, TemplateOverrides{})
	if err != nil {
		t.Fatalf("ApplyTemplate: %v", err)
	}
	if iss.Name != "Investigate bug" {
		t.Fatalf("issue name = %q, want template default", iss.Name)
	}
	if iss.Priority != 2 {
		t.Fatalf("issue priority = %d, want 2", iss.Priority)
	}
	if iss.StateID != stateID {
		t.Fatalf("issue state = %q, want %q", iss.StateID, stateID)
	}
	if iss.EstimatePointID == nil || *iss.EstimatePointID != pointID {
		t.Fatalf("issue estimate = %+v, want %q", iss.EstimatePointID, pointID)
	}
	if string(iss.Description) != `"Steps to reproduce"` {
		t.Fatalf("issue description = %s", iss.Description)
	}
	if iss.DisplayID == "" || iss.SequenceID == 0 {
		t.Fatalf("issue missing display/sequence: %+v", iss)
	}
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM issue_labels WHERE issue_id = $1::uuid AND label_id = $2::uuid`,
		iss.ID, labelID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("label attach: n=%d err=%v", n, err)
	}

	// Overrides replace the defaults field-by-field.
	ovName := "Override title"
	ovPriority := 4
	ovLabels := []string{}
	iss2, err := ApplyTemplate(ctx, pool, slug, ident, actor, tmpl.ID, TemplateOverrides{
		Name:     &ovName,
		Priority: &ovPriority,
		LabelIDs: &ovLabels, // empty override clears the template labels
	})
	if err != nil {
		t.Fatalf("ApplyTemplate overrides: %v", err)
	}
	if iss2.Name != "Override title" || iss2.Priority != 4 {
		t.Fatalf("overrides not applied: %+v", iss2)
	}
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM issue_labels WHERE issue_id = $1::uuid`, iss2.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("override labels cleared: n=%d err=%v", n, err)
	}
	// Untouched fields still come from the template.
	if iss2.StateID != stateID {
		t.Fatalf("state should still be the template default, got %q", iss2.StateID)
	}

	// A template with no issue-name default falls back to its own name.
	tmpl3, err := CreateTemplate(ctx, pool, slug, ident, actor, TemplateInput{Name: "Chore"})
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	iss3, err := ApplyTemplate(ctx, pool, slug, ident, actor, tmpl3.ID, TemplateOverrides{})
	if err != nil {
		t.Fatalf("ApplyTemplate nameless: %v", err)
	}
	if iss3.Name != "Chore" {
		t.Fatalf("issue name = %q, want template name fallback", iss3.Name)
	}
	// No state default → the project's backlog state.
	var backlogID string
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM states WHERE project_id = (SELECT id FROM projects WHERE identifier = $1) AND "group" = 'backlog' ORDER BY sequence LIMIT 1`,
		ident).Scan(&backlogID); err != nil {
		t.Fatalf("backlog state: %v", err)
	}
	if iss3.StateID != backlogID {
		t.Fatalf("state = %q, want backlog %q", iss3.StateID, backlogID)
	}

	// Applying an unknown template → 404.
	if _, err := ApplyTemplate(ctx, pool, slug, ident, actor, "00000000-0000-0000-0000-000000000000", TemplateOverrides{}); !errors.Is(err, ErrTemplateNotFound) {
		t.Fatalf("apply unknown: err = %v, want ErrTemplateNotFound", err)
	}

	// An override priority outside 0-4 → 400, before any issue is made.
	badPriority := 9
	if _, err := ApplyTemplate(ctx, pool, slug, ident, actor, tmpl.ID, TemplateOverrides{Priority: &badPriority}); !errors.Is(err, ErrInvalidPriority) {
		t.Fatalf("apply bad priority: err = %v, want ErrInvalidPriority", err)
	}
}

func TestTemplateApplyStaleRefs(t *testing.T) {
	ctx, pool, slug, ident, actor := setupTemplateTest(t)
	labelID, pointID, stateID := seedTemplateRefs(t, ctx, pool, slug, ident, actor)

	mkTemplate := func(t *testing.T, name, doc string) string {
		t.Helper()
		tmpl, err := CreateTemplate(ctx, pool, slug, ident, actor, TemplateInput{
			Name:         name,
			TemplateData: templateDataJSON(t, doc),
		})
		if err != nil {
			t.Fatalf("CreateTemplate %s: %v", name, err)
		}
		return tmpl.ID
	}
	assertStale := func(t *testing.T, what string, err error, kind, id string) {
		t.Helper()
		var stale *TemplateStaleRef
		if !errors.As(err, &stale) {
			t.Fatalf("%s: err = %v, want *TemplateStaleRef", what, err)
		}
		if stale.Kind != kind || stale.ID != id {
			t.Fatalf("%s: stale = %+v, want kind=%q id=%q", what, stale, kind, id)
		}
	}

	// Stale label: the template's label was deleted after saving.
	staleLabelID := mkTemplate(t, "stale-label", `{"label_ids":["`+labelID+`"]}`)
	if err := DeleteLabel(ctx, pool, slug, ident, labelID, actor); err != nil {
		t.Fatalf("DeleteLabel: %v", err)
	}
	_, err := ApplyTemplate(ctx, pool, slug, ident, actor, staleLabelID, TemplateOverrides{})
	assertStale(t, "deleted label", err, "label", labelID)

	// Never-existed label id → stale too (honest, not a 500 from the cast).
	ghostLabel := "11111111-2222-3333-4444-555555555555"
	ghostID := mkTemplate(t, "ghost-label", `{"label_ids":["`+ghostLabel+`"]}`)
	_, err = ApplyTemplate(ctx, pool, slug, ident, actor, ghostID, TemplateOverrides{})
	assertStale(t, "ghost label", err, "label", ghostLabel)

	// Stale state.
	staleStateID := mkTemplate(t, "stale-state", `{"state_id":"`+stateID+`"}`)
	if err := DeleteState(ctx, pool, slug, ident, stateID, actor, nil); err != nil {
		t.Fatalf("DeleteState: %v", err)
	}
	_, err = ApplyTemplate(ctx, pool, slug, ident, actor, staleStateID, TemplateOverrides{})
	assertStale(t, "deleted state", err, "state", stateID)

	// Stale estimate point.
	staleEstID := mkTemplate(t, "stale-estimate", `{"estimate_point_id":"`+pointID+`"}`)
	if _, err := pool.Exec(ctx, `DELETE FROM estimate_points WHERE id = $1::uuid`, pointID); err != nil {
		t.Fatalf("delete estimate point: %v", err)
	}
	_, err = ApplyTemplate(ctx, pool, slug, ident, actor, staleEstID, TemplateOverrides{})
	assertStale(t, "deleted estimate point", err, "estimate", pointID)

	// An override can point at a stale ref even when the template is fine.
	fineID := mkTemplate(t, "fine", `{"priority":1}`)
	ovState := stateID // deleted above
	_, err = ApplyTemplate(ctx, pool, slug, ident, actor, fineID, TemplateOverrides{StateID: &ovState})
	assertStale(t, "stale override state", err, "state", stateID)

	// And an override can RESCUE a stale template ref.
	rescueLabel, err := CreateLabel(ctx, pool, slug, ident, actor, LabelInput{Name: "template-rescue"})
	if err != nil {
		t.Fatalf("CreateLabel rescue: %v", err)
	}
	rescue := []string{rescueLabel.ID}
	iss, err := ApplyTemplate(ctx, pool, slug, ident, actor, staleLabelID, TemplateOverrides{LabelIDs: &rescue})
	if err != nil {
		t.Fatalf("rescue apply: %v", err)
	}
	if iss.Name != "stale-label" {
		t.Fatalf("rescued issue name = %q", iss.Name)
	}
}
