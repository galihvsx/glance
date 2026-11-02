package service

// C120T0: visual ?mentions= list filter on GET
// /api/v1/workspaces/:slug/projects/:identifier/issues. The
// dimension rides the persisted issue_mentions index
// (migration 000082) via an EXISTS cond — never a join.
// Values are mentioned-user UUIDs, OR-matched; the "none"
// sentinel matches issues with NO mention rows (NOT EXISTS),
// mirroring the Assignees none shape exactly — NOT the
// created_by rejection shape. Malformed UUIDs are rejected
// by validateListFilterValues (ErrInvalidListFilter), never
// silently dropped. The filter ANDs with a detail-page
// scope (Cycle) and is zeroed by withoutVisualFilters in
// PQL mode. Fixture rows are inserted directly into
// issue_mentions (the write path that populates them is
// covered by issue_mentions_c120t0_test.go).

import (
	"context"
	"errors"
	"testing"
	"time"
)

// setupMentionsFixture builds three issues: A and C mention
// bob (via live comments), B mentions nobody.
func setupMentionsFixture(t *testing.T) (listTestSetup, map[string]*Issue, string) {
	t.Helper()
	s := setupListTest(t)
	ctx := context.Background()
	bob := createTestUser(t, s.pool, uniqueTestEmail("c120-bob"))
	addTestMember(t, s.pool, s.slug, s.actor, bob, RoleMember)

	a := createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "m-A")
	b := createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "m-B")
	c := createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "m-C")
	mention := func(iss *Issue) {
		t.Helper()
		var commentID string
		if err := s.pool.QueryRow(ctx,
			`INSERT INTO comments (issue_id, actor_id, content) VALUES ($1::uuid, $2::uuid, '{}'::jsonb) RETURNING id::text`,
			iss.ID, s.actor).Scan(&commentID); err != nil {
			t.Fatalf("insert comment: %v", err)
		}
		if _, err := s.pool.Exec(ctx,
			`INSERT INTO issue_mentions (issue_id, user_id, comment_id) VALUES ($1::uuid, $2::uuid, $3::uuid)`,
			iss.ID, bob, commentID); err != nil {
			t.Fatalf("insert issue_mentions: %v", err)
		}
	}
	mention(a)
	mention(c)
	return s, map[string]*Issue{"a": a, "b": b, "c": c}, bob
}

func TestListIssuesMentionsFilter(t *testing.T) {
	s, iss, bob := setupMentionsFixture(t)

	assertIssueSet(t, "mentions=[bob]", listFiltered(t, s, ListIssuesInput{FilterMentions: []string{bob}}), iss["a"], iss["c"])
	assertIssueSet(t, "mentions=[none]", listFiltered(t, s, ListIssuesInput{FilterMentions: []string{"none"}}), iss["b"])
	assertIssueSet(t, "mentions=[bob,none]", listFiltered(t, s, ListIssuesInput{FilterMentions: []string{bob, "none"}}), iss["a"], iss["b"], iss["c"])
	assertIssueSet(t, "mentions empty", listFiltered(t, s, ListIssuesInput{}), iss["a"], iss["b"], iss["c"])
	// An unknown-but-well-formed UUID matches nothing — never a 400.
	assertIssueSet(t, "mentions=[unknown uuid]", listFiltered(t, s, ListIssuesInput{FilterMentions: []string{"11111111-1111-1111-1111-111111111111"}}))
}

func TestListIssuesMentionsFilterScopeAND(t *testing.T) {
	s, iss, bob := setupMentionsFixture(t)
	ctx := context.Background()

	c1, err := CreateCycle(ctx, s.pool, s.slug, s.ident, s.actor, CycleInput{
		Name: "Cycle One", StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), EndDate: time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CreateCycle c1: %v", err)
	}
	// c1 holds the mentioned A and the unmentioned B only.
	if err := AddCycleIssues(ctx, s.pool, s.slug, s.ident, s.actor, c1.ID, []string{iss["a"].ID, iss["b"].ID}); err != nil {
		t.Fatalf("AddCycleIssues c1: %v", err)
	}

	assertIssueSet(t, "scope c1 + filter bob",
		listFiltered(t, s, ListIssuesInput{Cycle: c1.ID, FilterMentions: []string{bob}}), iss["a"])
	assertIssueSet(t, "scope c1 + filter none",
		listFiltered(t, s, ListIssuesInput{Cycle: c1.ID, FilterMentions: []string{"none"}}), iss["b"])
}

func TestListIssuesMentionsFilterInvalidValues(t *testing.T) {
	s := setupListTest(t)
	ctx := context.Background()
	createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "one")
	bob := createTestUser(t, s.pool, uniqueTestEmail("c120-bob2"))

	for _, tc := range []struct {
		label string
		vals  []string
	}{
		{"malformed", []string{"not-a-uuid"}},
		{"mixed valid + malformed", []string{bob, "bogus"}},
		{"empty string", []string{""}},
	} {
		if _, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor, ListIssuesInput{FilterMentions: tc.vals}); !errors.Is(err, ErrInvalidListFilter) {
			t.Fatalf("ListIssues mentions %s: err = %v, want ErrInvalidListFilter", tc.label, err)
		}
	}
	// "none" alone is NOT an error — it is the sentinel.
	if _, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor, ListIssuesInput{FilterMentions: []string{"none"}}); err != nil {
		t.Fatalf("ListIssues mentions=[none]: err = %v, want nil", err)
	}
}

func TestListIssuesMentionsFilterPQLExclusivity(t *testing.T) {
	s, iss, bob := setupMentionsFixture(t)
	assertIssueSet(t, "pql ignores visual mentions filter",
		listFiltered(t, s, ListIssuesInput{
			PQL:            "priority >= 0",
			FilterMentions: []string{bob},
		}), iss["a"], iss["b"], iss["c"])
}

func TestWithoutVisualFiltersZeroesMentions(t *testing.T) {
	in := ListIssuesInput{
		FilterMentions:    []string{"x"},
		FilterStateGroups: []string{"started"},
		Cycle:             "cycle-scope",
	}
	got := in.withoutVisualFilters()
	if len(got.FilterMentions) != 0 {
		t.Fatalf("withoutVisualFilters: FilterMentions = %v, want empty", got.FilterMentions)
	}
	if got.Cycle != "cycle-scope" {
		t.Fatalf("withoutVisualFilters: Cycle = %q, want scope preserved", got.Cycle)
	}
}
