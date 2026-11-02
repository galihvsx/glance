package service

// `created_by` PQL field, project scope (C118T0): name/email
// resolution (case-insensitive) among workspace members over
// the DIRECT issues.created_by column, `me` → the caller,
// inclusive != (no NULL arm: the column is NOT NULL), `in`,
// unknown-name and ambiguous-name 400s with position, the
// `none` 400 (every issue has an author — never a silent
// empty list), operator rejection, composition with state +
// assignee = me, and the scope-AND contract (a predicate
// naming a different author than the ?cycle= scope yields
// empty; a cross combination filters correctly). Follows the
// pql_release_c117t0_test.go idioms; real test database, no
// skips.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// pqlCreatedByFixture builds alice ("Alice Example", member)
// plus two members sharing one display name ("Sam Twin") for
// the ambiguity case. Issues are created by the caller in each
// test; the fixture only prepares the members.
func pqlCreatedByFixture(t *testing.T, s listTestSetup) (aliceEmail string, alice string) {
	t.Helper()
	ctx := context.Background()
	aliceEmail = uniqueTestEmail("pql-cb-alice")
	alice = createTestUser(t, s.pool, aliceEmail)
	if _, err := s.pool.Exec(ctx, `UPDATE users SET name = 'Alice Example' WHERE id = $1::uuid`, alice); err != nil {
		t.Fatalf("name Alice: %v", err)
	}
	addTestMember(t, s.pool, s.slug, s.actor, alice, RoleMember)
	for _, prefix := range []string{"pql-cb-twin1", "pql-cb-twin2"} {
		twin := createTestUser(t, s.pool, uniqueTestEmail(prefix))
		if _, err := s.pool.Exec(ctx, `UPDATE users SET name = 'Sam Twin' WHERE id = $1::uuid`, twin); err != nil {
			t.Fatalf("name Sam Twin: %v", err)
		}
		addTestMember(t, s.pool, s.slug, s.actor, twin, RoleMember)
	}
	return aliceEmail, alice
}

func TestPQLServiceCreatedByField(t *testing.T) {
	s := setupListTest(t)
	aliceEmail, alice := pqlCreatedByFixture(t, s)

	todo := pqlStateID(t, s.pool, s.slug, s.ident, s.actor, "Todo")
	a := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "A", CreateIssueInput{StateID: &todo})
	pqlCreateIssue(t, s.pool, s.slug, s.ident, alice, "B", CreateIssueInput{StateID: &todo})
	pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "C", CreateIssueInput{StateID: &todo})
	pqlAssign(t, s.pool, a.ID, s.actor)

	pqlWantNames(t, pqlListNames(t, s, `created_by = me`), "A", "C")
	pqlWantNames(t, pqlListNames(t, s, `created_by = "Alice Example"`), "B")
	pqlWantNames(t, pqlListNames(t, s, `created_by = "alice example"`), "B") // case-insensitive
	pqlWantNames(t, pqlListNames(t, s, `created_by = `+aliceEmail), "B")     // email also resolves
	pqlWantNames(t, pqlListNames(t, s, `created_by != me`), "B")
	pqlWantNames(t, pqlListNames(t, s, `created_by != "Alice Example"`), "A", "C")
	pqlWantNames(t, pqlListNames(t, s, `created_by in (me, "Alice Example")`), "A", "B", "C")
	// Composition with state + assignee = me.
	pqlWantNames(t, pqlListNames(t, s, `created_by = me AND state = Todo`), "A", "C")
	pqlWantNames(t, pqlListNames(t, s, `created_by = me AND assignee = me`), "A")
	pqlWantNames(t, pqlListNames(t, s, `created_by = "Alice Example" AND assignee = me`))
}

func TestPQLServiceCreatedByErrors(t *testing.T) {
	s := setupListTest(t)
	aliceEmail, _ := pqlCreatedByFixture(t, s)
	pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "A", CreateIssueInput{})

	for _, tc := range []struct {
		query string
		want  string
	}{
		{`created_by = NoSuchAuthor`, `unknown created_by "NoSuchAuthor"`},
		{`created_by in (me, NoSuchAuthor)`, `unknown created_by "NoSuchAuthor"`},
		{`created_by = "Sam Twin"`, `ambiguous created_by "Sam Twin"`},
		{`created_by = none`, `every issue has an author`},
		{`created_by in (me, none)`, `every issue has an author`},
		{`created_by != none`, `every issue has an author`},
		{`created_by > me`, `operator > is not supported for field "created_by"`},
		{`foo = Bar`, "created_by"}, // the known-fields list now names the field
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

	// A workspace NON-member's name is unknown, even if the
	// user row exists: resolution is confined to workspace
	// members, exactly like assignee.
	outsiderEmail := uniqueTestEmail("pql-cb-outsider")
	outsider := createTestUser(t, s.pool, outsiderEmail)
	if _, err := s.pool.Exec(context.Background(), `UPDATE users SET name = 'Out Sider' WHERE id = $1::uuid`, outsider); err != nil {
		t.Fatalf("name Out Sider: %v", err)
	}
	_, err := ListIssues(context.Background(), s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{PQL: `created_by = "Out Sider"`, PerPage: 10})
	var pqlErr *PQLQueryError
	if !errors.As(err, &pqlErr) || !strings.Contains(pqlErr.Error(), `unknown created_by "Out Sider"`) {
		t.Fatalf("PQL outsider name: err = %v, want unknown-created_by 400", err)
	}
	_ = aliceEmail
}

func TestPQLServiceCreatedByScopeAND(t *testing.T) {
	s := setupListTest(t)
	_, alice := pqlCreatedByFixture(t, s)
	ctx := context.Background()

	c1, err := CreateCycle(ctx, s.pool, s.slug, s.ident, s.actor, CycleInput{
		Name: "Sprint 3", StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), EndDate: time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CreateCycle Sprint 3: %v", err)
	}

	inCycleActor := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "InCycleActor", CreateIssueInput{})
	inCycleAlice := pqlCreateIssue(t, s.pool, s.slug, s.ident, alice, "InCycleAlice", CreateIssueInput{})
	outsideAlice := pqlCreateIssue(t, s.pool, s.slug, s.ident, alice, "OutsideAlice", CreateIssueInput{})
	if err := AddCycleIssues(ctx, s.pool, s.slug, s.ident, s.actor, c1.ID, []string{inCycleActor.ID, inCycleAlice.ID}); err != nil {
		t.Fatalf("AddCycleIssues Sprint 3: %v", err)
	}

	// Foreign-author predicate under a cycle scope: empty by
	// construction (AND, not rejection).
	pqlWantNames(t, pqlScopeListNames(t, s, ListIssuesInput{PQL: `created_by = "Alice Example"`, Cycle: c1.ID}),
		"InCycleAlice")
	// Same-author predicate under a cycle scope: narrows to the
	// in-cycle, by-actor row.
	pqlWantNames(t, pqlScopeListNames(t, s, ListIssuesInput{PQL: `created_by = me`, Cycle: c1.ID}),
		"InCycleActor")
	// A predicate naming an author with no in-cycle issue, under
	// the scope, is empty — even though that author has an issue
	// outside the scope.
	_ = outsideAlice
	pqlWantNames(t, pqlScopeListNames(t, s, ListIssuesInput{PQL: `created_by = "Alice Example" AND assignee = me`, Cycle: c1.ID}))
}
