package ticker

// Recurring-schedule ticker tests (C28T1): RunOnce executes due
// schedules against a fake clock, leaves future ones alone, respects
// the batch limit, and never double-runs an occurrence — even across
// concurrent passes.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"glance/internal/service"
)

// mustRecurringSchedule creates a daily schedule and forces next_run_at
// to runAt (CreateSchedule always computes a future next_run_at).
func mustRecurringSchedule(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, projectID, actor, name string, runAt time.Time) *service.RecurringSchedule {
	t.Helper()
	in := service.CreateScheduleInput{
		Name:          name,
		Frequency:     "daily",
		Hour:          9,
		StartsOn:      time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		TitleTemplate: name + " {{date}}",
		Priority:      2,
	}
	s, err := service.CreateSchedule(ctx, pool, slug, projectID, in, actor)
	if err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE issue_schedules SET next_run_at = $1 WHERE id = $2::uuid`, runAt, s.ID); err != nil {
		t.Fatalf("force next_run_at: %v", err)
	}
	return s
}

func countProjectIssues(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM issues WHERE project_id = $1::uuid`, projectID).Scan(&n); err != nil {
		t.Fatalf("count issues: %v", err)
	}
	return n
}

func TestRecurringTickerRunsDueOnly(t *testing.T) {
	ctx, pool, slug, _, actor, projectID := tickerTestSetup(t, "recur-tick")

	dueAt := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	futureAt := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	mustRecurringSchedule(t, ctx, pool, slug, projectID, actor, "due-job", dueAt)
	mustRecurringSchedule(t, ctx, pool, slug, projectID, actor, "future-job", futureAt)

	fakeNow := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	rt := &RecurringTicker{Pool: pool}
	if err := rt.RunOnce(ctx, fakeNow); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if n := countProjectIssues(t, ctx, pool, projectID); n != 1 {
		t.Fatalf("issues = %d, want 1 (only the due schedule runs)", n)
	}
	// The created issue carries the rendered title of the due run.
	var name string
	if err := pool.QueryRow(ctx,
		`SELECT name FROM issues WHERE project_id = $1::uuid`, projectID).Scan(&name); err != nil {
		t.Fatalf("issue name: %v", err)
	}
	if name != "due-job 2026-10-12" {
		t.Fatalf("issue name = %q", name)
	}
	// The future schedule is untouched.
	var next time.Time
	if err := pool.QueryRow(ctx,
		`SELECT next_run_at FROM issue_schedules WHERE name = 'future-job'`).Scan(&next); err != nil {
		t.Fatalf("future next_run_at: %v", err)
	}
	if !next.Equal(futureAt) {
		t.Fatalf("future next_run_at = %s, want %s", next, futureAt)
	}
}

func TestRecurringTickerClaimLimit(t *testing.T) {
	ctx, pool, slug, _, actor, projectID := tickerTestSetup(t, "recur-limit")

	dueAt := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		mustRecurringSchedule(t, ctx, pool, slug, projectID, actor, "x", dueAt)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	due, err := service.ClaimDueSchedules(ctx, tx, dueAt.Add(time.Hour), 2)
	if err != nil {
		t.Fatalf("ClaimDueSchedules: %v", err)
	}
	if len(due) != 2 {
		t.Fatalf("claimed = %d, want 2 (LIMIT respected)", len(due))
	}
}

func TestRecurringTickerConcurrentNoDoubleRun(t *testing.T) {
	ctx, pool, slug, _, actor, projectID := tickerTestSetup(t, "recur-conc")

	dueAt := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	mustRecurringSchedule(t, ctx, pool, slug, projectID, actor, "conc-job", dueAt)

	fakeNow := dueAt.Add(time.Hour)
	rt := &RecurringTicker{Pool: pool}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = rt.RunOnce(ctx, fakeNow)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("RunOnce %d: %v", i, err)
		}
	}
	if n := countProjectIssues(t, ctx, pool, projectID); n != 1 {
		t.Fatalf("issues = %d, want exactly 1 across concurrent passes", n)
	}
	var runCount int
	if err := pool.QueryRow(ctx,
		`SELECT run_count FROM issue_schedules WHERE name = 'conc-job'`).Scan(&runCount); err != nil {
		t.Fatalf("run_count: %v", err)
	}
	if runCount != 1 {
		t.Fatalf("run_count = %d, want 1", runCount)
	}
}

func TestRecurringTickerDefaultInterval(t *testing.T) {
	ctx, pool, _, _, _, _ := tickerTestSetup(t, "recur-def")
	_ = ctx
	rt := &RecurringTicker{Pool: pool}
	done := make(chan struct{})
	go func() {
		defer close(done)
		// Start with Interval 0 must not panic and must stop on cancel.
		c, cancel := context.WithCancel(context.Background())
		rt.Start(c)
		cancel()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("Start did not return after cancel")
	}
}
