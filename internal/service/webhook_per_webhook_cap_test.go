package service

// C58T0: per-webhook claim cap in the global (unscoped) claim. A single
// webhook's due backlog must not fill the whole claim batch and starve
// every other webhook's rows (head-of-line blocking). The global claim
// takes at most webhookPerWebhookCap rows per webhook per pass; the
// scoped (test-only) claim stays uncapped.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// clearDueWebhookOutboxRows deletes leftover due webhook outbox rows from
// other tests. The test DB is shared, and an unscoped Run claims the
// oldest rows globally, so another test's pending rows (e.g. the scoped
// test's 30 untouched decoy rows) would fill the claim batch and starve
// this test's own rows. Other tests assert on their rows within their
// own test body, so deleting their leftovers here is safe.
func clearDueWebhookOutboxRows(t *testing.T, pool *pgxpool.Pool, ctx context.Context) {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`DELETE FROM outbox WHERE event LIKE 'webhook.%'
		   AND next_retry_at <= now()
		   AND (status = 'pending' OR status = 'delivering')`); err != nil {
		t.Fatalf("clear due webhook outbox rows: %v", err)
	}
}

// insertWebhookOutboxRowWithData seeds one due pending webhook.deliver row
// carrying the given data payload (used for ordering markers), returning
// its outbox id.
func insertWebhookOutboxRowWithData(t *testing.T, pool *pgxpool.Pool, ctx context.Context,
	webhookID, url string, data map[string]any) int64 {
	t.Helper()
	payload, err := json.Marshal(webhookDelivery{
		WebhookID: webhookID,
		URL:       url,
		Secret:    "test-secret",
		Event:     EventIssueCreated,
		Data:      data,
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

// countingServer records how many deliveries hit it and, in order, the
// "marker" field of each delivered body. Delivery within one Run is
// sequential, so the recorded order is the claim order.
type countingServer struct {
	srv     *httptest.Server
	hits    atomic.Int64
	mu      sync.Mutex
	markers []string
}

func newCountingServer(t *testing.T) *countingServer {
	t.Helper()
	cs := &countingServer{}
	cs.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cs.hits.Add(1)
		body, _ := io.ReadAll(r.Body)
		var decoded struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(body, &decoded); err == nil {
			if m, ok := decoded.Data["marker"].(string); ok {
				cs.mu.Lock()
				cs.markers = append(cs.markers, m)
				cs.mu.Unlock()
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(cs.srv.Close)
	return cs
}

func (cs *countingServer) orderedMarkers() []string {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	out := make([]string, len(cs.markers))
	copy(out, cs.markers)
	return out
}

// outboxStatuses returns id -> status for the given outbox rows.
func outboxStatuses(t *testing.T, pool *pgxpool.Pool, ctx context.Context, ids []int64) map[int64]string {
	t.Helper()
	got := make(map[int64]string, len(ids))
	for _, id := range ids {
		var status string
		if err := pool.QueryRow(ctx,
			`SELECT status FROM outbox WHERE id = $1`, id).Scan(&status); err != nil {
			t.Fatalf("read outbox status %d: %v", id, err)
		}
		got[id] = status
	}
	return got
}

// seedWebhookRows seeds n due rows for one webhook, all with distinct
// markers "prefix-i", and returns their outbox ids in seed (oldest-first) order.
func seedWebhookRows(t *testing.T, pool *pgxpool.Pool, ctx context.Context,
	webhookID, url, prefix string, n int) []int64 {
	t.Helper()
	ids := make([]int64, 0, n)
	for i := 0; i < n; i++ {
		ids = append(ids, insertWebhookOutboxRowWithData(t, pool, ctx, webhookID, url,
			map[string]any{"marker": prefix + "-" + strconv.Itoa(i)}))
	}
	return ids
}

func TestWebhookGlobalClaimCapsRowsPerWebhook(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	clearDueWebhookOutboxRows(t, pool, ctx)
	actorID, _, wsSlug, _, _ := createNotifyFixture(t, pool)

	flood := newCountingServer(t)
	other := newCountingServer(t)
	whA := createTestWebhook(t, pool, ctx, wsSlug, actorID, flood.srv.URL)
	whB := createTestWebhook(t, pool, ctx, wsSlug, actorID, other.srv.URL)

	// Flood: MORE rows for webhook A than one claim batch can hold, all
	// older than B's single row. Without a per-webhook cap, one unscoped
	// Run fills with A's rows and B's row starves.
	floodIDs := seedWebhookRows(t, pool, ctx, whA.ID, flood.srv.URL, "a", webhookBatchSize)
	bID := insertWebhookOutboxRowWithData(t, pool, ctx, whB.ID, other.srv.URL,
		map[string]any{"marker": "b-0"})

	d := NewWebhookDispatcher(pool)
	d.allowPrivateTargets = true
	if err := d.Run(ctx); err != nil {
		t.Fatalf("unscoped run: %v", err)
	}

	if got := other.hits.Load(); got != 1 {
		t.Errorf("webhook B hits = %d, want 1 (B's row must not wait behind A's backlog)", got)
	}
	if got := flood.hits.Load(); got > int64(webhookPerWebhookCap) {
		t.Errorf("webhook A hits = %d, want at most cap %d in one pass", got, webhookPerWebhookCap)
	}
	statuses := outboxStatuses(t, pool, ctx, append(append([]int64{}, floodIDs...), bID))
	if statuses[bID] != "done" {
		t.Errorf("B row status = %q, want done", statuses[bID])
	}
	doneA := 0
	for _, id := range floodIDs {
		if statuses[id] == "done" {
			doneA++
		}
	}
	if doneA != webhookPerWebhookCap {
		t.Errorf("delivered A rows = %d, want exactly cap %d", doneA, webhookPerWebhookCap)
	}
}

func TestWebhookGlobalClaimNormalLoad(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	clearDueWebhookOutboxRows(t, pool, ctx)
	actorID, _, wsSlug, _, _ := createNotifyFixture(t, pool)

	srv := newCountingServer(t)
	wh := createTestWebhook(t, pool, ctx, wsSlug, actorID, srv.srv.URL)
	ids := seedWebhookRows(t, pool, ctx, wh.ID, srv.srv.URL, "n", 3)

	d := NewWebhookDispatcher(pool)
	d.allowPrivateTargets = true
	if err := d.Run(ctx); err != nil {
		t.Fatalf("unscoped run: %v", err)
	}

	if got := srv.hits.Load(); got != 3 {
		t.Errorf("hits = %d, want 3 (normal load below the cap is unchanged)", got)
	}
	for id, status := range outboxStatuses(t, pool, ctx, ids) {
		if status != "done" {
			t.Errorf("row %d status = %q, want done", id, status)
		}
	}
}

func TestWebhookGlobalClaimCapBoundary(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	clearDueWebhookOutboxRows(t, pool, ctx)
	actorID, _, wsSlug, _, _ := createNotifyFixture(t, pool)

	srvA := newCountingServer(t)
	srvB := newCountingServer(t)
	whA := createTestWebhook(t, pool, ctx, wsSlug, actorID, srvA.srv.URL)
	whB := createTestWebhook(t, pool, ctx, wsSlug, actorID, srvB.srv.URL)

	// Exactly cap rows for A + 2 for B: one pass must deliver ALL of
	// them — the cap binds only above cap rows per webhook.
	idsA := seedWebhookRows(t, pool, ctx, whA.ID, srvA.srv.URL, "a", webhookPerWebhookCap)
	idsB := seedWebhookRows(t, pool, ctx, whB.ID, srvB.srv.URL, "b", 2)

	d := NewWebhookDispatcher(pool)
	d.allowPrivateTargets = true
	if err := d.Run(ctx); err != nil {
		t.Fatalf("unscoped run: %v", err)
	}

	if got := srvA.hits.Load(); got != int64(webhookPerWebhookCap) {
		t.Errorf("webhook A hits = %d, want %d", got, webhookPerWebhookCap)
	}
	if got := srvB.hits.Load(); got != 2 {
		t.Errorf("webhook B hits = %d, want 2", got)
	}
	for id, status := range outboxStatuses(t, pool, ctx, append(idsA, idsB...)) {
		if status != "done" {
			t.Errorf("row %d status = %q, want done", id, status)
		}
	}
}

func TestWebhookScopedRunUncapped(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	clearDueWebhookOutboxRows(t, pool, ctx)
	actorID, _, wsSlug, _, _ := createNotifyFixture(t, pool)

	srv := newCountingServer(t)
	wh := createTestWebhook(t, pool, ctx, wsSlug, actorID, srv.srv.URL)
	// MORE than the global per-webhook cap, but the scoped claim must
	// still deliver all of them in one pass.
	ids := seedWebhookRows(t, pool, ctx, wh.ID, srv.srv.URL, "s", webhookPerWebhookCap+3)

	d := NewWebhookDispatcher(pool)
	d.allowPrivateTargets = true
	if err := d.runScoped(ctx, wh.ID); err != nil {
		t.Fatalf("scoped run: %v", err)
	}

	if got := srv.hits.Load(); got != int64(webhookPerWebhookCap+3) {
		t.Errorf("hits = %d, want %d (scoped run is uncapped)", got, webhookPerWebhookCap+3)
	}
	for id, status := range outboxStatuses(t, pool, ctx, ids) {
		if status != "done" {
			t.Errorf("row %d status = %q, want done", id, status)
		}
	}
}

func TestWebhookGlobalClaimOldestFirstWithinWebhook(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	clearDueWebhookOutboxRows(t, pool, ctx)
	actorID, _, wsSlug, _, _ := createNotifyFixture(t, pool)

	srv := newCountingServer(t)
	wh := createTestWebhook(t, pool, ctx, wsSlug, actorID, srv.srv.URL)
	// Seed in a known order; the server must see them oldest-first.
	seedWebhookRows(t, pool, ctx, wh.ID, srv.srv.URL, "o", 3)

	d := NewWebhookDispatcher(pool)
	d.allowPrivateTargets = true
	if err := d.Run(ctx); err != nil {
		t.Fatalf("unscoped run: %v", err)
	}

	want := []string{"o-0", "o-1", "o-2"}
	got := srv.orderedMarkers()
	if len(got) != len(want) {
		t.Fatalf("markers = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("markers[%d] = %q, want %q (oldest-first preserved)", i, got[i], want[i])
		}
	}
}

func TestWebhookGlobalClaimDrainsEverything(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	clearDueWebhookOutboxRows(t, pool, ctx)
	actorID, _, wsSlug, _, _ := createNotifyFixture(t, pool)

	srvA := newCountingServer(t)
	srvB := newCountingServer(t)
	whA := createTestWebhook(t, pool, ctx, wsSlug, actorID, srvA.srv.URL)
	whB := createTestWebhook(t, pool, ctx, wsSlug, actorID, srvB.srv.URL)
	idsA := seedWebhookRows(t, pool, ctx, whA.ID, srvA.srv.URL, "a", 12)
	idsB := seedWebhookRows(t, pool, ctx, whB.ID, srvB.srv.URL, "b", 3)

	d := NewWebhookDispatcher(pool)
	d.allowPrivateTargets = true
	// The cap delays, never drops: repeated passes must deliver every row.
	for pass := 0; pass < 10; pass++ {
		if err := d.Run(ctx); err != nil {
			t.Fatalf("unscoped run pass %d: %v", pass, err)
		}
		if srvA.hits.Load() == 12 && srvB.hits.Load() == 3 {
			break
		}
	}
	if got := srvA.hits.Load(); got != 12 {
		t.Errorf("webhook A hits = %d, want 12 (all rows eventually delivered)", got)
	}
	if got := srvB.hits.Load(); got != 3 {
		t.Errorf("webhook B hits = %d, want 3", got)
	}
	for id, status := range outboxStatuses(t, pool, ctx, append(idsA, idsB...)) {
		if status != "done" {
			t.Errorf("row %d status = %q, want done (cap must not drop rows)", id, status)
		}
	}
}
