package service

// PQL scope tests (C95T0): Cycle and Module are scope containers, not
// filter dimensions — they survive PQL mode (withoutVisualFilters)
// and AND with the PQL predicate, while every other visual filter
// dimension is still zeroed. Module scope without PQL returns exactly
// the ListModuleIssues membership; a foreign cycle/module UUID yields
// an empty list (project confinement via the list query's project
// predicate).

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"
)

func pqlScopeListNames(t *testing.T, s listTestSetup, in ListIssuesInput) []string {
	t.Helper()
	in.OrderBy = "-sequence_id"
	in.PerPage = 100
	res, err := ListIssues(context.Background(), s.pool, s.slug, s.ident, s.actor, in)
	if err != nil {
		t.Fatalf("ListIssues %+v: %v", in, err)
	}
	names := make([]string, 0, len(res.Issues))
	for _, it := range res.Issues {
		names = append(names, it.Name)
	}
	sort.Strings(names)
	return names
}

func TestPQLScopeCycleAndModule(t *testing.T) {
	s := setupListTest(t)
	ctx := context.Background()

	now := time.Now()
	cycle, err := CreateCycle(ctx, s.pool, s.slug, s.ident, s.actor, CycleInput{
		Name: "Sprint C95", StartDate: now.AddDate(0, 0, -1), EndDate: now.AddDate(0, 0, 13),
	})
	if err != nil {
		t.Fatalf("CreateCycle: %v", err)
	}
	module, err := CreateModule(ctx, s.pool, s.slug, s.ident, s.actor, ModuleInput{Name: "Mod C95"})
	if err != nil {
		t.Fatalf("CreateModule: %v", err)
	}

	cycleMatch := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "CycleMatch", CreateIssueInput{Priority: intPtr(3)})
	cycleNoMatch := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "CycleNoMatch", CreateIssueInput{Priority: intPtr(1)})
	outsideMatch := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "OutsideMatch", CreateIssueInput{Priority: intPtr(3)})
	moduleMatch := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "ModuleMatch", CreateIssueInput{Priority: intPtr(3)})
	moduleNoMatch := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "ModuleNoMatch", CreateIssueInput{Priority: intPtr(1)})
	outsideModuleMatch := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "OutsideModuleMatch", CreateIssueInput{Priority: intPtr(3)})
	bothMatch := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "BothMatch", CreateIssueInput{Priority: intPtr(3)})

	if err := AddCycleIssues(ctx, s.pool, s.slug, s.ident, s.actor, cycle.ID,
		[]string{cycleMatch.ID, cycleNoMatch.ID, bothMatch.ID}); err != nil {
		t.Fatalf("AddCycleIssues: %v", err)
	}
	if err := AddModuleIssues(ctx, s.pool, s.slug, s.ident, s.actor, module.ID,
		[]string{moduleMatch.ID, moduleNoMatch.ID, bothMatch.ID}); err != nil {
		t.Fatalf("AddModuleIssues: %v", err)
	}

	high := `priority = High`
	allHigh := []string{bothMatch.Name, cycleMatch.Name, moduleMatch.Name, outsideMatch.Name, outsideModuleMatch.Name}

	// PQL + cycle: in-cycle + matching shown; matching but outside
	// the cycle hidden; in-cycle but not matching hidden.
	pqlWantNames(t, pqlScopeListNames(t, s, ListIssuesInput{PQL: high, Cycle: cycle.ID}),
		"BothMatch", "CycleMatch")
	// PQL + module: same three cases.
	pqlWantNames(t, pqlScopeListNames(t, s, ListIssuesInput{PQL: high, Module: module.ID}),
		"BothMatch", "ModuleMatch")
	// Both scopes AND together.
	pqlWantNames(t, pqlScopeListNames(t, s, ListIssuesInput{PQL: high, Cycle: cycle.ID, Module: module.ID}),
		"BothMatch")

	// PQL + state still ignores state (contract unchanged): a Done
	// state filter must not narrow the PQL result.
	done := pqlStateID(t, s.pool, s.slug, s.ident, s.actor, "Done")
	pqlWantNames(t, pqlScopeListNames(t, s, ListIssuesInput{PQL: high, State: done}), allHigh...)

	// PQL alone unchanged.
	pqlWantNames(t, pqlScopeListNames(t, s, ListIssuesInput{PQL: high}), allHigh...)

	// Module scope without PQL returns exactly the ListModuleIssues
	// membership.
	pqlWantNames(t, pqlScopeListNames(t, s, ListIssuesInput{Module: module.ID}),
		"BothMatch", "ModuleMatch", "ModuleNoMatch")
	members, err := ListModuleIssues(ctx, s.pool, s.slug, s.ident, s.actor, module.ID)
	if err != nil {
		t.Fatalf("ListModuleIssues: %v", err)
	}
	memberNames := make([]string, 0, len(members))
	for _, it := range members {
		memberNames = append(memberNames, it.Name)
	}
	sort.Strings(memberNames)
	pqlWantNames(t, memberNames, "BothMatch", "ModuleMatch", "ModuleNoMatch")

	// Foreign cycle/module UUIDs yield an empty list (project
	// confinement), with and without PQL.
	foreign := "11111111-1111-1111-1111-111111111111"
	for _, in := range []ListIssuesInput{
		{Cycle: foreign},
		{Module: foreign},
		{PQL: high, Cycle: foreign},
		{PQL: high, Module: foreign},
	} {
		if got := pqlScopeListNames(t, s, in); len(got) != 0 {
			t.Fatalf("foreign scope %+v: got %v, want empty", in, got)
		}
	}

	// A malformed module UUID is a 400-class filter error, with and
	// without PQL (the scope survives zeroing, so it is validated).
	for _, in := range []ListIssuesInput{
		{Module: "not-a-uuid"},
		{PQL: high, Module: "not-a-uuid"},
	} {
		_, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor, in)
		if !errors.Is(err, ErrInvalidListFilter) {
			t.Fatalf("malformed module %+v: err = %v, want ErrInvalidListFilter", in, err)
		}
	}
}
