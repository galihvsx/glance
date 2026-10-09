package service

// Shared saved views (C10T1): the shared flag makes a view visible to
// every project member (annotated with owner id/name); toggling it is
// owner-only (403 otherwise); deletion is owner-only with a workspace
// admin moderation override; non-members still see nothing; per-user
// name uniqueness is unchanged. Real test database, no skips.

import (
	"errors"
	"testing"
)

func shareView(t *testing.T, fx *viewFixture, actor, viewID string, shared bool) *IssueView {
	t.Helper()
	v, err := UpdateIssueView(fx.ctx, fx.pool, actor, fx.projectID, viewID,
		IssueViewPatch{Shared: &shared})
	if err != nil {
		t.Fatalf("UpdateIssueView(shared=%v): %v", shared, err)
	}
	return v
}

func TestIssueViewSharedVisibleToOtherMember(t *testing.T) {
	fx := setupViewTest(t)
	v := mustCreateView(t, fx, fx.actorA, viewInput("Team bugs"))

	// Private: actorB sees nothing.
	if views, err := ListIssueViews(fx.ctx, fx.pool, fx.actorB, fx.projectID); err != nil || len(views) != 0 {
		t.Fatalf("actorB list before share = %d, err=%v; want 0", len(views), err)
	}

	// Share it.
	shared := shareView(t, fx, fx.actorA, v.ID, true)
	if !shared.Shared {
		t.Fatalf("shared flag = false after toggle on")
	}

	// actorB now sees it, annotated with owner id/name and shared=true.
	views, err := ListIssueViews(fx.ctx, fx.pool, fx.actorB, fx.projectID)
	if err != nil {
		t.Fatalf("ListIssueViews(actorB): %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("actorB sees %d views, want 1", len(views))
	}
	got := views[0]
	if !got.Shared {
		t.Fatalf("shared = false in list item, want true")
	}
	if got.OwnerID != fx.actorA {
		t.Fatalf("owner_id = %q, want actorA %q", got.OwnerID, fx.actorA)
	}
	if got.OwnerName == "" {
		t.Fatalf("owner_name is empty, want a display name")
	}

	// Unshare: invisible to actorB again.
	shareView(t, fx, fx.actorA, v.ID, false)
	if views, err := ListIssueViews(fx.ctx, fx.pool, fx.actorB, fx.projectID); err != nil || len(views) != 0 {
		t.Fatalf("actorB list after unshare = %d, err=%v; want 0", len(views), err)
	}
}

func TestIssueViewSharedInvisibleToNonMember(t *testing.T) {
	fx := setupViewTest(t)
	v := mustCreateView(t, fx, fx.actorA, viewInput("Team bugs"))
	shareView(t, fx, fx.actorA, v.ID, true)

	// A user outside the workspace gets the same 404 as for a missing
	// project — a shared view must not leak existence across workspaces.
	outsider := createTestUser(t, fx.pool, uniqueTestEmail("views-out"))
	if _, err := ListIssueViews(fx.ctx, fx.pool, outsider, fx.projectID); !errors.Is(err, ErrViewProjectNotFound) {
		t.Fatalf("outsider list: err = %v, want ErrViewProjectNotFound", err)
	}
}

func TestIssueViewShareToggleNonOwnerForbidden(t *testing.T) {
	fx := setupViewTest(t)
	v := mustCreateView(t, fx, fx.actorA, viewInput("Team bugs"))
	shareView(t, fx, fx.actorA, v.ID, true)

	// Non-owner (a project member) cannot toggle sharing on someone
	// else's view — even a shared one they can see. 403, not 404: the
	// view is visible to them in the list.
	on := true
	if _, err := UpdateIssueView(fx.ctx, fx.pool, fx.actorB, fx.projectID, v.ID,
		IssueViewPatch{Shared: &on}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-owner share toggle: err = %v, want ErrForbidden", err)
	}

	// Non-owner cannot rename another's shared view either.
	rename := "Hijacked"
	if _, err := UpdateIssueView(fx.ctx, fx.pool, fx.actorB, fx.projectID, v.ID,
		IssueViewPatch{Name: &rename}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-owner rename of shared view: err = %v, want ErrForbidden", err)
	}

	// A private view of another user still reads as not found (no
	// existence leak through the toggle path).
	priv := mustCreateView(t, fx, fx.actorA, viewInput("Secret"))
	if _, err := UpdateIssueView(fx.ctx, fx.pool, fx.actorB, fx.projectID, priv.ID,
		IssueViewPatch{Shared: &on}); !errors.Is(err, ErrViewNotFound) {
		t.Fatalf("non-owner toggle on private view: err = %v, want ErrViewNotFound", err)
	}
}

func TestIssueViewSharedNameUniquenessUnchanged(t *testing.T) {
	fx := setupViewTest(t)
	v := mustCreateView(t, fx, fx.actorA, viewInput("My bugs"))
	shareView(t, fx, fx.actorA, v.ID, true)

	// Sharing does not relax per-(user, project) uniqueness: the owner
	// still cannot create or rename onto a duplicate name.
	if _, err := CreateIssueView(fx.ctx, fx.pool, fx.actorA, fx.projectID, viewInput("MY BUGS")); !errors.Is(err, ErrViewNameTaken) {
		t.Fatalf("duplicate of shared view: err = %v, want ErrViewNameTaken", err)
	}

	// Another user may share a view with the same name (per-user scope).
	bv := mustCreateView(t, fx, fx.actorB, viewInput("My bugs"))
	shareView(t, fx, fx.actorB, bv.ID, true)
	views, err := ListIssueViews(fx.ctx, fx.pool, fx.actorB, fx.projectID)
	if err != nil {
		t.Fatalf("ListIssueViews(actorB): %v", err)
	}
	if len(views) != 2 {
		t.Fatalf("actorB sees %d views, want 2 (own shared + A's shared)", len(views))
	}
}

func TestIssueViewDeleteSharedOwnership(t *testing.T) {
	fx := setupViewTest(t)
	// actorA is the workspace creator (RoleAdmin); actorB is a plain
	// member. Add a third admin to exercise the moderation rule.
	actorC := createTestUser(t, fx.pool, uniqueTestEmail("views"))
	addTestMember(t, fx.pool, fx.slug, fx.actorA, actorC, RoleAdmin)

	v := mustCreateView(t, fx, fx.actorA, viewInput("Team bugs"))
	shareView(t, fx, fx.actorA, v.ID, true)

	// Non-owner, non-admin member cannot delete a shared view: 403.
	if err := DeleteIssueView(fx.ctx, fx.pool, fx.actorB, fx.projectID, v.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member delete of shared view: err = %v, want ErrForbidden", err)
	}

	// Workspace admin may delete any shared view (moderation rule).
	if err := DeleteIssueView(fx.ctx, fx.pool, actorC, fx.projectID, v.ID); err != nil {
		t.Fatalf("admin delete of shared view: %v", err)
	}
	if views, err := ListIssueViews(fx.ctx, fx.pool, fx.actorA, fx.projectID); err != nil || len(views) != 0 {
		t.Fatalf("views after admin delete = %d, err=%v; want 0", len(views), err)
	}

	// Owner can always delete their own shared view.
	v2 := mustCreateView(t, fx, fx.actorA, viewInput("Team bugs 2"))
	shareView(t, fx, fx.actorA, v2.ID, true)
	if err := DeleteIssueView(fx.ctx, fx.pool, fx.actorA, fx.projectID, v2.ID); err != nil {
		t.Fatalf("owner delete of shared view: %v", err)
	}

	// Deleting another user's PRIVATE view stays a silent no-op (no
	// existence leak, no 403).
	priv := mustCreateView(t, fx, fx.actorA, viewInput("Secret"))
	if err := DeleteIssueView(fx.ctx, fx.pool, fx.actorB, fx.projectID, priv.ID); err != nil {
		t.Fatalf("member delete of private view: %v", err)
	}
	if views, err := ListIssueViews(fx.ctx, fx.pool, fx.actorA, fx.projectID); err != nil || len(views) != 1 {
		t.Fatalf("private view after cross-user delete: %d views, err=%v; want 1", len(views), err)
	}

	// Admin deleting a private view of another user is also a no-op —
	// the moderation override covers shared views only.
	if err := DeleteIssueView(fx.ctx, fx.pool, actorC, fx.projectID, priv.ID); err != nil {
		t.Fatalf("admin delete of private view: %v", err)
	}
	if views, err := ListIssueViews(fx.ctx, fx.pool, fx.actorA, fx.projectID); err != nil || len(views) != 1 {
		t.Fatalf("private view after admin delete: %d views, err=%v; want 1", len(views), err)
	}
}

func TestIssueViewSharedCascadeOnProjectDelete(t *testing.T) {
	fx := setupViewTest(t)
	v := mustCreateView(t, fx, fx.actorA, viewInput("Doomed"))
	shareView(t, fx, fx.actorA, v.ID, true)

	if _, err := fx.pool.Exec(fx.ctx,
		`DELETE FROM projects WHERE id = $1::uuid`, fx.projectID); err != nil {
		t.Fatalf("delete project: %v", err)
	}
	var n int
	if err := fx.pool.QueryRow(fx.ctx,
		`SELECT COUNT(*) FROM issue_views WHERE project_id = $1::uuid`, fx.projectID).Scan(&n); err != nil {
		t.Fatalf("count views: %v", err)
	}
	if n != 0 {
		t.Fatalf("issue_views rows after project delete = %d, want 0", n)
	}
}
