package ticker

// Stale-issue nudge ticker (C9T5): RunOnce delegates to
// service.RunStaleNudge against the live clock. The nudge logic itself
// (scan, claim, throttle, prefs) is covered by the service-level tests;
// here we verify the ticker wiring end to end — a 31-day-untouched issue
// gets its stale notification after one RunOnce, and a second RunOnce
// does not duplicate it.

import (
	"testing"
	"time"

	"glance/internal/service"
)

func TestStaleTickerRunOnce(t *testing.T) {
	ctx, pool, slug, ident, actor, _ := tickerTestSetup(t, "stale-tick")

	// 31 days before the live clock (timestamptz, TZ-safe).
	old := time.Now().UTC().AddDate(0, 0, -31)
	iss, err := service.CreateIssue(ctx, pool, slug, ident, actor,
		service.CreateIssueInput{Name: "Ticker stale"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE issues SET updated_at = $2 WHERE id = $1::uuid`,
		iss.ID, old); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO issue_assignees (issue_id, user_id) VALUES ($1::uuid, $2::uuid)`,
		iss.ID, actor); err != nil {
		t.Fatalf("assign: %v", err)
	}

	count := func() int {
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM notifications WHERE user_id = $1::uuid AND type = $2`,
			actor, service.NotifyStale).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}

	st := &StaleTicker{Pool: pool}
	if err := st.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if n := count(); n != 1 {
		t.Fatalf("stale after RunOnce = %d, want 1", n)
	}

	// Second pass: no duplicate.
	if err := st.RunOnce(ctx); err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	if n := count(); n != 1 {
		t.Fatalf("stale after two RunOnce = %d, want 1", n)
	}
}
