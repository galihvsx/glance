package service

// BulkSet cascade broadcast (C46T0): a labels_changed / assigned /
// unassigned automation rule firing set_state inside
// replaceIssueLabelsTx / replaceIssueAssigneesTx moves the issue AFTER
// updateIssueTx ran, so the cascade move must fan out
// objective.updated, and the announced issue.updated snapshot must
// carry the final committed state. Labels-only sets with no cascade
// rule announce issue.updated but no objective.updated. A direct
// set.StateID + labels in one BulkSet fans out exactly once per
// linked objective even when a cascade fires on top of the direct
// move (one append per item; DISTINCT in the batch helper).

import (
	"context"
	"testing"
)

// bulkSetIssueLabels returns the sorted label ids currently attached
// to an issue (committed state).
func bulkSetIssueLabels(t *testing.T, fx *linkedIssueFixture, issueID string) []string {
	t.Helper()
	rows, err := fx.pool.Query(fx.ctx,
		`SELECT label_id::text FROM issue_labels WHERE issue_id = $1::uuid ORDER BY 1`, issueID)
	if err != nil {
		t.Fatalf("issue_labels: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan label: %v", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if out == nil {
		out = []string{}
	}
	return out
}

// bulkSetIssueAssignees returns the sorted assignee ids of an issue.
func bulkSetIssueAssignees(t *testing.T, fx *linkedIssueFixture, issueID string) []string {
	t.Helper()
	rows, err := fx.pool.Query(fx.ctx,
		`SELECT user_id::text FROM issue_assignees WHERE issue_id = $1::uuid ORDER BY 1`, issueID)
	if err != nil {
		t.Fatalf("issue_assignees: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan assignee: %v", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if out == nil {
		out = []string{}
	}
	return out
}

// createBulkSetLabel creates one workspace label for BulkSet tests.
func createBulkSetLabel(t *testing.T, fx *linkedIssueFixture, name string) string {
	t.Helper()
	label, err := CreateLabel(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		LabelInput{Name: name, Color: autoStrPtr("#3b82f6")})
	if err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}
	return label.ID
}

func TestBulkSetLabelsChangedCascadeBroadcasts(t *testing.T) {
	fx := setupLinkedIssue(t)
	ctx := context.Background()
	labelID := createBulkSetLabel(t, fx, "cascade-label")
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.actor, AutomationRuleInput{
		Name:    "label finisher",
		Trigger: AutomationTrigger{Type: "issue.labels_changed"},
		Actions: []AutomationAction{{Type: "set_state", StateID: &fx.doneID}},
	}); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	labelIDs := []string{labelID}
	if _, _, err := BulkSetIssues(ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		[]string{fx.iss.ID}, BulkIssueSet{LabelIDs: &labelIDs}); err != nil {
		t.Fatalf("BulkSetIssues labels: %v", err)
	}

	// The cascade moved the issue; the announced snapshot carries the
	// final state, and the committed label set is the new one.
	if got := issueStateID(t, fx, fx.iss.ID); got != fx.doneID {
		t.Fatalf("issue state = %s, want cascade-moved to %s", got, fx.doneID)
	}
	assertIssueUpdatedIDs(t, fake, fx.iss.ID)
	for _, payload := range issueUpdatedPayloads(t, fake) {
		if payload.StateID != fx.doneID {
			t.Fatalf("issue.updated state_id = %s, want final %s", payload.StateID, fx.doneID)
		}
	}
	if got := bulkSetIssueLabels(t, fx, fx.iss.ID); len(got) != 1 || got[0] != labelID {
		t.Fatalf("committed labels = %v, want [%s]", got, labelID)
	}
	assertLinkedObjectiveEvents(t, fake, fx.obID)
}

func TestBulkSetAssignedCascadeBroadcasts(t *testing.T) {
	fx := setupLinkedIssue(t)
	ctx := context.Background()
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.actor, AutomationRuleInput{
		Name:    "assign finisher",
		Trigger: AutomationTrigger{Type: "issue.assigned"},
		Actions: []AutomationAction{{Type: "set_state", StateID: &fx.doneID}},
	}); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	uid := fx.actor
	if _, _, err := BulkSetIssues(ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		[]string{fx.iss.ID}, BulkIssueSet{AssigneeID: PatchField[string]{Set: true, Value: &uid}}); err != nil {
		t.Fatalf("BulkSetIssues assign: %v", err)
	}

	if got := issueStateID(t, fx, fx.iss.ID); got != fx.doneID {
		t.Fatalf("issue state = %s, want cascade-moved to %s", got, fx.doneID)
	}
	assertIssueUpdatedIDs(t, fake, fx.iss.ID)
	for _, payload := range issueUpdatedPayloads(t, fake) {
		if payload.StateID != fx.doneID {
			t.Fatalf("issue.updated state_id = %s, want final %s", payload.StateID, fx.doneID)
		}
	}
	if got := bulkSetIssueAssignees(t, fx, fx.iss.ID); len(got) != 1 || got[0] != fx.actor {
		t.Fatalf("committed assignees = %v, want [%s]", got, fx.actor)
	}
	assertLinkedObjectiveEvents(t, fake, fx.obID)
}

func TestBulkSetLabelsOnlyNoCascadeBroadcastsNoObjective(t *testing.T) {
	fx := setupLinkedIssue(t)
	ctx := context.Background()
	labelID := createBulkSetLabel(t, fx, "plain-label")
	fake := installCapturingBroadcaster(t)

	labelIDs := []string{labelID}
	if _, _, err := BulkSetIssues(ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		[]string{fx.iss.ID}, BulkIssueSet{LabelIDs: &labelIDs}); err != nil {
		t.Fatalf("BulkSetIssues labels: %v", err)
	}

	// Labels changed, state did not: one issue.updated, no objective fan-out.
	if got := issueStateID(t, fx, fx.iss.ID); got != fx.backlogID {
		t.Fatalf("issue state = %s, want unmoved %s", got, fx.backlogID)
	}
	assertIssueUpdatedIDs(t, fake, fx.iss.ID)
	for _, payload := range issueUpdatedPayloads(t, fake) {
		if payload.StateID != fx.backlogID {
			t.Fatalf("issue.updated state_id = %s, want %s", payload.StateID, fx.backlogID)
		}
	}
	if got := bulkSetIssueLabels(t, fx, fx.iss.ID); len(got) != 1 || got[0] != labelID {
		t.Fatalf("committed labels = %v, want [%s]", got, labelID)
	}
	assertLinkedObjectiveEvents(t, fake)
}

func TestBulkSetDirectStatePlusLabelsCascadeFansOutOnce(t *testing.T) {
	fx := setupLinkedIssue(t)
	ctx := context.Background()
	started, err := CreateState(ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		StateInput{Name: "Started", Group: "started"})
	if err != nil {
		t.Fatalf("CreateState started: %v", err)
	}
	labelID := createBulkSetLabel(t, fx, "double-move-label")
	// Cascade rule fires on the label change and moves the issue one
	// step past the direct set — direct + cascade on one item.
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.actor, AutomationRuleInput{
		Name:    "label finisher",
		Trigger: AutomationTrigger{Type: "issue.labels_changed"},
		Actions: []AutomationAction{{Type: "set_state", StateID: &fx.doneID}},
	}); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	// Second issue linked to the SAME objective: the batch must still
	// fan out exactly once per objective (DISTINCT across the batch).
	other, err := CreateIssue(ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		CreateIssueInput{Name: "linked 2", StateID: &fx.backlogID})
	if err != nil {
		t.Fatalf("CreateIssue other: %v", err)
	}
	if err := LinkIssueToKeyResult(ctx, fx.pool, fx.slug, fx.actor, fx.obID, fx.krID, other.ID); err != nil {
		t.Fatalf("link other: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	labelIDs := []string{labelID}
	if _, _, err := BulkSetIssues(ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		[]string{fx.iss.ID, other.ID},
		BulkIssueSet{StateID: &started.ID, LabelIDs: &labelIDs}); err != nil {
		t.Fatalf("BulkSetIssues state+labels: %v", err)
	}

	// Both issues end at the cascade target, not the direct set.
	for _, id := range []string{fx.iss.ID, other.ID} {
		if got := issueStateID(t, fx, id); got != fx.doneID {
			t.Fatalf("issue %s state = %s, want cascade-moved to %s", id, got, fx.doneID)
		}
	}
	assertIssueUpdatedIDs(t, fake, fx.iss.ID, other.ID)
	for _, payload := range issueUpdatedPayloads(t, fake) {
		if payload.StateID != fx.doneID {
			t.Fatalf("issue.updated state_id = %s, want final %s", payload.StateID, fx.doneID)
		}
	}
	// Exactly one objective.updated for the shared objective, even
	// though two issues moved (direct + cascade each).
	assertLinkedObjectiveEvents(t, fake, fx.obID)
}
