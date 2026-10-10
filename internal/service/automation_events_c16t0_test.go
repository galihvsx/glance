package service

// C16T0 automation event triggers: issue.assigned / issue.unassigned,
// issue.labels_changed, issue.priority_changed, issue.due_date_changed,
// issue.estimate_changed, issue.comment_added. Each test asserts the
// three properties the task demands: the rule fires on a matching
// mutation, stays silent on a non-matching one, and never fires for a
// rolled-back write.

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

// eventRuleInput builds a rule input for one of the C16T0 trigger types.
func eventRuleInput(name string, trig AutomationTrigger, actions []AutomationAction) AutomationRuleInput {
	return AutomationRuleInput{Name: name, Trigger: trig, Actions: actions}
}

func commentCount(t *testing.T, fx automationFixture, issueID string) int {
	t.Helper()
	return countRows(t, fx.pool,
		`SELECT count(*) FROM comments WHERE issue_id = $1::uuid AND deleted_at IS NULL`,
		issueID)
}

func runTriggerTypes(t *testing.T, fx automationFixture) []string {
	t.Helper()
	var out []string
	for _, r := range automationRunRows(t, fx) {
		out = append(out, r.TriggerType)
	}
	return out
}

// runTriggerTypeSet returns the fired trigger types as a set: run rows
// list newest first, so multi-event tests compare multisets.
func runTriggerTypeSet(t *testing.T, fx automationFixture) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, typ := range runTriggerTypes(t, fx) {
		out[typ]++
	}
	return out
}

func TestAutomationAssignedTriggerFires(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	in := eventRuleInput("assign bot",
		AutomationTrigger{Type: "issue.assigned"},
		[]AutomationAction{{Type: "add_comment", Body: autoStrPtr("assigned!")}})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "assign fires")
	if err := AssignAssignee(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.member, fx.admin); err != nil {
		t.Fatalf("AssignAssignee: %v", err)
	}

	if n := commentCount(t, fx, iss.ID); n != 1 {
		t.Fatalf("comments = %d, want 1 (assigned trigger must fire)", n)
	}
	types := runTriggerTypes(t, fx)
	if len(types) != 1 || types[0] != "issue.assigned" {
		t.Fatalf("run trigger types = %v, want [issue.assigned]", types)
	}
}

func TestAutomationAssignedTriggerSilentOnUnrelatedChange(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	in := eventRuleInput("assign bot",
		AutomationTrigger{Type: "issue.assigned"},
		[]AutomationAction{{Type: "add_comment", Body: autoStrPtr("assigned!")}})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "assign silent")
	if _, err := UpdateIssue(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin,
		IssuePatch{Priority: autoIntPtr(3)}); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	if n := commentCount(t, fx, iss.ID); n != 0 {
		t.Fatalf("comments = %d, want 0 (priority change must not fire issue.assigned)", n)
	}
	if runs := automationRunRows(t, fx); len(runs) != 0 {
		t.Fatalf("runs = %d, want 0", len(runs))
	}
}

func TestAutomationAssignedTriggerIdempotentRefire(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	in := eventRuleInput("assign bot",
		AutomationTrigger{Type: "issue.assigned"},
		[]AutomationAction{{Type: "add_comment", Body: autoStrPtr("assigned!")}})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "assign idempotent")
	for i := 0; i < 2; i++ {
		if err := AssignAssignee(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.member, fx.admin); err != nil {
			t.Fatalf("AssignAssignee %d: %v", i, err)
		}
	}
	// The second assign is idempotent (0 rows): only one firing.
	if runs := automationRunRows(t, fx); len(runs) != 1 {
		t.Fatalf("runs = %d, want 1 (idempotent re-assign must not re-fire)", len(runs))
	}
}

func TestAutomationUnassignedTriggerFires(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "unassign fires")
	if err := AssignAssignee(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.member, fx.admin); err != nil {
		t.Fatalf("AssignAssignee: %v", err)
	}

	in := eventRuleInput("unassign bot",
		AutomationTrigger{Type: "issue.unassigned"},
		[]AutomationAction{{Type: "add_comment", Body: autoStrPtr("unassigned!")}})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	if err := UnassignAssignee(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.member, fx.admin); err != nil {
		t.Fatalf("UnassignAssignee: %v", err)
	}
	if n := commentCount(t, fx, iss.ID); n != 1 {
		t.Fatalf("comments = %d, want 1 (unassigned trigger must fire)", n)
	}
	types := runTriggerTypes(t, fx)
	if len(types) != 1 || types[0] != "issue.unassigned" {
		t.Fatalf("run trigger types = %v, want [issue.unassigned]", types)
	}
}

func TestAutomationLabelsChangedTriggerFilter(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	labelB, err := CreateLabel(ctx, fx.pool, fx.slug, fx.ident, fx.admin,
		LabelInput{Name: "label-b", Color: autoStrPtr("#ef4444")})
	if err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}

	in := eventRuleInput("label bot",
		AutomationTrigger{Type: "issue.labels_changed", LabelIDs: []string{labelB.ID}},
		[]AutomationAction{{Type: "add_comment", Body: autoStrPtr("label!")}})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "labels filter")
	// An unlisted label must not fire.
	if err := AssignLabel(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.labelID, fx.admin); err != nil {
		t.Fatalf("AssignLabel A: %v", err)
	}
	if runs := automationRunRows(t, fx); len(runs) != 0 {
		t.Fatalf("runs = %d, want 0 (unlisted label must not fire)", len(runs))
	}
	// A listed label fires on attach...
	if err := AssignLabel(ctx, fx.pool, fx.slug, fx.ident, iss.ID, labelB.ID, fx.admin); err != nil {
		t.Fatalf("AssignLabel B: %v", err)
	}
	if runs := automationRunRows(t, fx); len(runs) != 1 {
		t.Fatalf("runs = %d, want 1 (listed label attach must fire)", len(runs))
	}
	// ...and on detach (removal counts).
	if err := UnassignLabel(ctx, fx.pool, fx.slug, fx.ident, iss.ID, labelB.ID, fx.admin); err != nil {
		t.Fatalf("UnassignLabel B: %v", err)
	}
	if runs := automationRunRows(t, fx); len(runs) != 2 {
		t.Fatalf("runs = %d, want 2 (listed label detach must fire)", len(runs))
	}
	for _, typ := range runTriggerTypes(t, fx) {
		if typ != "issue.labels_changed" {
			t.Fatalf("run trigger type = %q, want issue.labels_changed", typ)
		}
	}
}

func TestAutomationLabelsChangedNoFilterMatchesAny(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	in := eventRuleInput("label bot",
		AutomationTrigger{Type: "issue.labels_changed"},
		[]AutomationAction{{Type: "add_comment", Body: autoStrPtr("label!")}})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "labels any")
	if err := AssignLabel(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.labelID, fx.admin); err != nil {
		t.Fatalf("AssignLabel: %v", err)
	}
	if err := UnassignLabel(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.labelID, fx.admin); err != nil {
		t.Fatalf("UnassignLabel: %v", err)
	}
	if runs := automationRunRows(t, fx); len(runs) != 2 {
		t.Fatalf("runs = %d, want 2 (empty filter matches any label change)", len(runs))
	}
}

func TestAutomationPriorityChangedTriggerFilters(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	toRule := eventRuleInput("pri-to bot",
		AutomationTrigger{Type: "issue.priority_changed", ToPriorities: []int{2}},
		[]AutomationAction{{Type: "add_comment", Body: autoStrPtr("to 2!")}})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, toRule); err != nil {
		t.Fatalf("CreateAutomationRule to: %v", err)
	}
	fromRule := eventRuleInput("pri-from bot",
		AutomationTrigger{Type: "issue.priority_changed", FromPriorities: []int{0}},
		[]AutomationAction{{Type: "add_comment", Body: autoStrPtr("from 0!")}})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, fromRule); err != nil {
		t.Fatalf("CreateAutomationRule from: %v", err)
	}

	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "priority filters")
	if iss.Priority != 0 {
		t.Fatalf("fresh issue priority = %d, want 0", iss.Priority)
	}
	// 0 -> 3: only the from_priorities=[0] rule fires.
	if _, err := UpdateIssue(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin,
		IssuePatch{Priority: autoIntPtr(3)}); err != nil {
		t.Fatalf("UpdateIssue 3: %v", err)
	}
	if runs := automationRunRows(t, fx); len(runs) != 1 {
		t.Fatalf("runs = %d, want 1 (only the from filter matches 0->3)", len(runs))
	}
	// 3 -> 2: only the to_priorities=[2] rule fires.
	if _, err := UpdateIssue(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin,
		IssuePatch{Priority: autoIntPtr(2)}); err != nil {
		t.Fatalf("UpdateIssue 2: %v", err)
	}
	runs := automationRunRows(t, fx)
	if len(runs) != 2 {
		t.Fatalf("runs = %d, want 2 (to filter matches 3->2)", len(runs))
	}
	for _, r := range runs {
		if r.TriggerType != "issue.priority_changed" {
			t.Fatalf("run trigger type = %q, want issue.priority_changed", r.TriggerType)
		}
	}
	// No-op patch (same priority) fires nothing.
	if _, err := UpdateIssue(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin,
		IssuePatch{Priority: autoIntPtr(2), Name: autoStrPtr("priority filters")}); err != nil {
		t.Fatalf("UpdateIssue no-op: %v", err)
	}
	if runs := automationRunRows(t, fx); len(runs) != 2 {
		t.Fatalf("runs = %d, want 2 (unchanged priority must not fire)", len(runs))
	}
}

func TestAutomationDueDateChangedTriggerFires(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	in := eventRuleInput("due bot",
		AutomationTrigger{Type: "issue.due_date_changed"},
		[]AutomationAction{{Type: "add_comment", Body: autoStrPtr("due!")}})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "due fires")
	// An unrelated field change stays silent.
	if _, err := UpdateIssue(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin,
		IssuePatch{Priority: autoIntPtr(1)}); err != nil {
		t.Fatalf("UpdateIssue priority: %v", err)
	}
	if runs := automationRunRows(t, fx); len(runs) != 0 {
		t.Fatalf("runs = %d, want 0 (priority change must not fire due_date)", len(runs))
	}
	// Setting the target date (the due date) fires.
	due := time.Date(2026, 12, 25, 0, 0, 0, 0, time.UTC)
	if _, err := UpdateIssue(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin,
		IssuePatch{TargetDate: PatchField[time.Time]{Set: true, Value: &due}}); err != nil {
		t.Fatalf("UpdateIssue due: %v", err)
	}
	types := runTriggerTypes(t, fx)
	if len(types) != 1 || types[0] != "issue.due_date_changed" {
		t.Fatalf("run trigger types = %v, want [issue.due_date_changed]", types)
	}
	// Clearing it fires again.
	if _, err := UpdateIssue(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin,
		IssuePatch{TargetDate: PatchField[time.Time]{Set: true}}); err != nil {
		t.Fatalf("UpdateIssue clear due: %v", err)
	}
	if runs := automationRunRows(t, fx); len(runs) != 2 {
		t.Fatalf("runs = %d, want 2 (clearing the due date fires too)", len(runs))
	}
}

func TestAutomationEstimateChangedTriggerFires(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	est, err := CreateEstimate(ctx, fx.pool, fx.slug, fx.ident, fx.admin, EstimateInput{
		Name:   "Fibonacci",
		Points: []EstimatePointInput{{Key: "1", Value: 1}, {Key: "2", Value: 2}},
	})
	if err != nil {
		t.Fatalf("CreateEstimate: %v", err)
	}

	in := eventRuleInput("estimate bot",
		AutomationTrigger{Type: "issue.estimate_changed"},
		[]AutomationAction{{Type: "add_comment", Body: autoStrPtr("estimated!")}})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "estimate fires")
	pid := est.Points[0].ID
	if _, err := UpdateIssue(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin,
		IssuePatch{EstimatePointID: PatchField[string]{Set: true, Value: &pid}}); err != nil {
		t.Fatalf("UpdateIssue estimate: %v", err)
	}
	types := runTriggerTypes(t, fx)
	if len(types) != 1 || types[0] != "issue.estimate_changed" {
		t.Fatalf("run trigger types = %v, want [issue.estimate_changed]", types)
	}
	// An unrelated change stays silent.
	if _, err := UpdateIssue(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin,
		IssuePatch{Priority: autoIntPtr(4)}); err != nil {
		t.Fatalf("UpdateIssue priority: %v", err)
	}
	if runs := automationRunRows(t, fx); len(runs) != 1 {
		t.Fatalf("runs = %d, want 1 (priority change must not fire estimate)", len(runs))
	}
}

func TestAutomationCommentAddedTriggerFiresNoCascade(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	in := eventRuleInput("comment bot",
		AutomationTrigger{Type: "issue.comment_added"},
		[]AutomationAction{{Type: "add_comment", Body: autoStrPtr("noted!")}})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "comment fires")
	doc := json.RawMessage(`{"type":"doc","content":[]}`)
	if _, err := CreateComment(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.member, doc, nil); err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	// Exactly two comments: the user's and the automation's. The
	// automation's own comment must not re-trigger (depth-1 loop guard
	// plus the automation bypass of CreateComment).
	if n := commentCount(t, fx, iss.ID); n != 2 {
		t.Fatalf("comments = %d, want 2 (user comment + one automation comment, no cascade)", n)
	}
	runs := automationRunRows(t, fx)
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1 (no cascading firings)", len(runs))
	}
	if runs[0].TriggerType != "issue.comment_added" {
		t.Fatalf("run trigger type = %q, want issue.comment_added", runs[0].TriggerType)
	}
}

func TestAutomationCommentAddedTriggerSilentOnUnrelatedChange(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	in := eventRuleInput("comment bot",
		AutomationTrigger{Type: "issue.comment_added"},
		[]AutomationAction{{Type: "add_comment", Body: autoStrPtr("noted!")}})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "comment silent")
	if _, err := UpdateIssue(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin,
		IssuePatch{Priority: autoIntPtr(2)}); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	if runs := automationRunRows(t, fx); len(runs) != 0 {
		t.Fatalf("runs = %d, want 0 (priority change must not fire comment_added)", len(runs))
	}
}

func TestAutomationBulkSetFiresLabelAndAssigneeEvents(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	labelIn := eventRuleInput("bulk label bot",
		AutomationTrigger{Type: "issue.labels_changed"},
		[]AutomationAction{{Type: "add_comment", Body: autoStrPtr("bulk label!")}})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, labelIn); err != nil {
		t.Fatalf("CreateAutomationRule labels: %v", err)
	}
	assignIn := eventRuleInput("bulk assign bot",
		AutomationTrigger{Type: "issue.assigned"},
		[]AutomationAction{{Type: "add_comment", Body: autoStrPtr("bulk assigned!")}})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, assignIn); err != nil {
		t.Fatalf("CreateAutomationRule assigned: %v", err)
	}
	unassignIn := eventRuleInput("bulk unassign bot",
		AutomationTrigger{Type: "issue.unassigned"},
		[]AutomationAction{{Type: "add_comment", Body: autoStrPtr("bulk unassigned!")}})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, unassignIn); err != nil {
		t.Fatalf("CreateAutomationRule unassigned: %v", err)
	}

	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "bulk events")
	labelIDs := []string{fx.labelID}
	if _, _, err := BulkSetIssues(ctx, fx.pool, fx.slug, fx.ident, fx.admin,
		[]string{iss.ID}, BulkIssueSet{LabelIDs: &labelIDs}); err != nil {
		t.Fatalf("BulkSetIssues labels: %v", err)
	}
	if got, want := runTriggerTypeSet(t, fx), map[string]int{"issue.labels_changed": 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("run trigger types = %v, want %v", got, want)
	}

	uid := fx.member
	if _, _, err := BulkSetIssues(ctx, fx.pool, fx.slug, fx.ident, fx.admin,
		[]string{iss.ID}, BulkIssueSet{AssigneeID: PatchField[string]{Set: true, Value: &uid}}); err != nil {
		t.Fatalf("BulkSetIssues assign: %v", err)
	}
	if got, want := runTriggerTypeSet(t, fx), map[string]int{"issue.labels_changed": 1, "issue.assigned": 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("run trigger types = %v, want %v", got, want)
	}

	// Clearing the assignee fires issue.unassigned.
	if _, _, err := BulkSetIssues(ctx, fx.pool, fx.slug, fx.ident, fx.admin,
		[]string{iss.ID}, BulkIssueSet{AssigneeID: PatchField[string]{Set: true}}); err != nil {
		t.Fatalf("BulkSetIssues unassign: %v", err)
	}
	if got, want := runTriggerTypeSet(t, fx), map[string]int{
		"issue.labels_changed": 1, "issue.assigned": 1, "issue.unassigned": 1,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("run trigger types = %v, want %v", got, want)
	}
}

// TestAutomationEventsRollbackFiresNothing drives updateIssueTx inside a
// manual tx, watches the automation write land in-tx, then rolls back:
// the firing must vanish with the write. This is the rollback half of
// the task's "never fire for rolled-back writes" requirement — the
// in-tx emission is what makes it hold.
func TestAutomationEventsRollbackFiresNothing(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	in := eventRuleInput("rollback bot",
		AutomationTrigger{Type: "issue.priority_changed", ToPriorities: []int{3}},
		[]AutomationAction{{Type: "add_comment", Body: autoStrPtr("rolled back?")}})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "rollback")
	var projectID string
	if err := fx.pool.QueryRow(ctx,
		`SELECT project_id::text FROM issues WHERE id = $1::uuid`, iss.ID).Scan(&projectID); err != nil {
		t.Fatalf("project_id: %v", err)
	}

	tx, err := fx.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, _, err := updateIssueTx(ctx, tx, projectID, fx.ident, iss.ID, fx.admin,
		IssuePatch{Priority: autoIntPtr(3)}); err != nil {
		tx.Rollback(ctx)
		t.Fatalf("updateIssueTx: %v", err)
	}
	// The automation write is visible in-tx...
	var inTx int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM comments WHERE issue_id = $1::uuid`, iss.ID).Scan(&inTx); err != nil {
		tx.Rollback(ctx)
		t.Fatalf("in-tx comment count: %v", err)
	}
	if inTx != 1 {
		tx.Rollback(ctx)
		t.Fatalf("in-tx comments = %d, want 1 (automation runs in the firing tx)", inTx)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	// ...and gone after the rollback: no comment, no run row.
	if n := commentCount(t, fx, iss.ID); n != 0 {
		t.Fatalf("comments = %d, want 0 (rolled-back write must not fire)", n)
	}
	if runs := automationRunRows(t, fx); len(runs) != 0 {
		t.Fatalf("runs = %d, want 0 (rolled-back write must not fire)", len(runs))
	}
	// The issue itself is untouched too.
	got, err := GetIssue(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.Priority != 0 {
		t.Fatalf("priority = %d, want 0 (rolled back)", got.Priority)
	}
}

func TestAutomationTriggerValidationC16T0(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	unknownLabel := "12345678-1234-1234-1234-1234567890ab"
	bad := []struct {
		name string
		trig AutomationTrigger
	}{
		{"unknown label id", AutomationTrigger{Type: "issue.labels_changed", LabelIDs: []string{unknownLabel}}},
		{"malformed label id", AutomationTrigger{Type: "issue.labels_changed", LabelIDs: []string{"not-a-uuid"}}},
		{"priority 5 in from", AutomationTrigger{Type: "issue.priority_changed", FromPriorities: []int{5}}},
		{"priority -1 in to", AutomationTrigger{Type: "issue.priority_changed", ToPriorities: []int{-1}}},
		{"filter on assigned", AutomationTrigger{Type: "issue.assigned", LabelIDs: []string{fx.labelID}}},
		{"filter on unassigned", AutomationTrigger{Type: "issue.unassigned", ToPriorities: []int{1}}},
		{"filter on created", AutomationTrigger{Type: "issue.created", LabelIDs: []string{fx.labelID}}},
		{"filter on comment_added", AutomationTrigger{Type: "issue.comment_added", FromStates: []string{fx.backlog}}},
		{"filter on due_date_changed", AutomationTrigger{Type: "issue.due_date_changed", ToStates: []string{fx.backlog}}},
		{"filter on estimate_changed", AutomationTrigger{Type: "issue.estimate_changed", FromPriorities: []int{1}}},
		{"label_ids on state_changed", AutomationTrigger{Type: "issue.state_changed", LabelIDs: []string{fx.labelID}}},
		{"to_states on labels_changed", AutomationTrigger{Type: "issue.labels_changed", ToStates: []string{fx.backlog}}},
		{"from_states on priority_changed", AutomationTrigger{Type: "issue.priority_changed", FromStates: []string{fx.backlog}}},
		{"unknown type", AutomationTrigger{Type: "issue.exploded"}},
	}
	for _, tc := range bad {
		in := AutomationRuleInput{
			Name:    "bad trigger",
			Trigger: tc.trig,
			Actions: []AutomationAction{{Type: "add_comment", Body: autoStrPtr("x")}},
		}
		if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); !errors.Is(err, ErrInvalidAutomationTrigger) {
			t.Errorf("%s: err = %v, want ErrInvalidAutomationTrigger", tc.name, err)
		}
	}

	good := []struct {
		name string
		trig AutomationTrigger
	}{
		{"assigned", AutomationTrigger{Type: "issue.assigned"}},
		{"unassigned", AutomationTrigger{Type: "issue.unassigned"}},
		{"labels_changed bare", AutomationTrigger{Type: "issue.labels_changed"}},
		{"labels_changed filtered", AutomationTrigger{Type: "issue.labels_changed", LabelIDs: []string{fx.labelID}}},
		{"priority_changed bare", AutomationTrigger{Type: "issue.priority_changed"}},
		{"priority_changed filtered", AutomationTrigger{Type: "issue.priority_changed", FromPriorities: []int{0}, ToPriorities: []int{4}}},
		{"due_date_changed", AutomationTrigger{Type: "issue.due_date_changed"}},
		{"estimate_changed", AutomationTrigger{Type: "issue.estimate_changed"}},
		{"comment_added", AutomationTrigger{Type: "issue.comment_added"}},
	}
	for _, tc := range good {
		in := AutomationRuleInput{
			Name:    "good trigger " + tc.name,
			Trigger: tc.trig,
			Actions: []AutomationAction{{Type: "add_comment", Body: autoStrPtr("x")}},
		}
		if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
			t.Errorf("%s: CreateAutomationRule: %v", tc.name, err)
		}
	}

	// The new filter fields round-trip through the JSONB trigger column.
	rules, err := ListAutomationRules(ctx, fx.pool, fx.slug, fx.ident, fx.admin)
	if err != nil {
		t.Fatalf("ListAutomationRules: %v", err)
	}
	var found bool
	for _, r := range rules {
		var trig AutomationTrigger
		if err := json.Unmarshal(r.Trigger, &trig); err != nil {
			t.Fatalf("unmarshal trigger: %v", err)
		}
		if trig.Type == "issue.labels_changed" && len(trig.LabelIDs) == 1 && trig.LabelIDs[0] == fx.labelID {
			found = true
		}
	}
	if !found {
		t.Fatal("labels_changed rule with label_ids did not round-trip through storage")
	}
}
