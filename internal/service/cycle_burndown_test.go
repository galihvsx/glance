package service

// Burndown (C2T4): GET .../cycles/{id}/burndown reconstructs remaining
// scope per day from the issue_activities audit log — state_id
// transitions bucketed by UTC date, done = completed/cancelled groups.

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCycleBurndown(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	actor := createTestUser(t, pool, uniqueTestEmail("burndown"))
	slug := uniqueTestSlug("burndown-ws")
	createTestWorkspace(t, pool, "Burndown Co", slug, actor)
	ident := uniqueTestIdentifier()
	proj := createTestProject(t, pool, slug, actor, "Eng", ident)

	now := time.Now().UTC()
	// 8-day cycle: T-4 .. T+3, status current.
	c, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "S1", StartDate: now.AddDate(0, 0, -4), EndDate: now.AddDate(0, 0, 3),
	})
	if err != nil {
		t.Fatalf("CreateCycle: %v", err)
	}

	doneState := stateIDByGroup(t, pool, proj.ID, "completed")
	mk := func(name string) string {
		iss := createTestIssue(t, pool, slug, ident, actor, name)
		return iss.ID
	}
	a, b, cIss, d := mk("a"), mk("b"), mk("c"), mk("d")
	for _, id := range []string{a, b, cIss, d} {
		if err := AddCycleIssues(ctx, pool, slug, ident, actor, c.ID, []string{id}); err != nil {
			t.Fatalf("AddCycleIssues: %v", err)
		}
	}
	// A/B/C were in the cycle since it started (backdate membership);
	// D joined yesterday (see below).
	cycleStart := now.AddDate(0, 0, -4)
	for _, id := range []string{a, b, cIss} {
		if _, err := pool.Exec(ctx,
			`UPDATE cycle_issues SET created_at = $1 WHERE cycle_id = $2::uuid AND issue_id = $3::uuid`,
			cycleStart, c.ID, id); err != nil {
			t.Fatalf("backdate membership: %v", err)
		}
	}

	// A completed 2 days ago (backdate the audit row); B completed today.
	if _, err := UpdateIssue(ctx, pool, slug, ident, a, actor, IssuePatch{StateID: &doneState}); err != nil {
		t.Fatalf("complete A: %v", err)
	}
	twoDaysAgo := now.AddDate(0, 0, -2)
	if _, err := pool.Exec(ctx,
		`UPDATE issue_activities SET created_at = $1 WHERE issue_id = $2::uuid AND field = 'state_id'`,
		twoDaysAgo, a); err != nil {
		t.Fatalf("backdate A activity: %v", err)
	}
	if _, err := UpdateIssue(ctx, pool, slug, ident, b, actor, IssuePatch{StateID: &doneState}); err != nil {
		t.Fatalf("complete B: %v", err)
	}
	// D joined the cycle 1 day ago (scope growth mid-cycle).
	yesterday := now.AddDate(0, 0, -1)
	if _, err := pool.Exec(ctx,
		`UPDATE cycle_issues SET created_at = $1 WHERE cycle_id = $2::uuid AND issue_id = $3::uuid`,
		yesterday, c.ID, d); err != nil {
		t.Fatalf("backdate D membership: %v", err)
	}

	bd, err := GetCycleBurndown(ctx, pool, slug, ident, actor, c.ID)
	if err != nil {
		t.Fatalf("GetCycleBurndown: %v", err)
	}
	if bd.TotalScope != 4 {
		t.Fatalf("total_scope = %d, want 4", bd.TotalScope)
	}
	if len(bd.Days) != 8 {
		t.Fatalf("days = %d, want 8", len(bd.Days))
	}
	if bd.StartDate != now.AddDate(0, 0, -4).Format("2006-01-02") {
		t.Fatalf("start_date = %q", bd.StartDate)
	}
	if bd.EndDate != now.AddDate(0, 0, 3).Format("2006-01-02") {
		t.Fatalf("end_date = %q", bd.EndDate)
	}

	rem := func(i int) *int { return bd.Days[i].Remaining }
	deref := func(p *int) int {
		if p == nil {
			return -1
		}
		return *p
	}
	// T-4, T-3: A/B/C open, D not yet in scope → 3.
	if got := deref(rem(0)); got != 3 {
		t.Fatalf("day0 remaining = %d, want 3", got)
	}
	if got := deref(rem(1)); got != 3 {
		t.Fatalf("day1 remaining = %d, want 3", got)
	}
	// T-2: A done → 2.
	if got := deref(rem(2)); got != 2 {
		t.Fatalf("day2 remaining = %d, want 2", got)
	}
	// T-1: D joins, open → 3.
	if got := deref(rem(3)); got != 3 {
		t.Fatalf("day3 remaining = %d, want 3", got)
	}
	// T (today): B done → 2.
	if got := deref(rem(4)); got != 2 {
		t.Fatalf("day4 remaining = %d, want 2", got)
	}
	// Future days: no actuals.
	for _, i := range []int{5, 6, 7} {
		if rem(i) != nil {
			t.Fatalf("day%d remaining = %d, want nil (future)", i, *rem(i))
		}
	}
	// Ideal: straight line 4 → 0.
	if bd.Days[0].Ideal != 4 {
		t.Fatalf("ideal[0] = %v, want 4", bd.Days[0].Ideal)
	}
	if bd.Days[7].Ideal != 0 {
		t.Fatalf("ideal[7] = %v, want 0", bd.Days[7].Ideal)
	}
}

func TestCycleBurndownEmpty(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	actor := createTestUser(t, pool, uniqueTestEmail("burndown-empty"))
	slug := uniqueTestSlug("burndown-empty-ws")
	createTestWorkspace(t, pool, "Burndown Co", slug, actor)
	ident := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Eng", ident)

	now := time.Now().UTC()
	c, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "S1", StartDate: now.AddDate(0, 0, -2), EndDate: now.AddDate(0, 0, 2),
	})
	if err != nil {
		t.Fatalf("CreateCycle: %v", err)
	}
	bd, err := GetCycleBurndown(ctx, pool, slug, ident, actor, c.ID)
	if err != nil {
		t.Fatalf("GetCycleBurndown: %v", err)
	}
	if bd.TotalScope != 0 {
		t.Fatalf("total_scope = %d, want 0", bd.TotalScope)
	}
	if len(bd.Days) != 5 {
		t.Fatalf("days = %d, want 5", len(bd.Days))
	}
	for i, d := range bd.Days {
		if i <= 2 { // past days: zero scope → zero remaining
			if d.Remaining == nil || *d.Remaining != 0 {
				t.Fatalf("day%d remaining = %v, want 0", i, d.Remaining)
			}
		} else { // future days: no actuals
			if d.Remaining != nil {
				t.Fatalf("day%d remaining = %d, want nil (future)", i, *d.Remaining)
			}
		}
		if d.Ideal != 0 {
			t.Fatalf("day%d ideal = %v, want 0", i, d.Ideal)
		}
	}
}

func TestCycleBurndownAccess(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	actor := createTestUser(t, pool, uniqueTestEmail("burndown-acc"))
	slug := uniqueTestSlug("burndown-acc-ws")
	createTestWorkspace(t, pool, "Burndown Co", slug, actor)
	ident := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Eng", ident)

	now := time.Now().UTC()
	c, err := CreateCycle(ctx, pool, slug, ident, actor, CycleInput{
		Name: "S1", StartDate: now.AddDate(0, 0, -1), EndDate: now.AddDate(0, 0, 6),
	})
	if err != nil {
		t.Fatalf("CreateCycle: %v", err)
	}

	// Malformed id → 400-class sentinel.
	if _, err := GetCycleBurndown(ctx, pool, slug, ident, actor, "nope"); !errors.Is(err, ErrInvalidCycleID) {
		t.Fatalf("malformed id: err = %v, want ErrInvalidCycleID", err)
	}
	// Unknown id → not found.
	if _, err := GetCycleBurndown(ctx, pool, slug, ident, actor, "123e4567-e89b-12d3-a456-426614174000"); !errors.Is(err, ErrCycleNotFound) {
		t.Fatalf("unknown id: err = %v, want ErrCycleNotFound", err)
	}
	// Non-member → not found (no tenancy leak).
	outsider := createTestUser(t, pool, uniqueTestEmail("burndown-out"))
	if _, err := GetCycleBurndown(ctx, pool, slug, ident, outsider, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider: err = %v, want ErrNotFound", err)
	}
	_ = c
}
