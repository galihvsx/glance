package service

// Slack incoming-webhook notifications (C9T3): write-time URL validation
// (the SSRF control), admin-only write, transactional enqueue, and the
// slack.deliver dispatcher. Real test database + stub HTTP servers; no
// skips. Harness from workspace_test.go / notify_test.go.
//
// NOTE: the write path only accepts https://hooks.slack.com/ URLs, so
// dispatcher tests set workspaces.slack_webhook_url directly via SQL to
// point at the httptest stub. The dispatcher never re-validates the
// prefix — write-time validation is the SSRF control (documented in
// slack.go).

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// slackCapture is a stub Slack endpoint: records every POST body, can be
// flipped to 500 to simulate a down endpoint.
type slackCapture struct {
	srv    *httptest.Server
	calls  atomic.Int64
	fail   atomic.Bool
	mu     sync.Mutex
	bodies []string
}

func newSlackCapture(t *testing.T) *slackCapture {
	t.Helper()
	c := &slackCapture{}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.bodies = append(c.bodies, string(body))
		c.mu.Unlock()
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		if c.fail.Load() {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(c.srv.Close)
	return c
}

// setSlackURLRaw points the workspace's slack_webhook_url at raw, bypassing
// write-time validation (tests only — see the package note above).
func setSlackURLRaw(t *testing.T, pool *pgxpool.Pool, wsSlug, raw string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE workspaces SET slack_webhook_url = $1 WHERE slug = $2`, raw, wsSlug); err != nil {
		t.Fatalf("set slack url: %v", err)
	}
}

// slackTexts returns the "text" field of every captured Slack POST body.
func (c *slackCapture) slackTexts(t *testing.T) []string {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, b := range c.bodies {
		var v struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(b), &v); err != nil {
			t.Fatalf("unmarshal slack body %q: %v", b, err)
		}
		if v.Text == "" {
			t.Fatalf("slack body has empty text: %q", b)
		}
		out = append(out, v.Text)
	}
	return out
}

// drainSlackOutbox runs the dispatcher until no due slack.deliver rows
// remain (a safety bound keeps a stuck row from hanging the test).
func drainSlackOutbox(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	d := NewSlackDispatcher(pool)
	for i := 0; i < 10; i++ {
		if err := d.Run(context.Background()); err != nil {
			t.Fatalf("dispatcher run: %v", err)
		}
		var n int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM outbox
			  WHERE event LIKE 'slack.%' AND status IN ('pending','delivering')
			    AND next_retry_at <= now()`).Scan(&n); err != nil {
			t.Fatalf("count slack outbox: %v", err)
		}
		if n == 0 {
			return
		}
		// Force any backoff rows due so the next pass claims them.
		if _, err := pool.Exec(context.Background(),
			`UPDATE outbox SET next_retry_at = now()
			  WHERE event LIKE 'slack.%' AND status = 'pending'`); err != nil {
			t.Fatalf("force due: %v", err)
		}
	}
	t.Fatal("slack outbox did not drain")
}

// TestValidateSlackWebhookURL pins the write-time SSRF control: only
// https://hooks.slack.com/ URLs pass. Lookalike hosts are rejected — a
// naive string-prefix check would accept hooks.slack.com.evil.com.
func TestValidateSlackWebhookURL(t *testing.T) {
	bad := []string{
		"",
		"   ",
		"not a url",
		"http://hooks.slack.com/services/T/B/X", // http, not https
		"https://evil.com/services/T/B/X",       // wrong host
		"https://hooks.slack.com.evil.com/x",    // lookalike host
		"https://evilhoooks.slack.com/x",        // lookalike host
		"https://hooks.slack.com@evil.com/x",    // userinfo trick
		"ftp://hooks.slack.com/x",               // wrong scheme
		"//hooks.slack.com/x",                   // scheme-relative
	}
	for _, raw := range bad {
		if err := validateSlackWebhookURL(raw); !errors.Is(err, ErrBadSlackWebhookURL) {
			t.Errorf("validateSlackWebhookURL(%q) = %v, want ErrBadSlackWebhookURL", raw, err)
		}
	}
	good := []string{
		"https://hooks.slack.com/services/FAKEW0RKSPAC/FAKECHANNELI/FAKESIGNINGSECRE", // obviously-fake fixture shape
		"https://hooks.slack.com/services/T/B/X",
		"  https://hooks.slack.com/services/T/B/X  ", // surrounding whitespace trimmed
	}
	for _, raw := range good {
		if err := validateSlackWebhookURL(raw); err != nil {
			t.Errorf("validateSlackWebhookURL(%q) = %v, want nil", raw, err)
		}
	}
}

// TestSetSlackWebhookURLAdminOnly: the write path is admin-only; members
// get ErrForbidden, non-members ErrNotFound (no existence leak).
func TestSetSlackWebhookURLAdminOnly(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, memberID, wsSlug, _, _ := createNotifyFixture(t, pool)
	// createNotifyFixture already added memberID as a member (role 15).
	_ = memberID

	url := "https://hooks.slack.com/services/T/B/X"
	if _, err := SetWorkspaceSlackURL(ctx, pool, wsSlug, memberID, &url); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member set = %v, want ErrForbidden", err)
	}
	outsider := createTestUser(t, pool, uniqueTestEmail("slack-outsider"))
	if _, err := SetWorkspaceSlackURL(ctx, pool, wsSlug, outsider, &url); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider set = %v, want ErrNotFound", err)
	}
	// Admin write succeeds and the flag flips on.
	ws, err := SetWorkspaceSlackURL(ctx, pool, wsSlug, actorID, &url)
	if err != nil {
		t.Fatalf("admin set: %v", err)
	}
	if !ws.SlackConfigured {
		t.Fatal("SlackConfigured = false after set, want true")
	}
	// Bad URL → 400-class sentinel, and the previous value is untouched.
	if _, err := SetWorkspaceSlackURL(ctx, pool, wsSlug, actorID, strPtr2("https://evil.example/hook")); !errors.Is(err, ErrBadSlackWebhookURL) {
		t.Fatalf("bad url set = %v, want ErrBadSlackWebhookURL", err)
	}
	got, _, err := GetWorkspace(ctx, pool, wsSlug, actorID)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	if !got.SlackConfigured {
		t.Fatal("SlackConfigured flipped by a rejected write")
	}
	// Clear with nil.
	ws, err = SetWorkspaceSlackURL(ctx, pool, wsSlug, actorID, nil)
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if ws.SlackConfigured {
		t.Fatal("SlackConfigured = true after clear, want false")
	}
}

// TestSlackWebhookURLNeverSerialized: the secret URL must not appear in
// GET or PATCH responses — only the boolean flag.
func TestSlackWebhookURLNeverSerialized(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, _, wsSlug, _, _ := createNotifyFixture(t, pool)

	url := "https://hooks.slack.com/services/T/B/SECRETSECRET"
	ws, err := SetWorkspaceSlackURL(ctx, pool, wsSlug, actorID, &url)
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	raw, err := json.Marshal(ws)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "SECRETSECRET") {
		t.Fatalf("PATCH response leaks the webhook URL: %s", raw)
	}
	got, _, err := GetWorkspace(ctx, pool, wsSlug, actorID)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	raw, err = json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "SECRETSECRET") {
		t.Fatalf("GET response leaks the webhook URL: %s", raw)
	}
	if !strings.Contains(string(raw), `"slack_configured":true`) {
		t.Fatalf("GET response missing slack_configured flag: %s", raw)
	}
}

// TestSlackIssueCreatedDelivers is the event → POST pin: creating an issue
// in a workspace with a Slack URL configured enqueues a slack.deliver row
// (in-tx, never inline), and the dispatcher POSTs a compact message.
func TestSlackIssueCreatedDelivers(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, _, wsSlug, ident, _ := createNotifyFixture(t, pool)

	cap := newSlackCapture(t)
	setSlackURLRaw(t, pool, wsSlug, cap.srv.URL+"/hook")

	iss := createTestIssue(t, pool, wsSlug, ident, actorID, "Fix the login redirect")
	drainSlackOutbox(t, pool)

	if cap.calls.Load() != 1 {
		t.Fatalf("slack POSTs = %d, want 1", cap.calls.Load())
	}
	texts := cap.slackTexts(t)
	if !strings.Contains(texts[0], iss.DisplayID) {
		t.Fatalf("slack text %q does not mention display id %q", texts[0], iss.DisplayID)
	}
	if !strings.Contains(texts[0], "Fix the login redirect") {
		t.Fatalf("slack text %q does not mention the issue name", texts[0])
	}
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM outbox WHERE event = 'slack.deliver' ORDER BY id DESC LIMIT 1`,
	).Scan(&status); err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	if status != "done" {
		t.Fatalf("outbox status = %q, want done", status)
	}
}

// TestSlackFailureDoesNotBlockMutation: a down Slack endpoint must not 500
// the request path. The mutation only enqueues (never dials inline); the
// dispatcher records the failure with backoff and Run itself returns nil.
func TestSlackFailureDoesNotBlockMutation(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, _, wsSlug, ident, _ := createNotifyFixture(t, pool)

	cap := newSlackCapture(t)
	cap.fail.Store(true) // Slack is down from the start
	setSlackURLRaw(t, pool, wsSlug, cap.srv.URL+"/hook")

	// The mutation must succeed even though every delivery will fail.
	if _, err := CreateIssue(ctx, pool, wsSlug, ident, actorID, CreateIssueInput{Name: "Survives Slack outage"}); err != nil {
		t.Fatalf("CreateIssue with Slack down: %v", err)
	}

	d := NewSlackDispatcher(pool)
	if err := d.Run(ctx); err != nil {
		t.Fatalf("dispatcher Run with 500 endpoint = %v, want nil (failures are recorded, not returned)", err)
	}
	if cap.calls.Load() != 1 {
		t.Fatalf("delivery attempts = %d, want 1", cap.calls.Load())
	}
	var status string
	var attempts int
	if err := pool.QueryRow(ctx,
		`SELECT status, attempts FROM outbox WHERE event = 'slack.deliver' ORDER BY id DESC LIMIT 1`,
	).Scan(&status, &attempts); err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	if status != "pending" || attempts != 1 {
		t.Fatalf("status/attempts = %q/%d, want pending/1", status, attempts)
	}
}

// TestSlackStateChangeDelivers: moving an issue to another state posts a
// compact state-change message.
func TestSlackStateChangeDelivers(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, _, wsSlug, ident, issueID := createNotifyFixture(t, pool)

	cap := newSlackCapture(t)
	setSlackURLRaw(t, pool, wsSlug, cap.srv.URL+"/hook")

	states, err := ListStates(ctx, pool, wsSlug, ident, actorID)
	if err != nil {
		t.Fatalf("ListStates: %v", err)
	}
	var target string
	var targetName string
	for _, s := range states {
		if s.Group == "started" {
			target, targetName = s.ID, s.Name
		}
	}
	if target == "" {
		t.Fatal("no started-group state seeded")
	}
	if _, err := UpdateIssue(ctx, pool, wsSlug, ident, issueID, actorID, IssuePatch{StateID: &target}); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	drainSlackOutbox(t, pool)

	// One POST for the state change (the fixture's createTestIssue ran
	// before the URL was set, so no created-event POST).
	if cap.calls.Load() != 1 {
		t.Fatalf("slack POSTs = %d, want 1 (state change only)", cap.calls.Load())
	}
	text := cap.slackTexts(t)[0]
	if !strings.Contains(text, targetName) {
		t.Fatalf("slack text %q does not mention the new state %q", text, targetName)
	}
}

// TestSlackCommentAndMentionDeliver: a plain comment posts one message; a
// comment with an @-mention posts two (commented + mentioned).
func TestSlackCommentAndMentionDeliver(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, mentionedID, wsSlug, ident, issueID := createNotifyFixture(t, pool)

	// Give the mentioned member a resolvable display name.
	if _, err := pool.Exec(ctx, `UPDATE users SET name = 'Slack Buddy' WHERE id = $1::uuid`, mentionedID); err != nil {
		t.Fatalf("set name: %v", err)
	}

	cap := newSlackCapture(t)
	setSlackURLRaw(t, pool, wsSlug, cap.srv.URL+"/hook")

	tiptap := func(text string) json.RawMessage {
		doc := map[string]any{
			"type": "doc",
			"content": []any{
				map[string]any{"type": "paragraph", "content": []any{
					map[string]any{"type": "text", "text": text},
				}},
			},
		}
		raw, err := json.Marshal(doc)
		if err != nil {
			t.Fatalf("marshal tiptap: %v", err)
		}
		return raw
	}

	if _, err := CreateComment(ctx, pool, wsSlug, ident, issueID, actorID, tiptap("plain comment, no mention"), nil); err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	if _, err := CreateComment(ctx, pool, wsSlug, ident, issueID, actorID, tiptap("@Slack Buddy please review"), nil); err != nil {
		t.Fatalf("CreateComment with mention: %v", err)
	}
	drainSlackOutbox(t, pool)

	// 1 (comment) + 2 (comment + mention) = 3 POSTs.
	if cap.calls.Load() != 3 {
		t.Fatalf("slack POSTs = %d, want 3 (comment, comment, mention)", cap.calls.Load())
	}
	texts := cap.slackTexts(t)
	joined := strings.Join(texts, "\n")
	if !strings.Contains(strings.ToLower(joined), "mention") {
		t.Fatalf("no mention message among %q", texts)
	}
}

// TestSlackNoURLNoDelivery: without a configured URL, events enqueue
// nothing — Slack stays silent and costs nothing.
func TestSlackNoURLNoDelivery(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, _, wsSlug, ident, _ := createNotifyFixture(t, pool)

	// Watermark the shared outbox before the mutation under test.
	var watermark int64
	if err := pool.QueryRow(ctx, `SELECT COALESCE(max(id), 0) FROM outbox`).Scan(&watermark); err != nil {
		t.Fatalf("watermark outbox: %v", err)
	}

	createTestIssue(t, pool, wsSlug, ident, actorID, "Silent issue")
	// The test DB is shared and never truncated: compare against a
	// watermark taken before the mutation, not an absolute count.
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM outbox WHERE event LIKE 'slack.%' AND id > $1`, watermark).Scan(&n); err != nil {
		t.Fatalf("count slack outbox: %v", err)
	}
	if n != 0 {
		t.Fatalf("slack outbox rows = %d, want 0 (no URL configured)", n)
	}
}

// TestSendSlackTest: the test-message endpoint helper enqueues a probe
// message; without a URL it fails fast with ErrSlackNotConfigured.
func TestSendSlackTest(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, _, wsSlug, _, _ := createNotifyFixture(t, pool)

	if err := SendSlackTest(ctx, pool, wsSlug, actorID); !errors.Is(err, ErrSlackNotConfigured) {
		t.Fatalf("SendSlackTest without URL = %v, want ErrSlackNotConfigured", err)
	}

	cap := newSlackCapture(t)
	setSlackURLRaw(t, pool, wsSlug, cap.srv.URL+"/hook")
	if err := SendSlackTest(ctx, pool, wsSlug, actorID); err != nil {
		t.Fatalf("SendSlackTest: %v", err)
	}
	drainSlackOutbox(t, pool)
	if cap.calls.Load() != 1 {
		t.Fatalf("slack POSTs = %d, want 1 (test message)", cap.calls.Load())
	}

	// Non-admins cannot fire test messages.
	memberID := createTestUser(t, pool, uniqueTestEmail("slack-member"))
	if _, err := pool.Exec(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role)
		 SELECT id, $2::uuid, $3 FROM workspaces WHERE slug = $1`,
		wsSlug, memberID, RoleMember); err != nil {
		t.Fatalf("add member: %v", err)
	}
	if err := SendSlackTest(ctx, pool, wsSlug, memberID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member SendSlackTest = %v, want ErrForbidden", err)
	}
}
