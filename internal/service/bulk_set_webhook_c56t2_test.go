package service

// BulkSet webhook fan-out (C56T2): BulkSetIssues must enqueue exactly
// ONE issue.updated webhook in-tx per issue whose final row differs
// from its pre-batch row or whose label/assignee set changed.
// changed_fields is the union of the direct patch fields (already
// enqueued by updateIssueTx when the patch changed something), the
// direct labels/assignees set changes, and the cascade-caused row
// diffs (pre-batch before-image vs final row). When the patch already
// enqueued a row, BulkSetIssues EXTENDS that row's changed_fields
// in-tx to the union instead of adding a second row — so patch +
// cascade / patch + labels still yields exactly one row per issue.
// No-op BulkSets (same set, no patch change) enqueue nothing.

import (
	"strconv"
	"testing"

	"glance/internal/mail"
)

func TestBulkSetWebhookLabelsOnly(t *testing.T) {
	fx := setupLinkedIssue(t)
	whID := c56t1Webhook(t, fx)
	labelID := createBulkSetLabel(t, fx, "bulk-wh-label")

	watermark := outboxWatermark(t, fx.pool)
	labelIDs := []string{labelID}
	if _, _, err := BulkSetIssues(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		[]string{fx.iss.ID}, BulkIssueSet{LabelIDs: &labelIDs}); err != nil {
		t.Fatalf("BulkSetIssues labels: %v", err)
	}
	rows := webhookOutboxRows(t, fx.pool, watermark, whID, EventIssueUpdated)
	if len(rows) != 1 {
		t.Fatalf("issue.updated rows = %d, want exactly 1", len(rows))
	}
	c56t1AssertRowShape(t, fx, rows[0])
	c56t1AssertChangedFields(t, rows[0].Data.ChangedFields, "labels")
}

func TestBulkSetWebhookAssigneesOnly(t *testing.T) {
	fx := setupLinkedIssue(t)
	whID := c56t1Webhook(t, fx)
	member := createTestUser(t, fx.pool, uniqueTestEmail("bulk-wh-assignee"))
	addTestMember(t, fx.pool, fx.slug, fx.actor, member, RoleMember)

	watermark := outboxWatermark(t, fx.pool)
	if _, _, err := BulkSetIssues(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		[]string{fx.iss.ID}, BulkIssueSet{AssigneeID: PatchField[string]{Set: true, Value: &member}}); err != nil {
		t.Fatalf("BulkSetIssues assignee: %v", err)
	}
	rows := webhookOutboxRows(t, fx.pool, watermark, whID, EventIssueUpdated)
	if len(rows) != 1 {
		t.Fatalf("issue.updated rows = %d, want exactly 1", len(rows))
	}
	c56t1AssertRowShape(t, fx, rows[0])
	c56t1AssertChangedFields(t, rows[0].Data.ChangedFields, "assignees")
}

func TestBulkSetWebhookLabelsOnlyCascadeSetState(t *testing.T) {
	fx := setupLinkedIssue(t)
	whID := c56t1Webhook(t, fx)
	bulkSetLabelsChangedRule(t, fx, "label finisher",
		[]AutomationAction{{Type: "set_state", StateID: &fx.doneID}})
	labelID := createBulkSetLabel(t, fx, "bulk-wh-cascade-label")

	watermark := outboxWatermark(t, fx.pool)
	labelIDs := []string{labelID}
	if _, _, err := BulkSetIssues(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		[]string{fx.iss.ID}, BulkIssueSet{LabelIDs: &labelIDs}); err != nil {
		t.Fatalf("BulkSetIssues labels: %v", err)
	}
	rows := webhookOutboxRows(t, fx.pool, watermark, whID, EventIssueUpdated)
	if len(rows) != 1 {
		t.Fatalf("issue.updated rows = %d, want exactly 1 (one row, not one per change)", len(rows))
	}
	c56t1AssertRowShape(t, fx, rows[0])
	c56t1AssertChangedFields(t, rows[0].Data.ChangedFields, "labels", "state_id")
}

func TestBulkSetWebhookNoopSameLabelSet(t *testing.T) {
	fx := setupLinkedIssue(t)
	whID := c56t1Webhook(t, fx)
	labelID := createBulkSetLabel(t, fx, "bulk-wh-noop-label")
	labelIDs := []string{labelID}
	if _, _, err := BulkSetIssues(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		[]string{fx.iss.ID}, BulkIssueSet{LabelIDs: &labelIDs}); err != nil {
		t.Fatalf("BulkSetIssues labels setup: %v", err)
	}

	// Same set again: no row change, no set change, no webhook.
	watermark := outboxWatermark(t, fx.pool)
	if _, _, err := BulkSetIssues(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		[]string{fx.iss.ID}, BulkIssueSet{LabelIDs: &labelIDs}); err != nil {
		t.Fatalf("BulkSetIssues labels again: %v", err)
	}
	if rows := webhookOutboxRows(t, fx.pool, watermark, whID, EventIssueUpdated); len(rows) != 0 {
		t.Fatalf("issue.updated rows after no-op BulkSet = %d, want 0", len(rows))
	}
}

func TestBulkSetWebhookPatchOnly(t *testing.T) {
	fx := setupLinkedIssue(t)
	whID := c56t1Webhook(t, fx)

	watermark := outboxWatermark(t, fx.pool)
	if _, _, err := BulkSetIssues(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		[]string{fx.iss.ID}, BulkIssueSet{Priority: autoIntPtr(2)}); err != nil {
		t.Fatalf("BulkSetIssues priority: %v", err)
	}
	rows := webhookOutboxRows(t, fx.pool, watermark, whID, EventIssueUpdated)
	if len(rows) != 1 {
		t.Fatalf("issue.updated rows = %d, want exactly 1 (no regression)", len(rows))
	}
	c56t1AssertRowShape(t, fx, rows[0])
	c56t1AssertChangedFields(t, rows[0].Data.ChangedFields, "priority")
}

func TestBulkSetWebhookPatchPlusLabelsNoCascade(t *testing.T) {
	fx := setupLinkedIssue(t)
	whID := c56t1Webhook(t, fx)
	labelID := createBulkSetLabel(t, fx, "bulk-wh-patch-label")

	// Patch change + label-set change, no rule: the patch row is
	// EXTENDED to the union, not duplicated.
	watermark := outboxWatermark(t, fx.pool)
	labelIDs := []string{labelID}
	if _, _, err := BulkSetIssues(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		[]string{fx.iss.ID}, BulkIssueSet{Priority: autoIntPtr(1), LabelIDs: &labelIDs}); err != nil {
		t.Fatalf("BulkSetIssues priority+labels: %v", err)
	}
	rows := webhookOutboxRows(t, fx.pool, watermark, whID, EventIssueUpdated)
	if len(rows) != 1 {
		t.Fatalf("issue.updated rows = %d, want exactly 1 (extended row, not two)", len(rows))
	}
	c56t1AssertRowShape(t, fx, rows[0])
	c56t1AssertChangedFields(t, rows[0].Data.ChangedFields, "priority", "labels")
}

func TestBulkSetWebhookPatchPlusCascadeSingleRow(t *testing.T) {
	fx := setupLinkedIssue(t)
	whID := c56t1Webhook(t, fx)
	bulkSetLabelsChangedRule(t, fx, "label finisher",
		[]AutomationAction{{Type: "set_state", StateID: &fx.doneID}})
	labelID := createBulkSetLabel(t, fx, "bulk-wh-patch-cascade-label")

	// Patch change (priority) + label-set change + cascade move
	// (labels_changed -> set_state): exactly ONE row carrying the full
	// union. The two-row form is NOT acceptable here — the extend
	// mechanism must cover this case.
	watermark := outboxWatermark(t, fx.pool)
	labelIDs := []string{labelID}
	if _, _, err := BulkSetIssues(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		[]string{fx.iss.ID}, BulkIssueSet{Priority: autoIntPtr(1), LabelIDs: &labelIDs}); err != nil {
		t.Fatalf("BulkSetIssues priority+labels: %v", err)
	}
	rows := webhookOutboxRows(t, fx.pool, watermark, whID, EventIssueUpdated)
	if len(rows) != 1 {
		t.Fatalf("issue.updated rows = %d, want exactly 1 (extended row, not two)", len(rows))
	}
	c56t1AssertRowShape(t, fx, rows[0])
	c56t1AssertChangedFields(t, rows[0].Data.ChangedFields, "priority", "labels", "state_id")
}

func TestBulkSetWebhookSameStatePatchPlusLabels(t *testing.T) {
	fx := setupLinkedIssue(t)
	whID := c56t1Webhook(t, fx)
	labelID := createBulkSetLabel(t, fx, "bulk-wh-same-state-label")

	// The patch names the state the issue is already in, so
	// updateIssueTx enqueues nothing — the labels change must still
	// produce exactly one fresh row, not zero and not a patch row.
	watermark := outboxWatermark(t, fx.pool)
	labelIDs := []string{labelID}
	sameState := fx.backlogID
	if _, _, err := BulkSetIssues(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		[]string{fx.iss.ID}, BulkIssueSet{StateID: &sameState, LabelIDs: &labelIDs}); err != nil {
		t.Fatalf("BulkSetIssues same-state+labels: %v", err)
	}
	rows := webhookOutboxRows(t, fx.pool, watermark, whID, EventIssueUpdated)
	if len(rows) != 1 {
		t.Fatalf("issue.updated rows = %d, want exactly 1", len(rows))
	}
	c56t1AssertRowShape(t, fx, rows[0])
	c56t1AssertChangedFields(t, rows[0].Data.ChangedFields, "labels")
}

func TestBulkSetWebhookAssigneeClear(t *testing.T) {
	fx := setupLinkedIssue(t)
	whID := c56t1Webhook(t, fx)
	member := createTestUser(t, fx.pool, uniqueTestEmail("bulk-wh-clear"))
	addTestMember(t, fx.pool, fx.slug, fx.actor, member, RoleMember)
	if _, _, err := BulkSetIssues(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		[]string{fx.iss.ID}, BulkIssueSet{AssigneeID: PatchField[string]{Set: true, Value: &member}}); err != nil {
		t.Fatalf("BulkSetIssues assign setup: %v", err)
	}

	// Clearing the assignee set is a real set change: one row.
	watermark := outboxWatermark(t, fx.pool)
	if _, _, err := BulkSetIssues(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		[]string{fx.iss.ID}, BulkIssueSet{AssigneeID: PatchField[string]{Set: true, Value: nil}}); err != nil {
		t.Fatalf("BulkSetIssues clear assignee: %v", err)
	}
	rows := webhookOutboxRows(t, fx.pool, watermark, whID, EventIssueUpdated)
	if len(rows) != 1 {
		t.Fatalf("issue.updated rows = %d, want exactly 1", len(rows))
	}
	c56t1AssertRowShape(t, fx, rows[0])
	c56t1AssertChangedFields(t, rows[0].Data.ChangedFields, "assignees")
}

func TestBulkSetWebhookLabelsOnlyTwoIssues(t *testing.T) {
	fx := setupLinkedIssue(t)
	whID := c56t1Webhook(t, fx)
	second, err := CreateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		CreateIssueInput{Name: "second", StateID: &fx.backlogID})
	if err != nil {
		t.Fatalf("CreateIssue second: %v", err)
	}
	labelID := createBulkSetLabel(t, fx, "bulk-wh-two-label")

	watermark := outboxWatermark(t, fx.pool)
	labelIDs := []string{labelID}
	if _, _, err := BulkSetIssues(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		[]string{fx.iss.ID, second.ID}, BulkIssueSet{LabelIDs: &labelIDs}); err != nil {
		t.Fatalf("BulkSetIssues labels: %v", err)
	}
	rows := webhookOutboxRows(t, fx.pool, watermark, whID, EventIssueUpdated)
	if len(rows) != 2 {
		t.Fatalf("issue.updated rows = %d, want exactly 2 (one per changed issue)", len(rows))
	}
	seen := map[string]bool{}
	for _, row := range rows {
		seen[row.Data.ID] = true
		c56t1AssertChangedFields(t, row.Data.ChangedFields, "labels")
		if row.Data.ActorID != fx.actor {
			t.Fatalf("data.actor_id = %q, want %q", row.Data.ActorID, fx.actor)
		}
	}
	if !seen[fx.iss.ID] || !seen[second.ID] {
		t.Fatalf("row issue ids = %v, want both %s and %s", seen, fx.iss.ID, second.ID)
	}
}

// TestBulkSetWebhookExtendSkipsTaxonomyDecoy pins the C56T2 fix-loop
// guard: a concurrent AssignLabel on THIS issue commits an outbox row
// above BulkSet's watermark with changed_fields ["labels"], and the
// label/assignee writers take NO row lock, so the extend SELECT must
// not match it. Deterministic pin — no real concurrency: the decoy is
// inserted in-tx, then bulkSetWebhookTx is called directly with
// patchFields ["priority"] (no patch row exists in this tx, so the
// union must land in a fresh row and the decoy must stay untouched).
func TestBulkSetWebhookExtendSkipsTaxonomyDecoy(t *testing.T) {
	fx := setupLinkedIssue(t)
	whID := c56t1Webhook(t, fx)

	var wsID string
	if err := fx.pool.QueryRow(fx.ctx,
		`SELECT id::text FROM workspaces WHERE slug = $1`, fx.slug).Scan(&wsID); err != nil {
		t.Fatalf("workspace id: %v", err)
	}

	// Watermark chosen BEFORE the decoy, as BulkSetIssues takes it
	// before updateIssueTx runs.
	watermark := outboxWatermark(t, fx.pool)

	tx, err := fx.pool.Begin(fx.ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	// Decoy: exactly what a concurrent AssignLabel on THIS issue
	// commits — same issue, same domain event, id above the
	// watermark, changed_fields ["labels"]. Without the taxonomy
	// guard the extend would rewrite this row's changed_fields to
	// the BulkSet union, corrupting the AssignLabel webhook payload.
	if err := mail.Enqueue(fx.ctx, tx, WebhookDeliverEvent, webhookDelivery{
		WebhookID: whID,
		URL:       "http://example.invalid/decoy",
		Secret:    "decoy",
		Event:     EventIssueUpdated,
		Data: map[string]any{
			"id":             fx.iss.ID,
			"display_id":     fx.ident + "-" + strconv.Itoa(fx.iss.SequenceID),
			"changed_fields": []string{"labels"},
			"actor_id":       fx.actor,
		},
	}); err != nil {
		tx.Rollback(fx.ctx)
		t.Fatalf("enqueue decoy: %v", err)
	}

	// BulkSet item whose patch changed priority. before/final are
	// identical (no cascade row diff), so the union is exactly
	// ["priority"] and the patch row is the ONLY row the extend may
	// touch. No patch row exists in this tx — the decoy must be
	// skipped, so the union lands in a fresh row.
	before := *fx.iss
	final := *fx.iss
	if err := bulkSetWebhookTx(fx.ctx, tx, wsID, fx.iss.ID, fx.actor,
		watermark, []string{"priority"}, nil, &before, &final); err != nil {
		tx.Rollback(fx.ctx)
		t.Fatalf("bulkSetWebhookTx: %v", err)
	}
	if err := tx.Commit(fx.ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	rows := webhookOutboxRows(t, fx.pool, watermark, whID, EventIssueUpdated)
	if len(rows) != 2 {
		t.Fatalf("issue.updated rows = %d, want exactly 2 (decoy + fresh union row)", len(rows))
	}
	// Decoy first (enqueued first), untouched.
	c56t1AssertRowShape(t, fx, rows[0])
	c56t1AssertChangedFields(t, rows[0].Data.ChangedFields, "labels")
	// Fresh row carries the full union.
	c56t1AssertRowShape(t, fx, rows[1])
	c56t1AssertChangedFields(t, rows[1].Data.ChangedFields, "priority")
}
