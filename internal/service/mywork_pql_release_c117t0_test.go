package service

// Workspace-scoped `release` PQL field on MyWork (C117T0):
// multi-match set semantics across accessible projects, the
// `none` sentinel, `in` unions incl. mixed named + none,
// unknown names (incl. names existing ONLY in an inaccessible
// private project) 400 with position, unknown values inside an
// `in` list 400, and composition with state + assignee = me.
// Follows the mywork_pql_cycle_module_c115t0_test.go idioms;
// the querying actor is Bob, a plain workspace Member, so the
// private-project gate applies (a workspace Admin would pass
// it by override).

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func myworkPQLReleaseFixture(t *testing.T, pool *pgxpool.Pool, slug, identA, identB, alice string) (relA, relB string) {
	t.Helper()
	ctx := context.Background()
	rA, err := CreateRelease(ctx, pool, slug, identA, alice, ReleaseInput{Name: "Shared Release"})
	if err != nil {
		t.Fatalf("CreateRelease A: %v", err)
	}
	rB, err := CreateRelease(ctx, pool, slug, identB, alice, ReleaseInput{Name: "Shared Release"})
	if err != nil {
		t.Fatalf("CreateRelease B: %v", err)
	}
	return rA.ID, rB.ID
}

func TestMyWorkPQLReleaseMultiMatch(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	slug, identA, identB, _, alice, bob := myworkPQLFixture(t, pool)
	rA, rB := myworkPQLReleaseFixture(t, pool, slug, identA, identB, alice)

	todoA := pqlStateID(t, pool, slug, identA, alice, "Todo")
	todoB := pqlStateID(t, pool, slug, identB, alice, "Todo")

	// Assigned set (Bob): A1 in release A, B1 in release B,
	// N0 in neither release.
	a1 := pqlCreateIssue(t, pool, slug, identA, alice, "A1", CreateIssueInput{StateID: &todoA})
	b1 := pqlCreateIssue(t, pool, slug, identB, alice, "B1", CreateIssueInput{StateID: &todoB})
	n0 := pqlCreateIssue(t, pool, slug, identA, alice, "N0", CreateIssueInput{StateID: &todoA})
	if err := AssignReleaseIssues(ctx, pool, slug, identA, alice, rA, []string{a1.ID}); err != nil {
		t.Fatalf("AssignReleaseIssues A: %v", err)
	}
	if err := AssignReleaseIssues(ctx, pool, slug, identB, alice, rB, []string{b1.ID}); err != nil {
		t.Fatalf("AssignReleaseIssues B: %v", err)
	}
	assignIssueTo(t, pool, a1.ID, bob)
	assignIssueTo(t, pool, b1.ID, bob)
	assignIssueTo(t, pool, n0.ID, bob)

	// Same name in both projects: the set matches rows in BOTH.
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `release = "Shared Release"`), "A1", "B1")
	// Case-insensitive name match in workspace scope too.
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `release = "shared release"`), "A1", "B1")
	// `none` and inclusive !=.
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `release = none`), "N0")
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `release != "Shared Release"`), "N0")
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `release != none`), "A1", "B1")
	// `in` unions the sets, incl. mixed named + none.
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `release in ("Shared Release", none)`), "A1", "B1", "N0")
	// Composition with state + assignee = me.
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `release = "Shared Release" AND state = Todo`), "A1", "B1")
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `release = "Shared Release" AND assignee = me`), "A1", "B1")
	// Unknown names and unknown values inside an `in` list: 400
	// with position, never a silent partial match.
	myworkPQLWantErr(t, pool, slug, bob, `release = NoSuchRelease`, `unknown release "NoSuchRelease"`)
	myworkPQLWantErr(t, pool, slug, bob, `release in ("Shared Release", NoSuchRelease)`, `unknown release "NoSuchRelease"`)
}

func TestMyWorkPQLReleasePrivateProjectTaxonomyLeak(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	slug, _, _, identP, alice, bob := myworkPQLFixture(t, pool)

	// Name existing ONLY in the private project: Bob has no direct
	// membership, is not a workspace Admin, and holds no teamspace
	// seat — the project is inaccessible to him.
	shadowRelease, err := CreateRelease(ctx, pool, slug, identP, alice, ReleaseInput{Name: "ShadowRelease"})
	if err != nil {
		t.Fatalf("CreateRelease private: %v", err)
	}
	privIss := pqlCreateIssue(t, pool, slug, identP, alice, "Hidden", CreateIssueInput{})
	assignIssueTo(t, pool, privIss.ID, bob)
	if err := AssignReleaseIssues(ctx, pool, slug, identP, alice, shadowRelease.ID, []string{privIss.ID}); err != nil {
		t.Fatalf("AssignReleaseIssues private: %v", err)
	}

	// The unfiltered assigned list yields NO private-project rows.
	for _, it := range myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, ``) {
		if it == "Hidden" {
			t.Fatal("row leak: private-project issue visible in unfiltered list")
		}
	}
	// And the private-only name resolves to unknown — the 400-vs-
	// match difference must not leak the private project's taxonomy.
	myworkPQLWantErr(t, pool, slug, bob, `release = ShadowRelease`, `unknown release "ShadowRelease"`)
}
