package service

// C11T1 workflow automations: service + execution-path tests.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

type automationFixture struct {
	pool      *pgxpool.Pool
	slug      string
	ident     string
	admin     string
	member    string
	guest     string
	backlog   string // state id
	started   string // state id (another group for transitions)
	completed string // state id (a third group)
	labelID   string
}

func uniqueTestIdent() string {
	return fmt.Sprintf("a%x%x", os.Getpid(), testSeq.Add(1))
}

func setupAutomationFixture(t *testing.T) automationFixture {
	t.Helper()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	admin := createTestUser(t, pool, uniqueTestEmail("auto-admin"))
	member := createTestUser(t, pool, uniqueTestEmail("auto-member"))
	guest := createTestUser(t, pool, uniqueTestEmail("auto-guest"))
	slug := uniqueTestSlug("auto")
	createTestWorkspace(t, pool, "Auto Corp", slug, admin)
	if err := UpsertMember(ctx, pool, slug, admin, member, RoleMember); err != nil {
		t.Fatalf("UpsertMember member: %v", err)
	}
	if err := UpsertMember(ctx, pool, slug, admin, guest, RoleGuest); err != nil {
		t.Fatalf("UpsertMember guest: %v", err)
	}
	ident := uniqueTestIdent()
	if _, err := CreateProject(ctx, pool, slug, admin, "Auto Project", ident); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	states, err := ListStates(ctx, pool, slug, ident, admin)
	if err != nil {
		t.Fatalf("ListStates: %v", err)
	}
	var backlog, started, completed string
	for _, s := range states {
		switch s.Group {
		case "backlog":
			backlog = s.ID
		case "started":
			started = s.ID
		case "completed":
			completed = s.ID
		}
	}
	if backlog == "" || started == "" || completed == "" {
		t.Fatal("seeded states missing backlog/started/completed groups")
	}
	label, err := CreateLabel(ctx, pool, slug, ident, admin, LabelInput{Name: "auto-label", Color: autoStrPtr("#3b82f6")})
	if err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}
	return automationFixture{pool, slug, ident, admin, member, guest, backlog, started, completed, label.ID}
}

func automationRuleInput(fx automationFixture, toStates []string, actions []AutomationAction) AutomationRuleInput {
	return AutomationRuleInput{
		Name:    "auto rule",
		Trigger: AutomationTrigger{Type: "issue.state_changed", ToStates: toStates},
		Actions: actions,
	}
}

func autoStrPtr(s string) *string { return &s }

func countRows(t *testing.T, pool *pgxpool.Pool, q string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("countRows %q: %v", q, err)
	}
	return n
}

func TestAutomationCRUD(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	in := automationRuleInput(fx, []string{fx.started}, []AutomationAction{
		{Type: "assign", UserID: autoStrPtr(fx.member)},
		{Type: "add_label", LabelID: autoStrPtr(fx.labelID)},
		{Type: "add_comment", Body: autoStrPtr("moved!")},
	})
	r, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in)
	if err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	if r.ID == "" || !r.Enabled || r.CreatedBy != fx.admin {
		t.Fatalf("rule = %+v, want id/enabled/created_by", r)
	}

	rules, err := ListAutomationRules(ctx, fx.pool, fx.slug, fx.ident, fx.member)
	if err != nil {
		t.Fatalf("ListAutomationRules: %v", err)
	}
	if len(rules) != 1 || rules[0].ID != r.ID {
		t.Fatalf("list = %d rules, want 1 with id %s", len(rules), r.ID)
	}

	// Non-member read: 404-shaped.
	outsider := createTestUser(t, fx.pool, uniqueTestEmail("auto-out"))
	if _, err := ListAutomationRules(ctx, fx.pool, fx.slug, fx.ident, outsider); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider list err = %v, want ErrNotFound", err)
	}
	// Guest read: forbidden.
	if _, err := ListAutomationRules(ctx, fx.pool, fx.slug, fx.ident, fx.guest); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest list err = %v, want ErrForbidden", err)
	}
	// Guest write: forbidden (acceptance: non-maintainer write 403).
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.guest, in); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest create err = %v, want ErrForbidden", err)
	}

	// PATCH: disable + rename.
	disabled := false
	name := "renamed"
	updated, err := UpdateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, r.ID, fx.member,
		AutomationRulePatch{Name: &name, Enabled: &disabled})
	if err != nil {
		t.Fatalf("UpdateAutomationRule: %v", err)
	}
	if updated.Name != "renamed" || updated.Enabled {
		t.Fatalf("patched = %+v, want renamed/disabled", updated)
	}
	// Empty patch: nothing to update.
	if _, err := UpdateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, r.ID, fx.member,
		AutomationRulePatch{}); !errors.Is(err, ErrNothingToUpdate) {
		t.Fatalf("empty patch err = %v, want ErrNothingToUpdate", err)
	}
	// Missing rule: 404-shaped.
	if _, err := UpdateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, "00000000-0000-0000-0000-000000000000", fx.member,
		AutomationRulePatch{Enabled: &disabled}); !errors.Is(err, ErrAutomationRuleNotFound) {
		t.Fatalf("update missing err = %v, want ErrAutomationRuleNotFound", err)
	}

	if err := DeleteAutomationRule(ctx, fx.pool, fx.slug, fx.ident, r.ID, fx.member); err != nil {
		t.Fatalf("DeleteAutomationRule: %v", err)
	}
	if err := DeleteAutomationRule(ctx, fx.pool, fx.slug, fx.ident, r.ID, fx.member); !errors.Is(err, ErrAutomationRuleNotFound) {
		t.Fatalf("delete missing err = %v, want ErrAutomationRuleNotFound", err)
	}
	rules, err = ListAutomationRules(ctx, fx.pool, fx.slug, fx.ident, fx.admin)
	if err != nil || len(rules) != 0 {
		t.Fatalf("list after delete = %d, err = %v; want 0", len(rules), err)
	}
}

func TestAutomationValidation(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()
	other := createTestUser(t, fx.pool, uniqueTestEmail("auto-other")) // not a member

	cases := []struct {
		name string
		in   AutomationRuleInput
		want error
	}{
		{"blank name", AutomationRuleInput{Trigger: AutomationTrigger{Type: "issue.state_changed"},
			Actions: []AutomationAction{{Type: "add_comment", Body: autoStrPtr("x")}}}, ErrNameRequired},
		{"bad trigger type", automationRuleInput(fx, nil, []AutomationAction{{Type: "add_comment", Body: autoStrPtr("x")}}), ErrInvalidAutomationTrigger},
		{"empty actions", automationRuleInput(fx, nil, nil), ErrInvalidAutomationAction},
		{"unknown action type", automationRuleInput(fx, nil, []AutomationAction{{Type: "teleport"}}), ErrInvalidAutomationAction},
		{"assign non-member", automationRuleInput(fx, nil, []AutomationAction{{Type: "assign", UserID: autoStrPtr(other)}}), ErrInvalidAutomationAction},
		{"assign missing user", automationRuleInput(fx, nil, []AutomationAction{{Type: "assign"}}), ErrInvalidAutomationAction},
		{"label unknown", automationRuleInput(fx, nil, []AutomationAction{{Type: "add_label", LabelID: autoStrPtr("00000000-0000-0000-0000-000000000000")}}), ErrInvalidAutomationAction},
		{"comment blank", automationRuleInput(fx, nil, []AutomationAction{{Type: "add_comment", Body: autoStrPtr("  ")}}), ErrInvalidAutomationAction},
	}
	// C12T2: issue.created is now a valid trigger; use a genuinely
	// unknown type for the bad-trigger case.
	cases[1].in.Trigger.Type = "issue.deleted"
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, tc.in); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}

	// Unknown state UUID in the trigger filter.
	bad := automationRuleInput(fx, []string{"00000000-0000-0000-0000-000000000000"},
		[]AutomationAction{{Type: "add_comment", Body: autoStrPtr("x")}})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, bad); !errors.Is(err, ErrInvalidAutomationTrigger) {
		t.Fatalf("bad state filter err = %v, want ErrInvalidAutomationTrigger", err)
	}
}

func TestAutomationRuleLimit(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()
	for i := 0; i < MaxAutomationRulesPerProject; i++ {
		in := automationRuleInput(fx, nil, []AutomationAction{{Type: "add_comment", Body: autoStrPtr("x")}})
		in.Name = fmt.Sprintf("rule %d", i)
		if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	in := automationRuleInput(fx, nil, []AutomationAction{{Type: "add_comment", Body: autoStrPtr("x")}})
	in.Name = "rule 26"
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); !errors.Is(err, ErrAutomationRuleLimit) {
		t.Fatalf("26th rule err = %v, want ErrAutomationRuleLimit", err)
	}
}

// moveIssueToState patches the issue's state via the real service path.
func moveIssueToState(t *testing.T, fx automationFixture, issueID, actorID, stateID string) {
	t.Helper()
	patch := IssuePatch{StateID: &stateID}
	if _, err := UpdateIssue(context.Background(), fx.pool, fx.slug, fx.ident, issueID, actorID, patch); err != nil {
		t.Fatalf("UpdateIssue state move: %v", err)
	}
}

func TestAutomationFiresOnMatchingTransition(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	in := automationRuleInput(fx, []string{fx.started}, []AutomationAction{
		{Type: "assign", UserID: autoStrPtr(fx.member)},
		{Type: "add_label", LabelID: autoStrPtr(fx.labelID)},
		{Type: "add_comment", Body: autoStrPtr("auto: moved to started")},
	})
	in.Name = "on start"
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "auto fire")
	moveIssueToState(t, fx, iss.ID, fx.admin, fx.started)

	if n := countRows(t, fx.pool, `SELECT count(*) FROM issue_assignees WHERE issue_id = $1::uuid AND user_id = $2::uuid`, iss.ID, fx.member); n != 1 {
		t.Fatalf("assignees = %d, want 1", n)
	}
	if n := countRows(t, fx.pool, `SELECT count(*) FROM issue_labels WHERE issue_id = $1::uuid AND label_id = $2::uuid`, iss.ID, fx.labelID); n != 1 {
		t.Fatalf("labels = %d, want 1", n)
	}
	if n := countRows(t, fx.pool, `SELECT count(*) FROM comments WHERE issue_id = $1::uuid AND deleted_at IS NULL`, iss.ID); n != 1 {
		t.Fatalf("comments = %d, want 1", n)
	}
	// The automation comment is attributable and renders as plain text.
	var content string
	if err := fx.pool.QueryRow(ctx,
		`SELECT content::text FROM comments WHERE issue_id = $1::uuid AND deleted_at IS NULL`, iss.ID).Scan(&content); err != nil {
		t.Fatalf("comment content: %v", err)
	}
	if !strings.Contains(content, "auto: moved to started") {
		t.Fatalf("comment content = %s, want the rule body", content)
	}
}

func TestAutomationFiltersRespected(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	// Rule A: from backlog only. Rule B: to unstarted... use to_states
	// that never match (backlog) while we move backlog -> started.
	inA := automationRuleInput(fx, nil, []AutomationAction{{Type: "add_comment", Body: autoStrPtr("A")}})
	inA.Name = "rule A"
	inA.Trigger.FromStates = []string{fx.backlog}
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, inA); err != nil {
		t.Fatalf("rule A: %v", err)
	}
	inB := automationRuleInput(fx, []string{fx.backlog}, []AutomationAction{{Type: "add_comment", Body: autoStrPtr("B")}})
	inB.Name = "rule B"
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, inB); err != nil {
		t.Fatalf("rule B: %v", err)
	}
	// Rule C: from started (wrong from) — must stay silent.
	inC := automationRuleInput(fx, nil, []AutomationAction{{Type: "add_comment", Body: autoStrPtr("C")}})
	inC.Name = "rule C"
	inC.Trigger.FromStates = []string{fx.started}
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, inC); err != nil {
		t.Fatalf("rule C: %v", err)
	}

	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "auto filters")
	moveIssueToState(t, fx, iss.ID, fx.admin, fx.started)

	// A fires (from backlog ✓, to any ✓). B is silent (to backlog ✗).
	// C is silent (from started ✗).
	var bodies []string
	rows, err := fx.pool.Query(ctx,
		`SELECT content::text FROM comments WHERE issue_id = $1::uuid AND deleted_at IS NULL`, iss.ID)
	if err != nil {
		t.Fatalf("comments: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatalf("scan: %v", err)
		}
		bodies = append(bodies, c)
	}
	if len(bodies) != 1 || !strings.Contains(bodies[0], `"A"`) {
		t.Fatalf("comments = %v, want exactly the A comment", bodies)
	}
}

func TestAutomationNonMatchingTransitionSilent(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	in := automationRuleInput(fx, []string{fx.started}, []AutomationAction{
		{Type: "add_comment", Body: autoStrPtr("should not fire")},
	})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "auto silent")

	// A non-state patch (rename) must not fire anything.
	rename := "renamed"
	if _, err := UpdateIssue(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin, IssuePatch{Name: &rename}); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if n := countRows(t, fx.pool, `SELECT count(*) FROM comments WHERE issue_id = $1::uuid AND deleted_at IS NULL`, iss.ID); n != 0 {
		t.Fatalf("comments after rename = %d, want 0", n)
	}

	// A real state change to a non-matching state must not fire either.
	moveIssueToState(t, fx, iss.ID, fx.admin, fx.completed)
	if n := countRows(t, fx.pool, `SELECT count(*) FROM comments WHERE issue_id = $1::uuid AND deleted_at IS NULL`, iss.ID); n != 0 {
		t.Fatalf("comments after non-matching move = %d, want 0", n)
	}
}

func TestAutomationDisabledRuleSilent(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	in := automationRuleInput(fx, []string{fx.started}, []AutomationAction{
		{Type: "add_comment", Body: autoStrPtr("disabled should not fire")},
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
	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "auto disabled")
	moveIssueToState(t, fx, iss.ID, fx.admin, fx.started)
	if n := countRows(t, fx.pool, `SELECT count(*) FROM comments WHERE issue_id = $1::uuid AND deleted_at IS NULL`, iss.ID); n != 0 {
		t.Fatalf("comments = %d, want 0 (rule disabled)", n)
	}
}

func TestAutomationLoopGuard(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	// A rule whose only action is add_comment: if automation writes
	// re-triggered evaluation, this would cascade (each comment is a
	// write in the same tx). Exactly one comment must exist.
	in := automationRuleInput(fx, nil, []AutomationAction{
		{Type: "add_comment", Body: autoStrPtr("loop?")},
	})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "auto loop")
	moveIssueToState(t, fx, iss.ID, fx.admin, fx.started)
	if n := countRows(t, fx.pool, `SELECT count(*) FROM comments WHERE issue_id = $1::uuid AND deleted_at IS NULL`, iss.ID); n != 1 {
		t.Fatalf("comments = %d, want exactly 1 (loop guard)", n)
	}
}

func TestAutomationActionFailureDoesNotRollBack(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	in := automationRuleInput(fx, []string{fx.started}, []AutomationAction{
		{Type: "assign", UserID: autoStrPtr(fx.member)},
	})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	// The assignee leaves the workspace between rule creation and the
	// state change: the action fails (logged), the move must commit.
	if err := RemoveMember(ctx, fx.pool, fx.slug, fx.admin, fx.member); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "auto failsoft")
	// Must not error: the state change commits despite the broken action.
	moveIssueToState(t, fx, iss.ID, fx.admin, fx.started)

	got, err := GetIssue(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.StateID != fx.started {
		t.Fatalf("state = %s, want %s (move must commit)", got.StateID, fx.started)
	}
	if n := countRows(t, fx.pool, `SELECT count(*) FROM issue_assignees WHERE issue_id = $1::uuid`, iss.ID); n != 0 {
		t.Fatalf("assignees = %d, want 0 (action failed softly)", n)
	}
}
