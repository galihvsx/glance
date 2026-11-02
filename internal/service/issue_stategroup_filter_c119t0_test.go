package service

// C119T0: visual ?state_group= list filter on GET
// /api/v1/workspaces/:slug/projects/:identifier/issues. The
// dimension rides the issue's state row (states."group",
// migration 000006) via an EXISTS cond — NOT a UUID filter:
// values are group SLUGS from the closed five-group
// vocabulary (backlog, unstarted, started, completed,
// cancelled), OR-matched, case-insensitive input normalized
// to lowercase. There is NO "none" sentinel (every issue
// has a state and every state has a group) and glance's
// own "triage" group is NOT filterable (the cycle-69
// webhook-filter scope decision) — unknown, triage, and
// none values are rejected by validateListFilterValues
// (ErrInvalidListFilter), never silently dropped. There
// is no singular ?state_group= scope param, so the filter
// ANDs with any detail-page scope (cycle / module / epic)
// exactly like FilterCreatedBy.

import (
	"context"
	"errors"
	"testing"
	"time"
)

// setupStateGroupFixture builds one issue per default state
// group: backlog (Backlog), unstarted (Todo), started (In
// Progress), completed (Done), cancelled (Cancelled).
func setupStateGroupFixture(t *testing.T) (listTestSetup, map[string]*Issue) {
	t.Helper()
	s := setupListTest(t)
	byName := map[string]string{
		"backlog":   pqlStateID(t, s.pool, s.slug, s.ident, s.actor, "Backlog"),
		"unstarted": pqlStateID(t, s.pool, s.slug, s.ident, s.actor, "Todo"),
		"started":   pqlStateID(t, s.pool, s.slug, s.ident, s.actor, "In Progress"),
		"completed": pqlStateID(t, s.pool, s.slug, s.ident, s.actor, "Done"),
		"cancelled": pqlStateID(t, s.pool, s.slug, s.ident, s.actor, "Cancelled"),
	}
	iss := map[string]*Issue{}
	for group, stateID := range byName {
		sid := stateID
		iss[group] = pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "in-"+group, CreateIssueInput{StateID: &sid})
	}
	return s, iss
}

func TestListIssuesStateGroupFilter(t *testing.T) {
	s, iss := setupStateGroupFixture(t)

	assertIssueSet(t, "state_group=[started]", listFiltered(t, s, ListIssuesInput{FilterStateGroups: []string{"started"}}), iss["started"])
	assertIssueSet(t, "state_group=[backlog]", listFiltered(t, s, ListIssuesInput{FilterStateGroups: []string{"backlog"}}), iss["backlog"])
	assertIssueSet(t, "state_group=[started,completed]", listFiltered(t, s, ListIssuesInput{FilterStateGroups: []string{"started", "completed"}}), iss["started"], iss["completed"])
	assertIssueSet(t, "state_group empty", listFiltered(t, s, ListIssuesInput{}), iss["backlog"], iss["unstarted"], iss["started"], iss["completed"], iss["cancelled"])
	// Case-insensitive input is normalized to lowercase by
	// the service layer (issueListConds), so a direct
	// caller passing "Started" matches exactly like "started".
	assertIssueSet(t, "state_group=[Started]", listFiltered(t, s, ListIssuesInput{FilterStateGroups: []string{"Started"}}), iss["started"])
	assertIssueSet(t, "state_group=[COMPLETED]", listFiltered(t, s, ListIssuesInput{FilterStateGroups: []string{"COMPLETED"}}), iss["completed"])
}

// TestListIssuesStateGroupFilterScopeAND: the visual
// state-group filter ANDs with the Cycle scope container,
// never replaces it — a started issue outside the cycle
// is excluded, a backlog issue inside it is excluded.
func TestListIssuesStateGroupFilterScopeAND(t *testing.T) {
	s, iss := setupStateGroupFixture(t)
	ctx := context.Background()

	c1, err := CreateCycle(ctx, s.pool, s.slug, s.ident, s.actor, CycleInput{
		Name: "Cycle One", StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), EndDate: time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CreateCycle c1: %v", err)
	}
	// c1 holds the started and backlog issues only.
	if err := AddCycleIssues(ctx, s.pool, s.slug, s.ident, s.actor, c1.ID, []string{iss["started"].ID, iss["backlog"].ID}); err != nil {
		t.Fatalf("AddCycleIssues c1: %v", err)
	}

	assertIssueSet(t, "scope c1 + filter started",
		listFiltered(t, s, ListIssuesInput{Cycle: c1.ID, FilterStateGroups: []string{"started"}}), iss["started"])
	assertIssueSet(t, "scope c1 + filter completed",
		listFiltered(t, s, ListIssuesInput{Cycle: c1.ID, FilterStateGroups: []string{"completed"}}))
}

// TestListIssuesStateGroupFilterInvalidValues: unknown
// slugs, triage, and "none" are rejected with
// ErrInvalidListFilter — never a silent empty list. A
// mixed valid + invalid list is rejected wholesale.
func TestListIssuesStateGroupFilterInvalidValues(t *testing.T) {
	s := setupListTest(t)
	ctx := context.Background()
	createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "one")

	for _, tc := range []struct {
		label  string
		groups []string
	}{
		{"unknown slug", []string{"bogus"}},
		{"triage", []string{"triage"}},
		{"TRIAGE", []string{"TRIAGE"}},
		{"none", []string{"none"}},
		{"mixed valid + none", []string{"started", "none"}},
		{"mixed valid + unknown", []string{"started", "bogus"}},
		{"empty string", []string{""}},
	} {
		if _, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor, ListIssuesInput{FilterStateGroups: tc.groups}); !errors.Is(err, ErrInvalidListFilter) {
			t.Fatalf("ListIssues state_group %s: err = %v, want ErrInvalidListFilter", tc.label, err)
		}
	}
}

// TestListIssuesStateGroupFilterPQLExclusivity: in PQL mode
// the visual state-group filter is ignored
// (withoutVisualFilters zeroes it), exactly like the other
// visual dimensions — the C118 precedent for
// FilterCreatedBy. The unit check pins the zeroing itself
// (scope containers survive it).
func TestListIssuesStateGroupFilterPQLExclusivity(t *testing.T) {
	s, iss := setupStateGroupFixture(t)
	assertIssueSet(t, "pql ignores visual state_group filter",
		listFiltered(t, s, ListIssuesInput{
			PQL:               "priority >= 0",
			FilterStateGroups: []string{"started"},
		}), iss["backlog"], iss["unstarted"], iss["started"], iss["completed"], iss["cancelled"])
}

func TestWithoutVisualFiltersZeroesStateGroups(t *testing.T) {
	in := ListIssuesInput{
		FilterStateGroups: []string{"started"},
		FilterCreatedBy:   []string{"x"},
		Cycle:             "cycle-scope",
	}
	got := in.withoutVisualFilters()
	if len(got.FilterStateGroups) != 0 {
		t.Fatalf("withoutVisualFilters: FilterStateGroups = %v, want empty", got.FilterStateGroups)
	}
	if got.Cycle != "cycle-scope" {
		t.Fatalf("withoutVisualFilters: Cycle = %q, want scope preserved", got.Cycle)
	}
}
