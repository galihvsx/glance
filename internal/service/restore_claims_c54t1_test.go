package service

// Restore-mechanism pins (C54T1): the issue-version restore undo claim
// (openapi.yaml: version restore description) and its one proven
// exception — a sort-order rebalance writes no version, so after one
// the latest version's sort_order predates the live row.
//
// Page-restore pre-restore snapshotting is already pinned by
// TestPageRestore (page_test.go); the trash-restore trail (activity
// row + version snapshot + webhook + WS broadcast) is already pinned
// by TestRestoreIssueClearsDeletedAtAndWritesTrail
// (trash_restore_c52t0_test.go) and the bulk trail by
// TestBulkRestoreTrail (trash_bulk_restore_c52t1_test.go). Those are
// not duplicated here.

import (
	"context"
	"encoding/json"
	"testing"
)

// TestIssueVersionRestoreUndoableViaLatestVersion: restoring v1 lands
// as a new version (v3), and restoring v2 — the version that was
// latest at restore time, i.e. the pre-restore state — returns the
// live row to exactly that pre-restore state (v4).
func TestIssueVersionRestoreUndoableViaLatestVersion(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	wsSlug, ident, issueID, actor := testSatelliteFixture(t, pool, "c54t1-undo")

	renamed := "Renamed"
	if _, err := UpdateIssue(ctx, pool, wsSlug, ident, issueID, actor, IssuePatch{Name: &renamed}); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	vs, err := ListVersions(ctx, pool, wsSlug, ident, issueID, actor)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(vs) != 2 {
		t.Fatalf("versions after patch = %d, want 2", len(vs))
	}

	// Restore v1: live row returns to the v1 name, as a NEW version v3.
	restored, err := RestoreIssueVersion(ctx, pool, wsSlug, ident, issueID, actor, 1)
	if err != nil {
		t.Fatalf("RestoreIssueVersion v1: %v", err)
	}
	if restored.Name == renamed {
		t.Fatalf("restored name = %q, want the v1 name", restored.Name)
	}
	preRestoreName := renamed // v2, latest at restore time, = pre-restore state

	// Undo: restore v2 (the pre-restore state) → v4, live row back to
	// the pre-restore name.
	undone, err := RestoreIssueVersion(ctx, pool, wsSlug, ident, issueID, actor, 2)
	if err != nil {
		t.Fatalf("RestoreIssueVersion v2 (undo): %v", err)
	}
	if undone.Name != preRestoreName {
		t.Fatalf("undone name = %q, want pre-restore %q", undone.Name, preRestoreName)
	}
	vs, err = ListVersions(ctx, pool, wsSlug, ident, issueID, actor)
	if err != nil {
		t.Fatalf("ListVersions after undo: %v", err)
	}
	if len(vs) != 4 || vs[3].VersionNo != 4 {
		t.Fatalf("versions after undo = %d, want 4 (v1 v2 v3 v4)", len(vs))
	}
	var v4 Issue
	if err := json.Unmarshal(vs[3].Snapshot, &v4); err != nil {
		t.Fatalf("decode v4 snapshot: %v", err)
	}
	if v4.Name != preRestoreName {
		t.Fatalf("v4 snapshot name = %q, want pre-restore %q", v4.Name, preRestoreName)
	}
}

// TestIssueVersionLatestVersionStaleAfterRebalance: RebalanceSortOrder
// rewrites sort_order with no version snapshot, so the latest version's
// sort_order predates the live row — the documented exception to the
// "pre-restore state is already the latest version" claim. Restoring
// that latest version reverts sort_order to the stale value.
func TestIssueVersionLatestVersionStaleAfterRebalance(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	creator := createTestUser(t, pool, uniqueTestEmail("c54t1-rebalance"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("c54t1-rebalance"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())
	a := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "A")
	createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "B")

	// PATCH sort_order to a value no rebalance can produce
	// (rebalance assigns (i+1)*1024), snapshotting it as A's latest
	// version.
	dense := 12345.0
	if _, err := UpdateIssue(ctx, pool, ws.Slug, p.Identifier, a.ID, creator, IssuePatch{SortOrder: &dense}); err != nil {
		t.Fatalf("UpdateIssue sort_order: %v", err)
	}

	n, err := RebalanceSortOrder(ctx, pool, ws.Slug, p.Identifier, creator, a.StateID)
	if err != nil {
		t.Fatalf("RebalanceSortOrder: %v", err)
	}
	if n != 2 {
		t.Fatalf("rebalanced = %d, want 2", n)
	}
	live, err := GetIssue(ctx, pool, ws.Slug, p.Identifier, a.ID, creator)
	if err != nil {
		t.Fatalf("GetIssue after rebalance: %v", err)
	}
	if live.SortOrder == dense {
		t.Fatalf("live sort_order = %v after rebalance, want != %v", live.SortOrder, dense)
	}
	vs, err := ListVersions(ctx, pool, ws.Slug, p.Identifier, a.ID, creator)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(vs) != 2 {
		t.Fatalf("versions after rebalance = %d, want 2 (rebalance writes none)", len(vs))
	}
	var latest Issue
	if err := json.Unmarshal(vs[len(vs)-1].Snapshot, &latest); err != nil {
		t.Fatalf("decode latest snapshot: %v", err)
	}
	if latest.SortOrder != dense {
		t.Fatalf("latest snapshot sort_order = %v, want stale pre-rebalance %v", latest.SortOrder, dense)
	}
	if latest.SortOrder == live.SortOrder {
		t.Fatalf("latest snapshot sort_order == live %v, want stale (predates rebalance)", live.SortOrder)
	}

	// Restoring the stale latest version reverts sort_order to the
	// pre-rebalance value.
	restored, err := RestoreIssueVersion(ctx, pool, ws.Slug, p.Identifier, a.ID, creator, vs[len(vs)-1].VersionNo)
	if err != nil {
		t.Fatalf("RestoreIssueVersion latest: %v", err)
	}
	if restored.SortOrder != dense {
		t.Fatalf("restored sort_order = %v, want reverted %v", restored.SortOrder, dense)
	}
}
