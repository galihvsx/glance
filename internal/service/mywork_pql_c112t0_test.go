package service

// Workspace-scoped PQL on MyWork (C112T0): the translated predicate
// ANDs with each filter, multi-match set semantics match rows in both
// accessible projects, unknown names 400 with position, and names /
// keys existing ONLY in an inaccessible private project 400 unknown
// (taxonomy-leak rule) while the unfiltered list yields no rows from
// that project. Real test database, no skips.
//
// The querying actor is Bob, a plain workspace Member (15): a
// workspace Admin would pass the private-project gate by override
// (C92T0), so the leak tests must run as a non-admin.

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// myworkPQLFixture builds: two public projects A + B, one private
// project P, and Bob (member) as the querying actor. Alice (creator,
// workspace Admin) creates everything.
func myworkPQLFixture(t *testing.T, pool *pgxpool.Pool) (slug, identA, identB, identP, alice, bob string) {
	t.Helper()
	ctx := context.Background()
	alice = createTestUser(t, pool, uniqueTestEmail("mw-pql-alice"))
	bob = createTestUser(t, pool, uniqueTestEmail("mw-pql-bob"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-mw-pql"), alice)
	addTestMember(t, pool, ws.Slug, alice, bob, RoleMember)
	slug = ws.Slug

	pA := createTestProject(t, pool, slug, alice, "Alpha", uniqueTestIdentifier())
	pB := createTestProject(t, pool, slug, alice, "Beta", uniqueTestIdentifier())
	pP := createTestProject(t, pool, slug, alice, "Private", uniqueTestIdentifier())
	priv := VisibilityPrivate
	if _, err := UpdateProject(ctx, pool, slug, pP.Identifier, alice, ProjectPatch{Visibility: &priv}); err != nil {
		t.Fatalf("make private: %v", err)
	}
	return slug, pA.Identifier, pB.Identifier, pP.Identifier, alice, bob
}

func myworkPQLNames(t *testing.T, pool *pgxpool.Pool, slug, actor string, filter MyWorkFilter, q string) []string {
	t.Helper()
	items, err := ListMyWorkPQL(context.Background(), pool, slug, actor, filter, 100, q)
	if err != nil {
		t.Fatalf("ListMyWorkPQL %q: %v", q, err)
	}
	names := make([]string, 0, len(items))
	for _, it := range items {
		names = append(names, it.Name)
	}
	sort.Strings(names)
	return names
}

func myworkPQLWant(t *testing.T, got []string, want ...string) {
	t.Helper()
	sort.Strings(want)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func myworkPQLWantErr(t *testing.T, pool *pgxpool.Pool, slug, actor string, q, substr string) {
	t.Helper()
	_, err := ListMyWorkPQL(context.Background(), pool, slug, actor, MyWorkAssigned, 100, q)
	var pqlErr *PQLQueryError
	if !errors.As(err, &pqlErr) {
		t.Fatalf("query %q: err = %v, want *PQLQueryError", q, err)
	}
	if pqlErr.Pos <= 0 {
		t.Fatalf("query %q: position = %d, want > 0", q, pqlErr.Pos)
	}
	if !strings.Contains(pqlErr.Msg, substr) {
		t.Fatalf("query %q: message = %q, want it to contain %q", q, pqlErr.Msg, substr)
	}
}

func TestMyWorkPQLAndsWithEachFilter(t *testing.T) {
	pool := newTestPool(t)
	slug, identA, identB, _, alice, bob := myworkPQLFixture(t, pool)

	todoA := pqlStateID(t, pool, slug, identA, alice, "Todo")
	doneA := pqlStateID(t, pool, slug, identA, alice, "Done")
	todoB := pqlStateID(t, pool, slug, identB, alice, "Todo")
	doneB := pqlStateID(t, pool, slug, identB, alice, "Done")

	// Assigned set (Bob): A1 Todo/A, A2 Done/A, B1 Todo/B, B2 Done/B.
	a1 := pqlCreateIssue(t, pool, slug, identA, alice, "A1", CreateIssueInput{StateID: &todoA})
	a2 := pqlCreateIssue(t, pool, slug, identA, alice, "A2", CreateIssueInput{StateID: &doneA})
	b1 := pqlCreateIssue(t, pool, slug, identB, alice, "B1", CreateIssueInput{StateID: &todoB})
	b2 := pqlCreateIssue(t, pool, slug, identB, alice, "B2", CreateIssueInput{StateID: &doneB})
	for _, iss := range []*Issue{a1, a2, b1, b2} {
		assignIssueTo(t, pool, iss.ID, bob)
	}

	// Created set (Bob): C1 Todo/A, created by Bob.
	pqlCreateIssue(t, pool, slug, identA, bob, "C1", CreateIssueInput{StateID: &todoA})

	// Watched set (Bob): B2 Done/B and W1 Done/B (neither assigned
	// to Bob except B2, neither created by him).
	w1 := pqlCreateIssue(t, pool, slug, identB, alice, "W1", CreateIssueInput{StateID: &doneB})
	subscribeUser(t, pool, b2.ID, bob)
	subscribeUser(t, pool, w1.ID, bob)

	// state = Todo is a MULTI-match set (Todo in A and in B): the
	// assigned filter keeps only Bob's assigned rows, but from BOTH
	// projects — not one, and not an ambiguity error.
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `state = Todo`), "A1", "B1")
	// Same predicate under created: only Bob's own Todo issue.
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkCreated, `state = Todo`), "C1")
	// Same predicate under watched: no watched Todo issue.
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkWatched, `state = Todo`))
	// Done under watched: both watched Done issues, and only those
	// (A2 is Done but not watched; B1 is watched? no — B1 is only
	// assigned, not subscribed).
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkWatched, `state = Done`), "B2", "W1")
	// != over the set: assigned issues NOT in a Todo-like state.
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `state != Todo`), "A2", "B2")
	// in unions the sets.
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `state in (Todo, Done)`), "A1", "A2", "B1", "B2")

	// Blank pql degrades to the unfiltered filter list.
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `   `), "A1", "A2", "B1", "B2")
	// Carried-over fields still work: priority + has*() unchanged.
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `hasAssignee()`), "A1", "A2", "B1", "B2")
}

func TestMyWorkPQLUnknownNames(t *testing.T) {
	pool := newTestPool(t)
	slug, _, _, _, _, bob := myworkPQLFixture(t, pool)

	myworkPQLWantErr(t, pool, slug, bob, `state = NoSuchState`, `unknown state "NoSuchState"`)
	myworkPQLWantErr(t, pool, slug, bob, `epic = NoSuchEpic`, `unknown epic "NoSuchEpic"`)
	myworkPQLWantErr(t, pool, slug, bob, `estimate = XXL`, `unknown estimate point "XXL"`)
	// Probe failure: a syntax error 400s with message + position.
	myworkPQLWantErr(t, pool, slug, bob, `state == Todo`, `expected a value`)
}

func TestMyWorkPQLEpicEstimateMultiMatch(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	slug, identA, identB, _, alice, bob := myworkPQLFixture(t, pool)

	// Same epic name in both projects; same estimate key in both.
	epA, err := CreateEpic(ctx, pool, slug, identA, alice, EpicInput{Name: "Shared"})
	if err != nil {
		t.Fatalf("CreateEpic A: %v", err)
	}
	epB, err := CreateEpic(ctx, pool, slug, identB, alice, EpicInput{Name: "Shared"})
	if err != nil {
		t.Fatalf("CreateEpic B: %v", err)
	}
	estA, err := CreateEstimate(ctx, pool, slug, identA, alice, EstimateInput{Name: "Fib", Points: []EstimatePointInput{{Key: "M", Value: 3}}})
	if err != nil {
		t.Fatalf("CreateEstimate A: %v", err)
	}
	estB, err := CreateEstimate(ctx, pool, slug, identB, alice, EstimateInput{Name: "Fib", Points: []EstimatePointInput{{Key: "M", Value: 3}}})
	if err != nil {
		t.Fatalf("CreateEstimate B: %v", err)
	}
	ptA, ptB := estA.Points[0].ID, estB.Points[0].ID

	a1 := pqlCreateIssue(t, pool, slug, identA, alice, "EA1", CreateIssueInput{EstimatePointID: &ptA})
	b1 := pqlCreateIssue(t, pool, slug, identB, alice, "EB1", CreateIssueInput{EstimatePointID: &ptB})
	ea0 := pqlCreateIssue(t, pool, slug, identA, alice, "EA0", CreateIssueInput{})
	if err := AddEpicIssues(ctx, pool, slug, identA, alice, epA.ID, []string{a1.ID}); err != nil {
		t.Fatalf("AddEpicIssues A: %v", err)
	}
	if err := AddEpicIssues(ctx, pool, slug, identB, alice, epB.ID, []string{b1.ID}); err != nil {
		t.Fatalf("AddEpicIssues B: %v", err)
	}
	assignIssueTo(t, pool, a1.ID, bob)
	assignIssueTo(t, pool, b1.ID, bob)
	assignIssueTo(t, pool, ea0.ID, bob)

	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `epic = Shared`), "EA1", "EB1")
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `epic = none`), "EA0")
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `estimate = M`), "EA1", "EB1")
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `estimate = none`), "EA0")
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `estimate != M`), "EA0")
}

func TestMyWorkPQLPrivateProjectTaxonomyLeak(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	slug, _, _, identP, alice, bob := myworkPQLFixture(t, pool)

	// Names existing ONLY in the private project: a custom state, an
	// epic, and an estimate key. Bob has no direct membership, is not
	// a workspace Admin, and holds no teamspace seat — the project is
	// inaccessible to him.
	shadow, err := CreateState(ctx, pool, slug, identP, alice, StateInput{Name: "ShadowState", Group: "unstarted"})
	if err != nil {
		t.Fatalf("CreateState private: %v", err)
	}
	shadowEpic, err := CreateEpic(ctx, pool, slug, identP, alice, EpicInput{Name: "ShadowEpic"})
	if err != nil {
		t.Fatalf("CreateEpic private: %v", err)
	}
	if _, err := CreateEstimate(ctx, pool, slug, identP, alice, EstimateInput{Name: "Fib", Points: []EstimatePointInput{{Key: "SHD", Value: 1}}}); err != nil {
		t.Fatalf("CreateEstimate private: %v", err)
	}
	privIss := pqlCreateIssue(t, pool, slug, identP, alice, "Hidden", CreateIssueInput{StateID: &shadow.ID})
	assignIssueTo(t, pool, privIss.ID, bob)
	if err := AddEpicIssues(ctx, pool, slug, identP, alice, shadowEpic.ID, []string{privIss.ID}); err != nil {
		t.Fatalf("AddEpicIssues private: %v", err)
	}

	// The unfiltered assigned list yields NO private-project rows.
	for _, it := range myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, ``) {
		if it == "Hidden" {
			t.Fatal("row leak: private-project issue visible in unfiltered list")
		}
	}
	// And the private-only names resolve to unknown — the 400-vs-match
	// difference must not leak the private project's taxonomy.
	myworkPQLWantErr(t, pool, slug, bob, `state = ShadowState`, `unknown state "ShadowState"`)
	myworkPQLWantErr(t, pool, slug, bob, `epic = ShadowEpic`, `unknown epic "ShadowEpic"`)
	myworkPQLWantErr(t, pool, slug, bob, `estimate = SHD`, `unknown estimate point "SHD"`)
	// A parent key naming the private project is unknown too.
	myworkPQLWantErr(t, pool, slug, bob, `parent = `+privIss.DisplayID, `unknown parent`)
}

func TestMyWorkPQLParentAcrossProjects(t *testing.T) {
	pool := newTestPool(t)
	slug, _, identB, _, alice, bob := myworkPQLFixture(t, pool)

	// Parent lives in project B; the workspace query resolves its key
	// (project scope would 400 a key from another project — here there
	// is no single project, and any accessible project's key resolves).
	parent := pqlCreateIssue(t, pool, slug, identB, alice, "ParentB", CreateIssueInput{})
	pid := parent.ID
	child := pqlCreateIssue(t, pool, slug, identB, alice, "ChildB", CreateIssueInput{ParentID: &pid})
	assignIssueTo(t, pool, child.ID, bob)
	// A top-level issue in B, assigned to Bob, for the none case — and
	// the parent itself assigned to Bob, so it shows under assigned.
	top := pqlCreateIssue(t, pool, slug, identB, alice, "TopB", CreateIssueInput{})
	assignIssueTo(t, pool, top.ID, bob)
	assignIssueTo(t, pool, parent.ID, bob)

	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `parent = `+parent.DisplayID), "ChildB")
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `parent = none`), "ParentB", "TopB")
	myworkPQLWant(t, myworkPQLNames(t, pool, slug, bob, MyWorkAssigned, `parent != `+parent.DisplayID), "ParentB", "TopB")
	// Malformed + deleted-parent rules carry over unchanged.
	myworkPQLWantErr(t, pool, slug, bob, `parent = NOPE`, `invalid parent key`)
}
