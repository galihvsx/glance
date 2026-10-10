package service

// C12T2 automation trigger/action expansion: issue.created trigger plus
// set_priority / set_state actions. TDD — these tests were written
// before the implementation.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// createdRuleInput builds a rule input with the issue.created trigger.
func createdRuleInput(name string, actions []AutomationAction) AutomationRuleInput {
	return AutomationRuleInput{
		Name:    name,
		Trigger: AutomationTrigger{Type: "issue.created"},
		Actions: actions,
	}
}

func autoIntPtr(i int) *int { return &i }

func automationRunRows(t *testing.T, fx automationFixture) []*AutomationRun {
	t.Helper()
	runs, err := ListAutomationRuns(context.Background(), fx.pool, fx.slug, fx.ident, fx.admin, AutomationRunFilter{})
	if err != nil {
		t.Fatalf("ListAutomationRuns: %v", err)
	}
	return runs
}

func issueActivityCount(t *testing.T, fx automationFixture, issueID, field string) int {
	t.Helper()
	return countRows(t, fx.pool,
		`SELECT count(*) FROM issue_activities WHERE issue_id = $1::uuid AND field = $2`,
		issueID, field)
}

func TestAutomationCreatedTriggerFiresOnCreate(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	in := createdRuleInput("welcome bot", []AutomationAction{
		{Type: "add_comment", Body: autoStrPtr("welcome!")},
	})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "created fires")

	if n := countRows(t, fx.pool,
		`SELECT count(*) FROM comments WHERE issue_id = $1::uuid AND deleted_at IS NULL`, iss.ID); n != 1 {
		t.Fatalf("comments = %d, want 1 (created trigger must fire on create)", n)
	}

	runs := automationRunRows(t, fx)
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want exactly 1", len(runs))
	}
	if runs[0].TriggerType != "issue.created" {
		t.Fatalf("run trigger_type = %q, want issue.created", runs[0].TriggerType)
	}
	results := automationRunActionResults(t, runs[0])
	if len(results) != 1 || results[0].Type != "add_comment" || !results[0].OK {
		t.Fatalf("results = %+v, want one ok add_comment", results)
	}
}

func TestAutomationCreatedTriggerSilentOnStateChange(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	in := createdRuleInput("welcome bot", []AutomationAction{
		{Type: "add_comment", Body: autoStrPtr("welcome!")},
	})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "created silent later")
	moveIssueToState(t, fx, iss.ID, fx.admin, fx.started)

	// Exactly one comment (from creation) and one run row: the created
	// trigger must not fire on a later state change.
	if n := countRows(t, fx.pool,
		`SELECT count(*) FROM comments WHERE issue_id = $1::uuid AND deleted_at IS NULL`, iss.ID); n != 1 {
		t.Fatalf("comments = %d, want 1 (created trigger fired on state change)", n)
	}
	if n := len(automationRunRows(t, fx)); n != 1 {
		t.Fatalf("runs = %d, want 1 (created trigger fired on state change)", n)
	}
}

func TestAutomationStateChangedRuleSilentOnCreate(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	in := automationRuleInput(fx, []string{fx.started}, []AutomationAction{
		{Type: "add_comment", Body: autoStrPtr("state changed!")},
	})
	in.Name = "on start"
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "state rule quiet on create")

	if n := countRows(t, fx.pool,
		`SELECT count(*) FROM comments WHERE issue_id = $1::uuid AND deleted_at IS NULL`, iss.ID); n != 0 {
		t.Fatalf("comments = %d, want 0 (state_changed rule fired on create)", n)
	}
	if n := len(automationRunRows(t, fx)); n != 0 {
		t.Fatalf("runs = %d, want 0 (state_changed rule fired on create)", n)
	}
}

func TestAutomationSetPriorityApplies(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	in := createdRuleInput("urgent triage", []AutomationAction{
		{Type: "set_priority", Priority: autoIntPtr(4)},
	})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "priority set")
	got, err := GetIssue(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.Priority != 4 {
		t.Fatalf("priority = %d, want 4 (set_priority did not apply)", got.Priority)
	}
	if n := issueActivityCount(t, fx, iss.ID, "priority"); n != 1 {
		t.Fatalf("priority activities = %d, want 1", n)
	}
	runs := automationRunRows(t, fx)
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
	results := automationRunActionResults(t, runs[0])
	if len(results) != 1 || results[0].Type != "set_priority" || !results[0].OK {
		t.Fatalf("results = %+v, want one ok set_priority", results)
	}
}

func TestAutomationSetStateTransitions(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	in := createdRuleInput("auto triage", []AutomationAction{
		{Type: "set_state", StateID: autoStrPtr(fx.started)},
	})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "state set")
	got, err := GetIssue(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.StateID != fx.started {
		t.Fatalf("state = %s, want %s (set_state did not apply)", got.StateID, fx.started)
	}
	if n := issueActivityCount(t, fx, iss.ID, "state_id"); n != 1 {
		t.Fatalf("state_id activities = %d, want 1", n)
	}
	runs := automationRunRows(t, fx)
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
	results := automationRunActionResults(t, runs[0])
	if len(results) != 1 || results[0].Type != "set_state" || !results[0].OK {
		t.Fatalf("results = %+v, want one ok set_state", results)
	}
}

func TestAutomationSetStateNoCascade(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	// Rule A: on creation, move the issue to started.
	inA := createdRuleInput("triage on create", []AutomationAction{
		{Type: "set_state", StateID: autoStrPtr(fx.started)},
	})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, inA); err != nil {
		t.Fatalf("rule A: %v", err)
	}
	// Rule B: on any move to started, comment. If automation-driven
	// changes re-triggered evaluation, rule A's set_state would fire B.
	inB := automationRuleInput(fx, []string{fx.started}, []AutomationAction{
		{Type: "add_comment", Body: autoStrPtr("B must never fire from automation")},
	})
	inB.Name = "comment on start"
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, inB); err != nil {
		t.Fatalf("rule B: %v", err)
	}

	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "no cascade")
	got, err := GetIssue(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.StateID != fx.started {
		t.Fatalf("state = %s, want %s (rule A set_state did not apply)", got.StateID, fx.started)
	}
	// Rule B's comment must not exist: no cascade.
	if n := countRows(t, fx.pool,
		`SELECT count(*) FROM comments WHERE issue_id = $1::uuid AND deleted_at IS NULL`, iss.ID); n != 0 {
		t.Fatalf("comments = %d, want 0 (automation set_state cascaded into rule B)", n)
	}
	// Exactly one run row: rule A's firing. Rule B never fired.
	runs := automationRunRows(t, fx)
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1 (only rule A fired)", len(runs))
	}
	if runs[0].TriggerType != "issue.created" {
		t.Fatalf("run trigger_type = %q, want issue.created", runs[0].TriggerType)
	}
}

func TestAutomationSetStateNoOpWritesNoRow(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	in := createdRuleInput("redundant move", []AutomationAction{
		{Type: "set_state", StateID: autoStrPtr(fx.started)},
	})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	// Create the issue already in started: the set_state is a no-op.
	iss, err := CreateIssue(ctx, fx.pool, fx.slug, fx.ident, fx.admin,
		CreateIssueInput{Name: "already started", StateID: &fx.started})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if n := issueActivityCount(t, fx, iss.ID, "state_id"); n != 0 {
		t.Fatalf("state_id activities = %d, want 0 (no-op set_state wrote activity)", n)
	}
	if n := len(automationRunRows(t, fx)); n != 0 {
		t.Fatalf("runs = %d, want 0 (no-op set_state wrote a run row)", n)
	}
}

func TestAutomationSetStateNoOpMixedStillRecordsRow(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	// The set_state is a no-op (issue lands in backlog by default) but
	// the comment is real work: the firing records a run row with both
	// per-action results.
	in := createdRuleInput("noop plus comment", []AutomationAction{
		{Type: "set_state", StateID: autoStrPtr(fx.backlog)},
		{Type: "add_comment", Body: autoStrPtr("hi")},
	})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "mixed noop")
	if n := issueActivityCount(t, fx, iss.ID, "state_id"); n != 0 {
		t.Fatalf("state_id activities = %d, want 0", n)
	}
	runs := automationRunRows(t, fx)
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1 (mixed firing must record its row)", len(runs))
	}
	results := automationRunActionResults(t, runs[0])
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	for _, res := range results {
		if !res.OK || res.Error != nil {
			t.Fatalf("result = %+v, want ok with no error", res)
		}
	}
}

func TestAutomationCreatedTriggerValidation(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	// A state from another project: set_state must reject it at write time.
	otherIdent := uniqueTestIdent()
	if _, err := CreateProject(ctx, fx.pool, fx.slug, fx.admin, "Other Project", otherIdent); err != nil {
		t.Fatalf("CreateProject other: %v", err)
	}
	otherStates, err := ListStates(ctx, fx.pool, fx.slug, otherIdent, fx.admin)
	if err != nil {
		t.Fatalf("ListStates other: %v", err)
	}
	var otherBacklog string
	for _, s := range otherStates {
		if s.Group == "backlog" {
			otherBacklog = s.ID
		}
	}
	if otherBacklog == "" {
		t.Fatal("other project has no backlog state")
	}

	cases := []struct {
		name string
		in   AutomationRuleInput
		want error
	}{
		{"unknown trigger type", createdRuleInput("x", []AutomationAction{{Type: "add_comment", Body: autoStrPtr("x")}}), ErrInvalidAutomationTrigger},
		{"created trigger rejects from_states", AutomationRuleInput{Name: "x",
			Trigger: AutomationTrigger{Type: "issue.created", FromStates: []string{fx.backlog}},
			Actions: []AutomationAction{{Type: "add_comment", Body: autoStrPtr("x")}}}, ErrInvalidAutomationTrigger},
		{"created trigger rejects to_states", AutomationRuleInput{Name: "x",
			Trigger: AutomationTrigger{Type: "issue.created", ToStates: []string{fx.started}},
			Actions: []AutomationAction{{Type: "add_comment", Body: autoStrPtr("x")}}}, ErrInvalidAutomationTrigger},
		{"set_priority out of range", createdRuleInput("x", []AutomationAction{{Type: "set_priority", Priority: autoIntPtr(9)}}), ErrInvalidAutomationAction},
		{"set_priority negative", createdRuleInput("x", []AutomationAction{{Type: "set_priority", Priority: autoIntPtr(-1)}}), ErrInvalidAutomationAction},
		{"set_priority missing", createdRuleInput("x", []AutomationAction{{Type: "set_priority"}}), ErrInvalidAutomationAction},
		{"set_state missing", createdRuleInput("x", []AutomationAction{{Type: "set_state"}}), ErrInvalidAutomationAction},
		{"set_state unknown uuid", createdRuleInput("x", []AutomationAction{{Type: "set_state", StateID: autoStrPtr("00000000-0000-0000-0000-000000000000")}}), ErrInvalidAutomationAction},
		{"set_state other project", createdRuleInput("x", []AutomationAction{{Type: "set_state", StateID: autoStrPtr(otherBacklog)}}), ErrInvalidAutomationAction},
	}
	cases[0].in.Trigger.Type = "issue.deleted"
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, tc.in); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestAutomationCreatedWriteAuthGuestForbidden(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	in := createdRuleInput("guest rule", []AutomationAction{
		{Type: "set_priority", Priority: autoIntPtr(2)},
	})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.guest, in); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest create err = %v, want ErrForbidden", err)
	}
}

func TestAutomationNewTriggerActionsJSONRoundTrip(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	in := createdRuleInput("round trip", []AutomationAction{
		{Type: "set_priority", Priority: autoIntPtr(3)},
		{Type: "set_state", StateID: autoStrPtr(fx.completed)},
		{Type: "assign", UserID: autoStrPtr(fx.member)},
	})
	created, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in)
	if err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	rules, err := ListAutomationRules(ctx, fx.pool, fx.slug, fx.ident, fx.admin)
	if err != nil {
		t.Fatalf("ListAutomationRules: %v", err)
	}
	var found *AutomationRule
	for _, r := range rules {
		if r.ID == created.ID {
			found = r
		}
	}
	if found == nil {
		t.Fatal("created rule not listed")
	}
	var trig AutomationTrigger
	if err := json.Unmarshal(found.Trigger, &trig); err != nil {
		t.Fatalf("trigger unmarshal: %v", err)
	}
	if trig.Type != "issue.created" || len(trig.FromStates) != 0 || len(trig.ToStates) != 0 {
		t.Fatalf("trigger = %+v, want bare issue.created", trig)
	}
	var actions []AutomationAction
	if err := json.Unmarshal(found.Actions, &actions); err != nil {
		t.Fatalf("actions unmarshal: %v", err)
	}
	if len(actions) != 3 {
		t.Fatalf("actions = %d, want 3", len(actions))
	}
	if actions[0].Type != "set_priority" || actions[0].Priority == nil || *actions[0].Priority != 3 {
		t.Fatalf("actions[0] = %+v, want set_priority 3", actions[0])
	}
	if actions[1].Type != "set_state" || actions[1].StateID == nil || *actions[1].StateID != fx.completed {
		t.Fatalf("actions[1] = %+v, want set_state %s", actions[1], fx.completed)
	}
	if actions[2].Type != "assign" || actions[2].UserID == nil || *actions[2].UserID != fx.member {
		t.Fatalf("actions[2] = %+v, want assign %s", actions[2], fx.member)
	}
}
