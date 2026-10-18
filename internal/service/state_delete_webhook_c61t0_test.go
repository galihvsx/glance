package service

// DeleteState webhook fan-out (C61T0): deleting a state with reassign_to
// enqueues exactly one in-tx issue.updated webhook per moved LIVE issue
// (changed_fields = ["state_id"], mirroring the restoreIssueTx payload
// shape at issue.go:1247). Soft-deleted issues moved by the same UPDATE
// get NO row. A delete with no referencing issues enqueues nothing
// issue-related.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// c61t0Webhook creates a webhook subscribed to EventIssueUpdated and
// returns its id. Outbox rows for the webhook are cleaned up at test end
// (same good-citizen pattern as c56t1Webhook: the dispatcher claims the
// oldest rows first, so leftover rows would starve later tests).
func c61t0Webhook(t *testing.T, pool *pgxpool.Pool, wsSlug, actorID string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	wh, err := CreateWebhook(context.Background(), pool, wsSlug, actorID, WebhookInput{
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

// c61t0ParkInDoomed moves issues into the doomed state via direct SQL (so
// no webhooks fire from the setup itself) and soft-deletes the ghost.
func c61t0ParkInDoomed(t *testing.T, pool *pgxpool.Pool, doomedID string, liveIDs []string, ghostID string) {
	t.Helper()
	ctx := context.Background()
	all := append([]string{}, liveIDs...)
	all = append(all, ghostID)
	if _, err := pool.Exec(ctx,
		`UPDATE issues SET state_id = $1::uuid WHERE id = ANY($2::uuid[])`,
		doomedID, all); err != nil {
		t.Fatalf("park issues: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE issues SET deleted_at = now() WHERE id = $1::uuid`, ghostID); err != nil {
		t.Fatalf("soft-delete ghost: %v", err)
	}
}

// TestDeleteStateReassignEnqueuesIssueUpdatedWebhooks: deleting a state
// with reassign_to enqueues exactly one issue.updated outbox row per
// moved LIVE issue — changed_fields carries exactly ["state_id"], and
// id/display_id/actor_id are present. The soft-deleted issue moved by
// the same UPDATE enqueues NOTHING.
func TestDeleteStateReassignEnqueuesIssueUpdatedWebhooks(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	slug, ident, creator := setupStateProject(t, pool)

	doomed, err := CreateState(ctx, pool, slug, ident, creator,
		StateInput{Name: "Doomed", Group: "backlog"})
	if err != nil {
		t.Fatalf("CreateState doomed: %v", err)
	}
	target, err := CreateState(ctx, pool, slug, ident, creator,
		StateInput{Name: "Safe", Group: "completed"})
	if err != nil {
		t.Fatalf("CreateState target: %v", err)
	}
	live1 := createTestIssue(t, pool, slug, ident, creator, "live one")
	live2 := createTestIssue(t, pool, slug, ident, creator, "live two")
	ghost := createTestIssue(t, pool, slug, ident, creator, "ghost")
	c61t0ParkInDoomed(t, pool, doomed.ID, []string{live1.ID, live2.ID}, ghost.ID)

	whID := c61t0Webhook(t, pool, slug, creator)
	watermark := outboxWatermark(t, pool)
	if err := DeleteState(ctx, pool, slug, ident, doomed.ID, creator, &target.ID); err != nil {
		t.Fatalf("DeleteState with reassign: %v", err)
	}

	rows := webhookOutboxRows(t, pool, watermark, whID, EventIssueUpdated)
	if len(rows) != 2 {
		t.Fatalf("issue.updated rows = %d, want exactly 2 (one per moved live issue)", len(rows))
	}
	gotIDs := []string{rows[0].Data.ID, rows[1].Data.ID}
	sort.Strings(gotIDs)
	wantIDs := []string{live1.ID, live2.ID}
	sort.Strings(wantIDs)
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("row ids = %v, want %v (soft-deleted issue must not enqueue)", gotIDs, wantIDs)
	}
	wantDisplay := map[string]string{
		live1.ID: ident + "-" + strconv.Itoa(live1.SequenceID),
		live2.ID: ident + "-" + strconv.Itoa(live2.SequenceID),
	}
	for _, row := range rows {
		if !reflect.DeepEqual(row.Data.ChangedFields, []string{"state_id"}) {
			t.Fatalf("changed_fields = %v, want exactly [state_id]", row.Data.ChangedFields)
		}
		if row.Data.DisplayID != wantDisplay[row.Data.ID] {
			t.Fatalf("display_id = %q, want %q", row.Data.DisplayID, wantDisplay[row.Data.ID])
		}
		if row.Data.ActorID != creator {
			t.Fatalf("actor_id = %q, want %q", row.Data.ActorID, creator)
		}
	}
}

// TestDeleteStateZeroRefsEnqueuesNoIssueWebhooks: deleting a state no
// issue references enqueues nothing issue-related (the zero-refs path
// touches no issue and must stay webhook-silent).
func TestDeleteStateZeroRefsEnqueuesNoIssueWebhooks(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	slug, ident, creator := setupStateProject(t, pool)

	bystander := createTestIssue(t, pool, slug, ident, creator, "bystander")
	empty, err := CreateState(ctx, pool, slug, ident, creator,
		StateInput{Name: "Empty", Group: "backlog"})
	if err != nil {
		t.Fatalf("CreateState: %v", err)
	}

	whID := c61t0Webhook(t, pool, slug, creator)
	watermark := outboxWatermark(t, pool)
	if err := DeleteState(ctx, pool, slug, ident, empty.ID, creator, nil); err != nil {
		t.Fatalf("DeleteState zero refs: %v", err)
	}

	if rows := webhookOutboxRows(t, pool, watermark, whID, EventIssueUpdated); len(rows) != 0 {
		t.Fatalf("issue.updated rows after zero-ref delete = %d, want 0", len(rows))
	}
	_ = bystander
}
