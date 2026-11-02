package service

// `release` PQL field, project scope (C117T0): name resolution
// (case-insensitive) over the DIRECT issues.release_id column,
// the `none` sentinel, inclusive !=, `in` incl. mixed named +
// none values, unknown-name 400s with position, operator
// rejection, composition with state + assignee = me, and the
// scope-AND contract (a predicate naming a different release
// than the ?cycle= scope yields empty; a cross combination —
// release predicate on a cycle-scoped list — filters
// correctly). Follows the pql_cycle_module_c115t0_test.go
// idioms; real test database, no skips.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func pqlReleaseFixture(t *testing.T, s listTestSetup) (relA, relB string) {
	t.Helper()
	ctx := context.Background()
	r1, err := CreateRelease(ctx, s.pool, s.slug, s.ident, s.actor, ReleaseInput{Name: "v1.0"})
	if err != nil {
		t.Fatalf("CreateRelease v1.0: %v", err)
	}
	r2, err := CreateRelease(ctx, s.pool, s.slug, s.ident, s.actor, ReleaseInput{Name: "v2.0"})
	if err != nil {
		t.Fatalf("CreateRelease v2.0: %v", err)
	}
	return r1.ID, r2.ID
}

func TestPQLServiceReleaseField(t *testing.T) {
	s := setupListTest(t)
	ctx := context.Background()
	todo := pqlStateID(t, s.pool, s.slug, s.ident, s.actor, "Todo")
	r1, r2 := pqlReleaseFixture(t, s)

	a := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "A", CreateIssueInput{StateID: &todo})
	b := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "B", CreateIssueInput{StateID: &todo})
	pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "C", CreateIssueInput{StateID: &todo})
	if err := AssignReleaseIssues(ctx, s.pool, s.slug, s.ident, s.actor, r1, []string{a.ID}); err != nil {
		t.Fatalf("AssignReleaseIssues v1.0: %v", err)
	}
	if err := AssignReleaseIssues(ctx, s.pool, s.slug, s.ident, s.actor, r2, []string{b.ID}); err != nil {
		t.Fatalf("AssignReleaseIssues v2.0: %v", err)
	}
	pqlAssign(t, s.pool, a.ID, s.actor)

	pqlWantNames(t, pqlListNames(t, s, `release = "v1.0"`), "A")
	pqlWantNames(t, pqlListNames(t, s, `release = "V1.0"`), "A") // case-insensitive
	pqlWantNames(t, pqlListNames(t, s, `release = "v2.0"`), "B")
	pqlWantNames(t, pqlListNames(t, s, `release != "v1.0"`), "B", "C") // != includes no-release issues
	pqlWantNames(t, pqlListNames(t, s, `release in ("v1.0", "v2.0")`), "A", "B")
	pqlWantNames(t, pqlListNames(t, s, `release in ("v1.0", none)`), "A", "C") // mixed named + none
	pqlWantNames(t, pqlListNames(t, s, `release = none`), "C")
	pqlWantNames(t, pqlListNames(t, s, `release != none`), "A", "B")
	pqlWantNames(t, pqlListNames(t, s, `release in (none)`), "C")
	// Composition with state + assignee = me.
	pqlWantNames(t, pqlListNames(t, s, `release = "v1.0" AND state = Todo`), "A")
	pqlWantNames(t, pqlListNames(t, s, `release = "v1.0" AND assignee = me`), "A")
	pqlWantNames(t, pqlListNames(t, s, `release = "v2.0" AND assignee = me`))
}

func TestPQLServiceReleaseUnknownNamesAndOperators(t *testing.T) {
	s := setupListTest(t)
	pqlReleaseFixture(t, s)
	pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "A", CreateIssueInput{})

	for _, tc := range []struct {
		query string
		want  string
	}{
		{`release = NoSuchRelease`, `unknown release "NoSuchRelease"`},
		{`release in ("v1.0", NoSuchRelease)`, `unknown release "NoSuchRelease"`},
		{`release > "v1.0"`, `operator > is not supported for field "release"`},
		{`foo = Bar`, "release"}, // the known-fields list now names the field
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
}

func TestPQLServiceReleaseScopeAND(t *testing.T) {
	s := setupListTest(t)
	ctx := context.Background()
	r1, r2 := pqlReleaseFixture(t, s)

	c1, err := CreateCycle(ctx, s.pool, s.slug, s.ident, s.actor, CycleInput{
		Name: "Sprint 3", StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), EndDate: time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CreateCycle Sprint 3: %v", err)
	}

	inCycleRel := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "InCycleRel", CreateIssueInput{})
	inCycleOnly := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "InCycleOnly", CreateIssueInput{})
	outsideRel := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "OutsideRel", CreateIssueInput{})
	if err := AddCycleIssues(ctx, s.pool, s.slug, s.ident, s.actor, c1.ID, []string{inCycleRel.ID, inCycleOnly.ID}); err != nil {
		t.Fatalf("AddCycleIssues Sprint 3: %v", err)
	}
	if err := AssignReleaseIssues(ctx, s.pool, s.slug, s.ident, s.actor, r1, []string{inCycleRel.ID}); err != nil {
		t.Fatalf("AssignReleaseIssues v1.0: %v", err)
	}
	if err := AssignReleaseIssues(ctx, s.pool, s.slug, s.ident, s.actor, r2, []string{outsideRel.ID}); err != nil {
		t.Fatalf("AssignReleaseIssues v2.0: %v", err)
	}

	// Foreign-release predicate under a cycle scope: empty by
	// construction (AND, not rejection).
	pqlWantNames(t, pqlScopeListNames(t, s, ListIssuesInput{PQL: `release = "v2.0"`, Cycle: c1.ID}))
	// Same-release predicate under a cycle scope: narrows to the
	// in-cycle, in-release row.
	pqlWantNames(t, pqlScopeListNames(t, s, ListIssuesInput{PQL: `release = "v1.0"`, Cycle: c1.ID}),
		"InCycleRel")
	// `release = none` under a cycle scope: the in-cycle row with
	// no release.
	pqlWantNames(t, pqlScopeListNames(t, s, ListIssuesInput{PQL: `release = none`, Cycle: c1.ID}),
		"InCycleOnly")
}
