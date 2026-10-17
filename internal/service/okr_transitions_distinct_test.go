package service

// C48T0: the OKR transitions loader joins key_result_issues without
// selecting the KR, so an issue linked to N KRs used to yield each
// state_id activity row N times. The loader must return each row
// once. All tests run against the real test database — no skips.

import (
	"context"
	"testing"
)

func TestIssueTransitionsDedupedAcrossKRs(t *testing.T) {
	fx := setupReplayFixture(t, "Dedup")
	t0 := mustTime("2026-01-01T00:00:00Z")
	t1 := mustTime("2026-01-02T00:00:00Z")
	t2 := mustTime("2026-01-03T00:00:00Z")

	// A second issue KR on the same objective.
	kr2, err := CreateKeyResult(fx.ctx, fx.pool, fx.slug, fx.actor, fx.obID, KeyResultInput{
		Title: "Ship issues too", Kind: "issue", TargetValue: fptr(100),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult second: %v", err)
	}

	issID := fx.createIssue(t, "shared", fx.backlogID)
	setKRCreatedAt(t, fx.pool, fx.krID, t0)
	setKRCreatedAt(t, fx.pool, kr2.ID, t0)
	setIssueCreatedAt(t, fx.pool, issID, t0)
	fx.link(t, issID)
	if err := LinkIssueToKeyResult(fx.ctx, fx.pool, fx.slug, fx.actor, fx.obID, kr2.ID, issID); err != nil {
		t.Fatalf("link to second KR: %v", err)
	}
	setLinkCreatedAt(t, fx.pool, fx.krID, issID, t1)
	setLinkCreatedAt(t, fx.pool, kr2.ID, issID, t1)

	// Exactly one state_id transition (backlog → done).
	if _, err := UpdateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, issID, fx.actor,
		IssuePatch{StateID: &fx.doneID}); err != nil {
		t.Fatalf("UpdateIssue to done: %v", err)
	}
	setStateActivityCreatedAt(t, fx.pool, issID, fx.doneID, t2)

	got, err := loadIssueTransitions(context.Background(), fx.pool, []string{fx.obID})
	if err != nil {
		t.Fatalf("loadIssueTransitions: %v", err)
	}
	trs := got[issID]
	if len(trs) != 1 {
		t.Fatalf("transitions for issue linked to 2 KRs = %d, want exactly 1: %+v", len(trs), trs)
	}
	if trs[0].OldStateID != fx.backlogID || trs[0].NewStateID != fx.doneID {
		t.Fatalf("transition = %+v, want backlog → done", trs[0])
	}
	if !trs[0].At.Equal(t2) {
		t.Fatalf("transition At = %v, want %v", trs[0].At, t2)
	}
}

func TestMultiKRHistoryMatchesSingleLink(t *testing.T) {
	t0 := mustTime("2026-01-01T00:00:00Z")
	t1 := mustTime("2026-01-02T00:00:00Z")
	t2 := mustTime("2026-01-03T00:00:00Z")
	want := []ObjectiveProgressPoint{{T: t1, Progress: 0}, {T: t2, Progress: 1}}

	// Single-link case: one issue KR, issue linked once.
	single := setupReplayFixture(t, "Single")
	sIssID := single.createIssue(t, "single", single.backlogID)
	setKRCreatedAt(t, single.pool, single.krID, t0)
	setIssueCreatedAt(t, single.pool, sIssID, t0)
	single.link(t, sIssID)
	setLinkCreatedAt(t, single.pool, single.krID, sIssID, t1)
	if _, err := UpdateIssue(single.ctx, single.pool, single.slug, single.ident, sIssID, single.actor,
		IssuePatch{StateID: &single.doneID}); err != nil {
		t.Fatalf("UpdateIssue single to done: %v", err)
	}
	setStateActivityCreatedAt(t, single.pool, sIssID, single.doneID, t2)
	assertReplayHistory(t, single.ctx, single.pool, single.slug, single.actor, single.obID, want)

	// Multi-KR case: two issue KRs on one objective, the same
	// issue linked to both. Both KRs track the same issue in
	// lockstep, so the objective history must be identical to
	// the single-link case.
	multi := setupReplayFixture(t, "Multi")
	kr2, err := CreateKeyResult(multi.ctx, multi.pool, multi.slug, multi.actor, multi.obID, KeyResultInput{
		Title: "Ship issues too", Kind: "issue", TargetValue: fptr(100),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult second: %v", err)
	}
	mIssID := multi.createIssue(t, "multi", multi.backlogID)
	setKRCreatedAt(t, multi.pool, multi.krID, t0)
	setKRCreatedAt(t, multi.pool, kr2.ID, t0)
	setIssueCreatedAt(t, multi.pool, mIssID, t0)
	multi.link(t, mIssID)
	if err := LinkIssueToKeyResult(multi.ctx, multi.pool, multi.slug, multi.actor, multi.obID, kr2.ID, mIssID); err != nil {
		t.Fatalf("link to second KR: %v", err)
	}
	setLinkCreatedAt(t, multi.pool, multi.krID, mIssID, t1)
	setLinkCreatedAt(t, multi.pool, kr2.ID, mIssID, t1)
	if _, err := UpdateIssue(multi.ctx, multi.pool, multi.slug, multi.ident, mIssID, multi.actor,
		IssuePatch{StateID: &multi.doneID}); err != nil {
		t.Fatalf("UpdateIssue multi to done: %v", err)
	}
	setStateActivityCreatedAt(t, multi.pool, mIssID, multi.doneID, t2)
	assertReplayHistory(t, multi.ctx, multi.pool, multi.slug, multi.actor, multi.obID, want)
}
