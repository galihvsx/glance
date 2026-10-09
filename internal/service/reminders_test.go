package service

// Due-date reminders (C8T6, spec §3): the daily job notifies assignees
// and watchers about issues whose target_date is tomorrow (due_soon,
// once per issue) and overdue issues (overdue, throttled to once per
// 24h), honoring notification prefs (absent = in_app on, email off).
// All tests run against the real test database — no skips. Harness from
// workspace_test.go / issue_test.go / notify_test.go.

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// reminderFixture is a workspace + project + users ready for reminder
// tests. now is a fixed fake clock: "today" = 2026-10-09, so due_soon
// fires for target_date = 2026-10-10 and overdue for < 2026-10-09.
type reminderFixture struct {
	pool         *pgxpool.Pool
	ctx          context.Context
	wsSlug       string
	ident        string
	creator      string
	assignee     string
	assigneeMail string
	watcher      string
	watcherMail  string
	now          time.Time
}

func setupReminderFixture(t *testing.T) *reminderFixture {
	t.Helper()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	creator := createTestUser(t, pool, uniqueTestEmail("rem-creator"))
	assigneeMail := uniqueTestEmail("rem-assignee")
	watcherMail := uniqueTestEmail("rem-watcher")
	assignee := createTestUser(t, pool, assigneeMail)
	watcher := createTestUser(t, pool, watcherMail)

	wsSlug := uniqueTestSlug("rem")
	createTestWorkspace(t, pool, "Rem Corp", wsSlug, creator)
	if _, err := CreateProject(ctx, pool, wsSlug, creator, "Rem", "REM"); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	return &reminderFixture{
		pool: pool, ctx: ctx, wsSlug: wsSlug, ident: "REM",
		creator: creator, assignee: assignee, assigneeMail: assigneeMail,
		watcher: watcher, watcherMail: watcherMail, now: now,
	}
}

// reminderIssue creates an issue with the given target date (nil = no
// target date), assigns the fixture assignee, and subscribes the
// fixture watcher. Midnight-UTC dates keep the DATE column TZ-safe
// under the test DB session.
func reminderIssue(t *testing.T, f *reminderFixture, name string, targetDate *time.Time) string {
	t.Helper()
	var td *time.Time
	if targetDate != nil {
		d := time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(), 0, 0, 0, 0, time.UTC)
		td = &d
	}
	iss, err := CreateIssue(f.ctx, f.pool, f.wsSlug, f.ident, f.creator,
		CreateIssueInput{Name: name, TargetDate: td})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if _, err := f.pool.Exec(f.ctx,
		`INSERT INTO issue_assignees (issue_id, user_id) VALUES ($1::uuid, $2::uuid)`,
		iss.ID, f.assignee); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if _, err := f.pool.Exec(f.ctx,
		`INSERT INTO issue_subscribers (issue_id, user_id) VALUES ($1::uuid, $2::uuid)`,
		iss.ID, f.watcher); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	return iss.ID
}

func countRemNotifications(t *testing.T, pool *pgxpool.Pool, userID, typ string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM notifications WHERE user_id = $1::uuid AND type = $2`,
		userID, typ).Scan(&n); err != nil {
		t.Fatalf("count notifications: %v", err)
	}
	return n
}

func countRemOutbox(t *testing.T, pool *pgxpool.Pool, event, to string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM outbox WHERE event = $1 AND payload->>'to' = $2`,
		event, to).Scan(&n); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	return n
}

func reminderKind(t *testing.T, pool *pgxpool.Pool, issueID string) string {
	t.Helper()
	var kind string
	if err := pool.QueryRow(context.Background(),
		`SELECT kind FROM issue_reminders WHERE issue_id = $1::uuid`,
		issueID).Scan(&kind); err != nil {
		t.Fatalf("read issue_reminders: %v", err)
	}
	return kind
}

func notificationTitles(t *testing.T, pool *pgxpool.Pool, userID, typ string) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT title FROM notifications WHERE user_id = $1::uuid AND type = $2 ORDER BY created_at`,
		userID, typ)
	if err != nil {
		t.Fatalf("read notification titles: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			t.Fatalf("scan title: %v", err)
		}
		out = append(out, title)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

// setReminderPref writes one notification_prefs row for the event.
func setReminderPref(t *testing.T, pool *pgxpool.Pool, userID, event string, inApp, email bool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO notification_prefs (user_id, event, in_app, email)
		 VALUES ($1::uuid, $2, $3, $4)
		 ON CONFLICT (user_id, event) DO UPDATE SET in_app = EXCLUDED.in_app, email = EXCLUDED.email`,
		userID, event, inApp, email); err != nil {
		t.Fatalf("set pref: %v", err)
	}
}

// TestRemindersDueTomorrowNotifies: an issue due tomorrow notifies the
// assignee and the watcher exactly once each, in-app, and marks the
// issue_reminders row as due_soon.
func TestRemindersDueTomorrowNotifies(t *testing.T) {
	f := setupReminderFixture(t)
	tomorrow := f.now.AddDate(0, 0, 1)
	issueID := reminderIssue(t, f, "Ship it", &tomorrow)

	if err := RunReminders(f.ctx, f.pool, f.now); err != nil {
		t.Fatalf("RunReminders: %v", err)
	}

	if n := countRemNotifications(t, f.pool, f.assignee, NotifyDueSoon); n != 1 {
		t.Fatalf("assignee due_soon = %d, want 1", n)
	}
	if n := countRemNotifications(t, f.pool, f.watcher, NotifyDueSoon); n != 1 {
		t.Fatalf("watcher due_soon = %d, want 1", n)
	}
	if kind := reminderKind(t, f.pool, issueID); kind != NotifyDueSoon {
		t.Fatalf("issue_reminders kind = %q, want %q", kind, NotifyDueSoon)
	}

	titles := notificationTitles(t, f.pool, f.assignee, NotifyDueSoon)
	if len(titles) != 1 {
		t.Fatalf("titles = %v, want 1", titles)
	}
	if got := titles[0]; !containsAll(got, f.ident+"-", "Ship it", "due tomorrow") {
		t.Fatalf("title = %q, want display id + name + 'due tomorrow'", got)
	}

	// Default prefs: in_app on, email off — no outbox rows.
	if n := countRemOutbox(t, f.pool, "email.notification", f.assigneeMail); n != 0 {
		t.Fatalf("outbox rows for assignee = %d, want 0 (email off by default)", n)
	}
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		found := false
		for i := 0; i+len(p) <= len(s); i++ {
			if s[i:i+len(p)] == p {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// TestRemindersDueSoonFiresOnce: a second pass for the same due-soon
// issue creates no further notifications.
func TestRemindersDueSoonFiresOnce(t *testing.T) {
	f := setupReminderFixture(t)
	tomorrow := f.now.AddDate(0, 0, 1)
	reminderIssue(t, f, "Ship it", &tomorrow)

	if err := RunReminders(f.ctx, f.pool, f.now); err != nil {
		t.Fatalf("first RunReminders: %v", err)
	}
	if err := RunReminders(f.ctx, f.pool, f.now.Add(time.Hour)); err != nil {
		t.Fatalf("second RunReminders: %v", err)
	}

	if n := countRemNotifications(t, f.pool, f.assignee, NotifyDueSoon); n != 1 {
		t.Fatalf("assignee due_soon after two passes = %d, want 1", n)
	}
	if n := countRemNotifications(t, f.pool, f.watcher, NotifyDueSoon); n != 1 {
		t.Fatalf("watcher due_soon after two passes = %d, want 1", n)
	}
}

// TestRemindersOverdueThrottle: an overdue issue notifies once, is NOT
// re-notified within 24h, and IS re-notified once 24h have passed.
func TestRemindersOverdueThrottle(t *testing.T) {
	f := setupReminderFixture(t)
	twoDaysAgo := f.now.AddDate(0, 0, -2)
	issueID := reminderIssue(t, f, "Late already", &twoDaysAgo)

	if err := RunReminders(f.ctx, f.pool, f.now); err != nil {
		t.Fatalf("first RunReminders: %v", err)
	}
	if n := countRemNotifications(t, f.pool, f.assignee, NotifyOverdue); n != 1 {
		t.Fatalf("assignee overdue after first pass = %d, want 1", n)
	}
	if kind := reminderKind(t, f.pool, issueID); kind != NotifyOverdue {
		t.Fatalf("issue_reminders kind = %q, want %q", kind, NotifyOverdue)
	}

	// 1h later: throttled — no new notification.
	if err := RunReminders(f.ctx, f.pool, f.now.Add(time.Hour)); err != nil {
		t.Fatalf("second RunReminders: %v", err)
	}
	if n := countRemNotifications(t, f.pool, f.assignee, NotifyOverdue); n != 1 {
		t.Fatalf("assignee overdue after throttled pass = %d, want 1", n)
	}
	if n := countRemNotifications(t, f.pool, f.watcher, NotifyOverdue); n != 1 {
		t.Fatalf("watcher overdue after throttled pass = %d, want 1", n)
	}

	// 25h later: the throttle window has passed — re-notify once.
	if err := RunReminders(f.ctx, f.pool, f.now.Add(25*time.Hour)); err != nil {
		t.Fatalf("third RunReminders: %v", err)
	}
	if n := countRemNotifications(t, f.pool, f.assignee, NotifyOverdue); n != 2 {
		t.Fatalf("assignee overdue after 25h = %d, want 2", n)
	}
	if n := countRemNotifications(t, f.pool, f.watcher, NotifyOverdue); n != 2 {
		t.Fatalf("watcher overdue after 25h = %d, want 2", n)
	}
}

// TestRemindersRespectPrefs: in_app=false suppresses the in-app row;
// email=true enqueues an email.notification outbox row; defaults are
// in_app on / email off.
func TestRemindersRespectPrefs(t *testing.T) {
	f := setupReminderFixture(t)
	// Assignee opted out of in-app for due_soon.
	setReminderPref(t, f.pool, f.assignee, NotifyDueSoon, false, false)
	// Watcher wants email too.
	setReminderPref(t, f.pool, f.watcher, NotifyDueSoon, true, true)

	tomorrow := f.now.AddDate(0, 0, 1)
	reminderIssue(t, f, "Prefs check", &tomorrow)
	if err := RunReminders(f.ctx, f.pool, f.now); err != nil {
		t.Fatalf("RunReminders: %v", err)
	}

	if n := countRemNotifications(t, f.pool, f.assignee, NotifyDueSoon); n != 0 {
		t.Fatalf("opted-out assignee due_soon = %d, want 0", n)
	}
	if n := countRemNotifications(t, f.pool, f.watcher, NotifyDueSoon); n != 1 {
		t.Fatalf("watcher due_soon = %d, want 1", n)
	}
	if n := countRemOutbox(t, f.pool, "email.notification", f.watcherMail); n != 1 {
		t.Fatalf("outbox email rows for watcher = %d, want 1", n)
	}
	if n := countRemOutbox(t, f.pool, "email.notification", f.assigneeMail); n != 0 {
		t.Fatalf("outbox email rows for assignee = %d, want 0", n)
	}
}

// TestRemindersSkipDoneIssues: issues in a completed (or cancelled)
// state are never reminded.
func TestRemindersSkipDoneIssues(t *testing.T) {
	f := setupReminderFixture(t)
	tomorrow := f.now.AddDate(0, 0, 1)
	issueID := reminderIssue(t, f, "Already done", &tomorrow)

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

	if err := RunReminders(f.ctx, f.pool, f.now); err != nil {
		t.Fatalf("RunReminders: %v", err)
	}
	if n := countRemNotifications(t, f.pool, f.assignee, NotifyDueSoon); n != 0 {
		t.Fatalf("assignee due_soon for done issue = %d, want 0", n)
	}
	if n := countRemNotifications(t, f.pool, f.watcher, NotifyDueSoon); n != 0 {
		t.Fatalf("watcher due_soon for done issue = %d, want 0", n)
	}
}

// TestRemindersIgnoresUndated: an issue without a target date is never
// matched.
func TestRemindersIgnoresUndated(t *testing.T) {
	f := setupReminderFixture(t)
	reminderIssue(t, f, "No date", nil)

	if err := RunReminders(f.ctx, f.pool, f.now); err != nil {
		t.Fatalf("RunReminders: %v", err)
	}
	if n := countRemNotifications(t, f.pool, f.assignee, NotifyDueSoon); n != 0 {
		t.Fatalf("assignee due_soon for undated issue = %d, want 0", n)
	}
}

// TestRemindersIssueDueTodayNotDueSoon: with date granularity, an issue
// due today is not "due tomorrow" — it gets the overdue reminder once
// its target date has passed.
func TestRemindersIssueDueTodayNotDueSoon(t *testing.T) {
	f := setupReminderFixture(t)
	reminderIssue(t, f, "Due today", &f.now) // target_date == today

	if err := RunReminders(f.ctx, f.pool, f.now); err != nil {
		t.Fatalf("RunReminders: %v", err)
	}
	if n := countRemNotifications(t, f.pool, f.assignee, NotifyDueSoon); n != 0 {
		t.Fatalf("assignee due_soon for issue due today = %d, want 0", n)
	}
}
