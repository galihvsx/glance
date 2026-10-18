package service

// Workflow transition controls (C68T0): per-project state-transition
// allowlist, enforced on user-initiated state moves. Zero rules =
// unrestricted (backwards compatible). Real test database, no skips.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// workflowFixture is a project with its seeded states resolved by group.
type workflowFixture struct {
	pool                                       *pgxpool.Pool
	slug, ident, admin, member                 string
	backlog, todo, inProgress, done, cancelled string
}

func setupWorkflowFixture(t *testing.T) workflowFixture {
	t.Helper()
	pool := newTestPool(t)
	ctx := context.Background()
	admin := createTestUser(t, pool, uniqueTestEmail("wf-admin"))
	member := createTestUser(t, pool, uniqueTestEmail("wf-member"))
	slug := uniqueTestSlug("wf")
	createTestWorkspace(t, pool, "WF Corp", slug, admin)
	if err := UpsertMember(ctx, pool, slug, admin, member, RoleMember); err != nil {
		t.Fatalf("UpsertMember member: %v", err)
	}
	ident := uniqueTestIdent()
	if _, err := CreateProject(ctx, pool, slug, admin, "WF Project", ident); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	states, err := ListStates(ctx, pool, slug, ident, admin)
	if err != nil {
		t.Fatalf("ListStates: %v", err)
	}
	fx := workflowFixture{pool: pool, slug: slug, ident: ident, admin: admin, member: member}
	for _, s := range states {
		switch s.Group {
		case "backlog":
			fx.backlog = s.ID
		case "unstarted":
			fx.todo = s.ID
		case "started":
			fx.inProgress = s.ID
		case "completed":
			fx.done = s.ID
		case "cancelled":
			fx.cancelled = s.ID
		}
	}
	if fx.backlog == "" || fx.todo == "" || fx.inProgress == "" || fx.done == "" {
		t.Fatal("seeded states missing backlog/todo/in-progress/done groups")
	}
	return fx
}

func (fx workflowFixture) setRules(t *testing.T, rules []WorkflowTransition) {
	t.Helper()
	if _, err := SetWorkflow(context.Background(), fx.pool, fx.slug, fx.ident, fx.admin, rules); err != nil {
		t.Fatalf("SetWorkflow: %v", err)
	}
}

// TestSetWorkflowValidation: unknown states, self-loops, and foreign-project
// states are rejected; an empty list clears the rules.
func TestSetWorkflowValidation(t *testing.T) {
	fx := setupWorkflowFixture(t)
	ctx := context.Background()

	// Unknown state id → 400-class sentinel.
	if _, err := SetWorkflow(ctx, fx.pool, fx.slug, fx.ident, fx.admin,
		[]WorkflowTransition{{FromStateID: fx.todo, ToStateID: "00000000-0000-0000-0000-000000000000"}}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("unknown to-state: err = %v, want ErrInvalidState", err)
	}
	// Self-loop → 400-class sentinel.
	if _, err := SetWorkflow(ctx, fx.pool, fx.slug, fx.ident, fx.admin,
		[]WorkflowTransition{{FromStateID: fx.todo, ToStateID: fx.todo}}); !errors.Is(err, ErrWorkflowSelfTransition) {
		t.Fatalf("self-loop: err = %v, want ErrWorkflowSelfTransition", err)
	}
	// State from another project → 400-class sentinel.
	other := setupWorkflowFixture(t)
	if _, err := SetWorkflow(ctx, fx.pool, fx.slug, fx.ident, fx.admin,
		[]WorkflowTransition{{FromStateID: fx.todo, ToStateID: other.done}}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("foreign state: err = %v, want ErrInvalidState", err)
	}
	// Guest cannot configure → 403-class sentinel.
	guest := createTestUser(t, fx.pool, uniqueTestEmail("wf-guest"))
	if err := UpsertMember(ctx, fx.pool, fx.slug, fx.admin, guest, RoleGuest); err != nil {
		t.Fatalf("UpsertMember guest: %v", err)
	}
	if _, err := SetWorkflow(ctx, fx.pool, fx.slug, fx.ident, guest,
		[]WorkflowTransition{{FromStateID: fx.todo, ToStateID: fx.inProgress}}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest set: err = %v, want ErrForbidden", err)
	}

	// Happy path + empty list clears.
	wf, err := SetWorkflow(ctx, fx.pool, fx.slug, fx.ident, fx.admin,
		[]WorkflowTransition{{FromStateID: fx.todo, ToStateID: fx.inProgress}})
	if err != nil {
		t.Fatalf("SetWorkflow: %v", err)
	}
	if len(wf.Transitions) != 1 {
		t.Fatalf("transitions = %d, want 1", len(wf.Transitions))
	}
	wf, err = SetWorkflow(ctx, fx.pool, fx.slug, fx.ident, fx.admin, nil)
	if err != nil {
		t.Fatalf("SetWorkflow clear: %v", err)
	}
	if len(wf.Transitions) != 0 {
		t.Fatalf("transitions after clear = %d, want 0", len(wf.Transitions))
	}
}

// TestGetWorkflow: returns the project's states plus the configured rules;
// a fresh project reports zero transitions (unrestricted).
func TestGetWorkflow(t *testing.T) {
	fx := setupWorkflowFixture(t)
	ctx := context.Background()

	wf, err := GetWorkflow(ctx, fx.pool, fx.slug, fx.ident, fx.member)
	if err != nil {
		t.Fatalf("GetWorkflow: %v", err)
	}
	if len(wf.States) != 5 {
		t.Fatalf("states = %d, want 5 seeded", len(wf.States))
	}
	if len(wf.Transitions) != 0 {
		t.Fatalf("transitions = %d, want 0 (fresh project unrestricted)", len(wf.Transitions))
	}

	fx.setRules(t, []WorkflowTransition{
		{FromStateID: fx.backlog, ToStateID: fx.todo},
		{FromStateID: fx.todo, ToStateID: fx.inProgress},
	})
	wf, err = GetWorkflow(ctx, fx.pool, fx.slug, fx.ident, fx.member)
	if err != nil {
		t.Fatalf("GetWorkflow after set: %v", err)
	}
	if len(wf.Transitions) != 2 {
		t.Fatalf("transitions = %d, want 2", len(wf.Transitions))
	}
}

// TestSetWorkflowReplaceSemantics: SetWorkflow replaces the whole rule set,
// never appends; duplicate pairs in one call are deduped, not 500s.
func TestSetWorkflowReplaceSemantics(t *testing.T) {
	fx := setupWorkflowFixture(t)
	ctx := context.Background()

	fx.setRules(t, []WorkflowTransition{
		{FromStateID: fx.todo, ToStateID: fx.inProgress},
		{FromStateID: fx.inProgress, ToStateID: fx.done},
	})
	wf, err := SetWorkflow(ctx, fx.pool, fx.slug, fx.ident, fx.admin,
		[]WorkflowTransition{
			{FromStateID: fx.backlog, ToStateID: fx.todo},
			{FromStateID: fx.backlog, ToStateID: fx.todo}, // duplicate pair
		})
	if err != nil {
		t.Fatalf("SetWorkflow replace: %v", err)
	}
	if len(wf.Transitions) != 1 {
		t.Fatalf("transitions = %d, want 1 (replaced + deduped)", len(wf.Transitions))
	}
	if wf.Transitions[0].FromStateID != fx.backlog || wf.Transitions[0].ToStateID != fx.todo {
		t.Fatalf("transitions = %+v, want only backlog→todo", wf.Transitions)
	}
}

// TestCheckTransitionMatrix: zero rules = unrestricted; with rules, only
// listed pairs pass; denial carries the allowed next-state IDs.
func TestCheckTransitionMatrix(t *testing.T) {
	fx := setupWorkflowFixture(t)
	ctx := context.Background()

	// checkTransitionTx is unexported; drive it through the exported
	// helper used by the call sites.
	allow := func(from, to string) error {
		return checkTransitionTx(ctx, fx.pool, fx.projectID(t), from, to)
	}

	// No rules: everything allowed (backwards compatible).
	if err := allow(fx.todo, fx.done); err != nil {
		t.Fatalf("unrestricted todo→done: %v", err)
	}

	fx.setRules(t, []WorkflowTransition{
		{FromStateID: fx.todo, ToStateID: fx.inProgress},
		{FromStateID: fx.todo, ToStateID: fx.done},
		{FromStateID: fx.inProgress, ToStateID: fx.done},
	})

	if err := allow(fx.todo, fx.inProgress); err != nil {
		t.Fatalf("allowed todo→in_progress: %v", err)
	}
	// Same-state "move" is not a transition.
	if err := allow(fx.todo, fx.todo); err != nil {
		t.Fatalf("same-state: %v", err)
	}
	// Not in the rules → sentinel + allowed list.
	err := allow(fx.todo, fx.cancelled)
	if !errors.Is(err, ErrStateTransitionNotAllowed) {
		t.Fatalf("todo→cancelled: err = %v, want ErrStateTransitionNotAllowed", err)
	}
	var ste *StateTransitionError
	if !errors.As(err, &ste) {
		t.Fatalf("todo→cancelled: err type %T, want *StateTransitionError", err)
	}
	if len(ste.Allowed) != 2 {
		t.Fatalf("allowed = %v, want 2 entries", ste.Allowed)
	}
	// Reverse direction not listed → denied even though the pair exists
	// in the other direction.
	if err := allow(fx.inProgress, fx.todo); !errors.Is(err, ErrStateTransitionNotAllowed) {
		t.Fatalf("in_progress→todo: err = %v, want ErrStateTransitionNotAllowed", err)
	}
	// From-state with no rules at all → denied, empty allowed list.
	err = allow(fx.backlog, fx.todo)
	if !errors.Is(err, ErrStateTransitionNotAllowed) {
		t.Fatalf("backlog→todo: err = %v, want ErrStateTransitionNotAllowed", err)
	}
	if errors.As(err, &ste) && len(ste.Allowed) != 0 {
		t.Fatalf("backlog allowed = %v, want empty", ste.Allowed)
	}
}

func (fx workflowFixture) projectID(t *testing.T) string {
	t.Helper()
	var id string
	if err := fx.pool.QueryRow(context.Background(),
		`SELECT p.id::text FROM projects p JOIN workspaces w ON w.id = p.workspace_id
		  WHERE w.slug = $1 AND p.identifier = $2`, fx.slug, strings.ToUpper(fx.ident)).Scan(&id); err != nil {
		t.Fatalf("project id: %v", err)
	}
	return id
}

// TestUpdateIssueEnforcesWorkflow: a denied move returns the sentinel, the
// issue row is untouched, and no activity row is written.
func TestUpdateIssueEnforcesWorkflow(t *testing.T) {
	fx := setupWorkflowFixture(t)
	ctx := context.Background()
	fx.setRules(t, []WorkflowTransition{{FromStateID: fx.todo, ToStateID: fx.inProgress}})

	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "governed")
	// Position the issue in todo BEFORE setting rules (fresh project =
	// unrestricted, so the positioning move is ungoverned).
	if _, err := SetWorkflow(ctx, fx.pool, fx.slug, fx.ident, fx.admin, nil); err != nil {
		t.Fatalf("clear rules: %v", err)
	}
	patch := IssuePatch{StateID: &fx.todo}
	moved, err := UpdateIssue(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin, patch)
	if err != nil {
		t.Fatalf("position in todo: %v", err)
	}
	if moved.StateID != fx.todo {
		t.Fatalf("state = %s, want todo", moved.StateID)
	}
	fx.setRules(t, []WorkflowTransition{{FromStateID: fx.todo, ToStateID: fx.inProgress}})

	before := countRows(t, fx.pool,
		`SELECT count(*) FROM issue_activities WHERE issue_id = $1::uuid`, iss.ID)

	denied := fx.done
	if _, err := UpdateIssue(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin,
		IssuePatch{StateID: &denied}); !errors.Is(err, ErrStateTransitionNotAllowed) {
		t.Fatalf("todo→done: err = %v, want ErrStateTransitionNotAllowed", err)
	}

	// No mutation: state unchanged, no new activity row.
	var stateID string
	if err := fx.pool.QueryRow(ctx,
		`SELECT state_id::text FROM issues WHERE id = $1::uuid`, iss.ID).Scan(&stateID); err != nil {
		t.Fatalf("re-read state: %v", err)
	}
	if stateID != fx.todo {
		t.Fatalf("state after denied move = %s, want still todo", stateID)
	}
	if n := countRows(t, fx.pool,
		`SELECT count(*) FROM issue_activities WHERE issue_id = $1::uuid`, iss.ID); n != before {
		t.Fatalf("activity rows = %d, want %d (denied move writes nothing)", n, before)
	}

	// The allowed move still works.
	allowed := fx.inProgress
	moved, err = UpdateIssue(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin,
		IssuePatch{StateID: &allowed})
	if err != nil {
		t.Fatalf("allowed todo→in_progress: %v", err)
	}
	if moved.StateID != fx.inProgress {
		t.Fatalf("state = %s, want in_progress", moved.StateID)
	}
}

// TestBulkUpdateIssuesWorkflowPerItem: a denied item reports a per-item
// error while the allowed items in the same batch still move.
func TestBulkUpdateIssuesWorkflowPerItem(t *testing.T) {
	fx := setupWorkflowFixture(t)
	ctx := context.Background()

	a := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "bulk-a")
	b := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "bulk-b")
	todo := fx.todo
	for _, id := range []string{a.ID, b.ID} {
		if _, err := UpdateIssue(ctx, fx.pool, fx.slug, fx.ident, id, fx.admin,
			IssuePatch{StateID: &todo}); err != nil {
			t.Fatalf("position in todo: %v", err)
		}
	}
	fx.setRules(t, []WorkflowTransition{{FromStateID: fx.todo, ToStateID: fx.inProgress}})

	done := fx.done
	results, err := BulkUpdateIssues(ctx, fx.pool, fx.slug, fx.ident, fx.admin,
		[]string{a.ID, b.ID}, IssuePatch{StateID: &done})
	if err != nil {
		t.Fatalf("BulkUpdateIssues: %v", err)
	}
	for _, r := range results {
		if r.OK {
			t.Fatalf("item %s unexpectedly OK", r.ID)
		}
		if r.Error == nil || *r.Error == "" {
			t.Fatalf("item %s has no error message", r.ID)
		}
	}
	// Neither moved.
	for _, id := range []string{a.ID, b.ID} {
		var stateID string
		if err := fx.pool.QueryRow(ctx,
			`SELECT state_id::text FROM issues WHERE id = $1::uuid`, id).Scan(&stateID); err != nil {
			t.Fatalf("re-read: %v", err)
		}
		if stateID != fx.todo {
			t.Fatalf("issue %s state = %s, want still todo", id, stateID)
		}
	}

	// Allowed batch move works for both.
	inProgress := fx.inProgress
	results, err = BulkUpdateIssues(ctx, fx.pool, fx.slug, fx.ident, fx.admin,
		[]string{a.ID, b.ID}, IssuePatch{StateID: &inProgress})
	if err != nil {
		t.Fatalf("BulkUpdateIssues allowed: %v", err)
	}
	for _, r := range results {
		if !r.OK {
			t.Fatalf("item %s not OK: %v", r.ID, r.Error)
		}
	}
}

// TestBulkSetIssuesWorkflowFinalState: the check applies to the FINAL state
// value (C46 discipline). A labels_changed automation cascade-moves the row
// after the requested move; the net (before → final) movement must be
// allowed, else the whole batch fails 409 and rolls back.
func TestBulkSetIssuesWorkflowFinalState(t *testing.T) {
	fx := setupWorkflowFixture(t)
	ctx := context.Background()

	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "bulkset-final")
	todo := fx.todo
	if _, err := UpdateIssue(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin,
		IssuePatch{StateID: &todo}); err != nil {
		t.Fatalf("position in todo: %v", err)
	}

	// Automation: any label change cascade-moves the issue to Done.
	label, err := CreateLabel(ctx, fx.pool, fx.slug, fx.ident, fx.admin,
		LabelInput{Name: "wf-label", Color: strPtr("#3b82f6")})
	if err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}
	doneID := fx.done
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin,
		AutomationRuleInput{
			Name:    "labels finish",
			Trigger: AutomationTrigger{Type: "issue.labels_changed"},
			Actions: []AutomationAction{{Type: "set_state", StateID: &doneID}},
		}); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	// Rules allow todo→in_progress only. The requested move is allowed,
	// but the cascade lands on done → net (todo→done) denied.
	fx.setRules(t, []WorkflowTransition{{FromStateID: fx.todo, ToStateID: fx.inProgress}})

	labelIDs := []string{label.ID}
	inProgress := fx.inProgress
	_, _, err = BulkSetIssues(ctx, fx.pool, fx.slug, fx.ident, fx.admin,
		[]string{iss.ID}, BulkIssueSet{StateID: &inProgress, LabelIDs: &labelIDs})
	if !errors.Is(err, ErrStateTransitionNotAllowed) {
		t.Fatalf("bulk-set with violating cascade: err = %v, want ErrStateTransitionNotAllowed", err)
	}

	// Rolled back: still in todo, label not attached.
	var stateID string
	if err := fx.pool.QueryRow(ctx,
		`SELECT state_id::text FROM issues WHERE id = $1::uuid`, iss.ID).Scan(&stateID); err != nil {
		t.Fatalf("re-read state: %v", err)
	}
	if stateID != fx.todo {
		t.Fatalf("state after denied bulk-set = %s, want still todo", stateID)
	}
	if n := countRows(t, fx.pool,
		`SELECT count(*) FROM issue_labels WHERE issue_id = $1::uuid`, iss.ID); n != 0 {
		t.Fatalf("labels attached = %d, want 0 (rolled back)", n)
	}
}

// sameWorkspaceMoveFixtures builds two projects in ONE workspace for move
// tests (MoveIssue requires both projects in the same workspace).
func sameWorkspaceMoveFixtures(t *testing.T) (pool *pgxpool.Pool, slug, srcIdent, dstIdent, admin string, srcStates, dstStates map[string]string) {
	t.Helper()
	pool = newTestPool(t)
	ctx := context.Background()
	admin = createTestUser(t, pool, uniqueTestEmail("mv-admin"))
	slug = uniqueTestSlug("mv")
	createTestWorkspace(t, pool, "MV Corp", slug, admin)
	srcIdent = uniqueTestIdent()
	dstIdent = uniqueTestIdent()
	if _, err := CreateProject(ctx, pool, slug, admin, "Source", srcIdent); err != nil {
		t.Fatalf("CreateProject src: %v", err)
	}
	if _, err := CreateProject(ctx, pool, slug, admin, "Dest", dstIdent); err != nil {
		t.Fatalf("CreateProject dst: %v", err)
	}
	byGroup := func(ident string) map[string]string {
		t.Helper()
		states, err := ListStates(ctx, pool, slug, ident, admin)
		if err != nil {
			t.Fatalf("ListStates: %v", err)
		}
		m := map[string]string{}
		for _, s := range states {
			m[s.Group] = s.ID
		}
		return m
	}
	return pool, slug, srcIdent, dstIdent, admin, byGroup(srcIdent), byGroup(dstIdent)
}

func TestMoveIssueWorkflowEnforcement(t *testing.T) {
	pool, slug, srcIdent, dstIdent, admin, srcStates, dstStates := sameWorkspaceMoveFixtures(t)
	ctx := context.Background()

	// Target rules: backlog→todo only.
	if _, err := SetWorkflow(ctx, pool, slug, dstIdent, admin,
		[]WorkflowTransition{{FromStateID: dstStates["backlog"], ToStateID: dstStates["unstarted"]}}); err != nil {
		t.Fatalf("SetWorkflow dst: %v", err)
	}

	move := func(issueID string) error {
		_, err := MoveIssue(ctx, pool, slug, srcIdent, issueID, admin,
			MoveIssueInput{TargetProjectIdentifier: dstIdent})
		return err
	}
	position := func(issueID, stateID string) {
		t.Helper()
		if _, err := UpdateIssue(ctx, pool, slug, srcIdent, issueID, admin,
			IssuePatch{StateID: &stateID}); err != nil {
			t.Fatalf("position: %v", err)
		}
	}

	// Issue in Done → lands in target "Done" (name-matched):
	// (dstBacklog → dstDone) not allowed → 409, nothing moved.
	issDone := createTestIssue(t, pool, slug, srcIdent, admin, "mv-done")
	position(issDone.ID, srcStates["completed"])
	if err := move(issDone.ID); !errors.Is(err, ErrStateTransitionNotAllowed) {
		t.Fatalf("move landing in done: err = %v, want ErrStateTransitionNotAllowed", err)
	}
	var proj string
	if err := pool.QueryRow(ctx,
		`SELECT project_id::text FROM issues WHERE id = $1::uuid`, issDone.ID).Scan(&proj); err != nil {
		t.Fatalf("re-read project: %v", err)
	}
	var srcProj string
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM projects WHERE workspace_id = (SELECT id FROM workspaces WHERE slug = $1)
		  AND identifier = $2`, slug, strings.ToUpper(srcIdent)).Scan(&srcProj); err != nil {
		t.Fatalf("src project id: %v", err)
	}
	if proj != srcProj {
		t.Fatalf("issue moved despite denial")
	}

	// Issue in Todo → lands in target "Todo" (name-matched):
	// (dstBacklog → dstTodo) IS allowed → move succeeds.
	issTodo := createTestIssue(t, pool, slug, srcIdent, admin, "mv-todo")
	position(issTodo.ID, srcStates["unstarted"])
	if err := move(issTodo.ID); err != nil {
		t.Fatalf("move landing in todo: %v", err)
	}

	// No rules on target → unrestricted (backwards compatible).
	if _, err := SetWorkflow(ctx, pool, slug, dstIdent, admin, nil); err != nil {
		t.Fatalf("clear dst rules: %v", err)
	}
	issDone2 := createTestIssue(t, pool, slug, srcIdent, admin, "mv-done2")
	position(issDone2.ID, srcStates["completed"])
	if err := move(issDone2.ID); err != nil {
		t.Fatalf("move with no rules: %v", err)
	}
}

// TestDeleteStateCascadesWorkflowRules: deleting a state drops the rule
// rows that mention it (ON DELETE CASCADE) — no dangling rules.
func TestDeleteStateCascadesWorkflowRules(t *testing.T) {
	fx := setupWorkflowFixture(t)
	ctx := context.Background()
	fx.setRules(t, []WorkflowTransition{
		{FromStateID: fx.todo, ToStateID: fx.inProgress},
		{FromStateID: fx.inProgress, ToStateID: fx.done},
	})

	// in_progress is unused (no issues) → plain delete.
	if err := DeleteState(ctx, fx.pool, fx.slug, fx.ident, fx.inProgress, fx.admin, nil); err != nil {
		t.Fatalf("DeleteState: %v", err)
	}
	wf, err := GetWorkflow(ctx, fx.pool, fx.slug, fx.ident, fx.admin)
	if err != nil {
		t.Fatalf("GetWorkflow: %v", err)
	}
	if len(wf.Transitions) != 0 {
		t.Fatalf("transitions after delete = %+v, want none (both rows referenced the deleted state)", wf.Transitions)
	}
}

// TestIntakeAcceptBypassesWorkflow: the system accept path (triage →
// backlog) is NOT governed by transition rules.
func TestIntakeAcceptBypassesWorkflow(t *testing.T) {
	fx := setupWorkflowFixture(t)
	ctx := context.Background()

	// Rules that would deny (triage → backlog): only todo→in_progress.
	fx.setRules(t, []WorkflowTransition{{FromStateID: fx.todo, ToStateID: fx.inProgress}})

	// Intake issue created in a non-backlog state so accept has a move to do.
	started := fx.inProgress
	iss, err := CreateIssue(ctx, fx.pool, fx.slug, fx.ident, fx.admin,
		CreateIssueInput{Name: "intake bypass", StateID: &started, Intake: true})
	if err != nil {
		t.Fatalf("CreateIssue intake: %v", err)
	}
	if _, err := AcceptIntakeIssue(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin); err != nil {
		t.Fatalf("AcceptIntakeIssue with rules: %v (system path must bypass)", err)
	}
	var stateID string
	if err := fx.pool.QueryRow(ctx,
		`SELECT state_id::text FROM issues WHERE id = $1::uuid`, iss.ID).Scan(&stateID); err != nil {
		t.Fatalf("re-read state: %v", err)
	}
	if stateID != fx.backlog {
		t.Fatalf("accepted state = %s, want backlog", stateID)
	}
}

// TestRestoreIssueVersionBypassesWorkflow: restoring a version that moves
// the state backwards is NOT governed by transition rules.
func TestRestoreIssueVersionBypassesWorkflow(t *testing.T) {
	fx := setupWorkflowFixture(t)
	ctx := context.Background()

	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "restorable")
	todo := fx.todo
	if _, err := UpdateIssue(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin,
		IssuePatch{StateID: &todo}); err != nil {
		t.Fatalf("position in todo: %v", err)
	}
	inProgress := fx.inProgress
	if _, err := UpdateIssue(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin,
		IssuePatch{StateID: &inProgress}); err != nil {
		t.Fatalf("move to in_progress: %v", err)
	}
	// v1 = backlog, v2 = todo, v3 = in_progress. Rules allow only
	// todo→in_progress, so restoring v2 (in_progress→todo) would be
	// denied if governed.
	fx.setRules(t, []WorkflowTransition{{FromStateID: fx.todo, ToStateID: fx.inProgress}})

	restored, err := RestoreIssueVersion(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin, 2)
	if err != nil {
		t.Fatalf("RestoreIssueVersion with rules: %v (system path must bypass)", err)
	}
	if restored.StateID != fx.todo {
		t.Fatalf("restored state = %s, want todo", restored.StateID)
	}
}
