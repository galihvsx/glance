package service

// Label/assignee broadcast (C46T1): the four standalone label.go paths
// announce exactly one issue.updated after commit on a REAL change
// (RowsAffected > 0), with the post-commit snapshot. Idempotent no-ops
// (0 rows) and failed mutations announce nothing. When an in-tx
// automation firing's set_state actually moves the issue, linked
// objectives get objective.updated too (before/after state_id
// detector). The recurring caller inherits the broadcast: one
// ExecuteSchedule run emits created then updated per attach.

import (
	"testing"
	"time"
)

// assertLabelBroadcastUpdated asserts the fake captured exactly one
// issue.updated for issueID with the final snapshot (name, state_id),
// and nothing else of that type. DisplayID is NOT asserted: the
// post-commit snapshot (C45T1 pattern: bare scanIssue) carries no
// derived DisplayID — the same as the C45 restore/automation/state
// snapshot paths.
func assertLabelBroadcastUpdated(t *testing.T, fake *capturingBroadcaster, fx *linkedIssueFixture, wantState string) {
	t.Helper()
	if n := countEventsOfType(t, fake, EventIssueUpdated); n != 1 {
		t.Fatalf("issue.updated = %d, want exactly 1", n)
	}
	payloads := issueUpdatedPayloads(t, fake)
	payload, ok := payloads[fx.iss.ID]
	if !ok {
		t.Fatalf("no issue.updated for %s: %+v", fx.iss.ID, payloads)
	}
	if payload.Name != fx.iss.Name || payload.StateID != wantState {
		t.Fatalf("issue.updated payload = %+v, want name %q state %q",
			payload, fx.iss.Name, wantState)
	}
}

func TestLabelBroadcastAssignLabel(t *testing.T) {
	fx := setupLinkedIssue(t)
	label := createTestLabel(t, fx.pool, fx.slug, fx.ident, fx.actor, "broadcast-bug")
	fake := installCapturingBroadcaster(t)

	if err := AssignLabel(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, label.ID, fx.actor); err != nil {
		t.Fatalf("AssignLabel: %v", err)
	}
	assertLabelBroadcastUpdated(t, fake, fx, fx.backlogID)
	assertLinkedObjectiveEvents(t, fake)

	// Idempotent re-attach: still exactly one issue.updated, no
	// objective fan-out.
	if err := AssignLabel(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, label.ID, fx.actor); err != nil {
		t.Fatalf("AssignLabel twice: %v", err)
	}
	assertLabelBroadcastUpdated(t, fake, fx, fx.backlogID)
	assertLinkedObjectiveEvents(t, fake)
}

func TestLabelBroadcastUnassignLabel(t *testing.T) {
	fx := setupLinkedIssue(t)
	label := createTestLabel(t, fx.pool, fx.slug, fx.ident, fx.actor, "broadcast-bug")
	if err := AssignLabel(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, label.ID, fx.actor); err != nil {
		t.Fatalf("AssignLabel setup: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	if err := UnassignLabel(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, label.ID, fx.actor); err != nil {
		t.Fatalf("UnassignLabel: %v", err)
	}
	assertLabelBroadcastUpdated(t, fake, fx, fx.backlogID)
	assertLinkedObjectiveEvents(t, fake)

	// Idempotent unassign of an absent label: no new events.
	if err := UnassignLabel(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, label.ID, fx.actor); err != nil {
		t.Fatalf("UnassignLabel twice: %v", err)
	}
	assertLabelBroadcastUpdated(t, fake, fx, fx.backlogID)
	assertLinkedObjectiveEvents(t, fake)
}

func TestLabelBroadcastAssignAssignee(t *testing.T) {
	fx := setupLinkedIssue(t)
	member := createTestUser(t, fx.pool, uniqueTestEmail("label-bcast-assignee"))
	addTestMember(t, fx.pool, fx.slug, fx.actor, member, RoleMember)
	fake := installCapturingBroadcaster(t)

	if err := AssignAssignee(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, member, fx.actor); err != nil {
		t.Fatalf("AssignAssignee: %v", err)
	}
	assertLabelBroadcastUpdated(t, fake, fx, fx.backlogID)
	assertLinkedObjectiveEvents(t, fake)

	// Idempotent re-assign: no new issue.updated.
	if err := AssignAssignee(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, member, fx.actor); err != nil {
		t.Fatalf("AssignAssignee twice: %v", err)
	}
	assertLabelBroadcastUpdated(t, fake, fx, fx.backlogID)
	assertLinkedObjectiveEvents(t, fake)
}

func TestLabelBroadcastUnassignAssignee(t *testing.T) {
	fx := setupLinkedIssue(t)
	member := createTestUser(t, fx.pool, uniqueTestEmail("label-bcast-unassign"))
	addTestMember(t, fx.pool, fx.slug, fx.actor, member, RoleMember)
	if err := AssignAssignee(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, member, fx.actor); err != nil {
		t.Fatalf("AssignAssignee setup: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	if err := UnassignAssignee(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, member, fx.actor); err != nil {
		t.Fatalf("UnassignAssignee: %v", err)
	}
	assertLabelBroadcastUpdated(t, fake, fx, fx.backlogID)
	assertLinkedObjectiveEvents(t, fake)

	// Idempotent unassign of an absent assignee: no new events.
	if err := UnassignAssignee(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, member, fx.actor); err != nil {
		t.Fatalf("UnassignAssignee twice: %v", err)
	}
	assertLabelBroadcastUpdated(t, fake, fx, fx.backlogID)
	assertLinkedObjectiveEvents(t, fake)
}

func TestLabelBroadcastAssignLabelCascadeSetState(t *testing.T) {
	fx := setupLinkedIssue(t)
	label := createTestLabel(t, fx.pool, fx.slug, fx.ident, fx.actor, "broadcast-cascade")
	if _, err := CreateAutomationRule(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor, AutomationRuleInput{
		Name:    "label finisher",
		Trigger: AutomationTrigger{Type: "issue.labels_changed"},
		Actions: []AutomationAction{{Type: "set_state", StateID: &fx.doneID}},
	}); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	if err := AssignLabel(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, label.ID, fx.actor); err != nil {
		t.Fatalf("AssignLabel: %v", err)
	}

	// Exactly one issue.updated carrying the cascade-moved state, plus
	// the objective fan-out — not one announce per cascade step.
	assertLabelBroadcastUpdated(t, fake, fx, fx.doneID)
	assertLinkedObjectiveEvents(t, fake, fx.obID)
}

func TestLabelBroadcastAssignAssigneeCascadeSetState(t *testing.T) {
	fx := setupLinkedIssue(t)
	member := createTestUser(t, fx.pool, uniqueTestEmail("label-bcast-acascade"))
	addTestMember(t, fx.pool, fx.slug, fx.actor, member, RoleMember)
	if _, err := CreateAutomationRule(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor, AutomationRuleInput{
		Name:    "assigned finisher",
		Trigger: AutomationTrigger{Type: "issue.assigned"},
		Actions: []AutomationAction{{Type: "set_state", StateID: &fx.doneID}},
	}); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	if err := AssignAssignee(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, member, fx.actor); err != nil {
		t.Fatalf("AssignAssignee: %v", err)
	}

	assertLabelBroadcastUpdated(t, fake, fx, fx.doneID)
	assertLinkedObjectiveEvents(t, fake, fx.obID)
}

func TestLabelBroadcastUnassignLabelCascadeSetState(t *testing.T) {
	fx := setupLinkedIssue(t)
	label := createTestLabel(t, fx.pool, fx.slug, fx.ident, fx.actor, "broadcast-uncascade")
	if err := AssignLabel(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, label.ID, fx.actor); err != nil {
		t.Fatalf("AssignLabel setup: %v", err)
	}
	// Rule created AFTER the attach, so the detach firing is the one
	// that moves the issue (backlog -> done).
	if _, err := CreateAutomationRule(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor, AutomationRuleInput{
		Name:    "unlabel finisher",
		Trigger: AutomationTrigger{Type: "issue.labels_changed"},
		Actions: []AutomationAction{{Type: "set_state", StateID: &fx.doneID}},
	}); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	if err := UnassignLabel(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, label.ID, fx.actor); err != nil {
		t.Fatalf("UnassignLabel: %v", err)
	}

	assertLabelBroadcastUpdated(t, fake, fx, fx.doneID)
	assertLinkedObjectiveEvents(t, fake, fx.obID)
}

func TestLabelBroadcastUnassignAssigneeCascadeSetState(t *testing.T) {
	fx := setupLinkedIssue(t)
	member := createTestUser(t, fx.pool, uniqueTestEmail("label-bcast-ucascade"))
	addTestMember(t, fx.pool, fx.slug, fx.actor, member, RoleMember)
	if err := AssignAssignee(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, member, fx.actor); err != nil {
		t.Fatalf("AssignAssignee setup: %v", err)
	}
	if _, err := CreateAutomationRule(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor, AutomationRuleInput{
		Name:    "unassigned finisher",
		Trigger: AutomationTrigger{Type: "issue.unassigned"},
		Actions: []AutomationAction{{Type: "set_state", StateID: &fx.doneID}},
	}); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	if err := UnassignAssignee(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, member, fx.actor); err != nil {
		t.Fatalf("UnassignAssignee: %v", err)
	}

	assertLabelBroadcastUpdated(t, fake, fx, fx.doneID)
	assertLinkedObjectiveEvents(t, fake, fx.obID)
}

func TestLabelBroadcastCascadeNoopSetState(t *testing.T) {
	fx := setupLinkedIssue(t)
	label := createTestLabel(t, fx.pool, fx.slug, fx.ident, fx.actor, "broadcast-noop")
	if _, err := CreateAutomationRule(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor, AutomationRuleInput{
		Name:    "label no-op",
		Trigger: AutomationTrigger{Type: "issue.labels_changed"},
		Actions: []AutomationAction{{Type: "set_state", StateID: &fx.backlogID}},
	}); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	if err := AssignLabel(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, label.ID, fx.actor); err != nil {
		t.Fatalf("AssignLabel: %v", err)
	}

	// The attach is real, so issue.updated fires — but the firing's
	// set_state targeted the current state, so no objective fan-out.
	assertLabelBroadcastUpdated(t, fake, fx, fx.backlogID)
	assertLinkedObjectiveEvents(t, fake)
}

func TestLabelBroadcastIdempotentReattachWithRuleAnnouncesNothingNew(t *testing.T) {
	fx := setupLinkedIssue(t)
	label := createTestLabel(t, fx.pool, fx.slug, fx.ident, fx.actor, "broadcast-reattach")
	if _, err := CreateAutomationRule(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor, AutomationRuleInput{
		Name:    "label finisher",
		Trigger: AutomationTrigger{Type: "issue.labels_changed"},
		Actions: []AutomationAction{{Type: "set_state", StateID: &fx.doneID}},
	}); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	fake := installCapturingBroadcaster(t)

	if err := AssignLabel(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, label.ID, fx.actor); err != nil {
		t.Fatalf("AssignLabel: %v", err)
	}
	assertLabelBroadcastUpdated(t, fake, fx, fx.doneID)
	assertLinkedObjectiveEvents(t, fake, fx.obID)

	// Idempotent re-attach: 0 rows, no firing, no new events at all.
	if err := AssignLabel(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, label.ID, fx.actor); err != nil {
		t.Fatalf("AssignLabel twice: %v", err)
	}
	assertLabelBroadcastUpdated(t, fake, fx, fx.doneID)
	assertLinkedObjectiveEvents(t, fake, fx.obID)
}

func TestLabelBroadcastFailedMutationAnnouncesNothing(t *testing.T) {
	fx := setupLinkedIssue(t)
	label := createTestLabel(t, fx.pool, fx.slug, fx.ident, fx.actor, "broadcast-fail")
	fake := installCapturingBroadcaster(t)

	// Foreign label: rejected before any mutation.
	other := createTestUser(t, fx.pool, uniqueTestEmail("label-bcast-other"))
	ws2 := createTestWorkspace(t, fx.pool, "Other", uniqueTestSlug("other-bcast"), other)
	p2 := createTestProject(t, fx.pool, ws2.Slug, other, "Eng", uniqueTestIdentifier())
	foreign := createTestLabel(t, fx.pool, ws2.Slug, p2.Identifier, other, "foreign")
	if err := AssignLabel(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, foreign.ID, fx.actor); !isErr(err, ErrLabelNotFound) {
		t.Fatalf("foreign label err = %v, want ErrLabelNotFound", err)
	}
	// Missing issue: rejected before any mutation.
	if err := AssignLabel(fx.ctx, fx.pool, fx.slug, fx.ident,
		"00000000-0000-0000-0000-000000000000", label.ID, fx.actor); !isErr(err, ErrIssueNotFound) {
		t.Fatalf("missing issue err = %v, want ErrIssueNotFound", err)
	}
	// Non-member assignee: rejected before any mutation.
	outsider := createTestUser(t, fx.pool, uniqueTestEmail("label-bcast-outsider"))
	if err := AssignAssignee(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, outsider, fx.actor); !isErr(err, ErrAssigneeNotMember) {
		t.Fatalf("outsider err = %v, want ErrAssigneeNotMember", err)
	}
	// Malformed ids: idempotent no-ops, not errors, not events.
	if err := UnassignLabel(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, "not-a-uuid", fx.actor); err != nil {
		t.Fatalf("malformed UnassignLabel: %v", err)
	}
	if err := UnassignAssignee(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, "not-a-uuid", fx.actor); err != nil {
		t.Fatalf("malformed UnassignAssignee: %v", err)
	}
	assertZeroEvents(t, fake)
}

func TestLabelBroadcastRecurringSequence(t *testing.T) {
	ctx, pool, slug, wsID, actor, projectID, _, labelID, stateID, assignee := executeTestSetup(t)

	runAt := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	in := validScheduleInput()
	in.Frequency = "daily"
	in.DaysOfWeek = nil
	in.TitleTemplate = "Daily standup {{date}}"
	in.StateID = &stateID
	in.LabelIDs = []string{labelID}
	in.AssigneeIDs = []string{assignee}
	s := mustDueSchedule(t, ctx, pool, slug, projectID, actor, in, runAt)
	fake := installCapturingBroadcaster(t)

	if _, err := ExecuteSchedule(ctx, pool, wsID, projectID, s, runAt, actor); err != nil {
		t.Fatalf("ExecuteSchedule: %v", err)
	}

	// The recurring run inherits the broadcast: CreateIssue announces
	// created, then one issue.updated per attach (label + assignee).
	// No cascade rule exists, so no objective fan-out.
	if n := countEventsOfType(t, fake, EventIssueCreated); n != 1 {
		t.Fatalf("issue.created = %d, want 1", n)
	}
	if n := countEventsOfType(t, fake, EventIssueUpdated); n != 2 {
		t.Fatalf("issue.updated = %d, want 2 (label + assignee attaches)", n)
	}
	assertLinkedObjectiveEvents(t, fake)
}
