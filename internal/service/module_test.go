package service

// Modules (C3T1): project-scoped CRUD, bulk issue membership via the
// module_issues junction, and the delete guard.

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func setupModuleTest(t *testing.T) (context.Context, *pgxpool.Pool, string, string, string) {
	t.Helper()
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	actor := createTestUser(t, pool, uniqueTestEmail("module"))
	slug := uniqueTestSlug("module-ws")
	createTestWorkspace(t, pool, "Module Co", slug, actor)
	ident := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Eng", ident)
	return ctx, pool, slug, ident, actor
}

func strptr(s string) *string { return &s }

func TestModuleCRUD(t *testing.T) {
	ctx, pool, slug, ident, actor := setupModuleTest(t)

	desc := "The epic for everything"
	lead := actor
	m, err := CreateModule(ctx, pool, slug, ident, actor, ModuleInput{
		Name:        "Platform",
		Description: &desc,
		Status:      "active",
		LeadID:      &lead,
		StartDate:   strptr("2026-10-01"),
		TargetDate:  strptr("2026-12-31"),
	})
	if err != nil {
		t.Fatalf("CreateModule: %v", err)
	}
	if m.Name != "Platform" || m.Status != "active" {
		t.Fatalf("got %+v", m)
	}
	if m.Description == nil || *m.Description != desc {
		t.Fatalf("description = %v", m.Description)
	}
	if m.LeadID == nil || *m.LeadID != actor {
		t.Fatalf("lead = %v", m.LeadID)
	}
	if m.StartDate == nil || m.StartDate.Format("2006-01-02") != "2026-10-01" {
		t.Fatalf("start_date = %v", m.StartDate)
	}
	if m.TargetDate == nil || m.TargetDate.Format("2006-01-02") != "2026-12-31" {
		t.Fatalf("target_date = %v", m.TargetDate)
	}
	if m.IssueCount != 0 {
		t.Fatalf("issue_count = %d, want 0", m.IssueCount)
	}

	got, err := GetModule(ctx, pool, slug, ident, actor, m.ID)
	if err != nil {
		t.Fatalf("GetModule: %v", err)
	}
	if got.ID != m.ID || got.IssueCount != 0 {
		t.Fatalf("got %+v", got)
	}

	list, err := ListModules(ctx, pool, slug, ident, actor)
	if err != nil {
		t.Fatalf("ListModules: %v", err)
	}
	if len(list) != 1 || list[0].ID != m.ID {
		t.Fatalf("list = %+v", list)
	}

	// Default status is active when omitted.
	m2, err := CreateModule(ctx, pool, slug, ident, actor, ModuleInput{Name: "Second"})
	if err != nil {
		t.Fatalf("CreateModule minimal: %v", err)
	}
	if m2.Status != "active" || m2.Description != nil || m2.LeadID != nil {
		t.Fatalf("defaults: %+v", m2)
	}

	// Duplicate name in the project conflicts.
	if _, err := CreateModule(ctx, pool, slug, ident, actor, ModuleInput{Name: "Platform"}); !errors.Is(err, ErrModuleConflict) {
		t.Fatalf("dup name: err = %v, want ErrModuleConflict", err)
	}
	// Empty name is rejected.
	if _, err := CreateModule(ctx, pool, slug, ident, actor, ModuleInput{Name: "  "}); !errors.Is(err, ErrNameRequired) {
		t.Fatalf("empty name: err = %v, want ErrNameRequired", err)
	}
	// Bad status vocabulary is rejected.
	if _, err := CreateModule(ctx, pool, slug, ident, actor, ModuleInput{Name: "X", Status: "nope"}); !errors.Is(err, ErrInvalidModule) {
		t.Fatalf("bad status: err = %v, want ErrInvalidModule", err)
	}
	// start_date after target_date is rejected.
	if _, err := CreateModule(ctx, pool, slug, ident, actor, ModuleInput{
		Name: "Y", StartDate: strptr("2026-12-31"), TargetDate: strptr("2026-10-01"),
	}); !errors.Is(err, ErrInvalidModule) {
		t.Fatalf("start>target: err = %v, want ErrInvalidModule", err)
	}
	// Unparseable date is rejected.
	if _, err := CreateModule(ctx, pool, slug, ident, actor, ModuleInput{Name: "Z", StartDate: strptr("tomorrow")}); !errors.Is(err, ErrInvalidModule) {
		t.Fatalf("bad date: err = %v, want ErrInvalidModule", err)
	}
	// Lead naming no user is 404.
	ghost := "00000000-0000-4000-8000-000000000000"
	if _, err := CreateModule(ctx, pool, slug, ident, actor, ModuleInput{Name: "W", LeadID: &ghost}); !errors.Is(err, ErrLeadNotFound) {
		t.Fatalf("ghost lead: err = %v, want ErrLeadNotFound", err)
	}
	// Malformed lead id is 400.
	badLead := "not-a-uuid"
	if _, err := CreateModule(ctx, pool, slug, ident, actor, ModuleInput{Name: "V", LeadID: &badLead}); !errors.Is(err, ErrInvalidModule) {
		t.Fatalf("bad lead: err = %v, want ErrInvalidModule", err)
	}

	// Partial update: rename + status only.
	newName, newStatus := "Platform v2", "completed"
	upd, err := UpdateModule(ctx, pool, slug, ident, actor, m.ID, ModulePatch{Name: &newName, Status: &newStatus})
	if err != nil {
		t.Fatalf("UpdateModule: %v", err)
	}
	if upd.Name != newName || upd.Status != newStatus {
		t.Fatalf("updated = %+v", upd)
	}
	if upd.Description == nil || *upd.Description != desc {
		t.Fatalf("description should be untouched: %+v", upd)
	}

	// "" clears nullable fields.
	empty := ""
	cleared, err := UpdateModule(ctx, pool, slug, ident, actor, m.ID, ModulePatch{
		Description: &empty, LeadID: &empty, StartDate: &empty,
	})
	if err != nil {
		t.Fatalf("clear patch: %v", err)
	}
	if cleared.Description != nil || cleared.LeadID != nil || cleared.StartDate != nil {
		t.Fatalf("cleared = %+v", cleared)
	}
	if cleared.TargetDate == nil {
		t.Fatalf("target_date should be untouched: %+v", cleared)
	}

	// Rename to an existing name conflicts.
	if _, err := UpdateModule(ctx, pool, slug, ident, actor, m2.ID, ModulePatch{Name: &newName}); !errors.Is(err, ErrModuleConflict) {
		t.Fatalf("rename conflict: err = %v, want ErrModuleConflict", err)
	}
	// Empty patch is rejected.
	if _, err := UpdateModule(ctx, pool, slug, ident, actor, m.ID, ModulePatch{}); !errors.Is(err, ErrNothingToUpdate) {
		t.Fatalf("empty patch: err = %v, want ErrNothingToUpdate", err)
	}
	// Patch making start > target is rejected.
	if _, err := UpdateModule(ctx, pool, slug, ident, actor, m.ID, ModulePatch{StartDate: strptr("2027-01-01")}); !errors.Is(err, ErrInvalidModule) {
		t.Fatalf("patch start>target: err = %v, want ErrInvalidModule", err)
	}

	// Unknown module is 404; malformed id is 400.
	if _, err := GetModule(ctx, pool, slug, ident, actor, "00000000-0000-4000-8000-000000000000"); !errors.Is(err, ErrModuleNotFound) {
		t.Fatalf("get missing: err = %v, want ErrModuleNotFound", err)
	}
	if _, err := GetModule(ctx, pool, slug, ident, actor, "not-a-uuid"); !errors.Is(err, ErrInvalidModuleID) {
		t.Fatalf("get malformed: err = %v, want ErrInvalidModuleID", err)
	}
}

func TestModuleMemberScoping(t *testing.T) {
	ctx, pool, slug, ident, actor := setupModuleTest(t)

	m, err := CreateModule(ctx, pool, slug, ident, actor, ModuleInput{Name: "Scoped"})
	if err != nil {
		t.Fatalf("CreateModule: %v", err)
	}

	// Outsider (no membership): everything is "workspace not found".
	outsider := createTestUser(t, pool, uniqueTestEmail("module-out"))
	for name, call := range map[string]func() error{
		"create": func() error {
			_, err := CreateModule(ctx, pool, slug, ident, outsider, ModuleInput{Name: "X"})
			return err
		},
		"list": func() error { _, err := ListModules(ctx, pool, slug, ident, outsider); return err },
		"get":  func() error { _, err := GetModule(ctx, pool, slug, ident, outsider, m.ID); return err },
		"update": func() error {
			_, err := UpdateModule(ctx, pool, slug, ident, outsider, m.ID, ModulePatch{Name: strptr("Y")})
			return err
		},
		"delete": func() error { return DeleteModule(ctx, pool, slug, ident, outsider, m.ID, true) },
		"add": func() error {
			return AddModuleIssues(ctx, pool, slug, ident, outsider, m.ID, []string{"00000000-0000-4000-8000-000000000000"})
		},
	} {
		if err := call(); !errors.Is(err, ErrNotFound) {
			t.Fatalf("outsider %s: err = %v, want ErrNotFound", name, err)
		}
	}

	// Guest (role 5): reads OK, mutations forbidden.
	guest := createTestUser(t, pool, uniqueTestEmail("module-guest"))
	if _, err := pool.Exec(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role)
		 SELECT w.id, $2::uuid, 5 FROM workspaces w WHERE w.slug = $1`, slug, guest); err != nil {
		t.Fatalf("add guest: %v", err)
	}
	if _, err := ListModules(ctx, pool, slug, ident, guest); err != nil {
		t.Fatalf("guest list: %v", err)
	}
	if _, err := GetModule(ctx, pool, slug, ident, guest, m.ID); err != nil {
		t.Fatalf("guest get: %v", err)
	}
	for name, call := range map[string]func() error{
		"create": func() error {
			_, err := CreateModule(ctx, pool, slug, ident, guest, ModuleInput{Name: "X"})
			return err
		},
		"update": func() error {
			_, err := UpdateModule(ctx, pool, slug, ident, guest, m.ID, ModulePatch{Name: strptr("Y")})
			return err
		},
		"delete": func() error { return DeleteModule(ctx, pool, slug, ident, guest, m.ID, true) },
		"add": func() error {
			return AddModuleIssues(ctx, pool, slug, ident, guest, m.ID, []string{"00000000-0000-4000-8000-000000000000"})
		},
		"remove": func() error {
			return RemoveModuleIssues(ctx, pool, slug, ident, guest, m.ID, []string{"00000000-0000-4000-8000-000000000000"})
		},
	} {
		if err := call(); !errors.Is(err, ErrForbidden) {
			t.Fatalf("guest %s: err = %v, want ErrForbidden", name, err)
		}
	}
}

func TestModuleIssues(t *testing.T) {
	ctx, pool, slug, ident, actor := setupModuleTest(t)

	m, err := CreateModule(ctx, pool, slug, ident, actor, ModuleInput{Name: "WithIssues"})
	if err != nil {
		t.Fatalf("CreateModule: %v", err)
	}
	i1 := createTestIssue(t, pool, slug, ident, actor, "issue one")
	i2 := createTestIssue(t, pool, slug, ident, actor, "issue two")

	if err := AddModuleIssues(ctx, pool, slug, ident, actor, m.ID, []string{i1.ID, i2.ID}); err != nil {
		t.Fatalf("AddModuleIssues: %v", err)
	}
	// Idempotent: re-adding is a no-op, never a 409.
	if err := AddModuleIssues(ctx, pool, slug, ident, actor, m.ID, []string{i1.ID}); err != nil {
		t.Fatalf("re-add: %v", err)
	}

	items, err := ListModuleIssues(ctx, pool, slug, ident, actor, m.ID)
	if err != nil {
		t.Fatalf("ListModuleIssues: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len = %d, want 2", len(items))
	}
	for _, it := range items {
		if it.DisplayID == "" {
			t.Fatalf("missing display_id: %+v", it)
		}
		if it.Assignees == nil || it.Labels == nil {
			t.Fatalf("assignees/labels shape must be present (possibly empty): %+v", it)
		}
	}

	got, err := GetModule(ctx, pool, slug, ident, actor, m.ID)
	if err != nil {
		t.Fatalf("GetModule: %v", err)
	}
	if got.IssueCount != 2 {
		t.Fatalf("issue_count = %d, want 2", got.IssueCount)
	}

	// Remove one; removing a non-member is a silent no-op.
	if err := RemoveModuleIssues(ctx, pool, slug, ident, actor, m.ID, []string{i1.ID}); err != nil {
		t.Fatalf("RemoveModuleIssues: %v", err)
	}
	if err := RemoveModuleIssues(ctx, pool, slug, ident, actor, m.ID, []string{i1.ID}); err != nil {
		t.Fatalf("re-remove: %v", err)
	}
	items, err = ListModuleIssues(ctx, pool, slug, ident, actor, m.ID)
	if err != nil {
		t.Fatalf("ListModuleIssues: %v", err)
	}
	if len(items) != 1 || items[0].ID != i2.ID {
		t.Fatalf("after remove: %+v", items)
	}

	// Foreign issue (other project) is 404.
	otherSlug := uniqueTestSlug("module-ws2")
	createTestWorkspace(t, pool, "Module Co 2", otherSlug, actor)
	otherIdent := uniqueTestIdentifier()
	createTestProject(t, pool, otherSlug, actor, "Eng2", otherIdent)
	foreign := createTestIssue(t, pool, otherSlug, otherIdent, actor, "foreign")
	if err := AddModuleIssues(ctx, pool, slug, ident, actor, m.ID, []string{foreign.ID}); !errors.Is(err, ErrIssueNotFound) {
		t.Fatalf("foreign issue: err = %v, want ErrIssueNotFound", err)
	}
	// Empty id list is rejected.
	if err := AddModuleIssues(ctx, pool, slug, ident, actor, m.ID, nil); !errors.Is(err, ErrBulkEmptyIDs) {
		t.Fatalf("empty add: err = %v, want ErrBulkEmptyIDs", err)
	}
	if err := RemoveModuleIssues(ctx, pool, slug, ident, actor, m.ID, nil); !errors.Is(err, ErrBulkEmptyIDs) {
		t.Fatalf("empty remove: err = %v, want ErrBulkEmptyIDs", err)
	}
	// Malformed ids are 400.
	if err := AddModuleIssues(ctx, pool, slug, ident, actor, m.ID, []string{"not-a-uuid"}); !errors.Is(err, ErrInvalidModuleID) {
		t.Fatalf("bad issue id: err = %v, want ErrInvalidModuleID", err)
	}
	if err := RemoveModuleIssues(ctx, pool, slug, ident, actor, m.ID, []string{"not-a-uuid"}); !errors.Is(err, ErrInvalidModuleID) {
		t.Fatalf("bad remove id: err = %v, want ErrInvalidModuleID", err)
	}
}

func TestModuleDeleteGuard(t *testing.T) {
	ctx, pool, slug, ident, actor := setupModuleTest(t)

	m, err := CreateModule(ctx, pool, slug, ident, actor, ModuleInput{Name: "Doomed"})
	if err != nil {
		t.Fatalf("CreateModule: %v", err)
	}
	i1 := createTestIssue(t, pool, slug, ident, actor, "doomed issue")
	if err := AddModuleIssues(ctx, pool, slug, ident, actor, m.ID, []string{i1.ID}); err != nil {
		t.Fatalf("AddModuleIssues: %v", err)
	}

	// Without force, a module with issues is 409.
	if err := DeleteModule(ctx, pool, slug, ident, actor, m.ID, false); !errors.Is(err, ErrModuleHasIssues) {
		t.Fatalf("delete guarded: err = %v, want ErrModuleHasIssues", err)
	}
	// The module is still there.
	if _, err := GetModule(ctx, pool, slug, ident, actor, m.ID); err != nil {
		t.Fatalf("module should survive guarded delete: %v", err)
	}

	// With force the module goes away but the issue survives (unassigned).
	if err := DeleteModule(ctx, pool, slug, ident, actor, m.ID, true); err != nil {
		t.Fatalf("force delete: %v", err)
	}
	if _, err := GetModule(ctx, pool, slug, ident, actor, m.ID); !errors.Is(err, ErrModuleNotFound) {
		t.Fatalf("get deleted: err = %v, want ErrModuleNotFound", err)
	}
	if _, err := GetIssue(ctx, pool, slug, ident, i1.ID, actor); err != nil {
		t.Fatalf("issue should survive module delete: %v", err)
	}

	// Deleting an empty module needs no force.
	m2, err := CreateModule(ctx, pool, slug, ident, actor, ModuleInput{Name: "Empty"})
	if err != nil {
		t.Fatalf("CreateModule: %v", err)
	}
	if err := DeleteModule(ctx, pool, slug, ident, actor, m2.ID, false); err != nil {
		t.Fatalf("delete empty: %v", err)
	}
}
