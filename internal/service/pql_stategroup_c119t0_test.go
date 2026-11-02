package service

// `state_group` PQL field, project scope (C119T0): a
// CLOSED-VOCABULARY field, translated without any DB name
// resolution — the priority translator's shape, not the
// name fields'. `=` and `!=`; case-insensitive values
// normalized to lowercase; unknown, triage, and none
// values are 400s with position (none's message states
// every issue belongs to a state group, never a silent
// empty list); other operators are rejected; the field is
// listed in pqlKnownFields. Workspace scope (MyWork)
// inherits the same translator with no separate path —
// see TestMyWorkPQLStateGroup in this file's companion
// assertions below via the shared translatePQLCmp case.
// Follows the pql_createdby_c118t0_test.go idioms; real
// test database, no skips.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPQLServiceStateGroupField(t *testing.T) {
	s, _ := setupStateGroupFixture(t)

	pqlWantNames(t, pqlListNames(t, s, `state_group = started`), "in-started")
	pqlWantNames(t, pqlListNames(t, s, `state_group = backlog`), "in-backlog")
	pqlWantNames(t, pqlListNames(t, s, `state_group = Started`), "in-started") // case-insensitive
	pqlWantNames(t, pqlListNames(t, s, `state_group = STARTED`), "in-started")
	pqlWantNames(t, pqlListNames(t, s, `state_group != started`), "in-backlog", "in-unstarted", "in-completed", "in-cancelled")
	pqlWantNames(t, pqlListNames(t, s, `state_group != "completed"`), "in-backlog", "in-unstarted", "in-started", "in-cancelled")
	// Composition with a name field and with priority.
	pqlWantNames(t, pqlListNames(t, s, `state_group = started AND priority >= 0`), "in-started")
	pqlWantNames(t, pqlListNames(t, s, `state_group = started AND state = "In Progress"`), "in-started")
	pqlWantNames(t, pqlListNames(t, s, `state_group = started AND state = Todo`))
}

func TestPQLServiceStateGroupErrors(t *testing.T) {
	s := setupListTest(t)
	pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "A", CreateIssueInput{})

	for _, tc := range []struct {
		query string
		want  string
	}{
		{`state_group = bogus`, `unknown state_group "bogus"`},
		{`state_group = triage`, `unknown state_group "triage"`},
		{`state_group = none`, `every issue belongs to a state group`},
		{`state_group != none`, `every issue belongs to a state group`},
		{`state_group in (started)`, `operator in is not supported for field "state_group"`},
		{`state_group > started`, `operator > is not supported for field "state_group"`},
		{`foo = Bar`, "state_group"}, // the known-fields list now names the field
	} {
		_, err := ListIssues(context.Background(), s.pool, s.slug, s.ident, s.actor,
			ListIssuesInput{PQL: tc.query, PerPage: 10})
		if err == nil {
			t.Errorf("PQL %q: want error, got nil", tc.query)
			continue
		}
		var pqlErr *PQLQueryError
		if !errors.As(err, &pqlErr) {
			t.Errorf("PQL %q: error type = %T, want *PQLQueryError", tc.query, err)
			continue
		}
		if pqlErr.Pos <= 0 {
			t.Errorf("PQL %q: position = %d, want > 0", tc.query, pqlErr.Pos)
		}
		if !strings.Contains(pqlErr.Error(), tc.want) {
			t.Errorf("PQL %q: error = %v, want substring %q", tc.query, pqlErr, tc.want)
		}
	}

	// pqlKnownFields lists the field verbatim.
	if !strings.Contains(pqlKnownFields, "state_group") {
		t.Fatalf("pqlKnownFields = %q, want it to list state_group", pqlKnownFields)
	}
}

func TestMyWorkPQLStateGroup(t *testing.T) {
	pool := newTestPool(t)
	slug, identA, _, _, alice, bob := myworkPQLFixture(t, pool)

	startedA := pqlStateID(t, pool, slug, identA, alice, "In Progress")
	backlogA := pqlStateID(t, pool, slug, identA, alice, "Backlog")

	// Assigned set (Bob): one started, one backlog issue.
	s1 := pqlCreateIssue(t, pool, slug, identA, alice, "SG-Started", CreateIssueInput{StateID: &startedA})
	b1 := pqlCreateIssue(t, pool, slug, identA, alice, "SG-Backlog", CreateIssueInput{StateID: &backlogA})
	for _, iss := range []*Issue{s1, b1} {
		assignIssueTo(t, pool, iss.ID, bob)
	}

	// Workspace scope shares the project-scope translator:
	// no separate MyWork-only path, no DB name resolution.
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `state_group = started`), "SG-Started")
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `state_group != started`), "SG-Backlog")
	myworkPQLWantErr(t, pool, slug, bob, `state_group = none`, `every issue belongs to a state group`)
	myworkPQLWantErr(t, pool, slug, bob, `state_group = triage`, `unknown state_group "triage"`)
}
