package service

// Slack incoming-webhook notifications (C9T3): workspace-level Slack
// delivery for issue created / state changed / commented / @mentioned.
//
// Persistence choice: glance has no workspace settings table or KV
// mechanism (checked 2026-10-10 — migrations 000001–000033 contain
// none), so the URL lives as a single TEXT column,
// workspaces.slack_webhook_url (migration 000034). One value, no join,
// and it inherits the workspace row's tenancy naturally. NULL = not
// configured.
//
// SSRF control: the URL is validated at WRITE time — it must be an
// https://hooks.slack.com/ URL (exact hostname match after parsing, so
// lookalikes like hooks.slack.com.evil.com fail). Slack incoming
// webhooks only ever live under that host, so the allowlist is exact.
// Unlike the generic webhook dispatcher (which accepts arbitrary
// admin-provided URLs and therefore guards at delivery time with
// DNS resolution + dial pinning), the Slack dispatcher needs no
// delivery-time guard: it only ever POSTs to a URL that passed this
// write-time allowlist.
//
// Delivery rides the shared transactional outbox as "slack.deliver"
// rows, enqueued inside the mutation's tx next to the generic webhook
// fan-out — enqueue, never inline, so a down Slack endpoint can never
// 500 the request path. SlackDispatcher claims the "slack.%" namespace
// on its own ticker in main (like the mail and webhook dispatchers, so
// a slow Slack endpoint starves neither).
//
// The URL is a secret: it is never serialized into API responses (GET
// and PATCH return slack_configured, a boolean), never logged, and
// never echoed in errors. Tests use fake URLs / stub servers.
//
// Deliberately out of scope (speculative-free): no Slack OAuth app, no
// interactive message buttons, no slash commands — this is an incoming
// webhook, i.e. one-way POSTs from glance to Slack.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"glance/internal/mail"
)

// SlackDeliverEvent is the outbox event the Slack dispatcher claims.
const SlackDeliverEvent = "slack.deliver"

var (
	// ErrBadSlackWebhookURL is returned when the URL is not an
	// https://hooks.slack.com/ URL. This is the SSRF control — anything
	// else is rejected at write time.
	ErrBadSlackWebhookURL = errors.New("service: slack webhook url must be an https://hooks.slack.com/ URL")
	// ErrSlackNotConfigured is returned by SendSlackTest when the
	// workspace has no webhook URL set.
	ErrSlackNotConfigured = errors.New("service: slack webhook not configured")
)

// validateSlackWebhookURL enforces the write-time SSRF allowlist. The
// URL is parsed and the scheme + hostname are compared exactly — a
// naive strings.HasPrefix on the raw URL would accept
// "https://hooks.slack.com.evil.com/..." and userinfo tricks like
// "https://hooks.slack.com@evil.com/...".
func validateSlackWebhookURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" {
		return ErrBadSlackWebhookURL
	}
	if !strings.EqualFold(u.Scheme, "https") || !strings.EqualFold(u.Hostname(), "hooks.slack.com") {
		return ErrBadSlackWebhookURL
	}
	return nil
}

// SetWorkspaceSlackURL sets or clears the workspace's Slack
// incoming-webhook URL. Admin (role 20) only; non-members get
// ErrNotFound (same tenancy hygiene as the rest of the codebase).
// url nil (or empty) clears the configuration. The URL is validated
// before anything is written. Returns the workspace with the updated
// SlackConfigured flag — never the URL itself.
func SetWorkspaceSlackURL(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID string, raw *string) (*Workspace, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var ws Workspace
	var role int
	err = tx.QueryRow(ctx,
		`SELECT w.id::text, w.slug::text, w.name, w.created_at, w.updated_at,
		        w.slack_webhook_url IS NOT NULL, m.role
		   FROM workspaces w
		   JOIN workspace_members m ON m.workspace_id = w.id
		  WHERE w.slug = $1 AND m.user_id = $2::uuid`,
		wsSlug, actorID).Scan(&ws.ID, &ws.Slug, &ws.Name, &ws.CreatedAt, &ws.UpdatedAt, &ws.SlackConfigured, &role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if role != RoleAdmin {
		return nil, ErrForbidden
	}

	var newURL any // nil interface → SQL NULL (clears the column)
	if raw != nil {
		u := strings.TrimSpace(*raw)
		if u != "" {
			if err := validateSlackWebhookURL(u); err != nil {
				return nil, err
			}
			newURL = u
		}
	}
	if err := tx.QueryRow(ctx,
		`UPDATE workspaces SET slack_webhook_url = $1, updated_at = now()
		  WHERE id = $2::uuid
		  RETURNING slack_webhook_url IS NOT NULL`,
		newURL, ws.ID).Scan(&ws.SlackConfigured); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &ws, nil
}

// SendSlackTest enqueues a probe message to the workspace's Slack
// webhook. Admin only. ErrSlackNotConfigured when no URL is set — the
// frontend surfaces that as "configure the webhook first".
func SendSlackTest(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var wsID string
	var role int
	var webhookURL *string
	err = tx.QueryRow(ctx,
		`SELECT w.id::text, m.role, w.slack_webhook_url
		   FROM workspaces w
		   JOIN workspace_members m ON m.workspace_id = w.id
		  WHERE w.slug = $1 AND m.user_id = $2::uuid`,
		wsSlug, actorID).Scan(&wsID, &role, &webhookURL)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if role != RoleAdmin {
		return ErrForbidden
	}
	u := ""
	if webhookURL != nil {
		u = strings.TrimSpace(*webhookURL)
	}
	if u == "" {
		return ErrSlackNotConfigured
	}
	if err := mail.Enqueue(ctx, tx, SlackDeliverEvent, slackDelivery{
		URL:  u,
		Text: ":test_tube: Glance → Slack test message: workspace notifications are wired up.",
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// enqueueSlackDeliveryTx enqueues one "slack.deliver" outbox row inside
// the caller's tx — the transactional outbox guarantee: no delivery
// without a committed mutation, and the mutation never dials Slack
// inline. A workspace without a configured URL enqueues nothing.
func enqueueSlackDeliveryTx(ctx context.Context, tx pgx.Tx, wsID, text string) error {
	var webhookURL *string
	if err := tx.QueryRow(ctx,
		`SELECT slack_webhook_url FROM workspaces WHERE id = $1::uuid`,
		wsID).Scan(&webhookURL); err != nil {
		return err
	}
	if webhookURL == nil {
		return nil
	}
	u := strings.TrimSpace(*webhookURL)
	if u == "" {
		return nil
	}
	return mail.Enqueue(ctx, tx, SlackDeliverEvent, slackDelivery{URL: u, Text: text})
}

// ---------- compact message builders ----------

// slackCompact collapses whitespace (comments may span lines) and caps
// the length so one event is one short Slack message.
func slackCompact(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}

func slackIssueCreatedText(displayID, name, actorName string) string {
	return fmt.Sprintf(":new: *%s* %s\nCreated by %s",
		displayID, slackCompact(name, 120), actorName)
}

func slackStateChangedText(displayID, name, stateName, actorName string) string {
	return fmt.Sprintf(":arrows_counterclockwise: *%s* %s — moved to *%s* by %s",
		displayID, slackCompact(name, 120), stateName, actorName)
}

func slackCommentText(displayID, actorName, commentText string) string {
	return fmt.Sprintf(":speech_balloon: *%s* — %s commented: %s",
		displayID, actorName, slackCompact(commentText, 160))
}

func slackMentionText(displayID, actorName string) string {
	return fmt.Sprintf(":bell: *%s* — %s mentioned you", displayID, actorName)
}

// ---------- dispatcher ----------

const (
	// slackEventPrefix scopes this dispatcher to Slack rows; the mail
	// ("email.%") and webhook ("webhook.%") dispatchers never see them.
	slackEventPrefix = "slack."
	// slackBatchSize caps how many rows one Run claims.
	slackBatchSize = 25
	// slackMaxAttempts: after this many failed attempts the row is
	// marked failed instead of retried forever.
	slackMaxAttempts = 8
	// slackBackoffBase is the delay before the first retry; it doubles
	// per attempt.
	slackBackoffBase = 5 * time.Second
	// slackBackoffMax caps the retry delay.
	slackBackoffMax = 5 * time.Minute
	// slackHTTPTimeout bounds a single delivery attempt (spec: 5s) so
	// one slow endpoint cannot stall the whole pass.
	slackHTTPTimeout = 5 * time.Second
	// slackClaimLease bounds how long a claimed row may sit 'delivering'
	// before another pass reclaims it (crashed-pass recovery).
	slackClaimLease = 5 * time.Minute
)

// slackDelivery is the outbox payload for SlackDeliverEvent.
type slackDelivery struct {
	URL  string `json:"url"`
	Text string `json:"text"`
}

// SlackDispatcher delivers due "slack.%" outbox rows. Run performs a
// single pass; the long-running ticker loop belongs to main (its own
// goroutine, so Slack HTTP latency never starves the mail or webhook
// dispatchers). The claim/deliver/backoff machinery mirrors
// WebhookDispatcher; the payload is Slack's {"text": ...} shape with no
// signature headers (incoming webhooks don't verify signatures).
type SlackDispatcher struct {
	pool   *pgxpool.Pool
	client *http.Client
	// inFlight is the single-flight guard: a tick that fires while a
	// previous pass is still delivering skips instead of piling up
	// another claim + another long-lived pass.
	inFlight atomic.Bool
}

// NewSlackDispatcher builds a dispatcher with a 5s per-attempt timeout.
func NewSlackDispatcher(pool *pgxpool.Pool) *SlackDispatcher {
	return &SlackDispatcher{
		pool: pool,
		client: &http.Client{
			Timeout:       slackHTTPTimeout,
			CheckRedirect: noRedirect, // same as webhooks: never follow redirects
		},
	}
}

type slackOutboxRow struct {
	id       int64
	payload  []byte
	attempts int
}

// Run performs one dispatch pass: claim due rows, commit the claim,
// deliver each row OUTSIDE any transaction, and record each outcome with
// a short single-statement write. Delivery failures are recorded with
// backoff — Run returns nil unless the database itself fails, so a down
// Slack endpoint never surfaces as an error to the ticker (and, by
// construction, never to a request path, which only ever enqueues).
func (d *SlackDispatcher) Run(ctx context.Context) error {
	if !d.inFlight.CompareAndSwap(false, true) {
		return nil // previous pass still in flight; skip this tick
	}
	defer d.inFlight.Store(false)

	claimed, err := d.claimDue(ctx)
	if err != nil {
		return err
	}
	var firstErr error
	for _, r := range claimed {
		if err := d.deliverRow(ctx, r); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// claimDue claims due Slack rows inside ONE short tx: rows are locked
// (FOR UPDATE SKIP LOCKED — safe across dispatcher instances), marked
// 'delivering' with a lease, and the tx commits BEFORE any HTTP happens.
func (d *SlackDispatcher) claimDue(ctx context.Context) ([]slackOutboxRow, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("slack: begin claim tx: %w", err)
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
		SELECT id, payload, attempts
		FROM outbox
		WHERE event LIKE $2
		  AND next_retry_at <= now()
		  AND (status = 'pending' OR status = 'delivering')
		ORDER BY id
		LIMIT $1
		FOR UPDATE SKIP LOCKED`, slackBatchSize, slackEventPrefix+"%")
	if err != nil {
		return nil, fmt.Errorf("slack: claim due rows: %w", err)
	}
	var claimed []slackOutboxRow
	for rows.Next() {
		var r slackOutboxRow
		if err := rows.Scan(&r.id, &r.payload, &r.attempts); err != nil {
			rows.Close()
			return nil, fmt.Errorf("slack: scan claimed row: %w", err)
		}
		claimed = append(claimed, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("slack: iterate claimed rows: %w", err)
	}

	if len(claimed) > 0 {
		ids := make([]int64, len(claimed))
		for i, r := range claimed {
			ids[i] = r.id
		}
		// Mark 'delivering' with a lease: next_retry_at doubles as the
		// lease expiry for delivering rows (recovery predicate above).
		if _, err := tx.Exec(ctx,
			`UPDATE outbox SET status = 'delivering', next_retry_at = $2
			  WHERE id = ANY($1)`,
			ids, time.Now().Add(slackClaimLease)); err != nil {
			return nil, fmt.Errorf("slack: mark rows delivering: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("slack: commit claim tx: %w", err)
	}
	return claimed, nil
}

// deliverRow POSTs one claimed row and records the outcome with short
// single-statement writes — no transaction spans the HTTP call. The URL
// is never logged: on failure only the status code / error is recorded.
func (d *SlackDispatcher) deliverRow(ctx context.Context, r slackOutboxRow) error {
	var sd slackDelivery
	if err := json.Unmarshal(r.payload, &sd); err != nil {
		// Poison row: never decodes, fail permanently.
		return d.markFailed(ctx, r.id, r.attempts, fmt.Errorf("slack: bad payload: %w", err))
	}
	if sd.URL == "" || sd.Text == "" {
		return d.markFailed(ctx, r.id, r.attempts, errors.New("slack: missing url or text"))
	}

	body, err := json.Marshal(map[string]any{"text": sd.Text})
	if err != nil {
		return d.markFailed(ctx, r.id, r.attempts, fmt.Errorf("slack: marshal body: %w", err))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sd.URL, bytes.NewReader(body))
	if err != nil {
		return d.markFailed(ctx, r.id, r.attempts, fmt.Errorf("slack: build request: %w", err))
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "glance-slack/1")

	resp, err := d.client.Do(req)
	if err != nil {
		return d.recordFailure(ctx, r.id, r.attempts, err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return d.recordFailure(ctx, r.id, r.attempts,
			fmt.Errorf("slack: delivery returned status %d", resp.StatusCode))
	}
	return d.markDone(ctx, r.id)
}

// Outcome writes are single statements on the pool (short-lived, no tx
// spans the HTTP call). A failed outcome write leaves the row
// 'delivering' with its lease — a later pass reclaims and redelivers
// (at-least-once is preserved).
func (d *SlackDispatcher) markDone(ctx context.Context, id int64) error {
	if _, err := d.pool.Exec(ctx,
		`UPDATE outbox SET status = 'done', processed_at = now() WHERE id = $1`, id); err != nil {
		return fmt.Errorf("slack: mark row %d done: %w", id, err)
	}
	return nil
}

// recordFailure bumps attempts; the row goes back to pending with
// backoff, or is marked failed once it exhausts slackMaxAttempts.
func (d *SlackDispatcher) recordFailure(ctx context.Context, id int64, attempts int, sendErr error) error {
	attempts++
	if attempts >= slackMaxAttempts {
		return d.markFailed(ctx, id, attempts, sendErr)
	}
	retryAt := time.Now().Add(slackRetryBackoff(attempts))
	if _, err := d.pool.Exec(ctx, `
		UPDATE outbox
		SET status = 'pending', attempts = $2, last_error = $3, next_retry_at = $4
		WHERE id = $1`, id, attempts, sendErr.Error(), retryAt); err != nil {
		return fmt.Errorf("slack: record failure for row %d: %w", id, err)
	}
	return nil
}

func (d *SlackDispatcher) markFailed(ctx context.Context, id int64, attempts int, sendErr error) error {
	if _, err := d.pool.Exec(ctx, `
		UPDATE outbox
		SET status = 'failed', attempts = $2, last_error = $3,
		    processed_at = now()
		WHERE id = $1`, id, attempts, sendErr.Error()); err != nil {
		return fmt.Errorf("slack: mark row %d failed: %w", id, err)
	}
	return nil
}

// slackRetryBackoff doubles from slackBackoffBase, capped at
// slackBackoffMax; the overflow guard keeps huge counts from wrapping.
func slackRetryBackoff(attempts int) time.Duration {
	d := slackBackoffBase << (attempts - 1)
	if d <= 0 || d > slackBackoffMax {
		return slackBackoffMax
	}
	return d
}
