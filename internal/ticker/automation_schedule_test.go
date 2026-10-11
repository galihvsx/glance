package ticker

// Automation-schedule ticker tests (C16T1): RunOnce evaluates
// scheduled-trigger rules on the test DB — a due_soon rule fires, the
// same-day second tick is deduped, and a pass with no matching rules
// is a no-op. The deep match-logic coverage lives in
// internal/service/automation_schedule_c16t1_test.go.

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"glance/internal/service"
)

func TestAutomationScheduleTickerRunsPass(t *testing.T) {
	ctx, pool, slug, ident, actor, _ := tickerTestSetup(t, "sched-tick")

	in := service.AutomationRuleInput{
		Name:    "due soon bot",
		Trigger: service.AutomationTrigger{Type: "issue.due_soon"},
		Actions: []service.AutomationAction{{Type: "add_comment", Body: strPtr("due soon!")}},
	}
	rule, err := service.CreateAutomationRule(ctx, pool, slug, ident, actor, in)
	if err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	due := time.Now().Add(24 * time.Hour)
	iss, err := service.CreateIssue(ctx, pool, slug, ident, actor,
		service.CreateIssueInput{Name: "due tomorrow", TargetDate: &due})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	tr := &AutomationScheduleTicker{Pool: pool, Interval: time.Minute}
	if err := tr.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if n := automationRunRowCount(t, ctx, pool, rule.ID); n != 1 {
		t.Fatalf("run rows after tick 1 = %d, want 1", n)
	}
	if n := automationCommentCount(t, ctx, pool, iss.ID); n != 1 {
		t.Fatalf("comments after tick 1 = %d, want 1", n)
	}

	// The dedupe window is enforced at the ticker level too: the next
	// tick the same day must not fire again.
	if err := tr.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce (tick 2): %v", err)
	}
	if n := automationRunRowCount(t, ctx, pool, rule.ID); n != 1 {
		t.Fatalf("run rows after tick 2 = %d, want 1 (dedupe)", n)
	}
}

func TestAutomationScheduleTickerNoRulesNoOp(t *testing.T) {
	ctx, pool, _, _, _, projID := tickerTestSetup(t, "sched-noop")
	tr := &AutomationScheduleTicker{Pool: pool, Interval: time.Minute}
	if err := tr.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce with no rules: %v", err)
	}
	if n := automationProjectRunCount(t, ctx, pool, projID); n != 0 {
		t.Fatalf("run rows = %d, want 0", n)
	}
}

func strPtr(s string) *string { return &s }

// automationRunRowCount counts run rows for one rule: automation_runs
// is a global table shared by every test (and every concurrently-run
// package on the same test DB), so a global count would pick up rows
// from unrelated rules.
func automationRunRowCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, ruleID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM automation_runs WHERE rule_id = $1::uuid`, ruleID).Scan(&n); err != nil {
		t.Fatalf("count automation_runs: %v", err)
	}
	return n
}

// automationProjectRunCount counts run rows for every rule of one
// project — the no-op test creates no rules, so a pass must record
// nothing at all in its project regardless of what other projects'
// rules fired.
func automationProjectRunCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM automation_runs r
		 JOIN automation_rules ar ON ar.id = r.rule_id
		 WHERE ar.project_id = $1::uuid`, projID).Scan(&n); err != nil {
		t.Fatalf("count project automation_runs: %v", err)
	}
	return n
}

func automationCommentCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, issueID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM comments WHERE issue_id = $1::uuid AND deleted_at IS NULL`,
		issueID).Scan(&n); err != nil {
		t.Fatalf("count comments: %v", err)
	}
	return n
}
