package service

// C80T0: per-field is-empty due-date filter (?due_date_empty=1 ↔
// ListIssuesInput.DueDateEmpty). Semantics pinned here: target_date IS
// NULL — the due-date field alone, ANDed with every other filter. An
// issue with a start date but no target date still matches (only the
// target column is checked); `undated` (both columns NULL) is the
// narrower cousin, covered by TestListIssuesStartDateAndUndated. Real
// test database, no skips.

import (
	"context"
	"testing"
	"time"
)

// TestListIssuesDueDateEmpty pins the due_date_empty semantics: it
// returns issues whose target date is NULL, regardless of the start
// date, and excludes issues with a target date.
func TestListIssuesDueDateEmpty(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	creator := createTestUser(t, pool, uniqueTestEmail("due-empty"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("due-empty"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())

	oct5 := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	oct12 := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	mk := func(name string, start, target *time.Time) string {
		iss, err := CreateIssue(ctx, pool, ws.Slug, p.Identifier, creator,
			CreateIssueInput{Name: name, StartDate: start, TargetDate: target})
		if err != nil {
			t.Fatalf("CreateIssue %s: %v", name, err)
		}
		return iss.ID
	}

	noDueA := mk("no due A", nil, nil)
	noDueB := mk("no due B", nil, nil)
	startOnly := mk("start only", &oct5, nil) // start date, no due date — still matches
	dueOnly := mk("due only", nil, &oct12)

	ids := func(in ListIssuesInput) map[string]bool {
		res, err := ListIssues(ctx, pool, ws.Slug, p.Identifier, creator, in)
		if err != nil {
			t.Fatalf("ListIssues %+v: %v", in, err)
		}
		m := map[string]bool{}
		for _, it := range res.Issues {
			m[it.ID] = true
		}
		return m
	}

	// Default (flag off): everything, no behavior change.
	all := ids(ListIssuesInput{})
	if len(all) != 4 {
		t.Fatalf("default list = %d issues, want 4", len(all))
	}

	byDueEmpty := ids(ListIssuesInput{DueDateEmpty: true})
	if len(byDueEmpty) != 3 || !byDueEmpty[noDueA] || !byDueEmpty[noDueB] || !byDueEmpty[startOnly] {
		t.Errorf("due_date_empty: got %v, want the two undated + the start-only issue", byDueEmpty)
	}
	if byDueEmpty[dueOnly] {
		t.Errorf("due_date_empty: matched the due-dated issue, want target_date IS NULL only")
	}
}

// TestListIssuesDueDateEmptyComposes pins that due_date_empty ANDs with
// the other filters (state=, assignee=), like every other list filter.
func TestListIssuesDueDateEmptyComposes(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	creator := createTestUser(t, pool, uniqueTestEmail("due-cmp"))
	teammate := createTestUser(t, pool, uniqueTestEmail("due-cmp-mate"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("due-cmp"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())
	if err := UpsertMember(ctx, pool, ws.Slug, creator, teammate, RoleMember); err != nil {
		t.Fatalf("add teammate: %v", err)
	}
	doneState := stateIDByGroup(t, pool, p.ID, "completed")

	oct12 := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	mk := func(name string, target *time.Time) string {
		iss, err := CreateIssue(ctx, pool, ws.Slug, p.Identifier, creator,
			CreateIssueInput{Name: name, TargetDate: target})
		if err != nil {
			t.Fatalf("CreateIssue %s: %v", name, err)
		}
		return iss.ID
	}
	setState := func(id, stateID string) {
		if _, err := UpdateIssue(ctx, pool, ws.Slug, p.Identifier, id, creator, IssuePatch{StateID: &stateID}); err != nil {
			t.Fatalf("set state on %s: %v", id, err)
		}
	}

	// Matrix: due-empty × state × assignee.
	noDueBacklog := mk("no due, backlog", nil)
	noDueDoneAssigned := mk("no due, done, assigned", nil)
	setState(noDueDoneAssigned, doneState)
	if err := AssignAssignee(ctx, pool, ws.Slug, p.Identifier, noDueDoneAssigned, teammate, creator); err != nil {
		t.Fatalf("assign: %v", err)
	}
	dueDoneAssigned := mk("due, done, assigned", &oct12)
	setState(dueDoneAssigned, doneState)
	if err := AssignAssignee(ctx, pool, ws.Slug, p.Identifier, dueDoneAssigned, teammate, creator); err != nil {
		t.Fatalf("assign: %v", err)
	}
	dueBacklog := mk("due, backlog", &oct12)
	_ = dueBacklog

	ids := func(in ListIssuesInput) map[string]bool {
		res, err := ListIssues(ctx, pool, ws.Slug, p.Identifier, creator, in)
		if err != nil {
			t.Fatalf("ListIssues %+v: %v", in, err)
		}
		m := map[string]bool{}
		for _, it := range res.Issues {
			m[it.ID] = true
		}
		return m
	}

	// due_date_empty + state=: only no-due issues in the completed state.
	byState := ids(ListIssuesInput{DueDateEmpty: true, State: doneState})
	if len(byState) != 1 || !byState[noDueDoneAssigned] {
		t.Errorf("due_date_empty + state: got %v, want only the no-due completed issue", byState)
	}

	// due_date_empty + assignee=: only no-due issues assigned to the teammate.
	byAssignee := ids(ListIssuesInput{DueDateEmpty: true, Assignees: []string{teammate}})
	if len(byAssignee) != 1 || !byAssignee[noDueDoneAssigned] {
		t.Errorf("due_date_empty + assignee: got %v, want only the no-due assigned issue", byAssignee)
	}

	// All three together: still the one issue.
	byBoth := ids(ListIssuesInput{DueDateEmpty: true, State: doneState, Assignees: []string{teammate}})
	if len(byBoth) != 1 || !byBoth[noDueDoneAssigned] {
		t.Errorf("due_date_empty + state + assignee: got %v, want only the no-due completed assigned issue", byBoth)
	}

	// And the due_date_empty filter alone over the matrix returns both
	// no-due issues (the backlog one too).
	byDueEmpty := ids(ListIssuesInput{DueDateEmpty: true})
	if len(byDueEmpty) != 2 || !byDueEmpty[noDueBacklog] || !byDueEmpty[noDueDoneAssigned] {
		t.Errorf("due_date_empty alone: got %v, want both no-due issues", byDueEmpty)
	}
}
