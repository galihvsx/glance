package service

// C117T0: visual ?releases= list filter on GET
// /api/v1/workspaces/:slug/projects/:identifier/issues. The
// dimension rides the DIRECT issues.release_id column (C19T3),
// not a junction: UUIDs OR-matched, "none" = in no release
// (release_id IS NULL). There is no singular ?release= scope
// param, so the filter ANDs with any detail-page scope
// (cycle / module / epic) exactly like FilterCycles /
// FilterModules.

import (
	"context"
	"errors"
	"testing"
	"time"
)

// setupReleaseFixture builds: releases r1, r2; a in r1, b in
// r2, c in neither, d in r1. All four share the default state.
func setupReleaseFixture(t *testing.T) (listTestSetup, map[string]*Issue, map[string]string) {
	t.Helper()
	s := setupListTest(t)
	ctx := context.Background()

	r1, err := CreateRelease(ctx, s.pool, s.slug, s.ident, s.actor, ReleaseInput{Name: "Release One"})
	if err != nil {
		t.Fatalf("CreateRelease r1: %v", err)
	}
	r2, err := CreateRelease(ctx, s.pool, s.slug, s.ident, s.actor, ReleaseInput{Name: "Release Two"})
	if err != nil {
		t.Fatalf("CreateRelease r2: %v", err)
	}

	a := createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "in r1")
	b := createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "in r2")
	c := createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "in neither")
	d := createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "in r1 too")
	if err := AssignReleaseIssues(ctx, s.pool, s.slug, s.ident, s.actor, r1.ID, []string{a.ID, d.ID}); err != nil {
		t.Fatalf("AssignReleaseIssues r1: %v", err)
	}
	if err := AssignReleaseIssues(ctx, s.pool, s.slug, s.ident, s.actor, r2.ID, []string{b.ID}); err != nil {
		t.Fatalf("AssignReleaseIssues r2: %v", err)
	}
	return s, map[string]*Issue{"a": a, "b": b, "c": c, "d": d},
		map[string]string{"r1": r1.ID, "r2": r2.ID}
}

// TestListIssuesReleaseFilter: single UUID, multi-UUID OR,
// "none" only, mixed UUIDs + "none", and empty = no filter.
func TestListIssuesReleaseFilter(t *testing.T) {
	s, iss, ids := setupReleaseFixture(t)

	assertIssueSet(t, "releases=[r1]", listFiltered(t, s, ListIssuesInput{FilterReleases: []string{ids["r1"]}}), iss["a"], iss["d"])
	assertIssueSet(t, "releases=[r1,r2]", listFiltered(t, s, ListIssuesInput{FilterReleases: []string{ids["r1"], ids["r2"]}}), iss["a"], iss["b"], iss["d"])
	assertIssueSet(t, "releases=[none]", listFiltered(t, s, ListIssuesInput{FilterReleases: []string{"none"}}), iss["c"])
	assertIssueSet(t, "releases=[r1,none]", listFiltered(t, s, ListIssuesInput{FilterReleases: []string{ids["r1"], "none"}}), iss["a"], iss["c"], iss["d"])
	assertIssueSet(t, "releases empty", listFiltered(t, s, ListIssuesInput{}), iss["a"], iss["b"], iss["c"], iss["d"])
}

// TestListIssuesReleaseFilterScopeAND: the visual release
// filter ANDs with the Cycle scope container and the Epics
// scope container, never replaces them — a foreign release
// inside a scope yields empty, the current release inside its
// own scope is unchanged.
func TestListIssuesReleaseFilterScopeAND(t *testing.T) {
	s, iss, ids := setupReleaseFixture(t)
	ctx := context.Background()

	c1, err := CreateCycle(ctx, s.pool, s.slug, s.ident, s.actor, CycleInput{
		Name: "Cycle One", StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), EndDate: time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CreateCycle c1: %v", err)
	}
	// c1 holds a (in r1) and c (in neither).
	if err := AddCycleIssues(ctx, s.pool, s.slug, s.ident, s.actor, c1.ID, []string{iss["a"].ID, iss["c"].ID}); err != nil {
		t.Fatalf("AddCycleIssues c1: %v", err)
	}
	epic, err := CreateEpic(ctx, s.pool, s.slug, s.ident, s.actor, EpicInput{Name: "combo epic"})
	if err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	// epic holds a (in r1) and b (in r2).
	if err := AddEpicIssues(ctx, s.pool, s.slug, s.ident, s.actor, epic.ID, []string{iss["a"].ID, iss["b"].ID}); err != nil {
		t.Fatalf("AddEpicIssues: %v", err)
	}

	// Scope c1 + filter [r1]: a only (c is in c1 but no release).
	assertIssueSet(t, "scope c1 + filter r1",
		listFiltered(t, s, ListIssuesInput{Cycle: c1.ID, FilterReleases: []string{ids["r1"]}}), iss["a"])
	// Scope c1 + filter [r2]: empty (b is in r2 but not in c1).
	assertIssueSet(t, "scope c1 + filter r2",
		listFiltered(t, s, ListIssuesInput{Cycle: c1.ID, FilterReleases: []string{ids["r2"]}}))
	// Scope c1 + filter [none]: c only.
	assertIssueSet(t, "scope c1 + filter none",
		listFiltered(t, s, ListIssuesInput{Cycle: c1.ID, FilterReleases: []string{"none"}}), iss["c"])
	// Scope epic + filter [r1]: a only (b is in the epic but in r2).
	assertIssueSet(t, "scope epic + filter r1",
		listFiltered(t, s, ListIssuesInput{Epics: []string{epic.ID}, FilterReleases: []string{ids["r1"]}}), iss["a"])
	// Scope epic + filter [none]: empty (both epic issues have a release).
	assertIssueSet(t, "scope epic + filter none",
		listFiltered(t, s, ListIssuesInput{Epics: []string{epic.ID}, FilterReleases: []string{"none"}}))
	// Cross-dimension AND: releases x cycles visual filters.
	assertIssueSet(t, "releases x cycles",
		listFiltered(t, s, ListIssuesInput{FilterReleases: []string{ids["r1"]}, FilterCycles: []string{c1.ID}}), iss["a"])
}

// TestListIssuesReleaseFilterInvalidValues: malformed UUIDs are
// rejected with ErrInvalidListFilter; "none" passes validation.
func TestListIssuesReleaseFilterInvalidValues(t *testing.T) {
	s := setupListTest(t)
	ctx := context.Background()
	createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "one")

	if _, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor, ListIssuesInput{FilterReleases: []string{"not-a-uuid"}}); !errors.Is(err, ErrInvalidListFilter) {
		t.Fatalf("ListIssues releases malformed uuid: err = %v, want ErrInvalidListFilter", err)
	}
	if _, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor, ListIssuesInput{FilterReleases: []string{"none"}}); err != nil {
		t.Fatalf("ListIssues releases=none: %v", err)
	}
}

// TestListIssuesReleaseFilterPQLExclusivity: in PQL mode the
// visual release filter is ignored (withoutVisualFilters zeroes
// it), exactly like the other visual dimensions — the C116
// precedent for FilterCycles / FilterModules.
func TestListIssuesReleaseFilterPQLExclusivity(t *testing.T) {
	s, iss, ids := setupReleaseFixture(t)
	assertIssueSet(t, "pql ignores visual release filter",
		listFiltered(t, s, ListIssuesInput{
			PQL:            "priority >= 0",
			FilterReleases: []string{ids["r1"]},
		}), iss["a"], iss["b"], iss["c"], iss["d"])
}
