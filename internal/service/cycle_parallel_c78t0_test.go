package service

// Parallel cycles (C78T0): the per-project parallel_cycles toggle
// (migration 000071) and the overlap validation on CreateCycle /
// UpdateCycle, plus the rollover rulings with still-running parallel
// cycles. Real test database, no skips.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func parallelTestSetup(t *testing.T, prefix string) (*pgxpool.Pool, string, string, string, *Project) {
	t.Helper()
	pool := newTestPool(t)
	actor := createTestUser(t, pool, uniqueTestEmail(prefix))
	slug := uniqueTestSlug(prefix + "-ws")
	createTestWorkspace(t, pool, "Cycle Co", slug, actor)
	ident := uniqueTestIdentifier()
	proj := createTestProject(t, pool, slug, actor, "Eng", ident)
	return pool, actor, slug, ident, proj
}

// memberInCycle reports whether issueID is a member of cycleID.
func memberInCycle(t *testing.T, pool *pgxpool.Pool, cycleID, issueID string) bool {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM cycle_issues WHERE cycle_id = $1::uuid AND issue_id = $2::uuid`,
		cycleID, issueID).Scan(&n); err != nil {
		t.Fatalf("membership check: %v", err)
	}
	return n > 0
}

// TestParallelCyclesToggleDefaultOff pins the default (false) and that
// the flag is exposed on create/detail/list payloads.
func TestParallelCyclesToggleDefaultOff(t *testing.T) {
	ctx := context.Background()
	pool, actor, slug, ident, proj := parallelTestSetup(t, "pc-default")
	if proj.ParallelCycles {
		t.Fatalf("new project ParallelCycles = true, want false")
	}
	got, err := GetProject(ctx, pool, slug, ident, actor)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got.ParallelCycles {
		t.Fatalf("GetProject ParallelCycles = true, want false")
	}
	list, err := ListProjects(ctx, pool, slug, actor, false, nil)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(list) != 1 || list[0].ParallelCycles {
		t.Fatalf("ListProjects ParallelCycles = %+v, want one project with false", list)
	}
}

// TestParallelCyclesToggleUpdate pins the toggle flip through the
// existing UpdateProject path and the member (15)+ gate.
func TestParallelCyclesToggleUpdate(t *testing.T) {
	ctx := context.Background()
	pool, actor, slug, ident, _ := parallelTestSetup(t, "pc-toggle")

	upd, err := UpdateProject(ctx, pool, slug, ident, actor, ProjectPatch{ParallelCycles: boolPtr(true)})
	if err != nil {
		t.Fatalf("UpdateProject parallel_cycles=true: %v", err)
	}
	if !upd.ParallelCycles {
		t.Fatalf("toggled ParallelCycles = false, want true")
	}
	// Round-trip back off.
	upd, err = UpdateProject(ctx, pool, slug, ident, actor, ProjectPatch{ParallelCycles: boolPtr(false)})
	if err != nil {
		t.Fatalf("UpdateProject parallel_cycles=false: %v", err)
	}
	if upd.ParallelCycles {
		t.Fatalf("toggled-back ParallelCycles = true, want false")
	}

	// Guest (5) is read-only: the toggle gate mirrors the existing
	// project-update gate.
	guest := createTestUser(t, pool, uniqueTestEmail("pc-guest"))
	addTestMember(t, pool, slug, actor, guest, 5)
	if _, err := UpdateProject(ctx, pool, slug, ident, guest, ProjectPatch{ParallelCycles: boolPtr(true)}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest toggle: err = %v, want ErrForbidden", err)
	}
}

// TestCycleOverlapRejectedWhenToggleOff is the core OFF-semantics check:
// a create whose [start,end] overlaps another live cycle of the project
// fails with ErrCycleOverlap, and the error names the conflicting cycle.
func TestCycleOverlapRejectedWhenToggleOff(t *testing.T) {
	ctx := context.Background()
	pool, actor, slug, ident, _ := parallelTestSetup(t, "pc-overlap")
	now := time.Now()

	a, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "Sprint 12", StartDate: now.AddDate(0, 0, -7), EndDate: now.AddDate(0, 0, 7),
	})
	if err != nil {
		t.Fatalf("CreateCycle A: %v", err)
	}

	// Strict overlap.
	if _, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "Overlap", StartDate: now.AddDate(0, 0, -3), EndDate: now.AddDate(0, 0, 10),
	}); !errors.Is(err, ErrCycleOverlap) {
		t.Fatalf("overlapping create: err = %v, want ErrCycleOverlap", err)
	} else {
		var oe *CycleOverlapError
		if !errors.As(err, &oe) {
			t.Fatalf("overlap err type = %T, want *CycleOverlapError", err)
		}
		if oe.Name != "Sprint 12" {
			t.Fatalf("conflicting cycle = %q, want %q", oe.Name, a.Name)
		}
	}

	// Boundary overlap is still overlap (inclusive predicate):
	// new start == existing end.
	if _, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "Boundary", StartDate: now.AddDate(0, 0, 7), EndDate: now.AddDate(0, 0, 14),
	}); !errors.Is(err, ErrCycleOverlap) {
		t.Fatalf("boundary create: err = %v, want ErrCycleOverlap", err)
	}

	// Adjacent (no shared day) is fine.
	if _, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "Next", StartDate: now.AddDate(0, 0, 8), EndDate: now.AddDate(0, 0, 21),
	}); err != nil {
		t.Fatalf("adjacent create: %v", err)
	}
}

// TestCycleOverlapAllowedWhenToggleOn pins that the toggle keeps today's
// behavior: no overlap restriction.
func TestCycleOverlapAllowedWhenToggleOn(t *testing.T) {
	ctx := context.Background()
	pool, actor, slug, ident, _ := parallelTestSetup(t, "pc-on")
	now := time.Now()

	if _, err := UpdateProject(ctx, pool, slug, ident, actor, ProjectPatch{ParallelCycles: boolPtr(true)}); err != nil {
		t.Fatalf("toggle on: %v", err)
	}
	if _, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "Sprint A", StartDate: now.AddDate(0, 0, -7), EndDate: now.AddDate(0, 0, 7),
	}); err != nil {
		t.Fatalf("CreateCycle A: %v", err)
	}
	if _, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "Sprint B", StartDate: now.AddDate(0, 0, -3), EndDate: now.AddDate(0, 0, 10),
	}); err != nil {
		t.Fatalf("overlapping create with toggle on: %v", err)
	}
}

// TestCycleUpdateIntoOverlap pins that moving a cycle's dates into an
// overlap is rejected, while a name-only update of a grandfathered
// overlapping cycle is not (no retroactive alteration).
func TestCycleUpdateIntoOverlap(t *testing.T) {
	ctx := context.Background()
	pool, actor, slug, ident, _ := parallelTestSetup(t, "pc-upd")
	now := time.Now()

	if _, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "S1", StartDate: now.AddDate(0, 0, -14), EndDate: now.AddDate(0, 0, -8),
	}); err != nil {
		t.Fatalf("CreateCycle S1: %v", err)
	}
	b, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "S2", StartDate: now.AddDate(0, 0, -7), EndDate: now.AddDate(0, 0, 7),
	})
	if err != nil {
		t.Fatalf("CreateCycle S2: %v", err)
	}

	// Move S2's start back into S1's range → blocked.
	start := now.AddDate(0, 0, -10)
	if _, err := UpdateCycle(ctx, pool, slug, ident, actor, b.ID, CyclePatch{StartDate: &start}); !errors.Is(err, ErrCycleOverlap) {
		t.Fatalf("update into overlap: err = %v, want ErrCycleOverlap", err)
	}
	// Self-exclusion: moving S2's end later (no other live overlap) is fine.
	end := now.AddDate(0, 0, 21)
	if _, err := UpdateCycle(ctx, pool, slug, ident, actor, b.ID, CyclePatch{EndDate: &end}); err != nil {
		t.Fatalf("update out of overlap: %v", err)
	}
}

// TestCycleNameConflictPrecedence keeps the existing contract: a
// duplicate name still reports ErrCycleConflict even when the dates
// would also overlap (name check runs first).
func TestCycleNameConflictPrecedence(t *testing.T) {
	ctx := context.Background()
	pool, actor, slug, ident, _ := parallelTestSetup(t, "pc-nameprec")
	now := time.Now()

	if _, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "S1", StartDate: now.AddDate(0, 0, -7), EndDate: now.AddDate(0, 0, 7),
	}); err != nil {
		t.Fatalf("CreateCycle S1: %v", err)
	}
	if _, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "S1", StartDate: now.AddDate(0, 0, -3), EndDate: now.AddDate(0, 0, 10),
	}); !errors.Is(err, ErrCycleConflict) {
		t.Fatalf("dup name + overlap: err = %v, want ErrCycleConflict", err)
	}
}

// TestCycleOverlapExcludesCompleted pins that completed cycles are not
// overlap blockers: a new cycle may reuse a finished cycle's range.
func TestCycleOverlapExcludesCompleted(t *testing.T) {
	ctx := context.Background()
	pool, actor, slug, ident, proj := parallelTestSetup(t, "pc-compl")
	now := time.Now()

	a, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "Old", StartDate: now.AddDate(0, 0, -14), EndDate: now.AddDate(0, 0, -1),
	})
	if err != nil {
		t.Fatalf("CreateCycle A: %v", err)
	}
	if err := CompleteCycle(ctx, pool, a.ID, proj.ID, now); err != nil {
		t.Fatalf("CompleteCycle: %v", err)
	}
	if _, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "New", StartDate: now.AddDate(0, 0, -10), EndDate: now.AddDate(0, 0, 5),
	}); err != nil {
		t.Fatalf("create overlapping a completed cycle: %v", err)
	}
}

// TestParallelCyclesGrandfathering pins the deliberate ruling: flipping
// the toggle OFF with pre-existing overlaps does not retroactively alter
// them; only new creates/updates into an overlap are blocked, and a
// name-only edit of a grandfathered cycle still works.
func TestParallelCyclesGrandfathering(t *testing.T) {
	ctx := context.Background()
	pool, actor, slug, ident, _ := parallelTestSetup(t, "pc-grand")
	now := time.Now()

	if _, err := UpdateProject(ctx, pool, slug, ident, actor, ProjectPatch{ParallelCycles: boolPtr(true)}); err != nil {
		t.Fatalf("toggle on: %v", err)
	}
	if _, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "S1", StartDate: now.AddDate(0, 0, -7), EndDate: now.AddDate(0, 0, 7),
	}); err != nil {
		t.Fatalf("CreateCycle S1: %v", err)
	}
	b, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "S2", StartDate: now.AddDate(0, 0, -3), EndDate: now.AddDate(0, 0, 10),
	})
	if err != nil {
		t.Fatalf("CreateCycle S2 (overlap, toggle on): %v", err)
	}

	// Flip OFF: the pre-existing overlap survives.
	if _, err := UpdateProject(ctx, pool, slug, ident, actor, ProjectPatch{ParallelCycles: boolPtr(false)}); err != nil {
		t.Fatalf("toggle off: %v", err)
	}
	cycles, err := ListCycles(ctx, pool, slug, ident, actor)
	if err != nil {
		t.Fatalf("ListCycles: %v", err)
	}
	if len(cycles) != 2 {
		t.Fatalf("grandfathered cycles = %d, want 2", len(cycles))
	}

	// Name-only update of a grandfathered overlapping cycle: allowed.
	renamed := "S2-renamed"
	if _, err := UpdateCycle(ctx, pool, slug, ident, actor, b.ID, CyclePatch{Name: &renamed}); err != nil {
		t.Fatalf("name-only update of grandfathered cycle: %v", err)
	}

	// New overlapping create: blocked.
	if _, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "S3", StartDate: now.AddDate(0, 0, -1), EndDate: now.AddDate(0, 0, 12),
	}); !errors.Is(err, ErrCycleOverlap) {
		t.Fatalf("post-toggle overlapping create: err = %v, want ErrCycleOverlap", err)
	}

	// Date change keeping the grandfathered overlap: blocked.
	end := now.AddDate(0, 0, 11)
	if _, err := UpdateCycle(ctx, pool, slug, ident, actor, b.ID, CyclePatch{EndDate: &end}); !errors.Is(err, ErrCycleOverlap) {
		t.Fatalf("date change into overlap: err = %v, want ErrCycleOverlap", err)
	}
}

// TestRolloverKeepsParallelCycleMembership pins the rollover ruling: an
// issue attached to a still-running parallel cycle keeps that membership
// when the other cycle completes (completion deletes only the completed
// cycle's rows), and detaches from the completed cycle per close_in_days.
func TestRolloverKeepsParallelCycleMembership(t *testing.T) {
	ctx := context.Background()
	pool, actor, slug, ident, proj := parallelTestSetup(t, "pc-roll")
	now := time.Now()

	if _, err := UpdateProject(ctx, pool, slug, ident, actor, ProjectPatch{ParallelCycles: boolPtr(true)}); err != nil {
		t.Fatalf("toggle on: %v", err)
	}
	c1, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "Old", StartDate: now.AddDate(0, 0, -14), EndDate: now.AddDate(0, 0, -1),
	})
	if err != nil {
		t.Fatalf("CreateCycle C1: %v", err)
	}
	// C2 overlaps C1 and is still running: never a transfer target
	// (transfer needs start_date >= completed end_date).
	c2, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "Parallel", StartDate: now.AddDate(0, 0, -10), EndDate: now.AddDate(0, 0, 10),
	})
	if err != nil {
		t.Fatalf("CreateCycle C2: %v", err)
	}

	iss := createTestIssue(t, pool, slug, ident, actor, "shared")
	if err := AddCycleIssues(ctx, pool, slug, ident, actor, c1.ID, []string{iss.ID}); err != nil {
		t.Fatalf("add to C1: %v", err)
	}
	if err := AddCycleIssues(ctx, pool, slug, ident, actor, c2.ID, []string{iss.ID}); err != nil {
		t.Fatalf("add to C2: %v", err)
	}

	if err := CompleteCycle(ctx, pool, c1.ID, proj.ID, now); err != nil {
		t.Fatalf("CompleteCycle C1: %v", err)
	}
	if memberInCycle(t, pool, c1.ID, iss.ID) {
		t.Fatalf("issue still in completed C1")
	}
	if !memberInCycle(t, pool, c2.ID, iss.ID) {
		t.Fatalf("issue lost membership in still-running parallel C2")
	}
}

// TestRolloverTransfersToNonOverlappingNext pins that with parallel
// cycles, incomplete issues of the completed cycle transfer only to the
// non-overlapping next cycle — never to a still-current overlapping one.
func TestRolloverTransfersToNonOverlappingNext(t *testing.T) {
	ctx := context.Background()
	pool, actor, slug, ident, proj := parallelTestSetup(t, "pc-rollnext")
	now := time.Now()

	if _, err := UpdateProject(ctx, pool, slug, ident, actor, ProjectPatch{ParallelCycles: boolPtr(true)}); err != nil {
		t.Fatalf("toggle on: %v", err)
	}
	c1, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "Old", StartDate: now.AddDate(0, 0, -14), EndDate: now.AddDate(0, 0, -1),
	})
	if err != nil {
		t.Fatalf("CreateCycle C1: %v", err)
	}
	c2, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "Parallel", StartDate: now.AddDate(0, 0, -10), EndDate: now.AddDate(0, 0, 10),
	})
	if err != nil {
		t.Fatalf("CreateCycle C2: %v", err)
	}
	c3, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "Next", StartDate: now.AddDate(0, 0, 2), EndDate: now.AddDate(0, 0, 16),
	})
	if err != nil {
		t.Fatalf("CreateCycle C3: %v", err)
	}

	iss := createTestIssue(t, pool, slug, ident, actor, "mover")
	if err := AddCycleIssues(ctx, pool, slug, ident, actor, c1.ID, []string{iss.ID}); err != nil {
		t.Fatalf("add to C1: %v", err)
	}
	if err := AddCycleIssues(ctx, pool, slug, ident, actor, c2.ID, []string{iss.ID}); err != nil {
		t.Fatalf("add to C2: %v", err)
	}

	if err := CompleteCycle(ctx, pool, c1.ID, proj.ID, now); err != nil {
		t.Fatalf("CompleteCycle C1: %v", err)
	}
	if !memberInCycle(t, pool, c3.ID, iss.ID) {
		t.Fatalf("issue not transferred to non-overlapping next cycle C3")
	}
	if !memberInCycle(t, pool, c2.ID, iss.ID) {
		t.Fatalf("issue lost membership in still-running parallel C2")
	}
	if memberInCycle(t, pool, c1.ID, iss.ID) {
		t.Fatalf("issue still in completed C1")
	}
}
