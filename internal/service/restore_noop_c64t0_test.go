package service

// No-op version-restore rejection (C64T0): a restore whose snapshot
// content is identical to the live row must NOT mint a new version row
// or a _restored activity marker for an event that never happened.
// Both reachable no-op paths pin ErrVersionRestoreNoOp:
//   1. restoring the latest version (snapshot == live row), and
//   2. restoring an older version whose content equals the current row
//      (A→B→A histories).

import (
	"context"
	"errors"
	"testing"
)

func TestRestoreVersionNoOpRejected(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	wsSlug, ident, issueID, actor := testSatelliteFixture(t, pool, "noop")

	// v1 = create ("Satellite host"); v2 = rename; restore v1 → v3,
	// live row back to the v1 name. v1's snapshot now equals the live row.
	renamed := "Renamed"
	if _, err := UpdateIssue(ctx, pool, wsSlug, ident, issueID, actor, IssuePatch{Name: &renamed}); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	if _, err := RestoreIssueVersion(ctx, pool, wsSlug, ident, issueID, actor, 1); err != nil {
		t.Fatalf("RestoreIssueVersion v1: %v", err)
	}
	restored, err := GetIssue(ctx, pool, wsSlug, ident, issueID, actor)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if restored.Name == renamed {
		t.Fatalf("live name = %q, want the v1 name after restore", renamed)
	}
	vs, err := ListVersions(ctx, pool, wsSlug, ident, issueID, actor)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(vs) != 3 {
		t.Fatalf("versions before no-op restores = %d, want 3", len(vs))
	}
	if n := countActivities(t, pool, issueID, "_restored"); n != 1 {
		t.Fatalf("_restored rows before no-op restores = %d, want 1", n)
	}

	// Path 1: restore the latest version (v3 == live row) — no-op.
	if _, err := RestoreIssueVersion(ctx, pool, wsSlug, ident, issueID, actor, 3); !errors.Is(err, ErrVersionRestoreNoOp) {
		t.Fatalf("restore of latest version: err = %v, want ErrVersionRestoreNoOp", err)
	}
	// Path 2: restore the old v1 (A→B→A: v1 content == live row) — no-op.
	if _, err := RestoreIssueVersion(ctx, pool, wsSlug, ident, issueID, actor, 1); !errors.Is(err, ErrVersionRestoreNoOp) {
		t.Fatalf("restore of stale-but-identical v1: err = %v, want ErrVersionRestoreNoOp", err)
	}

	// Neither no-op restore may write: version count frozen, no new
	// _restored marker, live row untouched.
	vs, err = ListVersions(ctx, pool, wsSlug, ident, issueID, actor)
	if err != nil {
		t.Fatalf("ListVersions after no-op restores: %v", err)
	}
	if len(vs) != 3 {
		t.Fatalf("versions after no-op restores = %d, want 3 (no version spam)", len(vs))
	}
	if n := countActivities(t, pool, issueID, "_restored"); n != 1 {
		t.Fatalf("_restored rows after no-op restores = %d, want 1 (no fake marker)", n)
	}
	live, err := GetIssue(ctx, pool, wsSlug, ident, issueID, actor)
	if err != nil {
		t.Fatalf("GetIssue after no-op restores: %v", err)
	}
	if live.Name != restored.Name {
		t.Fatalf("live name after no-op restores = %q, want unchanged %q", live.Name, restored.Name)
	}
}
