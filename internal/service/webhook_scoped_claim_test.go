package service

// C57T0: dispatcher test-isolation — a scoped run delivers ONLY the rows
// whose payload names the given webhook id, even when older due rows for
// other webhooks fill the claim batch. This is the regression pin for the
// leaked-row flakiness the SSRF dispatcher tests used to hit (an unscoped
// Run claims the oldest webhookBatchSize rows globally, so a test's own
// row could starve behind other tests' leftovers).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// outboxRowWebhookID reads the webhook_id out of an outbox row's payload —
// the scope key a scoped run filters on.
func outboxRowWebhookID(t *testing.T, pool *pgxpool.Pool, ctx context.Context, outboxID int64) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx,
		`SELECT payload->>'webhook_id' FROM outbox WHERE id = $1`, outboxID,
	).Scan(&id); err != nil {
		t.Fatalf("read outbox webhook_id: %v", err)
	}
	if id == "" {
		t.Fatalf("outbox row %d has empty payload webhook_id", outboxID)
	}
	return id
}

// insertWebhookOutboxRow seeds one due pending webhook.deliver row for the
// given webhook id directly, returning its outbox id.
func insertWebhookOutboxRow(t *testing.T, pool *pgxpool.Pool, ctx context.Context,
	webhookID, url string) int64 {
	t.Helper()
	payload, err := json.Marshal(webhookDelivery{
		WebhookID: webhookID,
		URL:       url,
		Secret:    "test-secret",
		Event:     EventIssueCreated,
		Data:      map[string]any{"id": "x"},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	var id int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO outbox (event, payload, status, attempts, next_retry_at)
		 VALUES ('webhook.deliver', $1, 'pending', 0, now())
		 RETURNING id`, payload).Scan(&id); err != nil {
		t.Fatalf("insert outbox row: %v", err)
	}
	return id
}

func TestWebhookDispatcherScopedRunDeliversOnlyOwnRows(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	actorID, _, wsSlug, _, _ := createNotifyFixture(t, pool)

	var decoyHits, ownHits atomic.Int64
	decoySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		decoyHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer decoySrv.Close()
	ownSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ownHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer ownSrv.Close()

	decoy := createTestWebhook(t, pool, ctx, wsSlug, actorID, decoySrv.URL)
	own := createTestWebhook(t, pool, ctx, wsSlug, actorID, ownSrv.URL)

	// Seed MORE older due rows for the decoy webhook than one unscoped
	// claim batch can hold: under an unscoped Run the test's own row
	// would starve behind them (and the decoy endpoint would be hit).
	// A scoped run must ignore all of them.
	decoyCount := webhookBatchSize + 5
	var decoyIDs []int64
	for i := 0; i < decoyCount; i++ {
		decoyIDs = append(decoyIDs, insertWebhookOutboxRow(t, pool, ctx, decoy.ID, decoySrv.URL))
	}
	ownID := insertWebhookOutboxRow(t, pool, ctx, own.ID, ownSrv.URL)

	d := NewWebhookDispatcher(pool)
	d.allowPrivateTargets = true // httptest targets are loopback by construction
	if err := d.runScoped(ctx, own.ID); err != nil {
		t.Fatalf("scoped run: %v", err)
	}

	if got := ownHits.Load(); got != 1 {
		t.Errorf("own endpoint hits = %d, want 1 (own row must be delivered)", got)
	}
	if got := decoyHits.Load(); got != 0 {
		t.Errorf("decoy endpoint hits = %d, want 0 (decoy rows must not be claimed)", got)
	}
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM outbox WHERE id = $1`, ownID).Scan(&status); err != nil {
		t.Fatalf("read own status: %v", err)
	}
	if status != "done" {
		t.Errorf("own row status = %q, want done", status)
	}
	for _, id := range decoyIDs {
		if err := pool.QueryRow(ctx,
			`SELECT status FROM outbox WHERE id = $1`, id).Scan(&status); err != nil {
			t.Fatalf("read decoy status: %v", err)
		}
		if status != "pending" {
			t.Errorf("decoy row %d status = %q, want pending (untouched by the scoped run)", id, status)
		}
	}
}
