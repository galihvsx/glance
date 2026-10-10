package service

// Blocker guard on completion (C16T3): moving an issue to a completed
// state while it has open 'blocks' inbound links is rejected, unless the
// request bypasses with ignore_blockers.
//
// Fixture notes: CreateIssueLink(issueID, targetID, kind) stores
// issue_id → target_issue_id, so "B blocks A" is CreateIssueLink(B, A,
// "blocks") — an inbound edge on A.

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

type blockerGuardFixture struct {
	ctx       context.Context
	pool      *pgxpool.Pool
	slug      string
	ident     string
	actor     string
	a         *Issue // the blocked issue
	b         *Issue // the blocker
	completed string // completed-group state id
	started   string // started-group state id
}

// newBlockerGuardFixture creates two issues with B blocking A, plus the
// completed/started state ids of the seeded project states.
func newBlockerGuardFixture(t *testing.T) blockerGuardFixture {
	t.Helper()
	ctx, pool, slug, ident, actor := setupIssueLinkTest(t)
	a := createTestIssue(t, pool, slug, ident, actor, "blocked issue")
	b := createTestIssue(t, pool, slug, ident, actor, "the blocker")
	if _, err := CreateIssueLink(ctx, pool, slug, ident, b.ID, actor, a.ID, "blocks"); err != nil {
		t.Fatalf("CreateIssueLink: %v", err)
	}
	states, err := ListStates(ctx, pool, slug, ident, actor)
	if err != nil {
		t.Fatalf("ListStates: %v", err)
	}
	fx := blockerGuardFixture{ctx: ctx, pool: pool, slug: slug, ident: ident, actor: actor, a: a, b: b}
	for _, s := range states {
		switch s.Group {
		case "completed":
			fx.completed = s.ID
		case "started":
			fx.started = s.ID
		}
	}
	if fx.completed == "" || fx.started == "" {
		t.Fatal("seeded states missing completed/started groups")
	}
	return fx
}

// openBlockersOf asserts err is the guard rejection and returns the typed
// error carrying the blocker display IDs.
func openBlockersOf(t *testing.T, err error) *OpenBlockersError {
	t.Helper()
	if !errors.Is(err, ErrOpenBlockers) {
		t.Fatalf("err = %v, want ErrOpenBlockers", err)
	}
	var obe *OpenBlockersError
	if !errors.As(err, &obe) {
		t.Fatalf("err = %T, want *OpenBlockersError", err)
	}
	return obe
}

func TestBlockerGuardFiresOnCompletion(t *testing.T) {
	fx := newBlockerGuardFixture(t)

	_, err := UpdateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.a.ID, fx.actor,
		IssuePatch{StateID: &fx.completed})
	obe := openBlockersOf(t, err)
	if len(obe.Blockers) != 1 || obe.Blockers[0] != fx.b.DisplayID {
		t.Fatalf("blockers = %v, want [%s]", obe.Blockers, fx.b.DisplayID)
	}
	// The guard runs before any write: the issue keeps its old state.
	got, err := GetIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.a.ID, fx.actor)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.StateID == fx.completed {
		t.Fatal("issue moved to completed despite the guard")
	}
}

func TestBlockerGuardIgnoresCompletedAndArchivedBlockers(t *testing.T) {
	fx := newBlockerGuardFixture(t)

	// Completing the blocker clears the guard.
	if _, err := UpdateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.b.ID, fx.actor,
		IssuePatch{StateID: &fx.completed}); err != nil {
		t.Fatalf("complete blocker: %v", err)
	}
	if _, err := UpdateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.a.ID, fx.actor,
		IssuePatch{StateID: &fx.completed}); err != nil {
		t.Fatalf("complete blocked issue with completed blocker: %v", err)
	}

	// A second pair: an archived blocker is not open either.
	c := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.actor, "blocked again")
	d := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.actor, "archived blocker")
	if _, err := CreateIssueLink(fx.ctx, fx.pool, fx.slug, fx.ident, d.ID, fx.actor, c.ID, "blocks"); err != nil {
		t.Fatalf("CreateIssueLink: %v", err)
	}
	archived := true
	if _, err := UpdateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, d.ID, fx.actor,
		IssuePatch{Archived: &archived}); err != nil {
		t.Fatalf("archive blocker: %v", err)
	}
	if _, err := UpdateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, c.ID, fx.actor,
		IssuePatch{StateID: &fx.completed}); err != nil {
		t.Fatalf("complete issue with archived blocker: %v", err)
	}
}

func TestBlockerGuardIgnoreBlockersBypass(t *testing.T) {
	fx := newBlockerGuardFixture(t)

	if _, err := UpdateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.a.ID, fx.actor,
		IssuePatch{StateID: &fx.completed, IgnoreBlockers: true}); err != nil {
		t.Fatalf("ignore_blockers bypass: %v", err)
	}
	got, err := GetIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.a.ID, fx.actor)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.StateID != fx.completed {
		t.Fatalf("state = %s, want completed", got.StateID)
	}
}

func TestBlockerGuardBulkSetAborts(t *testing.T) {
	fx := newBlockerGuardFixture(t)

	// Atomic bulk-set: the guard hit aborts the whole batch.
	_, _, err := BulkSetIssues(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		[]string{fx.a.ID}, BulkIssueSet{StateID: &fx.completed})
	obe := openBlockersOf(t, err)
	if len(obe.Blockers) != 1 || obe.Blockers[0] != fx.b.DisplayID {
		t.Fatalf("blockers = %v, want [%s]", obe.Blockers, fx.b.DisplayID)
	}

	// With ignore_blockers the batch goes through.
	updated, ids, err := BulkSetIssues(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		[]string{fx.a.ID}, BulkIssueSet{StateID: &fx.completed, IgnoreBlockers: true})
	if err != nil {
		t.Fatalf("BulkSetIssues ignore_blockers: %v", err)
	}
	if updated != 1 || len(ids) != 1 || ids[0] != fx.a.ID {
		t.Fatalf("updated = %d, ids = %v", updated, ids)
	}
}

func TestBlockerGuardBulkUpdatePerItem(t *testing.T) {
	fx := newBlockerGuardFixture(t)

	// Per-item savepoint semantics: the blocked item fails while the
	// unblocked one still moves.
	results, err := BulkUpdateIssues(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		[]string{fx.a.ID, fx.b.ID}, IssuePatch{StateID: &fx.completed})
	if err != nil {
		t.Fatalf("BulkUpdateIssues: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	if results[0].OK || results[0].Error == nil {
		t.Fatalf("results[0] = %+v, want per-item failure", results[0])
	}
	if !results[1].OK {
		t.Fatalf("results[1] = %+v, want ok", results[1])
	}
}

func TestBlockerGuardNonCompletedMoveUnaffected(t *testing.T) {
	fx := newBlockerGuardFixture(t)

	// Moving between non-completed states never consults the guard.
	if _, err := UpdateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.a.ID, fx.actor,
		IssuePatch{StateID: &fx.started}); err != nil {
		t.Fatalf("move to started with open blockers: %v", err)
	}

	// Only 'blocks' edges count: a relates_to inbound edge from the same
	// issue never blocks completion.
	rel := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.actor, "relates only")
	if _, err := CreateIssueLink(fx.ctx, fx.pool, fx.slug, fx.ident, rel.ID, fx.actor, fx.a.ID, "relates_to"); err != nil {
		t.Fatalf("CreateIssueLink relates_to: %v", err)
	}
	// Unblock: complete the real blocker, then complete the issue — the
	// relates_to edge must not trip the guard.
	if _, err := UpdateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.b.ID, fx.actor,
		IssuePatch{StateID: &fx.completed}); err != nil {
		t.Fatalf("complete blocker: %v", err)
	}
	if _, err := UpdateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.a.ID, fx.actor,
		IssuePatch{StateID: &fx.completed}); err != nil {
		t.Fatalf("complete with only relates_to edge: %v", err)
	}
}

func TestAutomationSetStateBlockerGuardRecordsFailure(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	in := automationRuleInput(fx, []string{fx.started}, []AutomationAction{
		{Type: "set_state", StateID: autoStrPtr(fx.completed)},
	})
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "auto blocked")
	blocker := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "auto blocker")
	if _, err := CreateIssueLink(ctx, fx.pool, fx.slug, fx.ident, blocker.ID, fx.admin, iss.ID, "blocks"); err != nil {
		t.Fatalf("CreateIssueLink: %v", err)
	}

	// The manual move to started fires the rule; the set_state action
	// into completed must fail the guard and record it loudly on the run
	// row — the issue itself keeps its current state.
	moveIssueToState(t, fx, iss.ID, fx.admin, fx.started)

	got, err := GetIssue(ctx, fx.pool, fx.slug, fx.ident, iss.ID, fx.admin)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.StateID != fx.started {
		t.Fatalf("state = %s, want %s (failed automation must not move)", got.StateID, fx.started)
	}

	runs := automationRunRows(t, fx)
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1 (failed action still writes its run)", len(runs))
	}
	results := automationRunActionResults(t, runs[0])
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if results[0].Type != "set_state" || results[0].OK {
		t.Fatalf("result = %+v, want set_state ok:false", results[0])
	}
	if results[0].Error == nil || *results[0].Error == "" {
		t.Fatalf("result error = %v, want non-empty error text", results[0].Error)
	}
}
