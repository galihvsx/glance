package service

// Workspace-scoped `mentions` PQL field on MyWork (C120T0):
// the SAME workspace-member translator as project scope (no
// separate workspace variant — see translatePQLMentions for
// the verified reasoning against projectAccessibleSQL, the
// C118 created_by precedent). PQL covers MyWork via the
// shared translator: MyWork has no visual-param path, so no
// separate MyWork-only path is built (the C119 state_group
// precedent). The querying actor is Bob, a plain workspace
// Member. Follows the mywork_pql_createdby_c118t0_test.go
// idioms.

import (
	"context"
	"testing"
)

func TestMyWorkPQLMentions(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	slug, identA, identB, _, alice, bob := myworkPQLFixture(t, pool)

	if _, err := pool.Exec(ctx, `UPDATE users SET name = 'Alice Example' WHERE id = $1::uuid`, alice); err != nil {
		t.Fatalf("name Alice: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET name = 'Bob Builder' WHERE id = $1::uuid`, bob); err != nil {
		t.Fatalf("name Bob: %v", err)
	}

	todoA := pqlStateID(t, pool, slug, identA, alice, "Todo")
	todoB := pqlStateID(t, pool, slug, identB, alice, "Todo")

	// Assigned set (Bob): A1 by alice/A, B1 by alice/B, C1 by bob/A.
	a1 := pqlCreateIssue(t, pool, slug, identA, alice, "A1", CreateIssueInput{StateID: &todoA})
	b1 := pqlCreateIssue(t, pool, slug, identB, alice, "B1", CreateIssueInput{StateID: &todoB})
	c1 := pqlCreateIssue(t, pool, slug, identA, bob, "C1", CreateIssueInput{StateID: &todoA})
	for _, iss := range []*Issue{a1, b1, c1} {
		assignIssueTo(t, pool, iss.ID, bob)
	}

	// A1 mentions Bob (comment by Alice); C1 mentions Alice
	// (comment by Bob); B1 mentions nobody.
	mention := func(ident, issueID, authorID, text string) {
		t.Helper()
		if _, err := CreateComment(ctx, pool, slug, ident, issueID, authorID, tiptapComment(text), nil); err != nil {
			t.Fatalf("CreateComment %q: %v", text, err)
		}
	}
	mention(identA, a1.ID, alice, "hey @Bob Builder take a look")
	mention(identA, c1.ID, bob, "hey @Alice Example take a look")

	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `mentions = me`), "A1")
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `mentions = "Alice Example"`), "C1")
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `mentions = none`), "B1")
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `mentions != me`), "B1", "C1")
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `mentions in (me, none)`), "A1", "B1")
	// Unknown names 400 with position, never a silent empty list.
	myworkPQLWantErr(t, pool, slug, bob, `mentions = NoSuchPerson`, `unknown mentions "NoSuchPerson"`)
}
