package service

// C60T0: delivery-detail service. GetWebhookDelivery returns ONE
// webhook_deliveries row — the full payload view: list fields PLUS
// the stored domain-event `data` (which the list deliberately
// omits). Admin-only (role 20), workspace-scoped. A delivery under
// another webhook reads as ErrWebhookDeliveryNotFound (never leaks
// cross-webhook existence); a missing webhook / wrong workspace
// maps to ErrWebhookNotFound / ErrNotFound exactly like the list
// path. NO outbox_row_id: internal correlation only.
//
// All tests run against the real test database — no skips.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// TestGetWebhookDeliveryDetail: the stored delivery comes back with
// its full payload — every list field plus data.
func TestGetWebhookDeliveryDetail(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	actorID, _, wsSlug, _, _ := createNotifyFixture(t, pool)

	wh := createTestWebhook(t, pool, ctx, wsSlug, actorID, "https://example.com/hook")
	now := time.Now().UTC()
	http200 := 200
	id := insertWebhookDeliveryRow(t, pool, ctx, wh.ID, nil,
		EventIssueCreated, "delivered", intPtr(http200), nil, now,
		`{"id":"issue-1","title":"Bug","priority":3}`)

	got, err := GetWebhookDelivery(ctx, pool, wsSlug, actorID, wh.ID, id)
	if err != nil {
		t.Fatalf("GetWebhookDelivery = %v, want nil", err)
	}
	if got.ID != id {
		t.Fatalf("detail id = %d, want %d", got.ID, id)
	}
	if got.WebhookID != wh.ID {
		t.Fatalf("detail webhook_id = %q, want %q", got.WebhookID, wh.ID)
	}
	if got.Event != EventIssueCreated {
		t.Fatalf("detail event = %q, want %q", got.Event, EventIssueCreated)
	}
	if got.Status != "delivered" {
		t.Fatalf("detail status = %q, want delivered", got.Status)
	}
	if got.HTTPStatus == nil || *got.HTTPStatus != 200 {
		t.Fatalf("detail http_status = %v, want 200", got.HTTPStatus)
	}
	if got.Error != nil {
		t.Fatalf("detail error = %q, want NULL", *got.Error)
	}
	if got.DurationMs != 12 {
		t.Fatalf("detail duration_ms = %d, want 12 (seeded)", got.DurationMs)
	}
	if got.AttemptedAt.IsZero() {
		t.Fatal("detail attempted_at is zero, want the stored timestamp")
	}
	// The payload: the full domain-event data the dispatcher POSTed.
	if got.Data["id"] != "issue-1" || got.Data["title"] != "Bug" || got.Data["priority"] != float64(3) {
		t.Fatalf("detail data = %v, want the stored payload", got.Data)
	}
	raw, _ := json.Marshal(got)
	var shaped map[string]any
	if err := json.Unmarshal(raw, &shaped); err != nil {
		t.Fatalf("remarshal: %v", err)
	}
	if _, ok := shaped["outbox_row_id"]; ok {
		t.Fatalf("detail JSON carries outbox_row_id, must not: %v", shaped)
	}
}

// TestGetWebhookDeliveryNotFound: unknown delivery id, delivery
// under another webhook, and missing webhook all read as the same
// not-found sentinels — no cross-webhook existence leak.
func TestGetWebhookDeliveryNotFound(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	actorID, _, wsSlug, _, _ := createNotifyFixture(t, pool)

	wh := createTestWebhook(t, pool, ctx, wsSlug, actorID, "https://example.com/hook")
	if _, err := GetWebhookDelivery(ctx, pool, wsSlug, actorID, wh.ID, 999999999); !errors.Is(err, ErrWebhookDeliveryNotFound) {
		t.Fatalf("missing delivery = %v, want ErrWebhookDeliveryNotFound", err)
	}

	// Cross-webhook: the delivery exists under another webhook —
	// same 404, never leak cross-webhook existence.
	wh2 := createTestWebhook(t, pool, ctx, wsSlug, actorID, "https://example.com/hook2")
	otherID := insertWebhookDeliveryRow(t, pool, ctx, wh2.ID, nil,
		EventIssueCreated, "failed", nil, strPtr2("dead"), time.Now().UTC(), "")
	if _, err := GetWebhookDelivery(ctx, pool, wsSlug, actorID, wh.ID, otherID); !errors.Is(err, ErrWebhookDeliveryNotFound) {
		t.Fatalf("cross-webhook delivery = %v, want ErrWebhookDeliveryNotFound", err)
	}

	// Missing webhook: 404 via the webhook-existence check, not via
	// the delivery query (the delivery query can never leak another
	// workspace's history).
	if _, err := GetWebhookDelivery(ctx, pool, wsSlug, actorID,
		"00000000-0000-0000-0000-000000000000", otherID); !errors.Is(err, ErrWebhookNotFound) {
		t.Fatalf("missing webhook = %v, want ErrWebhookNotFound", err)
	}
}

// TestGetWebhookDeliveryAuthz: admin-only, workspace-scoped.
func TestGetWebhookDeliveryAuthz(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	actorID, memberID, wsSlug, _, _ := createNotifyFixture(t, pool)

	wh := createTestWebhook(t, pool, ctx, wsSlug, actorID, "https://example.com/hook")
	failedID := insertWebhookDeliveryRow(t, pool, ctx, wh.ID, nil,
		EventIssueCreated, "failed", nil, strPtr2("dead"), time.Now().UTC(), "")

	if _, err := GetWebhookDelivery(ctx, pool, wsSlug, memberID, wh.ID, failedID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member detail = %v, want ErrForbidden", err)
	}
	outsider := createTestUser(t, pool, uniqueTestEmail("outsider"))
	if _, err := GetWebhookDelivery(ctx, pool, wsSlug, outsider, wh.ID, failedID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider detail = %v, want ErrNotFound", err)
	}
	// Wrong workspace slug: the webhook does not belong to it —
	// same tenancy hygiene as the rest of the codebase.
	if _, err := GetWebhookDelivery(ctx, pool, "no-such-workspace", actorID, wh.ID, failedID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong workspace detail = %v, want ErrNotFound", err)
	}
}
