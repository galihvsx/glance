package service

// Sub-issue parent service tests (C5T4): SetParent happy path + no-op,
// self-parent / cross-project / unknown-parent / cycle 409s, parent
// clearing, delete-detaches-children, and ListIssueChildren summaries.
// Real test database, no skips. Reuses the harness from
// workspace_test.go (newTestPool, migrateTestDB, createTestUser,
// createTestWorkspace, uniqueTestEmail, uniqueTestSlug,
// createTestProject, uniqueTestIdentifier, addTestMember) and the
// issue helpers (createTestIssue).

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

type parentFixture struct {
	pool       *pgxpool.Pool
	wsSlug     string
	identifier string
	actor      string
}

func setupParentTest(t *testing.T, tag string) parentFixture {
	t.Helper()
	pool := newTestPool(t)
	migrateTestDB(t, pool)

	actor := createTestUser(t, pool, uniqueTestEmail("subissue-"+tag))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-sub-"+tag), actor)
	p := createTestProject(t, pool, ws.Slug, actor, "Engineering", uniqueTestIdentifier())
	return parentFixture{pool: pool, wsSlug: ws.Slug, identifier: p.Identifier, actor: actor}
}

func TestSetParentHappyPath(t *testing.T) {
	fx := setupParentTest(t, "happy")
	ctx := context.Background()

	parent := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Parent")
	child := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Child")

	got, err := SetParent(ctx, fx.pool, fx.wsSlug, fx.identifier, child.ID, fx.actor, parent.ID)
	if err != nil {
		t.Fatalf("SetParent: %v", err)
	}
	if got == nil || *got != parent.ID {
		t.Fatalf("SetParent returned %v, want %s", got, parent.ID)
	}

	fetched, err := GetIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, child.ID, fx.actor)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if fetched.ParentID == nil || *fetched.ParentID != parent.ID {
		t.Fatalf("child parent_id = %v, want %s", fetched.ParentID, parent.ID)
	}

	if n := countActivities(t, fx.pool, child.ID, "parent_id"); n != 1 {
		t.Fatalf("parent_id activity rows = %d, want 1", n)
	}

	// No-op re-set writes nothing new.
	if _, err := SetParent(ctx, fx.pool, fx.wsSlug, fx.identifier, child.ID, fx.actor, parent.ID); err != nil {
		t.Fatalf("SetParent no-op: %v", err)
	}
	if n := countActivities(t, fx.pool, child.ID, "parent_id"); n != 1 {
		t.Fatalf("parent_id activity rows after no-op = %d, want 1", n)
	}
}

func TestSetParentSelf(t *testing.T) {
	fx := setupParentTest(t, "self")
	ctx := context.Background()

	iss := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Lonely")
	if _, err := SetParent(ctx, fx.pool, fx.wsSlug, fx.identifier, iss.ID, fx.actor, iss.ID); !errors.Is(err, ErrIssueSelfParent) {
		t.Fatalf("self-parent err = %v, want ErrIssueSelfParent", err)
	}
}

func TestSetParentCrossProject(t *testing.T) {
	fx := setupParentTest(t, "xproj")
	ctx := context.Background()

	other := createTestProject(t, fx.pool, fx.wsSlug, fx.actor, "Design", uniqueTestIdentifier())
	foreignParent := createTestIssue(t, fx.pool, fx.wsSlug, other.Identifier, fx.actor, "Foreign parent")
	child := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Child")

	if _, err := SetParent(ctx, fx.pool, fx.wsSlug, fx.identifier, child.ID, fx.actor, foreignParent.ID); !errors.Is(err, ErrIssueCrossProjectParent) {
		t.Fatalf("cross-project err = %v, want ErrIssueCrossProjectParent", err)
	}
}

func TestSetParentUnknownParent(t *testing.T) {
	fx := setupParentTest(t, "unknown")
	ctx := context.Background()

	child := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Child")
	if _, err := SetParent(ctx, fx.pool, fx.wsSlug, fx.identifier, child.ID, fx.actor, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrParentNotFound) {
		t.Fatalf("unknown parent err = %v, want ErrParentNotFound", err)
	}
	if _, err := SetParent(ctx, fx.pool, fx.wsSlug, fx.identifier, child.ID, fx.actor, "not-a-uuid"); !errors.Is(err, ErrParentNotFound) {
		t.Fatalf("malformed parent err = %v, want ErrParentNotFound", err)
	}
}

func TestSetParentCycle(t *testing.T) {
	fx := setupParentTest(t, "cycle")
	ctx := context.Background()

	a := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "A")
	b := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "B")
	c := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "C")

	// Chain: A is the root, B child of A, C child of B.
	if _, err := SetParent(ctx, fx.pool, fx.wsSlug, fx.identifier, b.ID, fx.actor, a.ID); err != nil {
		t.Fatalf("SetParent B->A: %v", err)
	}
	if _, err := SetParent(ctx, fx.pool, fx.wsSlug, fx.identifier, c.ID, fx.actor, b.ID); err != nil {
		t.Fatalf("SetParent C->B: %v", err)
	}

	// Closing the loop A->C would make A its own descendant.
	if _, err := SetParent(ctx, fx.pool, fx.wsSlug, fx.identifier, a.ID, fx.actor, c.ID); !errors.Is(err, ErrIssueCyclicParent) {
		t.Fatalf("cycle A->C err = %v, want ErrIssueCyclicParent", err)
	}
	// Direct back-edge A->B (B is A's child) is a cycle too.
	if _, err := SetParent(ctx, fx.pool, fx.wsSlug, fx.identifier, a.ID, fx.actor, b.ID); !errors.Is(err, ErrIssueCyclicParent) {
		t.Fatalf("cycle A->B err = %v, want ErrIssueCyclicParent", err)
	}
	// Diamond-free re-parent under a non-descendant is fine: C under A
	// (C's current parent B is A's descendant — moving C up is legal).
	if _, err := SetParent(ctx, fx.pool, fx.wsSlug, fx.identifier, c.ID, fx.actor, a.ID); err != nil {
		t.Fatalf("legal re-parent C->A: %v", err)
	}
}

func TestClearParent(t *testing.T) {
	fx := setupParentTest(t, "clear")
	ctx := context.Background()

	parent := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Parent")
	child := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Child")
	if _, err := SetParent(ctx, fx.pool, fx.wsSlug, fx.identifier, child.ID, fx.actor, parent.ID); err != nil {
		t.Fatalf("SetParent: %v", err)
	}

	got, err := SetParent(ctx, fx.pool, fx.wsSlug, fx.identifier, child.ID, fx.actor, "")
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if got != nil {
		t.Fatalf("clear returned %v, want nil", *got)
	}
	fetched, err := GetIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, child.ID, fx.actor)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if fetched.ParentID != nil {
		t.Fatalf("parent_id after clear = %v, want nil", *fetched.ParentID)
	}
}

func TestSetParentRoleGate(t *testing.T) {
	fx := setupParentTest(t, "role")
	ctx := context.Background()

	guest := createTestUser(t, fx.pool, uniqueTestEmail("subissue-guest"))
	addTestMember(t, fx.pool, fx.wsSlug, fx.actor, guest, RoleGuest)

	parent := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Parent")
	child := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Child")

	if _, err := SetParent(ctx, fx.pool, fx.wsSlug, fx.identifier, child.ID, guest, parent.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest SetParent err = %v, want ErrForbidden", err)
	}
	// A guest may still read children.
	if _, err := ListIssueChildren(ctx, fx.pool, fx.wsSlug, fx.identifier, parent.ID, guest); err != nil {
		t.Fatalf("guest ListIssueChildren: %v", err)
	}
}

func TestDeleteParentDetachesChildren(t *testing.T) {
	fx := setupParentTest(t, "detach")
	ctx := context.Background()

	parent := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Parent")
	child := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Child")
	if _, err := SetParent(ctx, fx.pool, fx.wsSlug, fx.identifier, child.ID, fx.actor, parent.ID); err != nil {
		t.Fatalf("SetParent: %v", err)
	}

	if err := DeleteIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, parent.ID, fx.actor); err != nil {
		t.Fatalf("DeleteIssue: %v", err)
	}

	// The child survives as a top-level issue with a NULL parent.
	fetched, err := GetIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, child.ID, fx.actor)
	if err != nil {
		t.Fatalf("GetIssue(child): %v", err)
	}
	if fetched.ParentID != nil {
		t.Fatalf("child parent_id after parent delete = %v, want nil", *fetched.ParentID)
	}

	// The deleted parent reads as not found.
	if _, err := GetIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, parent.ID, fx.actor); !errors.Is(err, ErrIssueNotFound) {
		t.Fatalf("GetIssue(deleted parent) err = %v, want ErrIssueNotFound", err)
	}
}

func TestListIssueChildren(t *testing.T) {
	fx := setupParentTest(t, "children")
	ctx := context.Background()

	parent := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Parent")
	c1 := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "First child")
	c2 := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Second child")
	unrelated := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Unrelated")
	for _, c := range []*Issue{c1, c2} {
		if _, err := SetParent(ctx, fx.pool, fx.wsSlug, fx.identifier, c.ID, fx.actor, parent.ID); err != nil {
			t.Fatalf("SetParent: %v", err)
		}
	}

	children, err := ListIssueChildren(ctx, fx.pool, fx.wsSlug, fx.identifier, parent.ID, fx.actor)
	if err != nil {
		t.Fatalf("ListIssueChildren: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("children = %d, want 2", len(children))
	}
	// Ordered by sequence_id: c1 (earlier) first.
	if children[0].UUID != c1.ID || children[1].UUID != c2.ID {
		t.Fatalf("order = [%s %s], want [%s %s]", children[0].UUID, children[1].UUID, c1.ID, c2.ID)
	}
	for i, c := range []*Issue{c1, c2} {
		got := children[i]
		if got.Identifier != c.DisplayID {
			t.Fatalf("child identifier = %q, want %q", got.Identifier, c.DisplayID)
		}
		if got.Title != c.Name {
			t.Fatalf("child title = %q, want %q", got.Title, c.Name)
		}
		if got.State == "" {
			t.Fatalf("child state is empty")
		}
	}
	// A soft-deleted child drops out of the summaries.
	if err := DeleteIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, c1.ID, fx.actor); err != nil {
		t.Fatalf("DeleteIssue(child): %v", err)
	}
	children, err = ListIssueChildren(ctx, fx.pool, fx.wsSlug, fx.identifier, parent.ID, fx.actor)
	if err != nil {
		t.Fatalf("ListIssueChildren after delete: %v", err)
	}
	if len(children) != 1 || children[0].UUID != c2.ID {
		t.Fatalf("children after delete = %+v, want only c2", children)
	}

	// A childless issue yields an empty (non-nil) slice.
	empty, err := ListIssueChildren(ctx, fx.pool, fx.wsSlug, fx.identifier, unrelated.ID, fx.actor)
	if err != nil {
		t.Fatalf("ListIssueChildren(childless): %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("childless children = %v, want empty non-nil slice", empty)
	}
}
