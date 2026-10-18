package service

// Label/assignee cascade + membership version snapshots (C50T1,
// extended by C65T0): an in-tx automation firing (labels_changed /
// assigned / unassigned) can move the issue row via set_state /
// set_priority / etc. — that cascade move lands exactly one version
// snapshot, in the same tx, carrying the post-cascade row (C50T1). On
// top of that, a REAL membership set change (attach/detach/assign/
// unassign, 1 row) with NO cascade row movement lands exactly one
// membership version carrying the post-mutation row (C65T0) — so the
// membership change is never invisible in the version history. A
// firing whose actions are all no-ops yields no cascade snapshot (but
// the real membership change still yields its one membership version).
// Idempotent 0-row calls (re-attach, detach of an absent label, ...)
// snapshot nothing. Baseline: setupLinkedIssue's CreateIssue already
// wrote v1.

import (
	"testing"
)

func TestLabelCascadeVersionAssignLabelSetState(t *testing.T) {
	fx := setupLinkedIssue(t)
	label := createTestLabel(t, fx.pool, fx.slug, fx.ident, fx.actor, "cascade-bug")
	if _, err := CreateAutomationRule(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor, AutomationRuleInput{
		Name:    "label finisher",
		Trigger: AutomationTrigger{Type: "issue.labels_changed"},
		Actions: []AutomationAction{{Type: "set_state", StateID: &fx.doneID}},
	}); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	if got := versionCount(t, fx, fx.iss.ID); got != 1 {
		t.Fatalf("versions before = %d, want 1 (v1 create)", got)
	}

	if err := AssignLabel(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, label.ID, fx.actor); err != nil {
		t.Fatalf("AssignLabel: %v", err)
	}
	if got := versionCount(t, fx, fx.iss.ID); got != 2 {
		t.Fatalf("versions = %d, want 2 (exactly one cascade snapshot)", got)
	}
	if snap := latestVersionSnapshot(t, fx, fx.iss.ID); snap.StateID != fx.doneID {
		t.Fatalf("snapshot StateID = %s, want cascade-moved %s", snap.StateID, fx.doneID)
	}
}

func TestLabelCascadeVersionUnassignLabelSetPriority(t *testing.T) {
	fx := setupLinkedIssue(t)
	label := createTestLabel(t, fx.pool, fx.slug, fx.ident, fx.actor, "cascade-prio")
	// Attach BEFORE the rule exists, so the attach itself fires
	// nothing: C65T0 still snapshots the real membership change.
	if err := AssignLabel(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, label.ID, fx.actor); err != nil {
		t.Fatalf("AssignLabel setup: %v", err)
	}
	if got := versionCount(t, fx, fx.iss.ID); got != 2 {
		t.Fatalf("versions after attach = %d, want 2 (v1 create + v2 membership)", got)
	}
	if _, err := CreateAutomationRule(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor, AutomationRuleInput{
		Name:    "unlabel prioritizer",
		Trigger: AutomationTrigger{Type: "issue.labels_changed"},
		Actions: []AutomationAction{{Type: "set_priority", Priority: autoIntPtr(3)}},
	}); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	if err := UnassignLabel(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, label.ID, fx.actor); err != nil {
		t.Fatalf("UnassignLabel: %v", err)
	}
	if got := versionCount(t, fx, fx.iss.ID); got != 3 {
		t.Fatalf("versions = %d, want 3 (v2 membership + exactly one cascade snapshot)", got)
	}
	if snap := latestVersionSnapshot(t, fx, fx.iss.ID); snap.Priority != 3 {
		t.Fatalf("snapshot Priority = %d, want cascade-set 3", snap.Priority)
	}
}

func TestLabelCascadeVersionAssignAssigneeSetState(t *testing.T) {
	fx := setupLinkedIssue(t)
	member := createTestUser(t, fx.pool, uniqueTestEmail("cascade-assignee"))
	addTestMember(t, fx.pool, fx.slug, fx.actor, member, RoleMember)
	if _, err := CreateAutomationRule(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor, AutomationRuleInput{
		Name:    "assigned finisher",
		Trigger: AutomationTrigger{Type: "issue.assigned"},
		Actions: []AutomationAction{{Type: "set_state", StateID: &fx.doneID}},
	}); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	if err := AssignAssignee(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, member, fx.actor); err != nil {
		t.Fatalf("AssignAssignee: %v", err)
	}
	if got := versionCount(t, fx, fx.iss.ID); got != 2 {
		t.Fatalf("versions = %d, want 2 (exactly one cascade snapshot)", got)
	}
	if snap := latestVersionSnapshot(t, fx, fx.iss.ID); snap.StateID != fx.doneID {
		t.Fatalf("snapshot StateID = %s, want cascade-moved %s", snap.StateID, fx.doneID)
	}
}

func TestLabelCascadeVersionUnassignAssigneeSetState(t *testing.T) {
	fx := setupLinkedIssue(t)
	member := createTestUser(t, fx.pool, uniqueTestEmail("cascade-unassignee"))
	addTestMember(t, fx.pool, fx.slug, fx.actor, member, RoleMember)
	// Assign BEFORE the rule exists, so the assignment itself fires
	// nothing: C65T0 still snapshots the real membership change.
	if err := AssignAssignee(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, member, fx.actor); err != nil {
		t.Fatalf("AssignAssignee setup: %v", err)
	}
	if got := versionCount(t, fx, fx.iss.ID); got != 2 {
		t.Fatalf("versions after assign = %d, want 2 (v1 create + v2 membership)", got)
	}
	if _, err := CreateAutomationRule(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor, AutomationRuleInput{
		Name:    "unassigned finisher",
		Trigger: AutomationTrigger{Type: "issue.unassigned"},
		Actions: []AutomationAction{{Type: "set_state", StateID: &fx.doneID}},
	}); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	if err := UnassignAssignee(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, member, fx.actor); err != nil {
		t.Fatalf("UnassignAssignee: %v", err)
	}
	if got := versionCount(t, fx, fx.iss.ID); got != 3 {
		t.Fatalf("versions = %d, want 3 (v2 membership + exactly one cascade snapshot)", got)
	}
	if snap := latestVersionSnapshot(t, fx, fx.iss.ID); snap.StateID != fx.doneID {
		t.Fatalf("snapshot StateID = %s, want cascade-moved %s", snap.StateID, fx.doneID)
	}
}

func TestLabelMembershipVersionNoRuleStillSnapshots(t *testing.T) {
	fx := setupLinkedIssue(t)
	label := createTestLabel(t, fx.pool, fx.slug, fx.ident, fx.actor, "cascade-norule")
	member := createTestUser(t, fx.pool, uniqueTestEmail("cascade-norule-member"))
	addTestMember(t, fx.pool, fx.slug, fx.actor, member, RoleMember)

	// No rule at all: real attach/detach/assign/unassign change the
	// join tables, never the issue row — but C65T0 still snapshots
	// each real membership change exactly once.
	if err := AssignLabel(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, label.ID, fx.actor); err != nil {
		t.Fatalf("AssignLabel: %v", err)
	}
	if err := AssignAssignee(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, member, fx.actor); err != nil {
		t.Fatalf("AssignAssignee: %v", err)
	}
	if err := UnassignLabel(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, label.ID, fx.actor); err != nil {
		t.Fatalf("UnassignLabel: %v", err)
	}
	if err := UnassignAssignee(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, member, fx.actor); err != nil {
		t.Fatalf("UnassignAssignee: %v", err)
	}
	if got := versionCount(t, fx, fx.iss.ID); got != 5 {
		t.Fatalf("versions = %d, want 5 (v1 create + one per real membership change)", got)
	}
}

func TestLabelCascadeVersionNoopFiringNoSnapshot(t *testing.T) {
	fx := setupLinkedIssue(t)
	label := createTestLabel(t, fx.pool, fx.slug, fx.ident, fx.actor, "cascade-noop")
	// The firing runs (set_state targeting the state the issue is
	// already in), but every action is a no-op, so the row is
	// unchanged and the cascade comparison yields no snapshot — the
	// real membership change still yields its one C65T0 snapshot.
	if _, err := CreateAutomationRule(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor, AutomationRuleInput{
		Name:    "label no-op",
		Trigger: AutomationTrigger{Type: "issue.labels_changed"},
		Actions: []AutomationAction{{Type: "set_state", StateID: &fx.backlogID}},
	}); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	if err := AssignLabel(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, label.ID, fx.actor); err != nil {
		t.Fatalf("AssignLabel: %v", err)
	}
	if got := versionCount(t, fx, fx.iss.ID); got != 2 {
		t.Fatalf("versions = %d, want 2 (no-op firing => no cascade snapshot, but one membership snapshot)", got)
	}
}

func TestLabelCascadeVersionIdempotentNoopNoSnapshot(t *testing.T) {
	fx := setupLinkedIssue(t)
	label := createTestLabel(t, fx.pool, fx.slug, fx.ident, fx.actor, "cascade-idem")
	member := createTestUser(t, fx.pool, uniqueTestEmail("cascade-idem-member"))
	addTestMember(t, fx.pool, fx.slug, fx.actor, member, RoleMember)
	// A live set_state rule is present, so a real firing WOULD
	// snapshot — the idempotent calls must fire nothing (0 rows).
	if _, err := CreateAutomationRule(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor, AutomationRuleInput{
		Name:    "label finisher",
		Trigger: AutomationTrigger{Type: "issue.labels_changed"},
		Actions: []AutomationAction{{Type: "set_state", StateID: &fx.doneID}},
	}); err != nil {
		t.Fatalf("CreateAutomationRule labels: %v", err)
	}
	if _, err := CreateAutomationRule(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor, AutomationRuleInput{
		Name:    "assigned finisher",
		Trigger: AutomationTrigger{Type: "issue.assigned"},
		Actions: []AutomationAction{{Type: "set_state", StateID: &fx.doneID}},
	}); err != nil {
		t.Fatalf("CreateAutomationRule assigned: %v", err)
	}

	// First real attach fires the rule: exactly one snapshot.
	if err := AssignLabel(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, label.ID, fx.actor); err != nil {
		t.Fatalf("AssignLabel: %v", err)
	}
	if got := versionCount(t, fx, fx.iss.ID); got != 2 {
		t.Fatalf("versions after attach = %d, want 2", got)
	}
	// Idempotent re-attach: 0 rows, no firing, no snapshot.
	if err := AssignLabel(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, label.ID, fx.actor); err != nil {
		t.Fatalf("AssignLabel twice: %v", err)
	}
	if got := versionCount(t, fx, fx.iss.ID); got != 2 {
		t.Fatalf("versions after re-attach = %d, want 2 (idempotent => no snapshot)", got)
	}
	// Idempotent unassign of an absent label: 0 rows, no snapshot.
	if err := UnassignLabel(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, label.ID, fx.actor); err != nil {
		t.Fatalf("UnassignLabel: %v", err)
	}
	// The detach was real (label was attached), so it fired the rule —
	// but set_state to done is now a no-op (already done), so no
	// cascade snapshot; the real membership change still yields its
	// one C65T0 snapshot.
	if got := versionCount(t, fx, fx.iss.ID); got != 3 {
		t.Fatalf("versions after detach = %d, want 3 (no-op cascade, one membership snapshot)", got)
	}
	if err := UnassignLabel(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, label.ID, fx.actor); err != nil {
		t.Fatalf("UnassignLabel twice: %v", err)
	}
	if got := versionCount(t, fx, fx.iss.ID); got != 3 {
		t.Fatalf("versions after re-detach = %d, want 3 (idempotent => no snapshot)", got)
	}

	// Assignee side, on a fresh issue (the first issue is already in
	// done, so its assigned firing would be a no-op).
	second, err := CreateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		CreateIssueInput{Name: "cascade-idem-2", StateID: &fx.backlogID})
	if err != nil {
		t.Fatalf("CreateIssue second: %v", err)
	}
	if err := AssignAssignee(fx.ctx, fx.pool, fx.slug, fx.ident, second.ID, member, fx.actor); err != nil {
		t.Fatalf("AssignAssignee: %v", err)
	}
	if got := versionCount(t, fx, second.ID); got != 2 {
		t.Fatalf("versions second after assign = %d, want 2", got)
	}
	if err := AssignAssignee(fx.ctx, fx.pool, fx.slug, fx.ident, second.ID, member, fx.actor); err != nil {
		t.Fatalf("AssignAssignee twice: %v", err)
	}
	if got := versionCount(t, fx, second.ID); got != 2 {
		t.Fatalf("versions second after re-assign = %d, want 2 (idempotent => no snapshot)", got)
	}
	if err := UnassignAssignee(fx.ctx, fx.pool, fx.slug, fx.ident, second.ID, member, fx.actor); err != nil {
		t.Fatalf("UnassignAssignee: %v", err)
	}
	// No unassigned rule exists: real unassign, no firing — but the
	// real membership change still yields its one C65T0 snapshot.
	if got := versionCount(t, fx, second.ID); got != 3 {
		t.Fatalf("versions second after unassign = %d, want 3 (no rule, one membership snapshot)", got)
	}
	if err := UnassignAssignee(fx.ctx, fx.pool, fx.slug, fx.ident, second.ID, member, fx.actor); err != nil {
		t.Fatalf("UnassignAssignee twice: %v", err)
	}
	if got := versionCount(t, fx, second.ID); got != 3 {
		t.Fatalf("versions second after re-unassign = %d, want 3 (idempotent => no snapshot)", got)
	}
}
