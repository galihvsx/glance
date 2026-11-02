package service

// `project` PQL field (C114T0): workspace-scoped resolution and
// project-scope rejection, on the real test database, no skips.
// Reuses the C112 MyWork fixture (two public projects A + B, one
// private project P, Bob a plain workspace Member) and its helpers.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestMyWorkPQLProjectField(t *testing.T) {
	pool := newTestPool(t)
	slug, identA, identB, identP, alice, bob := myworkPQLFixture(t, pool)

	// Two further public projects sharing one name, for the
	// multi-project name-set case, plus a project NAMED identA, for
	// the identifier-wins-over-name case.
	shared1 := createTestProject(t, pool, slug, alice, "Shared", uniqueTestIdentifier())
	shared2 := createTestProject(t, pool, slug, alice, "Shared", uniqueTestIdentifier())
	shadow := createTestProject(t, pool, slug, alice, identA, uniqueTestIdentifier())

	todoA := pqlStateID(t, pool, slug, identA, alice, "Todo")
	doneA := pqlStateID(t, pool, slug, identA, alice, "Done")

	// Assigned set (Bob): PA1 Todo/A, PA2 Done/A, PB1/B, PS1/Shared-1,
	// PS2/Shared-2, and PShadow in the project named identA.
	pa1 := pqlCreateIssue(t, pool, slug, identA, alice, "PA1", CreateIssueInput{StateID: &todoA})
	pa2 := pqlCreateIssue(t, pool, slug, identA, alice, "PA2", CreateIssueInput{StateID: &doneA})
	pb1 := pqlCreateIssue(t, pool, slug, identB, alice, "PB1", CreateIssueInput{})
	ps1 := pqlCreateIssue(t, pool, slug, shared1.Identifier, alice, "PS1", CreateIssueInput{})
	ps2 := pqlCreateIssue(t, pool, slug, shared2.Identifier, alice, "PS2", CreateIssueInput{})
	pshadow := pqlCreateIssue(t, pool, slug, shadow.Identifier, alice, "PShadow", CreateIssueInput{})
	for _, iss := range []*Issue{pa1, pa2, pb1, ps1, ps2, pshadow} {
		assignIssueTo(t, pool, iss.ID, bob)
	}

	// = by identifier: only project A's issues.
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `project = `+identA), "PA1", "PA2")
	// = by name: same set, and case-insensitive.
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `project = Alpha`), "PA1", "PA2")
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `project = alpha`), "PA1", "PA2")
	// Identifier wins over a same-named project: the project NAMED
	// identA contributes nothing to `project = identA`.
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `project = `+identA), "PA1", "PA2")
	// = by a shared name: issues from BOTH projects bearing it.
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `project = Shared`), "PS1", "PS2")
	// != over the set: everything assigned except project A.
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `project != `+identA), "PB1", "PS1", "PS2", "PShadow")
	// in unions identifier + name sets.
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `project in (`+identA+`, Beta)`), "PA1", "PA2", "PB1")
	// in with mixed identifier + name values.
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `project in (`+identA+`, Shared)`), "PA1", "PA2", "PS1", "PS2")
	// Composition with state + assignee = me.
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `project = `+identA+` AND state = Todo AND assignee = me`), "PA1")
	// Composition with OR / NOT through the predicate tree.
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `project = `+identA+` OR project = Beta`), "PA1", "PA2", "PB1")
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `NOT project = `+identA), "PB1", "PS1", "PS2", "PShadow")

	// Unknown identifier / name 400 with position, never a silent
	// empty list — including an unknown value inside an `in` list
	// (partial matches never silently drop a value).
	myworkPQLWantErr(t, pool, slug, bob, `project = NOPE123`, `unknown project "NOPE123"`)
	myworkPQLWantErr(t, pool, slug, bob, `project = NoSuchProject`, `unknown project "NoSuchProject"`)
	myworkPQLWantErr(t, pool, slug, bob, `project in (`+identA+`, NoSuchProject)`, `unknown project "NoSuchProject"`)
	// A name / identifier existing ONLY in the inaccessible private
	// project is unknown too (no leak): "Private" is the private
	// project's name, identP its identifier.
	myworkPQLWantErr(t, pool, slug, bob, `project = Private`, `unknown project "Private"`)
	myworkPQLWantErr(t, pool, slug, bob, `project = `+identP, `unknown project`)
	// Operators outside = / != / in are rejected by ValidatePQL with
	// the name-field operator message.
	myworkPQLWantErr(t, pool, slug, bob, `project > `+identA, `operator > is not supported for field "project"`)
}

func TestPQLProjectFieldProjectScopeRejected(t *testing.T) {
	s := setupListTest(t)
	todo := pqlStateID(t, s.pool, s.slug, s.ident, s.actor, "Todo")
	pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "Only", CreateIssueInput{StateID: &todo, Priority: intPtr(3)})

	wantErr := func(q, substr string) {
		t.Helper()
		_, err := ListIssues(context.Background(), s.pool, s.slug, s.ident, s.actor,
			ListIssuesInput{PQL: q, PerPage: 10})
		if err == nil {
			t.Fatalf("PQL %q: want error, got nil", q)
		}
		var pqlErr *PQLQueryError
		if !errors.As(err, &pqlErr) {
			t.Fatalf("PQL %q: error type = %T, want *PQLQueryError", q, err)
		}
		if pqlErr.Pos <= 0 {
			t.Fatalf("PQL %q: position = %d, want > 0", q, pqlErr.Pos)
		}
		if !strings.Contains(pqlErr.Msg, substr) {
			t.Fatalf("PQL %q: message = %q, want it to contain %q", q, pqlErr.Msg, substr)
		}
	}

	// 400 for =, !=, in — against the current project and against
	// another project alike, and inside a composed predicate.
	wantErr(`project = `+s.ident, `only available on workspace-scoped lists`)
	wantErr(`project = OTHER`, `only available on workspace-scoped lists`)
	wantErr(`project != `+s.ident, `only available on workspace-scoped lists`)
	wantErr(`project in (`+s.ident+`, OTHER)`, `only available on workspace-scoped lists`)
	wantErr(`project = `+s.ident+` AND priority = 3`, `only available on workspace-scoped lists`)

	// Every other field stays byte-for-byte today's behavior.
	pqlWantNames(t, pqlListNames(t, s, `priority = 3`), "Only")
	pqlWantNames(t, pqlListNames(t, s, `state = Todo`), "Only")
}
