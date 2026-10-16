package service

// Release progress snapshots (C38T0): the live six-group snapshot on
// List/Get, the all-zero snapshot on empty releases, and the
// progressCounts fold from web/src/pages/Releases.tsx (triage+backlog+
// unstarted → open, started → in progress, completed+cancelled → done).

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// stateIDForGroup returns the id of the project's first state in the
// given group, via the same ListStates the endpoints use.
func stateIDForGroup(t *testing.T, pool *pgxpool.Pool, slug, ident, actor, group string) string {
	t.Helper()
	states, err := ListStates(context.Background(), pool, slug, ident, actor)
	if err != nil {
		t.Fatalf("ListStates: %v", err)
	}
	for _, s := range states {
		if s.Group == group {
			return s.ID
		}
	}
	t.Fatalf("no state in group %q", group)
	return ""
}

// foldProgressCounts mirrors progressCounts in Releases.tsx: the client
// folds the six snapshot groups into open / inProgress / done.
func foldProgressCounts(snap map[string]int64) (open, inProgress, done int64) {
	open = snap["triage"] + snap["backlog"] + snap["unstarted"]
	inProgress = snap["started"]
	done = snap["completed"] + snap["cancelled"]
	return open, inProgress, done
}

func TestReleaseProgressSnapshot(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	actor := createTestUser(t, pool, uniqueTestEmail("release-snap"))
	slug := uniqueTestSlug("release-snap-ws")
	createTestWorkspace(t, pool, "Release Co", slug, actor)
	ident := uniqueTestIdentifier()
	proj := createTestProject(t, pool, slug, actor, "Eng", ident)

	// One issue per group: triage, backlog, unstarted, started,
	// completed, cancelled. The triage state is seeded by hand — the
	// project defaults only cover the other five groups.
	var triageStateID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO states (project_id, name, "group", color, sequence)
		 VALUES ($1::uuid, 'Triage', 'triage', '#8b5cf6', 0)
		 RETURNING id::text`, proj.ID).Scan(&triageStateID); err != nil {
		t.Fatalf("create triage state: %v", err)
	}
	groups := []string{"triage", "backlog", "unstarted", "started", "completed", "cancelled"}

	r, err := CreateRelease(ctx, pool, slug, ident, actor, ReleaseInput{Name: "v1.0"})
	if err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	// Create already carries the exact all-zero snapshot: the release
	// has no issues yet.
	for _, g := range groups {
		v, ok := r.ProgressSnapshot[g]
		if !ok || v != 0 {
			t.Fatalf("create snapshot[%s] = %d, present=%v; want 0 present", g, v, ok)
		}
	}

	var ids []string
	for _, g := range groups {
		iss := createTestIssue(t, pool, slug, ident, actor, "issue-"+g)
		sid := triageStateID
		if g != "triage" {
			sid = stateIDForGroup(t, pool, slug, ident, actor, g)
		}
		if _, err := UpdateIssue(ctx, pool, slug, ident, iss.ID, actor,
			IssuePatch{StateID: &sid}); err != nil {
			t.Fatalf("set state %s: %v", g, err)
		}
		ids = append(ids, iss.ID)
	}
	if err := AssignReleaseIssues(ctx, pool, slug, ident, actor, r.ID, ids); err != nil {
		t.Fatalf("AssignReleaseIssues: %v", err)
	}

	// Get carries one per group.
	got, err := GetRelease(ctx, pool, slug, ident, actor, r.ID)
	if err != nil {
		t.Fatalf("GetRelease: %v", err)
	}
	for _, g := range groups {
		if got.ProgressSnapshot[g] != 1 {
			t.Fatalf("get snapshot[%s] = %d, want 1 (full: %+v)", g, got.ProgressSnapshot[g], got.ProgressSnapshot)
		}
	}
	if got.IssueCount != 6 {
		t.Fatalf("get issue_count = %d, want 6", got.IssueCount)
	}

	// List carries the same snapshot — and the fold matches the detail
	// view's client-side progressCounts formula.
	list, err := ListReleases(ctx, pool, slug, ident, actor)
	if err != nil {
		t.Fatalf("ListReleases: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len = %d, want 1", len(list))
	}
	for _, g := range groups {
		if list[0].ProgressSnapshot[g] != 1 {
			t.Fatalf("list snapshot[%s] = %d, want 1 (full: %+v)", g, list[0].ProgressSnapshot[g], list[0].ProgressSnapshot)
		}
	}
	open, inProgress, done := foldProgressCounts(list[0].ProgressSnapshot)
	if open != 3 || inProgress != 1 || done != 2 {
		t.Fatalf("fold = open %d, inProgress %d, done %d; want 3/1/2", open, inProgress, done)
	}

	// Update keeps a live snapshot (it touches no issues, so the
	// counts are unchanged) instead of a stale/missing one.
	newName := "v1.0-renamed"
	upd, err := UpdateRelease(ctx, pool, slug, ident, actor, r.ID, ReleasePatch{Name: &newName})
	if err != nil {
		t.Fatalf("UpdateRelease: %v", err)
	}
	for _, g := range groups {
		if upd.ProgressSnapshot[g] != 1 {
			t.Fatalf("update snapshot[%s] = %d, want 1 (full: %+v)", g, upd.ProgressSnapshot[g], upd.ProgressSnapshot)
		}
	}

	// Soft-deleting an issue drops it from the snapshot: the snapshot
	// counts live issues only.
	if err := DeleteIssue(ctx, pool, slug, ident, ids[0], actor); err != nil {
		t.Fatalf("DeleteIssue: %v", err)
	}
	after, err := GetRelease(ctx, pool, slug, ident, actor, r.ID)
	if err != nil {
		t.Fatalf("GetRelease after delete: %v", err)
	}
	if after.ProgressSnapshot["triage"] != 0 || after.IssueCount != 5 {
		t.Fatalf("after delete: snapshot=%+v issue_count=%d, want triage 0 / count 5",
			after.ProgressSnapshot, after.IssueCount)
	}
}

func TestReleaseProgressSnapshotEmptyAndIsolated(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	actor := createTestUser(t, pool, uniqueTestEmail("release-snap-empty"))
	slug := uniqueTestSlug("release-snap-empty-ws")
	createTestWorkspace(t, pool, "Release Co", slug, actor)
	ident := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Eng", ident)

	// One release with an issue, one empty: the empty release's
	// snapshot must be all zeros, not the sibling's counts.
	full, err := CreateRelease(ctx, pool, slug, ident, actor, ReleaseInput{Name: "full"})
	if err != nil {
		t.Fatalf("CreateRelease full: %v", err)
	}
	empty, err := CreateRelease(ctx, pool, slug, ident, actor, ReleaseInput{Name: "empty"})
	if err != nil {
		t.Fatalf("CreateRelease empty: %v", err)
	}
	iss := createTestIssue(t, pool, slug, ident, actor, "only issue")
	if err := AssignReleaseIssues(ctx, pool, slug, ident, actor, full.ID, []string{iss.ID}); err != nil {
		t.Fatalf("AssignReleaseIssues: %v", err)
	}

	got, err := GetRelease(ctx, pool, slug, ident, actor, empty.ID)
	if err != nil {
		t.Fatalf("GetRelease empty: %v", err)
	}
	if got.IssueCount != 0 {
		t.Fatalf("empty issue_count = %d, want 0", got.IssueCount)
	}
	for _, g := range snapshotGroups {
		v, ok := got.ProgressSnapshot[g]
		if !ok || v != 0 {
			t.Fatalf("empty snapshot[%s] = %d, present=%v; want 0 present", g, v, ok)
		}
	}

	list, err := ListReleases(ctx, pool, slug, ident, actor)
	if err != nil {
		t.Fatalf("ListReleases: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("len = %d, want 2", len(list))
	}
	for _, r := range list {
		if r.ID == empty.ID {
			for _, g := range snapshotGroups {
				if r.ProgressSnapshot[g] != 0 {
					t.Fatalf("list empty snapshot[%s] = %d, want 0", g, r.ProgressSnapshot[g])
				}
			}
		}
		if r.ID == full.ID && r.IssueCount != 1 {
			t.Fatalf("list full issue_count = %d, want 1", r.IssueCount)
		}
	}
}
