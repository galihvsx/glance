package service

// Time tracking (C3T7): start/stop/log/list, the double-start 409, and
// the stop-without-start 404.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func setupTimeTest(t *testing.T) (context.Context, *pgxpool.Pool, string, string, string, string) {
	t.Helper()
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	actor := createTestUser(t, pool, uniqueTestEmail("time"))
	slug := uniqueTestSlug("time-ws")
	createTestWorkspace(t, pool, "Time Co", slug, actor)
	ident := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Eng", ident)
	iss := createTestIssue(t, pool, slug, ident, actor, "Timed work")
	return ctx, pool, slug, ident, actor, iss.ID
}

func TestTimeStartStop(t *testing.T) {
	ctx, pool, slug, ident, actor, issueID := setupTimeTest(t)

	e, err := StartTimer(ctx, pool, slug, ident, issueID, actor)
	if err != nil {
		t.Fatalf("StartTimer: %v", err)
	}
	if e.EndedAt != nil {
		t.Fatalf("new timer should be running, got ended_at %v", e.EndedAt)
	}
	if e.UserID != actor {
		t.Fatalf("user_id = %s, want %s", e.UserID, actor)
	}

	// Double start is a 409, and it must not create a second row.
	if _, err := StartTimer(ctx, pool, slug, ident, issueID, actor); !errors.Is(err, ErrTimerAlreadyRunning) {
		t.Fatalf("double StartTimer = %v, want ErrTimerAlreadyRunning", err)
	}

	stopped, err := StopTimer(ctx, pool, slug, ident, issueID, actor)
	if err != nil {
		t.Fatalf("StopTimer: %v", err)
	}
	if stopped.EndedAt == nil {
		t.Fatal("stopped timer should have ended_at")
	}
	if !stopped.EndedAt.After(stopped.StartedAt) {
		t.Fatalf("ended_at %v not after started_at %v", stopped.EndedAt, stopped.StartedAt)
	}

	// Stopping again is a 404 — the running row is gone.
	if _, err := StopTimer(ctx, pool, slug, ident, issueID, actor); !errors.Is(err, ErrNoRunningTimer) {
		t.Fatalf("second StopTimer = %v, want ErrNoRunningTimer", err)
	}

	// After a stop, starting again works.
	if _, err := StartTimer(ctx, pool, slug, ident, issueID, actor); err != nil {
		t.Fatalf("StartTimer after stop: %v", err)
	}
}

func TestTimeLogManual(t *testing.T) {
	ctx, pool, slug, ident, actor, issueID := setupTimeTest(t)

	start := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	end := start.Add(90 * time.Minute)
	e, err := LogTimeEntry(ctx, pool, slug, ident, issueID, actor, start, end, "morning session")
	if err != nil {
		t.Fatalf("LogTimeEntry: %v", err)
	}
	if e.Note != "morning session" || e.EndedAt == nil {
		t.Fatalf("got %+v", e)
	}

	// ended before started is a 400.
	if _, err := LogTimeEntry(ctx, pool, slug, ident, issueID, actor, end, start, ""); !errors.Is(err, ErrInvalidTimeEntry) {
		t.Fatalf("reversed LogTimeEntry = %v, want ErrInvalidTimeEntry", err)
	}
	// ended == started is a 400 too.
	if _, err := LogTimeEntry(ctx, pool, slug, ident, issueID, actor, start, start, ""); !errors.Is(err, ErrInvalidTimeEntry) {
		t.Fatalf("zero-length LogTimeEntry = %v, want ErrInvalidTimeEntry", err)
	}

	entries, total, err := ListTimeEntries(ctx, pool, slug, ident, issueID, actor)
	if err != nil {
		t.Fatalf("ListTimeEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	if total != 5400 {
		t.Fatalf("total_seconds = %d, want 5400 (90m)", total)
	}
}

func TestTimeTotalExcludesRunning(t *testing.T) {
	ctx, pool, slug, ident, actor, issueID := setupTimeTest(t)

	start := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	if _, err := LogTimeEntry(ctx, pool, slug, ident, issueID, actor, start, start.Add(time.Hour), ""); err != nil {
		t.Fatalf("LogTimeEntry: %v", err)
	}
	if _, err := StartTimer(ctx, pool, slug, ident, issueID, actor); err != nil {
		t.Fatalf("StartTimer: %v", err)
	}

	entries, total, err := ListTimeEntries(ctx, pool, slug, ident, issueID, actor)
	if err != nil {
		t.Fatalf("ListTimeEntries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	// The running entry contributes nothing to the stored total; the
	// client ticks it live from started_at.
	if total != 3600 {
		t.Fatalf("total_seconds = %d, want 3600", total)
	}
	// Newest first: the running entry (started now) sorts first.
	if entries[0].EndedAt != nil {
		t.Fatal("entries[0] should be the running entry")
	}
}

func TestTimeGuestReadOnly(t *testing.T) {
	ctx, pool, slug, ident, _, issueID := setupTimeTest(t)
	guest := createTestUser(t, pool, uniqueTestEmail("time-guest"))
	if _, err := pool.Exec(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role)
		 SELECT w.id, $2::uuid, 5 FROM workspaces w WHERE w.slug = $1`, slug, guest); err != nil {
		t.Fatalf("add guest: %v", err)
	}

	// Guest may read...
	if _, _, err := ListTimeEntries(ctx, pool, slug, ident, issueID, guest); err != nil {
		t.Fatalf("guest ListTimeEntries: %v", err)
	}
	// ...but not mutate.
	if _, err := StartTimer(ctx, pool, slug, ident, issueID, guest); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest StartTimer = %v, want ErrForbidden", err)
	}
	if _, err := LogTimeEntry(ctx, pool, slug, ident, issueID, guest,
		time.Now().Add(-time.Hour), time.Now(), ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest LogTimeEntry = %v, want ErrForbidden", err)
	}
}

func TestTimeUnknownIssue(t *testing.T) {
	ctx, pool, slug, ident, actor, _ := setupTimeTest(t)
	if _, err := StartTimer(ctx, pool, slug, ident, "00000000-0000-0000-0000-000000000000", actor); !errors.Is(err, ErrIssueNotFound) {
		t.Fatalf("StartTimer unknown issue = %v, want ErrIssueNotFound", err)
	}
}

// TestGetTimeSummary (C6T8): the project time report aggregates completed
// entries by day/week/user/issue. Running timers are excluded (same
// convention as ListTimeEntries' total_seconds).
func TestGetTimeSummary(t *testing.T) {
	ctx, pool, slug, ident, actor, issueID := setupTimeTest(t)

	now := time.Now().UTC()
	// 2h yesterday + 1h today by actor.
	if _, err := LogTimeEntry(ctx, pool, slug, ident, issueID, actor,
		now.Add(-26*time.Hour), now.Add(-24*time.Hour), "yesterday"); err != nil {
		t.Fatalf("log yesterday: %v", err)
	}
	if _, err := LogTimeEntry(ctx, pool, slug, ident, issueID, actor,
		now.Add(-2*time.Hour), now.Add(-1*time.Hour), "today"); err != nil {
		t.Fatalf("log today: %v", err)
	}
	// A second member logs 30m today.
	other := createTestUser(t, pool, uniqueTestEmail("time-other"))
	var wsID string
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM workspaces WHERE slug = $1`, slug).Scan(&wsID); err != nil {
		t.Fatalf("workspace id: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role)
		 VALUES ($1::uuid, $2::uuid, $3) ON CONFLICT DO NOTHING`,
		wsID, other, RoleMember); err != nil {
		t.Fatalf("add member: %v", err)
	}
	if _, err := LogTimeEntry(ctx, pool, slug, ident, issueID, other,
		now.Add(-50*time.Minute), now.Add(-20*time.Minute), "other"); err != nil {
		t.Fatalf("log other: %v", err)
	}
	// A running timer must NOT appear in the summary.
	if _, err := StartTimer(ctx, pool, slug, ident, issueID, actor); err != nil {
		t.Fatalf("start timer: %v", err)
	}

	byDay, err := GetTimeSummary(ctx, pool, slug, ident, actor, 7, "day")
	if err != nil {
		t.Fatalf("GetTimeSummary day: %v", err)
	}
	if byDay.GroupBy != "day" || byDay.Days != 7 {
		t.Fatalf("meta = %+v, want group_by=day days=7", byDay)
	}
	if len(byDay.Buckets) != 7 {
		t.Fatalf("day buckets = %d, want 7 zero-filled", len(byDay.Buckets))
	}
	if byDay.TotalSeconds != 3*3600+1800 {
		t.Fatalf("total = %d, want %d (2h+1h+30m)", byDay.TotalSeconds, 3*3600+1800)
	}
	var nonZero int
	for _, b := range byDay.Buckets {
		if b.Seconds > 0 {
			nonZero++
		}
	}
	if nonZero != 2 {
		t.Fatalf("non-zero day buckets = %d, want 2", nonZero)
	}

	byUser, err := GetTimeSummary(ctx, pool, slug, ident, actor, 7, "user")
	if err != nil {
		t.Fatalf("GetTimeSummary user: %v", err)
	}
	if len(byUser.Buckets) != 2 {
		t.Fatalf("user buckets = %d, want 2", len(byUser.Buckets))
	}
	// Descending by seconds: actor (3h) first.
	if byUser.Buckets[0].Seconds != 3*3600 || byUser.Buckets[1].Seconds != 1800 {
		t.Fatalf("user buckets = %+v, want actor 3h then other 30m", byUser.Buckets)
	}

	byIssue, err := GetTimeSummary(ctx, pool, slug, ident, actor, 7, "issue")
	if err != nil {
		t.Fatalf("GetTimeSummary issue: %v", err)
	}
	if len(byIssue.Buckets) != 1 || byIssue.Buckets[0].Entries != 3 {
		t.Fatalf("issue buckets = %+v, want one bucket with 3 entries", byIssue.Buckets)
	}

	byWeek, err := GetTimeSummary(ctx, pool, slug, ident, actor, 30, "week")
	if err != nil {
		t.Fatalf("GetTimeSummary week: %v", err)
	}
	if byWeek.TotalSeconds != byDay.TotalSeconds {
		t.Fatalf("week total = %d, want %d (same entries)", byWeek.TotalSeconds, byDay.TotalSeconds)
	}
	for _, b := range byWeek.Buckets {
		if len(b.Key) != 10 { // Monday "YYYY-MM-DD"
			t.Fatalf("week bucket key = %q, want YYYY-MM-DD", b.Key)
		}
	}

	// Unknown group_by falls back to day (the handler 400s; the service
	// stays defensive).
	def, err := GetTimeSummary(ctx, pool, slug, ident, actor, 7, "fortnight")
	if err != nil {
		t.Fatalf("GetTimeSummary bad group: %v", err)
	}
	if def.GroupBy != "day" {
		t.Fatalf("group_by = %q, want day fallback", def.GroupBy)
	}
}
