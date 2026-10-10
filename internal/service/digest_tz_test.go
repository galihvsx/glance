package service

// Digest per-user timezone (C13T1): digest.tz is an IANA timezone name
// stored as a value-encoded key in notification_prefs
// (digest.tz:Asia/Makassar); absent row = server-local (exact current
// behavior). The hour gate, the per-day claim window, and the watermark
// logic are all evaluated in the user's zone. All tests run against the
// real test database — no skips.

import (
	"testing"
	"time"
)

// setDigestScheduleTZ is the test helper that writes a user's schedule
// including the timezone via the service API under test.
func setDigestScheduleTZ(t *testing.T, f *digestFixture, frequency string, hour int, tz string) {
	t.Helper()
	if _, err := SetDigestSchedule(f.ctx, f.pool, f.user, frequency, hour, tz); err != nil {
		t.Fatalf("SetDigestSchedule(%q, %d, %q): %v", frequency, hour, tz, err)
	}
}

// digestNotifyAt inserts one in-app notification row for the fixture user
// at an explicit timestamp.
func digestNotifyAt(t *testing.T, f *digestFixture, eventType, title string, at time.Time) {
	t.Helper()
	if _, err := f.pool.Exec(f.ctx,
		`INSERT INTO notifications (user_id, type, title, payload, created_at)
		 VALUES ($1::uuid, $2, $3, '{}'::jsonb, $4)`,
		f.user, eventType, title, at); err != nil {
		t.Fatalf("insert notification: %v", err)
	}
}

// TestSetDigestScheduleTimezoneRoundTrip: set tz → read back; the stored
// rows use the digest.tz: value-encoded key.
func TestSetDigestScheduleTimezoneRoundTrip(t *testing.T) {
	f := setupDigestFixture(t)
	got, err := SetDigestSchedule(f.ctx, f.pool, f.user, DigestFrequencyDaily, 8, "Asia/Makassar")
	if err != nil {
		t.Fatalf("SetDigestSchedule: %v", err)
	}
	if got.Timezone != "Asia/Makassar" {
		t.Fatalf("SetDigestSchedule returned timezone %q, want Asia/Makassar", got.Timezone)
	}
	sched, err := GetDigestSchedule(f.ctx, f.pool, f.user)
	if err != nil {
		t.Fatalf("GetDigestSchedule: %v", err)
	}
	if sched.Timezone != "Asia/Makassar" {
		t.Errorf("GetDigestSchedule timezone = %q, want Asia/Makassar", sched.Timezone)
	}
	var n int
	if err := f.pool.QueryRow(f.ctx,
		`SELECT COUNT(*) FROM notification_prefs
		  WHERE user_id = $1::uuid AND event = 'digest.tz:Asia/Makassar'`,
		f.user).Scan(&n); err != nil {
		t.Fatalf("count tz rows: %v", err)
	}
	if n != 1 {
		t.Errorf("digest.tz rows = %d, want 1", n)
	}
}

// TestSetDigestScheduleTimezoneValidation: unknown IANA names are
// rejected without touching the stored schedule; empty resets to the
// server-local default.
func TestSetDigestScheduleTimezoneValidation(t *testing.T) {
	f := setupDigestFixture(t)
	setDigestScheduleTZ(t, f, DigestFrequencyDaily, 8, "Asia/Jakarta")

	for _, tz := range []string{"Not/AZone", "Mars/Olympus Mons", "EST5", "UTC+7", "jakarta"} {
		if _, err := SetDigestSchedule(f.ctx, f.pool, f.user, DigestFrequencyDaily, 8, tz); err == nil {
			t.Errorf("SetDigestSchedule(tz=%q): want error, got nil", tz)
		}
	}
	// Rejected writes must not clobber the stored tz.
	sched, err := GetDigestSchedule(f.ctx, f.pool, f.user)
	if err != nil {
		t.Fatalf("GetDigestSchedule: %v", err)
	}
	if sched.Timezone != "Asia/Jakarta" {
		t.Errorf("timezone after rejected writes = %q, want Asia/Jakarta", sched.Timezone)
	}
	// A valid IANA name is accepted; empty resets to server-local.
	if _, err := SetDigestSchedule(f.ctx, f.pool, f.user, DigestFrequencyDaily, 8, "UTC"); err != nil {
		t.Errorf("SetDigestSchedule(tz=UTC): %v", err)
	}
	if _, err := SetDigestSchedule(f.ctx, f.pool, f.user, DigestFrequencyDaily, 8, ""); err != nil {
		t.Fatalf("SetDigestSchedule(tz empty): %v", err)
	}
	sched, err = GetDigestSchedule(f.ctx, f.pool, f.user)
	if err != nil {
		t.Fatalf("GetDigestSchedule: %v", err)
	}
	if sched.Timezone != "" {
		t.Errorf("timezone after reset = %q, want empty (server-local default)", sched.Timezone)
	}
}

// TestSetDigestScheduleTimezoneOverwrite: a second write replaces the tz
// row — exactly one digest.tz row remains.
func TestSetDigestScheduleTimezoneOverwrite(t *testing.T) {
	f := setupDigestFixture(t)
	setDigestScheduleTZ(t, f, DigestFrequencyDaily, 8, "Asia/Jakarta")
	setDigestScheduleTZ(t, f, DigestFrequencyDaily, 8, "America/New_York")
	sched, err := GetDigestSchedule(f.ctx, f.pool, f.user)
	if err != nil {
		t.Fatalf("GetDigestSchedule: %v", err)
	}
	if sched.Timezone != "America/New_York" {
		t.Errorf("timezone = %q, want America/New_York", sched.Timezone)
	}
	var n int
	if err := f.pool.QueryRow(f.ctx,
		`SELECT COUNT(*) FROM notification_prefs
		  WHERE user_id = $1::uuid AND event LIKE 'digest.tz:%'`,
		f.user).Scan(&n); err != nil {
		t.Fatalf("count tz rows: %v", err)
	}
	if n != 1 {
		t.Errorf("digest.tz rows = %d, want 1 (overwrite, not accumulate)", n)
	}
}

// TestGetDigestScheduleTimezoneDefault: no tz row → empty (server-local),
// EffectiveTimezone names the server zone.
func TestGetDigestScheduleTimezoneDefault(t *testing.T) {
	pinTestLocal(t)
	f := setupDigestFixture(t)
	sched, err := GetDigestSchedule(f.ctx, f.pool, f.user)
	if err != nil {
		t.Fatalf("GetDigestSchedule: %v", err)
	}
	if sched.Timezone != "" {
		t.Errorf("default timezone = %q, want empty (server-local)", sched.Timezone)
	}
	if got := sched.EffectiveTimezone(); got != "UTC" {
		t.Errorf("EffectiveTimezone = %q, want UTC (pinned time.Local)", got)
	}
	jkt := DigestSchedule{Frequency: DigestFrequencyDaily, Hour: 8, Timezone: "Asia/Jakarta"}
	if got := jkt.EffectiveTimezone(); got != "Asia/Jakarta" {
		t.Errorf("EffectiveTimezone = %q, want Asia/Jakarta", got)
	}
}

// TestRunDigestHourGateUserTimezone: the hour gate runs in the user's
// zone, not the server's. Server is pinned to UTC; user tz is
// America/New_York (UTC-4 in October). Pref hour 8: at 08:00Z the old
// server-local logic would send (08:00 UTC >= 8) but the user's clock
// says 04:00 — must NOT send; at 12:00Z (08:00 New York) it must send.
func TestRunDigestHourGateUserTimezone(t *testing.T) {
	pinTestLocal(t)
	f := setupDigestFixture(t)
	enableDigestEmail(t, f, true)
	setDigestScheduleTZ(t, f, DigestFrequencyDaily, 8, "America/New_York")
	digestNotify(t, f, NotifyMention, "Bob mentioned you in GA-7", 30*time.Minute)

	at8Z := time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)
	if err := RunDigest(f.ctx, f.pool, at8Z); err != nil {
		t.Fatalf("RunDigest at 08:00Z: %v", err)
	}
	if n := digestOutboxCount(t, f); n != 0 {
		t.Fatalf("digest outbox rows = %d, want 0 (04:00 New York < pref hour 8)", n)
	}
	if n := digestWatermarkCount(t, f); n != 0 {
		t.Fatalf("digest watermarks = %d, want 0 (early pass claims nothing)", n)
	}

	digestNotifyAt(t, f, NotifyMention, "Bob mentioned you again in GA-7", at8Z.Add(time.Hour))
	at12Z := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	if err := RunDigest(f.ctx, f.pool, at12Z); err != nil {
		t.Fatalf("RunDigest at 12:00Z: %v", err)
	}
	if n := digestOutboxCount(t, f); n != 1 {
		t.Fatalf("digest outbox rows = %d, want 1 (08:00 New York >= pref hour 8)", n)
	}
}

// TestRunDigestHourGateJakarta: user in Asia/Jakarta (UTC+7), hour 8 —
// due at 08:00 Jakarta (01:00Z), not at 07:30 Jakarta (00:30Z).
func TestRunDigestHourGateJakarta(t *testing.T) {
	pinTestLocal(t)
	f := setupDigestFixture(t)
	enableDigestEmail(t, f, true)
	setDigestScheduleTZ(t, f, DigestFrequencyDaily, 8, "Asia/Jakarta")

	at0030Z := time.Date(2026, 10, 10, 0, 30, 0, 0, time.UTC)
	digestNotifyAt(t, f, NotifyMention, "Bob mentioned you in GA-7", at0030Z.Add(-30*time.Minute))
	if err := RunDigest(f.ctx, f.pool, at0030Z); err != nil {
		t.Fatalf("RunDigest at 00:30Z: %v", err)
	}
	if n := digestOutboxCount(t, f); n != 0 {
		t.Fatalf("digest outbox rows = %d, want 0 (07:30 Jakarta < pref hour 8)", n)
	}

	at0100Z := time.Date(2026, 10, 10, 1, 0, 0, 0, time.UTC)
	if err := RunDigest(f.ctx, f.pool, at0100Z); err != nil {
		t.Fatalf("RunDigest at 01:00Z: %v", err)
	}
	if n := digestOutboxCount(t, f); n != 1 {
		t.Fatalf("digest outbox rows = %d, want 1 (08:00 Jakarta >= pref hour 8)", n)
	}
}

// TestRunDigestNoDoubleSendAcrossTZMidnight: the claim window is the
// user's local day. A Jakarta user's digest claimed at 08:00 Jakarta
// (2026-10-10) must not resend at 08:30 Jakarta, and must send again at
// 08:00 Jakarta the next day.
func TestRunDigestNoDoubleSendAcrossTZMidnight(t *testing.T) {
	pinTestLocal(t)
	f := setupDigestFixture(t)
	enableDigestEmail(t, f, true)
	setDigestScheduleTZ(t, f, DigestFrequencyDaily, 8, "Asia/Jakarta")

	day1 := time.Date(2026, 10, 10, 1, 0, 0, 0, time.UTC) // 08:00 Jakarta Oct 10
	digestNotifyAt(t, f, NotifyMention, "day-1 mention", day1.Add(-time.Hour))
	if err := RunDigest(f.ctx, f.pool, day1); err != nil {
		t.Fatalf("RunDigest day 1: %v", err)
	}
	if n := digestOutboxCount(t, f); n != 1 {
		t.Fatalf("digest outbox rows = %d, want 1 (first day)", n)
	}

	// Same Jakarta day, later pass with a fresh event: no resend.
	day1b := time.Date(2026, 10, 10, 1, 30, 0, 0, time.UTC) // 08:30 Jakarta Oct 10
	digestNotifyAt(t, f, NotifyMention, "day-1 late mention", day1b.Add(-5*time.Minute))
	if err := RunDigest(f.ctx, f.pool, day1b); err != nil {
		t.Fatalf("RunDigest day 1 rerun: %v", err)
	}
	if n := digestOutboxCount(t, f); n != 1 {
		t.Fatalf("digest outbox rows = %d, want 1 (no double-send same local day)", n)
	}

	// Next Jakarta day: due again.
	day2 := time.Date(2026, 10, 11, 1, 0, 0, 0, time.UTC) // 08:00 Jakarta Oct 11
	digestNotifyAt(t, f, NotifyMention, "day-2 mention", day2.Add(-time.Hour))
	if err := RunDigest(f.ctx, f.pool, day2); err != nil {
		t.Fatalf("RunDigest day 2: %v", err)
	}
	if n := digestOutboxCount(t, f); n != 2 {
		t.Fatalf("digest outbox rows = %d, want 2 (new local day sends again)", n)
	}
}

// TestRunDigestDSTFallbackNoDoubleSend: America/New_York falls back on
// 2026-11-01 (02:00 EDT → 01:00 EST), so the local hour 00:00-01:00
// happens twice. A digest claimed in the first occurrence (00:30 EDT,
// 04:30Z) must not resend in the second (00:30 EST, 05:30Z) — both are
// the same local day.
func TestRunDigestDSTFallbackNoDoubleSend(t *testing.T) {
	pinTestLocal(t)
	f := setupDigestFixture(t)
	enableDigestEmail(t, f, true)
	setDigestScheduleTZ(t, f, DigestFrequencyDaily, 0, "America/New_York")

	edt := time.Date(2026, 11, 1, 4, 30, 0, 0, time.UTC) // 00:30 EDT, Nov 1
	digestNotifyAt(t, f, NotifyMention, "pre-fallback mention", edt.Add(-30*time.Minute))
	if err := RunDigest(f.ctx, f.pool, edt); err != nil {
		t.Fatalf("RunDigest at 00:30 EDT: %v", err)
	}
	if n := digestOutboxCount(t, f); n != 1 {
		t.Fatalf("digest outbox rows = %d, want 1", n)
	}
	if n := digestWatermarkCount(t, f); n != 1 {
		t.Fatalf("digest watermarks = %d, want 1", n)
	}

	est := time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC) // 00:30 EST, Nov 1
	digestNotifyAt(t, f, NotifyMention, "post-fallback mention", est.Add(-10*time.Minute))
	if err := RunDigest(f.ctx, f.pool, est); err != nil {
		t.Fatalf("RunDigest at 00:30 EST: %v", err)
	}
	if n := digestOutboxCount(t, f); n != 1 {
		t.Fatalf("digest outbox rows = %d, want 1 (no double-send across fallback)", n)
	}
	if n := digestWatermarkCount(t, f); n != 1 {
		t.Fatalf("digest watermarks = %d, want 1 (rerun claims nothing)", n)
	}
}
