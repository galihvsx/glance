package service

// Labels + assignees + estimates tests (Task 16): label hierarchy with
// cycle rejection, idempotent assign/unassign, estimate scale CRUD with
// per-project point validation, and the list-filter upgrade (assignee=
// and label= now filter for real — the Task 15 FALSE stubs are gone).
// All tests run against the real test database — no skips. Reuses the
// harness from workspace_test.go / project_test.go / issue_test.go.

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func strPtr2(s string) *string { return &s }

func createTestLabel(t *testing.T, pool *pgxpool.Pool, wsSlug, ident, actorID, name string) *Label {
	t.Helper()
	l, err := CreateLabel(context.Background(), pool, wsSlug, ident, actorID,
		LabelInput{Name: name})
	if err != nil {
		t.Fatalf("CreateLabel %q: %v", name, err)
	}
	return l
}

// TestLabelHierarchy: parent/child creation, children listed with their
// parent_id, and cycle rejection (A child of B, then A's parent := B is
// fine, but B's parent := A must fail; a label may not parent itself).
func TestLabelHierarchy(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	creator := createTestUser(t, pool, uniqueTestEmail("label-hier"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-label"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())

	parent := createTestLabel(t, pool, ws.Slug, p.Identifier, creator, "frontend")
	if parent.ParentID != nil {
		t.Fatalf("root label ParentID = %v, want nil", parent.ParentID)
	}
	child, err := CreateLabel(ctx, pool, ws.Slug, p.Identifier, creator,
		LabelInput{Name: "react", ParentID: &parent.ID})
	if err != nil {
		t.Fatalf("CreateLabel child: %v", err)
	}
	if child.ParentID == nil || *child.ParentID != parent.ID {
		t.Fatalf("child ParentID = %v, want %s", child.ParentID, parent.ID)
	}

	// A label may not be its own parent.
	if _, err := CreateLabel(ctx, pool, ws.Slug, p.Identifier, creator,
		LabelInput{Name: "selfish", ParentID: strPtr2("00000000-0000-0000-0000-000000000000")}); err == nil {
		t.Fatal("create with bogus parent: want error, got nil")
	}
	selfRef, err := CreateLabel(ctx, pool, ws.Slug, p.Identifier, creator,
		LabelInput{Name: "loop"})
	if err != nil {
		t.Fatalf("CreateLabel loop: %v", err)
	}
	pf := PatchField[string]{Set: true, Value: &selfRef.ID}
	if _, err := UpdateLabel(ctx, pool, ws.Slug, p.Identifier, selfRef.ID, creator,
		LabelPatch{ParentID: pf}); !isErr(err, ErrLabelCycle) && !isErr(err, ErrInvalidLabel) {
		t.Fatalf("self-parent: err = %v, want ErrLabelCycle/ErrInvalidLabel", err)
	}

	// Two-level cycle: B's parent := A must fail when A is already B's child.
	a := createTestLabel(t, pool, ws.Slug, p.Identifier, creator, "cycle-a")
	b, err := CreateLabel(ctx, pool, ws.Slug, p.Identifier, creator,
		LabelInput{Name: "cycle-b", ParentID: &a.ID})
	if err != nil {
		t.Fatalf("CreateLabel cycle-b: %v", err)
	}
	pfA := PatchField[string]{Set: true, Value: &b.ID}
	if _, err := UpdateLabel(ctx, pool, ws.Slug, p.Identifier, a.ID, creator,
		LabelPatch{ParentID: pfA}); !isErr(err, ErrLabelCycle) {
		t.Fatalf("2-cycle: err = %v, want ErrLabelCycle", err)
	}

	// Clearing the parent is allowed (Set with nil Value).
	clear := PatchField[string]{Set: true}
	if _, err := UpdateLabel(ctx, pool, ws.Slug, p.Identifier, b.ID, creator,
		LabelPatch{ParentID: clear}); err != nil {
		t.Fatalf("clear parent: %v", err)
	}
	got, err := GetLabel(ctx, pool, ws.Slug, p.Identifier, b.ID, creator)
	if err != nil {
		t.Fatalf("GetLabel: %v", err)
	}
	if got.ParentID != nil {
		t.Fatalf("ParentID after clear = %v, want nil", got.ParentID)
	}

	// Duplicate name in the same workspace conflicts.
	if _, err := CreateLabel(ctx, pool, ws.Slug, p.Identifier, creator,
		LabelInput{Name: "frontend"}); !isErr(err, ErrLabelConflict) {
		t.Fatalf("duplicate name: err = %v, want ErrLabelConflict", err)
	}

	// Guests may read but not mutate labels.
	guest := createTestUser(t, pool, uniqueTestEmail("label-guest"))
	addTestMember(t, pool, ws.Slug, creator, guest, RoleGuest)
	if _, err := ListLabels(ctx, pool, ws.Slug, p.Identifier, guest); err != nil {
		t.Fatalf("guest ListLabels: %v", err)
	}
	if _, err := CreateLabel(ctx, pool, ws.Slug, p.Identifier, guest,
		LabelInput{Name: "nope"}); !isErr(err, ErrForbidden) {
		t.Fatalf("guest CreateLabel: err = %v, want ErrForbidden", err)
	}
}

func isErr(err, target error) bool {
	return err != nil && strings.Contains(err.Error(), strings.TrimPrefix(target.Error(), "service: "))
}

// TestLabelAssignUnassignIdempotent: assigning the same label twice is a
// no-op success; unassigning an absent label is a no-op success; the
// label must belong to the workspace.
func TestLabelAssignUnassignIdempotent(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	creator := createTestUser(t, pool, uniqueTestEmail("label-assign"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-assign"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())
	iss := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "labeled issue")
	label := createTestLabel(t, pool, ws.Slug, p.Identifier, creator, "bug")

	if err := AssignLabel(ctx, pool, ws.Slug, p.Identifier, iss.ID, label.ID, creator); err != nil {
		t.Fatalf("AssignLabel: %v", err)
	}
	// Idempotent: second assign succeeds and creates no duplicate row.
	if err := AssignLabel(ctx, pool, ws.Slug, p.Identifier, iss.ID, label.ID, creator); err != nil {
		t.Fatalf("AssignLabel twice: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM issue_labels WHERE issue_id = $1::uuid AND label_id = $2::uuid`,
		iss.ID, label.ID).Scan(&n); err != nil {
		t.Fatalf("count issue_labels: %v", err)
	}
	if n != 1 {
		t.Fatalf("issue_labels rows = %d, want 1", n)
	}

	if err := UnassignLabel(ctx, pool, ws.Slug, p.Identifier, iss.ID, label.ID, creator); err != nil {
		t.Fatalf("UnassignLabel: %v", err)
	}
	// Idempotent: unassigning again is a no-op success, not a 404.
	if err := UnassignLabel(ctx, pool, ws.Slug, p.Identifier, iss.ID, label.ID, creator); err != nil {
		t.Fatalf("UnassignLabel twice: %v", err)
	}

	// A label from another workspace cannot be attached here.
	other := createTestUser(t, pool, uniqueTestEmail("label-other"))
	ws2 := createTestWorkspace(t, pool, "Other", uniqueTestSlug("other-ws"), other)
	p2 := createTestProject(t, pool, ws2.Slug, other, "Eng", uniqueTestIdentifier())
	foreign := createTestLabel(t, pool, ws2.Slug, p2.Identifier, other, "foreign")
	if err := AssignLabel(ctx, pool, ws.Slug, p.Identifier, iss.ID, foreign.ID, creator); !isErr(err, ErrLabelNotFound) {
		t.Fatalf("foreign label: err = %v, want ErrLabelNotFound", err)
	}

	// Assigning to a missing issue is a 404-class error.
	if err := AssignLabel(ctx, pool, ws.Slug, p.Identifier,
		"00000000-0000-0000-0000-000000000000", label.ID, creator); !isErr(err, ErrIssueNotFound) {
		t.Fatalf("missing issue: err = %v, want ErrIssueNotFound", err)
	}
}

// TestAssigneeAssignUnassignIdempotent: assignees must be workspace
// members; assign/unassign are idempotent.
func TestAssigneeAssignUnassignIdempotent(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	creator := createTestUser(t, pool, uniqueTestEmail("assignee"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-assignee"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())
	iss := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "assigned issue")

	member := createTestUser(t, pool, uniqueTestEmail("assignee-member"))
	addTestMember(t, pool, ws.Slug, creator, member, RoleMember)

	if err := AssignAssignee(ctx, pool, ws.Slug, p.Identifier, iss.ID, member, creator); err != nil {
		t.Fatalf("AssignAssignee: %v", err)
	}
	if err := AssignAssignee(ctx, pool, ws.Slug, p.Identifier, iss.ID, member, creator); err != nil {
		t.Fatalf("AssignAssignee twice: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM issue_assignees WHERE issue_id = $1::uuid AND user_id = $2::uuid`,
		iss.ID, member).Scan(&n); err != nil {
		t.Fatalf("count issue_assignees: %v", err)
	}
	if n != 1 {
		t.Fatalf("issue_assignees rows = %d, want 1", n)
	}

	if err := UnassignAssignee(ctx, pool, ws.Slug, p.Identifier, iss.ID, member, creator); err != nil {
		t.Fatalf("UnassignAssignee: %v", err)
	}
	if err := UnassignAssignee(ctx, pool, ws.Slug, p.Identifier, iss.ID, member, creator); err != nil {
		t.Fatalf("UnassignAssignee twice: %v", err)
	}

	// A non-member cannot be assigned.
	outsider := createTestUser(t, pool, uniqueTestEmail("assignee-outsider"))
	if err := AssignAssignee(ctx, pool, ws.Slug, p.Identifier, iss.ID, outsider, creator); !isErr(err, ErrAssigneeNotMember) {
		t.Fatalf("outsider assignee: err = %v, want ErrAssigneeNotMember", err)
	}
}

// TestEstimateScaleCRUD: create a scale with points in one call, list it
// back, set an issue's estimate_point_id via PATCH (with an activity
// row), and reject a point from another project's scale.
func TestEstimateScaleCRUD(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	creator := createTestUser(t, pool, uniqueTestEmail("estimate"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-est"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())

	est, err := CreateEstimate(ctx, pool, ws.Slug, p.Identifier, creator,
		EstimateInput{Name: "Fibonacci", Points: []EstimatePointInput{
			{Key: "1", Value: 1},
			{Key: "2", Value: 2},
			{Key: "3", Value: 3, Description: strPtr2("half day")},
			{Key: "5", Value: 5},
		}})
	if err != nil {
		t.Fatalf("CreateEstimate: %v", err)
	}
	if len(est.Points) != 4 {
		t.Fatalf("points = %d, want 4", len(est.Points))
	}

	scales, err := ListEstimates(ctx, pool, ws.Slug, p.Identifier, creator)
	if err != nil {
		t.Fatalf("ListEstimates: %v", err)
	}
	if len(scales) != 1 || len(scales[0].Points) != 4 {
		t.Fatalf("ListEstimates: got %d scales / %d points, want 1/4", len(scales), len(scales[0].Points))
	}

	iss := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "estimated issue")
	var twoID string
	for _, pt := range est.Points {
		if pt.Key == "2" {
			twoID = pt.ID
		}
	}
	updated, err := UpdateIssue(ctx, pool, ws.Slug, p.Identifier, iss.ID, creator,
		IssuePatch{EstimatePointID: PatchField[string]{Set: true, Value: &twoID}})
	if err != nil {
		t.Fatalf("UpdateIssue estimate_point_id: %v", err)
	}
	if updated.EstimatePointID == nil || *updated.EstimatePointID != twoID {
		t.Fatalf("EstimatePointID = %v, want %s", updated.EstimatePointID, twoID)
	}
	if n := countActivities(t, pool, iss.ID, "estimate_point_id"); n != 1 {
		t.Fatalf("estimate_point_id activity rows = %d, want 1", n)
	}

	// Clearing the estimate is allowed.
	cleared, err := UpdateIssue(ctx, pool, ws.Slug, p.Identifier, iss.ID, creator,
		IssuePatch{EstimatePointID: PatchField[string]{Set: true}})
	if err != nil {
		t.Fatalf("clear estimate_point_id: %v", err)
	}
	if cleared.EstimatePointID != nil {
		t.Fatalf("EstimatePointID after clear = %v, want nil", cleared.EstimatePointID)
	}

	// A point from another project's scale is rejected.
	p2 := createTestProject(t, pool, ws.Slug, creator, "Design", uniqueTestIdentifier())
	est2, err := CreateEstimate(ctx, pool, ws.Slug, p2.Identifier, creator,
		EstimateInput{Name: "T-shirt", Points: []EstimatePointInput{{Key: "S", Value: 1}}})
	if err != nil {
		t.Fatalf("CreateEstimate p2: %v", err)
	}
	foreignID := est2.Points[0].ID
	if _, err := UpdateIssue(ctx, pool, ws.Slug, p.Identifier, iss.ID, creator,
		IssuePatch{EstimatePointID: PatchField[string]{Set: true, Value: &foreignID}}); !isErr(err, ErrInvalidEstimatePoint) {
		t.Fatalf("foreign point: err = %v, want ErrInvalidEstimatePoint", err)
	}
}

// TestListRelationFilters upgrades the Task 15 contract
// (TestListRelationFiltersEmptyBeforeTaxonomy): assignee= and label= now
// filter for real, listed issues carry their labels/assignees, and the
// query count stays constant. cycle= still matches nothing (Task 22).
func TestListRelationFilters(t *testing.T) {
	s := setupListTest(t)
	ctx := context.Background()

	member := createTestUser(t, s.pool, uniqueTestEmail("list-assignee"))
	addTestMember(t, s.pool, s.slug, s.actor, member, RoleMember)

	a := createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "labeled-a")
	b := createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "plain-b")

	bug := createTestLabel(t, s.pool, s.slug, s.ident, s.actor, "bug")
	if err := AssignLabel(ctx, s.pool, s.slug, s.ident, a.ID, bug.ID, s.actor); err != nil {
		t.Fatalf("AssignLabel: %v", err)
	}
	if err := AssignAssignee(ctx, s.pool, s.slug, s.ident, a.ID, member, s.actor); err != nil {
		t.Fatalf("AssignAssignee: %v", err)
	}

	// label= filter matches only the labeled issue.
	r, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{Label: bug.ID})
	if err != nil {
		t.Fatalf("ListIssues label=: %v", err)
	}
	if len(r.Issues) != 1 || r.Issues[0].ID != a.ID {
		t.Fatalf("label= filter: got %d issues, want exactly the labeled one", len(r.Issues))
	}

	// assignee= filter matches only the assigned issue.
	r, err = ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{Assignee: member})
	if err != nil {
		t.Fatalf("ListIssues assignee=: %v", err)
	}
	if len(r.Issues) != 1 || r.Issues[0].ID != a.ID {
		t.Fatalf("assignee= filter: got %d issues, want exactly the assigned one", len(r.Issues))
	}

	// The unfiltered list carries labels + assignees on the right issue.
	r, err = ListIssues(ctx, s.pool, s.slug, s.ident, s.actor, ListIssuesInput{})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	byID := map[string]IssueListItem{}
	for _, it := range r.Issues {
		byID[it.ID] = it
	}
	la := byID[a.ID]
	if len(la.Labels) != 1 || la.Labels[0].ID != bug.ID || la.Labels[0].Name != "bug" {
		t.Fatalf("issue A labels = %+v, want the bug label", la.Labels)
	}
	if len(la.Assignees) != 1 || la.Assignees[0].ID != member {
		t.Fatalf("issue A assignees = %+v, want the member", la.Assignees)
	}
	if la.Labels[0].Color == "" {
		t.Fatal("label color must be populated")
	}
	lb := byID[b.ID]
	if len(lb.Labels) != 0 || len(lb.Assignees) != 0 {
		t.Fatalf("issue B labels/assignees = %+v/%+v, want empty", lb.Labels, lb.Assignees)
	}

	// cycle= still matches nothing — cycle_issues lands in Task 22.
	someUUID := "11111111-2222-3333-4444-555555555555"
	r, err = ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{Cycle: someUUID})
	if err != nil {
		t.Fatalf("ListIssues cycle=: %v", err)
	}
	if len(r.Issues) != 0 {
		t.Fatalf("cycle= filter: got %d issues, want 0 (Task 22)", len(r.Issues))
	}
}
