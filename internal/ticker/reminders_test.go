package ticker

// Due-date reminder ticker (C8T6): RunOnce delegates to
// service.RunReminders against the live clock. The reminder logic
// itself (scan, claim, throttle, prefs) is covered by the service-level
// tests; here we verify the ticker wiring end to end — an issue due
// tomorrow gets its due_soon notification after one RunOnce, and a
// second RunOnce does not duplicate it.

import (
	"testing"
	"time"

	"glance/internal/service"
)

func TestReminderTickerRunOnce(t *testing.T) {
	ctx, pool, slug, ident, actor, _ := tickerTestSetup(t, "rem-tick")

	// tomorrow relative to the live clock, midnight UTC (DATE-safe).
	tomorrow := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, 1)
	iss, err := service.CreateIssue(ctx, pool, slug, ident, actor,
		service.CreateIssueInput{Name: "Ticker reminder", TargetDate: &tomorrow})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
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
			actor, service.NotifyDueSoon).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}

	rt := &ReminderTicker{Pool: pool}
	if err := rt.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if n := count(); n != 1 {
		t.Fatalf("due_soon after RunOnce = %d, want 1", n)
	}

	// Second pass: no duplicate.
	if err := rt.RunOnce(ctx); err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	if n := count(); n != 1 {
		t.Fatalf("due_soon after two RunOnce = %d, want 1", n)
	}
}
