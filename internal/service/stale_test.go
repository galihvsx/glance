package service

// Stale-issue nudge (C9T5, beyond-parity automation): the daily job notifies
// assignees and watchers about issues in non-done states whose updated_at is
// older than 30 days — one stale nudge per issue until it is updated again
// (an update resets the throttle). Reuses the issue_reminders table with
// kind='stale' (single row per issue, shared with due_soon/overdue). Honors
// notification prefs (new "stale" key, absent = in_app on, email off).
// All tests run against the real test database — no skips. Harness from
// reminders_test.go.

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// backdateUpdatedAt sets an issue's updated_at to the given timestamp —
// the stale job reads updated_at, which CreateIssue always stamps with the
// real now.
func backdateUpdatedAt(t *testing.T, pool *pgxpool.Pool, issueID string, ts time.Time) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE issues SET updated_at = $2 WHERE id = $1::uuid`,
		issueID, ts); err != nil {
		t.Fatalf("backdate updated_at: %v", err)
	}
}

// TestStaleNudgeOldUntouchedNotifies: a 30-day-old untouched issue in a
// non-done state notifies assignee and watcher exactly once each, in-app,
// and marks the issue_reminders row as stale.
func TestStaleNudgeOldUntouchedNotifies(t *testing.T) {
	f := setupReminderFixture(t)
	issueID := reminderIssue(t, f, "Forgotten work", nil)
	backdateUpdatedAt(t, f.pool, issueID, f.now.AddDate(0, 0, -31))

	if err := RunStaleNudge(f.ctx, f.pool, f.now); err != nil {
		t.Fatalf("RunStaleNudge: %v", err)
	}

	if n := countRemNotifications(t, f.pool, f.assignee, NotifyStale); n != 1 {
		t.Fatalf("assignee stale = %d, want 1", n)
	}
	if n := countRemNotifications(t, f.pool, f.watcher, NotifyStale); n != 1 {
		t.Fatalf("watcher stale = %d, want 1", n)
	}
	if kind := reminderKind(t, f.pool, issueID); kind != NotifyStale {
		t.Fatalf("issue_reminders kind = %q, want %q", kind, NotifyStale)
	}

	titles := notificationTitles(t, f.pool, f.assignee, NotifyStale)
	if len(titles) != 1 {
		t.Fatalf("titles = %v, want 1", titles)
	}
	if got := titles[0]; !containsAll(got, f.ident+"-", "Forgotten work", "stale") {
		t.Fatalf("title = %q, want display id + name + 'stale'", got)
	}

	// Default prefs: in_app on, email off — no outbox rows.
	if n := countRemOutbox(t, f.pool, "email.notification", f.assigneeMail); n != 0 {
		t.Fatalf("outbox rows for assignee = %d, want 0 (email off by default)", n)
	}
}

// TestStaleNudgeFreshUpdateSilent: an issue updated <30 days ago is never
// nudged.
func TestStaleNudgeFreshUpdateSilent(t *testing.T) {
	f := setupReminderFixture(t)
	issueID := reminderIssue(t, f, "Recently active", nil)
	backdateUpdatedAt(t, f.pool, issueID, f.now.AddDate(0, 0, -29))

	if err := RunStaleNudge(f.ctx, f.pool, f.now); err != nil {
		t.Fatalf("RunStaleNudge: %v", err)
	}
	if n := countRemNotifications(t, f.pool, f.assignee, NotifyStale); n != 0 {
		t.Fatalf("assignee stale for fresh issue = %d, want 0", n)
	}
	if n := countRemNotifications(t, f.pool, f.watcher, NotifyStale); n != 0 {
		t.Fatalf("watcher stale for fresh issue = %d, want 0", n)
	}
}

// TestStaleNudgeNoDuplicateSecondRun: a second pass for the same stale issue
// (no update in between) creates no further notifications.
func TestStaleNudgeNoDuplicateSecondRun(t *testing.T) {
	f := setupReminderFixture(t)
	issueID := reminderIssue(t, f, "Still forgotten", nil)
	backdateUpdatedAt(t, f.pool, issueID, f.now.AddDate(0, 0, -31))

	if err := RunStaleNudge(f.ctx, f.pool, f.now); err != nil {
		t.Fatalf("first RunStaleNudge: %v", err)
	}
	if err := RunStaleNudge(f.ctx, f.pool, f.now.Add(24*time.Hour)); err != nil {
		t.Fatalf("second RunStaleNudge: %v", err)
	}

	if n := countRemNotifications(t, f.pool, f.assignee, NotifyStale); n != 1 {
		t.Fatalf("assignee stale after two passes = %d, want 1", n)
	}
	if n := countRemNotifications(t, f.pool, f.watcher, NotifyStale); n != 1 {
		t.Fatalf("watcher stale after two passes = %d, want 1", n)
	}
}

// TestStaleNudgeUpdateResetsThrottle: an update after the nudge resets the
// throttle — the next stale pass (once the issue is stale again) nudges.
func TestStaleNudgeUpdateResetsThrottle(t *testing.T) {
	f := setupReminderFixture(t)
	issueID := reminderIssue(t, f, "Revived then forgotten", nil)
	backdateUpdatedAt(t, f.pool, issueID, f.now.AddDate(0, 0, -31))

	if err := RunStaleNudge(f.ctx, f.pool, f.now); err != nil {
		t.Fatalf("first RunStaleNudge: %v", err)
	}
	if n := countRemNotifications(t, f.pool, f.assignee, NotifyStale); n != 1 {
		t.Fatalf("assignee stale after first pass = %d, want 1", n)
	}

	// The issue is touched after the nudge (updated_at = fake now + 1h),
	// then sits untouched again until it is stale once more.
	backdateUpdatedAt(t, f.pool, issueID, f.now.Add(time.Hour))
	later := f.now.AddDate(0, 0, 31)
	if err := RunStaleNudge(f.ctx, f.pool, later); err != nil {
		t.Fatalf("second RunStaleNudge: %v", err)
	}

	if n := countRemNotifications(t, f.pool, f.assignee, NotifyStale); n != 2 {
		t.Fatalf("assignee stale after update + 31d = %d, want 2", n)
	}
	if n := countRemNotifications(t, f.pool, f.watcher, NotifyStale); n != 2 {
		t.Fatalf("watcher stale after update + 31d = %d, want 2", n)
	}
}

// TestStaleNudgeSkipsDoneState: an untouched-but-completed issue is never
// nudged (same for cancelled — one representative state is enough).
func TestStaleNudgeSkipsDoneState(t *testing.T) {
	f := setupReminderFixture(t)
	issueID := reminderIssue(t, f, "Done long ago", nil)

	var completedState string
	if err := f.pool.QueryRow(f.ctx,
		`SELECT s.id::text FROM states s
		   JOIN projects p ON p.id = s.project_id
		 WHERE p.workspace_id = (SELECT id FROM workspaces WHERE slug = $1)
		   AND p.identifier = $2 AND s."group" = 'completed'
		 ORDER BY s.sequence LIMIT 1`,
		f.wsSlug, f.ident).Scan(&completedState); err != nil {
		t.Fatalf("completed state: %v", err)
	}
	if _, err := UpdateIssue(f.ctx, f.pool, f.wsSlug, f.ident, issueID, f.creator,
		IssuePatch{StateID: &completedState}); err != nil {
		t.Fatalf("UpdateIssue to completed: %v", err)
	}
	// Backdate AFTER the state change: UpdateIssue refreshes updated_at.
	backdateUpdatedAt(t, f.pool, issueID, f.now.AddDate(0, 0, -31))

	if err := RunStaleNudge(f.ctx, f.pool, f.now); err != nil {
		t.Fatalf("RunStaleNudge: %v", err)
	}
	if n := countRemNotifications(t, f.pool, f.assignee, NotifyStale); n != 0 {
		t.Fatalf("assignee stale for completed issue = %d, want 0", n)
	}
	if n := countRemNotifications(t, f.pool, f.watcher, NotifyStale); n != 0 {
		t.Fatalf("watcher stale for completed issue = %d, want 0", n)
	}
}

// TestStaleNudgeRespectsPrefs: in_app=false suppresses the in-app row;
// email=true enqueues an email.notification outbox row; defaults are
// in_app on / email off.
func TestStaleNudgeRespectsPrefs(t *testing.T) {
	f := setupReminderFixture(t)
	// Assignee opted out of in-app for stale.
	setReminderPref(t, f.pool, f.assignee, NotifyStale, false, false)
	// Watcher wants email too.
	setReminderPref(t, f.pool, f.watcher, NotifyStale, true, true)

	issueID := reminderIssue(t, f, "Prefs check", nil)
	backdateUpdatedAt(t, f.pool, issueID, f.now.AddDate(0, 0, -31))
	if err := RunStaleNudge(f.ctx, f.pool, f.now); err != nil {
		t.Fatalf("RunStaleNudge: %v", err)
	}

	if n := countRemNotifications(t, f.pool, f.assignee, NotifyStale); n != 0 {
		t.Fatalf("opted-out assignee stale = %d, want 0", n)
	}
	if n := countRemNotifications(t, f.pool, f.watcher, NotifyStale); n != 1 {
		t.Fatalf("watcher stale = %d, want 1", n)
	}
	if n := countRemOutbox(t, f.pool, "email.notification", f.watcherMail); n != 1 {
		t.Fatalf("outbox email rows for watcher = %d, want 1", n)
	}
	if n := countRemOutbox(t, f.pool, "email.notification", f.assigneeMail); n != 0 {
		t.Fatalf("outbox email rows for assignee = %d, want 0", n)
	}
}

// TestStaleNudgePrefKeyRegistered: the "stale" event key is a real,
// settable preference (listed with defaults in_app on / email off).
func TestStaleNudgePrefKeyRegistered(t *testing.T) {
	f := setupReminderFixture(t)

	prefs, err := ListNotificationPrefs(f.ctx, f.pool, f.assignee)
	if err != nil {
		t.Fatalf("ListNotificationPrefs: %v", err)
	}
	found := false
	for _, p := range prefs {
		if p.Event == NotifyStale {
			found = true
			if !p.InApp || p.Email {
				t.Fatalf("default pref for stale = %+v, want in_app on / email off", p)
			}
		}
	}
	if !found {
		t.Fatalf("NotifyStale not listed in notification prefs")
	}

	p, err := SetNotificationPref(f.ctx, f.pool, f.assignee, NotifyStale, false, true)
	if err != nil {
		t.Fatalf("SetNotificationPref(stale): %v", err)
	}
	if p.Event != NotifyStale || p.InApp || !p.Email {
		t.Fatalf("SetNotificationPref(stale) = %+v, want in_app false / email true", p)
	}
}
