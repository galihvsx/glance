package service

// `cycle` and `module` PQL fields, project scope (C115T0): name
// resolution (case-insensitive) over the cycle_issues /
// module_issues junctions, the `none` sentinel, inclusive !=,
// `in` incl. mixed named + none values, unknown-name 400s with
// position, operator rejection, composition with state +
// assignee = me, and the scope-AND contract (a predicate naming a
// different cycle than the ?cycle= scope yields empty; a cross
// combination — module predicate on a cycle-scoped list, and vice
// versa — filters correctly). Follows the pql_service_test.go /
// pql_scope_c95t0_test.go idioms; real test database, no skips.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func pqlCycleModuleFixture(t *testing.T, s listTestSetup) (cycleA, cycleB, moduleA, moduleB string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now()
	c1, err := CreateCycle(ctx, s.pool, s.slug, s.ident, s.actor, CycleInput{
		Name: "Sprint 3", StartDate: now.AddDate(0, 0, -30), EndDate: now.AddDate(0, 0, -16),
	})
	if err != nil {
		t.Fatalf("CreateCycle Sprint 3: %v", err)
	}
	c2, err := CreateCycle(ctx, s.pool, s.slug, s.ident, s.actor, CycleInput{
		Name: "Sprint 4", StartDate: now.AddDate(0, 0, -15), EndDate: now.AddDate(0, 0, -1),
	})
	if err != nil {
		t.Fatalf("CreateCycle Sprint 4: %v", err)
	}
	m1, err := CreateModule(ctx, s.pool, s.slug, s.ident, s.actor, ModuleInput{Name: "Auth"})
	if err != nil {
		t.Fatalf("CreateModule Auth: %v", err)
	}
	m2, err := CreateModule(ctx, s.pool, s.slug, s.ident, s.actor, ModuleInput{Name: "Billing"})
	if err != nil {
		t.Fatalf("CreateModule Billing: %v", err)
	}
	return c1.ID, c2.ID, m1.ID, m2.ID
}

func TestPQLServiceCycleField(t *testing.T) {
	s := setupListTest(t)
	ctx := context.Background()
	todo := pqlStateID(t, s.pool, s.slug, s.ident, s.actor, "Todo")
	c1, c2, _, _ := pqlCycleModuleFixture(t, s)

	a := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "A", CreateIssueInput{StateID: &todo})
	b := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "B", CreateIssueInput{StateID: &todo})
	pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "C", CreateIssueInput{StateID: &todo})
	if err := AddCycleIssues(ctx, s.pool, s.slug, s.ident, s.actor, c1, []string{a.ID}); err != nil {
		t.Fatalf("AddCycleIssues Sprint 3: %v", err)
	}
	if err := AddCycleIssues(ctx, s.pool, s.slug, s.ident, s.actor, c2, []string{b.ID}); err != nil {
		t.Fatalf("AddCycleIssues Sprint 4: %v", err)
	}
	pqlAssign(t, s.pool, a.ID, s.actor)

	pqlWantNames(t, pqlListNames(t, s, `cycle = "Sprint 3"`), "A")
	pqlWantNames(t, pqlListNames(t, s, `cycle = "sprint 3"`), "A") // case-insensitive
	pqlWantNames(t, pqlListNames(t, s, `cycle = "Sprint 4"`), "B")
	pqlWantNames(t, pqlListNames(t, s, `cycle != "Sprint 3"`), "B", "C") // != includes no-cycle issues
	pqlWantNames(t, pqlListNames(t, s, `cycle in ("Sprint 3", "Sprint 4")`), "A", "B")
	pqlWantNames(t, pqlListNames(t, s, `cycle in ("Sprint 3", none)`), "A", "C") // mixed named + none
	pqlWantNames(t, pqlListNames(t, s, `cycle = none`), "C")
	pqlWantNames(t, pqlListNames(t, s, `cycle != none`), "A", "B")
	pqlWantNames(t, pqlListNames(t, s, `cycle in (none)`), "C")
	// Composition with state + assignee = me.
	pqlWantNames(t, pqlListNames(t, s, `cycle = "Sprint 3" AND state = Todo`), "A")
	pqlWantNames(t, pqlListNames(t, s, `cycle = "Sprint 3" AND assignee = me`), "A")
	pqlWantNames(t, pqlListNames(t, s, `cycle = "Sprint 4" AND assignee = me`))
}

func TestPQLServiceModuleField(t *testing.T) {
	s := setupListTest(t)
	ctx := context.Background()
	todo := pqlStateID(t, s.pool, s.slug, s.ident, s.actor, "Todo")
	_, _, m1, m2 := pqlCycleModuleFixture(t, s)

	a := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "A", CreateIssueInput{StateID: &todo})
	b := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "B", CreateIssueInput{StateID: &todo})
	pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "C", CreateIssueInput{StateID: &todo})
	if err := AddModuleIssues(ctx, s.pool, s.slug, s.ident, s.actor, m1, []string{a.ID}); err != nil {
		t.Fatalf("AddModuleIssues Auth: %v", err)
	}
	if err := AddModuleIssues(ctx, s.pool, s.slug, s.ident, s.actor, m2, []string{b.ID}); err != nil {
		t.Fatalf("AddModuleIssues Billing: %v", err)
	}
	pqlAssign(t, s.pool, a.ID, s.actor)

	pqlWantNames(t, pqlListNames(t, s, `module = Auth`), "A")
	pqlWantNames(t, pqlListNames(t, s, `module = auth`), "A") // case-insensitive
	pqlWantNames(t, pqlListNames(t, s, `module = Billing`), "B")
	pqlWantNames(t, pqlListNames(t, s, `module != Auth`), "B", "C") // != includes no-module issues
	pqlWantNames(t, pqlListNames(t, s, `module in (Auth, Billing)`), "A", "B")
	pqlWantNames(t, pqlListNames(t, s, `module in (Auth, none)`), "A", "C") // mixed named + none
	pqlWantNames(t, pqlListNames(t, s, `module = none`), "C")
	pqlWantNames(t, pqlListNames(t, s, `module != none`), "A", "B")
	pqlWantNames(t, pqlListNames(t, s, `module in (none)`), "C")
	// Composition with state + assignee = me.
	pqlWantNames(t, pqlListNames(t, s, `module = Auth AND state = Todo`), "A")
	pqlWantNames(t, pqlListNames(t, s, `module = Auth AND assignee = me`), "A")
	pqlWantNames(t, pqlListNames(t, s, `module = Billing AND assignee = me`))
}

func TestPQLServiceCycleModuleUnknownNamesAndOperators(t *testing.T) {
	s := setupListTest(t)
	pqlCycleModuleFixture(t, s)
	pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "A", CreateIssueInput{})

	for _, tc := range []struct {
		query string
		want  string
	}{
		{`cycle = NoSuchCycle`, `unknown cycle "NoSuchCycle"`},
		{`module = NoSuchModule`, `unknown module "NoSuchModule"`},
		{`cycle in ("Sprint 3", NoSuchCycle)`, `unknown cycle "NoSuchCycle"`},
		{`module in (Auth, NoSuchModule)`, `unknown module "NoSuchModule"`},
		{`cycle > "Sprint 3"`, `operator > is not supported for field "cycle"`},
		{`module < Auth`, `operator < is not supported for field "module"`},
		{`foo = Bar`, "cycle"}, // the known-fields list now names both fields
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

func TestPQLServiceCycleModuleScopeAND(t *testing.T) {
	s := setupListTest(t)
	ctx := context.Background()
	c1, c2, m1, _ := pqlCycleModuleFixture(t, s)

	inCycleMod := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "InCycleMod", CreateIssueInput{})
	inCycleOnly := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "InCycleOnly", CreateIssueInput{})
	inOtherCycleMod := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "InOtherCycleMod", CreateIssueInput{})
	outsideMod := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "OutsideMod", CreateIssueInput{})
	if err := AddCycleIssues(ctx, s.pool, s.slug, s.ident, s.actor, c1, []string{inCycleMod.ID, inCycleOnly.ID}); err != nil {
		t.Fatalf("AddCycleIssues Sprint 3: %v", err)
	}
	if err := AddCycleIssues(ctx, s.pool, s.slug, s.ident, s.actor, c2, []string{inOtherCycleMod.ID}); err != nil {
		t.Fatalf("AddCycleIssues Sprint 4: %v", err)
	}
	if err := AddModuleIssues(ctx, s.pool, s.slug, s.ident, s.actor, m1, []string{inCycleMod.ID, inOtherCycleMod.ID, outsideMod.ID}); err != nil {
		t.Fatalf("AddModuleIssues Auth: %v", err)
	}

	// Foreign-cycle predicate under a cycle scope: empty by
	// construction (epic precedent — AND, not rejection).
	pqlWantNames(t, pqlScopeListNames(t, s, ListIssuesInput{PQL: `cycle = "Sprint 4"`, Cycle: c1}))
	// Same-cycle predicate under its own scope: redundant but harmless.
	pqlWantNames(t, pqlScopeListNames(t, s, ListIssuesInput{PQL: `cycle = "Sprint 3"`, Cycle: c1}),
		"InCycleMod", "InCycleOnly")
	// `cycle = none` under a cycle scope: empty (every scoped row is in a cycle).
	pqlWantNames(t, pqlScopeListNames(t, s, ListIssuesInput{PQL: `cycle = none`, Cycle: c1}))
	// Cross combination: module predicate on a cycle-scoped list.
	pqlWantNames(t, pqlScopeListNames(t, s, ListIssuesInput{PQL: `module = Auth`, Cycle: c1}),
		"InCycleMod")
	// Cross combination, other way: cycle predicate on a module-scoped list.
	pqlWantNames(t, pqlScopeListNames(t, s, ListIssuesInput{PQL: `cycle = "Sprint 3"`, Module: m1}),
		"InCycleMod")
	// Foreign-module predicate under a module scope: empty.
	pqlWantNames(t, pqlScopeListNames(t, s, ListIssuesInput{PQL: `module = Billing`, Module: m1}))
	// A PQL predicate ANDs with ?epic= scope params too: covered by
	// the epic scope-survives tests; here the module predicate ANDs
	// with the cycle scope and the epic scope is untouched.
}
