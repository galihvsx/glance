package service

// Cycles + rollover (Task 22, spec §4): project-scoped CRUD, bulk issue
// membership, live progress snapshots, and the Task 15 carry-over — the
// list's cycle= filter is now a real EXISTS filter on cycle_issues.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// stateIDByGroup returns the project's default state id for a group.
func stateIDByGroup(t *testing.T, pool *pgxpool.Pool, projectID, group string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM states WHERE project_id = $1::uuid AND "group" = $2 ORDER BY sequence LIMIT 1`,
		projectID, group).Scan(&id); err != nil {
		t.Fatalf("state for group %s: %v", group, err)
	}
	return id
}

func TestCycleCRUD(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	actor := createTestUser(t, pool, uniqueTestEmail("cycle"))
	slug := uniqueTestSlug("cycle-ws")
	createTestWorkspace(t, pool, "Cycle Co", slug, actor)
	ident := uniqueTestIdentifier()
	proj := createTestProject(t, pool, slug, actor, "Eng", ident)

	start := time.Now().AddDate(0, 0, -1)
	end := time.Now().AddDate(0, 0, 13)
	c, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "Sprint 1", StartDate: start, EndDate: end,
	})
	if err != nil {
		t.Fatalf("CreateCycle: %v", err)
	}
	if c.Status != "current" {
		t.Fatalf("status = %q, want current", c.Status)
	}

	got, err := GetCycle(ctx, pool, slug, ident, actor, c.ID)
	if err != nil {
		t.Fatalf("GetCycle: %v", err)
	}
	if got.Name != "Sprint 1" {
		t.Fatalf("name = %q", got.Name)
	}

	// Date validation: start after end is rejected.
	if _, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "Bad", StartDate: end, EndDate: start,
	}); !errors.Is(err, ErrInvalidCycle) {
		t.Fatalf("start>end: err = %v, want ErrInvalidCycle", err)
	}

	// Duplicate name in the project conflicts.
	if _, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "Sprint 1", StartDate: start, EndDate: end,
	}); !errors.Is(err, ErrCycleConflict) {
		t.Fatalf("dup name: err = %v, want ErrCycleConflict", err)
	}

	// Partial update: rename only.
	newName := "Sprint Uno"
	upd, err := UpdateCycle(ctx, pool, slug, ident, actor, c.ID, CyclePatch{Name: &newName})
	if err != nil {
		t.Fatalf("UpdateCycle: %v", err)
	}
	if upd.Name != "Sprint Uno" {
		t.Fatalf("renamed = %q", upd.Name)
	}

	if err := DeleteCycle(ctx, pool, slug, ident, actor, c.ID); err != nil {
		t.Fatalf("DeleteCycle: %v", err)
	}
	if _, err := GetCycle(ctx, pool, slug, ident, actor, c.ID); !errors.Is(err, ErrCycleNotFound) {
		t.Fatalf("get deleted: err = %v, want ErrCycleNotFound", err)
	}

	_ = proj
}

func TestCycleAddIssues(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	actor := createTestUser(t, pool, uniqueTestEmail("cycle-issues"))
	slug := uniqueTestSlug("cycle-iss-ws")
	createTestWorkspace(t, pool, "Cycle Co", slug, actor)
	ident := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Eng", ident)

	now := time.Now()
	c, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "S1", StartDate: now.AddDate(0, 0, -1), EndDate: now.AddDate(0, 0, 13),
	})
	if err != nil {
		t.Fatalf("CreateCycle: %v", err)
	}

	i1 := createTestIssue(t, pool, slug, ident, actor, "one")
	i2 := createTestIssue(t, pool, slug, ident, actor, "two")

	// Bulk add is idempotent: repeat adds are no-ops, never a 409.
	for range 2 {
		if err := AddCycleIssues(ctx, pool, slug, ident, actor, c.ID, []string{i1.ID, i2.ID}); err != nil {
			t.Fatalf("AddCycleIssues: %v", err)
		}
	}
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM cycle_issues WHERE cycle_id = $1::uuid`, c.ID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Fatalf("cycle_issues = %d, want 2 (idempotent)", n)
	}

	// An issue from another project is rejected.
	ident2 := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Eng2", ident2)
	other := createTestIssue(t, pool, slug, ident2, actor, "foreign")
	if err := AddCycleIssues(ctx, pool, slug, ident, actor, c.ID, []string{other.ID}); !errors.Is(err, ErrIssueNotFound) {
		t.Fatalf("foreign issue: err = %v, want ErrIssueNotFound", err)
	}

	// Guests cannot mutate cycles.
	guest := createTestUser(t, pool, uniqueTestEmail("cycle-guest"))
	if _, err := pool.Exec(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role)
		 SELECT w.id, $2::uuid, 5 FROM workspaces w WHERE w.slug = $1`, slug, guest); err != nil {
		t.Fatalf("add guest: %v", err)
	}
	if err := AddCycleIssues(ctx, pool, slug, ident, guest, c.ID, []string{i1.ID}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest add: err = %v, want ErrForbidden", err)
	}
}

func TestCycleProgressSnapshot(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	actor := createTestUser(t, pool, uniqueTestEmail("cycle-snap"))
	slug := uniqueTestSlug("cycle-snap-ws")
	createTestWorkspace(t, pool, "Cycle Co", slug, actor)
	ident := uniqueTestIdentifier()
	proj := createTestProject(t, pool, slug, actor, "Eng", ident)

	now := time.Now()
	c, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "S1", StartDate: now.AddDate(0, 0, -1), EndDate: now.AddDate(0, 0, 13),
	})
	if err != nil {
		t.Fatalf("CreateCycle: %v", err)
	}

	// One issue per group: triage, backlog, unstarted, started, completed,
	// cancelled. The triage state is seeded by hand — the project defaults
	// only cover the other five groups.
	var triageStateID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO states (project_id, name, "group", color, sequence)
		 VALUES ($1::uuid, 'Triage', 'triage', '#8b5cf6', 0)
		 RETURNING id::text`, proj.ID).Scan(&triageStateID); err != nil {
		t.Fatalf("create triage state: %v", err)
	}
	groups := []string{"triage", "backlog", "unstarted", "started", "completed", "cancelled"}
	var ids []string
	for _, g := range groups {
		iss := createTestIssue(t, pool, slug, ident, actor, "issue-"+g)
		sid := triageStateID
		if g != "triage" {
			sid = stateIDByGroup(t, pool, proj.ID, g)
		}
		if _, err := UpdateIssue(ctx, pool, slug, ident, iss.ID, actor,
			IssuePatch{StateID: &sid}); err != nil {
			t.Fatalf("set state %s: %v", g, err)
		}
		ids = append(ids, iss.ID)
	}
	if err := AddCycleIssues(ctx, pool, slug, ident, actor, c.ID, ids); err != nil {
		t.Fatalf("AddCycleIssues: %v", err)
	}

	got, err := GetCycle(ctx, pool, slug, ident, actor, c.ID)
	if err != nil {
		t.Fatalf("GetCycle: %v", err)
	}
	want := map[string]int64{"triage": 1, "backlog": 1, "unstarted": 1, "started": 1, "completed": 1, "cancelled": 1}
	for g, w := range want {
		if got.ProgressSnapshot[g] != w {
			t.Fatalf("snapshot[%s] = %d, want %d (full: %+v)", g, got.ProgressSnapshot[g], w, got.ProgressSnapshot)
		}
	}

	// Empty cycle: every group present as 0, never nil/missing.
	c2, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "S2", StartDate: now.AddDate(0, 0, 14), EndDate: now.AddDate(0, 0, 27),
	})
	if err != nil {
		t.Fatalf("CreateCycle S2: %v", err)
	}
	empty, err := GetCycle(ctx, pool, slug, ident, actor, c2.ID)
	if err != nil {
		t.Fatalf("GetCycle S2: %v", err)
	}
	for _, g := range groups {
		v, ok := empty.ProgressSnapshot[g]
		if !ok || v != 0 {
			t.Fatalf("empty snapshot[%s] = %d, present=%v; want 0 present", g, v, ok)
		}
	}
}

// TestCycleFrozenSnapshotKeepsTriage: completing a cycle with a
// triage-grouped issue must freeze triage into progress_snapshot — the
// completed cycle's final report card must not silently drop it.
func TestCycleFrozenSnapshotKeepsTriage(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	actor := createTestUser(t, pool, uniqueTestEmail("cycle-frozen"))
	slug := uniqueTestSlug("cycle-frozen-ws")
	createTestWorkspace(t, pool, "Cycle Co", slug, actor)
	ident := uniqueTestIdentifier()
	proj := createTestProject(t, pool, slug, actor, "Eng", ident)

	var triageStateID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO states (project_id, name, "group", color, sequence)
		 VALUES ($1::uuid, 'Triage', 'triage', '#8b5cf6', 0)
		 RETURNING id::text`, proj.ID).Scan(&triageStateID); err != nil {
		t.Fatalf("create triage state: %v", err)
	}

	now := time.Now()
	c, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "S1", StartDate: now.AddDate(0, 0, -14), EndDate: now.AddDate(0, 0, -1),
	})
	if err != nil {
		t.Fatalf("CreateCycle: %v", err)
	}

	iss := createTestIssue(t, pool, slug, ident, actor, "triage-issue")
	if _, err := UpdateIssue(ctx, pool, slug, ident, iss.ID, actor,
		IssuePatch{StateID: &triageStateID}); err != nil {
		t.Fatalf("set triage state: %v", err)
	}
	if err := AddCycleIssues(ctx, pool, slug, ident, actor, c.ID, []string{iss.ID}); err != nil {
		t.Fatalf("AddCycleIssues: %v", err)
	}

	// Live snapshot already counts triage.
	live, err := GetCycle(ctx, pool, slug, ident, actor, c.ID)
	if err != nil {
		t.Fatalf("GetCycle: %v", err)
	}
	if live.ProgressSnapshot["triage"] != 1 {
		t.Fatalf("live snapshot[triage] = %d, want 1 (full: %+v)",
			live.ProgressSnapshot["triage"], live.ProgressSnapshot)
	}

	// Complete the cycle; the freeze must keep triage.
	if err := CompleteCycle(ctx, pool, c.ID, proj.ID, now); err != nil {
		t.Fatalf("CompleteCycle: %v", err)
	}
	got, err := GetCycle(ctx, pool, slug, ident, actor, c.ID)
	if err != nil {
		t.Fatalf("GetCycle after complete: %v", err)
	}
	if got.Status != "completed" {
		t.Fatalf("status = %q, want completed", got.Status)
	}
	if got.ProgressSnapshot["triage"] != 1 {
		t.Fatalf("frozen snapshot[triage] = %d, want 1 (full: %+v)",
			got.ProgressSnapshot["triage"], got.ProgressSnapshot)
	}
	// All six groups present in the frozen report card.
	for _, g := range []string{"triage", "backlog", "unstarted", "started", "completed", "cancelled"} {
		if _, ok := got.ProgressSnapshot[g]; !ok {
			t.Fatalf("frozen snapshot missing group %q (full: %+v)", g, got.ProgressSnapshot)
		}
	}

	// C6T5: completing an already-completed cycle is a no-op, not a
	// second snapshot + second issue transfer (ticker race guard).
	if err := CompleteCycle(ctx, pool, c.ID, proj.ID, now); err != nil {
		t.Fatalf("second CompleteCycle: %v (want nil no-op)", err)
	}
	again, err := GetCycle(ctx, pool, slug, ident, actor, c.ID)
	if err != nil {
		t.Fatalf("GetCycle after second complete: %v", err)
	}
	if again.Status != "completed" || again.ProgressSnapshot["triage"] != 1 {
		t.Fatalf("second complete changed the cycle: status=%q snapshot=%+v",
			again.Status, again.ProgressSnapshot)
	}
}

// TestCycleMalformedUUID: malformed cycle/issue ids are 400-grade client
// errors (ErrInvalidCycleID), never a 500 from the ::uuid cast.
func TestCycleMalformedUUID(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	actor := createTestUser(t, pool, uniqueTestEmail("cycle-badid"))
	slug := uniqueTestSlug("cycle-badid-ws")
	createTestWorkspace(t, pool, "Cycle Co", slug, actor)
	ident := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Eng", ident)

	now := time.Now()
	c, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "S1", StartDate: now.AddDate(0, 0, -1), EndDate: now.AddDate(0, 0, 13),
	})
	if err != nil {
		t.Fatalf("CreateCycle: %v", err)
	}
	iss := createTestIssue(t, pool, slug, ident, actor, "one")
	name := "renamed"

	cases := []struct {
		name string
		call func() error
	}{
		{"GetCycle", func() error { _, err := GetCycle(ctx, pool, slug, ident, actor, "not-a-uuid"); return err }},
		{"UpdateCycle", func() error {
			_, err := UpdateCycle(ctx, pool, slug, ident, actor, "not-a-uuid", CyclePatch{Name: &name})
			return err
		}},
		{"DeleteCycle", func() error { return DeleteCycle(ctx, pool, slug, ident, actor, "not-a-uuid") }},
		{"AddCycleIssues bad cycle", func() error { return AddCycleIssues(ctx, pool, slug, ident, actor, "not-a-uuid", []string{iss.ID}) }},
		{"AddCycleIssues bad issue", func() error { return AddCycleIssues(ctx, pool, slug, ident, actor, c.ID, []string{"not-a-uuid"}) }},
	}
	for _, tc := range cases {
		if err := tc.call(); !errors.Is(err, ErrInvalidCycleID) {
			t.Fatalf("%s: err = %v, want ErrInvalidCycleID", tc.name, err)
		}
	}
}

func TestListCycleFilter(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	actor := createTestUser(t, pool, uniqueTestEmail("cycle-filter"))
	slug := uniqueTestSlug("cycle-filter-ws")
	createTestWorkspace(t, pool, "Cycle Co", slug, actor)
	ident := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Eng", ident)

	now := time.Now()
	c, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "S1", StartDate: now.AddDate(0, 0, -1), EndDate: now.AddDate(0, 0, 13),
	})
	if err != nil {
		t.Fatalf("CreateCycle: %v", err)
	}
	inCycle := createTestIssue(t, pool, slug, ident, actor, "in-cycle")
	outCycle := createTestIssue(t, pool, slug, ident, actor, "out-cycle")
	if err := AddCycleIssues(ctx, pool, slug, ident, actor, c.ID, []string{inCycle.ID}); err != nil {
		t.Fatalf("AddCycleIssues: %v", err)
	}

	// Task 15 carry-over: cycle= is now a real EXISTS filter on
	// cycle_issues — only the member issue lists.
	r, err := ListIssues(ctx, pool, slug, ident, actor, ListIssuesInput{Cycle: c.ID})
	if err != nil {
		t.Fatalf("ListIssues cycle=: %v", err)
	}
	if len(r.Issues) != 1 || r.Issues[0].ID != inCycle.ID {
		t.Fatalf("cycle filter: got %d issues, want exactly the in-cycle one", len(r.Issues))
	}
	_ = outCycle
}
