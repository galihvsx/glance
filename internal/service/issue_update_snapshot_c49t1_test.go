package service

// updateIssueTx post-automation snapshot + returned row (C49T1): the
// version snapshot used to be taken BEFORE the automation blocks, so a
// cascade rule (e.g. priority_changed → set_state) moved the row after
// the snapshot and after the returned row was scanned. Now updateIssueTx
// re-reads the full row in-tx AFTER all automation blocks, snapshots
// THAT row (one version per user action, not two), and returns the fresh
// row — so UpdateIssue announce + return, BulkUpdateIssues announce,
// and the RestoreIssueVersion has-fields branch all carry the
// post-automation row. Webhook changed_fields stays the user-patch
// fields; notification/Slack text stays on the user-patch state.

import (
	"context"
	"encoding/json"
	"testing"
)

// latestVersionSnapshot decodes the newest version snapshot of an issue
// into an Issue. The snapshot is the JSON marshal of the full Issue row,
// so StateID/Priority/etc. read back directly.
func latestVersionSnapshot(t *testing.T, fx *linkedIssueFixture, issueID string) Issue {
	t.Helper()
	vs, err := ListVersions(fx.ctx, fx.pool, fx.slug, fx.ident, issueID, fx.actor)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(vs) == 0 {
		t.Fatalf("versions = 0, want at least 1")
	}
	var snap Issue
	if err := json.Unmarshal(vs[len(vs)-1].Snapshot, &snap); err != nil {
		t.Fatalf("decode latest snapshot: %v", err)
	}
	return snap
}

func versionCount(t *testing.T, fx *linkedIssueFixture, issueID string) int {
	t.Helper()
	vs, err := ListVersions(fx.ctx, fx.pool, fx.slug, fx.ident, issueID, fx.actor)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	return len(vs)
}

func TestUpdateTxPriorityCascadeSnapshotCarriesNewState(t *testing.T) {
	fx := setupLinkedIssue(t)
	ctx := context.Background()
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.actor, AutomationRuleInput{
		Name:    "priority finisher",
		Trigger: AutomationTrigger{Type: "issue.priority_changed", ToPriorities: []int{3}},
		Actions: []AutomationAction{{Type: "set_state", StateID: &fx.doneID}},
	}); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	updated, err := UpdateIssue(ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor,
		IssuePatch{Priority: autoIntPtr(3)})
	if err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	if updated.StateID != fx.doneID {
		t.Fatalf("returned StateID = %s, want cascade-moved %s", updated.StateID, fx.doneID)
	}
	if updated.Priority != 3 {
		t.Fatalf("returned Priority = %d, want 3", updated.Priority)
	}

	// Exactly one new version (v1 create + v2 patch), carrying the
	// post-automation row — not the pre-cascade one.
	if got := versionCount(t, fx, fx.iss.ID); got != 2 {
		t.Fatalf("versions = %d, want 2 (one version per user action)", got)
	}
	snap := latestVersionSnapshot(t, fx, fx.iss.ID)
	if snap.StateID != fx.doneID {
		t.Fatalf("snapshot StateID = %s, want post-automation %s", snap.StateID, fx.doneID)
	}
	if snap.Priority != 3 {
		t.Fatalf("snapshot Priority = %d, want 3", snap.Priority)
	}

	// The broadcast payload carries the post-automation row too.
	assertIssueUpdatedIDs(t, fake, fx.iss.ID)
	if payload := issueUpdatedPayloads(t, fake)[fx.iss.ID]; payload.StateID != fx.doneID {
		t.Fatalf("issue.updated state_id = %s, want post-automation %s", payload.StateID, fx.doneID)
	}
	// The cascade moved a linked issue: objective fan-out fires.
	assertLinkedObjectiveEvents(t, fake, fx.obID)
}

func TestUpdateTxNoRulePatchSnapshotMatchesReturn(t *testing.T) {
	fx := setupLinkedIssue(t)
	ctx := context.Background()
	fake := installCapturingBroadcaster(t)

	updated, err := UpdateIssue(ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor,
		IssuePatch{Priority: autoIntPtr(2)})
	if err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	if updated.StateID != fx.backlogID || updated.Priority != 2 {
		t.Fatalf("returned = state %s priority %d, want state %s priority 2",
			updated.StateID, updated.Priority, fx.backlogID)
	}

	if got := versionCount(t, fx, fx.iss.ID); got != 2 {
		t.Fatalf("versions = %d, want 2", got)
	}
	snap := latestVersionSnapshot(t, fx, fx.iss.ID)
	if snap.StateID != updated.StateID || snap.Priority != updated.Priority || snap.Name != updated.Name {
		t.Fatalf("snapshot = state %s priority %d name %q, want state %s priority %d name %q",
			snap.StateID, snap.Priority, snap.Name, updated.StateID, updated.Priority, updated.Name)
	}

	assertIssueUpdatedIDs(t, fake, fx.iss.ID)
	if payload := issueUpdatedPayloads(t, fake)[fx.iss.ID]; payload.StateID != fx.backlogID {
		t.Fatalf("issue.updated state_id = %s, want %s", payload.StateID, fx.backlogID)
	}
	// Priority-only patch, no state move: no objective fan-out.
	assertLinkedObjectiveEvents(t, fake)
}

func TestUpdateTxNetZeroCascadeSnapshotKeepsPrePatchState(t *testing.T) {
	fx := setupLinkedIssue(t)
	ctx := context.Background()
	started, err := CreateState(ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		StateInput{Name: "Started", Group: "started"})
	if err != nil {
		t.Fatalf("CreateState started: %v", err)
	}
	// The rule moves the issue straight back to its pre-patch state:
	// a net-zero round trip.
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.actor, AutomationRuleInput{
		Name:    "started bounce-back",
		Trigger: AutomationTrigger{Type: "issue.state_changed", ToStates: []string{started.ID}},
		Actions: []AutomationAction{{Type: "set_state", StateID: &fx.backlogID}},
	}); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	updated, err := UpdateIssue(ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor,
		IssuePatch{StateID: &started.ID})
	if err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	if updated.StateID != fx.backlogID {
		t.Fatalf("returned StateID = %s, want net-zero pre-patch %s", updated.StateID, fx.backlogID)
	}

	if got := versionCount(t, fx, fx.iss.ID); got != 2 {
		t.Fatalf("versions = %d, want 2", got)
	}
	snap := latestVersionSnapshot(t, fx, fx.iss.ID)
	if snap.StateID != fx.backlogID {
		t.Fatalf("snapshot StateID = %s, want net-zero pre-patch %s", snap.StateID, fx.backlogID)
	}

	assertIssueUpdatedIDs(t, fake, fx.iss.ID)
	if payload := issueUpdatedPayloads(t, fake)[fx.iss.ID]; payload.StateID != fx.backlogID {
		t.Fatalf("issue.updated state_id = %s, want net-zero %s", payload.StateID, fx.backlogID)
	}
	// Net-zero: stateChanged false (C44T1 semantics kept) — no fan-out.
	assertLinkedObjectiveEvents(t, fake)
}

func TestBulkUpdateTxCascadeAnnouncesPostAutomationRow(t *testing.T) {
	fx := setupLinkedIssue(t)
	ctx := context.Background()
	second, err := CreateIssue(ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		CreateIssueInput{Name: "linked 2", StateID: &fx.backlogID})
	if err != nil {
		t.Fatalf("CreateIssue second: %v", err)
	}
	if err := LinkIssueToKeyResult(ctx, fx.pool, fx.slug, fx.actor, fx.obID, fx.krID, second.ID); err != nil {
		t.Fatalf("link second: %v", err)
	}
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.actor, AutomationRuleInput{
		Name:    "priority finisher",
		Trigger: AutomationTrigger{Type: "issue.priority_changed", ToPriorities: []int{3}},
		Actions: []AutomationAction{{Type: "set_state", StateID: &fx.doneID}},
	}); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	results, err := BulkUpdateIssues(ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		[]string{fx.iss.ID, second.ID}, IssuePatch{Priority: autoIntPtr(3)})
	if err != nil {
		t.Fatalf("BulkUpdateIssues: %v", err)
	}
	for _, r := range results {
		if !r.OK {
			t.Fatalf("bulk item %s not OK: %v", r.ID, r.Error)
		}
	}

	// Both issues committed at the cascade target, and both announced
	// payloads carry the post-automation row.
	assertIssueUpdatedIDs(t, fake, fx.iss.ID, second.ID)
	payloads := issueUpdatedPayloads(t, fake)
	for _, id := range []string{fx.iss.ID, second.ID} {
		if got := issueStateID(t, fx, id); got != fx.doneID {
			t.Fatalf("issue %s state = %s, want cascade-moved %s", id, got, fx.doneID)
		}
		if payloads[id].StateID != fx.doneID {
			t.Fatalf("issue %s announced state_id = %s, want post-automation %s",
				id, payloads[id].StateID, fx.doneID)
		}
		if snap := latestVersionSnapshot(t, fx, id); snap.StateID != fx.doneID || snap.Priority != 3 {
			t.Fatalf("issue %s snapshot = state %s priority %d, want state %s priority 3",
				id, snap.StateID, snap.Priority, fx.doneID)
		}
	}
	// Both linked issues moved: exactly one fan-out per objective.
	assertLinkedObjectiveEvents(t, fake, fx.obID)
}
