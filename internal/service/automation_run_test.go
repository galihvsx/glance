package service

// C12T1 automation run history: service tests. A run row is written for
// every rule firing, in the same tx as the state change, with
// per-action ok/error results.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// automationRunActionResults decodes a run's actions JSONB into the
// recorded per-action results.
func automationRunActionResults(t *testing.T, run *AutomationRun) []AutomationActionResult {
	t.Helper()
	var results []AutomationActionResult
	if err := json.Unmarshal(run.Actions, &results); err != nil {
		t.Fatalf("run actions unmarshal: %v", err)
	}
	return results
}

func TestAutomationRunWrittenOnFire(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	in := automationRuleInput(fx, []string{fx.started}, []AutomationAction{
		{Type: "assign", UserID: autoStrPtr(fx.member)},
		{Type: "add_comment", Body: autoStrPtr("auto run")},
	})
	in.Name = "run history"
	r, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in)
	if err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "auto run fire")
	moveIssueToState(t, fx, iss.ID, fx.admin, fx.started)

	runs, err := ListAutomationRuns(ctx, fx.pool, fx.slug, fx.ident, fx.admin, AutomationRunFilter{})
	if err != nil {
		t.Fatalf("ListAutomationRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want exactly 1", len(runs))
	}
	run := runs[0]
	if run.RuleID != r.ID || run.RuleName != "run history" {
		t.Fatalf("run rule = %s/%q, want %s/\"run history\"", run.RuleID, run.RuleName, r.ID)
	}
	if run.IssueID != iss.ID {
		t.Fatalf("run issue = %s, want %s", run.IssueID, iss.ID)
	}
	if run.TriggerType != "issue.state_changed" {
		t.Fatalf("run trigger_type = %q, want issue.state_changed", run.TriggerType)
	}
	if run.FiredAt.IsZero() {
		t.Fatal("run fired_at is zero")
	}
	// Display ID comes from the issue's sequence (derived, never stored).
	got, err := GetIssue(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if run.IssueDisplayID == nil || *run.IssueDisplayID != got.DisplayID {
		t.Fatalf("run issue_display_id = %v, want %s", run.IssueDisplayID, got.DisplayID)
	}
	results := automationRunActionResults(t, run)
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	for _, res := range results {
		if !res.OK || res.Error != nil {
			t.Fatalf("result = %+v, want ok with no error", res)
		}
	}
	if results[0].Type != "assign" || results[1].Type != "add_comment" {
		t.Fatalf("result types = %q/%q, want assign/add_comment",
			results[0].Type, results[1].Type)
	}
}

func TestAutomationRunFailedActionRecordedWithoutRollback(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	in := automationRuleInput(fx, []string{fx.started}, []AutomationAction{
		{Type: "add_comment", Body: autoStrPtr("first ok")},
		{Type: "assign", UserID: autoStrPtr(fx.member)},
	})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	// The assignee leaves between rule creation and the state change:
	// the assign action fails. The state change must still commit and
	// the run row must record ok:false + error text.
	if err := RemoveMember(ctx, fx.pool, fx.slug, fx.admin, fx.member); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "auto run fail")
	moveIssueToState(t, fx, iss.ID, fx.admin, fx.started)

	got, err := GetIssue(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.StateID != fx.started {
		t.Fatalf("state = %s, want %s (move must commit despite failed action)", got.StateID, fx.started)
	}

	runs, err := ListAutomationRuns(ctx, fx.pool, fx.slug, fx.ident, fx.admin, AutomationRunFilter{})
	if err != nil {
		t.Fatalf("ListAutomationRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1 (failed action still writes its run)", len(runs))
	}
	results := automationRunActionResults(t, runs[0])
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	if !results[0].OK || results[0].Error != nil {
		t.Fatalf("add_comment result = %+v, want ok", results[0])
	}
	if results[1].Type != "assign" || results[1].OK {
		t.Fatalf("assign result = %+v, want ok:false", results[1])
	}
	if results[1].Error == nil || *results[1].Error == "" {
		t.Fatalf("assign result error = %v, want non-empty error text", results[1].Error)
	}
}

func TestAutomationRunDisabledRuleWritesNoRow(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	in := automationRuleInput(fx, []string{fx.started}, []AutomationAction{
		{Type: "add_comment", Body: autoStrPtr("disabled run")},
	})
	r, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in)
	if err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	disabled := false
	if _, err := UpdateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, r.ID, fx.admin,
		AutomationRulePatch{Enabled: &disabled}); err != nil {
		t.Fatalf("disable: %v", err)
	}
	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "auto run disabled")
	moveIssueToState(t, fx, iss.ID, fx.admin, fx.started)

	runs, err := ListAutomationRuns(ctx, fx.pool, fx.slug, fx.ident, fx.admin, AutomationRunFilter{})
	if err != nil {
		t.Fatalf("ListAutomationRuns: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("runs = %d, want 0 (disabled rule must not write run rows)", len(runs))
	}
}

func TestAutomationRunNonMatchingTransitionWritesNoRow(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	in := automationRuleInput(fx, []string{fx.started}, []AutomationAction{
		{Type: "add_comment", Body: autoStrPtr("nope")},
	})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "auto run silent")
	moveIssueToState(t, fx, iss.ID, fx.admin, fx.completed)

	runs, err := ListAutomationRuns(ctx, fx.pool, fx.slug, fx.ident, fx.admin, AutomationRunFilter{})
	if err != nil {
		t.Fatalf("ListAutomationRuns: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("runs = %d, want 0 (rule that did not fire writes nothing)", len(runs))
	}
}

func TestAutomationRunListAuth(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	in := automationRuleInput(fx, nil, []AutomationAction{
		{Type: "add_comment", Body: autoStrPtr("auth run")},
	})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "auto run auth")
	moveIssueToState(t, fx, iss.ID, fx.admin, fx.started)

	// Member (15)+ reads fine.
	runs, err := ListAutomationRuns(ctx, fx.pool, fx.slug, fx.ident, fx.member, AutomationRunFilter{})
	if err != nil || len(runs) != 1 {
		t.Fatalf("member list = %d, err = %v; want 1", len(runs), err)
	}
	// Guest read: forbidden.
	if _, err := ListAutomationRuns(ctx, fx.pool, fx.slug, fx.ident, fx.guest, AutomationRunFilter{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest list err = %v, want ErrForbidden", err)
	}
	// Non-member: 404-shaped.
	outsider := createTestUser(t, fx.pool, uniqueTestEmail("auto-run-out"))
	if _, err := ListAutomationRuns(ctx, fx.pool, fx.slug, fx.ident, outsider, AutomationRunFilter{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider list err = %v, want ErrNotFound", err)
	}
}

func TestAutomationRunListFilters(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	mkRule := func(name string) string {
		in := automationRuleInput(fx, nil, []AutomationAction{
			{Type: "add_comment", Body: autoStrPtr(name)},
		})
		in.Name = name
		r, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in)
		if err != nil {
			t.Fatalf("CreateAutomationRule %s: %v", name, err)
		}
		return r.ID
	}
	ruleA := mkRule("rule A")
	ruleB := mkRule("rule B")

	issA := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "auto run filter a")
	issB := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "auto run filter b")
	moveIssueToState(t, fx, issA.ID, fx.admin, fx.started)
	moveIssueToState(t, fx, issB.ID, fx.admin, fx.completed)

	all, err := ListAutomationRuns(ctx, fx.pool, fx.slug, fx.ident, fx.admin, AutomationRunFilter{})
	if err != nil || len(all) != 4 {
		t.Fatalf("unfiltered = %d, err = %v; want 4 (2 rules x 2 moves)", len(all), err)
	}
	// Newest first: the later move (issB/completed) sorts before issA.
	if all[0].IssueID != issB.ID {
		t.Fatalf("newest run issue = %s, want %s (newest first)", all[0].IssueID, issB.ID)
	}

	byRule, err := ListAutomationRuns(ctx, fx.pool, fx.slug, fx.ident, fx.admin, AutomationRunFilter{RuleID: ruleA})
	if err != nil || len(byRule) != 2 {
		t.Fatalf("rule_id filter = %d, err = %v; want 2", len(byRule), err)
	}
	for _, r := range byRule {
		if r.RuleID != ruleA {
			t.Fatalf("filtered run rule = %s, want %s", r.RuleID, ruleA)
		}
	}
	_ = ruleB

	byIssue, err := ListAutomationRuns(ctx, fx.pool, fx.slug, fx.ident, fx.admin, AutomationRunFilter{IssueID: issA.ID})
	if err != nil || len(byIssue) != 2 {
		t.Fatalf("issue_id filter = %d, err = %v; want 2", len(byIssue), err)
	}
	for _, r := range byIssue {
		if r.IssueID != issA.ID {
			t.Fatalf("filtered run issue = %s, want %s", r.IssueID, issA.ID)
		}
	}

	both, err := ListAutomationRuns(ctx, fx.pool, fx.slug, fx.ident, fx.admin,
		AutomationRunFilter{RuleID: ruleA, IssueID: issB.ID})
	if err != nil || len(both) != 1 {
		t.Fatalf("rule+issue filter = %d, err = %v; want 1", len(both), err)
	}
}

func TestAutomationRunLimitValidation(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	in := automationRuleInput(fx, nil, []AutomationAction{
		{Type: "add_comment", Body: autoStrPtr("limit run")},
	})
	r, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in)
	if err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "auto run limit")
	// Seed 3 run rows directly (one per rule firing is 1:1, but the
	// clamp contract is about the read side, not the fire count).
	for i := 0; i < 3; i++ {
		if _, err := fx.pool.Exec(ctx,
			`INSERT INTO automation_runs (rule_id, issue_id, trigger_type, actions)
			 VALUES ($1::uuid, $2::uuid, 'issue.state_changed', '[]'::jsonb)`,
			r.ID, iss.ID); err != nil {
			t.Fatalf("seed run %d: %v", i, err)
		}
	}

	// limit=0 (explicit) and negative: 400-shaped.
	if _, err := ListAutomationRuns(ctx, fx.pool, fx.slug, fx.ident, fx.admin, AutomationRunFilter{Limit: 0 - 1}); !errors.Is(err, ErrInvalidAutomationRunLimit) {
		t.Fatalf("limit=-1 err = %v, want ErrInvalidAutomationRunLimit", err)
	}
	// limit above the max is clamped to 100, not rejected.
	runs, err := ListAutomationRuns(ctx, fx.pool, fx.slug, fx.ident, fx.admin, AutomationRunFilter{Limit: 500})
	if err != nil || len(runs) != 3 {
		t.Fatalf("limit=500 = %d, err = %v; want 3 (clamped, not rejected)", len(runs), err)
	}
	// limit=1 returns exactly one row, newest first.
	one, err := ListAutomationRuns(ctx, fx.pool, fx.slug, fx.ident, fx.admin, AutomationRunFilter{Limit: 1})
	if err != nil || len(one) != 1 {
		t.Fatalf("limit=1 = %d, err = %v; want 1", len(one), err)
	}
}

func TestAutomationRunDeleteRuleCascades(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	in := automationRuleInput(fx, nil, []AutomationAction{
		{Type: "add_comment", Body: autoStrPtr("cascade run")},
	})
	r, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in)
	if err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "auto run cascade")
	moveIssueToState(t, fx, iss.ID, fx.admin, fx.started)

	if n := countRows(t, fx.pool, `SELECT count(*) FROM automation_runs WHERE rule_id = $1::uuid`, r.ID); n != 1 {
		t.Fatalf("runs before delete = %d, want 1", n)
	}
	if err := DeleteAutomationRule(ctx, fx.pool, fx.slug, fx.ident, r.ID, fx.admin); err != nil {
		t.Fatalf("DeleteAutomationRule: %v", err)
	}
	if n := countRows(t, fx.pool, `SELECT count(*) FROM automation_runs WHERE rule_id = $1::uuid`, r.ID); n != 0 {
		t.Fatalf("runs after delete = %d, want 0 (ON DELETE CASCADE)", n)
	}
	// The list endpoint still works on an empty history.
	runs, err := ListAutomationRuns(ctx, fx.pool, fx.slug, fx.ident, fx.admin, AutomationRunFilter{})
	if err != nil || len(runs) != 0 {
		t.Fatalf("list after cascade = %d, err = %v; want 0", len(runs), err)
	}
}
