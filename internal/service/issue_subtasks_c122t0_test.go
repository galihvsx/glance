package service

// Batched direct-subtask rows (C122T0): ?include_subtasks=1 on
// ListIssues attaches each returned row's DIRECT children only —
// one level, sort_order ASC, assignees batched — as a non-nil
// array (empty for childless issues), never null. Flag off: no
// subtasks field and no extra queries (the existing
// TestListNoNPlusOne cap of 8 still holds untouched).

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

// createSubtaskTestIssue creates an issue with explicit parent,
// priority, and sort order for the subtask-row tests.
func createSubtaskTestIssue(t *testing.T, s listTestSetup, name string, parentID *string, priority int, sortOrder float64) *Issue {
	t.Helper()
	iss, err := CreateIssue(context.Background(), s.pool, s.slug, s.ident, s.actor,
		CreateIssueInput{Name: name, ParentID: parentID, Priority: &priority, SortOrder: &sortOrder})
	if err != nil {
		t.Fatalf("CreateIssue %q: %v", name, err)
	}
	return iss
}

func assignTestUser(t *testing.T, s listTestSetup, issueID, userID string) {
	t.Helper()
	if _, err := s.pool.Exec(context.Background(),
		`INSERT INTO issue_assignees (issue_id, user_id) VALUES ($1::uuid, $2::uuid) ON CONFLICT DO NOTHING`,
		issueID, userID); err != nil {
		t.Fatalf("assign %s to %s: %v", userID, issueID, err)
	}
}

func subtaskByParent(t *testing.T, items []IssueListItem) map[string][]SubtaskRow {
	t.Helper()
	out := make(map[string][]SubtaskRow, len(items))
	for i := range items {
		if items[i].Subtasks == nil {
			t.Fatalf("row %s has nil Subtasks with flag on (want [] when childless)", items[i].ID)
		}
		out[items[i].ID] = *items[i].Subtasks
	}
	return out
}

func TestListIncludeSubtasks(t *testing.T) {
	s := setupListTest(t)
	ctx := context.Background()

	// Parent P with two direct children (created out of sort
	// order to prove sort_order ASC) and one grandchild G under
	// child C1. C2 gets two assignees; C1 gets none.
	parent := createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "parent")
	c2 := createSubtaskTestIssue(t, s, "child-two", strptr(parent.ID), 3, 20)
	c1 := createSubtaskTestIssue(t, s, "child-one", strptr(parent.ID), 1, 10)
	grand := createSubtaskTestIssue(t, s, "grandchild", strptr(c1.ID), 4, 5)
	loner := createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "loner")
	// A second parent OUTSIDE the page: the state filter below
	// excludes it, so its child must be attached nowhere.
	otherState := ""
	states, err := ListStates(ctx, s.pool, s.slug, s.ident, s.actor)
	if err != nil {
		t.Fatalf("ListStates: %v", err)
	}
	for _, st := range states {
		if st.Group != "backlog" {
			otherState = st.ID
			break
		}
	}
	if otherState == "" {
		t.Fatal("no non-backlog state seeded")
	}
	outsider, err := CreateIssue(ctx, s.pool, s.slug, s.ident, s.actor,
		CreateIssueInput{Name: "outsider-parent", StateID: &otherState})
	if err != nil {
		t.Fatalf("CreateIssue outsider: %v", err)
	}
	outsiderChild := createSubtaskTestIssue(t, s, "outsider-child", strptr(outsider.ID), 2, 1)

	// Two assignees on C2 (actor + a second user), name-ordered
	// by the batch query like assigneesAgg.
	second := createTestUser(t, s.pool, uniqueTestEmail("subtask-assignee"))
	assignTestUser(t, s, c2.ID, s.actor)
	assignTestUser(t, s, c2.ID, second)

	// Flag on, filtered to the backlog state: outsider + its
	// child stay out of the page entirely.
	backlog := backlogStateID(t, s.pool, s.slug, s.ident, s.actor)
	r, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{PerPage: 100, State: backlog, IncludeSubtasks: true})
	if err != nil {
		t.Fatalf("ListIssues flag on: %v", err)
	}
	byID := subtaskByParent(t, r.Issues)

	// Parent: exactly its two direct children, sort_order ASC —
	// grandchild excluded (direct-only).
	kids := byID[parent.ID]
	if len(kids) != 2 {
		t.Fatalf("parent subtasks = %d rows, want 2", len(kids))
	}
	if kids[0].ID != c1.ID || kids[1].ID != c2.ID {
		t.Fatalf("parent subtasks = [%s %s], want [%s %s] (sort_order ASC)",
			kids[0].ID, kids[1].ID, c1.ID, c2.ID)
	}
	// Exact row shape: display id, title, state, priority string,
	// assignees, sort order.
	if kids[0].DisplayID != s.ident+"-"+fmt.Sprint(c1.SequenceID) {
		t.Fatalf("c1 display_id = %q", kids[0].DisplayID)
	}
	if kids[0].Title != "child-one" {
		t.Fatalf("c1 title = %q, want child-one", kids[0].Title)
	}
	if kids[0].StateID == nil || *kids[0].StateID != c1.StateID {
		t.Fatalf("c1 state_id = %+v, want %s", kids[0].StateID, c1.StateID)
	}
	if kids[0].Priority == nil || *kids[0].Priority != "low" {
		t.Fatalf("c1 priority = %+v, want \"low\" (int 1)", kids[0].Priority)
	}
	if kids[0].SortOrder != 10 {
		t.Fatalf("c1 sort_order = %v, want 10", kids[0].SortOrder)
	}
	if kids[0].AssigneeIDs == nil || len(kids[0].AssigneeIDs) != 0 {
		t.Fatalf("c1 assignee_ids = %v, want []", kids[0].AssigneeIDs)
	}
	if kids[1].Priority == nil || *kids[1].Priority != "high" {
		t.Fatalf("c2 priority = %+v, want \"high\" (int 3)", kids[1].Priority)
	}
	if len(kids[1].AssigneeIDs) != 2 {
		t.Fatalf("c2 assignee_ids = %v, want 2 batched assignees", kids[1].AssigneeIDs)
	}
	// Childless issue: empty array, never null.
	if len(byID[loner.ID]) != 0 {
		t.Fatalf("loner subtasks = %v, want []", byID[loner.ID])
	}
	// Direct-only per row: C1's own row carries ITS direct child
	// (the grandchild) — one level per row, no recursion into
	// the parent's array — and the grandchild's row is empty.
	if len(byID[c1.ID]) != 1 || byID[c1.ID][0].ID != grand.ID {
		t.Fatalf("c1 subtasks = %v, want [grandchild] (its direct child)", byID[c1.ID])
	}
	if len(byID[grand.ID]) != 0 {
		t.Fatalf("grandchild subtasks = %v, want []", byID[grand.ID])
	}
	// Children of parents outside the page are attached nowhere.
	for pid, rows := range byID {
		for _, row := range rows {
			if row.ID == outsiderChild.ID {
				t.Fatalf("outsider child attached under %s, want nowhere", pid)
			}
		}
	}
	if _, ok := byID[outsider.ID]; ok {
		t.Fatal("outsider parent leaked into the filtered page")
	}

	// Flag off: no subtasks field on any row (nil pointer), and
	// the JSON omits the key entirely.
	off, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{PerPage: 100, State: backlog})
	if err != nil {
		t.Fatalf("ListIssues flag off: %v", err)
	}
	for _, it := range off.Issues {
		if it.Subtasks != nil {
			t.Fatalf("row %s carries subtasks without the flag", it.ID)
		}
	}
	b, err := json.Marshal(off.Issues[0])
	if err != nil {
		t.Fatalf("marshal flag-off row: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("decode flag-off row: %v", err)
	}
	if _, ok := raw["subtasks"]; ok {
		t.Fatalf("flag-off row JSON carries subtasks: %s", b)
	}
	// Flag on: childless row marshals as [] (not null, not omitted).
	b, err = json.Marshal(r.Issues[0])
	if err != nil {
		t.Fatalf("marshal flag-on row: %v", err)
	}
	_ = b
	lonerRow := func() IssueListItem {
		for _, it := range r.Issues {
			if it.ID == loner.ID {
				return it
			}
		}
		t.Fatal("loner missing from flag-on page")
		return IssueListItem{}
	}()
	lb, err := json.Marshal(lonerRow)
	if err != nil {
		t.Fatalf("marshal loner row: %v", err)
	}
	var lraw map[string]json.RawMessage
	if err := json.Unmarshal(lb, &lraw); err != nil {
		t.Fatalf("decode loner row: %v", err)
	}
	if string(lraw["subtasks"]) != "[]" {
		t.Fatalf("loner subtasks JSON = %s, want []", lraw["subtasks"])
	}
}

// TestListIncludeSubtasksPQL: the flag is a list-mode flag, NOT a
// visual filter — withoutVisualFilters must leave it untouched, so
// the PQL list path (the board's second fetch path) attaches
// subtasks too. The child need not itself match the predicate:
// attachment keys on PARENTS in the page.
func TestListIncludeSubtasksPQL(t *testing.T) {
	s := setupListTest(t)
	ctx := context.Background()
	prio := 3
	parent, err := CreateIssue(ctx, s.pool, s.slug, s.ident, s.actor,
		CreateIssueInput{Name: "pql-parent", Priority: &prio})
	if err != nil {
		t.Fatalf("CreateIssue parent: %v", err)
	}
	child := createSubtaskTestIssue(t, s, "pql-child", strptr(parent.ID), 1, 1)
	r, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{PQL: `priority = High`, PerPage: 100, IncludeSubtasks: true})
	if err != nil {
		t.Fatalf("ListIssues PQL flag on: %v", err)
	}
	found := false
	for _, it := range r.Issues {
		if it.ID == parent.ID {
			found = true
			if it.Subtasks == nil || len(*it.Subtasks) != 1 || (*it.Subtasks)[0].ID != child.ID {
				t.Fatalf("PQL parent subtasks = %+v, want [pql-child]", it.Subtasks)
			}
		}
	}
	if !found {
		t.Fatal("PQL parent missing from PQL page")
	}
}

// TestListIncludeSubtasksNoNPlusOne: the flag adds exactly two
// batched queries (children + their assignees) — the count stays
// constant as parent/child volume grows. Cap math: the flag-off
// budget is 8 (TestListNoNPlusOne: membership + project + single
// list query + 3 batched count queries); flag-on adds exactly the
// 2 subtask queries, so the cap is 8 + 2 = 10.
func TestListIncludeSubtasksNoNPlusOne(t *testing.T) {
	s := setupListTest(t)
	ctx := context.Background()
	traced, counter := tracedTestPool(t)

	run := func() int64 {
		counter.n.Store(0)
		r, err := ListIssues(ctx, traced, s.slug, s.ident, s.actor,
			ListIssuesInput{PerPage: 100, IncludeSubtasks: true})
		if err != nil {
			t.Fatalf("ListIssues flag on: %v", err)
		}
		if len(r.Issues) == 0 {
			t.Fatal("flag-on page empty")
		}
		return counter.n.Load()
	}

	for i := 0; i < 20; i++ {
		p := createTestIssue(t, s.pool, s.slug, s.ident, s.actor, fmt.Sprintf("p-%d", i))
		createSubtaskTestIssue(t, s, fmt.Sprintf("c-%d", i), strptr(p.ID), 2, float64(i))
	}
	n1 := run()
	for i := 0; i < 20; i++ {
		p := createTestIssue(t, s.pool, s.slug, s.ident, s.actor, fmt.Sprintf("p2-%d", i))
		createSubtaskTestIssue(t, s, fmt.Sprintf("c2-%d", i), strptr(p.ID), 2, float64(i))
	}
	n2 := run()
	if n1 != n2 {
		t.Fatalf("flag-on query count grew with issue count: %d → %d", n1, n2)
	}
	if n1 > 10 {
		t.Fatalf("flag-on list took %d queries, want ≤ 10 (flag-off 8 + exactly 2 batched subtask queries)", n1)
	}
	t.Logf("flag-on list query count constant at %d queries", n1)
}

// TestListIncludeSubtasksFlagOffNoExtraQueries: flag off on the
// traced pool costs exactly the flag-off budget — the subtask
// queries never run without the flag.
func TestListIncludeSubtasksFlagOffNoExtraQueries(t *testing.T) {
	s := setupListTest(t)
	ctx := context.Background()
	traced, counter := tracedTestPool(t)
	p := createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "p")
	createSubtaskTestIssue(t, s, "c", strptr(p.ID), 2, 1)

	counter.n.Store(0)
	r, err := ListIssues(ctx, traced, s.slug, s.ident, s.actor, ListIssuesInput{PerPage: 100})
	if err != nil {
		t.Fatalf("ListIssues flag off: %v", err)
	}
	if n := counter.n.Load(); n > 8 {
		t.Fatalf("flag-off list took %d queries, want ≤ 8 (no subtask queries)", n)
	}
	for _, it := range r.Issues {
		if it.Subtasks != nil {
			t.Fatalf("row %s carries subtasks without the flag", it.ID)
		}
	}
}
