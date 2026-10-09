package ticker

// Snooze-expiry ticker tests (C2T7, spec §3): ExpireOnce flips snoozed
// rows whose snoozed_till has passed back to pending (clearing the
// till), leaves future snoozes alone, resurfaces the flipped rows in
// the main inbox, and is idempotent. Broadcasts go nowhere in tests
// (service.Realtime is nil).

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"glance/internal/service"
)

// mustCreateIntakeIssue creates an issue opted into the triage inbox.
func mustCreateIntakeIssue(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, ident, actor, name string) *service.Issue {
	t.Helper()
	iss, err := service.CreateIssue(ctx, pool, slug, ident, actor, service.CreateIssueInput{Name: name, Intake: true})
	if err != nil {
		t.Fatalf("CreateIssue(intake) %s: %v", name, err)
	}
	return iss
}

// backdateSnooze moves a row's snoozed_till into the past — the snooze
// API refuses past dates (ErrSnoozeDatePast), so expiry is simulated
// with a direct UPDATE.
func backdateSnooze(t *testing.T, ctx context.Context, pool *pgxpool.Pool, issueID string) {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`UPDATE intake_issues SET snoozed_till = now() - interval '1 minute' WHERE issue_id = $1::uuid`,
		issueID); err != nil {
		t.Fatalf("backdate snooze: %v", err)
	}
}

func intakeRowState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, issueID string) (int16, *time.Time) {
	t.Helper()
	var status int16
	var till *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT status, snoozed_till FROM intake_issues WHERE issue_id = $1::uuid`,
		issueID).Scan(&status, &till); err != nil {
		t.Fatalf("read intake row: %v", err)
	}
	return status, till
}

func TestSnoozeTickerExpiresRows(t *testing.T) {
	ctx, pool, slug, ident, actor, _ := tickerTestSetup(t, "snooze-exp")

	// A snooze still in the future: the ticker must not touch it.
	active := mustCreateIntakeIssue(t, ctx, pool, slug, ident, actor, "still snoozed")
	if _, err := service.SnoozeIntakeIssue(ctx, pool, slug, ident, active.ID, actor, time.Now().Add(2*time.Hour)); err != nil {
		t.Fatalf("snooze active: %v", err)
	}

	// A snooze whose time has come: the ticker must flip it to pending.
	done := mustCreateIntakeIssue(t, ctx, pool, slug, ident, actor, "wake me")
	if _, err := service.SnoozeIntakeIssue(ctx, pool, slug, ident, done.ID, actor, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("snooze done: %v", err)
	}
	backdateSnooze(t, ctx, pool, done.ID)

	st := &SnoozeTicker{Pool: pool}
	if err := st.ExpireOnce(ctx); err != nil {
		t.Fatalf("ExpireOnce: %v", err)
	}

	// Flipped: status pending, till cleared.
	if status, till := intakeRowState(t, ctx, pool, done.ID); status != service.IntakePending || till != nil {
		t.Fatalf("expired row: status=%d till=%v, want pending/nil", status, till)
	}
	// Untouched: still snoozed with its till intact.
	if status, till := intakeRowState(t, ctx, pool, active.ID); status != service.IntakeSnoozed || till == nil || !till.After(time.Now()) {
		t.Fatalf("active row: status=%d till=%v, want snoozed/future", status, till)
	}

	// Resurfaced: the flipped row now appears in the main inbox…
	inbox, err := service.ListIntakeIssues(ctx, pool, slug, ident, actor, 50)
	if err != nil {
		t.Fatalf("ListIntakeIssues: %v", err)
	}
	found := false
	for _, ii := range inbox {
		if ii.IssueID == done.ID {
			found = true
		}
		if ii.IssueID == active.ID {
			t.Fatalf("still-snoozed row %s leaked into the main inbox", active.ID)
		}
	}
	if !found {
		t.Fatalf("expired row %s missing from the main inbox", done.ID)
	}
	// …and no longer in the snoozed list.
	snoozed, err := service.ListSnoozedIntakeIssues(ctx, pool, slug, ident, actor, 50)
	if err != nil {
		t.Fatalf("ListSnoozedIntakeIssues: %v", err)
	}
	if len(snoozed) != 1 || snoozed[0].IssueID != active.ID {
		t.Fatalf("snoozed list has %d rows, want exactly the active one", len(snoozed))
	}

	// Idempotent: a second pass flips nothing and errors nothing.
	if err := st.ExpireOnce(ctx); err != nil {
		t.Fatalf("ExpireOnce (2nd): %v", err)
	}
	if status, _ := intakeRowState(t, ctx, pool, done.ID); status != service.IntakePending {
		t.Fatalf("second pass changed status to %d", status)
	}
}
