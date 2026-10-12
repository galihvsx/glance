package service

// Weekly digest capacity hook (C29T2): capacity.overloaded alert rows
// surface in the WEEKLY digest (a "Capacity overloads" section) but not
// in the daily digest — the per-day in-app alert already covers daily
// delivery. All tests run against the real test database — no skips.

import (
	"strings"
	"testing"
	"time"
)

// capacityDigestOutboxBody returns the email.digest body for the
// fixture user.
func capacityDigestOutboxBody(t *testing.T, f *digestFixture) string {
	t.Helper()
	var body string
	if err := f.pool.QueryRow(f.ctx,
		`SELECT payload->>'body' FROM outbox WHERE event = 'email.digest' AND payload->>'to' = $1`,
		f.email).Scan(&body); err != nil {
		t.Fatalf("digest body: %v", err)
	}
	return body
}

func TestWeeklyDigestIncludesCapacityOverloads(t *testing.T) {
	f := setupDigestFixture(t)
	enableDigestEmail(t, f, true)
	// Weekly frequency; hour 0 so the due-ness gate passes at 06:00.
	setDigestSchedule(t, f, DigestFrequencyWeekly, 0)

	digestNotify(t, f, NotifyCapacityOverloaded, "Dev One is over capacity: 12 pts open against 8 pts weekly", 2*time.Hour)
	digestNotify(t, f, NotifyMention, "Bob mentioned you in GA-7", 3*time.Hour)

	if err := RunDigest(f.ctx, f.pool, f.now); err != nil {
		t.Fatalf("RunDigest: %v", err)
	}
	if n := digestOutboxCount(t, f); n != 1 {
		t.Fatalf("digest outbox rows = %d, want 1", n)
	}
	body := capacityDigestOutboxBody(t, f)
	if !strings.Contains(body, "Capacity overloads (1):") {
		t.Fatalf("weekly digest missing the capacity section:\n%s", body)
	}
	if !strings.Contains(body, "over capacity") {
		t.Fatalf("weekly digest missing the alert title:\n%s", body)
	}
	if !strings.Contains(body, "Mentions (1):") {
		t.Fatalf("weekly digest missing the regular section:\n%s", body)
	}
}

func TestDailyDigestExcludesCapacityOverloads(t *testing.T) {
	f := setupDigestFixture(t)
	enableDigestEmail(t, f, true)
	setDigestSchedule(t, f, DigestFrequencyDaily, 0)

	digestNotify(t, f, NotifyCapacityOverloaded, "Dev One is over capacity: 12 pts open against 8 pts weekly", 2*time.Hour)
	digestNotify(t, f, NotifyMention, "Bob mentioned you in GA-7", 3*time.Hour)

	if err := RunDigest(f.ctx, f.pool, f.now); err != nil {
		t.Fatalf("RunDigest: %v", err)
	}
	if n := digestOutboxCount(t, f); n != 1 {
		t.Fatalf("digest outbox rows = %d, want 1", n)
	}
	body := capacityDigestOutboxBody(t, f)
	if strings.Contains(body, "Capacity overloads") {
		t.Fatalf("daily digest must NOT include the capacity section:\n%s", body)
	}
	if strings.Contains(body, "over capacity") {
		t.Fatalf("daily digest must NOT include the alert title:\n%s", body)
	}
	if !strings.Contains(body, "Mentions (1):") {
		t.Fatalf("daily digest missing the regular section:\n%s", body)
	}
}
