package service

// C13T0 automation run retention: service tests. A per-project retention
// window (default 90 days, pinned) bounds the unbounded automation_runs
// growth from C12T1; 0 = keep forever; negative rejected at write.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// insertAutomationRunAt writes a run row with an explicit fired_at —
// recordAutomationRunTx always stamps now(), so tests backdate via SQL.
func insertAutomationRunAt(t *testing.T, pool *pgxpool.Pool, ruleID, issueID string, firedAt time.Time) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO automation_runs (rule_id, issue_id, trigger_type, fired_at, actions)
		 VALUES ($1::uuid, $2::uuid, 'issue.state_changed', $3, '[]'::jsonb)`,
		ruleID, issueID, firedAt)
	if err != nil {
		t.Fatalf("insert automation run: %v", err)
	}
}

func countAutomationRuns(t *testing.T, pool *pgxpool.Pool, ruleID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM automation_runs WHERE rule_id = $1::uuid`, ruleID).Scan(&n); err != nil {
		t.Fatalf("count automation runs: %v", err)
	}
	return n
}

// retentionRule creates one firing rule and one issue for run tests.
func retentionRule(t *testing.T, fx automationFixture) (ruleID, issueID string) {
	t.Helper()
	ctx := context.Background()
	in := automationRuleInput(fx, []string{fx.started}, []AutomationAction{
		{Type: "add_comment", Body: autoStrPtr("retention")},
	})
	in.Name = "retention probe"
	r, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in)
	if err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "retention issue")
	return r.ID, iss.ID
}

func TestPruneAutomationRunsDefaultRetention(t *testing.T) {
	// The default window is pinned: NULL column = 90 days.
	if DefaultAutomationRunRetentionDays != 90 {
		t.Fatalf("DefaultAutomationRunRetentionDays = %d, want 90", DefaultAutomationRunRetentionDays)
	}
	fx := setupAutomationFixture(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	ruleID, issueID := retentionRule(t, fx)

	insertAutomationRunAt(t, fx.pool, ruleID, issueID, now.Add(-91*24*time.Hour)) // pruned
	insertAutomationRunAt(t, fx.pool, ruleID, issueID, now.Add(-89*24*time.Hour)) // kept
	insertAutomationRunAt(t, fx.pool, ruleID, issueID, now.Add(-time.Hour))       // kept

	deleted, err := PruneAutomationRuns(ctx, fx.pool, now)
	if err != nil {
		t.Fatalf("PruneAutomationRuns: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	if n := countAutomationRuns(t, fx.pool, ruleID); n != 2 {
		t.Fatalf("remaining runs = %d, want 2", n)
	}
}

func TestPruneAutomationRunsCustomRetentionAppliesOnNextSweep(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	ruleID, issueID := retentionRule(t, fx)

	days := 30
	if _, err := UpdateProject(ctx, fx.pool, fx.slug, fx.ident, fx.admin,
		ProjectPatch{AutomationRunRetentionDays: &days}); err != nil {
		t.Fatalf("UpdateProject retention 30: %v", err)
	}
	insertAutomationRunAt(t, fx.pool, ruleID, issueID, now.Add(-45*24*time.Hour)) // pruned by 30d window
	insertAutomationRunAt(t, fx.pool, ruleID, issueID, now.Add(-20*24*time.Hour)) // kept under 30d window

	if _, err := PruneAutomationRuns(ctx, fx.pool, now); err != nil {
		t.Fatalf("PruneAutomationRuns (30d): %v", err)
	}
	if n := countAutomationRuns(t, fx.pool, ruleID); n != 1 {
		t.Fatalf("remaining runs = %d, want 1 (the 20d-old run)", n)
	}

	// Tighten to 10 days: the surviving 20d-old run must go on the
	// NEXT sweep — the new window applies without any other change.
	days = 10
	if _, err := UpdateProject(ctx, fx.pool, fx.slug, fx.ident, fx.admin,
		ProjectPatch{AutomationRunRetentionDays: &days}); err != nil {
		t.Fatalf("UpdateProject retention 10: %v", err)
	}
	deleted, err := PruneAutomationRuns(ctx, fx.pool, now)
	if err != nil {
		t.Fatalf("PruneAutomationRuns (10d): %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	if n := countAutomationRuns(t, fx.pool, ruleID); n != 0 {
		t.Fatalf("remaining runs = %d, want 0", n)
	}
}

func TestPruneAutomationRunsZeroKeepsEverything(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	ruleID, issueID := retentionRule(t, fx)

	zero := 0
	if _, err := UpdateProject(ctx, fx.pool, fx.slug, fx.ident, fx.admin,
		ProjectPatch{AutomationRunRetentionDays: &zero}); err != nil {
		t.Fatalf("UpdateProject retention 0: %v", err)
	}
	insertAutomationRunAt(t, fx.pool, ruleID, issueID, now.Add(-400*24*time.Hour))

	deleted, err := PruneAutomationRuns(ctx, fx.pool, now)
	if err != nil {
		t.Fatalf("PruneAutomationRuns: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("deleted = %d, want 0 (retention 0 = keep forever)", deleted)
	}
	if n := countAutomationRuns(t, fx.pool, ruleID); n != 1 {
		t.Fatalf("remaining runs = %d, want 1", n)
	}
}

func TestPruneAutomationRunsRejectsNegative(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	neg := -1
	if _, err := UpdateProject(ctx, fx.pool, fx.slug, fx.ident, fx.admin,
		ProjectPatch{AutomationRunRetentionDays: &neg}); !errors.Is(err, ErrInvalidAutomationRunRetentionDays) {
		t.Fatalf("UpdateProject retention -1: err = %v, want ErrInvalidAutomationRunRetentionDays", err)
	}
	// The rejected write must not have touched the column.
	got, err := GetAutomationRunRetentionDays(ctx, fx.pool, fx.slug, fx.ident, fx.admin)
	if err != nil {
		t.Fatalf("GetAutomationRunRetentionDays: %v", err)
	}
	if got != DefaultAutomationRunRetentionDays {
		t.Fatalf("retention after rejected write = %d, want default %d", got, DefaultAutomationRunRetentionDays)
	}
}

func TestPruneAutomationRunsScopedToProject(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	ruleID, issueID := retentionRule(t, fx)
	insertAutomationRunAt(t, fx.pool, ruleID, issueID, now.Add(-100*24*time.Hour))

	// A second project with its own prunable run.
	ident2 := uniqueTestIdent()
	if _, err := CreateProject(ctx, fx.pool, fx.slug, fx.admin, "Other", ident2); err != nil {
		t.Fatalf("CreateProject other: %v", err)
	}
	var rule2, iss2 string
	{
		// The trigger must reference a state in the SECOND project.
		states2, err := ListStates(ctx, fx.pool, fx.slug, ident2, fx.admin)
		if err != nil {
			t.Fatalf("ListStates other: %v", err)
		}
		var started2 string
		for _, s := range states2 {
			if s.Group == "started" {
				started2 = s.ID
			}
		}
		if started2 == "" {
			t.Fatal("no started state in second project")
		}
		in2 := AutomationRuleInput{
			Name:    "other probe",
			Trigger: AutomationTrigger{Type: "issue.state_changed", ToStates: []string{started2}},
			Actions: []AutomationAction{{Type: "add_comment", Body: autoStrPtr("other")}},
		}
		r2, err := CreateAutomationRule(ctx, fx.pool, fx.slug, ident2, fx.admin, in2)
		if err != nil {
			t.Fatalf("CreateAutomationRule other: %v", err)
		}
		iss := createTestIssue(t, fx.pool, fx.slug, ident2, fx.admin, "other issue")
		rule2, iss2 = r2.ID, iss.ID
	}
	insertAutomationRunAt(t, fx.pool, rule2, iss2, now.Add(-100*24*time.Hour))

	deleted, err := PruneAutomationRuns(ctx, fx.pool, now)
	if err != nil {
		t.Fatalf("PruneAutomationRuns: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("deleted = %d, want 2 (one per project)", deleted)
	}
	// Tighten only the first project and re-seed: the second project's
	// runs must be untouched by the first project's window.
	days := 10
	if _, err := UpdateProject(ctx, fx.pool, fx.slug, fx.ident, fx.admin,
		ProjectPatch{AutomationRunRetentionDays: &days}); err != nil {
		t.Fatalf("UpdateProject: %v", err)
	}
	insertAutomationRunAt(t, fx.pool, ruleID, issueID, now.Add(-20*24*time.Hour))
	insertAutomationRunAt(t, fx.pool, rule2, iss2, now.Add(-20*24*time.Hour))
	if _, err := PruneAutomationRuns(ctx, fx.pool, now); err != nil {
		t.Fatalf("PruneAutomationRuns: %v", err)
	}
	if n := countAutomationRuns(t, fx.pool, ruleID); n != 0 {
		t.Fatalf("project 1 remaining = %d, want 0", n)
	}
	if n := countAutomationRuns(t, fx.pool, rule2); n != 1 {
		t.Fatalf("project 2 remaining = %d, want 1 (default 90d keeps the 20d run)", n)
	}
}

func TestGetAutomationRunRetentionDays(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	// NULL column → effective default, readable by a member.
	got, err := GetAutomationRunRetentionDays(ctx, fx.pool, fx.slug, fx.ident, fx.member)
	if err != nil {
		t.Fatalf("GetAutomationRunRetentionDays: %v", err)
	}
	if got != DefaultAutomationRunRetentionDays {
		t.Fatalf("retention = %d, want default %d", got, DefaultAutomationRunRetentionDays)
	}
	// Configured value round-trips; 0 (keep forever) round-trips too.
	for _, want := range []int{45, 0} {
		d := want
		if _, err := UpdateProject(ctx, fx.pool, fx.slug, fx.ident, fx.admin,
			ProjectPatch{AutomationRunRetentionDays: &d}); err != nil {
			t.Fatalf("UpdateProject retention %d: %v", want, err)
		}
		got, err := GetAutomationRunRetentionDays(ctx, fx.pool, fx.slug, fx.ident, fx.member)
		if err != nil {
			t.Fatalf("GetAutomationRunRetentionDays: %v", err)
		}
		if got != want {
			t.Fatalf("retention = %d, want %d", got, want)
		}
	}
	// Guests get 403's service sentinel; non-members get 404's.
	if _, err := GetAutomationRunRetentionDays(ctx, fx.pool, fx.slug, fx.ident, fx.guest); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest read: err = %v, want ErrForbidden", err)
	}
	outsider := createTestUser(t, fx.pool, uniqueTestEmail("ret-out"))
	if _, err := GetAutomationRunRetentionDays(ctx, fx.pool, fx.slug, fx.ident, outsider); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-member read: err = %v, want ErrNotFound", err)
	}
	// Write guard: member (15)+, mirroring UpdateProject — guests 403.
	d := 30
	if _, err := UpdateProject(ctx, fx.pool, fx.slug, fx.ident, fx.member,
		ProjectPatch{AutomationRunRetentionDays: &d}); err != nil {
		t.Fatalf("member UpdateProject: %v", err)
	}
	if _, err := UpdateProject(ctx, fx.pool, fx.slug, fx.ident, fx.guest,
		ProjectPatch{AutomationRunRetentionDays: &d}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest write: err = %v, want ErrForbidden", err)
	}
}

func TestPruneAutomationRunsNeverBlocksFiring(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	ruleID, issueID := retentionRule(t, fx)

	// A sweep runs over prunable rows...
	insertAutomationRunAt(t, fx.pool, ruleID, issueID, now.Add(-100*24*time.Hour))
	if _, err := PruneAutomationRuns(ctx, fx.pool, now); err != nil {
		t.Fatalf("PruneAutomationRuns: %v", err)
	}
	// ...and a subsequent firing still writes its run row and commits
	// the state change. The sweep never shares a transaction with the
	// firing path (recordAutomationRunTx), so a prune failure can never
	// block a state change.
	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "post-sweep fire")
	moveIssueToState(t, fx, iss.ID, fx.admin, fx.started)
	got, err := GetIssue(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.StateID != fx.started {
		t.Fatalf("state = %s, want %s (firing must commit after a sweep)", got.StateID, fx.started)
	}
	runs, err := ListAutomationRuns(ctx, fx.pool, fx.slug, fx.ident, fx.admin, AutomationRunFilter{})
	if err != nil {
		t.Fatalf("ListAutomationRuns: %v", err)
	}
	if len(runs) != 1 || runs[0].IssueID != iss.ID {
		t.Fatalf("runs after firing = %d, want the 1 fresh run", len(runs))
	}
}

func TestPruneAutomationRunsFailureIsNeverFatal(t *testing.T) {
	// A sweep failure surfaces as a returned error — no panic, no
	// process death. The ticker logs the error and keeps ticking, so a
	// broken pass is never fatal to the server or to rule firing.
	fx := setupAutomationFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := PruneAutomationRuns(ctx, fx.pool, time.Now()); err == nil {
		t.Fatal("PruneAutomationRuns with cancelled context: expected error, got nil")
	}
}
