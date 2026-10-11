package service

// C16T1 scheduled automation triggers: issue.due_soon (default and
// custom window), issue.overdue, issue.stale (default and custom
// threshold), cycle.ending_soon — match logic on the test DB, the
// per-(rule, issue)-per-day dedupe window, disabled rules, and
// trigger validation (honest 400s on foreign or out-of-range
// filters). The scheduled pass is exercised through
// RunScheduledAutomationPass; the ticker-level tests live in
// internal/ticker/automation_schedule_test.go.

import (
	"context"
	"errors"
	"testing"
	"time"
)

// schedRuleInput builds a rule input for one of the C16T1 scheduled
// trigger types with a single add_comment action.
func schedRuleInput(name string, trig AutomationTrigger) AutomationRuleInput {
	return AutomationRuleInput{
		Name:    name,
		Trigger: trig,
		Actions: []AutomationAction{{Type: "add_comment", Body: autoStrPtr("scheduled nudge")}},
	}
}

// createTargetIssue creates an issue with the given target date (nil =
// none) in the fixture project's default (backlog) state.
func createTargetIssue(t *testing.T, fx automationFixture, name string, target *time.Time) *Issue {
	t.Helper()
	iss, err := CreateIssue(context.Background(), fx.pool, fx.slug, fx.ident, fx.admin,
		CreateIssueInput{Name: name, TargetDate: target})
	if err != nil {
		t.Fatalf("CreateIssue %s: %v", name, err)
	}
	return iss
}

func timePtr(d time.Duration) *time.Time {
	ts := time.Now().Add(d)
	return &ts
}

// backdateIssueUpdated moves an issue's updated_at into the past — the
// service never ages issues by hand, so staleness is simulated with a
// direct UPDATE.
func backdateIssueUpdated(t *testing.T, fx automationFixture, issueID string, d time.Duration) {
	t.Helper()
	if _, err := fx.pool.Exec(context.Background(),
		`UPDATE issues SET updated_at = now() - ($1::interval) WHERE id = $2::uuid`,
		d.String(), issueID); err != nil {
		t.Fatalf("backdate updated_at: %v", err)
	}
}

// completeIssue moves an issue into the fixture's completed state.
func completeIssue(t *testing.T, fx automationFixture, issueID string) {
	t.Helper()
	if _, err := UpdateIssue(context.Background(), fx.pool, fx.slug, fx.ident, issueID, fx.admin,
		IssuePatch{StateID: &fx.completed}); err != nil {
		t.Fatalf("complete issue: %v", err)
	}
}

func scheduledRunCountForRule(t *testing.T, fx automationFixture, ruleID string) int {
	t.Helper()
	// Rule-scoped, never global: automation_runs is a global table
	// shared by every test (and every concurrently-run package on the
	// same test DB), so a global count would pick up rows from
	// unrelated rules.
	return countRows(t, fx.pool, `SELECT count(*) FROM automation_runs WHERE rule_id = $1::uuid`, ruleID)
}

func TestScheduledTriggerValidation(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	valid := []AutomationTrigger{
		{Type: "issue.due_soon"},
		{Type: "issue.due_soon", WindowHours: autoIntPtr(24)},
		{Type: "issue.overdue"},
		{Type: "issue.stale"},
		{Type: "issue.stale", StaleDays: autoIntPtr(3)},
		{Type: "cycle.ending_soon"},
	}
	for i, trig := range valid {
		in := schedRuleInput("valid", trig)
		if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
			t.Fatalf("valid trigger %d (%s): unexpected error: %v", i, trig.Type, err)
		}
	}

	invalid := []AutomationTrigger{
		{Type: "issue.due_soon", WindowHours: autoIntPtr(0)},
		{Type: "issue.due_soon", WindowHours: autoIntPtr(-5)},
		{Type: "issue.due_soon", FromStates: []string{fx.backlog}},
		{Type: "issue.due_soon", StaleDays: autoIntPtr(3)}, // foreign window field
		{Type: "issue.stale", StaleDays: autoIntPtr(0)},
		{Type: "issue.stale", StaleDays: autoIntPtr(-2)},
		{Type: "issue.stale", WindowHours: autoIntPtr(24)}, // foreign window field
		{Type: "issue.stale", LabelIDs: []string{fx.labelID}},
		{Type: "issue.overdue", WindowHours: autoIntPtr(24)},
		{Type: "cycle.ending_soon", StaleDays: autoIntPtr(3)},
		{Type: "issue.nonexistent"},
	}
	for i, trig := range invalid {
		in := schedRuleInput("invalid", trig)
		_, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in)
		if !errors.Is(err, ErrInvalidAutomationTrigger) {
			t.Fatalf("invalid trigger %d (%s): err = %v, want ErrInvalidAutomationTrigger", i, trig.Type, err)
		}
	}

	// The new window fields are also foreign on the event triggers:
	// rejected rather than silently ignored.
	for _, typ := range []string{"issue.state_changed", "issue.created", "issue.priority_changed"} {
		trig := AutomationTrigger{Type: typ, WindowHours: autoIntPtr(24)}
		if typ == "issue.state_changed" {
			trig.ToStates = []string{fx.started}
		}
		_, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, schedRuleInput("foreign", trig))
		if !errors.Is(err, ErrInvalidAutomationTrigger) {
			t.Fatalf("%s with window_hours: err = %v, want ErrInvalidAutomationTrigger", typ, err)
		}
	}
}

func TestScheduledDueSoonFires(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	in := schedRuleInput("due soon bot", AutomationTrigger{Type: "issue.due_soon"})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	dueTomorrow := createTargetIssue(t, fx, "due tomorrow", timePtr(24*time.Hour))
	farFuture := createTargetIssue(t, fx, "due far", timePtr(5*24*time.Hour))
	alreadyOver := createTargetIssue(t, fx, "already over", timePtr(-24*time.Hour))
	noTarget := createTargetIssue(t, fx, "no target", nil)
	doneSoon := createTargetIssue(t, fx, "done but due soon", timePtr(24*time.Hour))
	completeIssue(t, fx, doneSoon.ID)

	if err := RunScheduledAutomationPass(ctx, fx.pool, time.Now()); err != nil {
		t.Fatalf("RunScheduledAutomationPass: %v", err)
	}

	if n := commentCount(t, fx, dueTomorrow.ID); n != 1 {
		t.Errorf("due-tomorrow issue comments = %d, want 1 (due_soon must fire)", n)
	}
	for _, iss := range []*Issue{farFuture, alreadyOver, noTarget, doneSoon} {
		if n := commentCount(t, fx, iss.ID); n != 0 {
			t.Errorf("issue %q comments = %d, want 0 (due_soon must not fire)", iss.Name, n)
		}
	}
	rows := automationRunRows(t, fx)
	if len(rows) != 1 {
		t.Fatalf("run rows = %d, want 1", len(rows))
	}
	if rows[0].TriggerType != "issue.due_soon" || rows[0].IssueID != dueTomorrow.ID {
		t.Fatalf("run row = (%s, %s), want (issue.due_soon, %s)", rows[0].TriggerType, rows[0].IssueID, dueTomorrow.ID)
	}
}

func TestScheduledDueSoonCustomWindow(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	in := schedRuleInput("narrow window", AutomationTrigger{Type: "issue.due_soon", WindowHours: autoIntPtr(24)})
	rule, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in)
	if err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	inWindow := createTargetIssue(t, fx, "due in 12h", timePtr(12*time.Hour))
	outWindow := createTargetIssue(t, fx, "due in 36h", timePtr(36*time.Hour))

	if err := RunScheduledAutomationPass(ctx, fx.pool, time.Now()); err != nil {
		t.Fatalf("RunScheduledAutomationPass: %v", err)
	}
	if n := commentCount(t, fx, inWindow.ID); n != 1 {
		t.Errorf("in-window issue comments = %d, want 1", n)
	}
	if n := commentCount(t, fx, outWindow.ID); n != 0 {
		t.Errorf("out-of-window issue comments = %d, want 0", n)
	}
	if n := scheduledRunCountForRule(t, fx, rule.ID); n != 1 {
		t.Errorf("run rows = %d, want 1", n)
	}
}

func TestScheduledOverdueFires(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	in := schedRuleInput("overdue bot", AutomationTrigger{Type: "issue.overdue"})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	over := createTargetIssue(t, fx, "overdue", timePtr(-24*time.Hour))
	notYet := createTargetIssue(t, fx, "not yet", timePtr(24*time.Hour))
	doneOver := createTargetIssue(t, fx, "done but overdue", timePtr(-24*time.Hour))
	completeIssue(t, fx, doneOver.ID)

	if err := RunScheduledAutomationPass(ctx, fx.pool, time.Now()); err != nil {
		t.Fatalf("RunScheduledAutomationPass: %v", err)
	}
	if n := commentCount(t, fx, over.ID); n != 1 {
		t.Errorf("overdue issue comments = %d, want 1", n)
	}
	for _, iss := range []*Issue{notYet, doneOver} {
		if n := commentCount(t, fx, iss.ID); n != 0 {
			t.Errorf("issue %q comments = %d, want 0", iss.Name, n)
		}
	}
	rows := automationRunRows(t, fx)
	if len(rows) != 1 || rows[0].TriggerType != "issue.overdue" {
		t.Fatalf("run rows = %v, want one issue.overdue row", rows)
	}
}

func TestScheduledStaleFires(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	in := schedRuleInput("stale bot", AutomationTrigger{Type: "issue.stale"})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	stale := createTargetIssue(t, fx, "stale", nil)
	fresh := createTargetIssue(t, fx, "fresh", nil)
	doneStale := createTargetIssue(t, fx, "done and stale", nil)
	backdateIssueUpdated(t, fx, stale.ID, 8*24*time.Hour)
	backdateIssueUpdated(t, fx, doneStale.ID, 8*24*time.Hour)
	completeIssue(t, fx, doneStale.ID)

	if err := RunScheduledAutomationPass(ctx, fx.pool, time.Now()); err != nil {
		t.Fatalf("RunScheduledAutomationPass: %v", err)
	}
	if n := commentCount(t, fx, stale.ID); n != 1 {
		t.Errorf("stale issue comments = %d, want 1", n)
	}
	for _, iss := range []*Issue{fresh, doneStale} {
		if n := commentCount(t, fx, iss.ID); n != 0 {
			t.Errorf("issue %q comments = %d, want 0", iss.Name, n)
		}
	}
	rows := automationRunRows(t, fx)
	if len(rows) != 1 || rows[0].TriggerType != "issue.stale" {
		t.Fatalf("run rows = %v, want one issue.stale row", rows)
	}
}

func TestScheduledStaleCustomDays(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	in := schedRuleInput("quick stale", AutomationTrigger{Type: "issue.stale", StaleDays: autoIntPtr(3)})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	stale := createTargetIssue(t, fx, "stale 4d", nil)
	fresh := createTargetIssue(t, fx, "stale 2d", nil)
	backdateIssueUpdated(t, fx, stale.ID, 4*24*time.Hour)
	backdateIssueUpdated(t, fx, fresh.ID, 2*24*time.Hour)

	if err := RunScheduledAutomationPass(ctx, fx.pool, time.Now()); err != nil {
		t.Fatalf("RunScheduledAutomationPass: %v", err)
	}
	if n := commentCount(t, fx, stale.ID); n != 1 {
		t.Errorf("4-day-stale issue comments = %d, want 1", n)
	}
	if n := commentCount(t, fx, fresh.ID); n != 0 {
		t.Errorf("2-day issue comments = %d, want 0", n)
	}
}

func TestScheduledCycleEndingSoonFires(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	in := schedRuleInput("cycle ending bot", AutomationTrigger{Type: "cycle.ending_soon"})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	now := time.Now()
	endingSoon, err := CreateCycle(ctx, fx.pool, fx.slug, fx.ident, fx.admin,
		CycleInput{Name: "ending soon", StartDate: now.AddDate(0, 0, -7), EndDate: now.AddDate(0, 0, 2)})
	if err != nil {
		t.Fatalf("CreateCycle ending soon: %v", err)
	}
	farAway, err := CreateCycle(ctx, fx.pool, fx.slug, fx.ident, fx.admin,
		CycleInput{Name: "far away", StartDate: now.AddDate(0, 0, -7), EndDate: now.AddDate(0, 0, 10)})
	if err != nil {
		t.Fatalf("CreateCycle far away: %v", err)
	}

	inCycle := createTargetIssue(t, fx, "in ending cycle", nil)
	outCycle := createTargetIssue(t, fx, "in far cycle", nil)
	doneInCycle := createTargetIssue(t, fx, "done in ending cycle", nil)
	if err := AddCycleIssues(ctx, fx.pool, fx.slug, fx.ident, fx.admin, endingSoon.ID, []string{inCycle.ID, doneInCycle.ID}); err != nil {
		t.Fatalf("AddCycleIssues ending soon: %v", err)
	}
	if err := AddCycleIssues(ctx, fx.pool, fx.slug, fx.ident, fx.admin, farAway.ID, []string{outCycle.ID}); err != nil {
		t.Fatalf("AddCycleIssues far away: %v", err)
	}
	completeIssue(t, fx, doneInCycle.ID)

	if err := RunScheduledAutomationPass(ctx, fx.pool, now); err != nil {
		t.Fatalf("RunScheduledAutomationPass: %v", err)
	}
	if n := commentCount(t, fx, inCycle.ID); n != 1 {
		t.Errorf("issue in ending-soon cycle comments = %d, want 1", n)
	}
	for _, iss := range []*Issue{outCycle, doneInCycle} {
		if n := commentCount(t, fx, iss.ID); n != 0 {
			t.Errorf("issue %q comments = %d, want 0", iss.Name, n)
		}
	}
	rows := automationRunRows(t, fx)
	if len(rows) != 1 || rows[0].TriggerType != "cycle.ending_soon" {
		t.Fatalf("run rows = %v, want one cycle.ending_soon row", rows)
	}
}

func TestScheduledDedupeWindow(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	in := schedRuleInput("due soon bot", AutomationTrigger{Type: "issue.due_soon"})
	rule, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in)
	if err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	iss := createTargetIssue(t, fx, "due tomorrow", timePtr(24*time.Hour))
	now := time.Now()

	// First pass fires.
	if err := RunScheduledAutomationPass(ctx, fx.pool, now); err != nil {
		t.Fatalf("pass 1: %v", err)
	}
	if n := scheduledRunCountForRule(t, fx, rule.ID); n != 1 {
		t.Fatalf("pass 1 run rows = %d, want 1", n)
	}

	// Second pass the same day must not fire again (the whole point of
	// the dedupe window: a stale rule must not comment every 5 min).
	if err := RunScheduledAutomationPass(ctx, fx.pool, now.Add(5*time.Minute)); err != nil {
		t.Fatalf("pass 2: %v", err)
	}
	if n := scheduledRunCountForRule(t, fx, rule.ID); n != 1 {
		t.Fatalf("pass 2 run rows = %d, want 1 (dedupe window)", n)
	}
	if n := commentCount(t, fx, iss.ID); n != 1 {
		t.Fatalf("comments = %d, want 1 (no double comment)", n)
	}

	// The window is one calendar day: a run row from yesterday no longer
	// dedupes — the rule fires again today.
	if _, err := fx.pool.Exec(ctx,
		`UPDATE automation_runs SET fired_at = fired_at - interval '2 days'
		 WHERE rule_id = $1::uuid AND issue_id = $2::uuid`,
		rule.ID, iss.ID); err != nil {
		t.Fatalf("backdate run row: %v", err)
	}
	if err := RunScheduledAutomationPass(ctx, fx.pool, now.Add(24*time.Hour)); err != nil {
		t.Fatalf("pass 3: %v", err)
	}
	if n := scheduledRunCountForRule(t, fx, rule.ID); n != 2 {
		t.Fatalf("pass 3 run rows = %d, want 2 (window expired)", n)
	}
}

func TestScheduledDisabledRuleSilent(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	in := schedRuleInput("disabled bot", AutomationTrigger{Type: "issue.overdue"})
	rule, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in)
	if err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	disabled := false
	if _, err := UpdateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, rule.ID, fx.admin,
		AutomationRulePatch{Enabled: &disabled}); err != nil {
		t.Fatalf("disable rule: %v", err)
	}
	iss := createTargetIssue(t, fx, "overdue", timePtr(-24*time.Hour))

	if err := RunScheduledAutomationPass(ctx, fx.pool, time.Now()); err != nil {
		t.Fatalf("RunScheduledAutomationPass: %v", err)
	}
	if n := scheduledRunCountForRule(t, fx, rule.ID); n != 0 {
		t.Errorf("run rows = %d, want 0 (disabled rule must not fire)", n)
	}
	if n := commentCount(t, fx, iss.ID); n != 0 {
		t.Errorf("comments = %d, want 0", n)
	}
}

func TestScheduledNoMatchRecordsNothing(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	in := schedRuleInput("overdue bot", AutomationTrigger{Type: "issue.overdue"})
	rule, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in)
	if err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	createTargetIssue(t, fx, "not overdue", timePtr(24*time.Hour))

	if err := RunScheduledAutomationPass(ctx, fx.pool, time.Now()); err != nil {
		t.Fatalf("RunScheduledAutomationPass: %v", err)
	}
	if n := scheduledRunCountForRule(t, fx, rule.ID); n != 0 {
		t.Errorf("run rows = %d, want 0 (no match = no record)", n)
	}
}
