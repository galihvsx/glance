package service

// C118T0: visual ?created_by= list filter on GET
// /api/v1/workspaces/:slug/projects/:identifier/issues. The
// dimension rides the DIRECT issues.created_by column
// (migration 000007, UUID NOT NULL), not a junction: UUIDs
// OR-matched, NO "none" sentinel — there are no authorless
// issues, so "none" is rejected by validateListFilterValues
// (ErrInvalidListFilter). There is no singular ?created_by=
// scope param, so the filter ANDs with any detail-page scope
// (cycle / module / epic) exactly like FilterReleases.

import (
	"context"
	"errors"
	"testing"
	"time"
)

// setupCreatedByFixture builds: alice (workspace member,
// "Alice Example"); a + c created by the default actor, b +
// d created by alice. All four share the default state.
func setupCreatedByFixture(t *testing.T) (listTestSetup, map[string]*Issue, map[string]string) {
	t.Helper()
	s := setupListTest(t)
	ctx := context.Background()

	aliceEmail := uniqueTestEmail("cb-alice")
	alice := createTestUser(t, s.pool, aliceEmail)
	if _, err := s.pool.Exec(ctx, `UPDATE users SET name = 'Alice Example' WHERE id = $1::uuid`, alice); err != nil {
		t.Fatalf("name Alice: %v", err)
	}
	addTestMember(t, s.pool, s.slug, s.actor, alice, RoleMember)

	a := createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "by actor a")
	b := createTestIssue(t, s.pool, s.slug, s.ident, alice, "by alice b")
	c := createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "by actor c")
	d := createTestIssue(t, s.pool, s.slug, s.ident, alice, "by alice d")
	return s, map[string]*Issue{"a": a, "b": b, "c": c, "d": d},
		map[string]string{"actor": s.actor, "alice": alice, "aliceEmail": aliceEmail}
}

// TestListIssuesCreatedByFilter: single UUID, multi-UUID OR,
// and empty = no filter. There is no "none" case: a "none"
// value is a validation error, covered by
// TestListIssuesCreatedByFilterInvalidValues.
func TestListIssuesCreatedByFilter(t *testing.T) {
	s, iss, ids := setupCreatedByFixture(t)

	assertIssueSet(t, "created_by=[actor]", listFiltered(t, s, ListIssuesInput{FilterCreatedBy: []string{ids["actor"]}}), iss["a"], iss["c"])
	assertIssueSet(t, "created_by=[alice]", listFiltered(t, s, ListIssuesInput{FilterCreatedBy: []string{ids["alice"]}}), iss["b"], iss["d"])
	assertIssueSet(t, "created_by=[actor,alice]", listFiltered(t, s, ListIssuesInput{FilterCreatedBy: []string{ids["actor"], ids["alice"]}}), iss["a"], iss["b"], iss["c"], iss["d"])
	assertIssueSet(t, "created_by empty", listFiltered(t, s, ListIssuesInput{}), iss["a"], iss["b"], iss["c"], iss["d"])
	// A well-formed UUID with no issues is an empty list, not an error.
	assertIssueSet(t, "created_by=[stranger]", listFiltered(t, s, ListIssuesInput{FilterCreatedBy: []string{"33333333-3333-3333-3333-333333333333"}}))
}

// TestListIssuesCreatedByFilterScopeAND: the visual author
// filter ANDs with the Cycle scope container, the Module
// scope container, and the Epics scope container, never
// replaces them — a foreign author inside a scope yields
// empty, the current author inside its own scope is
// unchanged.
func TestListIssuesCreatedByFilterScopeAND(t *testing.T) {
	s, iss, ids := setupCreatedByFixture(t)
	ctx := context.Background()

	c1, err := CreateCycle(ctx, s.pool, s.slug, s.ident, s.actor, CycleInput{
		Name: "Cycle One", StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), EndDate: time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CreateCycle c1: %v", err)
	}
	// c1 holds a (actor) and b (alice).
	if err := AddCycleIssues(ctx, s.pool, s.slug, s.ident, s.actor, c1.ID, []string{iss["a"].ID, iss["b"].ID}); err != nil {
		t.Fatalf("AddCycleIssues c1: %v", err)
	}
	m1, err := CreateModule(ctx, s.pool, s.slug, s.ident, s.actor, ModuleInput{Name: "Module One"})
	if err != nil {
		t.Fatalf("CreateModule m1: %v", err)
	}
	// m1 holds a (actor) and d (alice).
	if err := AddModuleIssues(ctx, s.pool, s.slug, s.ident, s.actor, m1.ID, []string{iss["a"].ID, iss["d"].ID}); err != nil {
		t.Fatalf("AddModuleIssues m1: %v", err)
	}
	epic, err := CreateEpic(ctx, s.pool, s.slug, s.ident, s.actor, EpicInput{Name: "combo epic"})
	if err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	// epic holds b (alice) and c (actor).
	if err := AddEpicIssues(ctx, s.pool, s.slug, s.ident, s.actor, epic.ID, []string{iss["b"].ID, iss["c"].ID}); err != nil {
		t.Fatalf("AddEpicIssues: %v", err)
	}

	// Scope c1 + filter [alice]: b only (a is in c1 but by actor).
	assertIssueSet(t, "scope c1 + filter alice",
		listFiltered(t, s, ListIssuesInput{Cycle: c1.ID, FilterCreatedBy: []string{ids["alice"]}}), iss["b"])
	// Scope c1 + filter [stranger]: empty (neither c1 issue is by stranger).
	assertIssueSet(t, "scope c1 + filter stranger",
		listFiltered(t, s, ListIssuesInput{Cycle: c1.ID, FilterCreatedBy: []string{"33333333-3333-3333-3333-333333333333"}}))
	// Scope m1 + filter [actor]: a only (d is in m1 but by alice).
	assertIssueSet(t, "scope m1 + filter actor",
		listFiltered(t, s, ListIssuesInput{Module: m1.ID, FilterCreatedBy: []string{ids["actor"]}}), iss["a"])
	// Scope epic + filter [alice]: b only (c is in the epic but by actor).
	assertIssueSet(t, "scope epic + filter alice",
		listFiltered(t, s, ListIssuesInput{Epics: []string{epic.ID}, FilterCreatedBy: []string{ids["alice"]}}), iss["b"])
	// Cross-dimension AND: created_by x releases visual filters.
	// (Release attachment needs a release; attach one to a only.)
	rel, err := CreateRelease(ctx, s.pool, s.slug, s.ident, s.actor, ReleaseInput{Name: "CB Release"})
	if err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	if err := AssignReleaseIssues(ctx, s.pool, s.slug, s.ident, s.actor, rel.ID, []string{iss["a"].ID}); err != nil {
		t.Fatalf("AssignReleaseIssues: %v", err)
	}
	assertIssueSet(t, "created_by x releases",
		listFiltered(t, s, ListIssuesInput{FilterCreatedBy: []string{ids["actor"]}, FilterReleases: []string{rel.ID}}), iss["a"])
}

// TestListIssuesCreatedByFilterInvalidValues: malformed UUIDs
// AND the "none" sentinel are rejected with
// ErrInvalidListFilter — "none" is not a valid author filter
// because created_by is NOT NULL.
func TestListIssuesCreatedByFilterInvalidValues(t *testing.T) {
	s := setupListTest(t)
	ctx := context.Background()
	createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "one")

	for _, tc := range []struct {
		label string
		ids   []string
	}{
		{"malformed uuid", []string{"not-a-uuid"}},
		{"none", []string{"none"}},
		{"mixed uuid + none", []string{s.actor, "none"}},
	} {
		if _, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor, ListIssuesInput{FilterCreatedBy: tc.ids}); !errors.Is(err, ErrInvalidListFilter) {
			t.Fatalf("ListIssues created_by %s: err = %v, want ErrInvalidListFilter", tc.label, err)
		}
	}
}

// TestListIssuesCreatedByFilterPQLExclusivity: in PQL mode the
// visual author filter is ignored (withoutVisualFilters zeroes
// it), exactly like the other visual dimensions — the C117
// precedent for FilterReleases.
func TestListIssuesCreatedByFilterPQLExclusivity(t *testing.T) {
	s, iss, ids := setupCreatedByFixture(t)
	assertIssueSet(t, "pql ignores visual created_by filter",
		listFiltered(t, s, ListIssuesInput{
			PQL:             "priority >= 0",
			FilterCreatedBy: []string{ids["alice"]},
		}), iss["a"], iss["b"], iss["c"], iss["d"])
}
