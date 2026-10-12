package service

// Convert issue <-> sub-issue service tests (C24T1). Demote makes a
// standalone issue a sub-issue of a same-project parent (max depth 1,
// no children allowed on the issue, cycle-guarded); promote clears the
// parent. Real test database, no skips. Reuses the parent harness from
// issue_parent_test.go (setupParentTest, createTestIssue,
// addTestMember, RoleGuest, uniqueTestEmail).

import (
	"context"
	"errors"
	"testing"
)

func TestConvertDemoteHappyPath(t *testing.T) {
	fx := setupParentTest(t, "cv-demote")
	ctx := context.Background()

	parent := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Parent")
	child := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Child")

	res, err := ConvertIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, child.ID, fx.actor, strptr(parent.ID))
	if err != nil {
		t.Fatalf("ConvertIssue demote: %v", err)
	}
	if !res.Converted {
		t.Fatal("Converted = false, want true")
	}
	if res.Issue == nil || res.Issue.ID != child.ID {
		t.Fatalf("issue = %+v, want the converted issue", res.Issue)
	}
	if res.Issue.ParentID == nil || *res.Issue.ParentID != parent.ID {
		t.Fatalf("issue.parent_id = %v, want %s", res.Issue.ParentID, parent.ID)
	}
	if res.Issue.DisplayID == "" {
		t.Fatal("issue.display_id is empty")
	}

	// Activity: "_converted" entry on the issue mentioning the parent's
	// display id, and a matching entry on the parent mentioning the
	// child's display id.
	if n := countActivities(t, fx.pool, child.ID, "_converted"); n != 1 {
		t.Fatalf("_converted activity rows on child = %d, want 1", n)
	}
	if n := countActivities(t, fx.pool, parent.ID, "_converted"); n != 1 {
		t.Fatalf("_converted activity rows on parent = %d, want 1", n)
	}
}

func TestConvertPromoteHappyPath(t *testing.T) {
	fx := setupParentTest(t, "cv-promote")
	ctx := context.Background()

	parent := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Parent")
	child := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Child")
	if _, err := SetParent(ctx, fx.pool, fx.wsSlug, fx.identifier, child.ID, fx.actor, parent.ID); err != nil {
		t.Fatalf("setup SetParent: %v", err)
	}

	res, err := ConvertIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, child.ID, fx.actor, nil)
	if err != nil {
		t.Fatalf("ConvertIssue promote: %v", err)
	}
	if !res.Converted {
		t.Fatal("Converted = false, want true")
	}
	if res.Issue.ParentID != nil {
		t.Fatalf("issue.parent_id = %v, want nil", res.Issue.ParentID)
	}
	if n := countActivities(t, fx.pool, child.ID, "_converted"); n != 1 {
		t.Fatalf("_converted activity rows = %d, want 1", n)
	}
}

func TestConvertDemoteNoOp(t *testing.T) {
	fx := setupParentTest(t, "cv-demote-noop")
	ctx := context.Background()

	parent := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Parent")
	child := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Child")
	if _, err := SetParent(ctx, fx.pool, fx.wsSlug, fx.identifier, child.ID, fx.actor, parent.ID); err != nil {
		t.Fatalf("setup SetParent: %v", err)
	}

	res, err := ConvertIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, child.ID, fx.actor, strptr(parent.ID))
	if err != nil {
		t.Fatalf("ConvertIssue demote no-op: %v", err)
	}
	if res.Converted {
		t.Fatal("Converted = true, want false (already a sub-issue of the parent)")
	}
	if res.Issue.ParentID == nil || *res.Issue.ParentID != parent.ID {
		t.Fatalf("issue.parent_id = %v, want %s", res.Issue.ParentID, parent.ID)
	}
	if n := countActivities(t, fx.pool, child.ID, "_converted"); n != 0 {
		t.Fatalf("_converted activity rows after no-op = %d, want 0", n)
	}
}

func TestConvertPromoteNoOp(t *testing.T) {
	fx := setupParentTest(t, "cv-promote-noop")
	ctx := context.Background()

	iss := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Standalone")

	res, err := ConvertIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, iss.ID, fx.actor, nil)
	if err != nil {
		t.Fatalf("ConvertIssue promote no-op: %v", err)
	}
	if res.Converted {
		t.Fatal("Converted = true, want false (already standalone)")
	}
	if n := countActivities(t, fx.pool, iss.ID, "_converted"); n != 0 {
		t.Fatalf("_converted activity rows after no-op = %d, want 0", n)
	}
}

func TestConvertDemoteSelf(t *testing.T) {
	fx := setupParentTest(t, "cv-self")
	ctx := context.Background()

	iss := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Lonely")
	if _, err := ConvertIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, iss.ID, fx.actor, strptr(iss.ID)); !errors.Is(err, ErrIssueSelfParent) {
		t.Fatalf("self-parent err = %v, want ErrIssueSelfParent", err)
	}
}

func TestConvertDemoteUnknownParent(t *testing.T) {
	fx := setupParentTest(t, "cv-unknown-parent")
	ctx := context.Background()

	iss := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Child")
	if _, err := ConvertIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, iss.ID, fx.actor, strptr("00000000-0000-0000-0000-000000000000")); !errors.Is(err, ErrParentNotFound) {
		t.Fatalf("unknown parent err = %v, want ErrParentNotFound", err)
	}
}

func TestConvertDemoteCrossProject(t *testing.T) {
	fx := setupParentTest(t, "cv-xproj")
	ctx := context.Background()

	other := createTestProject(t, fx.pool, fx.wsSlug, fx.actor, "Design", uniqueTestIdentifier())
	foreign := createTestIssue(t, fx.pool, fx.wsSlug, other.Identifier, fx.actor, "Foreign parent")
	child := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Child")

	if _, err := ConvertIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, child.ID, fx.actor, strptr(foreign.ID)); !errors.Is(err, ErrConvertCrossProject) {
		t.Fatalf("cross-project err = %v, want ErrConvertCrossProject", err)
	}
}

func TestConvertDemoteCycle(t *testing.T) {
	fx := setupParentTest(t, "cv-cycle")
	ctx := context.Background()

	a := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "A")
	b := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "B")
	c := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "C")

	// Chain: B child of A, C child of B; then soft-delete B so A has no
	// LIVE children (children-block passes) while the parent_id graph
	// still records B -> A and C -> B. Demoting A under C would close the
	// cycle A -> C -> B -> A.
	if _, err := SetParent(ctx, fx.pool, fx.wsSlug, fx.identifier, b.ID, fx.actor, a.ID); err != nil {
		t.Fatalf("SetParent B->A: %v", err)
	}
	if _, err := SetParent(ctx, fx.pool, fx.wsSlug, fx.identifier, c.ID, fx.actor, b.ID); err != nil {
		t.Fatalf("SetParent C->B: %v", err)
	}
	if _, err := fx.pool.Exec(ctx, `UPDATE issues SET deleted_at = now() WHERE id = $1::uuid`, b.ID); err != nil {
		t.Fatalf("soft-delete B: %v", err)
	}

	if _, err := ConvertIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, a.ID, fx.actor, strptr(c.ID)); !errors.Is(err, ErrIssueCyclicParent) {
		t.Fatalf("cycle err = %v, want ErrIssueCyclicParent", err)
	}
}

func TestConvertDemoteDepthOne(t *testing.T) {
	fx := setupParentTest(t, "cv-depth")
	ctx := context.Background()

	top := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Top")
	mid := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Mid")
	leaf := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Leaf")
	if _, err := SetParent(ctx, fx.pool, fx.wsSlug, fx.identifier, mid.ID, fx.actor, top.ID); err != nil {
		t.Fatalf("SetParent mid->top: %v", err)
	}

	// mid is itself a sub-issue: it cannot be a convert parent (max depth 1).
	if _, err := ConvertIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, leaf.ID, fx.actor, strptr(mid.ID)); !errors.Is(err, ErrConvertParentIsSubIssue) {
		t.Fatalf("depth err = %v, want ErrConvertParentIsSubIssue", err)
	}
}

func TestConvertDemoteChildrenBlock(t *testing.T) {
	fx := setupParentTest(t, "cv-children")
	ctx := context.Background()

	parent := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Parent")
	child := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Child")
	grandchild := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Grandchild")
	if _, err := SetParent(ctx, fx.pool, fx.wsSlug, fx.identifier, grandchild.ID, fx.actor, child.ID); err != nil {
		t.Fatalf("SetParent grandchild->child: %v", err)
	}

	// child has its own sub-issue: converting it under parent is rejected.
	if _, err := ConvertIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, child.ID, fx.actor, strptr(parent.ID)); !errors.Is(err, ErrConvertHasChildren) {
		t.Fatalf("children err = %v, want ErrConvertHasChildren", err)
	}
}

func TestConvertUnknownIssue(t *testing.T) {
	fx := setupParentTest(t, "cv-unknown-issue")
	ctx := context.Background()

	if _, err := ConvertIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, "00000000-0000-0000-0000-000000000000", fx.actor, nil); !errors.Is(err, ErrIssueNotFound) {
		t.Fatalf("unknown issue err = %v, want ErrIssueNotFound", err)
	}
}

func TestConvertGuestForbidden(t *testing.T) {
	fx := setupParentTest(t, "cv-guest")
	ctx := context.Background()

	guest := createTestUser(t, fx.pool, uniqueTestEmail("convert-guest"))
	addTestMember(t, fx.pool, fx.wsSlug, fx.actor, guest, RoleGuest)
	parent := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Parent")
	iss := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Child")

	if _, err := ConvertIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, iss.ID, guest, strptr(parent.ID)); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest demote err = %v, want ErrForbidden", err)
	}
	if _, err := ConvertIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, iss.ID, guest, nil); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest promote err = %v, want ErrForbidden", err)
	}
}
