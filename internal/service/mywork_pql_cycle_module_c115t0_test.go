package service

// Workspace-scoped `cycle` / `module` PQL fields on MyWork
// (C115T0): multi-match set semantics across accessible projects,
// the `none` sentinel, `in` unions incl. mixed named + none,
// unknown names (incl. names existing ONLY in an inaccessible
// private project) 400 with position, unknown values inside an
// `in` list 400, and composition with state + assignee = me.
// Follows the mywork_pql_c112t0_test.go idioms; the querying actor
// is Bob, a plain workspace Member, so the private-project gate
// applies (a workspace Admin would pass it by override).

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func myworkPQLCycleModuleFixture(t *testing.T, pool *pgxpool.Pool, slug, identA, identB, alice string) (cycleA, cycleB, moduleA, moduleB string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now()
	cA, err := CreateCycle(ctx, pool, slug, identA, alice, CycleInput{
		Name: "Shared Sprint", StartDate: now.AddDate(0, 0, -30), EndDate: now.AddDate(0, 0, -16),
	})
	if err != nil {
		t.Fatalf("CreateCycle A: %v", err)
	}
	cB, err := CreateCycle(ctx, pool, slug, identB, alice, CycleInput{
		Name: "Shared Sprint", StartDate: now.AddDate(0, 0, -30), EndDate: now.AddDate(0, 0, -16),
	})
	if err != nil {
		t.Fatalf("CreateCycle B: %v", err)
	}
	mA, err := CreateModule(ctx, pool, slug, identA, alice, ModuleInput{Name: "Shared Mod"})
	if err != nil {
		t.Fatalf("CreateModule A: %v", err)
	}
	mB, err := CreateModule(ctx, pool, slug, identB, alice, ModuleInput{Name: "Shared Mod"})
	if err != nil {
		t.Fatalf("CreateModule B: %v", err)
	}
	return cA.ID, cB.ID, mA.ID, mB.ID
}

func TestMyWorkPQLCycleModuleMultiMatch(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	slug, identA, identB, _, alice, bob := myworkPQLFixture(t, pool)
	cA, cB, mA, mB := myworkPQLCycleModuleFixture(t, pool, slug, identA, identB, alice)

	todoA := pqlStateID(t, pool, slug, identA, alice, "Todo")
	todoB := pqlStateID(t, pool, slug, identB, alice, "Todo")

	// Assigned set (Bob): A1 in cycle A + module A, B1 in cycle B
	// + module B, N0 in neither cycle nor module.
	a1 := pqlCreateIssue(t, pool, slug, identA, alice, "A1", CreateIssueInput{StateID: &todoA})
	b1 := pqlCreateIssue(t, pool, slug, identB, alice, "B1", CreateIssueInput{StateID: &todoB})
	n0 := pqlCreateIssue(t, pool, slug, identA, alice, "N0", CreateIssueInput{StateID: &todoA})
	if err := AddCycleIssues(ctx, pool, slug, identA, alice, cA, []string{a1.ID}); err != nil {
		t.Fatalf("AddCycleIssues A: %v", err)
	}
	if err := AddCycleIssues(ctx, pool, slug, identB, alice, cB, []string{b1.ID}); err != nil {
		t.Fatalf("AddCycleIssues B: %v", err)
	}
	if err := AddModuleIssues(ctx, pool, slug, identA, alice, mA, []string{a1.ID}); err != nil {
		t.Fatalf("AddModuleIssues A: %v", err)
	}
	if err := AddModuleIssues(ctx, pool, slug, identB, alice, mB, []string{b1.ID}); err != nil {
		t.Fatalf("AddModuleIssues B: %v", err)
	}
	assignIssueTo(t, pool, a1.ID, bob)
	assignIssueTo(t, pool, b1.ID, bob)
	assignIssueTo(t, pool, n0.ID, bob)

	// Same name in both projects: the set matches rows in BOTH.
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `cycle = "Shared Sprint"`), "A1", "B1")
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `module = "Shared Mod"`), "A1", "B1")
	// Case-insensitive name match in workspace scope too.
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `cycle = "shared sprint"`), "A1", "B1")
	// `none` and inclusive !=.
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `cycle = none`), "N0")
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `module = none`), "N0")
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `cycle != "Shared Sprint"`), "N0")
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `module != "Shared Mod"`), "N0")
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `cycle != none`), "A1", "B1")
	// `in` unions the sets, incl. mixed named + none.
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `cycle in ("Shared Sprint", none)`), "A1", "B1", "N0")
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `module in ("Shared Mod", none)`), "A1", "B1", "N0")
	// Composition with state + assignee = me.
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `cycle = "Shared Sprint" AND state = Todo`), "A1", "B1")
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `module = "Shared Mod" AND assignee = me`), "A1", "B1")
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `cycle = "Shared Sprint" AND assignee = me`), "A1", "B1")
	// Unknown names and unknown values inside an `in` list: 400
	// with position, never a silent partial match.
	myworkPQLWantErr(t, pool, slug, bob, `cycle = NoSuchCycle`, `unknown cycle "NoSuchCycle"`)
	myworkPQLWantErr(t, pool, slug, bob, `module = NoSuchModule`, `unknown module "NoSuchModule"`)
	myworkPQLWantErr(t, pool, slug, bob, `cycle in ("Shared Sprint", NoSuchCycle)`, `unknown cycle "NoSuchCycle"`)
	myworkPQLWantErr(t, pool, slug, bob, `module in ("Shared Mod", NoSuchModule)`, `unknown module "NoSuchModule"`)
}

func TestMyWorkPQLCycleModulePrivateProjectTaxonomyLeak(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	slug, _, _, identP, alice, bob := myworkPQLFixture(t, pool)
	now := time.Now()

	// Names existing ONLY in the private project: Bob has no direct
	// membership, is not a workspace Admin, and holds no teamspace
	// seat — the project is inaccessible to him.
	shadowCycle, err := CreateCycle(ctx, pool, slug, identP, alice, CycleInput{
		Name: "ShadowCycle", StartDate: now.AddDate(0, 0, -30), EndDate: now.AddDate(0, 0, -16),
	})
	if err != nil {
		t.Fatalf("CreateCycle private: %v", err)
	}
	shadowModule, err := CreateModule(ctx, pool, slug, identP, alice, ModuleInput{Name: "ShadowModule"})
	if err != nil {
		t.Fatalf("CreateModule private: %v", err)
	}
	privIss := pqlCreateIssue(t, pool, slug, identP, alice, "Hidden", CreateIssueInput{})
	assignIssueTo(t, pool, privIss.ID, bob)
	if err := AddCycleIssues(ctx, pool, slug, identP, alice, shadowCycle.ID, []string{privIss.ID}); err != nil {
		t.Fatalf("AddCycleIssues private: %v", err)
	}
	if err := AddModuleIssues(ctx, pool, slug, identP, alice, shadowModule.ID, []string{privIss.ID}); err != nil {
		t.Fatalf("AddModuleIssues private: %v", err)
	}

	// The unfiltered assigned list yields NO private-project rows.
	for _, it := range myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, ``) {
		if it == "Hidden" {
			t.Fatal("row leak: private-project issue visible in unfiltered list")
		}
	}
	// And the private-only names resolve to unknown — the 400-vs-
	// match difference must not leak the private project's taxonomy.
	myworkPQLWantErr(t, pool, slug, bob, `cycle = ShadowCycle`, `unknown cycle "ShadowCycle"`)
	myworkPQLWantErr(t, pool, slug, bob, `module = ShadowModule`, `unknown module "ShadowModule"`)
}
