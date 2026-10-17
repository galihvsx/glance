package service

// BulkSet post-cascade version snapshot (C50T0): the label/assignee
// replace fns evaluate automations AFTER updateIssueTx ran, so a
// cascade rule (e.g. labels_changed → set_state / set_priority) moves
// the row after updateIssueTx snapshotted it. BulkSetIssues must write
// one extra version carrying the post-cascade final row when — and
// only when — the final row's content differs from the row
// updateIssueTx returned. No duplicate-content version on the
// no-cascade path.

import (
	"context"
	"testing"
)

// bulkSetLabelsChangedRule installs a labels_changed rule with the given
// actions for BulkSet post-cascade tests.
func bulkSetLabelsChangedRule(t *testing.T, fx *linkedIssueFixture, name string, actions []AutomationAction) {
	t.Helper()
	if _, err := CreateAutomationRule(context.Background(), fx.pool, fx.slug, fx.ident, fx.actor,
		AutomationRuleInput{
			Name:    name,
			Trigger: AutomationTrigger{Type: "issue.labels_changed"},
			Actions: actions,
		}); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
}

func TestBulkSetLabelsCascadeVersionCarriesNewState(t *testing.T) {
	fx := setupLinkedIssue(t)
	ctx := context.Background()
	bulkSetLabelsChangedRule(t, fx, "label finisher",
		[]AutomationAction{{Type: "set_state", StateID: &fx.doneID}})
	labelID := createBulkSetLabel(t, fx, "cascade-label")

	labelIDs := []string{labelID}
	if _, _, err := BulkSetIssues(ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		[]string{fx.iss.ID}, BulkIssueSet{LabelIDs: &labelIDs}); err != nil {
		t.Fatalf("BulkSetIssues labels: %v", err)
	}

	// (a) The latest version carries the NEW StateID — the post-cascade
	// row, not the create snapshot (backlog) updateIssueTx never
	// re-snapshotted.
	snap := latestVersionSnapshot(t, fx, fx.iss.ID)
	if snap.StateID != fx.doneID {
		t.Fatalf("latest snapshot StateID = %s, want post-cascade %s", snap.StateID, fx.doneID)
	}
}

func TestBulkSetLabelsCascadeVersionCarriesNewPriority(t *testing.T) {
	fx := setupLinkedIssue(t)
	ctx := context.Background()
	bulkSetLabelsChangedRule(t, fx, "label prioritizer",
		[]AutomationAction{{Type: "set_priority", Priority: autoIntPtr(4)}})
	labelID := createBulkSetLabel(t, fx, "priority-label")

	labelIDs := []string{labelID}
	if _, _, err := BulkSetIssues(ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		[]string{fx.iss.ID}, BulkIssueSet{LabelIDs: &labelIDs}); err != nil {
		t.Fatalf("BulkSetIssues labels: %v", err)
	}

	// (b) The latest version carries the cascade-set priority.
	snap := latestVersionSnapshot(t, fx, fx.iss.ID)
	if snap.Priority != 4 {
		t.Fatalf("latest snapshot Priority = %d, want cascade-set 4", snap.Priority)
	}
	if snap.StateID != fx.backlogID {
		t.Fatalf("latest snapshot StateID = %s, want unmoved %s", snap.StateID, fx.backlogID)
	}
}

func TestBulkSetLabelsOnlyNoCascadeWritesNoVersion(t *testing.T) {
	fx := setupLinkedIssue(t)
	ctx := context.Background()
	labelID := createBulkSetLabel(t, fx, "plain-label")

	// Creation wrote exactly one version (C49T0).
	if got := versionCount(t, fx, fx.iss.ID); got != 1 {
		t.Fatalf("versions before BulkSet = %d, want 1 (create only)", got)
	}

	labelIDs := []string{labelID}
	if _, _, err := BulkSetIssues(ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		[]string{fx.iss.ID}, BulkIssueSet{LabelIDs: &labelIDs}); err != nil {
		t.Fatalf("BulkSetIssues labels: %v", err)
	}

	// (c) Labels-only, no rule / no row change → version count UNCHANGED:
	// no duplicate-content version.
	if got := versionCount(t, fx, fx.iss.ID); got != 1 {
		t.Fatalf("versions after BulkSet = %d, want 1 (unchanged)", got)
	}
}

func TestBulkSetPatchChangePlusCascadeWritesExactlyTwoVersions(t *testing.T) {
	fx := setupLinkedIssue(t)
	ctx := context.Background()
	bulkSetLabelsChangedRule(t, fx, "label finisher",
		[]AutomationAction{{Type: "set_state", StateID: &fx.doneID}})
	labelID := createBulkSetLabel(t, fx, "cascade-label")

	// (d) Patch-field change (priority) + cascade move: exactly TWO
	// versions for the action — updateIssueTx's + the post-cascade one —
	// on top of the create version (total 3), latest = post-cascade row.
	labelIDs := []string{labelID}
	if _, _, err := BulkSetIssues(ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		[]string{fx.iss.ID}, BulkIssueSet{Priority: autoIntPtr(1), LabelIDs: &labelIDs}); err != nil {
		t.Fatalf("BulkSetIssues priority+labels: %v", err)
	}

	if got := versionCount(t, fx, fx.iss.ID); got != 3 {
		t.Fatalf("versions = %d, want 3 (create + updateIssueTx + post-cascade)", got)
	}
	snap := latestVersionSnapshot(t, fx, fx.iss.ID)
	if snap.StateID != fx.doneID {
		t.Fatalf("latest snapshot StateID = %s, want post-cascade %s", snap.StateID, fx.doneID)
	}
	if snap.Priority != 1 {
		t.Fatalf("latest snapshot Priority = %d, want patch-set 1", snap.Priority)
	}
}

func TestBulkSetLabelsOnlyCascadeWritesExactlyOneVersion(t *testing.T) {
	fx := setupLinkedIssue(t)
	ctx := context.Background()
	bulkSetLabelsChangedRule(t, fx, "label finisher",
		[]AutomationAction{{Type: "set_state", StateID: &fx.doneID}})
	labelID := createBulkSetLabel(t, fx, "cascade-label")

	// (e) Labels-only + cascade move: exactly ONE version for the action
	// (the cascade one — updateIssueTx wrote none on its no-change path),
	// on top of the create version (total 2), latest = post-cascade row.
	labelIDs := []string{labelID}
	if _, _, err := BulkSetIssues(ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		[]string{fx.iss.ID}, BulkIssueSet{LabelIDs: &labelIDs}); err != nil {
		t.Fatalf("BulkSetIssues labels: %v", err)
	}

	if got := versionCount(t, fx, fx.iss.ID); got != 2 {
		t.Fatalf("versions = %d, want 2 (create + post-cascade only)", got)
	}
	snap := latestVersionSnapshot(t, fx, fx.iss.ID)
	if snap.StateID != fx.doneID {
		t.Fatalf("latest snapshot StateID = %s, want post-cascade %s", snap.StateID, fx.doneID)
	}
}
