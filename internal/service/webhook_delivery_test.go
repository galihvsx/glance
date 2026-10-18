package service

// C59T0: webhook delivery history (webhook_deliveries) + redelivery.
//
// The dispatcher writes one history row per attempt alongside each
// outbox outcome (delivered / retrying / failed), with the receiver's
// HTTP status, the failure message, and the attempt wall time. The
// service exposes ListWebhookDeliveries (admin-only, newest-first,
// cursor paging) and RedeliverWebhookDelivery (failed rows only —
// a fresh outbox row from the CURRENT webhook row, original history
// row untouched). Retention prunes rows older than 90 days.
//
// All tests run against the real test database — no skips.

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// webhookDeliveryRow mirrors one webhook_deliveries row as read back
// from the DB — the source of truth, not the Go list struct.
type webhookDeliveryRow struct {
	id         int64
	webhookID  string
	outboxID   *int64
	event      string
	data       map[string]any
	status     string
	httpStatus *int
	errStr     *string
	durationMs int
	attempted  time.Time
}

func readWebhookDeliveries(t *testing.T, pool *pgxpool.Pool, ctx context.Context, webhookID string) []webhookDeliveryRow {
	t.Helper()
	rows, err := pool.Query(ctx,
		`SELECT id, webhook_id, outbox_row_id, event, data, status,
		        http_status, error, duration_ms, attempted_at
		   FROM webhook_deliveries WHERE webhook_id = $1::uuid
		  ORDER BY attempted_at DESC, id DESC`, webhookID)
	if err != nil {
		t.Fatalf("read webhook_deliveries: %v", err)
	}
	defer rows.Close()
	var out []webhookDeliveryRow
	for rows.Next() {
		var r webhookDeliveryRow
		var dataBytes []byte
		if err := rows.Scan(&r.id, &r.webhookID, &r.outboxID, &r.event,
			&dataBytes, &r.status, &r.httpStatus, &r.errStr,
			&r.durationMs, &r.attempted); err != nil {
			t.Fatalf("scan webhook_deliveries: %v", err)
		}
		if err := json.Unmarshal(dataBytes, &r.data); err != nil {
			t.Fatalf("decode delivery data: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate webhook_deliveries: %v", err)
	}
	return out
}

func countWebhookDeliveries(t *testing.T, pool *pgxpool.Pool, ctx context.Context, webhookID string) int {
	t.Helper()
	return len(readWebhookDeliveries(t, pool, ctx, webhookID))
}

// insertWebhookDeliveryRow seeds one history row directly, returning
// its id. attemptedAt is explicit so list-ordering and prune tests
// can backdate rows.
func insertWebhookDeliveryRow(t *testing.T, pool *pgxpool.Pool, ctx context.Context,
	webhookID string, outboxID *int64, event, status string,
	httpStatus *int, errStr *string, attemptedAt time.Time, data string) int64 {
	t.Helper()
	if data == "" {
		data = `{"id":"x"}`
	}
	var id int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO webhook_deliveries
		   (webhook_id, outbox_row_id, event, data, status, http_status, error, duration_ms, attempted_at)
		 VALUES ($1::uuid, $2, $3, $4::jsonb, $5, $6, $7, 12, $8) RETURNING id`,
		webhookID, outboxID, event, data, status, httpStatus, errStr, attemptedAt,
	).Scan(&id); err != nil {
		t.Fatalf("insert webhook_deliveries: %v", err)
	}
	return id
}

func assertHistoryRow(t *testing.T, rows []webhookDeliveryRow, wantStatus string, wantHTTP *int) webhookDeliveryRow {
	t.Helper()
	if len(rows) != 1 {
		t.Fatalf("history rows = %d, want exactly 1", len(rows))
	}
	r := rows[0]
	if r.status != wantStatus {
		t.Fatalf("history status = %q, want %q", r.status, wantStatus)
	}
	switch {
	case wantHTTP == nil && r.httpStatus != nil:
		t.Fatalf("history http_status = %d, want NULL", *r.httpStatus)
	case wantHTTP != nil && (r.httpStatus == nil || *r.httpStatus != *wantHTTP):
		t.Fatalf("history http_status = %v, want %d", r.httpStatus, *wantHTTP)
	}
	if r.outboxID == nil {
		t.Fatal("history outbox_row_id is NULL, want the originating outbox row id")
	}
	if r.event != EventIssueCreated {
		t.Fatalf("history event = %q, want %q", r.event, EventIssueCreated)
	}
	if time.Since(r.attempted) > 5*time.Minute || r.attempted.After(time.Now().Add(time.Minute)) {
		t.Fatalf("history attempted_at = %v, want ~now", r.attempted)
	}
	if r.durationMs < 0 {
		t.Fatalf("history duration_ms = %d, want >= 0", r.durationMs)
	}
	return r
}

// TestWebhookDeliveryHistoryDelivered: a 200 delivery writes one
// history row: delivered, http_status 200, error NULL, correlated to
// the outbox row.
func TestWebhookDeliveryHistoryDelivered(t *testing.T) {
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
	d.allowPrivateTargets = true
	if err := d.runScoped(ctx, wh.ID); err != nil {
		t.Fatalf("dispatcher run: %v", err)
	}

	r := assertHistoryRow(t, readWebhookDeliveries(t, pool, ctx, wh.ID), "delivered", intPtr(200))
	if r.errStr != nil {
		t.Fatalf("history error = %q, want NULL on delivered", *r.errStr)
	}
	if *r.outboxID != outboxID {
		t.Fatalf("history outbox_row_id = %d, want %d", *r.outboxID, outboxID)
	}
}

// TestWebhookDeliveryHistoryRetrying: a 500 delivery writes one
// history row: retrying, http_status 500, error mentioning the 500.
func TestWebhookDeliveryHistoryRetrying(t *testing.T) {
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

	r := assertHistoryRow(t, readWebhookDeliveries(t, pool, ctx, wh.ID), "retrying", intPtr(500))
	if r.errStr == nil || !strings.Contains(*r.errStr, "status 500") {
		t.Fatalf("history error = %v, want it to mention status 500", r.errStr)
	}
	if *r.outboxID != outboxID {
		t.Fatalf("history outbox_row_id = %d, want %d", *r.outboxID, outboxID)
	}
}

// TestWebhookDeliveryHistoryFailedExhausted: the last allowed attempt
// writes a failed history row (the recordFailure → markFailed path),
// not a retrying one.
func TestWebhookDeliveryHistoryFailedExhausted(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	actorID, _, wsSlug, _, _ := createNotifyFixture(t, pool)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	wh := createTestWebhook(t, pool, ctx, wsSlug, actorID, srv.URL)
	outboxID := enqueueWebhookRow(t, pool, ctx, wsSlug)
	if _, err := pool.Exec(ctx,
		`UPDATE outbox SET attempts = $2 WHERE id = $1`, outboxID, webhookMaxAttempts-1); err != nil {
		t.Fatalf("set attempts: %v", err)
	}

	d := NewWebhookDispatcher(pool)
	d.allowPrivateTargets = true
	if err := d.runScoped(ctx, wh.ID); err != nil {
		t.Fatalf("dispatcher run: %v", err)
	}

	r := assertHistoryRow(t, readWebhookDeliveries(t, pool, ctx, wh.ID), "failed", intPtr(500))
	if r.errStr == nil || !strings.Contains(*r.errStr, "status 500") {
		t.Fatalf("history error = %v, want it to mention status 500", r.errStr)
	}
}

// TestWebhookDeliveryHistoryNoRowOnSkip: a webhook deactivated after
// enqueue is skipped with NO history row — no delivery was attempted.
func TestWebhookDeliveryHistoryNoRowOnSkip(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	actorID, _, wsSlug, _, _ := createNotifyFixture(t, pool)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	wh := createTestWebhook(t, pool, ctx, wsSlug, actorID, srv.URL)
	enqueueWebhookRow(t, pool, ctx, wsSlug)
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
	if n := countWebhookDeliveries(t, pool, ctx, wh.ID); n != 0 {
		t.Fatalf("history rows = %d, want 0 (skip writes no history)", n)
	}
}

// TestWebhookDeliveryHistoryNoRowOnPoison: a pre-lookup failure with a
// malformed webhook id writes NO history and returns NO error — the
// malformed-id guard (uuid.Parse → silent no-op) must cover the
// history write exactly like it covers the status update.
func TestWebhookDeliveryHistoryNoRowOnPoison(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	createNotifyFixture(t, pool)

	// Missing-url path with a garbage (non-UUID) webhook id.
	outboxID := insertWebhookOutboxRow(t, pool, ctx, "not-a-uuid", "")
	d := NewWebhookDispatcher(pool)
	if err := d.runScoped(ctx, "not-a-uuid"); err != nil {
		t.Fatalf("dispatcher run: %v (want no error for a malformed webhook_id)", err)
	}
	var outboxStatus string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM outbox WHERE id = $1`, outboxID).Scan(&outboxStatus); err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	if outboxStatus != "failed" {
		t.Fatalf("outbox status = %q, want failed", outboxStatus)
	}
	// No history row can exist: the id is not a uuid, so no row could
	// be attributed to it.
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM webhook_deliveries WHERE webhook_id::text = 'not-a-uuid'`).Scan(&n); err != nil {
		// The cast fails on non-uuid text — a count of zero rows is
		// the same outcome; treat a query error as failure only if
		// the table itself is missing.
		t.Fatalf("count poison history: %v", err)
	}
	if n != 0 {
		t.Fatalf("history rows for malformed id = %d, want 0", n)
	}
}

// TestWebhookDeliveryHistoryUndecodablePayload: a payload that decodes
// as JSON but not as a webhookDelivery (type mismatch) is a poison row:
// outbox failed, NO history (no webhook id is recoverable).
func TestWebhookDeliveryHistoryUndecodablePayload(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	createNotifyFixture(t, pool)

	var outboxID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO outbox (event, payload, status, attempts, next_retry_at)
		 VALUES ('webhook.deliver', '{"webhook_id": 123}'::jsonb, 'pending', 0, now())
		 RETURNING id`).Scan(&outboxID); err != nil {
		t.Fatalf("insert poison row: %v", err)
	}

	d := NewWebhookDispatcher(pool)
	// The scoped claim matches payload->>'webhook_id' = '123'.
	if err := d.runScoped(ctx, "123"); err != nil {
		t.Fatalf("dispatcher run: %v (want no error for a poison row)", err)
	}
	var outboxStatus string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM outbox WHERE id = $1`, outboxID).Scan(&outboxStatus); err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	if outboxStatus != "failed" {
		t.Fatalf("outbox status = %q, want failed (poison row)", outboxStatus)
	}
	// Other tests in this package leave recent history rows behind
	// (shared DB), so assert on this row's absence via the outbox
	// correlation instead: no row may reference this outbox row.
	var correlated int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM webhook_deliveries WHERE outbox_row_id = $1`, outboxID).Scan(&correlated); err != nil {
		t.Fatalf("count correlated history: %v", err)
	}
	if correlated != 0 {
		t.Fatalf("history rows correlated to poison row = %d, want 0", correlated)
	}
}

// TestWebhookDeliveryHistorySSRF: an SSRF-blocked delivery writes a
// failed history row with NULL http_status — no HTTP response was
// ever received, but the refusal IS a delivery attempt worth logging.
func TestWebhookDeliveryHistorySSRF(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	actorID, _, wsSlug, _, _ := createNotifyFixture(t, pool)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	wh := createTestWebhook(t, pool, ctx, wsSlug, actorID, srv.URL)
	enqueueWebhookRow(t, pool, ctx, wsSlug)

	d := NewWebhookDispatcher(pool) // SSRF guard ON: loopback is blocked
	if err := d.runScoped(ctx, wh.ID); err != nil {
		t.Fatalf("dispatcher run: %v", err)
	}

	r := assertHistoryRow(t, readWebhookDeliveries(t, pool, ctx, wh.ID), "failed", nil)
	if r.errStr == nil || !strings.Contains(*r.errStr, "non-public IP") {
		t.Fatalf("history error = %v, want it to mention the SSRF block", r.errStr)
	}
}

// TestWebhookDeliveryHistoryDNSFailure: a DNS failure writes a
// retrying history row with NULL http_status — transient, so the
// row goes back to pending with backoff.
func TestWebhookDeliveryHistoryDNSFailure(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	actorID, _, wsSlug, _, _ := createNotifyFixture(t, pool)

	wh := createTestWebhook(t, pool, ctx, wsSlug, actorID, "https://example.com/hook")
	outboxID := enqueueWebhookRow(t, pool, ctx, wsSlug)

	d := NewWebhookDispatcher(pool)
	// Deterministic DNS failure: stub the resolver instead of
	// depending on a real unresolvable hostname.
	d.lookupIP = func(ctx context.Context, host string) ([]net.IP, error) {
		return nil, errors.New("boom: no such host")
	}
	if err := d.runScoped(ctx, wh.ID); err != nil {
		t.Fatalf("dispatcher run: %v", err)
	}

	r := assertHistoryRow(t, readWebhookDeliveries(t, pool, ctx, wh.ID), "retrying", nil)
	if r.errStr == nil || !strings.Contains(*r.errStr, "DNS resolution failed") {
		t.Fatalf("history error = %v, want it to mention DNS resolution", r.errStr)
	}
	var outboxStatus string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM outbox WHERE id = $1`, outboxID).Scan(&outboxStatus); err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	if outboxStatus != "pending" {
		t.Fatalf("outbox status = %q, want pending (DNS failure retries)", outboxStatus)
	}
}

// TestListWebhookDeliveriesPaging: newest-first ordering plus the
// before cursor: limit 2 returns the two newest, then before=<last
// seen id> returns the rest.
func TestListWebhookDeliveriesPaging(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	actorID, _, wsSlug, _, _ := createNotifyFixture(t, pool)

	wh := createTestWebhook(t, pool, ctx, wsSlug, actorID, "https://example.com/hook")
	now := time.Now().UTC()
	oldest := insertWebhookDeliveryRow(t, pool, ctx, wh.ID, nil,
		EventIssueCreated, "delivered", intPtr(200), nil, now.Add(-3*time.Hour), "")
	middle := insertWebhookDeliveryRow(t, pool, ctx, wh.ID, nil,
		EventIssueCreated, "retrying", intPtr(500), strPtr2("boom"), now.Add(-2*time.Hour), "")
	newest := insertWebhookDeliveryRow(t, pool, ctx, wh.ID, nil,
		EventIssueCreated, "failed", nil, strPtr2("dead"), now.Add(-time.Hour), "")

	page1, err := ListWebhookDeliveries(ctx, pool, wsSlug, actorID, wh.ID, 2, 0)
	if err != nil {
		t.Fatalf("ListWebhookDeliveries page 1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("page 1 = %d rows, want 2", len(page1))
	}
	if page1[0].ID != newest || page1[1].ID != middle {
		t.Fatalf("page 1 ids = [%d %d], want [%d %d] (newest-first)",
			page1[0].ID, page1[1].ID, newest, middle)
	}
	// http_status NULL on the newest row (failed with no response).
	if page1[0].Status != "failed" || page1[0].HTTPStatus != nil {
		t.Fatalf("newest row = %+v, want failed with NULL http_status", page1[0])
	}
	if page1[1].Status != "retrying" || page1[1].HTTPStatus == nil || *page1[1].HTTPStatus != 500 {
		t.Fatalf("middle row = %+v, want retrying/500", page1[1])
	}

	page2, err := ListWebhookDeliveries(ctx, pool, wsSlug, actorID, wh.ID, 2, page1[1].ID)
	if err != nil {
		t.Fatalf("ListWebhookDeliveries page 2: %v", err)
	}
	if len(page2) != 1 || page2[0].ID != oldest {
		t.Fatalf("page 2 ids = %v, want [%d]", page2, oldest)
	}

	// A before cursor past the end returns nothing.
	page3, err := ListWebhookDeliveries(ctx, pool, wsSlug, actorID, wh.ID, 2, oldest)
	if err != nil {
		t.Fatalf("ListWebhookDeliveries page 3: %v", err)
	}
	if len(page3) != 0 {
		t.Fatalf("page 3 = %d rows, want 0", len(page3))
	}
}

// TestListWebhookDeliveriesAuthz: admin-only and workspace-scoped —
// a member gets ErrForbidden, a non-member gets ErrNotFound, and a
// webhook from another workspace reads as not found (no leak).
func TestListWebhookDeliveriesAuthz(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	actorID, memberID, wsSlug, _, _ := createNotifyFixture(t, pool)

	wh := createTestWebhook(t, pool, ctx, wsSlug, actorID, "https://example.com/hook")

	if _, err := ListWebhookDeliveries(ctx, pool, wsSlug, memberID, wh.ID, 25, 0); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member list = %v, want ErrForbidden", err)
	}
	outsider := createTestUser(t, pool, uniqueTestEmail("outsider"))
	if _, err := ListWebhookDeliveries(ctx, pool, wsSlug, outsider, wh.ID, 25, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider list = %v, want ErrNotFound", err)
	}
	if _, err := ListWebhookDeliveries(ctx, pool, wsSlug, actorID, wh.ID, 25, 0); err != nil {
		t.Fatalf("admin list: %v", err)
	}
	if _, err := ListWebhookDeliveries(ctx, pool, wsSlug, actorID, "00000000-0000-0000-0000-000000000000", 25, 0); !errors.Is(err, ErrWebhookNotFound) {
		t.Fatalf("missing webhook list = %v, want ErrWebhookNotFound", err)
	}

	// Cross-workspace: a webhook that exists under another workspace
	// reads as not found from here.
	actor2 := createTestUser(t, pool, uniqueTestEmail("other-admin"))
	ws2 := createTestWorkspace(t, pool, "Other", uniqueTestSlug("other-ws"), actor2)
	wh2 := createTestWebhook(t, pool, ctx, ws2.Slug, actor2, "https://example.com/hook")
	if _, err := ListWebhookDeliveries(ctx, pool, wsSlug, actorID, wh2.ID, 25, 0); !errors.Is(err, ErrWebhookNotFound) {
		t.Fatalf("cross-workspace list = %v, want ErrWebhookNotFound", err)
	}
}

// TestRedeliverWebhookDelivery: a failed entry enqueues a FRESH outbox
// row (attempts 0, pending) from the CURRENT webhook row, and leaves
// the original history row untouched.
func TestRedeliverWebhookDelivery(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	actorID, _, wsSlug, _, _ := createNotifyFixture(t, pool)

	wh := createTestWebhook(t, pool, ctx, wsSlug, actorID, "https://old.example.com/hook")
	failedID := insertWebhookDeliveryRow(t, pool, ctx, wh.ID, nil,
		EventIssueUpdated, "failed", nil, strPtr2("dead"), time.Now().UTC(), `{"id":"issue-1"}`)

	// The admin changed the URL after the failure: redelivery goes
	// to the CURRENT url, not the one that failed.
	newURL := "https://new.example.com/hook"
	if _, err := UpdateWebhook(ctx, pool, wsSlug, actorID, wh.ID,
		WebhookPatch{URL: &newURL}); err != nil {
		t.Fatalf("update url: %v", err)
	}

	if err := RedeliverWebhookDelivery(ctx, pool, wsSlug, actorID, wh.ID, failedID); err != nil {
		t.Fatalf("RedeliverWebhookDelivery: %v", err)
	}

	// Fresh outbox row: pending, attempts 0, current URL + secret,
	// history event + data.
	var outbox struct {
		status   string
		attempts int
		payload  []byte
	}
	if err := pool.QueryRow(ctx,
		`SELECT status, attempts, payload FROM outbox
		  WHERE event = 'webhook.deliver' ORDER BY id DESC LIMIT 1`,
	).Scan(&outbox.status, &outbox.attempts, &outbox.payload); err != nil {
		t.Fatalf("read fresh outbox row: %v", err)
	}
	if outbox.status != "pending" {
		t.Fatalf("fresh outbox status = %q, want pending", outbox.status)
	}
	if outbox.attempts != 0 {
		t.Fatalf("fresh outbox attempts = %d, want 0", outbox.attempts)
	}
	var wd webhookDelivery
	if err := json.Unmarshal(outbox.payload, &wd); err != nil {
		t.Fatalf("decode fresh payload: %v", err)
	}
	if wd.URL != newURL {
		t.Fatalf("fresh payload url = %q, want current url %q", wd.URL, newURL)
	}
	if wd.WebhookID != wh.ID {
		t.Fatalf("fresh payload webhook_id = %q, want %q", wd.WebhookID, wh.ID)
	}
	if wd.Event != EventIssueUpdated {
		t.Fatalf("fresh payload event = %q, want %q", wd.Event, EventIssueUpdated)
	}
	if wd.Data["id"] != "issue-1" {
		t.Fatalf("fresh payload data = %v, want {id: issue-1}", wd.Data)
	}
	// The secret is re-read from the current webhook row, never from
	// history (history never stores it).
	if wd.Secret == "" || wd.Secret != wh.Secret {
		t.Fatalf("fresh payload secret mismatch: got empty or stale secret")
	}

	// Original history row untouched: still failed, same id.
	rows := readWebhookDeliveries(t, pool, ctx, wh.ID)
	if len(rows) != 1 || rows[0].id != failedID || rows[0].status != "failed" {
		t.Fatalf("history after redeliver = %+v, want the single untouched failed row", rows)
	}
}

// TestRedeliverWebhookDeliveryRoundTrip: a dispatcher-produced failed
// row redelivers, and the redelivery's own attempt creates a NEW
// history row (correlated to the new outbox row).
func TestRedeliverWebhookDeliveryRoundTrip(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	actorID, _, wsSlug, _, _ := createNotifyFixture(t, pool)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	wh := createTestWebhook(t, pool, ctx, wsSlug, actorID, srv.URL)
	outboxID := enqueueWebhookRow(t, pool, ctx, wsSlug)
	if _, err := pool.Exec(ctx,
		`UPDATE outbox SET attempts = $2 WHERE id = $1`, outboxID, webhookMaxAttempts-1); err != nil {
		t.Fatalf("set attempts: %v", err)
	}

	d := NewWebhookDispatcher(pool)
	d.allowPrivateTargets = true
	if err := d.runScoped(ctx, wh.ID); err != nil {
		t.Fatalf("dispatcher run: %v", err)
	}
	rows := readWebhookDeliveries(t, pool, ctx, wh.ID)
	if len(rows) != 1 || rows[0].status != "failed" {
		t.Fatalf("history = %+v, want one failed row", rows)
	}
	failedID := rows[0].id

	if err := RedeliverWebhookDelivery(ctx, pool, wsSlug, actorID, wh.ID, failedID); err != nil {
		t.Fatalf("redeliver: %v", err)
	}
	// The fresh row is due now: a second scoped run delivers it, and
	// the 500 writes a SECOND history row (retrying) for the new
	// outbox row — the original failed row stays untouched.
	if err := d.runScoped(ctx, wh.ID); err != nil {
		t.Fatalf("second dispatcher run: %v", err)
	}
	rows = readWebhookDeliveries(t, pool, ctx, wh.ID)
	if len(rows) != 2 {
		t.Fatalf("history rows = %d, want 2 (original failed + redelivery retrying)", len(rows))
	}
	if rows[0].status != "retrying" || rows[1].status != "failed" || rows[1].id != failedID {
		t.Fatalf("history rows = %+v, want [retrying, original failed]", rows)
	}
	if rows[0].outboxID == nil || rows[1].outboxID == nil || *rows[0].outboxID == *rows[1].outboxID {
		t.Fatalf("history outbox correlation = %+v, want distinct outbox rows", rows)
	}
}

// TestRedeliverWebhookDeliveryRejects: delivered / retrying entries
// are not redeliverable (409), missing or cross-webhook entries are
// 404, an inactive webhook is 409, a missing webhook is 404.
func TestRedeliverWebhookDeliveryRejects(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	actorID, _, wsSlug, _, _ := createNotifyFixture(t, pool)

	wh := createTestWebhook(t, pool, ctx, wsSlug, actorID, "https://example.com/hook")
	now := time.Now().UTC()
	deliveredID := insertWebhookDeliveryRow(t, pool, ctx, wh.ID, nil,
		EventIssueCreated, "delivered", intPtr(200), nil, now, "")
	retryingID := insertWebhookDeliveryRow(t, pool, ctx, wh.ID, nil,
		EventIssueCreated, "retrying", intPtr(500), strPtr2("boom"), now, "")

	if err := RedeliverWebhookDelivery(ctx, pool, wsSlug, actorID, wh.ID, deliveredID); !errors.Is(err, ErrDeliveryNotRedeliverable) {
		t.Fatalf("redeliver delivered = %v, want ErrDeliveryNotRedeliverable", err)
	}
	if err := RedeliverWebhookDelivery(ctx, pool, wsSlug, actorID, wh.ID, retryingID); !errors.Is(err, ErrDeliveryNotRedeliverable) {
		t.Fatalf("redeliver retrying = %v, want ErrDeliveryNotRedeliverable", err)
	}
	if err := RedeliverWebhookDelivery(ctx, pool, wsSlug, actorID, wh.ID, 999999999); !errors.Is(err, ErrWebhookDeliveryNotFound) {
		t.Fatalf("redeliver missing = %v, want ErrWebhookDeliveryNotFound", err)
	}

	// Cross-webhook: the delivery exists under another webhook — same
	// 404, never leak cross-webhook existence.
	wh2 := createTestWebhook(t, pool, ctx, wsSlug, actorID, "https://example.com/hook2")
	otherID := insertWebhookDeliveryRow(t, pool, ctx, wh2.ID, nil,
		EventIssueCreated, "failed", nil, strPtr2("dead"), now, "")
	if err := RedeliverWebhookDelivery(ctx, pool, wsSlug, actorID, wh.ID, otherID); !errors.Is(err, ErrWebhookDeliveryNotFound) {
		t.Fatalf("redeliver cross-webhook = %v, want ErrWebhookDeliveryNotFound", err)
	}

	// Inactive webhook: 409 (the dispatcher would skip it anyway).
	failedID := insertWebhookDeliveryRow(t, pool, ctx, wh.ID, nil,
		EventIssueCreated, "failed", nil, strPtr2("dead"), now, "")
	inactive := false
	if _, err := UpdateWebhook(ctx, pool, wsSlug, actorID, wh.ID,
		WebhookPatch{Active: &inactive}); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if err := RedeliverWebhookDelivery(ctx, pool, wsSlug, actorID, wh.ID, failedID); !errors.Is(err, ErrDeliveryNotRedeliverable) {
		t.Fatalf("redeliver inactive webhook = %v, want ErrDeliveryNotRedeliverable", err)
	}

	// Missing webhook: 404.
	if err := RedeliverWebhookDelivery(ctx, pool, wsSlug, actorID, "00000000-0000-0000-0000-000000000000", failedID); !errors.Is(err, ErrWebhookNotFound) {
		t.Fatalf("redeliver missing webhook = %v, want ErrWebhookNotFound", err)
	}
}

// TestRedeliverWebhookDeliveryAuthz: admin-only, workspace-scoped.
func TestRedeliverWebhookDeliveryAuthz(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	actorID, memberID, wsSlug, _, _ := createNotifyFixture(t, pool)

	wh := createTestWebhook(t, pool, ctx, wsSlug, actorID, "https://example.com/hook")
	failedID := insertWebhookDeliveryRow(t, pool, ctx, wh.ID, nil,
		EventIssueCreated, "failed", nil, strPtr2("dead"), time.Now().UTC(), "")

	if err := RedeliverWebhookDelivery(ctx, pool, wsSlug, memberID, wh.ID, failedID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member redeliver = %v, want ErrForbidden", err)
	}
	outsider := createTestUser(t, pool, uniqueTestEmail("outsider"))
	if err := RedeliverWebhookDelivery(ctx, pool, wsSlug, outsider, wh.ID, failedID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider redeliver = %v, want ErrNotFound", err)
	}
}

// TestWebhookDeliveryRetentionWindowPinned: history lives 90 days —
// longer than the outbox's 30 — so redelivery outlives the queue.
func TestWebhookDeliveryRetentionWindowPinned(t *testing.T) {
	if WebhookDeliveryRetentionDays != 90 {
		t.Fatalf("WebhookDeliveryRetentionDays = %d, want 90", WebhookDeliveryRetentionDays)
	}
}

// TestPruneWebhookDeliveries: deletes >90d rows of ALL statuses,
// keeps recent rows and rows exactly at the cutoff (strict <).
func TestPruneWebhookDeliveries(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	actorID, _, wsSlug, _, _ := createNotifyFixture(t, pool)

	wh := createTestWebhook(t, pool, ctx, wsSlug, actorID, "https://example.com/hook")
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -WebhookDeliveryRetentionDays-1)
	fresh := now.AddDate(0, 0, -WebhookDeliveryRetentionDays+1)
	cutoff := now.AddDate(0, 0, -WebhookDeliveryRetentionDays)

	oldDelivered := insertWebhookDeliveryRow(t, pool, ctx, wh.ID, nil,
		EventIssueCreated, "delivered", intPtr(200), nil, old, "")
	oldRetrying := insertWebhookDeliveryRow(t, pool, ctx, wh.ID, nil,
		EventIssueCreated, "retrying", intPtr(500), strPtr2("boom"), old, "")
	oldFailed := insertWebhookDeliveryRow(t, pool, ctx, wh.ID, nil,
		EventIssueCreated, "failed", nil, strPtr2("dead"), old, "")
	freshFailed := insertWebhookDeliveryRow(t, pool, ctx, wh.ID, nil,
		EventIssueCreated, "failed", nil, strPtr2("dead"), fresh, "")
	cutoffFailed := insertWebhookDeliveryRow(t, pool, ctx, wh.ID, nil,
		EventIssueCreated, "failed", nil, strPtr2("dead"), cutoff, "")

	deleted, err := PruneWebhookDeliveries(ctx, pool, now)
	if err != nil {
		t.Fatalf("PruneWebhookDeliveries: %v", err)
	}
	if deleted != 3 {
		t.Fatalf("deleted = %d, want 3 (the three old rows, all statuses)", deleted)
	}
	exists := func(id int64) bool {
		var ok bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM webhook_deliveries WHERE id = $1)`, id).Scan(&ok); err != nil {
			t.Fatalf("row exists: %v", err)
		}
		return ok
	}
	for _, tc := range []struct {
		name string
		id   int64
		want bool
	}{
		{"old delivered pruned", oldDelivered, false},
		{"old retrying pruned", oldRetrying, false},
		{"old failed pruned", oldFailed, false},
		{"fresh failed kept", freshFailed, true},
		{"cutoff failed kept", cutoffFailed, true},
	} {
		if got := exists(tc.id); got != tc.want {
			t.Fatalf("%s: exists = %v, want %v", tc.name, got, tc.want)
		}
	}

	// A second pass over an empty backlog deletes nothing.
	deleted, err = PruneWebhookDeliveries(ctx, pool, now)
	if err != nil {
		t.Fatalf("PruneWebhookDeliveries (second pass): %v", err)
	}
	if deleted != 0 {
		t.Fatalf("second pass deleted = %d, want 0", deleted)
	}
}

// TestPruneWebhookDeliveriesFailureIsNeverFatal: a sweep failure
// surfaces as a returned error — no panic, no process death.
func TestPruneWebhookDeliveriesFailureIsNeverFatal(t *testing.T) {
	pool := newTestPool(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := PruneWebhookDeliveries(ctx, pool, time.Now()); err == nil {
		t.Fatal("PruneWebhookDeliveries with cancelled context: expected error, got nil")
	}
}
