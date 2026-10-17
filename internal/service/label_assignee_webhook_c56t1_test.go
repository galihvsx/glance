package service

// Label/assignee attach/detach webhook fan-out (C56T1): the four
// standalone label.go endpoints must enqueue exactly ONE issue.updated
// webhook in-tx when — and only when — the mutation really changed
// something. changed_fields carries the direct change ("labels" /
// "assignees") plus any automation-cascade row change (e.g. a
// labels_changed -> set_state rule adds "state_id"), computed from a
// before/after row comparison. Idempotent no-op re-attach/detach
// enqueues nothing.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func c56t1Webhook(t *testing.T, fx *linkedIssueFixture) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	wh, err := CreateWebhook(fx.ctx, fx.pool, fx.slug, fx.actor, WebhookInput{
		URL: srv.URL, Secret: strPtr2("s3cret"),
		Events: []string{EventIssueUpdated},
	})
	if err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}
	// Good-citizen cleanup: the dispatcher claims the OLDEST 25
	// pending webhook rows per pass (webhookBatchSize), so leftover
	// rows from these tests would starve later dispatcher tests
	// (TestDeliverRowBlocksPrivateTarget asserts its own row is
	// processed in a single pass). Delete this webhook's rows when
	// the test ends — assertions already ran by then.
	t.Cleanup(func() {
		if _, err := fx.pool.Exec(context.Background(),
			`DELETE FROM outbox WHERE payload->>'webhook_id' = $1`, wh.ID); err != nil {
			t.Errorf("cleanup outbox rows: %v", err)
		}
	})
	return wh.ID
}

func c56t1AssertChangedFields(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("changed_fields = %v, want exactly %v", got, want)
	}
	seen := map[string]bool{}
	for _, f := range got {
		seen[f] = true
	}
	for _, f := range want {
		if !seen[f] {
			t.Fatalf("changed_fields = %v, want exactly %v", got, want)
		}
	}
}

func c56t1AssertRowShape(t *testing.T, fx *linkedIssueFixture, row webhookOutboxPayload) {
	t.Helper()
	if row.Data.ID != fx.iss.ID {
		t.Fatalf("data.id = %q, want %q", row.Data.ID, fx.iss.ID)
	}
	wantDisplay := fx.ident + "-" + strconv.Itoa(fx.iss.SequenceID)
	if row.Data.DisplayID != wantDisplay {
		t.Fatalf("data.display_id = %q, want %q", row.Data.DisplayID, wantDisplay)
	}
	if row.Data.ActorID != fx.actor {
		t.Fatalf("data.actor_id = %q, want %q", row.Data.ActorID, fx.actor)
	}
}

func TestLabelWebhookAssignLabel(t *testing.T) {
	fx := setupLinkedIssue(t)
	whID := c56t1Webhook(t, fx)
	label := createTestLabel(t, fx.pool, fx.slug, fx.ident, fx.actor, "wh-bug")

	watermark := outboxWatermark(t, fx.pool)
	if err := AssignLabel(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, label.ID, fx.actor); err != nil {
		t.Fatalf("AssignLabel: %v", err)
	}
	rows := webhookOutboxRows(t, fx.pool, watermark, whID, EventIssueUpdated)
	if len(rows) != 1 {
		t.Fatalf("issue.updated rows = %d, want exactly 1", len(rows))
	}
	c56t1AssertRowShape(t, fx, rows[0])
	c56t1AssertChangedFields(t, rows[0].Data.ChangedFields, "labels")
}

func TestLabelWebhookReattachLabelNoRow(t *testing.T) {
	fx := setupLinkedIssue(t)
	whID := c56t1Webhook(t, fx)
	label := createTestLabel(t, fx.pool, fx.slug, fx.ident, fx.actor, "wh-reattach")
	if err := AssignLabel(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, label.ID, fx.actor); err != nil {
		t.Fatalf("AssignLabel setup: %v", err)
	}

	watermark := outboxWatermark(t, fx.pool)
	if err := AssignLabel(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, label.ID, fx.actor); err != nil {
		t.Fatalf("AssignLabel twice: %v", err)
	}
	if rows := webhookOutboxRows(t, fx.pool, watermark, whID, EventIssueUpdated); len(rows) != 0 {
		t.Fatalf("issue.updated rows after re-attach = %d, want 0", len(rows))
	}
}

func TestLabelWebhookUnassignLabel(t *testing.T) {
	fx := setupLinkedIssue(t)
	whID := c56t1Webhook(t, fx)
	label := createTestLabel(t, fx.pool, fx.slug, fx.ident, fx.actor, "wh-detach")
	if err := AssignLabel(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, label.ID, fx.actor); err != nil {
		t.Fatalf("AssignLabel setup: %v", err)
	}

	watermark := outboxWatermark(t, fx.pool)
	if err := UnassignLabel(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, label.ID, fx.actor); err != nil {
		t.Fatalf("UnassignLabel: %v", err)
	}
	rows := webhookOutboxRows(t, fx.pool, watermark, whID, EventIssueUpdated)
	if len(rows) != 1 {
		t.Fatalf("issue.updated rows = %d, want exactly 1", len(rows))
	}
	c56t1AssertRowShape(t, fx, rows[0])
	c56t1AssertChangedFields(t, rows[0].Data.ChangedFields, "labels")

	// Idempotent re-detach enqueues nothing.
	watermark = outboxWatermark(t, fx.pool)
	if err := UnassignLabel(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, label.ID, fx.actor); err != nil {
		t.Fatalf("UnassignLabel twice: %v", err)
	}
	if rows := webhookOutboxRows(t, fx.pool, watermark, whID, EventIssueUpdated); len(rows) != 0 {
		t.Fatalf("issue.updated rows after re-detach = %d, want 0", len(rows))
	}
}

func TestLabelWebhookAssignAssignee(t *testing.T) {
	fx := setupLinkedIssue(t)
	whID := c56t1Webhook(t, fx)
	member := createTestUser(t, fx.pool, uniqueTestEmail("wh-assignee"))
	addTestMember(t, fx.pool, fx.slug, fx.actor, member, RoleMember)

	watermark := outboxWatermark(t, fx.pool)
	if err := AssignAssignee(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, member, fx.actor); err != nil {
		t.Fatalf("AssignAssignee: %v", err)
	}
	rows := webhookOutboxRows(t, fx.pool, watermark, whID, EventIssueUpdated)
	if len(rows) != 1 {
		t.Fatalf("issue.updated rows = %d, want exactly 1", len(rows))
	}
	c56t1AssertRowShape(t, fx, rows[0])
	c56t1AssertChangedFields(t, rows[0].Data.ChangedFields, "assignees")

	// Idempotent re-assign enqueues nothing.
	watermark = outboxWatermark(t, fx.pool)
	if err := AssignAssignee(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, member, fx.actor); err != nil {
		t.Fatalf("AssignAssignee twice: %v", err)
	}
	if rows := webhookOutboxRows(t, fx.pool, watermark, whID, EventIssueUpdated); len(rows) != 0 {
		t.Fatalf("issue.updated rows after re-assign = %d, want 0", len(rows))
	}
}

func TestLabelWebhookUnassignAssignee(t *testing.T) {
	fx := setupLinkedIssue(t)
	whID := c56t1Webhook(t, fx)
	member := createTestUser(t, fx.pool, uniqueTestEmail("wh-unassignee"))
	addTestMember(t, fx.pool, fx.slug, fx.actor, member, RoleMember)
	if err := AssignAssignee(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, member, fx.actor); err != nil {
		t.Fatalf("AssignAssignee setup: %v", err)
	}

	watermark := outboxWatermark(t, fx.pool)
	if err := UnassignAssignee(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, member, fx.actor); err != nil {
		t.Fatalf("UnassignAssignee: %v", err)
	}
	rows := webhookOutboxRows(t, fx.pool, watermark, whID, EventIssueUpdated)
	if len(rows) != 1 {
		t.Fatalf("issue.updated rows = %d, want exactly 1", len(rows))
	}
	c56t1AssertRowShape(t, fx, rows[0])
	c56t1AssertChangedFields(t, rows[0].Data.ChangedFields, "assignees")

	// Idempotent re-unassign enqueues nothing.
	watermark = outboxWatermark(t, fx.pool)
	if err := UnassignAssignee(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, member, fx.actor); err != nil {
		t.Fatalf("UnassignAssignee twice: %v", err)
	}
	if rows := webhookOutboxRows(t, fx.pool, watermark, whID, EventIssueUpdated); len(rows) != 0 {
		t.Fatalf("issue.updated rows after re-unassign = %d, want 0", len(rows))
	}
}

func TestLabelWebhookAssignLabelCascadeSetState(t *testing.T) {
	fx := setupLinkedIssue(t)
	whID := c56t1Webhook(t, fx)
	label := createTestLabel(t, fx.pool, fx.slug, fx.ident, fx.actor, "wh-cascade-state")
	if _, err := CreateAutomationRule(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor, AutomationRuleInput{
		Name:    "label finisher",
		Trigger: AutomationTrigger{Type: "issue.labels_changed"},
		Actions: []AutomationAction{{Type: "set_state", StateID: &fx.doneID}},
	}); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	watermark := outboxWatermark(t, fx.pool)
	if err := AssignLabel(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, label.ID, fx.actor); err != nil {
		t.Fatalf("AssignLabel: %v", err)
	}
	rows := webhookOutboxRows(t, fx.pool, watermark, whID, EventIssueUpdated)
	if len(rows) != 1 {
		t.Fatalf("issue.updated rows = %d, want exactly 1 (one row, not one per change)", len(rows))
	}
	c56t1AssertRowShape(t, fx, rows[0])
	c56t1AssertChangedFields(t, rows[0].Data.ChangedFields, "labels", "state_id")
}

func TestLabelWebhookUnassignLabelCascadeSetPriority(t *testing.T) {
	fx := setupLinkedIssue(t)
	whID := c56t1Webhook(t, fx)
	label := createTestLabel(t, fx.pool, fx.slug, fx.ident, fx.actor, "wh-cascade-prio")
	// Attach BEFORE the rule exists, so the attach itself fires nothing.
	if err := AssignLabel(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, label.ID, fx.actor); err != nil {
		t.Fatalf("AssignLabel setup: %v", err)
	}
	if _, err := CreateAutomationRule(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor, AutomationRuleInput{
		Name:    "unlabel prioritizer",
		Trigger: AutomationTrigger{Type: "issue.labels_changed"},
		Actions: []AutomationAction{{Type: "set_priority", Priority: autoIntPtr(3)}},
	}); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	watermark := outboxWatermark(t, fx.pool)
	if err := UnassignLabel(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, label.ID, fx.actor); err != nil {
		t.Fatalf("UnassignLabel: %v", err)
	}
	rows := webhookOutboxRows(t, fx.pool, watermark, whID, EventIssueUpdated)
	if len(rows) != 1 {
		t.Fatalf("issue.updated rows = %d, want exactly 1", len(rows))
	}
	c56t1AssertRowShape(t, fx, rows[0])
	c56t1AssertChangedFields(t, rows[0].Data.ChangedFields, "labels", "priority")
}

func TestLabelWebhookAssignAssigneeCascadeSetState(t *testing.T) {
	fx := setupLinkedIssue(t)
	whID := c56t1Webhook(t, fx)
	member := createTestUser(t, fx.pool, uniqueTestEmail("wh-assignee-cascade"))
	addTestMember(t, fx.pool, fx.slug, fx.actor, member, RoleMember)
	if _, err := CreateAutomationRule(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor, AutomationRuleInput{
		Name:    "assigned finisher",
		Trigger: AutomationTrigger{Type: "issue.assigned"},
		Actions: []AutomationAction{{Type: "set_state", StateID: &fx.doneID}},
	}); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	watermark := outboxWatermark(t, fx.pool)
	if err := AssignAssignee(fx.ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, member, fx.actor); err != nil {
		t.Fatalf("AssignAssignee: %v", err)
	}
	rows := webhookOutboxRows(t, fx.pool, watermark, whID, EventIssueUpdated)
	if len(rows) != 1 {
		t.Fatalf("issue.updated rows = %d, want exactly 1", len(rows))
	}
	c56t1AssertRowShape(t, fx, rows[0])
	c56t1AssertChangedFields(t, rows[0].Data.ChangedFields, "assignees", "state_id")
}
