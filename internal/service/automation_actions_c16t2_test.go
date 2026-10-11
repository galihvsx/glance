package service

// C16T2 new automation actions (backend): service tests for validation,
// execution, no-op semantics, and the abort-on-failure rule.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// c16t2UnknownID is a well-formed UUID that matches nothing in the test
// DB — the repo convention for unknown-ID validation probes.
const c16t2UnknownID = "00000000-0000-0000-0000-000000000000"

// c16t2Rule creates a state_changed rule firing on move-to-started with
// the given actions.
func c16t2Rule(t *testing.T, fx automationFixture, actions []AutomationAction) {
	t.Helper()
	in := automationRuleInput(fx, []string{fx.started}, actions)
	if _, err := CreateAutomationRule(context.Background(), fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
}

// c16t2ExpectInvalid asserts CreateAutomationRule rejects the action
// with ErrInvalidAutomationAction (honest 400).
func c16t2ExpectInvalid(t *testing.T, fx automationFixture, a AutomationAction) {
	t.Helper()
	in := automationRuleInput(fx, []string{fx.started}, []AutomationAction{a})
	_, err := CreateAutomationRule(context.Background(), fx.pool, fx.slug, fx.ident, fx.admin, in)
	if !errors.Is(err, ErrInvalidAutomationAction) {
		t.Fatalf("CreateAutomationRule(%s) err = %v, want ErrInvalidAutomationAction", a.Type, err)
	}
}

func c16t2Fire(t *testing.T, fx automationFixture, name string) string {
	t.Helper()
	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, name)
	moveIssueToState(t, fx, iss.ID, fx.admin, fx.started)
	return iss.ID
}

func TestAutomationRemoveLabelExecutes(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()
	c16t2Rule(t, fx, []AutomationAction{{Type: "remove_label", LabelID: autoStrPtr(fx.labelID)}})

	issID := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "remove label").ID
	if _, err := fx.pool.Exec(ctx,
		`INSERT INTO issue_labels (issue_id, label_id) VALUES ($1::uuid, $2::uuid)`, issID, fx.labelID); err != nil {
		t.Fatalf("attach label: %v", err)
	}
	moveIssueToState(t, fx, issID, fx.admin, fx.started)

	if n := countRows(t, fx.pool,
		`SELECT count(*) FROM issue_labels WHERE issue_id = $1::uuid`, issID); n != 0 {
		t.Fatalf("labels = %d, want 0 (removed)", n)
	}
	if n := issueActivityCount(t, fx, issID, "labels"); n != 1 {
		t.Fatalf("labels activities = %d, want 1", n)
	}
	runs := automationRunRows(t, fx)
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
}

func TestAutomationRemoveLabelNoOpWritesNoRun(t *testing.T) {
	fx := setupAutomationFixture(t)
	c16t2Rule(t, fx, []AutomationAction{{Type: "remove_label", LabelID: autoStrPtr(fx.labelID)}})
	c16t2Fire(t, fx, "remove label noop")
	if n := len(automationRunRows(t, fx)); n != 0 {
		t.Fatalf("runs = %d, want 0 (no-op firing writes no row)", n)
	}
}

func TestAutomationRemoveLabelValidation(t *testing.T) {
	fx := setupAutomationFixture(t)
	c16t2ExpectInvalid(t, fx, AutomationAction{Type: "remove_label"}) // missing label_id
	c16t2ExpectInvalid(t, fx, AutomationAction{Type: "remove_label", LabelID: autoStrPtr(c16t2UnknownID)})
	c16t2ExpectInvalid(t, fx, AutomationAction{Type: "remove_label", LabelID: autoStrPtr("not-a-uuid")})
}

func TestAutomationUnassignClearsAll(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()
	other := createTestUser(t, fx.pool, uniqueTestEmail("auto-other"))
	if err := UpsertMember(ctx, fx.pool, fx.slug, fx.admin, other, RoleMember); err != nil {
		t.Fatalf("UpsertMember other: %v", err)
	}
	c16t2Rule(t, fx, []AutomationAction{{Type: "unassign"}})

	issID := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "unassign all").ID
	for _, u := range []string{fx.member, other} {
		if err := AssignAssignee(ctx, fx.pool, fx.slug, fx.ident, issID, u, fx.admin); err != nil {
			t.Fatalf("AssignAssignee: %v", err)
		}
	}
	moveIssueToState(t, fx, issID, fx.admin, fx.started)

	if n := countRows(t, fx.pool,
		`SELECT count(*) FROM issue_assignees WHERE issue_id = $1::uuid`, issID); n != 0 {
		t.Fatalf("assignees = %d, want 0 (cleared)", n)
	}
	if n := issueActivityCount(t, fx, issID, "assignees"); n != 1 {
		t.Fatalf("assignees activities = %d, want 1", n)
	}
}

func TestAutomationUnassignNoOpWritesNoRun(t *testing.T) {
	fx := setupAutomationFixture(t)
	c16t2Rule(t, fx, []AutomationAction{{Type: "unassign"}})
	c16t2Fire(t, fx, "unassign noop")
	if n := len(automationRunRows(t, fx)); n != 0 {
		t.Fatalf("runs = %d, want 0 (no-op firing writes no row)", n)
	}
}

func c16t2EstimateFixture(t *testing.T, fx automationFixture) *Estimate {
	t.Helper()
	est, err := CreateEstimate(context.Background(), fx.pool, fx.slug, fx.ident, fx.admin, EstimateInput{
		Name:   "Fibonacci",
		Points: []EstimatePointInput{{Key: "S", Value: 1}, {Key: "M", Value: 3}},
	})
	if err != nil {
		t.Fatalf("CreateEstimate: %v", err)
	}
	if len(est.Points) != 2 {
		t.Fatalf("points = %d, want 2", len(est.Points))
	}
	return est
}

func TestAutomationSetEstimateApplies(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()
	est := c16t2EstimateFixture(t, fx)
	c16t2Rule(t, fx, []AutomationAction{{Type: "set_estimate", Estimate: autoStrPtr(est.Points[1].ID)}})

	issID := c16t2Fire(t, fx, "set estimate")
	got, err := GetIssue(ctx, fx.pool, fx.slug, fx.ident, issID, fx.admin)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.EstimatePointID == nil || *got.EstimatePointID != est.Points[1].ID {
		t.Fatalf("estimate_point_id = %v, want %s", got.EstimatePointID, est.Points[1].ID)
	}
	if n := issueActivityCount(t, fx, issID, "estimate_point_id"); n != 1 {
		t.Fatalf("estimate_point_id activities = %d, want 1", n)
	}
}

func TestAutomationSetEstimateNoOpWritesNoRun(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()
	est := c16t2EstimateFixture(t, fx)
	c16t2Rule(t, fx, []AutomationAction{{Type: "set_estimate", Estimate: autoStrPtr(est.Points[0].ID)}})

	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "estimate noop")
	if _, err := fx.pool.Exec(ctx,
		`UPDATE issues SET estimate_point_id = $1::uuid WHERE id = $2::uuid`, est.Points[0].ID, iss.ID); err != nil {
		t.Fatalf("preset estimate: %v", err)
	}
	moveIssueToState(t, fx, iss.ID, fx.admin, fx.started)
	if n := len(automationRunRows(t, fx)); n != 0 {
		t.Fatalf("runs = %d, want 0 (no-op firing writes no row)", n)
	}
}

func TestAutomationSetEstimateValidation(t *testing.T) {
	fx := setupAutomationFixture(t)
	est := c16t2EstimateFixture(t, fx)
	c16t2ExpectInvalid(t, fx, AutomationAction{Type: "set_estimate"}) // missing estimate
	c16t2ExpectInvalid(t, fx, AutomationAction{Type: "set_estimate", Estimate: autoStrPtr(c16t2UnknownID)})

	// A point on another project's scale is a 400, not a cross-project write.
	ctx := context.Background()
	otherIdent := uniqueTestIdent()
	if _, err := CreateProject(ctx, fx.pool, fx.slug, fx.admin, "Other Project", otherIdent); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	otherEst, err := CreateEstimate(ctx, fx.pool, fx.slug, otherIdent, fx.admin, EstimateInput{
		Name:   "T-shirt",
		Points: []EstimatePointInput{{Key: "XS", Value: 1}},
	})
	if err != nil {
		t.Fatalf("CreateEstimate other: %v", err)
	}
	c16t2ExpectInvalid(t, fx, AutomationAction{Type: "set_estimate", Estimate: autoStrPtr(otherEst.Points[0].ID)})

	// A valid point passes validation (creation succeeds).
	in := automationRuleInput(fx, []string{fx.started},
		[]AutomationAction{{Type: "set_estimate", Estimate: autoStrPtr(est.Points[0].ID)}})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
		t.Fatalf("CreateAutomationRule valid set_estimate: %v", err)
	}
}

func TestAutomationSetDueDateAbsolute(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()
	c16t2Rule(t, fx, []AutomationAction{{Type: "set_due_date", DueDate: autoStrPtr("2027-03-15")}})

	issID := c16t2Fire(t, fx, "due date absolute")
	got, err := GetIssue(ctx, fx.pool, fx.slug, fx.ident, issID, fx.admin)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.TargetDate == nil || got.TargetDate.UTC().Format("2006-01-02") != "2027-03-15" {
		t.Fatalf("target_date = %v, want 2027-03-15", got.TargetDate)
	}
	if n := issueActivityCount(t, fx, issID, "target_date"); n != 1 {
		t.Fatalf("target_date activities = %d, want 1", n)
	}
}

func TestAutomationSetDueDateRelative(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()
	c16t2Rule(t, fx, []AutomationAction{{Type: "set_due_date", DueDate: autoStrPtr("+5d")}})

	issID := c16t2Fire(t, fx, "due date relative")
	want := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, 5).Format("2006-01-02")
	got, err := GetIssue(ctx, fx.pool, fx.slug, fx.ident, issID, fx.admin)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.TargetDate == nil || got.TargetDate.UTC().Format("2006-01-02") != want {
		t.Fatalf("target_date = %v, want %s (+5d)", got.TargetDate, want)
	}
}

func TestAutomationSetDueDateNoOpWritesNoRun(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()
	c16t2Rule(t, fx, []AutomationAction{{Type: "set_due_date", DueDate: autoStrPtr("2027-03-15")}})

	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "due date noop")
	if _, err := fx.pool.Exec(ctx,
		`UPDATE issues SET target_date = '2027-03-15'::date WHERE id = $1::uuid`, iss.ID); err != nil {
		t.Fatalf("preset target_date: %v", err)
	}
	moveIssueToState(t, fx, iss.ID, fx.admin, fx.started)
	if n := len(automationRunRows(t, fx)); n != 0 {
		t.Fatalf("runs = %d, want 0 (no-op firing writes no row)", n)
	}
}

func TestAutomationSetDueDateValidation(t *testing.T) {
	fx := setupAutomationFixture(t)
	c16t2ExpectInvalid(t, fx, AutomationAction{Type: "set_due_date"}) // missing due_date
	for _, bad := range []string{"", "tomorrow", "2027-13-01", "2027-02-30", "+d", "+5", "5d", "-3d", "2027-03-15T10:00:00Z"} {
		c16t2ExpectInvalid(t, fx, AutomationAction{Type: "set_due_date", DueDate: autoStrPtr(bad)})
	}
	// Valid shapes pass.
	ctx := context.Background()
	for _, good := range []string{"2027-03-15", "+0d", "+30d"} {
		in := automationRuleInput(fx, []string{fx.started},
			[]AutomationAction{{Type: "set_due_date", DueDate: autoStrPtr(good)}})
		if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
			t.Fatalf("CreateAutomationRule set_due_date %q: %v", good, err)
		}
	}
}

// c16t2ProjIdent is the fixture project's identifier in its normalized
// (uppercase) form: CreateCycle/CreateModule/AddCycleIssues/AddModuleIssues
// resolve the project without normalizing the identifier, unlike
// CreateEstimate/CreateLabel — passing the raw fixture ident (lowercase)
// hits "project not found".
func c16t2ProjIdent(fx automationFixture) string { return strings.ToUpper(fx.ident) }

func c16t2Cycle(t *testing.T, fx automationFixture, name string) string {
	t.Helper()
	c, err := CreateCycle(context.Background(), fx.pool, fx.slug, c16t2ProjIdent(fx), fx.admin, CycleInput{
		Name:      name,
		StartDate: time.Now().UTC(),
		EndDate:   time.Now().UTC().AddDate(0, 0, 14),
	})
	if err != nil {
		t.Fatalf("CreateCycle: %v", err)
	}
	return c.ID
}

func TestAutomationMoveToCycle(t *testing.T) {
	fx := setupAutomationFixture(t)
	c1 := c16t2Cycle(t, fx, "Sprint 1")
	c2 := c16t2Cycle(t, fx, "Sprint 2")
	c16t2Rule(t, fx, []AutomationAction{{Type: "move_to_cycle", CycleID: autoStrPtr(c1)}})

	issID := c16t2Fire(t, fx, "move to cycle")
	if n := countRows(t, fx.pool,
		`SELECT count(*) FROM cycle_issues WHERE cycle_id = $1::uuid AND issue_id = $2::uuid`, c1, issID); n != 1 {
		t.Fatalf("cycle_issues(c1) = %d, want 1", n)
	}

	// Moving again replaces the cycle (move semantics): the issue rides
	// one sprint at a time.
	in := automationRuleInput(fx, []string{fx.completed},
		[]AutomationAction{{Type: "move_to_cycle", CycleID: autoStrPtr(c2)}})
	if _, err := CreateAutomationRule(context.Background(), fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	moveIssueToState(t, fx, issID, fx.admin, fx.completed)
	if n := countRows(t, fx.pool,
		`SELECT count(*) FROM cycle_issues WHERE issue_id = $1::uuid`, issID); n != 1 {
		t.Fatalf("cycle_issues = %d, want 1 (moved, not duplicated)", n)
	}
	if n := countRows(t, fx.pool,
		`SELECT count(*) FROM cycle_issues WHERE cycle_id = $1::uuid AND issue_id = $2::uuid`, c2, issID); n != 1 {
		t.Fatalf("cycle_issues(c2) = %d, want 1", n)
	}
	if n := issueActivityCount(t, fx, issID, "cycle_id"); n != 2 {
		t.Fatalf("cycle_id activities = %d, want 2", n)
	}
}

func TestAutomationMoveToCycleNoOpWritesNoRun(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()
	c1 := c16t2Cycle(t, fx, "Sprint 1")
	c16t2Rule(t, fx, []AutomationAction{{Type: "move_to_cycle", CycleID: autoStrPtr(c1)}})

	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "cycle noop")
	if err := AddCycleIssues(ctx, fx.pool, fx.slug, c16t2ProjIdent(fx), fx.admin, c1, []string{iss.ID}); err != nil {
		t.Fatalf("AddCycleIssues: %v", err)
	}
	moveIssueToState(t, fx, iss.ID, fx.admin, fx.started)
	if n := len(automationRunRows(t, fx)); n != 0 {
		t.Fatalf("runs = %d, want 0 (no-op firing writes no row)", n)
	}
}

func TestAutomationMoveToCycleValidation(t *testing.T) {
	fx := setupAutomationFixture(t)
	c16t2ExpectInvalid(t, fx, AutomationAction{Type: "move_to_cycle"}) // missing cycle_id
	c16t2ExpectInvalid(t, fx, AutomationAction{Type: "move_to_cycle", CycleID: autoStrPtr(c16t2UnknownID)})
	c16t2ExpectInvalid(t, fx, AutomationAction{Type: "move_to_cycle", CycleID: autoStrPtr("not-a-uuid")})

	// A cycle from another project is a 400.
	ctx := context.Background()
	otherIdent := uniqueTestIdent()
	if _, err := CreateProject(ctx, fx.pool, fx.slug, fx.admin, "Other Project", otherIdent); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	oc, err := CreateCycle(ctx, fx.pool, fx.slug, strings.ToUpper(otherIdent), fx.admin, CycleInput{
		Name:      "Foreign Sprint",
		StartDate: time.Now().UTC(),
		EndDate:   time.Now().UTC().AddDate(0, 0, 14),
	})
	if err != nil {
		t.Fatalf("CreateCycle other: %v", err)
	}
	c16t2ExpectInvalid(t, fx, AutomationAction{Type: "move_to_cycle", CycleID: autoStrPtr(oc.ID)})
}

func TestAutomationMoveToCycleExecFailureRecorded(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()
	c1 := c16t2Cycle(t, fx, "Doomed Sprint")
	c16t2Rule(t, fx, []AutomationAction{
		{Type: "add_comment", Body: autoStrPtr("before the failure")},
		{Type: "move_to_cycle", CycleID: autoStrPtr(c1)},
	})
	// The cycle vanishes between rule creation and the firing: the move
	// fails, is recorded ok:false, and the comment before it stays.
	if err := DeleteCycle(ctx, fx.pool, fx.slug, c16t2ProjIdent(fx), fx.admin, c1); err != nil {
		t.Fatalf("DeleteCycle: %v", err)
	}
	c16t2Fire(t, fx, "cycle exec failure")

	runs := automationRunRows(t, fx)
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
	results := automationRunActionResults(t, runs[0])
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	if !results[0].OK {
		t.Fatalf("add_comment result = %+v, want ok", results[0])
	}
	if results[1].Type != "move_to_cycle" || results[1].OK {
		t.Fatalf("move_to_cycle result = %+v, want ok:false", results[1])
	}
	if results[1].Error == nil || *results[1].Error == "" {
		t.Fatalf("move_to_cycle error = %v, want non-empty", results[1].Error)
	}
}

func c16t2Module(t *testing.T, fx automationFixture, name string) string {
	t.Helper()
	m, err := CreateModule(context.Background(), fx.pool, fx.slug, c16t2ProjIdent(fx), fx.admin, ModuleInput{Name: name})
	if err != nil {
		t.Fatalf("CreateModule: %v", err)
	}
	return m.ID
}

func TestAutomationMoveToModule(t *testing.T) {
	fx := setupAutomationFixture(t)
	m1 := c16t2Module(t, fx, "Platform")
	m2 := c16t2Module(t, fx, "Mobile")
	c16t2Rule(t, fx, []AutomationAction{{Type: "move_to_module", ModuleID: autoStrPtr(m1)}})

	issID := c16t2Fire(t, fx, "move to module")
	if n := countRows(t, fx.pool,
		`SELECT count(*) FROM module_issues WHERE module_id = $1::uuid AND issue_id = $2::uuid`, m1, issID); n != 1 {
		t.Fatalf("module_issues(m1) = %d, want 1", n)
	}

	in := automationRuleInput(fx, []string{fx.completed},
		[]AutomationAction{{Type: "move_to_module", ModuleID: autoStrPtr(m2)}})
	if _, err := CreateAutomationRule(context.Background(), fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	moveIssueToState(t, fx, issID, fx.admin, fx.completed)
	if n := countRows(t, fx.pool,
		`SELECT count(*) FROM module_issues WHERE issue_id = $1::uuid`, issID); n != 1 {
		t.Fatalf("module_issues = %d, want 1 (moved, not duplicated)", n)
	}
	if n := countRows(t, fx.pool,
		`SELECT count(*) FROM module_issues WHERE module_id = $1::uuid AND issue_id = $2::uuid`, m2, issID); n != 1 {
		t.Fatalf("module_issues(m2) = %d, want 1", n)
	}
	if n := issueActivityCount(t, fx, issID, "module_id"); n != 2 {
		t.Fatalf("module_id activities = %d, want 2", n)
	}
}

func TestAutomationMoveToModuleNoOpWritesNoRun(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()
	m1 := c16t2Module(t, fx, "Platform")
	c16t2Rule(t, fx, []AutomationAction{{Type: "move_to_module", ModuleID: autoStrPtr(m1)}})

	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "module noop")
	if err := AddModuleIssues(ctx, fx.pool, fx.slug, c16t2ProjIdent(fx), fx.admin, m1, []string{iss.ID}); err != nil {
		t.Fatalf("AddModuleIssues: %v", err)
	}
	moveIssueToState(t, fx, iss.ID, fx.admin, fx.started)
	if n := len(automationRunRows(t, fx)); n != 0 {
		t.Fatalf("runs = %d, want 0 (no-op firing writes no row)", n)
	}
}

func TestAutomationMoveToModuleValidation(t *testing.T) {
	fx := setupAutomationFixture(t)
	c16t2ExpectInvalid(t, fx, AutomationAction{Type: "move_to_module"}) // missing module_id
	c16t2ExpectInvalid(t, fx, AutomationAction{Type: "move_to_module", ModuleID: autoStrPtr(c16t2UnknownID)})
	c16t2ExpectInvalid(t, fx, AutomationAction{Type: "move_to_module", ModuleID: autoStrPtr("not-a-uuid")})
}

func TestAutomationAddWatcherExecutes(t *testing.T) {
	fx := setupAutomationFixture(t)
	c16t2Rule(t, fx, []AutomationAction{{Type: "add_watcher", UserID: autoStrPtr(fx.member)}})

	issID := c16t2Fire(t, fx, "add watcher")
	if n := countRows(t, fx.pool,
		`SELECT count(*) FROM issue_subscribers WHERE issue_id = $1::uuid AND user_id = $2::uuid`,
		issID, fx.member); n != 1 {
		t.Fatalf("subscribers = %d, want 1", n)
	}
}

func TestAutomationAddWatcherNoOpWhenAlreadyWatching(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()
	// An assignee already watches (watchers = subscribers ∪ assignees):
	// adding them is a no-op.
	c16t2Rule(t, fx, []AutomationAction{{Type: "add_watcher", UserID: autoStrPtr(fx.member)}})

	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "watcher noop")
	if err := AssignAssignee(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.member, fx.admin); err != nil {
		t.Fatalf("AssignAssignee: %v", err)
	}
	moveIssueToState(t, fx, iss.ID, fx.admin, fx.started)
	if n := len(automationRunRows(t, fx)); n != 0 {
		t.Fatalf("runs = %d, want 0 (no-op firing writes no row)", n)
	}
}

func TestAutomationAddWatcherValidation(t *testing.T) {
	fx := setupAutomationFixture(t)
	c16t2ExpectInvalid(t, fx, AutomationAction{Type: "add_watcher"}) // missing user_id
	c16t2ExpectInvalid(t, fx, AutomationAction{Type: "add_watcher", UserID: autoStrPtr(c16t2UnknownID)})
	c16t2ExpectInvalid(t, fx, AutomationAction{Type: "add_watcher", UserID: autoStrPtr("not-a-uuid")})

	// A user who is not a workspace member is a 400.
	outsider := createTestUser(t, fx.pool, uniqueTestEmail("auto-outsider"))
	c16t2ExpectInvalid(t, fx, AutomationAction{Type: "add_watcher", UserID: autoStrPtr(outsider)})
}

// TestAutomationFailedActionAbortsTail: a mid-rule failure stops the
// tail (C16T2 semantics). The failing action is recorded ok:false; the
// tail action never runs and never appears in the run row.
func TestAutomationFailedActionAbortsTail(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()
	c16t2Rule(t, fx, []AutomationAction{
		{Type: "add_comment", Body: autoStrPtr("first ok")},
		{Type: "assign", UserID: autoStrPtr(fx.member)},
		{Type: "add_comment", Body: autoStrPtr("tail must not run")},
	})
	// The assignee leaves between rule creation and the firing: the
	// middle action fails.
	if err := RemoveMember(ctx, fx.pool, fx.slug, fx.admin, fx.member); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	issID := c16t2Fire(t, fx, "abort tail")

	if n := countRows(t, fx.pool,
		`SELECT count(*) FROM comments WHERE issue_id = $1::uuid`, issID); n != 1 {
		t.Fatalf("comments = %d, want 1 (tail comment must not exist)", n)
	}
	runs := automationRunRows(t, fx)
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
	results := automationRunActionResults(t, runs[0])
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2 (tail action never recorded)", len(results))
	}
	if !results[0].OK || results[0].Type != "add_comment" {
		t.Fatalf("result[0] = %+v, want ok add_comment", results[0])
	}
	if results[1].Type != "assign" || results[1].OK {
		t.Fatalf("result[1] = %+v, want ok:false assign", results[1])
	}
	if results[1].Error == nil || *results[1].Error == "" {
		t.Fatalf("result[1] error = %v, want non-empty", results[1].Error)
	}
	// The firing event still committed despite the aborted rule.
	got, err := GetIssue(ctx, fx.pool, fx.slug, fx.ident, issID, fx.admin)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.StateID != fx.started {
		t.Fatalf("state = %s, want %s (move must commit)", got.StateID, fx.started)
	}
}
