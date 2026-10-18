package service

// C64T1 no-op snapshot-guard audit: restoreIssueTx (trash restore) and
// MergeIssue must not mint duplicate-content version rows when the
// mutation leaves the row content identical to the issue's latest
// version. deleteIssueTx snapshots the row at delete time and
// deleted_at is not in the Issue struct, so a plain delete→restore
// pair snapshots identical content twice; merging a source that is
// already in the cancelled state re-stamps only updated_at. Both are
// the same duplicate class as the C64T0 no-op version restore. The
// events themselves stay recorded: the _restored / _merged activity
// rows and the issue.updated webhooks are untouched. Real test
// database, no skips.

import (
	"context"
	"testing"
)

// TestTrashRestoreSkipsDuplicateSnapshot: delete→restore with no
// parent clear and no intervening change must not grow the version
// history — the restored row's content is identical to the latest
// (delete-time) version. The restore itself still succeeds and the
// _restored trail is still written.
func TestTrashRestoreSkipsDuplicateSnapshot(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	creator := createTestUser(t, pool, uniqueTestEmail("c64t1-restore"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("c64t1-restore"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())
	iss := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "Restore me")

	if err := DeleteIssue(ctx, pool, ws.Slug, p.Identifier, iss.ID, creator); err != nil {
		t.Fatalf("DeleteIssue: %v", err)
	}
	versionsBefore := countVersions(t, pool, iss.ID)
	if versionsBefore != 2 {
		t.Fatalf("versions after create+delete = %d, want 2 (v1 create, v2 delete)", versionsBefore)
	}

	restored, err := RestoreIssue(ctx, pool, ws.Slug, p.Identifier, iss.ID, creator)
	if err != nil {
		t.Fatalf("RestoreIssue: %v", err)
	}
	if restored.ID != iss.ID {
		t.Fatalf("restored id = %s, want %s", restored.ID, iss.ID)
	}
	if trashIssueStillDeleted(t, pool, iss.ID) {
		t.Fatalf("issue still has deleted_at after restore")
	}
	// No duplicate-content version: the restore event's trail is the
	// _restored activity row, not a version.
	if n := countVersions(t, pool, iss.ID); n != versionsBefore {
		t.Fatalf("version rows = %d, want %d (restore with identical content snapshots nothing)", n, versionsBefore)
	}
	if n := countActivities(t, pool, iss.ID, "_restored"); n != 1 {
		t.Fatalf("_restored rows = %d, want 1 (the restore event is still recorded)", n)
	}
}

// TestTrashRestoreWithParentClearStillSnapshots: when the restore
// clears a stale (deleted) parent, the row content really changed, so
// the version snapshot must still be written — the guard only skips
// content-identical writes.
func TestTrashRestoreWithParentClearStillSnapshots(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	creator := createTestUser(t, pool, uniqueTestEmail("c64t1-restore-pc"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("c64t1-restore-pc"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())
	parent := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "Parent")
	child, err := CreateIssue(ctx, pool, ws.Slug, p.Identifier, creator,
		CreateIssueInput{Name: "Child", ParentID: &parent.ID})
	if err != nil {
		t.Fatalf("CreateIssue child: %v", err)
	}
	if err := DeleteIssue(ctx, pool, ws.Slug, p.Identifier, child.ID, creator); err != nil {
		t.Fatalf("DeleteIssue child: %v", err)
	}
	if err := DeleteIssue(ctx, pool, ws.Slug, p.Identifier, parent.ID, creator); err != nil {
		t.Fatalf("DeleteIssue parent: %v", err)
	}
	versionsBefore := countVersions(t, pool, child.ID)

	restored, err := RestoreIssue(ctx, pool, ws.Slug, p.Identifier, child.ID, creator)
	if err != nil {
		t.Fatalf("RestoreIssue child: %v", err)
	}
	if restored.ParentID != nil {
		t.Fatalf("restored ParentID = %v, want nil (stale parent cleared)", *restored.ParentID)
	}
	if n := countVersions(t, pool, child.ID); n != versionsBefore+1 {
		t.Fatalf("version rows = %d, want %d (parent-clear restore changed content: snapshot it)", n, versionsBefore+1)
	}
}

// TestTrashRestoreAfterStateReassignStillSnapshots: DeleteState with
// reassign_to moves even soft-deleted issues to the surviving state,
// so a later restore lands on changed content and must snapshot.
func TestTrashRestoreAfterStateReassignStillSnapshots(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	creator := createTestUser(t, pool, uniqueTestEmail("c64t1-restore-rs"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("c64t1-restore-rs"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())

	oldState := backlogStateID(t, pool, ws.Slug, p.Identifier, creator)
	survivor, err := CreateState(ctx, pool, ws.Slug, p.Identifier, creator,
		StateInput{Name: "Survivor", Group: "backlog"})
	if err != nil {
		t.Fatalf("CreateState: %v", err)
	}
	iss := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "Reassigned")
	if iss.StateID != oldState {
		t.Fatalf("issue state = %s, want backlog %s", iss.StateID, oldState)
	}
	if err := DeleteIssue(ctx, pool, ws.Slug, p.Identifier, iss.ID, creator); err != nil {
		t.Fatalf("DeleteIssue: %v", err)
	}
	if err := DeleteState(ctx, pool, ws.Slug, p.Identifier, oldState, creator, &survivor.ID); err != nil {
		t.Fatalf("DeleteState with reassign: %v", err)
	}
	versionsBefore := countVersions(t, pool, iss.ID)

	restored, err := RestoreIssue(ctx, pool, ws.Slug, p.Identifier, iss.ID, creator)
	if err != nil {
		t.Fatalf("RestoreIssue after reassign: %v", err)
	}
	if restored.StateID != survivor.ID {
		t.Fatalf("restored state = %s, want reassigned %s", restored.StateID, survivor.ID)
	}
	if n := countVersions(t, pool, iss.ID); n != versionsBefore+1 {
		t.Fatalf("version rows = %d, want %d (state changed while deleted: snapshot it)", n, versionsBefore+1)
	}
}

// TestMergeAlreadyCancelledSourceSkipsDuplicateSnapshot: merging a
// source that is already in the cancelled state changes no row
// content (the UPDATE only re-stamps updated_at), so no new version
// may be written. The merge itself still succeeds: the new duplicate
// relation and the _merged activity rows are still recorded.
func TestMergeAlreadyCancelledSourceSkipsDuplicateSnapshot(t *testing.T) {
	fx := setupParentTest(t, "c64t1-merge")
	ctx := context.Background()

	source := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Dup A")
	target := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Original")
	target2 := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Original 2")

	res, err := MergeIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, source.ID, fx.actor, target.ID)
	if err != nil {
		t.Fatalf("first MergeIssue: %v", err)
	}
	if !res.Merged {
		t.Fatalf("first merge result = %+v, want Merged:true", res)
	}
	if g := mergeTestIssueStateGroup(t, fx.pool, ctx, source.ID); g != "cancelled" {
		t.Fatalf("source state group = %q, want cancelled", g)
	}
	versionsBefore := countVersions(t, fx.pool, source.ID)
	if versionsBefore != 2 {
		t.Fatalf("versions after create+merge = %d, want 2", versionsBefore)
	}

	// Merge the already-cancelled source into a NEW target: the merge
	// proceeds (new duplicate relation) but the row content is
	// unchanged.
	res2, err := MergeIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, source.ID, fx.actor, target2.ID)
	if err != nil {
		t.Fatalf("second MergeIssue: %v", err)
	}
	if !res2.Merged || res2.AlreadyMerged {
		t.Fatalf("second merge result = %+v, want {Merged:true AlreadyMerged:false}", res2)
	}
	if n := countVersions(t, fx.pool, source.ID); n != versionsBefore {
		t.Fatalf("version rows = %d, want %d (already-cancelled merge changes no content: snapshot nothing)", n, versionsBefore)
	}
	// The merge event is still recorded: a second duplicate relation
	// and _merged activity rows on both sides.
	if n := mergeTestCount(t, fx.pool, ctx,
		`SELECT COUNT(*) FROM issue_relations WHERE type = 'duplicate'
		  AND (issue_id = $1::uuid OR related_issue_id = $1::uuid)`,
		source.ID); n != 2 {
		t.Fatalf("duplicate relations touching source = %d, want 2", n)
	}
	if n := countActivities(t, fx.pool, source.ID, "_merged"); n != 2 {
		t.Fatalf("source _merged rows = %d, want 2 (one per merge)", n)
	}
}

// TestMergeRealStateChangeStillSnapshots: a normal merge (source not
// yet cancelled) changes state_id, so the version snapshot must still
// be written — the guard only skips content-identical writes.
func TestMergeRealStateChangeStillSnapshots(t *testing.T) {
	fx := setupParentTest(t, "c64t1-merge2")
	ctx := context.Background()

	source := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Dup B")
	target := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Original B")
	versionsBefore := countVersions(t, fx.pool, source.ID)

	res, err := MergeIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, source.ID, fx.actor, target.ID)
	if err != nil {
		t.Fatalf("MergeIssue: %v", err)
	}
	if !res.Merged {
		t.Fatalf("merge result = %+v, want Merged:true", res)
	}
	if n := countVersions(t, fx.pool, source.ID); n != versionsBefore+1 {
		t.Fatalf("version rows = %d, want %d (real state change: snapshot it)", n, versionsBefore+1)
	}
}
