package service

// PQL service tests (C91T0): translated queries against the real test
// DB return exactly the right issues, including `me` resolution,
// unknown-name 400s, and the PQL-wins-over-visual-filters rule.

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func pqlStateID(t *testing.T, pool *pgxpool.Pool, slug, ident, actor, name string) string {
	t.Helper()
	states, err := ListStates(context.Background(), pool, slug, ident, actor)
	if err != nil {
		t.Fatalf("ListStates: %v", err)
	}
	for _, s := range states {
		if s.Name == name {
			return s.ID
		}
	}
	t.Fatalf("no state named %q", name)
	return ""
}

func pqlCreateIssue(t *testing.T, pool *pgxpool.Pool, slug, ident, actor, name string, in CreateIssueInput) *Issue {
	t.Helper()
	in.Name = name
	iss, err := CreateIssue(context.Background(), pool, slug, ident, actor, in)
	if err != nil {
		t.Fatalf("CreateIssue %q: %v", name, err)
	}
	return iss
}

func pqlAssign(t *testing.T, pool *pgxpool.Pool, issueID, userID string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO issue_assignees (issue_id, user_id) VALUES ($1::uuid, $2::uuid)`,
		issueID, userID); err != nil {
		t.Fatalf("assign %s to %s: %v", issueID, userID, err)
	}
}

func pqlAttachLabel(t *testing.T, pool *pgxpool.Pool, issueID, labelID string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO issue_labels (issue_id, label_id) VALUES ($1::uuid, $2::uuid)`,
		issueID, labelID); err != nil {
		t.Fatalf("attach label %s to %s: %v", labelID, issueID, err)
	}
}

// pqlListNames runs a PQL query and returns the sorted issue names.
func pqlListNames(t *testing.T, s listTestSetup, q string) []string {
	t.Helper()
	res, err := ListIssues(context.Background(), s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{PQL: q, OrderBy: "-sequence_id", PerPage: 100})
	if err != nil {
		t.Fatalf("ListIssues PQL %q: %v", q, err)
	}
	names := make([]string, 0, len(res.Issues))
	for _, it := range res.Issues {
		names = append(names, it.Name)
	}
	sort.Strings(names)
	return names
}

func pqlWantNames(t *testing.T, got []string, want ...string) {
	t.Helper()
	sort.Strings(want)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestPQLServiceStateAndPriority(t *testing.T) {
	s := setupListTest(t)
	todo := pqlStateID(t, s.pool, s.slug, s.ident, s.actor, "Todo")
	done := pqlStateID(t, s.pool, s.slug, s.ident, s.actor, "Done")
	inProg := pqlStateID(t, s.pool, s.slug, s.ident, s.actor, "In Progress")

	pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "A", CreateIssueInput{StateID: &todo, Priority: intPtr(3)})
	pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "B", CreateIssueInput{StateID: &done, Priority: intPtr(1)})
	pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "C", CreateIssueInput{StateID: &inProg, Priority: intPtr(4)})

	pqlWantNames(t, pqlListNames(t, s, `state = Todo`), "A")
	pqlWantNames(t, pqlListNames(t, s, `state = todo`), "A") // name match is case-insensitive
	pqlWantNames(t, pqlListNames(t, s, `state = "In Progress"`), "C")
	pqlWantNames(t, pqlListNames(t, s, `state in (Todo, Done)`), "A", "B")
	pqlWantNames(t, pqlListNames(t, s, `state != Todo`), "B", "C")
	pqlWantNames(t, pqlListNames(t, s, `priority = High`), "A")
	pqlWantNames(t, pqlListNames(t, s, `priority = 3`), "A")
	pqlWantNames(t, pqlListNames(t, s, `priority >= 3`), "A", "C")
	pqlWantNames(t, pqlListNames(t, s, `priority < 2`), "B")
	pqlWantNames(t, pqlListNames(t, s, `priority in (low, urgent)`), "B", "C")
	pqlWantNames(t, pqlListNames(t, s, `priority != 3`), "B", "C")
	pqlWantNames(t, pqlListNames(t, s, `NOT priority = High`), "B", "C")
}

func TestPQLServiceAssignees(t *testing.T) {
	s := setupListTest(t)
	ctx := context.Background()

	aliceEmail := uniqueTestEmail("pql-alice")
	alice := createTestUser(t, s.pool, aliceEmail)
	if _, err := s.pool.Exec(ctx, `UPDATE users SET name = 'Alice Example' WHERE id = $1::uuid`, alice); err != nil {
		t.Fatalf("name Alice: %v", err)
	}
	addTestMember(t, s.pool, s.slug, s.actor, alice, RoleMember)

	x := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "X", CreateIssueInput{})
	y := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "Y", CreateIssueInput{})
	pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "Z", CreateIssueInput{})
	pqlAssign(t, s.pool, x.ID, s.actor)
	pqlAssign(t, s.pool, y.ID, alice)

	pqlWantNames(t, pqlListNames(t, s, `assignee = me`), "X")
	pqlWantNames(t, pqlListNames(t, s, `assignee = "Alice Example"`), "Y")
	pqlWantNames(t, pqlListNames(t, s, `assignee = "alice example"`), "Y") // case-insensitive name match
	pqlWantNames(t, pqlListNames(t, s, `assignee = `+aliceEmail), "Y")     // email also resolves
	pqlWantNames(t, pqlListNames(t, s, `assignee = none`), "Z")
	pqlWantNames(t, pqlListNames(t, s, `assignee != none`), "X", "Y")
	pqlWantNames(t, pqlListNames(t, s, `assignee != me`), "Y", "Z")
	pqlWantNames(t, pqlListNames(t, s, `hasNoAssignee()`), "Z")
	pqlWantNames(t, pqlListNames(t, s, `hasAssignee()`), "X", "Y")
	pqlWantNames(t, pqlListNames(t, s, `assignee in (me, none)`), "X", "Z")
	pqlWantNames(t, pqlListNames(t, s, `assignee in (me, "Alice Example")`), "X", "Y")
}

func TestPQLServiceLabelsDatesNested(t *testing.T) {
	s := setupListTest(t)
	todo := pqlStateID(t, s.pool, s.slug, s.ident, s.actor, "Todo")
	done := pqlStateID(t, s.pool, s.slug, s.ident, s.actor, "Done")

	fe := createTestLabel(t, s.pool, s.slug, s.ident, s.actor, "frontend")
	be := createTestLabel(t, s.pool, s.slug, s.ident, s.actor, "backend")

	early := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	late := time.Date(2026, 12, 15, 0, 0, 0, 0, time.UTC)
	a := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "A", CreateIssueInput{StateID: &todo, Priority: intPtr(3), TargetDate: &early})
	b := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "B", CreateIssueInput{StateID: &done, Priority: intPtr(1), TargetDate: &late})
	pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "C", CreateIssueInput{StateID: &todo, Priority: intPtr(2)})
	pqlAttachLabel(t, s.pool, a.ID, fe.ID)
	pqlAttachLabel(t, s.pool, b.ID, be.ID)

	pqlWantNames(t, pqlListNames(t, s, `label = frontend`), "A")
	pqlWantNames(t, pqlListNames(t, s, `label in (frontend, backend)`), "A", "B")
	pqlWantNames(t, pqlListNames(t, s, `label != frontend`), "B", "C")
	pqlWantNames(t, pqlListNames(t, s, `NOT label = frontend`), "B", "C")
	pqlWantNames(t, pqlListNames(t, s, `due < 2026-12-01`), "A")
	pqlWantNames(t, pqlListNames(t, s, `due = 2026-11-15`), "A")
	pqlWantNames(t, pqlListNames(t, s, `due >= 2026-12-01`), "B")
	pqlWantNames(t, pqlListNames(t, s, `due in (2026-11-15, 2026-12-15)`), "A", "B")
	pqlWantNames(t, pqlListNames(t, s, `(state = Todo OR state = Done) AND priority >= 2`), "A", "C")
	pqlWantNames(t, pqlListNames(t, s, `state = Todo AND NOT label = frontend`), "C")
	// Created today (whole-day semantics for a bare date on timestamptz).
	today := time.Now().UTC().Format("2006-01-02")
	pqlWantNames(t, pqlListNames(t, s, `created = `+today), "A", "B", "C")
	pqlWantNames(t, pqlListNames(t, s, `created != `+today))
}

func TestPQLServiceUnknownNamesAndParseErrors(t *testing.T) {
	s := setupListTest(t)
	pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "A", CreateIssueInput{})

	for _, tc := range []struct {
		query string
		want  string
	}{
		{`state = NoSuchState`, `unknown state "NoSuchState"`},
		{`label = NoSuchLabel`, `unknown label "NoSuchLabel"`},
		{`assignee = NoSuchPerson`, `unknown assignee "NoSuchPerson"`},
		{`state =`, "expected a value"},
		{`priority = banana`, "unknown priority"},
		{`foo = Bar`, "unknown field"},
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
		if !strings.Contains(pqlErr.Error(), tc.want) {
			t.Errorf("PQL %q: error = %v, want substring %q", tc.query, pqlErr, tc.want)
		}
	}
}

func TestPQLServiceIgnoresVisualFilters(t *testing.T) {
	s := setupListTest(t)
	pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "HighOne", CreateIssueInput{Priority: intPtr(3)})
	pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "LowOne", CreateIssueInput{Priority: intPtr(1)})

	// Stale/invalid visual filters must not 400 a PQL request and must
	// not narrow it: only the PQL predicate applies.
	res, err := ListIssues(context.Background(), s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{
			PQL:        `priority = High`,
			Priorities: []int{99},    // invalid on its own
			State:      "not-a-uuid", // invalid on its own
			Q:          "LowOne",     // would match the other issue
			PerPage:    100,
		})
	if err != nil {
		t.Fatalf("PQL + invalid visual filters: %v", err)
	}
	pqlWantNames(t, func() []string {
		names := make([]string, 0, len(res.Issues))
		for _, it := range res.Issues {
			names = append(names, it.Name)
		}
		sort.Strings(names)
		return names
	}(), "HighOne")

	// A blank PQL is "no PQL": visual filters apply as usual.
	res, err = ListIssues(context.Background(), s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{PQL: "   ", Priorities: []int{1}, PerPage: 100})
	if err != nil {
		t.Fatalf("blank PQL: %v", err)
	}
	if len(res.Issues) != 1 || res.Issues[0].Name != "LowOne" {
		t.Fatalf("blank PQL + priority=1: got %v, want [LowOne]", res.Issues)
	}
}
