package service

// Subtask progress rollup tests (C17T2): GET issue detail and the issue
// list carry a computed subtask_progress {total, done} — direct children
// only, done = state group 'completed', one aggregate subquery (no N+1).
// Real test database, no skips. Reuses the harness from workspace_test.go
// and issue_test.go (newTestPool, migrateTestDB, createTestUser,
// createTestWorkspace, uniqueTestEmail/Slug/Identifier, createTestProject,
// createTestIssue) and cycle_test.go (stateIDByGroup).

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

type rollupFixture struct {
	pool       *pgxpool.Pool
	wsSlug     string
	identifier string
	projectID  string
	actor      string
}

func setupRollupTest(t *testing.T, tag string) rollupFixture {
	t.Helper()
	pool := newTestPool(t)
	migrateTestDB(t, pool)

	actor := createTestUser(t, pool, uniqueTestEmail("rollup-"+tag))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-rollup-"+tag), actor)
	proj := createTestProject(t, pool, ws.Slug, actor, "Engineering", uniqueTestIdentifier())
	return rollupFixture{pool: pool, wsSlug: ws.Slug, identifier: proj.Identifier, projectID: proj.ID, actor: actor}
}

// moveToGroup puts an issue into the project's first state of the given
// group (e.g. 'completed').
func moveToGroup(t *testing.T, fx rollupFixture, issueID, group string) {
	t.Helper()
	stateID := stateIDByGroup(t, fx.pool, fx.projectID, group)
	if _, err := UpdateIssue(context.Background(), fx.pool, fx.wsSlug, fx.identifier, issueID, fx.actor,
		IssuePatch{StateID: &stateID}); err != nil {
		t.Fatalf("UpdateIssue to %s: %v", group, err)
	}
}

func assertProgress(t *testing.T, got *SubtaskProgress, total, done int, where string) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s: subtask_progress is nil, want {total:%d, done:%d}", where, total, done)
	}
	if got.Total != total || got.Done != done {
		t.Fatalf("%s: subtask_progress = {total:%d, done:%d}, want {total:%d, done:%d}",
			where, got.Total, got.Done, total, done)
	}
}

// Detail counts direct children only: a grandchild of the parent does not
// move the parent's rollup, and a cancelled-group child counts toward the
// total but NOT toward done.
func TestSubtaskRollupDetailMixedAndNested(t *testing.T) {
	fx := setupRollupTest(t, "detail")
	ctx := context.Background()

	parent := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Parent")
	doneChild := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Done child")
	moveToGroup(t, fx, doneChild.ID, "completed")
	cancelledChild := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Cancelled child")
	moveToGroup(t, fx, cancelledChild.ID, "cancelled")
	openChild := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Open child")
	grandchild := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Grandchild")
	// Attach: openChild is a direct child of parent, grandchild under openChild.
	if _, err := SetParent(ctx, fx.pool, fx.wsSlug, fx.identifier, doneChild.ID, fx.actor, parent.ID); err != nil {
		t.Fatalf("SetParent doneChild: %v", err)
	}
	if _, err := SetParent(ctx, fx.pool, fx.wsSlug, fx.identifier, cancelledChild.ID, fx.actor, parent.ID); err != nil {
		t.Fatalf("SetParent cancelledChild: %v", err)
	}
	if _, err := SetParent(ctx, fx.pool, fx.wsSlug, fx.identifier, openChild.ID, fx.actor, parent.ID); err != nil {
		t.Fatalf("SetParent openChild: %v", err)
	}
	if _, err := SetParent(ctx, fx.pool, fx.wsSlug, fx.identifier, grandchild.ID, fx.actor, openChild.ID); err != nil {
		t.Fatalf("SetParent grandchild: %v", err)
	}

	got, err := GetIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, parent.ID, fx.actor)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	// 3 direct children; only the completed-group one counts as done.
	assertProgress(t, got.SubtaskProgress, 3, 1, "parent")

	// The middle issue sees its own direct child only.
	mid, err := GetIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, openChild.ID, fx.actor)
	if err != nil {
		t.Fatalf("GetIssue openChild: %v", err)
	}
	assertProgress(t, mid.SubtaskProgress, 1, 0, "openChild")
}

// A childless issue reports {total:0, done:0} — never nil, never an error.
func TestSubtaskRollupDetailEmpty(t *testing.T) {
	fx := setupRollupTest(t, "empty")
	ctx := context.Background()

	iss := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Lonely")
	got, err := GetIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, iss.ID, fx.actor)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	assertProgress(t, got.SubtaskProgress, 0, 0, "lonely")
}

// The list query populates the rollup for every row in the same single
// query (no extra round-trips per issue).
func TestSubtaskRollupList(t *testing.T) {
	fx := setupRollupTest(t, "list")
	ctx := context.Background()

	parent := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "List parent")
	child := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "List child")
	moveToGroup(t, fx, child.ID, "completed")
	if _, err := SetParent(ctx, fx.pool, fx.wsSlug, fx.identifier, child.ID, fx.actor, parent.ID); err != nil {
		t.Fatalf("SetParent: %v", err)
	}
	other := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "No kids")

	res, err := ListIssues(ctx, fx.pool, fx.wsSlug, fx.identifier, fx.actor, ListIssuesInput{PerPage: 50})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	byID := map[string]*SubtaskProgress{}
	for i := range res.Issues {
		byID[res.Issues[i].ID] = res.Issues[i].SubtaskProgress
	}
	assertProgress(t, byID[parent.ID], 1, 1, "list parent")
	assertProgress(t, byID[child.ID], 0, 0, "list child")
	assertProgress(t, byID[other.ID], 0, 0, "list other")
}
