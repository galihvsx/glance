package service

// C75T0: parent + epic filters on GET
// /api/v1/workspaces/:slug/projects/:identifier/issues. parent matches
// on issues.parent_id (direct column, "none" = top-level); epic matches
// via the epic_issues junction (migration 000069, "none" = in no epic).
// Real test database, no skips.

import (
	"context"
	"errors"
	"testing"
)

// setupParentEpicFixture builds a small tree: parentA with two children,
// parentB with one child, and one lone top-level issue.
func setupParentEpicFixture(t *testing.T) (listTestSetup, map[string]*Issue) {
	t.Helper()
	s := setupListTest(t)
	ctx := context.Background()

	parentA := createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "parent A")
	parentB := createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "parent B")
	childA1 := createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "child A1")
	childA2 := createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "child A2")
	childB1 := createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "child B1")
	lone := createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "lone")
	for _, c := range []*Issue{childA1, childA2} {
		if _, err := SetParent(ctx, s.pool, s.slug, s.ident, c.ID, s.actor, parentA.ID); err != nil {
			t.Fatalf("SetParent %s -> A: %v", c.ID, err)
		}
	}
	if _, err := SetParent(ctx, s.pool, s.slug, s.ident, childB1.ID, s.actor, parentB.ID); err != nil {
		t.Fatalf("SetParent B1 -> B: %v", err)
	}
	return s, map[string]*Issue{
		"parentA": parentA, "parentB": parentB,
		"childA1": childA1, "childA2": childA2, "childB1": childB1,
		"lone": lone,
	}
}

func issueIDSet(items []IssueListItem) map[string]bool {
	m := map[string]bool{}
	for _, it := range items {
		m[it.ID] = true
	}
	return m
}

// TestListIssuesParentFilter: single parent match, OR across parents,
// "none" = top-level issues, and uuids+"none" = the whole working set.
func TestListIssuesParentFilter(t *testing.T) {
	s, iss := setupParentEpicFixture(t)
	ctx := context.Background()

	byA, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{Parents: []string{iss["parentA"].ID}})
	if err != nil {
		t.Fatalf("ListIssues parent: %v", err)
	}
	got := issueIDSet(byA.Issues)
	if len(got) != 2 || !got[iss["childA1"].ID] || !got[iss["childA2"].ID] {
		t.Fatalf("parent filter: got %v, want only childA1+childA2", got)
	}

	byBoth, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{Parents: []string{iss["parentA"].ID, iss["parentB"].ID}})
	if err != nil {
		t.Fatalf("ListIssues parents OR: %v", err)
	}
	got = issueIDSet(byBoth.Issues)
	if len(got) != 3 || !got[iss["childA1"].ID] || !got[iss["childA2"].ID] || !got[iss["childB1"].ID] {
		t.Fatalf("parents OR filter: got %v, want the 3 children", got)
	}

	byNone, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{Parents: []string{"none"}})
	if err != nil {
		t.Fatalf("ListIssues parent=none: %v", err)
	}
	got = issueIDSet(byNone.Issues)
	if len(got) != 3 || !got[iss["parentA"].ID] || !got[iss["parentB"].ID] || !got[iss["lone"].ID] {
		t.Fatalf("parent=none filter: got %v, want the 3 top-level issues", got)
	}

	byAll, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{Parents: []string{iss["parentA"].ID, "none"}})
	if err != nil {
		t.Fatalf("ListIssues parent uuids+none: %v", err)
	}
	if len(byAll.Issues) != 5 {
		t.Fatalf("parent uuids+none filter: got %d issues, want 5 (2 children of A + 3 top-level)", len(byAll.Issues))
	}
}

// TestListIssuesEpicFilter: junction match, OR across epics, "none" =
// issues in no epic, and uuids+"none" = the whole working set.
func TestListIssuesEpicFilter(t *testing.T) {
	s := setupListTest(t)
	ctx := context.Background()

	epic1, err := CreateEpic(ctx, s.pool, s.slug, s.ident, s.actor, EpicInput{Name: "epic one"})
	if err != nil {
		t.Fatalf("CreateEpic 1: %v", err)
	}
	epic2, err := CreateEpic(ctx, s.pool, s.slug, s.ident, s.actor, EpicInput{Name: "epic two"})
	if err != nil {
		t.Fatalf("CreateEpic 2: %v", err)
	}
	x := createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "in epic1")
	y := createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "in both epics")
	z := createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "in no epic")
	if err := AddEpicIssues(ctx, s.pool, s.slug, s.ident, s.actor, epic1.ID, []string{x.ID, y.ID}); err != nil {
		t.Fatalf("AddEpicIssues epic1: %v", err)
	}
	if err := AddEpicIssues(ctx, s.pool, s.slug, s.ident, s.actor, epic2.ID, []string{y.ID}); err != nil {
		t.Fatalf("AddEpicIssues epic2: %v", err)
	}

	byE1, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{Epics: []string{epic1.ID}})
	if err != nil {
		t.Fatalf("ListIssues epic: %v", err)
	}
	got := issueIDSet(byE1.Issues)
	if len(got) != 2 || !got[x.ID] || !got[y.ID] {
		t.Fatalf("epic filter: got %v, want x+y", got)
	}

	byE2, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{Epics: []string{epic2.ID}})
	if err != nil {
		t.Fatalf("ListIssues epic2: %v", err)
	}
	got = issueIDSet(byE2.Issues)
	if len(got) != 1 || !got[y.ID] {
		t.Fatalf("epic2 filter: got %v, want only y", got)
	}

	byBoth, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{Epics: []string{epic1.ID, epic2.ID}})
	if err != nil {
		t.Fatalf("ListIssues epics OR: %v", err)
	}
	got = issueIDSet(byBoth.Issues)
	if len(got) != 2 || !got[x.ID] || !got[y.ID] {
		t.Fatalf("epics OR filter: got %v, want x+y", got)
	}

	byNone, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{Epics: []string{"none"}})
	if err != nil {
		t.Fatalf("ListIssues epic=none: %v", err)
	}
	got = issueIDSet(byNone.Issues)
	if len(got) != 1 || !got[z.ID] {
		t.Fatalf("epic=none filter: got %v, want only z", got)
	}

	byAll, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{Epics: []string{epic1.ID, "none"}})
	if err != nil {
		t.Fatalf("ListIssues epic uuids+none: %v", err)
	}
	if len(byAll.Issues) != 3 {
		t.Fatalf("epic uuids+none filter: got %d issues, want 3", len(byAll.Issues))
	}
}

// TestListIssuesParentEpicCombos: parent/epic ANDed with each other and
// with existing filters (assignee, state); archived and deleted rows
// stay out of the filtered set (archived visible only with Archived: true).
func TestListIssuesParentEpicCombos(t *testing.T) {
	s, iss := setupParentEpicFixture(t)
	ctx := context.Background()

	epic, err := CreateEpic(ctx, s.pool, s.slug, s.ident, s.actor, EpicInput{Name: "combo epic"})
	if err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	childA1 := iss["childA1"]
	if err := AddEpicIssues(ctx, s.pool, s.slug, s.ident, s.actor, epic.ID, []string{childA1.ID}); err != nil {
		t.Fatalf("AddEpicIssues: %v", err)
	}
	if err := AssignAssignee(ctx, s.pool, s.slug, s.ident, childA1.ID, s.actor, s.actor); err != nil {
		t.Fatalf("AssignAssignee: %v", err)
	}

	// parent x assignee.
	byPA, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{Parents: []string{iss["parentA"].ID}, Assignees: []string{s.actor}})
	if err != nil {
		t.Fatalf("ListIssues parent x assignee: %v", err)
	}
	got := issueIDSet(byPA.Issues)
	if len(got) != 1 || !got[childA1.ID] {
		t.Fatalf("parent x assignee: got %v, want only childA1", got)
	}

	// parent x epic.
	byPE, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{Parents: []string{iss["parentA"].ID}, Epics: []string{epic.ID}})
	if err != nil {
		t.Fatalf("ListIssues parent x epic: %v", err)
	}
	got = issueIDSet(byPE.Issues)
	if len(got) != 1 || !got[childA1.ID] {
		t.Fatalf("parent x epic: got %v, want only childA1", got)
	}

	// epic x state: childA1 is in epic and in the default state.
	defState := iss["parentA"].StateID
	byES, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{Epics: []string{epic.ID}, State: defState})
	if err != nil {
		t.Fatalf("ListIssues epic x state: %v", err)
	}
	got = issueIDSet(byES.Issues)
	if len(got) != 1 || !got[childA1.ID] {
		t.Fatalf("epic x state: got %v, want only childA1", got)
	}

	// Archived child: excluded by the parent filter, visible with
	// Archived: true (archived/trash exclusion unchanged by C75T0).
	if _, err := s.pool.Exec(ctx, `UPDATE issues SET archived_at = now() WHERE id = $1::uuid`, childA1.ID); err != nil {
		t.Fatalf("archive: %v", err)
	}
	byA, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{Parents: []string{iss["parentA"].ID}})
	if err != nil {
		t.Fatalf("ListIssues parent after archive: %v", err)
	}
	got = issueIDSet(byA.Issues)
	if got[childA1.ID] || len(got) != 1 || !got[iss["childA2"].ID] {
		t.Fatalf("parent filter leaked archived child: got %v", got)
	}
	byArch, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{Parents: []string{iss["parentA"].ID}, Archived: true})
	if err != nil {
		t.Fatalf("ListIssues parent archived: %v", err)
	}
	got = issueIDSet(byArch.Issues)
	if len(got) != 2 || !got[childA1.ID] || !got[iss["childA2"].ID] {
		t.Fatalf("parent+archived: got %v, want childA1+childA2", got)
	}

	// Deleted child: invisible to the parent filter and unrecoverable
	// through it (trash mode is the only way to see deleted rows).
	if err := DeleteIssue(ctx, s.pool, s.slug, s.ident, iss["childA2"].ID, s.actor); err != nil {
		t.Fatalf("DeleteIssue: %v", err)
	}
	byA2, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{Parents: []string{iss["parentA"].ID}, Archived: true})
	if err != nil {
		t.Fatalf("ListIssues parent after delete: %v", err)
	}
	got = issueIDSet(byA2.Issues)
	if got[iss["childA2"].ID] {
		t.Fatalf("parent filter leaked deleted child: got %v", got)
	}
}

// TestListIssuesParentEpicEmptyShape: a well-formed filter that matches
// nothing answers 200 with results: [] — never null (list-endpoint
// discipline).
func TestListIssuesParentEpicEmptyShape(t *testing.T) {
	s := setupListTest(t)
	ctx := context.Background()
	createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "some issue")

	for name, in := range map[string]ListIssuesInput{
		"parent": {Parents: []string{"00000000-0000-0000-0000-000000000000"}},
		"epic":   {Epics: []string{"00000000-0000-0000-0000-000000000000"}},
	} {
		res, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor, in)
		if err != nil {
			t.Fatalf("ListIssues %s unknown uuid: %v", name, err)
		}
		if res.Issues == nil {
			t.Fatalf("ListIssues %s unknown uuid: Issues is nil, want empty slice", name)
		}
		if len(res.Issues) != 0 {
			t.Fatalf("ListIssues %s unknown uuid: got %d issues, want 0", name, len(res.Issues))
		}
	}
}

// TestListIssuesParentEpicInvalidValues: malformed UUIDs are rejected
// with ErrInvalidListFilter (the handler maps it to 400, C73/C74 ruling).
// "none" is a legal sentinel for both, not a malformed UUID.
func TestListIssuesParentEpicInvalidValues(t *testing.T) {
	s := setupListTest(t)
	ctx := context.Background()
	createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "one")

	for name, in := range map[string]ListIssuesInput{
		"parent": {Parents: []string{"not-a-uuid"}},
		"epic":   {Epics: []string{"not-a-uuid"}},
	} {
		if _, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor, in); !errors.Is(err, ErrInvalidListFilter) {
			t.Fatalf("ListIssues %s malformed uuid: err = %v, want ErrInvalidListFilter", name, err)
		}
	}

	// "none" alone must pass validation (empty result, no error).
	for name, in := range map[string]ListIssuesInput{
		"parent": {Parents: []string{"none"}},
		"epic":   {Epics: []string{"none"}},
	} {
		if _, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor, in); err != nil {
			t.Fatalf("ListIssues %s=none: %v", name, err)
		}
	}
}
