package service

// Email digest (C10T2): a daily ticker job aggregates each digest-opted-in
// user's last-24h digest-worthy events (issue.assigned, mention,
// issue.state_changed — all already recipient-gated to assignees /
// mentioned users / watchers+assignees at notify time) into ONE email.
// One digest_watermarks row per user per day makes re-runs idempotent;
// the watermark is claimed only when an email is actually sent, so an
// empty pass leaves nothing behind and events arriving later the same
// day can still be picked up.
// All tests run against the real test database — no skips.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// digestFixture is a user ready for digest tests. now is a fixed fake
// clock: the digest window is the 24h before now.
type digestFixture struct {
	pool  *pgxpool.Pool
	ctx   context.Context
	user  string
	email string
	now   time.Time
}

func setupDigestFixture(t *testing.T) *digestFixture {
	t.Helper()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	email := uniqueTestEmail("dig")
	user := createTestUser(t, pool, email)
	return &digestFixture{
		pool:  pool,
		ctx:   ctx,
		user:  user,
		email: email,
		now:   time.Date(2026, 10, 10, 6, 0, 0, 0, time.UTC),
	}
}

// digestNotify inserts one in-app notification row for the fixture user
// at the given age (relative to f.now).
func digestNotify(t *testing.T, f *digestFixture, eventType, title string, age time.Duration) {
	t.Helper()
	if _, err := f.pool.Exec(f.ctx,
		`INSERT INTO notifications (user_id, type, title, payload, created_at)
		 VALUES ($1::uuid, $2, $3, '{}'::jsonb, $4)`,
		f.user, eventType, title, f.now.Add(-age)); err != nil {
		t.Fatalf("insert notification: %v", err)
	}
}

// digestOutboxCount counts email.digest outbox rows addressed to the
// fixture user.
func digestOutboxCount(t *testing.T, f *digestFixture) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(f.ctx,
		`SELECT COUNT(*) FROM outbox WHERE event = 'email.digest' AND payload->>'to' = $1`,
		f.email).Scan(&n); err != nil {
		t.Fatalf("count digest outbox: %v", err)
	}
	return n
}

// digestWatermarkCount counts digest_watermarks rows for the fixture user.
func digestWatermarkCount(t *testing.T, f *digestFixture) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(f.ctx,
		`SELECT COUNT(*) FROM digest_watermarks WHERE user_id = $1::uuid`,
		f.user).Scan(&n); err != nil {
		t.Fatalf("count digest watermarks: %v", err)
	}
	return n
}

func enableDigestEmail(t *testing.T, f *digestFixture, email bool) {
	t.Helper()
	if _, err := SetNotificationPref(f.ctx, f.pool, f.user, NotifyDigestDaily, false, email); err != nil {
		t.Fatalf("SetNotificationPref: %v", err)
	}
}

// TestRunDigestAggregatesEvents: three digest-worthy events in the 24h
// window become ONE email.digest outbox row whose body mentions all
// three; an older event and a non-digest event are excluded.
func TestRunDigestAggregatesEvents(t *testing.T) {
	f := setupDigestFixture(t)
	enableDigestEmail(t, f, true)
	// C11T2: the pass is hour-gated (default send-after hour is 8, the
	// fixture clock is 06:00), so pin an explicit schedule — this test is
	// about aggregation, not due-ness.
	setDigestSchedule(t, f, DigestFrequencyDaily, 0)

	digestNotify(t, f, NotifyIssueAssigned, "Alice assigned you to GA-3", 2*time.Hour)
	digestNotify(t, f, NotifyMention, "Bob mentioned you in GA-7", 5*time.Hour)
	digestNotify(t, f, NotifyStateChanged, "Carol moved GA-2 to In Progress", 20*time.Hour)
	// Outside the 24h window: excluded.
	digestNotify(t, f, NotifyMention, "stale mention", 25*time.Hour)
	// Not digest-worthy: excluded.
	digestNotify(t, f, NotifyCommentCreated, "Dave commented on GA-1", 1*time.Hour)

	if err := RunDigest(f.ctx, f.pool, f.now); err != nil {
		t.Fatalf("RunDigest: %v", err)
	}

	if n := digestOutboxCount(t, f); n != 1 {
		t.Fatalf("digest outbox rows = %d, want 1 (one email per user per day)", n)
	}
	var body string
	if err := f.pool.QueryRow(f.ctx,
		`SELECT payload->>'body' FROM outbox WHERE event = 'email.digest' AND payload->>'to' = $1`,
		f.email).Scan(&body); err != nil {
		t.Fatalf("read digest body: %v", err)
	}
	for _, want := range []string{
		"Alice assigned you to GA-3",
		"Bob mentioned you in GA-7",
		"Carol moved GA-2 to In Progress",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("digest body missing %q\nbody:\n%s", want, body)
		}
	}
	for _, notWant := range []string{"stale mention", "Dave commented on GA-1"} {
		if strings.Contains(body, notWant) {
			t.Errorf("digest body should not contain %q\nbody:\n%s", notWant, body)
		}
	}
	if n := digestWatermarkCount(t, f); n != 1 {
		t.Fatalf("digest watermarks = %d, want 1", n)
	}
}

// TestRunDigestPrefOffSilent: digest email pref off (explicit false and
// absent row) → no email, no watermark.
func TestRunDigestPrefOffSilent(t *testing.T) {
	f := setupDigestFixture(t)
	enableDigestEmail(t, f, false)
	digestNotify(t, f, NotifyMention, "Bob mentioned you in GA-7", time.Hour)

	if err := RunDigest(f.ctx, f.pool, f.now); err != nil {
		t.Fatalf("RunDigest: %v", err)
	}
	if n := digestOutboxCount(t, f); n != 0 {
		t.Fatalf("digest outbox rows = %d, want 0 (pref off)", n)
	}
	if n := digestWatermarkCount(t, f); n != 0 {
		t.Fatalf("digest watermarks = %d, want 0 (pref off)", n)
	}
}

// TestRunDigestPrefAbsentSilent: no digest pref row at all → absent =
// email off (opt-in convention) → silent.
func TestRunDigestPrefAbsentSilent(t *testing.T) {
	f := setupDigestFixture(t)
	digestNotify(t, f, NotifyMention, "Bob mentioned you in GA-7", time.Hour)

	if err := RunDigest(f.ctx, f.pool, f.now); err != nil {
		t.Fatalf("RunDigest: %v", err)
	}
	if n := digestOutboxCount(t, f); n != 0 {
		t.Fatalf("digest outbox rows = %d, want 0 (no pref row)", n)
	}
}

// TestRunDigestSecondRunSameDayNoDuplicate: two passes the same day →
// exactly one email.
func TestRunDigestSecondRunSameDayNoDuplicate(t *testing.T) {
	f := setupDigestFixture(t)
	enableDigestEmail(t, f, true)
	// C11T2: pin an explicit schedule — the fixture clock (06:00) is
	// before the default send-after hour (8); this test is about
	// idempotency, not due-ness.
	setDigestSchedule(t, f, DigestFrequencyDaily, 0)
	digestNotify(t, f, NotifyMention, "Bob mentioned you in GA-7", time.Hour)

	if err := RunDigest(f.ctx, f.pool, f.now); err != nil {
		t.Fatalf("RunDigest first: %v", err)
	}
	if err := RunDigest(f.ctx, f.pool, f.now.Add(6*time.Hour)); err != nil {
		t.Fatalf("RunDigest second: %v", err)
	}
	if n := digestOutboxCount(t, f); n != 1 {
		t.Fatalf("digest outbox rows = %d, want 1 (second run idempotent)", n)
	}
}

// TestRunDigestEmptyDigestNoEmail: pref on but no digest-worthy events →
// no email and no watermark row claimed, so events arriving later the
// same day can still be picked up by a later pass.
func TestRunDigestEmptyDigestNoEmail(t *testing.T) {
	f := setupDigestFixture(t)
	enableDigestEmail(t, f, true)

	if err := RunDigest(f.ctx, f.pool, f.now); err != nil {
		t.Fatalf("RunDigest: %v", err)
	}
	if n := digestOutboxCount(t, f); n != 0 {
		t.Fatalf("digest outbox rows = %d, want 0 (empty digest)", n)
	}
	if n := digestWatermarkCount(t, f); n != 0 {
		t.Fatalf("digest watermarks = %d, want 0 (empty digest claims nothing)", n)
	}
}

// TestRunDigestInactiveUserSkipped: inactive users never get digests.
func TestRunDigestInactiveUserSkipped(t *testing.T) {
	f := setupDigestFixture(t)
	enableDigestEmail(t, f, true)
	digestNotify(t, f, NotifyMention, "Bob mentioned you in GA-7", time.Hour)
	if _, err := f.pool.Exec(f.ctx,
		`UPDATE users SET is_active = false WHERE id = $1::uuid`, f.user); err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	if err := RunDigest(f.ctx, f.pool, f.now); err != nil {
		t.Fatalf("RunDigest: %v", err)
	}
	if n := digestOutboxCount(t, f); n != 0 {
		t.Fatalf("digest outbox rows = %d, want 0 (inactive user)", n)
	}
}

// TestListNotificationPrefsDigestDefault: the digest key defaults to
// email off (opt-in) and in_app off (digest is email-only).
func TestListNotificationPrefsDigestDefault(t *testing.T) {
	f := setupDigestFixture(t)
	prefs, err := ListNotificationPrefs(f.ctx, f.pool, f.user)
	if err != nil {
		t.Fatalf("ListNotificationPrefs: %v", err)
	}
	for _, p := range prefs {
		if p.Event == NotifyDigestDaily {
			if p.Email {
				t.Errorf("digest default email = true, want false (opt-in)")
			}
			if p.InApp {
				t.Errorf("digest default in_app = true, want false (digest is email-only)")
			}
			return
		}
	}
	t.Errorf("digest key %q missing from ListNotificationPrefs", NotifyDigestDaily)
}
