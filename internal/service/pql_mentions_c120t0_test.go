package service

// `mentions` PQL field, project scope (C120T0): a NAME field
// in the assignee-resolver shape — member name, email, or
// `me`, plus `none` — over the persisted issue_mentions
// index (migration 000082). `=` / `!=` / `in`; unknown and
// ambiguous names 400 with position, never a silent empty
// list; `none` matches issues with no mention rows (the
// assignee none shape, NOT the created_by rejection shape).
// Workspace scope (MyWork) uses the SAME translator — no
// separate workspace variant, the C118 created_by precedent.
// Follows the pql_createdby_c118t0_test.go idioms; real
// test database, no skips.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// pqlMentionsFixture builds alice ("Alice Example", member)
// plus two members sharing one display name ("Sam Twin") for
// the ambiguity case, and names the setup actor "Andy Actor"
// so a comment by someone else can mention him (a
// self-mention is skipped by resolution, so `me` needs a
// comment authored by another member).
func pqlMentionsFixture(t *testing.T, s listTestSetup) (aliceEmail string, alice string) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, `UPDATE users SET name = 'Andy Actor' WHERE id = $1::uuid`, s.actor); err != nil {
		t.Fatalf("name actor: %v", err)
	}
	aliceEmail = uniqueTestEmail("pql-m-alice")
	alice = createTestUser(t, s.pool, aliceEmail)
	if _, err := s.pool.Exec(ctx, `UPDATE users SET name = 'Alice Example' WHERE id = $1::uuid`, alice); err != nil {
		t.Fatalf("name Alice: %v", err)
	}
	addTestMember(t, s.pool, s.slug, s.actor, alice, RoleMember)
	for _, prefix := range []string{"pql-m-twin1", "pql-m-twin2"} {
		twin := createTestUser(t, s.pool, uniqueTestEmail(prefix))
		if _, err := s.pool.Exec(ctx, `UPDATE users SET name = 'Sam Twin' WHERE id = $1::uuid`, twin); err != nil {
			t.Fatalf("name Sam Twin: %v", err)
		}
		addTestMember(t, s.pool, s.slug, s.actor, twin, RoleMember)
	}
	return aliceEmail, alice
}

func pqlMentionComment(t *testing.T, s listTestSetup, issueID, authorID, text string) {
	t.Helper()
	if _, err := CreateComment(context.Background(), s.pool, s.slug, s.ident, issueID, authorID,
		tiptapComment(text), nil); err != nil {
		t.Fatalf("CreateComment %q: %v", text, err)
	}
}

func TestPQLServiceMentionsField(t *testing.T) {
	s := setupListTest(t)
	aliceEmail, alice := pqlMentionsFixture(t, s)

	todo := pqlStateID(t, s.pool, s.slug, s.ident, s.actor, "Todo")
	a := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "A", CreateIssueInput{StateID: &todo})
	pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "B", CreateIssueInput{StateID: &todo})
	c := pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "C", CreateIssueInput{StateID: &todo})
	pqlAssign(t, s.pool, a.ID, s.actor)

	// A mentions Alice (comment by the actor); C mentions
	// the actor (comment by Alice — self-mentions never
	// resolve, so `me` needs another author); B mentions
	// nobody.
	pqlMentionComment(t, s, a.ID, s.actor, "hey @Alice Example take a look")
	pqlMentionComment(t, s, c.ID, alice, "hey @Andy Actor take a look")

	pqlWantNames(t, pqlListNames(t, s, `mentions = "Alice Example"`), "A")
	pqlWantNames(t, pqlListNames(t, s, `mentions = "alice example"`), "A") // case-insensitive
	pqlWantNames(t, pqlListNames(t, s, `mentions = `+aliceEmail), "A")     // email also resolves
	pqlWantNames(t, pqlListNames(t, s, `mentions = me`), "C")
	pqlWantNames(t, pqlListNames(t, s, `mentions = none`), "B")
	pqlWantNames(t, pqlListNames(t, s, `mentions != me`), "A", "B")
	pqlWantNames(t, pqlListNames(t, s, `mentions != "Alice Example"`), "B", "C")
	pqlWantNames(t, pqlListNames(t, s, `mentions != none`), "A", "C")
	pqlWantNames(t, pqlListNames(t, s, `mentions in (me, "Alice Example")`), "A", "C")
	pqlWantNames(t, pqlListNames(t, s, `mentions in (me, none)`), "B", "C")
	// Composition with state + assignee = me.
	pqlWantNames(t, pqlListNames(t, s, `mentions = "Alice Example" AND state = Todo`), "A")
	pqlWantNames(t, pqlListNames(t, s, `mentions = "Alice Example" AND assignee = me`), "A")
	pqlWantNames(t, pqlListNames(t, s, `mentions = "Alice Example" AND assignee = "Alice Example"`))
}

func TestPQLServiceMentionsErrors(t *testing.T) {
	s := setupListTest(t)
	_, _ = pqlMentionsFixture(t, s)
	pqlCreateIssue(t, s.pool, s.slug, s.ident, s.actor, "A", CreateIssueInput{})

	for _, tc := range []struct {
		query string
		want  string
	}{
		{`mentions = NoSuchPerson`, `unknown mentions "NoSuchPerson"`},
		{`mentions in (me, NoSuchPerson)`, `unknown mentions "NoSuchPerson"`},
		{`mentions = "Sam Twin"`, `ambiguous mentions "Sam Twin"`},
		{`mentions > me`, `operator > is not supported for field "mentions"`},
		{`foo = Bar`, "mentions"}, // the known-fields list now names the field
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
		if pqlErr.Pos <= 0 {
			t.Errorf("PQL %q: position = %d, want > 0", tc.query, pqlErr.Pos)
		}
		if !strings.Contains(pqlErr.Error(), tc.want) {
			t.Errorf("PQL %q: error = %v, want substring %q", tc.query, pqlErr, tc.want)
		}
	}

	// A workspace NON-member's name is unknown, even if the
	// user row exists: resolution is confined to workspace
	// members, exactly like assignee.
	outsiderEmail := uniqueTestEmail("pql-m-outsider")
	outsider := createTestUser(t, s.pool, outsiderEmail)
	if _, err := s.pool.Exec(context.Background(), `UPDATE users SET name = 'Out Sider' WHERE id = $1::uuid`, outsider); err != nil {
		t.Fatalf("name Out Sider: %v", err)
	}
	_, err := ListIssues(context.Background(), s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{PQL: `mentions = "Out Sider"`, PerPage: 10})
	var pqlErr *PQLQueryError
	if !errors.As(err, &pqlErr) || !strings.Contains(pqlErr.Error(), `unknown mentions "Out Sider"`) {
		t.Fatalf("PQL outsider name: err = %v, want unknown-mentions 400", err)
	}
}

func TestPQLKnownFieldsListsMentions(t *testing.T) {
	if !strings.Contains(pqlKnownFields, "mentions") {
		t.Fatalf("pqlKnownFields = %q, want it to list mentions", pqlKnownFields)
	}
}
