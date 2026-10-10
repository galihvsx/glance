package service

// Digest scheduling granularity (C11T2): per-user digest frequency
// (daily|weekly) and send-after hour (0-23, server-local), stored as
// value-encoded keys in notification_prefs (no migration). The digest
// pass gates each opted-in user on due-ness: the hour gate (current
// server-local hour >= pref hour) plus the frequency gate (daily: no
// watermark for today; weekly: no watermark in the last 7 days).
// All tests run against the real test database — no skips.

import (
	"testing"
	"time"
)

// pinTestLocal fixes time.Local for the server-local hour gate so the
// hour tests are deterministic regardless of the machine's zone.
func pinTestLocal(t *testing.T) {
	t.Helper()
	old := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = old })
}

// setDigestSchedule is the test helper that writes a user's schedule via
// the service API under test.
func setDigestSchedule(t *testing.T, f *digestFixture, frequency string, hour int) {
	t.Helper()
	if _, err := SetDigestSchedule(f.ctx, f.pool, f.user, frequency, hour); err != nil {
		t.Fatalf("SetDigestSchedule(%q, %d): %v", frequency, hour, err)
	}
}

// insertDigestWatermark plants a digest_watermarks row for the fixture
// user on the given UTC date.
func insertDigestWatermark(t *testing.T, f *digestFixture, date time.Time) {
	t.Helper()
	if _, err := f.pool.Exec(f.ctx,
		`INSERT INTO digest_watermarks (user_id, digest_date, sent_at)
		 VALUES ($1::uuid, $2::date, $3)`,
		f.user, date.UTC().Truncate(24*time.Hour), f.now); err != nil {
		t.Fatalf("insert digest watermark: %v", err)
	}
}

// TestGetDigestScheduleDefaults: no schedule rows → daily at 08:00.
func TestGetDigestScheduleDefaults(t *testing.T) {
	f := setupDigestFixture(t)
	sched, err := GetDigestSchedule(f.ctx, f.pool, f.user)
	if err != nil {
		t.Fatalf("GetDigestSchedule: %v", err)
	}
	if sched.Frequency != DigestFrequencyDaily {
		t.Errorf("frequency = %q, want %q", sched.Frequency, DigestFrequencyDaily)
	}
	if sched.Hour != DefaultDigestHour {
		t.Errorf("hour = %d, want %d", sched.Hour, DefaultDigestHour)
	}
}

// TestSetDigestScheduleRoundTrip: set weekly/14 → read back weekly/14,
// and the stored rows use the documented key encoding.
func TestSetDigestScheduleRoundTrip(t *testing.T) {
	f := setupDigestFixture(t)
	got, err := SetDigestSchedule(f.ctx, f.pool, f.user, DigestFrequencyWeekly, 14)
	if err != nil {
		t.Fatalf("SetDigestSchedule: %v", err)
	}
	if got.Frequency != DigestFrequencyWeekly || got.Hour != 14 {
		t.Fatalf("SetDigestSchedule returned %+v, want {weekly 14}", got)
	}
	sched, err := GetDigestSchedule(f.ctx, f.pool, f.user)
	if err != nil {
		t.Fatalf("GetDigestSchedule: %v", err)
	}
	if sched != got {
		t.Errorf("GetDigestSchedule = %+v, want %+v", sched, got)
	}
	var n int
	if err := f.pool.QueryRow(f.ctx,
		`SELECT COUNT(*) FROM notification_prefs
		  WHERE user_id = $1::uuid AND event IN ('digest.frequency:weekly', 'digest.hour:14')`,
		f.user).Scan(&n); err != nil {
		t.Fatalf("count schedule rows: %v", err)
	}
	if n != 2 {
		t.Errorf("schedule pref rows = %d, want 2 (frequency + hour keys)", n)
	}
}

// TestSetDigestScheduleValidation: unknown frequency and out-of-range
// hours are rejected; the 0 and 23 boundaries are accepted.
func TestSetDigestScheduleValidation(t *testing.T) {
	f := setupDigestFixture(t)
	for _, tc := range []struct {
		freq string
		hour int
	}{
		{"monthly", 8},
		{"", 8},
		{"daily", -1},
		{"daily", 24},
		{"weekly", 100},
	} {
		if _, err := SetDigestSchedule(f.ctx, f.pool, f.user, tc.freq, tc.hour); err == nil {
			t.Errorf("SetDigestSchedule(%q, %d): want error, got nil", tc.freq, tc.hour)
		}
	}
	for _, hour := range []int{0, 23} {
		if _, err := SetDigestSchedule(f.ctx, f.pool, f.user, DigestFrequencyDaily, hour); err != nil {
			t.Errorf("SetDigestSchedule(daily, %d): %v", hour, err)
		}
	}
	// A rejected write must not clobber the previous schedule.
	sched, err := GetDigestSchedule(f.ctx, f.pool, f.user)
	if err != nil {
		t.Fatalf("GetDigestSchedule: %v", err)
	}
	if sched.Hour != 23 {
		t.Errorf("hour after rejected writes = %d, want 23 (last valid write wins)", sched.Hour)
	}
}

// TestSetDigestScheduleOverwrite: a second write replaces the first —
// exactly one frequency row and one hour row remain.
func TestSetDigestScheduleOverwrite(t *testing.T) {
	f := setupDigestFixture(t)
	setDigestSchedule(t, f, DigestFrequencyWeekly, 14)
	setDigestSchedule(t, f, DigestFrequencyDaily, 9)
	sched, err := GetDigestSchedule(f.ctx, f.pool, f.user)
	if err != nil {
		t.Fatalf("GetDigestSchedule: %v", err)
	}
	if sched.Frequency != DigestFrequencyDaily || sched.Hour != 9 {
		t.Errorf("schedule = %+v, want {daily 9}", sched)
	}
	var n int
	if err := f.pool.QueryRow(f.ctx,
		`SELECT COUNT(*) FROM notification_prefs
		  WHERE user_id = $1::uuid AND (event LIKE 'digest.frequency:%' OR event LIKE 'digest.hour:%')`,
		f.user).Scan(&n); err != nil {
		t.Fatalf("count schedule rows: %v", err)
	}
	if n != 2 {
		t.Errorf("schedule pref rows = %d, want 2 (overwrite, not accumulate)", n)
	}
}

// TestRunDigestWeeklyNotResentWithin7d: a weekly user whose digest went
// out 3 days ago gets nothing from today's pass.
func TestRunDigestWeeklyNotResentWithin7d(t *testing.T) {
	pinTestLocal(t)
	f := setupDigestFixture(t)
	enableDigestEmail(t, f, true)
	setDigestSchedule(t, f, DigestFrequencyWeekly, 0)
	digestNotify(t, f, NotifyMention, "Bob mentioned you in GA-7", time.Hour)
	insertDigestWatermark(t, f, f.now.AddDate(0, 0, -3))

	if err := RunDigest(f.ctx, f.pool, f.now); err != nil {
		t.Fatalf("RunDigest: %v", err)
	}
	if n := digestOutboxCount(t, f); n != 0 {
		t.Fatalf("digest outbox rows = %d, want 0 (weekly digest sent 3d ago)", n)
	}
}

// TestRunDigestWeeklyResentAfter7d: a weekly user whose last digest is 8
// days old is due again.
func TestRunDigestWeeklyResentAfter7d(t *testing.T) {
	pinTestLocal(t)
	f := setupDigestFixture(t)
	enableDigestEmail(t, f, true)
	setDigestSchedule(t, f, DigestFrequencyWeekly, 0)
	digestNotify(t, f, NotifyMention, "Bob mentioned you in GA-7", time.Hour)
	insertDigestWatermark(t, f, f.now.AddDate(0, 0, -8))

	if err := RunDigest(f.ctx, f.pool, f.now); err != nil {
		t.Fatalf("RunDigest: %v", err)
	}
	if n := digestOutboxCount(t, f); n != 1 {
		t.Fatalf("digest outbox rows = %d, want 1 (weekly digest due after 8d)", n)
	}
}

// TestRunDigestWeeklyFirstSend: a weekly user with no watermark at all is
// due on the first pass.
func TestRunDigestWeeklyFirstSend(t *testing.T) {
	pinTestLocal(t)
	f := setupDigestFixture(t)
	enableDigestEmail(t, f, true)
	setDigestSchedule(t, f, DigestFrequencyWeekly, 0)
	digestNotify(t, f, NotifyMention, "Bob mentioned you in GA-7", time.Hour)

	if err := RunDigest(f.ctx, f.pool, f.now); err != nil {
		t.Fatalf("RunDigest: %v", err)
	}
	if n := digestOutboxCount(t, f); n != 1 {
		t.Fatalf("digest outbox rows = %d, want 1 (first weekly digest)", n)
	}
}

// TestRunDigestDailySentNextDay: a daily user whose watermark is
// yesterday's gets today's digest.
func TestRunDigestDailySentNextDay(t *testing.T) {
	pinTestLocal(t)
	f := setupDigestFixture(t)
	enableDigestEmail(t, f, true)
	setDigestSchedule(t, f, DigestFrequencyDaily, 0)
	digestNotify(t, f, NotifyMention, "Bob mentioned you in GA-7", time.Hour)
	insertDigestWatermark(t, f, f.now.AddDate(0, 0, -1))

	if err := RunDigest(f.ctx, f.pool, f.now); err != nil {
		t.Fatalf("RunDigest: %v", err)
	}
	if n := digestOutboxCount(t, f); n != 1 {
		t.Fatalf("digest outbox rows = %d, want 1 (daily digest due next day)", n)
	}
}

// TestRunDigestHourGateRespected: pref hour 8 — a 06:00 pass sends
// nothing (and claims no watermark, so the 09:00 pass still sends), a
// 09:00 pass sends.
func TestRunDigestHourGateRespected(t *testing.T) {
	pinTestLocal(t)
	f := setupDigestFixture(t)
	enableDigestEmail(t, f, true)
	setDigestSchedule(t, f, DigestFrequencyDaily, 8)
	digestNotify(t, f, NotifyMention, "Bob mentioned you in GA-7", time.Hour)

	early := time.Date(2026, 10, 10, 6, 0, 0, 0, time.UTC)
	if err := RunDigest(f.ctx, f.pool, early); err != nil {
		t.Fatalf("RunDigest at 06:00: %v", err)
	}
	if n := digestOutboxCount(t, f); n != 0 {
		t.Fatalf("digest outbox rows = %d, want 0 (06:00 < pref hour 8)", n)
	}
	if n := digestWatermarkCount(t, f); n != 0 {
		t.Fatalf("digest watermarks = %d, want 0 (early pass claims nothing)", n)
	}

	late := time.Date(2026, 10, 10, 9, 30, 0, 0, time.UTC)
	if err := RunDigest(f.ctx, f.pool, late); err != nil {
		t.Fatalf("RunDigest at 09:30: %v", err)
	}
	if n := digestOutboxCount(t, f); n != 1 {
		t.Fatalf("digest outbox rows = %d, want 1 (09:30 >= pref hour 8)", n)
	}
}

// TestRunDigestHourGateBoundary: the gate is inclusive — a pass exactly
// at the pref hour sends.
func TestRunDigestHourGateBoundary(t *testing.T) {
	pinTestLocal(t)
	f := setupDigestFixture(t)
	enableDigestEmail(t, f, true)
	setDigestSchedule(t, f, DigestFrequencyDaily, 8)
	digestNotify(t, f, NotifyMention, "Bob mentioned you in GA-7", time.Hour)

	at := time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)
	if err := RunDigest(f.ctx, f.pool, at); err != nil {
		t.Fatalf("RunDigest at 08:00: %v", err)
	}
	if n := digestOutboxCount(t, f); n != 1 {
		t.Fatalf("digest outbox rows = %d, want 1 (08:00 >= pref hour 8, inclusive)", n)
	}
}
