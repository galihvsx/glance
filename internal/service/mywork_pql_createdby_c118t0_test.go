package service

// Workspace-scoped `created_by` PQL field on MyWork (C118T0):
// the SAME workspace-member translator as project scope (no
// separate workspace variant — see translatePQLCreatedBy for
// the verified reasoning against projectAccessibleSQL):
// name/email/`me` resolution across projects, unknown and
// ambiguous names 400 with position, `none` 400, and the
// private-project contrast with C117's release rule: a
// private-only author's NAME still resolves (membership is
// not project taxonomy), but the predicate ANDs with
// projectAccessibleSQL, so it yields NO private rows for an
// actor who cannot reach that project. Follows the
// mywork_pql_release_c117t0_test.go idioms; the querying
// actor is Bob, a plain workspace Member, so the
// private-project gate applies (a workspace Admin would pass
// it by override).

import (
	"context"
	"testing"
)

func TestMyWorkPQLCreatedBy(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	slug, identA, identB, _, alice, bob := myworkPQLFixture(t, pool)

	// Alice ("Alice Example" is not set by the fixture, so
	// name her here) authors in both projects; Bob authors
	// in project A only.
	if _, err := pool.Exec(ctx, `UPDATE users SET name = 'Alice Example' WHERE id = $1::uuid`, alice); err != nil {
		t.Fatalf("name Alice: %v", err)
	}
	bobEmail := ""
	if err := pool.QueryRow(ctx, `SELECT email FROM users WHERE id = $1::uuid`, bob).Scan(&bobEmail); err != nil {
		t.Fatalf("bob email: %v", err)
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

	// One workspace member, two projects: the name matches
	// rows in BOTH.
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `created_by = "Alice Example"`), "A1", "B1")
	// Case-insensitive name match in workspace scope too.
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `created_by = "alice example"`), "A1", "B1")
	// Email resolves too.
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `created_by = `+bobEmail), "C1")
	// `me` resolves to the caller (Bob).
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `created_by = me`), "C1")
	// != and in.
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `created_by != me`), "A1", "B1")
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `created_by in (me, "Alice Example")`), "A1", "B1", "C1")
	// Composition with state + assignee = me.
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `created_by = "Alice Example" AND state = Todo`), "A1", "B1")
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `created_by = "Alice Example" AND assignee = me`), "A1", "B1")
	// Unknown names, ambiguous names, and `none`: 400 with
	// position, never a silent empty list.
	myworkPQLWantErr(t, pool, slug, bob, `created_by = NoSuchAuthor`, `unknown created_by "NoSuchAuthor"`)
	myworkPQLWantErr(t, pool, slug, bob, `created_by = none`, `every issue has an author`)
}

func TestMyWorkPQLCreatedByPrivateProject(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	slug, _, _, identP, alice, bob := myworkPQLFixture(t, pool)

	// Priv (member, "Priv Author") authors ONLY in the private
	// project; the issue is assigned to Bob, who cannot reach
	// that project (no direct membership, not a workspace
	// Admin, no teamspace seat).
	priv := createTestUser(t, pool, uniqueTestEmail("mw-cb-priv"))
	if _, err := pool.Exec(ctx, `UPDATE users SET name = 'Priv Author' WHERE id = $1::uuid`, priv); err != nil {
		t.Fatalf("name Priv Author: %v", err)
	}
	addTestMember(t, pool, slug, alice, priv, RoleMember)
	// Priv needs direct project membership to author in the
	// private project (a plain workspace member has no access
	// to it); Bob still cannot reach P — he has no seat there.
	if _, err := AddProjectMember(ctx, pool, slug, identP, alice, priv, RoleMember); err != nil {
		t.Fatalf("AddProjectMember priv: %v", err)
	}
	hidden := pqlCreateIssue(t, pool, slug, identP, priv, "Hidden", CreateIssueInput{})
	// Assigned to BOTH actors: Bob's view is still confined
	// by projectAccessibleSQL, while Alice (who can reach the
	// private project) sees it under her own Assigned filter.
	assignIssueTo(t, pool, hidden.ID, bob)
	assignIssueTo(t, pool, hidden.ID, alice)

	// The unfiltered assigned list yields NO private-project rows.
	for _, it := range myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, ``) {
		if it == "Hidden" {
			t.Fatal("row leak: private-project issue visible in unfiltered list")
		}
	}
	// Contrast with C117's release rule: the private-only
	// author's name STILL RESOLVES (workspace membership is
	// not project taxonomy), so the query succeeds — but it
	// returns no rows, because the predicate ANDs with
	// projectAccessibleSQL. Resolution without row leakage.
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `created_by = "Priv Author"`))
	// And the same author, queried by Alice (who CAN reach the
	// private project), does match the row.
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, alice, MyWorkAssigned, `created_by = "Priv Author"`), "Hidden")
}
