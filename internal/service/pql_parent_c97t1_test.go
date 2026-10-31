package service

// PQL parent field + hasParent()/hasChildren() service tests (C97T1):
// translated queries against the real test DB return exactly the
// right issues, including the `none` sentinel, `in (...)` lists,
// case-insensitive key matching, unknown/malformed/cross-project
// key 400s, and the live-child rule for hasChildren() (archived,
// deleted, and draft children never count).

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// pqlParentFixture builds: parent P with live children C1, C2, an
// archived child CA, a deleted child CD, a draft child CR, and
// top-level issues T1, T2. P itself is top-level.
func pqlParentFixture(t *testing.T) (listTestSetup, *Issue, map[string]*Issue) {
	t.Helper()
	s := setupListTest(t)
	ctx := context.Background()

	p := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "P", CreateIssueInput{})
	pid := p.ID
	c1 := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "C1", CreateIssueInput{ParentID: &pid})
	c2 := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "C2", CreateIssueInput{ParentID: &pid})
	ca := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "CA", CreateIssueInput{ParentID: &pid})
	cd := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "CD", CreateIssueInput{ParentID: &pid})
	cr := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "CR", CreateIssueInput{ParentID: &pid, IsDraft: true})
	t1 := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "T1", CreateIssueInput{})
	t2 := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "T2", CreateIssueInput{})
	// Archived / deleted children ride SQL: the list endpoint is
	// what's under test, and archiving/deleting already have their
	// own service tests.
	for _, q := range []string{
		fmt.Sprintf(`UPDATE issues SET archived_at = now() WHERE id = '%s'`, ca.ID),
		fmt.Sprintf(`UPDATE issues SET deleted_at = now() WHERE id = '%s'`, cd.ID),
	} {
		if _, err := s.pool.Exec(ctx, q); err != nil {
			t.Fatalf("fixture SQL %q: %v", q, err)
		}
	}
	return s, p, map[string]*Issue{"C1": c1, "C2": c2, "CA": ca, "CD": cd, "CR": cr, "T1": t1, "T2": t2}
}

func TestPQLServiceParent(t *testing.T) {
	s, p, _ := pqlParentFixture(t)
	key := p.DisplayID // {IDENTIFIER}-{sequence_id}, e.g. P12345-1

	pqlWantNames(t, pqlListNames(t, s, `parent = `+key), "C1", "C2")
	// Case-insensitive identifier match.
	pqlWantNames(t, pqlListNames(t, s, `parent = `+strings.ToLower(key)), "C1", "C2")
	pqlWantNames(t, pqlListNames(t, s, `parent = none`), "P", "T1", "T2")
	// != includes top-level (NULL) issues — the inclusive-!=
	// convention shared with estimate !=.
	pqlWantNames(t, pqlListNames(t, s, `parent != `+key), "P", "T1", "T2")
	pqlWantNames(t, pqlListNames(t, s, `parent != none`), "C1", "C2")
	pqlWantNames(t, pqlListNames(t, s, `parent in (`+key+`, none)`), "C1", "C2", "P", "T1", "T2")
	pqlWantNames(t, pqlListNames(t, s, `parent in (none)`), "P", "T1", "T2")
	// NOT composes over != (which is NULL-safe via IS DISTINCT
	// FROM); NOT over = would drop NULL-parent rows under SQL
	// three-valued logic, the same quirk estimate = has — != is
	// the inclusive "not this parent" form.
	pqlWantNames(t, pqlListNames(t, s, `NOT parent != `+key), "C1", "C2")
	pqlWantNames(t, pqlListNames(t, s, `parent = `+key+` AND hasParent()`), "C1", "C2")
}

func TestPQLServiceHasParentHasChildren(t *testing.T) {
	s, _, _ := pqlParentFixture(t)

	pqlWantNames(t, pqlListNames(t, s, `hasParent()`), "C1", "C2")
	pqlWantNames(t, pqlListNames(t, s, `NOT hasParent()`), "P", "T1", "T2")
	// Only P has live children: CA is archived, CD deleted, CR a
	// draft — none of them count, so no other issue qualifies.
	pqlWantNames(t, pqlListNames(t, s, `hasChildren()`), "P")
	pqlWantNames(t, pqlListNames(t, s, `NOT hasChildren()`), "C1", "C2", "T1", "T2")
	pqlWantNames(t, pqlListNames(t, s, `hasParent() AND NOT hasChildren()`), "C1", "C2")
}

func TestPQLServiceParentUnknownKeys(t *testing.T) {
	s, p, _ := pqlParentFixture(t)
	ctx := context.Background()
	key := p.DisplayID
	identPart := strings.Split(key, "-")[0]

	// A second project in the same workspace, with its own issue:
	// that issue's key must NOT resolve inside the first project.
	otherIdent := uniqueTestIdentifier()
	createTestProject(t, s.pool, s.slug, s.actor, "Other", otherIdent)
	other := pqlCreateIssue(t, s.pool, s.slug, otherIdent, s.actor, "OX", CreateIssueInput{})

	for _, tc := range []struct {
		query string
		want  string
	}{
		{`parent = ` + identPart + `-99999`, `unknown parent`},
		{`parent = ` + other.DisplayID, `unknown parent`},
		{`parent = NOPE`, `invalid parent key`},
		{`parent = ` + identPart + `-abc`, `invalid parent key`},
		{`parent = ` + identPart + `-0`, `invalid parent key`},
		{`parent in (` + key + `, ` + identPart + `-99999)`, `unknown parent`},
	} {
		_, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
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

func TestPQLServiceParentScopeSurvives(t *testing.T) {
	s, p, iss := pqlParentFixture(t)
	ctx := context.Background()

	// The C95 scope-survives contract, re-checked for parent: a
	// cycle scope ANDs with the parent predicate.
	now := time.Now()
	cycle, err := CreateCycle(ctx, s.pool, s.slug, s.ident, s.actor, CycleInput{
		Name: "Sprint C97", StartDate: now.AddDate(0, 0, -1), EndDate: now.AddDate(0, 0, 13),
	})
	if err != nil {
		t.Fatalf("CreateCycle: %v", err)
	}
	// Only C1 (not C2) is in the cycle.
	if err := AddCycleIssues(ctx, s.pool, s.slug, s.ident, s.actor, cycle.ID, []string{iss["C1"].ID}); err != nil {
		t.Fatalf("AddCycleIssues: %v", err)
	}
	pqlWantNames(t, pqlScopeListNames(t, s, ListIssuesInput{PQL: `parent = ` + p.DisplayID, Cycle: cycle.ID}), "C1")
	pqlWantNames(t, pqlScopeListNames(t, s, ListIssuesInput{PQL: `hasChildren()`, Cycle: cycle.ID}))
}
