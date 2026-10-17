package service

// Scheduled firings + PATCH-path changed_fields completeness (C56T3):
// a scheduled-rule firing that changes the issue row must enqueue
// exactly ONE issue.updated webhook in-tx, with changed_fields = the
// before/after row diff (a set_priority-only firing still enqueues —
// the WS announce stays state-only, the webhook gate is row-change).
// A no-op firing enqueues nothing. On the PATCH path, updateIssueTx's
// single webhook row must carry the union of the user-patch activities
// fields and the automation-cascade row diff (old before-image vs
// fresh post-automation row), so a priority_changed -> set_state
// cascade surfaces BOTH priority and state_id.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// c56t3Webhook creates an issue.updated webhook for an automation
// fixture (c56t1Webhook takes a *linkedIssueFixture) with the same
// good-citizen cleanup: delete this webhook's outbox rows at test end
// so leftovers cannot starve TestDeliverRowBlocksPrivateTarget under
// the full suite (dispatcher claims the oldest 25 pending rows).
func c56t3Webhook(t *testing.T, pool *pgxpool.Pool, slug, actor string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	wh, err := CreateWebhook(context.Background(), pool, slug, actor, WebhookInput{
		URL: srv.URL, Secret: strPtr2("s3cret"),
		Events: []string{EventIssueUpdated},
	})
	if err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM outbox WHERE payload->>'webhook_id' = $1`, wh.ID); err != nil {
			t.Errorf("cleanup outbox rows: %v", err)
		}
	})
	return wh.ID
}

func c56t3AssertRowShape(t *testing.T, fx automationFixture, iss *Issue, row webhookOutboxPayload) {
	t.Helper()
	if row.Data.ID != iss.ID {
		t.Fatalf("data.id = %q, want %q", row.Data.ID, iss.ID)
	}
	// fx.ident is the pre-normalization lowercase form; project
	// identifiers are stored uppercased (normalizeIdentifier) and
	// the scheduled pass reads the stored form.
	wantDisplay := strings.ToUpper(fx.ident) + "-" + strconv.Itoa(iss.SequenceID)
	if row.Data.DisplayID != wantDisplay {
		t.Fatalf("data.display_id = %q, want %q", row.Data.DisplayID, wantDisplay)
	}
	// Scheduled firings have no triggering user: the actor is the
	// rule author.
	if row.Data.ActorID != fx.admin {
		t.Fatalf("data.actor_id = %q, want rule author %q", row.Data.ActorID, fx.admin)
	}
}

func TestScheduledFiringSetStateEnqueuesWebhook(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()
	whID := c56t3Webhook(t, fx.pool, fx.slug, fx.admin)

	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, AutomationRuleInput{
		Name:    "overdue escalator",
		Trigger: AutomationTrigger{Type: "issue.overdue"},
		Actions: []AutomationAction{{Type: "set_state", StateID: autoStrPtr(fx.started)}},
	}); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	iss := createTargetIssue(t, fx, "overdue", timePtr(-24*time.Hour))

	watermark := outboxWatermark(t, fx.pool)
	if err := RunScheduledAutomationPass(ctx, fx.pool, time.Now()); err != nil {
		t.Fatalf("RunScheduledAutomationPass: %v", err)
	}
	rows := webhookOutboxRows(t, fx.pool, watermark, whID, EventIssueUpdated)
	if len(rows) != 1 {
		t.Fatalf("issue.updated rows = %d, want exactly 1", len(rows))
	}
	c56t3AssertRowShape(t, fx, iss, rows[0])
	c56t1AssertChangedFields(t, rows[0].Data.ChangedFields, "state_id")
}

func TestScheduledFiringSetPriorityEnqueuesWebhook(t *testing.T) {
	fx := setupAutomationFixture(t)
	ctx := context.Background()
	whID := c56t3Webhook(t, fx.pool, fx.slug, fx.admin)

	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, AutomationRuleInput{
		Name:    "overdue urgency",
		Trigger: AutomationTrigger{Type: "issue.overdue"},
		Actions: []AutomationAction{{Type: "set_priority", Priority: autoIntPtr(3)}},
	}); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}
	iss := createTargetIssue(t, fx, "overdue", timePtr(-24*time.Hour))

	watermark := outboxWatermark(t, fx.pool)
	if err := RunScheduledAutomationPass(ctx, fx.pool, time.Now()); err != nil {
		t.Fatalf("RunScheduledAutomationPass: %v", err)
	}
	rows := webhookOutboxRows(t, fx.pool, watermark, whID, EventIssueUpdated)
	if len(rows) != 1 {
		t.Fatalf("issue.updated rows = %d, want exactly 1", len(rows))
	}
	c56t3AssertRowShape(t, fx, iss, rows[0])
	c56t1AssertChangedFields(t, rows[0].Data.ChangedFields, "priority")
}

func TestScheduledFiringNoOpEnqueuesNothing(t *testing.T) {
	t.Run("set_state to current state", func(t *testing.T) {
		fx := setupAutomationFixture(t)
		ctx := context.Background()
		whID := c56t3Webhook(t, fx.pool, fx.slug, fx.admin)

		if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, AutomationRuleInput{
			Name:    "overdue no-op state",
			Trigger: AutomationTrigger{Type: "issue.overdue"},
			Actions: []AutomationAction{{Type: "set_state", StateID: autoStrPtr(fx.backlog)}},
		}); err != nil {
			t.Fatalf("CreateAutomationRule: %v", err)
		}
		createTargetIssue(t, fx, "overdue", timePtr(-24*time.Hour))

		watermark := outboxWatermark(t, fx.pool)
		if err := RunScheduledAutomationPass(ctx, fx.pool, time.Now()); err != nil {
			t.Fatalf("RunScheduledAutomationPass: %v", err)
		}
		if rows := webhookOutboxRows(t, fx.pool, watermark, whID, EventIssueUpdated); len(rows) != 0 {
			t.Fatalf("issue.updated rows after no-op firing = %d, want 0", len(rows))
		}
	})

	t.Run("set_priority to current priority", func(t *testing.T) {
		fx := setupAutomationFixture(t)
		ctx := context.Background()
		whID := c56t3Webhook(t, fx.pool, fx.slug, fx.admin)

		if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.admin, AutomationRuleInput{
			Name:    "overdue no-op priority",
			Trigger: AutomationTrigger{Type: "issue.overdue"},
			Actions: []AutomationAction{{Type: "set_priority", Priority: autoIntPtr(0)}},
		}); err != nil {
			t.Fatalf("CreateAutomationRule: %v", err)
		}
		createTargetIssue(t, fx, "overdue", timePtr(-24*time.Hour))

		watermark := outboxWatermark(t, fx.pool)
		if err := RunScheduledAutomationPass(ctx, fx.pool, time.Now()); err != nil {
			t.Fatalf("RunScheduledAutomationPass: %v", err)
		}
		if rows := webhookOutboxRows(t, fx.pool, watermark, whID, EventIssueUpdated); len(rows) != 0 {
			t.Fatalf("issue.updated rows after no-op firing = %d, want 0", len(rows))
		}
	})
}

func TestPatchPriorityCascadeWebhookChangedFields(t *testing.T) {
	fx := setupLinkedIssue(t)
	ctx := context.Background()
	whID := c56t1Webhook(t, fx)
	if _, err := CreateAutomationRule(ctx, fx.pool, fx.slug, fx.ident, fx.actor, AutomationRuleInput{
		Name:    "priority finisher",
		Trigger: AutomationTrigger{Type: "issue.priority_changed", ToPriorities: []int{3}},
		Actions: []AutomationAction{{Type: "set_state", StateID: &fx.doneID}},
	}); err != nil {
		t.Fatalf("CreateAutomationRule: %v", err)
	}

	watermark := outboxWatermark(t, fx.pool)
	if _, err := UpdateIssue(ctx, fx.pool, fx.slug, fx.ident, fx.iss.ID, fx.actor,
		IssuePatch{Priority: autoIntPtr(3)}); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	rows := webhookOutboxRows(t, fx.pool, watermark, whID, EventIssueUpdated)
	if len(rows) != 1 {
		t.Fatalf("issue.updated rows = %d, want exactly 1 (one row, not one per change)", len(rows))
	}
	c56t1AssertRowShape(t, fx, rows[0])
	// Activities-first order: the user patch (priority) then the
	// cascade diff (state_id).
	if got := rows[0].Data.ChangedFields; len(got) != 2 || got[0] != "priority" || got[1] != "state_id" {
		t.Fatalf("changed_fields = %v, want [priority state_id]", got)
	}
}
