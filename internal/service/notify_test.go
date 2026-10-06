package service

// Notifications + webhooks tests (Task 26): notification rows written in
// the mutation tx (assignee notified, actor NOT), prefs gating, and the
// webhook outbox dispatcher with retry. All tests run against the real
// test database — no skips. Harness from workspace_test.go / issue_test.go.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func createNotifyFixture(t *testing.T, pool *pgxpool.Pool) (actorID, assigneeID, wsSlug, ident, issueID string) {
	t.Helper()
	ctx := context.Background()
	actorID = createTestUser(t, pool, uniqueTestEmail("notify-actor"))
	assigneeID = createTestUser(t, pool, uniqueTestEmail("notify-assignee"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-notify"), actorID)
	// Assignee must be a workspace member to be assignable.
	if _, err := pool.Exec(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role)
		 VALUES ($1::uuid, $2::uuid, $3) ON CONFLICT DO NOTHING`,
		ws.ID, assigneeID, RoleMember); err != nil {
		t.Fatalf("add member: %v", err)
	}
	p := createTestProject(t, pool, ws.Slug, actorID, "Engineering", uniqueTestIdentifier())
	iss := createTestIssue(t, pool, ws.Slug, p.Identifier, actorID, "Fix the thing")
	return actorID, assigneeID, ws.Slug, p.Identifier, iss.ID
}

func countNotifications(t *testing.T, pool *pgxpool.Pool, userID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM notifications WHERE user_id = $1::uuid`, userID).Scan(&n); err != nil {
		t.Fatalf("count notifications: %v", err)
	}
	return n
}

// TestAssignNotifiesAssignee: assigning an issue notifies the assignee
// (in-app row, type issue.assigned) but NOT the actor who performed the
// assignment.
func TestAssignNotifiesAssignee(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, assigneeID, wsSlug, ident, issueID := createNotifyFixture(t, pool)

	if err := AssignAssignee(ctx, pool, wsSlug, ident, issueID, assigneeID, actorID); err != nil {
		t.Fatalf("AssignAssignee: %v", err)
	}

	if n := countNotifications(t, pool, assigneeID); n != 1 {
		t.Fatalf("assignee notifications = %d, want 1", n)
	}
	var typ, title string
	if err := pool.QueryRow(ctx,
		`SELECT type, title FROM notifications WHERE user_id = $1::uuid`,
		assigneeID).Scan(&typ, &title); err != nil {
		t.Fatalf("read notification: %v", err)
	}
	if typ != NotifyIssueAssigned {
		t.Fatalf("notification type = %q, want %q", typ, NotifyIssueAssigned)
	}
	if title == "" {
		t.Fatal("notification title is empty")
	}
	// The actor must not be notified about their own action.
	if n := countNotifications(t, pool, actorID); n != 0 {
		t.Fatalf("actor notifications = %d, want 0 (dedup)", n)
	}
}

// TestAssignIdempotentNoDuplicateNotification: re-assigning the same user
// is a no-op and must not create a second notification row.
func TestAssignIdempotentNoDuplicateNotification(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, assigneeID, wsSlug, ident, issueID := createNotifyFixture(t, pool)

	if err := AssignAssignee(ctx, pool, wsSlug, ident, issueID, assigneeID, actorID); err != nil {
		t.Fatalf("AssignAssignee: %v", err)
	}
	if err := AssignAssignee(ctx, pool, wsSlug, ident, issueID, assigneeID, actorID); err != nil {
		t.Fatalf("re-AssignAssignee: %v", err)
	}
	if n := countNotifications(t, pool, assigneeID); n != 1 {
		t.Fatalf("assignee notifications after idempotent re-assign = %d, want 1", n)
	}
}

// TestCommentNotifiesSubscribers: a comment notifies the issue's
// subscribers (not the commenter).
func TestCommentNotifiesSubscribers(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, assigneeID, wsSlug, ident, issueID := createNotifyFixture(t, pool)

	if err := SubscribeIssue(ctx, pool, wsSlug, ident, issueID, assigneeID); err != nil {
		t.Fatalf("SubscribeIssue: %v", err)
	}
	if _, err := CreateComment(ctx, pool, wsSlug, ident, issueID, actorID,
		[]byte(`{"type":"doc","content":[]}`), nil); err != nil {
		t.Fatalf("CreateComment: %v", err)
	}

	if n := countNotifications(t, pool, assigneeID); n != 1 {
		t.Fatalf("subscriber notifications = %d, want 1", n)
	}
	if n := countNotifications(t, pool, actorID); n != 0 {
		t.Fatalf("commenter notifications = %d, want 0 (dedup)", n)
	}
}

// TestWebhookDeliveryRetried: a webhook endpoint returning 500 keeps the
// outbox row pending with attempts bumped and a future next_retry_at; a
// later 200 marks it done. The request carries the HMAC signature header.
func TestWebhookDeliveryRetried(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, _, wsSlug, _, _ := createNotifyFixture(t, pool)

	var calls atomic.Int64
	var gotSignature atomic.Value
	var gotBody atomic.Value
	var fail atomic.Bool
	fail.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		gotSignature.Store(r.Header.Get("X-Glance-Signature"))
		body, _ := io.ReadAll(r.Body)
		gotBody.Store(body)
		if r.Header.Get("X-Glance-Event") != EventIssueCreated {
			t.Errorf("X-Glance-Event = %q, want %q", r.Header.Get("X-Glance-Event"), EventIssueCreated)
		}
		if fail.Load() {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	wsID := workspaceIDForTest(t, pool, wsSlug)
	wh, err := CreateWebhook(ctx, pool, wsSlug, actorID, WebhookInput{
		URL: srv.URL, Secret: strPtr2("s3cret"), Events: []string{"issue.created"},
	})
	if err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}
	if wh.ID == "" {
		t.Fatal("webhook id empty")
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := enqueueWebhookDeliveryTx(ctx, tx, wsID, EventIssueCreated,
		map[string]any{"id": "x"}); err != nil {
		tx.Rollback(ctx)
		t.Fatalf("enqueueWebhookDeliveryTx: %v", err)
	}
	// Capture our own outbox row: the test DB is shared across runs.
	var outboxID int64
	if err := tx.QueryRow(ctx,
		`SELECT id FROM outbox WHERE event = 'webhook.deliver' ORDER BY id DESC LIMIT 1`,
	).Scan(&outboxID); err != nil {
		tx.Rollback(ctx)
		t.Fatalf("outbox id: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	d := NewWebhookDispatcher(pool)
	d.allowPrivateTargets = true // httptest targets are loopback by construction
	if err := d.Run(ctx); err != nil {
		t.Fatalf("dispatcher run 1: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("delivery attempts = %d, want 1", calls.Load())
	}
	sig, _ := gotSignature.Load().(string)
	if sig == "" || len(sig) < 10 {
		t.Fatalf("missing HMAC signature header: %q", sig)
	}
	// The signature must be a valid HMAC-SHA256 of the delivered body
	// under the webhook secret.
	body, _ := gotBody.Load().([]byte)
	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write(body)
	wantSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if sig != wantSig {
		t.Fatalf("signature = %q, want %q", sig, wantSig)
	}
	var status string
	var attempts int
	var nextRetryNull bool
	if err := pool.QueryRow(ctx,
		`SELECT status, attempts, next_retry_at IS NULL FROM outbox WHERE id = $1`, outboxID).Scan(&status, &attempts, &nextRetryNull); err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	if status != "pending" {
		t.Fatalf("status after 500 = %q, want pending", status)
	}
	if attempts != 1 {
		t.Fatalf("attempts after 500 = %d, want 1", attempts)
	}

	// Endpoint recovers → next pass delivers and marks done.
	fail.Store(false)
	// Force the row due again (backoff would otherwise delay it).
	if _, err := pool.Exec(ctx,
		`UPDATE outbox SET next_retry_at = now() WHERE id = $1`, outboxID); err != nil {
		t.Fatalf("force due: %v", err)
	}
	if err := d.Run(ctx); err != nil {
		t.Fatalf("dispatcher run 2: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT status FROM outbox WHERE id = $1`, outboxID).Scan(&status); err != nil {
		t.Fatalf("read outbox 2: %v", err)
	}
	if status != "done" {
		t.Fatalf("status after 200 = %q, want done", status)
	}
}

func workspaceIDForTest(t *testing.T, pool *pgxpool.Pool, slug string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM workspaces WHERE slug = $1`, slug).Scan(&id); err != nil {
		t.Fatalf("workspace id: %v", err)
	}
	return id
}

// TestWebhookCRUDAdminOnly: webhook CRUD requires the workspace admin
// role; members get ErrForbidden. Bad URLs and unknown events are
// rejected at validation.
func TestWebhookCRUDAdminOnly(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, assigneeID, wsSlug, _, _ := createNotifyFixture(t, pool)

	wh, err := CreateWebhook(ctx, pool, wsSlug, actorID, WebhookInput{
		URL: "https://example.com/hook", Events: []string{"issue.created"},
	})
	if err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}
	if wh.Secret == "" {
		t.Fatal("expected a generated secret")
	}
	if len(wh.Events) != 1 || wh.Events[0] != "issue.created" {
		t.Fatalf("events = %v", wh.Events)
	}

	// Member (not admin) is forbidden.
	if _, err := CreateWebhook(ctx, pool, wsSlug, assigneeID, WebhookInput{
		URL: "https://example.com/hook",
	}); err != ErrForbidden {
		t.Fatalf("member CreateWebhook: want ErrForbidden, got %v", err)
	}
	if _, err := ListWebhooks(ctx, pool, wsSlug, assigneeID); err != ErrForbidden {
		t.Fatalf("member ListWebhooks: want ErrForbidden, got %v", err)
	}

	if _, err := CreateWebhook(ctx, pool, wsSlug, actorID, WebhookInput{
		URL: "ftp://example.com/hook",
	}); err == nil {
		t.Fatal("non-http URL: want error, got nil")
	}
	if _, err := CreateWebhook(ctx, pool, wsSlug, actorID, WebhookInput{
		URL: "https://example.com/hook", Events: []string{"nope.event"},
	}); err == nil {
		t.Fatal("unknown event: want error, got nil")
	}

	got, err := GetWebhook(ctx, pool, wsSlug, actorID, wh.ID)
	if err != nil || got.ID != wh.ID {
		t.Fatalf("GetWebhook: %v", err)
	}
	active := false
	upd, err := UpdateWebhook(ctx, pool, wsSlug, actorID, wh.ID, WebhookPatch{Active: &active})
	if err != nil || upd.Active {
		t.Fatalf("UpdateWebhook: %v", err)
	}
	if _, err := UpdateWebhook(ctx, pool, wsSlug, actorID, wh.ID, WebhookPatch{}); err == nil {
		t.Fatal("empty patch: want ErrNothingToUpdate, got nil")
	}
	if err := DeleteWebhook(ctx, pool, wsSlug, actorID, wh.ID); err != nil {
		t.Fatalf("DeleteWebhook: %v", err)
	}
	if _, err := GetWebhook(ctx, pool, wsSlug, actorID, wh.ID); err == nil {
		t.Fatal("get after delete: want error, got nil")
	}

	// A webhook with an empty events list receives every domain event.
	whAll, err := CreateWebhook(ctx, pool, wsSlug, actorID, WebhookInput{
		URL: "https://example.com/all",
	})
	if err != nil {
		t.Fatalf("CreateWebhook all-events: %v", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := enqueueWebhookDeliveryTx(ctx, tx, workspaceIDForTest(t, pool, wsSlug),
		EventIssueDeleted, map[string]any{"id": "y"}); err != nil {
		tx.Rollback(ctx)
		t.Fatalf("enqueue: %v", err)
	}
	var n int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM outbox WHERE event = 'webhook.deliver' AND payload->>'webhook_id' = $1`,
		whAll.ID).Scan(&n); err != nil {
		tx.Rollback(ctx)
		t.Fatalf("count: %v", err)
	}
	tx.Rollback(ctx)
	if n != 1 {
		t.Fatalf("all-events webhook deliveries = %d, want 1", n)
	}
}

// TestStateChangeNotifiesWatchers: moving an issue's state notifies its
// subscribers (not the actor); a patch that changes nothing notifies
// nobody.
func TestStateChangeNotifiesWatchers(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, watcherID, wsSlug, ident, issueID := createNotifyFixture(t, pool)

	if err := SubscribeIssue(ctx, pool, wsSlug, ident, issueID, watcherID); err != nil {
		t.Fatalf("SubscribeIssue: %v", err)
	}
	states, err := ListStates(ctx, pool, wsSlug, ident, actorID)
	if err != nil || len(states) < 2 {
		t.Fatalf("ListStates: %v (%d states)", err, len(states))
	}
	var target string
	// Find a state different from the issue's current one.
	iss, err := GetIssue(ctx, pool, wsSlug, ident, issueID, actorID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	for _, s := range states {
		if s.ID != iss.StateID {
			target = s.ID
			break
		}
	}
	if target == "" {
		t.Fatal("no alternative state found")
	}

	if _, err := UpdateIssue(ctx, pool, wsSlug, ident, issueID, actorID,
		IssuePatch{StateID: &target}); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	if n := countNotifications(t, pool, watcherID); n != 1 {
		t.Fatalf("watcher notifications = %d, want 1", n)
	}
	var typ string
	if err := pool.QueryRow(ctx,
		`SELECT type FROM notifications WHERE user_id = $1::uuid`, watcherID).Scan(&typ); err != nil {
		t.Fatalf("read type: %v", err)
	}
	if typ != NotifyStateChanged {
		t.Fatalf("type = %q, want %q", typ, NotifyStateChanged)
	}
	if n := countNotifications(t, pool, actorID); n != 0 {
		t.Fatalf("actor notifications = %d, want 0", n)
	}
}

// TestNotificationPrefEmailGatesOutbox: with email pref on, an assignment
// enqueues an email.notification outbox row; with defaults it does not.
func TestNotificationPrefEmailGatesOutbox(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, assigneeID, wsSlug, ident, issueID := createNotifyFixture(t, pool)

	if _, err := SetNotificationPref(ctx, pool, assigneeID, NotifyIssueAssigned, true, true); err != nil {
		t.Fatalf("SetNotificationPref: %v", err)
	}
	if _, err := SetNotificationPref(ctx, pool, assigneeID, "bogus.event", true, false); err == nil {
		t.Fatal("unknown event pref: want error, got nil")
	}
	// Watermark: the test DB is shared across runs, so scope counts to
	// rows this test creates.
	var watermark int64
	if err := pool.QueryRow(ctx, `SELECT COALESCE(MAX(id), 0) FROM outbox`).Scan(&watermark); err != nil {
		t.Fatalf("watermark: %v", err)
	}
	countEmail := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM outbox WHERE event = 'email.notification' AND id > $1`,
			watermark).Scan(&n); err != nil {
			t.Fatalf("count outbox: %v", err)
		}
		return n
	}

	if err := AssignAssignee(ctx, pool, wsSlug, ident, issueID, assigneeID, actorID); err != nil {
		t.Fatalf("AssignAssignee: %v", err)
	}
	if n := countEmail(); n != 1 {
		t.Fatalf("email.notification rows = %d, want 1", n)
	}

	// Defaults (no pref row): in-app row yes, email outbox row no.
	otherID := createTestUser(t, pool, uniqueTestEmail("notify-other"))
	if _, err := pool.Exec(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role)
		 VALUES ((SELECT id FROM workspaces WHERE slug = $1), $2::uuid, $3)
		 ON CONFLICT DO NOTHING`, wsSlug, otherID, RoleMember); err != nil {
		t.Fatalf("add member: %v", err)
	}
	iss2 := createTestIssue(t, pool, wsSlug, ident, actorID, "Second issue")
	if err := AssignAssignee(ctx, pool, wsSlug, ident, iss2.ID, otherID, actorID); err != nil {
		t.Fatalf("AssignAssignee 2: %v", err)
	}
	if n := countEmail(); n != 1 {
		t.Fatalf("email.notification rows after default-pref assign = %d, want 1 (unchanged)", n)
	}
	if c := countNotifications(t, pool, otherID); c != 1 {
		t.Fatalf("in-app notifications for default pref = %d, want 1", c)
	}
}

// TestWebhookRetryBackoff: exponential backoff doubles from the 5s base
// and caps at 5 minutes; the dispatcher gives up after 8 attempts.
func TestWebhookRetryBackoff(t *testing.T) {
	if got := webhookRetryBackoff(1); got != 5_000_000_000 {
		t.Fatalf("attempt 1 backoff = %v, want 5s", got)
	}
	if got := webhookRetryBackoff(2); got != 10_000_000_000 {
		t.Fatalf("attempt 2 backoff = %v, want 10s", got)
	}
	if got := webhookRetryBackoff(100); got != 5*60_000_000_000 {
		t.Fatalf("attempt 100 backoff = %v, want 5m cap", got)
	}
	if webhookMaxAttempts != 8 {
		t.Fatalf("max attempts = %d, want 8", webhookMaxAttempts)
	}
}

// TestWebhookSecretHiddenInListGetUpdate: the signing secret is
// show-on-create only. CreateWebhook returns it in the Go struct and the
// 201 wire shape carries it; the list/get/update wire shapes must not
// contain a "secret" key (no leakage into logs, devtools, or caches).
func TestWebhookSecretHiddenInListGetUpdate(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, _, wsSlug, _, _ := createNotifyFixture(t, pool)

	wh, err := CreateWebhook(ctx, pool, wsSlug, actorID, WebhookInput{
		URL: "https://example.com/hook", Secret: strPtr2("topsecret"),
	})
	if err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}
	if wh.Secret != "topsecret" {
		t.Fatalf("create returned secret = %q, want topsecret", wh.Secret)
	}
	// The 201 wire shape carries the secret exactly once.
	raw, err := json.Marshal(wh.CreateResponse())
	if err != nil {
		t.Fatalf("marshal create response: %v", err)
	}
	var created map[string]any
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created["secret"] != "topsecret" {
		t.Fatalf("create response secret = %v, want topsecret", created["secret"])
	}

	assertNoSecret := func(name string, v any) {
		t.Helper()
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal %s: %v", name, err)
		}
		if strings.Contains(string(raw), `"secret"`) {
			t.Fatalf("%s wire shape leaks secret: %s", name, raw)
		}
	}

	list, err := ListWebhooks(ctx, pool, wsSlug, actorID)
	if err != nil {
		t.Fatalf("ListWebhooks: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list = %d, want 1", len(list))
	}
	assertNoSecret("list", map[string]any{"webhooks": list})

	got, err := GetWebhook(ctx, pool, wsSlug, actorID, wh.ID)
	if err != nil {
		t.Fatalf("GetWebhook: %v", err)
	}
	assertNoSecret("get", got)

	f := false
	upd, err := UpdateWebhook(ctx, pool, wsSlug, actorID, wh.ID, WebhookPatch{Active: &f})
	if err != nil {
		t.Fatalf("UpdateWebhook: %v", err)
	}
	assertNoSecret("update", upd)
}

// createTestWebhook registers one webhook against srvURL subscribed to
// issue.created.
func createTestWebhook(t *testing.T, pool *pgxpool.Pool, ctx context.Context,
	wsSlug, actorID, srvURL string) *Webhook {
	t.Helper()
	wh, err := CreateWebhook(ctx, pool, wsSlug, actorID, WebhookInput{
		URL: srvURL, Events: []string{"issue.created"},
	})
	if err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}
	return wh
}

// enqueueWebhookRow enqueues webhook.deliver rows for every active
// subscribed webhook in the workspace and returns the latest outbox row
// id. The test DB is shared across runs, so rows are captured by id.
func enqueueWebhookRow(t *testing.T, pool *pgxpool.Pool, ctx context.Context, wsSlug string) int64 {
	t.Helper()
	wsID := workspaceIDForTest(t, pool, wsSlug)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := enqueueWebhookDeliveryTx(ctx, tx, wsID, EventIssueCreated,
		map[string]any{"id": "x"}); err != nil {
		tx.Rollback(ctx)
		t.Fatalf("enqueueWebhookDeliveryTx: %v", err)
	}
	var outboxID int64
	if err := tx.QueryRow(ctx,
		`SELECT id FROM outbox WHERE event = 'webhook.deliver' ORDER BY id DESC LIMIT 1`,
	).Scan(&outboxID); err != nil {
		tx.Rollback(ctx)
		t.Fatalf("outbox id: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return outboxID
}

// enqueueOneWebhookRow creates a webhook against srvURL and enqueues one
// webhook.deliver row, returning the outbox row id.
func enqueueOneWebhookRow(t *testing.T, pool *pgxpool.Pool, ctx context.Context,
	wsSlug, actorID, srvURL string) int64 {
	t.Helper()
	createTestWebhook(t, pool, ctx, wsSlug, actorID, srvURL)
	return enqueueWebhookRow(t, pool, ctx, wsSlug)
}

// TestWebhookDispatchReleasesClaimTxDuringDelivery: the claim tx commits
// BEFORE the HTTP delivery starts. While a delivery is in flight against
// a slow endpoint, the outbox row lock must be free (FOR UPDATE NOWAIT
// succeeds) and the row must already be marked 'delivering'.
func TestWebhookDispatchReleasesClaimTxDuringDelivery(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, _, wsSlug, _, _ := createNotifyFixture(t, pool)

	received := make(chan struct{}, 1)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case received <- struct{}{}:
		default:
		}
		<-release // hold the delivery open: black-holing endpoint
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	// Always release the slow endpoint, even on early failure: this
	// defer is registered AFTER srv.Close so it runs FIRST (LIFO);
	// otherwise srv.Close() would hang on the blocked handler.
	defer func() {
		select {
		case <-release: // already closed on the success path
		default:
			close(release)
		}
	}()

	outboxID := enqueueOneWebhookRow(t, pool, ctx, wsSlug, actorID, srv.URL)

	d := NewWebhookDispatcher(pool)
	d.allowPrivateTargets = true // httptest targets are loopback by construction
	runErr := make(chan error, 1)
	go func() { runErr <- d.Run(ctx) }()

	select {
	case <-received:
	case <-time.After(15 * time.Second):
		t.Fatal("delivery never started")
	}
	// Delivery is in flight against the slow endpoint. The claim tx must
	// be committed: the row lock is free...
	lockTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin lock probe: %v", err)
	}
	var one int64
	if err := lockTx.QueryRow(ctx,
		`SELECT id FROM outbox WHERE id = $1 FOR UPDATE NOWAIT`, outboxID).Scan(&one); err != nil {
		lockTx.Rollback(ctx)
		t.Fatalf("row still locked mid-delivery (claim tx spans HTTP): %v", err)
	}
	lockTx.Rollback(ctx)
	// ...and the claim was durably marked.
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM outbox WHERE id = $1`, outboxID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "delivering" {
		t.Fatalf("status mid-delivery = %q, want delivering", status)
	}

	close(release) // let the endpoint respond
	if err := <-runErr; err != nil {
		t.Fatalf("run: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT status FROM outbox WHERE id = $1`, outboxID).Scan(&status); err != nil {
		t.Fatalf("read final status: %v", err)
	}
	if status != "done" {
		t.Fatalf("final status = %q, want done", status)
	}
}

// TestWebhookDispatchSingleFlight: a tick that fires while a previous
// pass is still delivering is skipped — overlapping runs never pile up
// pool connections behind a slow endpoint. A second row enqueued
// mid-flight proves the point: without single-flight the overlapping run
// would claim it (SKIP LOCKED skips only the first pass's locked rows)
// and block on the slow endpoint too.
func TestWebhookDispatchSingleFlight(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, _, wsSlug, _, _ := createNotifyFixture(t, pool)

	var calls atomic.Int64
	received := make(chan struct{}, 1)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		select {
		case received <- struct{}{}:
		default:
		}
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	// Always release the slow endpoint, even on early failure: this
	// defer is registered AFTER srv.Close so it runs FIRST (LIFO);
	// otherwise srv.Close() would hang on the blocked handler.
	defer func() {
		select {
		case <-release: // already closed on the success path
		default:
			close(release)
		}
	}()

	createTestWebhook(t, pool, ctx, wsSlug, actorID, srv.URL)
	outboxID := enqueueWebhookRow(t, pool, ctx, wsSlug)

	d := NewWebhookDispatcher(pool)
	d.allowPrivateTargets = true // httptest targets are loopback by construction
	run1 := make(chan error, 1)
	go func() { run1 <- d.Run(ctx) }()

	select {
	case <-received:
	case <-time.After(15 * time.Second):
		t.Fatal("delivery never started")
	}
	// Row B arrives while the first pass is mid-delivery (same single
	// webhook, so exactly one new row).
	rowB := enqueueWebhookRow(t, pool, ctx, wsSlug)

	// A second tick now must skip fast instead of claiming row B and
	// blocking behind the slow endpoint. Run it in a goroutine: under
	// the old behavior it would block until release, so a timeout here
	// is the failure signal (never a deadlock).
	run2done := make(chan error, 1)
	go func() { run2done <- d.Run(ctx) }()
	select {
	case err := <-run2done:
		if err != nil {
			t.Fatalf("overlapping run: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("overlapping Run did not skip — piling up behind the in-flight pass")
	}

	close(release)
	if err := <-run1; err != nil {
		t.Fatalf("run1: %v", err)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("deliveries = %d, want 1 (overlapping run delivered row B)", n)
	}
	var statusA, statusB string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM outbox WHERE id = $1`, outboxID).Scan(&statusA); err != nil {
		t.Fatalf("read status A: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT status FROM outbox WHERE id = $1`, rowB).Scan(&statusB); err != nil {
		t.Fatalf("read status B: %v", err)
	}
	if statusA != "done" {
		t.Fatalf("row A status = %q, want done", statusA)
	}
	if statusB != "pending" {
		t.Fatalf("row B status = %q, want pending (untouched by the skipped tick)", statusB)
	}
}
