package service

// C116T0: visual ?cycles= / ?modules= list filters on GET
// /api/v1/workspaces/:slug/projects/:identifier/issues. Each
// dimension mirrors the C75T0 epic shape over its junction
// (cycle_issues / module_issues): UUIDs OR-matched, "none" =
// in no cycle / no module. The filters are distinct from the
// Cycle / Module scope containers and AND with them.

import (
	"context"
	"errors"
	"testing"
	"time"
)

// setupCycleModuleFixture builds: cycles c1, c2; modules m1, m2;
// a in c1+m1, b in c1+c2+m2, c in c2 only, d in m1 only,
// e in neither. All five share the default state.
func setupCycleModuleFixture(t *testing.T) (listTestSetup, map[string]*Issue, map[string]string) {
	t.Helper()
	s := setupListTest(t)
	ctx := context.Background()

	c1, err := CreateCycle(ctx, s.pool, s.slug, s.ident, s.actor, CycleInput{
		Name: "Cycle One", StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), EndDate: time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CreateCycle c1: %v", err)
	}
	c2, err := CreateCycle(ctx, s.pool, s.slug, s.ident, s.actor, CycleInput{
		Name: "Cycle Two", StartDate: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), EndDate: time.Date(2026, 2, 28, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CreateCycle c2: %v", err)
	}
	m1, err := CreateModule(ctx, s.pool, s.slug, s.ident, s.actor, ModuleInput{Name: "Module One"})
	if err != nil {
		t.Fatalf("CreateModule m1: %v", err)
	}
	m2, err := CreateModule(ctx, s.pool, s.slug, s.ident, s.actor, ModuleInput{Name: "Module Two"})
	if err != nil {
		t.Fatalf("CreateModule m2: %v", err)
	}

	a := createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "in c1 m1")
	b := createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "in c1 c2 m2")
	c := createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "in c2 only")
	d := createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "in m1 only")
	e := createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "in neither")
	if err := AddCycleIssues(ctx, s.pool, s.slug, s.ident, s.actor, c1.ID, []string{a.ID, b.ID}); err != nil {
		t.Fatalf("AddCycleIssues c1: %v", err)
	}
	if err := AddCycleIssues(ctx, s.pool, s.slug, s.ident, s.actor, c2.ID, []string{b.ID, c.ID}); err != nil {
		t.Fatalf("AddCycleIssues c2: %v", err)
	}
	if err := AddModuleIssues(ctx, s.pool, s.slug, s.ident, s.actor, m1.ID, []string{a.ID, d.ID}); err != nil {
		t.Fatalf("AddModuleIssues m1: %v", err)
	}
	if err := AddModuleIssues(ctx, s.pool, s.slug, s.ident, s.actor, m2.ID, []string{b.ID}); err != nil {
		t.Fatalf("AddModuleIssues m2: %v", err)
	}
	return s, map[string]*Issue{"a": a, "b": b, "c": c, "d": d, "e": e},
		map[string]string{"c1": c1.ID, "c2": c2.ID, "m1": m1.ID, "m2": m2.ID}
}

func assertIssueSet(t *testing.T, label string, got []IssueListItem, want ...*Issue) {
	t.Helper()
	set := issueIDSet(got)
	if len(set) != len(want) {
		t.Fatalf("%s: got %d issues %v, want %d", label, len(set), set, len(want))
	}
	for _, w := range want {
		if !set[w.ID] {
			t.Fatalf("%s: got %v, missing %s (%s)", label, set, w.ID, w.Name)
		}
	}
}

func listFiltered(t *testing.T, s listTestSetup, in ListIssuesInput) []IssueListItem {
	t.Helper()
	res, err := ListIssues(context.Background(), s.pool, s.slug, s.ident, s.actor, in)
	if err != nil {
		t.Fatalf("ListIssues %+v: %v", in, err)
	}
	return res.Issues
}

// TestListIssuesCycleFilter: single UUID, multi-UUID OR, "none"
// only, mixed UUIDs + "none", and empty = no filter.
func TestListIssuesCycleFilter(t *testing.T) {
	s, iss, ids := setupCycleModuleFixture(t)

	assertIssueSet(t, "cycles=[c1]", listFiltered(t, s, ListIssuesInput{FilterCycles: []string{ids["c1"]}}), iss["a"], iss["b"])
	assertIssueSet(t, "cycles=[c1,c2]", listFiltered(t, s, ListIssuesInput{FilterCycles: []string{ids["c1"], ids["c2"]}}), iss["a"], iss["b"], iss["c"])
	assertIssueSet(t, "cycles=[none]", listFiltered(t, s, ListIssuesInput{FilterCycles: []string{"none"}}), iss["d"], iss["e"])
	assertIssueSet(t, "cycles=[c1,none]", listFiltered(t, s, ListIssuesInput{FilterCycles: []string{ids["c1"], "none"}}), iss["a"], iss["b"], iss["d"], iss["e"])
	assertIssueSet(t, "cycles empty", listFiltered(t, s, ListIssuesInput{}), iss["a"], iss["b"], iss["c"], iss["d"], iss["e"])
}

// TestListIssuesModuleFilter: same shapes over module_issues.
func TestListIssuesModuleFilter(t *testing.T) {
	s, iss, ids := setupCycleModuleFixture(t)

	assertIssueSet(t, "modules=[m1]", listFiltered(t, s, ListIssuesInput{FilterModules: []string{ids["m1"]}}), iss["a"], iss["d"])
	assertIssueSet(t, "modules=[m1,m2]", listFiltered(t, s, ListIssuesInput{FilterModules: []string{ids["m1"], ids["m2"]}}), iss["a"], iss["b"], iss["d"])
	assertIssueSet(t, "modules=[none]", listFiltered(t, s, ListIssuesInput{FilterModules: []string{"none"}}), iss["c"], iss["e"])
	assertIssueSet(t, "modules=[m2,none]", listFiltered(t, s, ListIssuesInput{FilterModules: []string{ids["m2"], "none"}}), iss["b"], iss["c"], iss["e"])
	assertIssueSet(t, "modules empty", listFiltered(t, s, ListIssuesInput{}), iss["a"], iss["b"], iss["c"], iss["d"], iss["e"])
}

// TestListIssuesCycleModuleCombos: the two dimensions AND with
// each other and with state + epic + assignee.
func TestListIssuesCycleModuleCombos(t *testing.T) {
	s, iss, ids := setupCycleModuleFixture(t)
	ctx := context.Background()

	epic, err := CreateEpic(ctx, s.pool, s.slug, s.ident, s.actor, EpicInput{Name: "combo epic"})
	if err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	if err := AddEpicIssues(ctx, s.pool, s.slug, s.ident, s.actor, epic.ID, []string{iss["a"].ID}); err != nil {
		t.Fatalf("AddEpicIssues: %v", err)
	}
	if err := AssignAssignee(ctx, s.pool, s.slug, s.ident, iss["a"].ID, s.actor, s.actor); err != nil {
		t.Fatalf("AssignAssignee: %v", err)
	}

	// cycles x modules: only a is in c1 AND m1.
	assertIssueSet(t, "cycles x modules",
		listFiltered(t, s, ListIssuesInput{FilterCycles: []string{ids["c1"]}, FilterModules: []string{ids["m1"]}}), iss["a"])
	// cycles x state + epic + assignee: a is the only issue in
	// c1, in the combo epic, assigned to the actor, in the
	// default state.
	assertIssueSet(t, "cycles x state x epic x assignee",
		listFiltered(t, s, ListIssuesInput{
			FilterCycles: []string{ids["c1"]},
			State:        iss["a"].StateID,
			Epics:        []string{epic.ID},
			Assignees:    []string{s.actor},
		}), iss["a"])
	// modules x state: d is the only issue in m1 in the default
	// state that is NOT also in c1 — proves the module block
	// composes independently of the cycle block.
	assertIssueSet(t, "modules x state",
		listFiltered(t, s, ListIssuesInput{
			FilterModules: []string{ids["m1"]},
			State:         iss["d"].StateID,
			FilterCycles:  []string{"none"},
		}), iss["d"])
}

// TestListIssuesCycleModuleScopeAND: the visual filters AND
// with the Cycle / Module scope containers, never replace
// them — a foreign filter inside a scope yields empty, the
// current filter inside its own scope is unchanged.
func TestListIssuesCycleModuleScopeAND(t *testing.T) {
	s, iss, ids := setupCycleModuleFixture(t)
	ctx := context.Background()

	// A third cycle holding nothing: foreign to the c1 scope.
	c3, err := CreateCycle(ctx, s.pool, s.slug, s.ident, s.actor, CycleInput{
		Name: "Cycle Three", StartDate: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), EndDate: time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CreateCycle c3: %v", err)
	}

	// Scope c1 + filter [c3] (foreign): empty by construction.
	assertIssueSet(t, "scope c1 + filter c3",
		listFiltered(t, s, ListIssuesInput{Cycle: ids["c1"], FilterCycles: []string{c3.ID}}))
	// Scope c1 + filter [c1] (current): unchanged, a+b.
	assertIssueSet(t, "scope c1 + filter c1",
		listFiltered(t, s, ListIssuesInput{Cycle: ids["c1"], FilterCycles: []string{ids["c1"]}}), iss["a"], iss["b"])
	// Scope m1 + filter [m2] (foreign): empty — b is in m2 but
	// not in the m1 scope, a/d are in m1 but not m2.
	assertIssueSet(t, "scope m1 + filter m2",
		listFiltered(t, s, ListIssuesInput{Module: ids["m1"], FilterModules: []string{ids["m2"]}}))
	// Scope m1 + filter [m1] (current): unchanged, a+d.
	assertIssueSet(t, "scope m1 + filter m1",
		listFiltered(t, s, ListIssuesInput{Module: ids["m1"], FilterModules: []string{ids["m1"]}}), iss["a"], iss["d"])
	// Cross-dimension scope AND: scope c1 + filter modules
	// [m1] narrows the scope to a only.
	assertIssueSet(t, "scope c1 + filter m1",
		listFiltered(t, s, ListIssuesInput{Cycle: ids["c1"], FilterModules: []string{ids["m1"]}}), iss["a"])
}

// TestListIssuesCycleModuleInvalidValues: malformed UUIDs are
// rejected with ErrInvalidListFilter; "none" passes validation.
func TestListIssuesCycleModuleInvalidValues(t *testing.T) {
	s := setupListTest(t)
	ctx := context.Background()
	createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "one")

	for name, in := range map[string]ListIssuesInput{
		"cycles":  {FilterCycles: []string{"not-a-uuid"}},
		"modules": {FilterModules: []string{"not-a-uuid"}},
	} {
		if _, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor, in); !errors.Is(err, ErrInvalidListFilter) {
			t.Fatalf("ListIssues %s malformed uuid: err = %v, want ErrInvalidListFilter", name, err)
		}
	}
	for name, in := range map[string]ListIssuesInput{
		"cycles":  {FilterCycles: []string{"none"}},
		"modules": {FilterModules: []string{"none"}},
	} {
		if _, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor, in); err != nil {
			t.Fatalf("ListIssues %s=none: %v", name, err)
		}
	}
}

// TestListIssuesCycleModulePQLExclusivity: in PQL mode the
// visual cycle/module filters are ignored (withoutVisualFilters
// zeroes them), exactly like the other visual dimensions —
// the scope containers, by contrast, survive (proven by C95T0).
func TestListIssuesCycleModulePQLExclusivity(t *testing.T) {
	s, iss, ids := setupCycleModuleFixture(t)
	assertIssueSet(t, "pql ignores visual cycle/module filters",
		listFiltered(t, s, ListIssuesInput{
			PQL:           "priority >= 0",
			FilterCycles:  []string{ids["c1"]},
			FilterModules: []string{ids["m1"]},
		}), iss["a"], iss["b"], iss["c"], iss["d"], iss["e"])
}
