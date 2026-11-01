package service

// Workspace-scoped saved views (C113T0): CRUD, per-user isolation,
// workspace scoping, duplicate name → ErrViewNameTaken, default-view
// exclusivity per (user, workspace), shared-view visibility, non-owner
// mutation 403, admin delete-shared, guest access, non-member 404, and
// the taxonomy-leak check (a name clash in a workspace the caller
// cannot see must not surface). Real test database, no skips. Mirrors
// issue_views_test.go / issue_views_shared_test.go idioms.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func wsViewInput(name string) IssueViewInput {
	return IssueViewInput{
		Name:    name,
		Filters: json.RawMessage(`{"pql":"state = \"Todo\""}`),
		Display: nil,
	}
}

func mustCreateWSView(t *testing.T, fx *viewFixture, actor string, in IssueViewInput) *IssueView {
	t.Helper()
	v, err := CreateWorkspaceIssueView(fx.ctx, fx.pool, actor, fx.slug, in)
	if err != nil {
		t.Fatalf("CreateWorkspaceIssueView(%q): %v", in.Name, err)
	}
	return v
}

func shareWSView(t *testing.T, fx *viewFixture, actor, viewID string, shared bool) {
	t.Helper()
	if _, err := UpdateWorkspaceIssueView(fx.ctx, fx.pool, actor, fx.slug, viewID,
		IssueViewPatch{Shared: &shared}); err != nil {
		t.Fatalf("share workspace view: %v", err)
	}
}

func TestWorkspaceIssueViewCRUD(t *testing.T) {
	fx := setupViewTest(t)

	v := mustCreateWSView(t, fx, fx.actorA, wsViewInput("My work bugs"))
	if v.ID == "" || v.Name != "My work bugs" {
		t.Fatalf("created view = %+v, want name=My work bugs with id", v)
	}
	if v.IsDefault {
		t.Fatalf("new view is_default = true, want false")
	}
	var gotFilters map[string]any
	if err := json.Unmarshal(v.Filters, &gotFilters); err != nil {
		t.Fatalf("filters unmarshal: %v", err)
	}
	if gotFilters["pql"] != `state = "Todo"` {
		t.Fatalf("filters = %s, want pql round-tripped", v.Filters)
	}
	if v.Display != nil {
		t.Fatalf("display = %s, want nil (no display settings)", v.Display)
	}

	trimmed := mustCreateWSView(t, fx, fx.actorA, wsViewInput("  padded  "))
	if trimmed.Name != "padded" {
		t.Fatalf("name = %q, want trimmed", trimmed.Name)
	}

	views, err := ListWorkspaceIssueViews(fx.ctx, fx.pool, fx.actorA, fx.slug)
	if err != nil {
		t.Fatalf("ListWorkspaceIssueViews: %v", err)
	}
	if len(views) != 2 || views[0].ID != v.ID || views[1].ID != trimmed.ID {
		t.Fatalf("list = %d views, want [My work bugs padded] in creation order", len(views))
	}

	newName := "Critical work"
	updated, err := UpdateWorkspaceIssueView(fx.ctx, fx.pool, fx.actorA, fx.slug, v.ID,
		IssueViewPatch{Name: &newName})
	if err != nil {
		t.Fatalf("UpdateWorkspaceIssueView rename: %v", err)
	}
	if updated.Name != "Critical work" {
		t.Fatalf("renamed = %q, want Critical work", updated.Name)
	}

	if err := DeleteWorkspaceIssueView(fx.ctx, fx.pool, fx.actorA, fx.slug, v.ID); err != nil {
		t.Fatalf("DeleteWorkspaceIssueView: %v", err)
	}
	if err := DeleteWorkspaceIssueView(fx.ctx, fx.pool, fx.actorA, fx.slug, v.ID); err != nil {
		t.Fatalf("DeleteWorkspaceIssueView again: %v", err)
	}
	views, err = ListWorkspaceIssueViews(fx.ctx, fx.pool, fx.actorA, fx.slug)
	if err != nil || len(views) != 1 {
		t.Fatalf("list after delete = %d views, err=%v; want 1", len(views), err)
	}
}

func TestWorkspaceIssueViewDuplicateName(t *testing.T) {
	fx := setupViewTest(t)
	mustCreateWSView(t, fx, fx.actorA, wsViewInput("My bugs"))

	if _, err := CreateWorkspaceIssueView(fx.ctx, fx.pool, fx.actorA, fx.slug, wsViewInput("My bugs")); !errors.Is(err, ErrViewNameTaken) {
		t.Fatalf("duplicate create: err = %v, want ErrViewNameTaken", err)
	}
	if _, err := CreateWorkspaceIssueView(fx.ctx, fx.pool, fx.actorA, fx.slug, wsViewInput("my BUGS")); !errors.Is(err, ErrViewNameTaken) {
		t.Fatalf("case-insensitive duplicate: err = %v, want ErrViewNameTaken", err)
	}

	// Same name by a DIFFERENT user on the same workspace is fine.
	other := mustCreateWSView(t, fx, fx.actorB, wsViewInput("My bugs"))
	if other.Name != "My bugs" {
		t.Fatalf("other user's view name = %q", other.Name)
	}

	// Same name in PROJECT scope does not clash with workspace scope
	// (different scope columns, different partial indexes).
	proj := mustCreateView(t, fx, fx.actorA, viewInput("Scoped"))
	if _, err := CreateWorkspaceIssueView(fx.ctx, fx.pool, fx.actorA, fx.slug, wsViewInput("Scoped")); err != nil {
		t.Fatalf("workspace view sharing a project view's name: %v", err)
	}
	_ = proj

	// Rename onto a taken name → ErrViewNameTaken.
	second := mustCreateWSView(t, fx, fx.actorA, wsViewInput("Second"))
	taken := "my bugs"
	if _, err := UpdateWorkspaceIssueView(fx.ctx, fx.pool, fx.actorA, fx.slug, second.ID, IssueViewPatch{Name: &taken}); !errors.Is(err, ErrViewNameTaken) {
		t.Fatalf("rename onto taken: err = %v, want ErrViewNameTaken", err)
	}
}

func TestWorkspaceIssueViewValidation(t *testing.T) {
	fx := setupViewTest(t)

	for _, name := range []string{"", "   "} {
		if _, err := CreateWorkspaceIssueView(fx.ctx, fx.pool, fx.actorA, fx.slug, wsViewInput(name)); !errors.Is(err, ErrInvalidViewName) {
			t.Fatalf("CreateWorkspaceIssueView(%q): err = %v, want ErrInvalidViewName", name, err)
		}
	}
	for _, filters := range []json.RawMessage{
		nil,
		json.RawMessage(``),
		json.RawMessage(`[1,2]`),
		json.RawMessage(`{bad json`),
	} {
		in := wsViewInput("valid name")
		in.Filters = filters
		if _, err := CreateWorkspaceIssueView(fx.ctx, fx.pool, fx.actorA, fx.slug, in); !errors.Is(err, ErrInvalidViewFilters) {
			t.Fatalf("CreateWorkspaceIssueView(filters=%q): err = %v, want ErrInvalidViewFilters", filters, err)
		}
	}
}

func TestWorkspaceIssueViewUserIsolation(t *testing.T) {
	fx := setupViewTest(t)
	v := mustCreateWSView(t, fx, fx.actorA, wsViewInput("Mine"))

	views, err := ListWorkspaceIssueViews(fx.ctx, fx.pool, fx.actorB, fx.slug)
	if err != nil {
		t.Fatalf("ListWorkspaceIssueViews(actorB): %v", err)
	}
	if len(views) != 0 {
		t.Fatalf("actorB sees %d views, want 0", len(views))
	}

	newName := "Hijacked"
	if _, err := UpdateWorkspaceIssueView(fx.ctx, fx.pool, fx.actorB, fx.slug, v.ID, IssueViewPatch{Name: &newName}); !errors.Is(err, ErrViewNotFound) {
		t.Fatalf("cross-user update of private view: err = %v, want ErrViewNotFound", err)
	}
	if err := DeleteWorkspaceIssueView(fx.ctx, fx.pool, fx.actorB, fx.slug, v.ID); err != nil {
		t.Fatalf("cross-user delete of private view: %v", err)
	}
	views, err = ListWorkspaceIssueViews(fx.ctx, fx.pool, fx.actorA, fx.slug)
	if err != nil || len(views) != 1 || views[0].Name != "Mine" {
		t.Fatalf("actorA's view after cross-user ops: %v, err=%v", views, err)
	}
}

func TestWorkspaceIssueViewSharedVisibility(t *testing.T) {
	fx := setupViewTest(t)
	v := mustCreateWSView(t, fx, fx.actorA, wsViewInput("Team work"))
	shareWSView(t, fx, fx.actorA, v.ID, true)

	// Member B's list shows the shared view with the owner annotation.
	views, err := ListWorkspaceIssueViews(fx.ctx, fx.pool, fx.actorB, fx.slug)
	if err != nil {
		t.Fatalf("ListWorkspaceIssueViews(actorB): %v", err)
	}
	if len(views) != 1 || views[0].ID != v.ID || !views[0].Shared {
		t.Fatalf("actorB list = %v, want the shared view", views)
	}
	if views[0].OwnerID != fx.actorA {
		t.Fatalf("shared view owner = %q, want actorA", views[0].OwnerID)
	}

	// Non-owner mutating a shared view → 403 (visible, so honest).
	newName := "Hijacked"
	if _, err := UpdateWorkspaceIssueView(fx.ctx, fx.pool, fx.actorB, fx.slug, v.ID, IssueViewPatch{Name: &newName}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-owner update of shared view: err = %v, want ErrForbidden", err)
	}
	on := false
	if _, err := UpdateWorkspaceIssueView(fx.ctx, fx.pool, fx.actorB, fx.slug, v.ID, IssueViewPatch{Shared: &on}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-owner unshare of shared view: err = %v, want ErrForbidden", err)
	}
	if err := DeleteWorkspaceIssueView(fx.ctx, fx.pool, fx.actorB, fx.slug, v.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member delete of shared view: err = %v, want ErrForbidden", err)
	}
}

func TestWorkspaceIssueViewDeleteSharedOwnership(t *testing.T) {
	fx := setupViewTest(t)
	// actorA is the workspace creator (RoleAdmin); actorB is a plain
	// member. Add a third admin to exercise the moderation rule.
	actorC := createTestUser(t, fx.pool, uniqueTestEmail("views"))
	addTestMember(t, fx.pool, fx.slug, fx.actorA, actorC, RoleAdmin)

	v := mustCreateWSView(t, fx, fx.actorA, wsViewInput("Team work"))
	shareWSView(t, fx, fx.actorA, v.ID, true)

	// Workspace admin may delete any shared view (moderation rule).
	if err := DeleteWorkspaceIssueView(fx.ctx, fx.pool, actorC, fx.slug, v.ID); err != nil {
		t.Fatalf("admin delete of shared view: %v", err)
	}
	if views, err := ListWorkspaceIssueViews(fx.ctx, fx.pool, fx.actorA, fx.slug); err != nil || len(views) != 0 {
		t.Fatalf("views after admin delete = %d, err=%v; want 0", len(views), err)
	}

	// Owner can always delete their own shared view.
	v2 := mustCreateWSView(t, fx, fx.actorA, wsViewInput("Team work 2"))
	shareWSView(t, fx, fx.actorA, v2.ID, true)
	if err := DeleteWorkspaceIssueView(fx.ctx, fx.pool, fx.actorA, fx.slug, v2.ID); err != nil {
		t.Fatalf("owner delete of shared view: %v", err)
	}

	// Admin deleting a private view of another user is a no-op — the
	// moderation override covers shared views only.
	priv := mustCreateWSView(t, fx, fx.actorA, wsViewInput("Secret"))
	if err := DeleteWorkspaceIssueView(fx.ctx, fx.pool, actorC, fx.slug, priv.ID); err != nil {
		t.Fatalf("admin delete of private view: %v", err)
	}
	if views, err := ListWorkspaceIssueViews(fx.ctx, fx.pool, fx.actorA, fx.slug); err != nil || len(views) != 1 {
		t.Fatalf("private view after admin delete: %d views, err=%v; want 1", len(views), err)
	}
}

func TestWorkspaceIssueViewDefaultExclusivity(t *testing.T) {
	fx := setupViewTest(t)
	a := mustCreateWSView(t, fx, fx.actorA, wsViewInput("A"))
	b := mustCreateWSView(t, fx, fx.actorA, wsViewInput("B"))

	isDefault := true
	if _, err := UpdateWorkspaceIssueView(fx.ctx, fx.pool, fx.actorA, fx.slug, a.ID, IssueViewPatch{IsDefault: &isDefault}); err != nil {
		t.Fatalf("set default A: %v", err)
	}
	if _, err := UpdateWorkspaceIssueView(fx.ctx, fx.pool, fx.actorA, fx.slug, b.ID, IssueViewPatch{IsDefault: &isDefault}); err != nil {
		t.Fatalf("set default B: %v", err)
	}
	views, err := ListWorkspaceIssueViews(fx.ctx, fx.pool, fx.actorA, fx.slug)
	if err != nil {
		t.Fatalf("ListWorkspaceIssueViews: %v", err)
	}
	defaults := 0
	for _, v := range views {
		if v.IsDefault {
			defaults++
			if v.ID != b.ID {
				t.Fatalf("default = %q, want B (the last one set)", v.Name)
			}
		}
	}
	if defaults != 1 {
		t.Fatalf("defaults = %d, want exactly 1", defaults)
	}

	// A workspace default does not clear a project default (and vice
	// versa): the scopes clear independently.
	proj := mustCreateView(t, fx, fx.actorA, viewInput("Proj"))
	if _, err := UpdateIssueView(fx.ctx, fx.pool, fx.actorA, fx.projectID, proj.ID, IssueViewPatch{IsDefault: &isDefault}); err != nil {
		t.Fatalf("set project default: %v", err)
	}
	views, err = ListWorkspaceIssueViews(fx.ctx, fx.pool, fx.actorA, fx.slug)
	if err != nil {
		t.Fatalf("ListWorkspaceIssueViews after project default: %v", err)
	}
	defaults = 0
	for _, v := range views {
		if v.IsDefault {
			defaults++
		}
	}
	if defaults != 1 {
		t.Fatalf("workspace defaults after project default = %d, want 1", defaults)
	}
}

func TestWorkspaceIssueViewGuestAccess(t *testing.T) {
	fx := setupViewTest(t)
	guest := createTestUser(t, fx.pool, uniqueTestEmail("views"))
	addTestMember(t, fx.pool, fx.slug, fx.actorA, guest, RoleGuest)

	// Any workspace member (guest 5+) may manage their own views.
	v := mustCreateWSView(t, fx, guest, wsViewInput("Guest view"))
	views, err := ListWorkspaceIssueViews(fx.ctx, fx.pool, guest, fx.slug)
	if err != nil || len(views) != 1 || views[0].ID != v.ID {
		t.Fatalf("guest list = %v, err=%v; want own view", views, err)
	}
	// ... and see shared views.
	shared := mustCreateWSView(t, fx, fx.actorA, wsViewInput("Team"))
	shareWSView(t, fx, fx.actorA, shared.ID, true)
	views, err = ListWorkspaceIssueViews(fx.ctx, fx.pool, guest, fx.slug)
	if err != nil || len(views) != 2 {
		t.Fatalf("guest list with shared = %d views, err=%v; want 2", len(views), err)
	}
}

func TestWorkspaceIssueViewTenancy(t *testing.T) {
	fx := setupViewTest(t)

	// Unknown slug → ErrViewWorkspaceNotFound.
	if _, err := ListWorkspaceIssueViews(fx.ctx, fx.pool, fx.actorA, "no-such-workspace"); !errors.Is(err, ErrViewWorkspaceNotFound) {
		t.Fatalf("list unknown workspace: err = %v, want ErrViewWorkspaceNotFound", err)
	}
	if _, err := CreateWorkspaceIssueView(fx.ctx, fx.pool, fx.actorA, "no-such-workspace", wsViewInput("x")); !errors.Is(err, ErrViewWorkspaceNotFound) {
		t.Fatalf("create unknown workspace: err = %v, want ErrViewWorkspaceNotFound", err)
	}

	// A workspace the actor is not a member of → the SAME sentinel
	// (no existence leak).
	outsider := createTestUser(t, fx.pool, uniqueTestEmail("views-out"))
	otherSlug := uniqueTestSlug("views-ws2")
	createTestWorkspace(t, fx.pool, "Other Co", otherSlug, outsider)
	if _, err := ListWorkspaceIssueViews(fx.ctx, fx.pool, fx.actorA, otherSlug); !errors.Is(err, ErrViewWorkspaceNotFound) {
		t.Fatalf("list non-member workspace: err = %v, want ErrViewWorkspaceNotFound", err)
	}
	// Update/delete on a non-member workspace also 404, not 403/500.
	if _, err := UpdateWorkspaceIssueView(fx.ctx, fx.pool, fx.actorA, otherSlug, "00000000-0000-0000-0000-000000000000", IssueViewPatch{}); !errors.Is(err, ErrViewWorkspaceNotFound) {
		t.Fatalf("update non-member workspace: err = %v, want ErrViewWorkspaceNotFound", err)
	}
	if err := DeleteWorkspaceIssueView(fx.ctx, fx.pool, fx.actorA, otherSlug, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrViewWorkspaceNotFound) {
		t.Fatalf("delete non-member workspace: err = %v, want ErrViewWorkspaceNotFound", err)
	}
}

func TestWorkspaceIssueViewTaxonomyLeak(t *testing.T) {
	fx := setupViewTest(t)

	// A view name taken in a workspace the caller cannot see must not
	// surface: no false clash, no leak through create or rename.
	outsider := createTestUser(t, fx.pool, uniqueTestEmail("views-out"))
	otherSlug := uniqueTestSlug("views-ws3")
	createTestWorkspace(t, fx.pool, "Hidden Co", otherSlug, outsider)
	if _, err := CreateWorkspaceIssueView(fx.ctx, fx.pool, outsider, otherSlug, wsViewInput("Secret name")); err != nil {
		t.Fatalf("CreateWorkspaceIssueView(hidden workspace): %v", err)
	}
	if _, err := CreateWorkspaceIssueView(fx.ctx, fx.pool, outsider, otherSlug, wsViewInput("Hidden two")); err != nil {
		t.Fatalf("CreateWorkspaceIssueView(hidden workspace 2): %v", err)
	}

	// Same name in the caller's own workspace: succeeds, no 409.
	if _, err := CreateWorkspaceIssueView(fx.ctx, fx.pool, fx.actorA, fx.slug, wsViewInput("Secret name")); err != nil {
		t.Fatalf("create with hidden-workspace name: err = %v, want success (no leak)", err)
	}
	// Rename onto a name that exists ONLY in the hidden workspace:
	// also succeeds.
	other := mustCreateWSView(t, fx, fx.actorA, wsViewInput("Other"))
	hidden := "hidden TWO"
	if _, err := UpdateWorkspaceIssueView(fx.ctx, fx.pool, fx.actorA, fx.slug, other.ID, IssueViewPatch{Name: &hidden}); err != nil {
		t.Fatalf("rename onto hidden-workspace name: err = %v, want success (no leak)", err)
	}
	// And the hidden view never appears in the caller's list.
	views, err := ListWorkspaceIssueViews(fx.ctx, fx.pool, fx.actorA, fx.slug)
	if err != nil {
		t.Fatalf("ListWorkspaceIssueViews: %v", err)
	}
	for _, v := range views {
		if v.OwnerID == outsider {
			t.Fatalf("caller's list leaked hidden-workspace view %q", v.Name)
		}
	}
}

func TestWorkspaceIssueViewCascadeOnWorkspaceDelete(t *testing.T) {
	fx := setupViewTest(t)
	v := mustCreateWSView(t, fx, fx.actorA, wsViewInput("Doomed"))
	if _, err := fx.pool.Exec(context.Background(),
		`DELETE FROM workspaces WHERE slug = $1`, fx.slug); err != nil {
		t.Fatalf("delete workspace: %v", err)
	}
	var n int
	if err := fx.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM issue_views WHERE id = $1::uuid`, v.ID).Scan(&n); err != nil {
		t.Fatalf("count views: %v", err)
	}
	if n != 0 {
		t.Fatalf("issue_views rows after workspace delete = %d, want 0", n)
	}
}
