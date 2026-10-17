package service

// Issue-KR replay in progress_history (C47T1): issue-only objectives
// get a progress-over-time series from their linked issues'
// state_id activity rows, gated by link time. Timestamps are pinned
// via direct UPDATEs (the TestListObjectivesProgressHistory
// precedent) so every point is exact. All tests run against the real
// test database — no skips.

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func assertObjectiveHistory(t *testing.T, got []ObjectiveProgressPoint, want []ObjectiveProgressPoint) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("history = %+v, want %+v", got, want)
	}
	for i := range want {
		if !got[i].T.Equal(want[i].T) || got[i].Progress != want[i].Progress {
			t.Fatalf("history[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func setKRCreatedAt(t *testing.T, pool *pgxpool.Pool, krID string, at time.Time) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE key_results SET created_at = $1 WHERE id = $2::uuid`, at, krID); err != nil {
		t.Fatalf("pin KR created_at: %v", err)
	}
}

func setIssueCreatedAt(t *testing.T, pool *pgxpool.Pool, issueID string, at time.Time) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE issues SET created_at = $1 WHERE id = $2::uuid`, at, issueID); err != nil {
		t.Fatalf("pin issue created_at: %v", err)
	}
}

func setLinkCreatedAt(t *testing.T, pool *pgxpool.Pool, krID, issueID string, at time.Time) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE key_result_issues SET created_at = $1
		 WHERE key_result_id = $2::uuid AND issue_id = $3::uuid`, at, krID, issueID); err != nil {
		t.Fatalf("pin link created_at: %v", err)
	}
}

// setStateActivityCreatedAt pins the state_id activity row of one
// issue whose new_value is the given state (each transition has a
// distinct new_value, so this addresses exactly one row).
func setStateActivityCreatedAt(t *testing.T, pool *pgxpool.Pool, issueID, newStateID string, at time.Time) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE issue_activities SET created_at = $1
		 WHERE issue_id = $2::uuid AND field = 'state_id'
		   AND new_value #>> '{}' = $3`, at, issueID, newStateID); err != nil {
		t.Fatalf("pin state activity created_at: %v", err)
	}
}

func setCheckinCreatedAt(t *testing.T, pool *pgxpool.Pool, krID string, seed bool, at time.Time) {
	t.Helper()
	nullPred := "IS NOT NULL"
	if seed {
		nullPred = "IS NULL"
	}
	if _, err := pool.Exec(context.Background(),
		`UPDATE key_result_checkins SET created_at = $1
		 WHERE key_result_id = $2::uuid AND previous_value `+nullPred, at, krID); err != nil {
		t.Fatalf("pin check-in created_at: %v", err)
	}
}

// replayHistory fetches one objective's history via both read paths
// (they share fillObjectiveProgressHistory) and asserts both.
func assertReplayHistory(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, actor, obID string, want []ObjectiveProgressPoint) {
	t.Helper()
	list, err := ListObjectives(ctx, pool, slug, actor)
	if err != nil {
		t.Fatalf("ListObjectives: %v", err)
	}
	var listed *Objective
	for _, ob := range list {
		if ob.ID == obID {
			listed = ob
		}
	}
	if listed == nil {
		t.Fatalf("objective %s missing from ListObjectives", obID)
	}
	assertObjectiveHistory(t, listed.ProgressHistory, want)
	single, err := GetObjective(ctx, pool, slug, actor, obID)
	if err != nil {
		t.Fatalf("GetObjective: %v", err)
	}
	assertObjectiveHistory(t, single.ProgressHistory, want)
}

// replayFixture is one workspace + project + objective + issue KR
// with a completed-group "Finished" state and the project's backlog
// state, for issue-replay tests.
type replayFixture struct {
	ctx       context.Context
	pool      *pgxpool.Pool
	slug      string
	actor     string
	ident     string
	doneID    string
	backlogID string
	obID      string
	krID      string
}

func setupReplayFixture(t *testing.T, title string) replayFixture {
	t.Helper()
	ctx, pool, slug, actor := setupOKRTest(t)
	ident := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Alpha", ident)
	doneState, err := CreateState(ctx, pool, slug, ident, actor, StateInput{Name: "Finished", Group: "completed", Color: "green"})
	if err != nil {
		t.Fatalf("CreateState done: %v", err)
	}
	ob, err := CreateObjective(ctx, pool, slug, actor, ObjectiveInput{Title: title})
	if err != nil {
		t.Fatalf("CreateObjective: %v", err)
	}
	kr, err := CreateKeyResult(ctx, pool, slug, actor, ob.ID, KeyResultInput{
		Title: "Ship issues", Kind: "issue", TargetValue: fptr(100),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult issue: %v", err)
	}
	return replayFixture{ctx, pool, slug, actor, ident, doneState.ID,
		backlogStateID(t, pool, slug, ident, actor), ob.ID, kr.ID}
}

func (fx replayFixture) createIssue(t *testing.T, name string, stateID string) string {
	t.Helper()
	iss, err := CreateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		CreateIssueInput{Name: name, StateID: &stateID})
	if err != nil {
		t.Fatalf("CreateIssue %s: %v", name, err)
	}
	return iss.ID
}

func (fx replayFixture) link(t *testing.T, issueID string) {
	t.Helper()
	if err := LinkIssueToKeyResult(fx.ctx, fx.pool, fx.slug, fx.actor, fx.obID, fx.krID, issueID); err != nil {
		t.Fatalf("link %s: %v", issueID, err)
	}
}

func TestIssueOnlyObjectiveProgressHistory(t *testing.T) {
	fx := setupReplayFixture(t, "Ship")
	t0 := mustTime("2026-01-01T00:00:00Z")
	t1 := mustTime("2026-01-02T00:00:00Z")
	t2 := mustTime("2026-01-03T00:00:00Z")

	openID := fx.createIssue(t, "open", fx.backlogID)
	doneID := fx.createIssue(t, "done", fx.doneID)
	setKRCreatedAt(t, fx.pool, fx.krID, t0)
	setIssueCreatedAt(t, fx.pool, openID, t0)
	setIssueCreatedAt(t, fx.pool, doneID, t0)
	fx.link(t, openID)
	fx.link(t, doneID)
	setLinkCreatedAt(t, fx.pool, fx.krID, openID, t1)
	setLinkCreatedAt(t, fx.pool, fx.krID, doneID, t1)

	// History starts at link time with 1 of 2 done.
	assertReplayHistory(t, fx.ctx, fx.pool, fx.slug, fx.actor, fx.obID,
		[]ObjectiveProgressPoint{{T: t1, Progress: 0.5}})

	// Move the open issue to done via UpdateIssue → point at 1.0.
	if _, err := UpdateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, openID, fx.actor,
		IssuePatch{StateID: &fx.doneID}); err != nil {
		t.Fatalf("UpdateIssue to done: %v", err)
	}
	setStateActivityCreatedAt(t, fx.pool, openID, fx.doneID, t2)
	assertReplayHistory(t, fx.ctx, fx.pool, fx.slug, fx.actor, fx.obID,
		[]ObjectiveProgressPoint{{T: t1, Progress: 0.5}, {T: t2, Progress: 1}})

	got, err := GetObjective(fx.ctx, fx.pool, fx.slug, fx.actor, fx.obID)
	if err != nil {
		t.Fatalf("GetObjective: %v", err)
	}
	if got.Progress != 1 {
		t.Fatalf("current progress = %v, want 1", got.Progress)
	}
}

func TestIssueHistoryLinkTimeGating(t *testing.T) {
	fx := setupReplayFixture(t, "Gated")
	t0 := mustTime("2026-01-01T00:00:00Z")
	t1 := mustTime("2026-01-02T00:00:00Z")
	t2 := mustTime("2026-01-03T00:00:00Z")

	issID := fx.createIssue(t, "early finisher", fx.backlogID)
	setIssueCreatedAt(t, fx.pool, issID, t0)
	// Moved to done BEFORE linking: the transition shapes the state
	// at link time but must emit no point of its own.
	if _, err := UpdateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, issID, fx.actor,
		IssuePatch{StateID: &fx.doneID}); err != nil {
		t.Fatalf("UpdateIssue to done: %v", err)
	}
	setStateActivityCreatedAt(t, fx.pool, issID, fx.doneID, t1)
	setKRCreatedAt(t, fx.pool, fx.krID, t0)
	fx.link(t, issID)
	setLinkCreatedAt(t, fx.pool, fx.krID, issID, t2)

	// Exactly one point, at link time, counting the issue as done.
	assertReplayHistory(t, fx.ctx, fx.pool, fx.slug, fx.actor, fx.obID,
		[]ObjectiveProgressPoint{{T: t2, Progress: 1}})
}

func TestIssueHistoryMoveTransition(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	wsSlug, srcIdent, dstIdent, owner := moveTestSetup(t, pool, "replaymove")

	// Source-only completed state: the move falls back to the
	// target project's backlog, so done-ness flips 1 → 0 and the
	// move must appear in history (C47T0 state_id row).
	shipped, err := CreateState(ctx, pool, wsSlug, srcIdent, owner,
		StateInput{Name: "Shipped", Group: "completed", Color: "green"})
	if err != nil {
		t.Fatalf("CreateState shipped: %v", err)
	}
	iss, err := CreateIssue(ctx, pool, wsSlug, srcIdent, owner,
		CreateIssueInput{Name: "Move me", StateID: &shipped.ID})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	ob, err := CreateObjective(ctx, pool, wsSlug, owner, ObjectiveInput{Title: "Move"})
	if err != nil {
		t.Fatalf("CreateObjective: %v", err)
	}
	kr, err := CreateKeyResult(ctx, pool, wsSlug, owner, ob.ID, KeyResultInput{
		Title: "Ship issues", Kind: "issue", TargetValue: fptr(100),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult issue: %v", err)
	}
	t0 := mustTime("2026-01-01T00:00:00Z")
	t1 := mustTime("2026-01-02T00:00:00Z")
	t2 := mustTime("2026-01-03T00:00:00Z")
	setKRCreatedAt(t, pool, kr.ID, t0)
	setIssueCreatedAt(t, pool, iss.ID, t0)
	if err := LinkIssueToKeyResult(ctx, pool, wsSlug, owner, ob.ID, kr.ID, iss.ID); err != nil {
		t.Fatalf("link: %v", err)
	}
	setLinkCreatedAt(t, pool, kr.ID, iss.ID, t1)

	res := mustMove(t, pool, wsSlug, srcIdent, iss.ID, owner, dstIdent)
	if !res.StateRemapped {
		t.Fatal("state_remapped = false, want true (backlog fallback)")
	}
	dstBacklog := stateIDByName(t, pool, wsSlug, dstIdent, owner, "Backlog")
	setStateActivityCreatedAt(t, pool, iss.ID, dstBacklog, t2)

	assertReplayHistory(t, ctx, pool, wsSlug, owner, ob.ID,
		[]ObjectiveProgressPoint{{T: t1, Progress: 1}, {T: t2, Progress: 0}})
}

func TestIssueHistoryDeleteStateReassign(t *testing.T) {
	fx := setupReplayFixture(t, "Reassign")
	t0 := mustTime("2026-01-01T00:00:00Z")
	t0b := mustTime("2026-01-01T12:00:00Z")
	t1 := mustTime("2026-01-02T00:00:00Z")
	t2 := mustTime("2026-01-03T00:00:00Z")

	// A not-done state that will be deleted with reassign to the
	// completed "Finished" state.
	doomed, err := CreateState(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		StateInput{Name: "Doomed", Group: "unstarted", Color: "#fff"})
	if err != nil {
		t.Fatalf("CreateState doomed: %v", err)
	}
	issID := fx.createIssue(t, "doomed issue", fx.backlogID)
	setIssueCreatedAt(t, fx.pool, issID, t0)
	if _, err := UpdateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, issID, fx.actor,
		IssuePatch{StateID: &doomed.ID}); err != nil {
		t.Fatalf("UpdateIssue to doomed: %v", err)
	}
	setStateActivityCreatedAt(t, fx.pool, issID, doomed.ID, t0b)
	setKRCreatedAt(t, fx.pool, fx.krID, t0)
	fx.link(t, issID)
	setLinkCreatedAt(t, fx.pool, fx.krID, issID, t1)

	// DeleteState with reassign writes the C47T0 state_id row
	// (doomed → Finished) for the moved live issue.
	if err := DeleteState(fx.ctx, fx.pool, fx.slug, fx.ident, doomed.ID, fx.actor, &fx.doneID); err != nil {
		t.Fatalf("DeleteState with reassign: %v", err)
	}
	setStateActivityCreatedAt(t, fx.pool, issID, fx.doneID, t2)

	assertReplayHistory(t, fx.ctx, fx.pool, fx.slug, fx.actor, fx.obID,
		[]ObjectiveProgressPoint{{T: t1, Progress: 0}, {T: t2, Progress: 1}})
}

func TestIssueHistoryAutomationSetState(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()

	// Cascade rule: any move to started finishes the issue.
	in := automationRuleInput(fx, []string{fx.started}, []AutomationAction{
		{Type: "set_state", StateID: autoStrPtr(fx.completed)},
	})
	in.Name = "finish on start"
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, in); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	ob, err := CreateObjective(ctx, fx.pool, fx.slug, fx.admin, ObjectiveInput{Title: "Auto"})
	if err != nil {
		t.Fatalf("CreateObjective: %v", err)
	}
	kr, err := CreateKeyResult(ctx, fx.pool, fx.slug, fx.admin, ob.ID, KeyResultInput{
		Title: "Ship issues", Kind: "issue", TargetValue: fptr(100),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult issue: %v", err)
	}
	t0 := mustTime("2026-01-01T00:00:00Z")
	t1 := mustTime("2026-01-02T00:00:00Z")
	t2 := mustTime("2026-01-03T00:00:00Z")
	t3 := mustTime("2026-01-04T00:00:00Z")
	setKRCreatedAt(t, fx.pool, kr.ID, t0)
	iss := createTestIssue(t, fx.pool, fx.slug, fx.ident, fx.admin, "cascade me")
	setIssueCreatedAt(t, fx.pool, iss.ID, t0)
	if err := LinkIssueToKeyResult(ctx, fx.pool, fx.slug, fx.admin, ob.ID, kr.ID, iss.ID); err != nil {
		t.Fatalf("link: %v", err)
	}
	setLinkCreatedAt(t, fx.pool, kr.ID, iss.ID, t1)

	// The UpdateIssue move to started fires the rule, which
	// cascades set_state to completed — two state_id rows.
	moveIssueToState(t, fx, iss.ID, fx.admin, fx.started)
	setStateActivityCreatedAt(t, fx.pool, iss.ID, fx.started, t2)
	setStateActivityCreatedAt(t, fx.pool, iss.ID, fx.completed, t3)

	assertReplayHistory(t, ctx, fx.pool, fx.slug, fx.admin, ob.ID,
		[]ObjectiveProgressPoint{{T: t1, Progress: 0}, {T: t2, Progress: 0}, {T: t3, Progress: 1}})
}

func TestMetricOnlyHistoryUnchangedByIssueReplay(t *testing.T) {
	// Builder-level pin: the new builder with empty issue data
	// reproduces the C43 vectors exactly (two metric KRs, and the
	// same-timestamp collapse).
	t0 := mustTime("2026-01-01T00:00:00Z")
	t1 := mustTime("2026-01-02T00:00:00Z")
	t2 := mustTime("2026-01-03T00:00:00Z")
	metric := func(id string, start, target, current float64, created time.Time) historyKR {
		return historyKR{ID: id, Kind: "metric", StartValue: start,
			TargetValue: target, CurrentValue: current, CreatedAt: created}
	}
	seed := func(kr string, v float64, at time.Time) historyCheckin {
		return historyCheckin{KeyResultID: kr, PreviousValue: nil, NewValue: v, CreatedAt: at}
	}
	move := func(kr string, prev, v float64, at time.Time) historyCheckin {
		return historyCheckin{KeyResultID: kr, PreviousValue: f64ptr(prev), NewValue: v, CreatedAt: at}
	}
	got := buildObjectiveProgressHistoryWithIssues(
		[]historyKR{
			metric("kr1", 0, 100, 100, t0),
			metric("kr2", 0, 10, 5, t0),
		},
		[]historyCheckin{
			seed("kr1", 0, t0), seed("kr2", 0, t0),
			move("kr1", 0, 50, t1), move("kr2", 0, 5, t1),
			move("kr1", 50, 100, t2),
		},
		map[string][]historyIssueLink{}, map[string]bool{})
	assertObjectiveHistory(t, got, []ObjectiveProgressPoint{
		{T: t0, Progress: 0},
		{T: t1, Progress: 0.5},
		{T: t2, Progress: 0.75},
	})
	if legacy := buildObjectiveProgressHistory(
		[]historyKR{
			metric("kr1", 0, 100, 100, t0),
			metric("kr2", 0, 10, 5, t0),
		},
		[]historyCheckin{
			seed("kr1", 0, t0), seed("kr2", 0, t0),
			move("kr1", 0, 50, t1), move("kr2", 0, 5, t1),
			move("kr1", 50, 100, t2),
		}); len(legacy) != len(got) {
		t.Fatalf("legacy builder points = %d, want %d", len(legacy), len(got))
	}

	// Service-level: a metric KR plus a zero-link issue KR replays
	// exactly like the metric-only C43 pin (the issue KR scores 0
	// at every metric event, contributing nothing).
	ctx, pool, slug, actor := setupOKRTest(t)
	ob, err := CreateObjective(ctx, pool, slug, actor, ObjectiveInput{Title: "Metric pin"})
	if err != nil {
		t.Fatalf("CreateObjective: %v", err)
	}
	mkr, err := CreateKeyResult(ctx, pool, slug, actor, ob.ID, KeyResultInput{
		Title: "Metric", Kind: "metric",
		StartValue: fptr(0), TargetValue: fptr(100), CurrentValue: fptr(25),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult metric: %v", err)
	}
	ikr, err := CreateKeyResult(ctx, pool, slug, actor, ob.ID, KeyResultInput{
		Title: "No links", Kind: "issue", TargetValue: fptr(100),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult issue: %v", err)
	}
	if _, err := UpdateKeyResult(ctx, pool, slug, actor, ob.ID, mkr.ID, KeyResultPatch{
		CurrentValue: fptr(50),
	}); err != nil {
		t.Fatalf("UpdateKeyResult: %v", err)
	}
	setKRCreatedAt(t, pool, mkr.ID, t0)
	setKRCreatedAt(t, pool, ikr.ID, t0)
	setCheckinCreatedAt(t, pool, mkr.ID, true, t0)
	setCheckinCreatedAt(t, pool, mkr.ID, false, t1)
	assertReplayHistory(t, ctx, pool, slug, actor, ob.ID,
		[]ObjectiveProgressPoint{{T: t0, Progress: 0.125}, {T: t1, Progress: 0.25}})
}

func TestMixedObjectiveHistoryMean(t *testing.T) {
	fx := setupReplayFixture(t, "Mixed")
	t0 := mustTime("2026-01-01T00:00:00Z")
	t1 := mustTime("2026-01-02T00:00:00Z")
	t2 := mustTime("2026-01-03T00:00:00Z")
	t3 := mustTime("2026-01-04T00:00:00Z")

	mkr, err := CreateKeyResult(fx.ctx, fx.pool, fx.slug, fx.actor, fx.obID, KeyResultInput{
		Title: "Metric", Kind: "metric",
		StartValue: fptr(0), TargetValue: fptr(100), CurrentValue: fptr(0),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult metric: %v", err)
	}
	issID := fx.createIssue(t, "mixed issue", fx.backlogID)
	setKRCreatedAt(t, fx.pool, fx.krID, t0)
	setKRCreatedAt(t, fx.pool, mkr.ID, t0)
	setIssueCreatedAt(t, fx.pool, issID, t0)
	fx.link(t, issID)
	setLinkCreatedAt(t, fx.pool, fx.krID, issID, t1)
	if _, err := UpdateKeyResult(fx.ctx, fx.pool, fx.slug, fx.actor, fx.obID, mkr.ID, KeyResultPatch{
		CurrentValue: fptr(50),
	}); err != nil {
		t.Fatalf("UpdateKeyResult: %v", err)
	}
	setCheckinCreatedAt(t, fx.pool, mkr.ID, true, t0)
	setCheckinCreatedAt(t, fx.pool, mkr.ID, false, t2)
	if _, err := UpdateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, issID, fx.actor,
		IssuePatch{StateID: &fx.doneID}); err != nil {
		t.Fatalf("UpdateIssue to done: %v", err)
	}
	setStateActivityCreatedAt(t, fx.pool, issID, fx.doneID, t3)

	// T0: mean(metric 0, issue 0 — not yet linked) = 0.
	// T1: mean(0, open 0) = 0. T2: mean(0.5, 0) = 0.25.
	// T3: mean(0.5, done 1) = 0.75.
	assertReplayHistory(t, fx.ctx, fx.pool, fx.slug, fx.actor, fx.obID,
		[]ObjectiveProgressPoint{
			{T: t0, Progress: 0},
			{T: t1, Progress: 0},
			{T: t2, Progress: 0.25},
			{T: t3, Progress: 0.75},
		})
}

func TestUnlinkedIssueVanishesFromHistory(t *testing.T) {
	fx := setupReplayFixture(t, "Unlink")
	t0 := mustTime("2026-01-01T00:00:00Z")
	t1 := mustTime("2026-01-02T00:00:00Z")
	t2 := mustTime("2026-01-03T00:00:00Z")

	openID := fx.createIssue(t, "open", fx.backlogID)
	doneID := fx.createIssue(t, "done", fx.doneID)
	setKRCreatedAt(t, fx.pool, fx.krID, t0)
	setIssueCreatedAt(t, fx.pool, openID, t0)
	setIssueCreatedAt(t, fx.pool, doneID, t0)
	fx.link(t, openID)
	fx.link(t, doneID)
	setLinkCreatedAt(t, fx.pool, fx.krID, openID, t1)
	setLinkCreatedAt(t, fx.pool, fx.krID, doneID, t1)
	if _, err := UpdateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, openID, fx.actor,
		IssuePatch{StateID: &fx.doneID}); err != nil {
		t.Fatalf("UpdateIssue to done: %v", err)
	}
	setStateActivityCreatedAt(t, fx.pool, openID, fx.doneID, t2)
	assertReplayHistory(t, fx.ctx, fx.pool, fx.slug, fx.actor, fx.obID,
		[]ObjectiveProgressPoint{{T: t1, Progress: 0.5}, {T: t2, Progress: 1}})

	// Unlink the issue that carried the transition: the replay over
	// surviving rows has only the always-done issue, so the T2
	// point vanishes and T1 recomputes to 1.0.
	if err := UnlinkIssueFromKeyResult(fx.ctx, fx.pool, fx.slug, fx.actor, fx.obID, fx.krID, openID); err != nil {
		t.Fatalf("UnlinkIssueFromKeyResult: %v", err)
	}
	assertReplayHistory(t, fx.ctx, fx.pool, fx.slug, fx.actor, fx.obID,
		[]ObjectiveProgressPoint{{T: t1, Progress: 1}})
}

func TestLinkIssueCreatedAtDefault(t *testing.T) {
	ctx, pool, slug, actor := setupOKRTest(t)

	// Migration 000062 applied on the freshly migrated test DB.
	var applied bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = '000062_kr_link_created_at')`,
	).Scan(&applied); err != nil {
		t.Fatalf("migration recorded check: %v", err)
	}
	if !applied {
		t.Fatal("migration 000062_kr_link_created_at not recorded in schema_migrations")
	}

	ident := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Alpha", ident)
	iss, err := CreateIssue(ctx, pool, slug, ident, actor, CreateIssueInput{Name: "linked"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	ob, err := CreateObjective(ctx, pool, slug, actor, ObjectiveInput{Title: "Link time"})
	if err != nil {
		t.Fatalf("CreateObjective: %v", err)
	}
	kr, err := CreateKeyResult(ctx, pool, slug, actor, ob.ID, KeyResultInput{
		Title: "Ship issues", Kind: "issue", TargetValue: fptr(100),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult issue: %v", err)
	}

	// A new link gets its created_at from DEFAULT now() — no SQL
	// change to LinkIssueToKeyResult was needed.
	before := time.Now()
	if err := LinkIssueToKeyResult(ctx, pool, slug, actor, ob.ID, kr.ID, iss.ID); err != nil {
		t.Fatalf("link: %v", err)
	}
	var linkAt time.Time
	if err := pool.QueryRow(ctx,
		`SELECT created_at FROM key_result_issues
		 WHERE key_result_id = $1::uuid AND issue_id = $2::uuid`, kr.ID, iss.ID,
	).Scan(&linkAt); err != nil {
		t.Fatalf("link created_at: %v", err)
	}
	if linkAt.IsZero() || linkAt.Before(before.Add(-time.Minute)) {
		t.Fatalf("link created_at = %v, want ~now (>= %v)", linkAt, before)
	}
}
