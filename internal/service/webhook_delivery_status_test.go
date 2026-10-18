package service

// C57T2: last-delivery outcome on the webhook row. The dispatcher outcome
// writes update the webhook row's delivery status alongside the outbox
// outcome write: delivered / retrying / failed + last_error, with
// last_delivery_at stamped at the attempt. A never-attempted webhook
// reads all three fields as NULL.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// readWebhookDeliveryStatus reads the three delivery columns straight
// from the row — the source of truth, not the Go struct's JSON.
func readWebhookDeliveryStatus(t *testing.T, pool *pgxpool.Pool, ctx context.Context, webhookID string) (status, lastErr *string, at *time.Time) {
	t.Helper()
	if err := pool.QueryRow(ctx,
		`SELECT last_delivery_status, last_error, last_delivery_at
		   FROM webhooks WHERE id = $1::uuid`, webhookID,
	).Scan(&status, &lastErr, &at); err != nil {
		t.Fatalf("read webhook delivery status: %v", err)
	}
	return status, lastErr, at
}

func assertDeliveryStatus(t *testing.T, status *string, want string) {
	t.Helper()
	if status == nil || *status != want {
		t.Fatalf("last_delivery_status = %v, want %q", status, want)
	}
}

func assertDeliveryAtRecent(t *testing.T, at *time.Time) {
	t.Helper()
	if at == nil {
		t.Fatal("last_delivery_at is NULL, want ~now")
	}
	if time.Since(*at) > 5*time.Minute || at.After(time.Now().Add(time.Minute)) {
		t.Fatalf("last_delivery_at = %v, want within the last few minutes", *at)
	}
}

// TestWebhookDeliveryStatusSuccess: a 200 delivery marks the outbox row
// done AND the webhook row delivered with last_error NULL and
// last_delivery_at ~now.
func TestWebhookDeliveryStatusSuccess(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	actorID, _, wsSlug, _, _ := createNotifyFixture(t, pool)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	wh := createTestWebhook(t, pool, ctx, wsSlug, actorID, srv.URL)
	outboxID := enqueueWebhookRow(t, pool, ctx, wsSlug)

	d := NewWebhookDispatcher(pool)
	d.allowPrivateTargets = true // httptest targets are loopback by construction
	if err := d.runScoped(ctx, wh.ID); err != nil {
		t.Fatalf("dispatcher run: %v", err)
	}

	var outboxStatus string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM outbox WHERE id = $1`, outboxID).Scan(&outboxStatus); err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	if outboxStatus != "done" {
		t.Fatalf("outbox status = %q, want done", outboxStatus)
	}
	status, lastErr, at := readWebhookDeliveryStatus(t, pool, ctx, wh.ID)
	assertDeliveryStatus(t, status, "delivered")
	if lastErr != nil {
		t.Fatalf("last_error = %q, want NULL after a successful delivery", *lastErr)
	}
	assertDeliveryAtRecent(t, at)
}

// TestWebhookDeliveryStatusRetrying: a 500 delivery keeps the outbox row
// pending AND marks the webhook row retrying with last_error set.
func TestWebhookDeliveryStatusRetrying(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	actorID, _, wsSlug, _, _ := createNotifyFixture(t, pool)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	wh := createTestWebhook(t, pool, ctx, wsSlug, actorID, srv.URL)
	outboxID := enqueueWebhookRow(t, pool, ctx, wsSlug)

	d := NewWebhookDispatcher(pool)
	d.allowPrivateTargets = true
	if err := d.runScoped(ctx, wh.ID); err != nil {
		t.Fatalf("dispatcher run: %v", err)
	}

	var outboxStatus string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM outbox WHERE id = $1`, outboxID).Scan(&outboxStatus); err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	if outboxStatus != "pending" {
		t.Fatalf("outbox status = %q, want pending", outboxStatus)
	}
	status, lastErr, at := readWebhookDeliveryStatus(t, pool, ctx, wh.ID)
	assertDeliveryStatus(t, status, "retrying")
	if lastErr == nil || !strings.Contains(*lastErr, "status 500") {
		t.Fatalf("last_error = %v, want it to mention status 500", lastErr)
	}
	assertDeliveryAtRecent(t, at)
}

// TestWebhookDeliveryStatusFailedSSRF: an SSRF-blocked delivery fails the
// outbox row AND marks the webhook row failed with last_error mentioning
// the block.
func TestWebhookDeliveryStatusFailedSSRF(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	actorID, _, wsSlug, _, _ := createNotifyFixture(t, pool)

	// httptest binds loopback: the guard blocks the literal loopback IP
	// before any dial.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	wh := createTestWebhook(t, pool, ctx, wsSlug, actorID, srv.URL)
	outboxID := enqueueWebhookRow(t, pool, ctx, wsSlug)

	d := NewWebhookDispatcher(pool)
	if err := d.runScoped(ctx, wh.ID); err != nil {
		t.Fatalf("dispatcher run: %v", err)
	}

	var outboxStatus string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM outbox WHERE id = $1`, outboxID).Scan(&outboxStatus); err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	if outboxStatus != "failed" {
		t.Fatalf("outbox status = %q, want failed", outboxStatus)
	}
	status, lastErr, at := readWebhookDeliveryStatus(t, pool, ctx, wh.ID)
	assertDeliveryStatus(t, status, "failed")
	if lastErr == nil || !strings.Contains(*lastErr, "non-public IP") {
		t.Fatalf("last_error = %v, want it to mention the SSRF block", lastErr)
	}
	assertDeliveryAtRecent(t, at)
}

// TestWebhookDeliveryStatusExhaustedRetries: the last allowed attempt
// (recordFailure → markFailed) marks the webhook row failed, not
// retrying — the markFailed forwarding path.
func TestWebhookDeliveryStatusExhaustedRetries(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	actorID, _, wsSlug, _, _ := createNotifyFixture(t, pool)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	wh := createTestWebhook(t, pool, ctx, wsSlug, actorID, srv.URL)
	outboxID := enqueueWebhookRow(t, pool, ctx, wsSlug)
	// One attempt short of the cap: this delivery is the last.
	if _, err := pool.Exec(ctx,
		`UPDATE outbox SET attempts = $2 WHERE id = $1`, outboxID, webhookMaxAttempts-1); err != nil {
		t.Fatalf("set attempts: %v", err)
	}

	d := NewWebhookDispatcher(pool)
	d.allowPrivateTargets = true
	if err := d.runScoped(ctx, wh.ID); err != nil {
		t.Fatalf("dispatcher run: %v", err)
	}

	var outboxStatus string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM outbox WHERE id = $1`, outboxID).Scan(&outboxStatus); err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	if outboxStatus != "failed" {
		t.Fatalf("outbox status = %q, want failed (attempts exhausted)", outboxStatus)
	}
	status, lastErr, at := readWebhookDeliveryStatus(t, pool, ctx, wh.ID)
	assertDeliveryStatus(t, status, "failed")
	if lastErr == nil || !strings.Contains(*lastErr, "status 500") {
		t.Fatalf("last_error = %v, want it to mention status 500", lastErr)
	}
	assertDeliveryAtRecent(t, at)
}

// TestWebhookDeliveryStatusDeletedWebhook: a delivery for a webhook
// deleted after enqueue still marks the outbox row done with no error —
// the missing webhook row skips the status update silently.
func TestWebhookDeliveryStatusDeletedWebhook(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	actorID, _, wsSlug, _, _ := createNotifyFixture(t, pool)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	wh := createTestWebhook(t, pool, ctx, wsSlug, actorID, srv.URL)
	outboxID := enqueueWebhookRow(t, pool, ctx, wsSlug)
	if err := DeleteWebhook(ctx, pool, wsSlug, actorID, wh.ID); err != nil {
		t.Fatalf("DeleteWebhook: %v", err)
	}

	d := NewWebhookDispatcher(pool)
	d.allowPrivateTargets = true
	if err := d.runScoped(ctx, wh.ID); err != nil {
		t.Fatalf("dispatcher run for deleted webhook: %v (want no error)", err)
	}

	var outboxStatus string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM outbox WHERE id = $1`, outboxID).Scan(&outboxStatus); err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	if outboxStatus != "done" {
		t.Fatalf("outbox status = %q, want done (deleted webhook skips delivery)", outboxStatus)
	}
}

// TestWebhookDeliveryStatusDeactivatedSkipsStatus: a webhook deactivated
// after enqueue is skipped (outbox done) WITHOUT writing a delivery
// outcome — no delivery was attempted, so the row must stay
// never-attempted, not read 'delivered'.
func TestWebhookDeliveryStatusDeactivatedSkipsStatus(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	actorID, _, wsSlug, _, _ := createNotifyFixture(t, pool)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	wh := createTestWebhook(t, pool, ctx, wsSlug, actorID, srv.URL)
	outboxID := enqueueWebhookRow(t, pool, ctx, wsSlug)
	inactive := false
	if _, err := UpdateWebhook(ctx, pool, wsSlug, actorID, wh.ID,
		WebhookPatch{Active: &inactive}); err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	d := NewWebhookDispatcher(pool)
	d.allowPrivateTargets = true
	if err := d.runScoped(ctx, wh.ID); err != nil {
		t.Fatalf("dispatcher run: %v", err)
	}

	var outboxStatus string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM outbox WHERE id = $1`, outboxID).Scan(&outboxStatus); err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	if outboxStatus != "done" {
		t.Fatalf("outbox status = %q, want done (deactivated webhook skips delivery)", outboxStatus)
	}
	status, lastErr, at := readWebhookDeliveryStatus(t, pool, ctx, wh.ID)
	if status != nil || lastErr != nil || at != nil {
		t.Fatalf("delivery fields = (%v, %v, %v), want all NULL (no delivery attempted)", status, lastErr, at)
	}
}

// TestWebhookDeliveryStatusNeverAttempted: a new webhook reads all three
// delivery fields as NULL in Go and as present-but-null keys in the
// list/get/create wire shapes (same null convention as AdminUser.name:
// pointer fields, no omitempty).
func TestWebhookDeliveryStatusNeverAttempted(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	actorID, _, wsSlug, _, _ := createNotifyFixture(t, pool)

	wh, err := CreateWebhook(ctx, pool, wsSlug, actorID, WebhookInput{
		URL: "https://example.com/hook", Events: []string{"issue.created"},
	})
	if err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}
	if wh.LastDeliveryAt != nil || wh.LastDeliveryStatus != nil || wh.LastError != nil {
		t.Fatalf("create webhook delivery fields = (%v, %v, %v), want all nil",
			wh.LastDeliveryAt, wh.LastDeliveryStatus, wh.LastError)
	}

	got, err := GetWebhook(ctx, pool, wsSlug, actorID, wh.ID)
	if err != nil {
		t.Fatalf("GetWebhook: %v", err)
	}
	if got.LastDeliveryAt != nil || got.LastDeliveryStatus != nil || got.LastError != nil {
		t.Fatalf("get webhook delivery fields = (%v, %v, %v), want all nil",
			got.LastDeliveryAt, got.LastDeliveryStatus, got.LastError)
	}

	list, err := ListWebhooks(ctx, pool, wsSlug, actorID)
	if err != nil {
		t.Fatalf("ListWebhooks: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list = %d, want 1", len(list))
	}
	if list[0].LastDeliveryAt != nil || list[0].LastDeliveryStatus != nil || list[0].LastError != nil {
		t.Fatalf("list webhook delivery fields = (%v, %v, %v), want all nil",
			list[0].LastDeliveryAt, list[0].LastDeliveryStatus, list[0].LastError)
	}

	// Wire shape: the three keys are present with null values, in all
	// three response shapes (create-201 carries the secret too).
	assertNullDeliveryKeys := func(name string, v any) {
		t.Helper()
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal %s: %v", name, err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		for _, key := range []string{"last_delivery_at", "last_delivery_status", "last_error"} {
			val, ok := m[key]
			if !ok {
				t.Fatalf("%s wire shape lacks key %q: %s", name, key, raw)
			}
			if val != nil {
				t.Fatalf("%s wire shape %q = %v, want null", name, key, val)
			}
		}
	}
	assertNullDeliveryKeys("create", wh.CreateResponse())
	assertNullDeliveryKeys("get", got)
	assertNullDeliveryKeys("list[0]", list[0])
}
