package service

// Webhooks (Task 26): workspace-level HTTP delivery targets plus the outbox
// dispatcher that POSTs domain events to them.
//
// CRUD is workspace-admin-only (role 20): webhooks exfiltrate workspace
// data to third parties and hold signing secrets — members and guests get
// ErrForbidden (documented choice; the spec leaves the role open).
//
// Delivery goes through the shared outbox as "webhook.deliver" rows,
// enqueued inside the mutation's tx (enqueueWebhookDeliveryTx) so no event
// is lost when the mutation commits. The WebhookDispatcher claims only the
// "webhook.%" namespace — mail delivery is untouched, and the dispatcher
// runs on its own ticker in main so slow webhook endpoints can never starve
// the mail pass.

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"glance/internal/mail"
)

// WebhookDeliverEvent is the outbox event the dispatcher claims.
const WebhookDeliverEvent = "webhook.deliver"

// WebhookDomainEvents is the closed set of domain events webhooks can
// subscribe to. A webhook's events list must be a subset of this.
var WebhookDomainEvents = []string{
	EventIssueCreated,
	EventIssueUpdated,
	EventIssueDeleted,
	EventCommentCreated,
}

var (
	// ErrWebhookNotFound is returned for unknown webhook ids (scoped to
	// the workspace — no cross-workspace existence leak).
	ErrWebhookNotFound = errors.New("service: webhook not found")
	// ErrBadWebhookURL is returned when the target URL is missing, not
	// http(s), or unparseable.
	ErrBadWebhookURL = errors.New("service: webhook url must be http(s)")
	// ErrUnknownWebhookEvent is returned when subscribing to an event
	// outside WebhookDomainEvents.
	ErrUnknownWebhookEvent = errors.New("service: unknown webhook event")
)

// Webhook is one workspace delivery target.
type Webhook struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	URL         string    `json:"url"`
	Secret      string    `json:"secret"`
	Events      []string  `json:"events"`
	Active      bool      `json:"active"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// WebhookInput carries webhook creation fields. Secret nil/empty → a random
// 32-byte hex secret is generated. Events empty → all domain events.
type WebhookInput struct {
	URL    string
	Secret *string
	Events []string
	Active *bool
}

// WebhookPatch carries partial webhook updates (nil = untouched).
type WebhookPatch struct {
	URL    *string
	Secret *string
	Events []string // nil = untouched; non-nil (even empty) replaces
	Active *bool
}

func scanWebhook(row pgx.Row) (*Webhook, error) {
	var w Webhook
	var events []string
	if err := row.Scan(&w.ID, &w.WorkspaceID, &w.URL, &w.Secret, &events,
		&w.Active, &w.CreatedAt, &w.UpdatedAt); err != nil {
		return nil, err
	}
	w.Events = events
	return &w, nil
}

const webhookColumns = `id::text, workspace_id::text, url, secret, events, active, created_at, updated_at`

func validateWebhookURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" {
		return ErrBadWebhookURL
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ErrBadWebhookURL
	}
	if u.Host == "" {
		return ErrBadWebhookURL
	}
	return nil
}

func validateWebhookEvents(events []string) error {
	for _, ev := range events {
		known := false
		for _, k := range WebhookDomainEvents {
			if ev == k {
				known = true
				break
			}
		}
		if !known {
			return fmt.Errorf("%w: %q", ErrUnknownWebhookEvent, ev)
		}
	}
	return nil
}

func randomWebhookSecret() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// resolveWebhookAdmin resolves the workspace and requires the admin role.
// Non-members get ErrNotFound (same tenancy hygiene as the rest of the
// codebase); members/guests get ErrForbidden.
func resolveWebhookAdmin(ctx context.Context, q queryRower, wsSlug, actorID string) (string, error) {
	wsID, role, err := workspaceIDForActor(q.QueryRow(ctx,
		`SELECT w.id::text, m.role
		   FROM workspaces w
		   JOIN workspace_members m ON m.workspace_id = w.id
		  WHERE w.slug = $1 AND m.user_id = $2::uuid`,
		wsSlug, actorID))
	if err != nil {
		return "", err
	}
	if role != RoleAdmin {
		return "", ErrForbidden
	}
	return wsID, nil
}

// CreateWebhook registers a delivery target. Workspace admin (20) only.
func CreateWebhook(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID string, in WebhookInput) (*Webhook, error) {
	if err := validateWebhookURL(in.URL); err != nil {
		return nil, err
	}
	if err := validateWebhookEvents(in.Events); err != nil {
		return nil, err
	}
	wsID, err := resolveWebhookAdmin(ctx, pool, wsSlug, actorID)
	if err != nil {
		return nil, err
	}
	secret := ""
	if in.Secret != nil {
		secret = *in.Secret
	}
	if secret == "" {
		secret, err = randomWebhookSecret()
		if err != nil {
			return nil, err
		}
	}
	active := true
	if in.Active != nil {
		active = *in.Active
	}
	events := in.Events
	if events == nil {
		events = []string{}
	}
	w, err := scanWebhook(pool.QueryRow(ctx,
		`INSERT INTO webhooks (workspace_id, url, secret, events, active)
		 VALUES ($1::uuid, $2, $3, $4, $5)
		 RETURNING `+webhookColumns,
		wsID, strings.TrimSpace(in.URL), secret, events, active))
	if err != nil {
		return nil, err
	}
	return w, nil
}

// ListWebhooks returns the workspace's webhooks, newest first. Admin only.
func ListWebhooks(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID string) ([]*Webhook, error) {
	wsID, err := resolveWebhookAdmin(ctx, pool, wsSlug, actorID)
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT `+webhookColumns+` FROM webhooks
		  WHERE workspace_id = $1::uuid ORDER BY created_at DESC`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Webhook{}
	for rows.Next() {
		w, err := scanWebhook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// GetWebhook returns one webhook. Admin only; scoped to the workspace.
func GetWebhook(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID, id string) (*Webhook, error) {
	wsID, err := resolveWebhookAdmin(ctx, pool, wsSlug, actorID)
	if err != nil {
		return nil, err
	}
	w, err := scanWebhook(pool.QueryRow(ctx,
		`SELECT `+webhookColumns+` FROM webhooks
		  WHERE id = $1::uuid AND workspace_id = $2::uuid`, id, wsID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil, ErrWebhookNotFound
		}
		return nil, err
	}
	return w, nil
}

// UpdateWebhook applies a partial update. Admin only.
func UpdateWebhook(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID, id string, patch WebhookPatch) (*Webhook, error) {
	wsID, err := resolveWebhookAdmin(ctx, pool, wsSlug, actorID)
	if err != nil {
		return nil, err
	}
	var sets []string
	var args []any
	add := func(column, cast string, arg any) {
		args = append(args, arg)
		sets = append(sets, fmt.Sprintf("%s = $%d%s", column, len(args)+2, cast))
	}
	if patch.URL != nil {
		if err := validateWebhookURL(*patch.URL); err != nil {
			return nil, err
		}
		add("url", "", strings.TrimSpace(*patch.URL))
	}
	if patch.Secret != nil {
		add("secret", "", *patch.Secret)
	}
	if patch.Events != nil {
		if err := validateWebhookEvents(patch.Events); err != nil {
			return nil, err
		}
		add("events", "", patch.Events)
	}
	if patch.Active != nil {
		add("active", "", *patch.Active)
	}
	if len(sets) == 0 {
		return nil, ErrNothingToUpdate
	}
	w, err := scanWebhook(pool.QueryRow(ctx,
		`UPDATE webhooks SET `+strings.Join(sets, ", ")+`, updated_at = now()
		  WHERE id = $1::uuid AND workspace_id = $2::uuid
		  RETURNING `+webhookColumns,
		append([]any{id, wsID}, args...)...))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil, ErrWebhookNotFound
		}
		return nil, err
	}
	return w, nil
}

// DeleteWebhook removes a webhook. Admin only; idempotent (missing id →
// ErrWebhookNotFound, not silent success — unlike unassign, deleting
// something that isn't there is a client bug worth surfacing).
func DeleteWebhook(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID, id string) error {
	wsID, err := resolveWebhookAdmin(ctx, pool, wsSlug, actorID)
	if err != nil {
		return err
	}
	tag, err := pool.Exec(ctx,
		`DELETE FROM webhooks WHERE id = $1::uuid AND workspace_id = $2::uuid`, id, wsID)
	if err != nil {
		if isInvalidUUID(err) {
			return ErrWebhookNotFound
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrWebhookNotFound
	}
	return nil
}

// enqueueWebhookDeliveryTx enqueues one "webhook.deliver" outbox row per
// active webhook subscribed to domainEvent, inside the caller's tx — the
// transactional outbox guarantee: no delivery without a committed mutation.
// A webhook with an empty events list receives every domain event.
func enqueueWebhookDeliveryTx(ctx context.Context, tx pgx.Tx, wsID, domainEvent string, data map[string]any) error {
	rows, err := tx.Query(ctx,
		`SELECT id::text, url, secret FROM webhooks
		  WHERE workspace_id = $1::uuid AND active
		    AND (events = '{}' OR $2 = ANY(events))`,
		wsID, domainEvent)
	if err != nil {
		return err
	}
	defer rows.Close()
	// Drain before enqueueing: pgx forbids a new query on the tx while
	// rows are still open ("conn busy").
	type hook struct{ id, target, secret string }
	var hooks []hook
	for rows.Next() {
		var h hook
		if err := rows.Scan(&h.id, &h.target, &h.secret); err != nil {
			return err
		}
		hooks = append(hooks, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, h := range hooks {
		if err := mail.Enqueue(ctx, tx, WebhookDeliverEvent, webhookDelivery{
			WebhookID: h.id,
			URL:       h.target,
			Secret:    h.secret,
			Event:     domainEvent,
			Data:      data,
		}); err != nil {
			return err
		}
	}
	return nil
}

// ---------- dispatcher ----------

const (
	// webhookEventPrefix scopes this dispatcher to webhook events; the
	// mail dispatcher claims "email.%" and never sees these rows.
	webhookEventPrefix = "webhook."
	// webhookBatchSize caps how many rows one Run claims.
	webhookBatchSize = 25
	// webhookMaxAttempts: after this many failed attempts the row is
	// marked failed instead of retried forever.
	webhookMaxAttempts = 8
	// webhookBackoffBase is the delay before the first retry; it doubles
	// per attempt.
	webhookBackoffBase = 5 * time.Second
	// webhookBackoffMax caps the retry delay (tighter than mail's: webhook
	// endpoints are usually back soon or dead).
	webhookBackoffMax = 5 * time.Minute
	// webhookHTTPTimeout bounds a single delivery attempt so one slow
	// endpoint cannot stall the whole pass.
	webhookHTTPTimeout = 10 * time.Second
)

// webhookDelivery is the outbox payload for WebhookDeliverEvent.
type webhookDelivery struct {
	WebhookID string         `json:"webhook_id"`
	URL       string         `json:"url"`
	Secret    string         `json:"secret"`
	Event     string         `json:"event"`
	Data      map[string]any `json:"data"`
}

// WebhookDispatcher delivers due "webhook.%" outbox rows. Run performs a
// single pass; the long-running ticker loop belongs to main (its own
// goroutine, so webhook HTTP latency never starves the mail dispatcher).
type WebhookDispatcher struct {
	pool   *pgxpool.Pool
	client *http.Client
}

// NewWebhookDispatcher builds a dispatcher with a 10s per-attempt timeout.
func NewWebhookDispatcher(pool *pgxpool.Pool) *WebhookDispatcher {
	return &WebhookDispatcher{
		pool:   pool,
		client: &http.Client{Timeout: webhookHTTPTimeout},
	}
}

type webhookOutboxRow struct {
	id       int64
	payload  []byte
	attempts int
}

// Run claims due webhook rows with FOR UPDATE SKIP LOCKED and delivers
// each. Per-message failures are recorded on the row; Run only returns an
// error when the database itself fails.
func (d *WebhookDispatcher) Run(ctx context.Context) error {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("webhook: begin dispatch tx: %w", err)
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
		SELECT id, payload, attempts
		FROM outbox
		WHERE status = 'pending'
		  AND next_retry_at <= now()
		  AND event LIKE $2
		ORDER BY id
		LIMIT $1
		FOR UPDATE SKIP LOCKED`, webhookBatchSize, webhookEventPrefix+"%")
	if err != nil {
		return fmt.Errorf("webhook: claim due rows: %w", err)
	}
	var claimed []webhookOutboxRow
	for rows.Next() {
		var r webhookOutboxRow
		if err := rows.Scan(&r.id, &r.payload, &r.attempts); err != nil {
			rows.Close()
			return fmt.Errorf("webhook: scan claimed row: %w", err)
		}
		claimed = append(claimed, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("webhook: iterate claimed rows: %w", err)
	}

	for _, r := range claimed {
		if err := d.deliver(ctx, tx, r); err != nil {
			return err // tx rolls back via defer
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("webhook: commit dispatch tx: %w", err)
	}
	return nil
}

// deliver POSTs one claimed row and records the outcome on it.
func (d *WebhookDispatcher) deliver(ctx context.Context, tx pgx.Tx, r webhookOutboxRow) error {
	var wd webhookDelivery
	if err := json.Unmarshal(r.payload, &wd); err != nil {
		// Poison row: never decodes, fail permanently.
		return d.markFailed(ctx, tx, r.id, r.attempts, fmt.Errorf("webhook: bad payload: %w", err))
	}
	if wd.URL == "" || wd.Event == "" {
		return d.markFailed(ctx, tx, r.id, r.attempts, errors.New("webhook: missing url or event"))
	}
	// A webhook deleted or deactivated after enqueue must not fire:
	// mark the row done (skip), not failed.
	var active bool
	if err := tx.QueryRow(ctx,
		`SELECT active FROM webhooks WHERE id = $1::uuid`, wd.WebhookID).Scan(&active); err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return d.markDone(ctx, tx, r.id)
		}
		return fmt.Errorf("webhook: lookup webhook: %w", err)
	}
	if !active {
		return d.markDone(ctx, tx, r.id)
	}

	body, err := json.Marshal(map[string]any{
		"event":        wd.Event,
		"data":         wd.Data,
		"delivered_at": time.Now().UTC(),
	})
	if err != nil {
		return d.markFailed(ctx, tx, r.id, r.attempts, fmt.Errorf("webhook: marshal body: %w", err))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, wd.URL, bytes.NewReader(body))
	if err != nil {
		return d.markFailed(ctx, tx, r.id, r.attempts, fmt.Errorf("webhook: build request: %w", err))
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Glance-Event", wd.Event)
	req.Header.Set("X-Glance-Delivery", fmt.Sprintf("%d", r.id))
	req.Header.Set("X-Glance-Signature", "sha256="+webhookSignature(wd.Secret, body))

	resp, err := d.client.Do(req)
	if err != nil {
		return d.recordFailure(ctx, tx, r.id, r.attempts, err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return d.recordFailure(ctx, tx, r.id, r.attempts,
			fmt.Errorf("webhook: %s returned status %d", wd.URL, resp.StatusCode))
	}
	return d.markDone(ctx, tx, r.id)
}

// webhookSignature is the hex HMAC-SHA256 of the body under the secret.
func webhookSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func (d *WebhookDispatcher) markDone(ctx context.Context, tx pgx.Tx, id int64) error {
	if _, err := tx.Exec(ctx,
		`UPDATE outbox SET status = 'done', processed_at = now() WHERE id = $1`, id); err != nil {
		return fmt.Errorf("webhook: mark row %d done: %w", id, err)
	}
	return nil
}

// recordFailure bumps attempts; the row stays pending with backoff, or is
// marked failed once it exhausts webhookMaxAttempts.
func (d *WebhookDispatcher) recordFailure(ctx context.Context, tx pgx.Tx, id int64, attempts int, sendErr error) error {
	attempts++
	if attempts >= webhookMaxAttempts {
		return d.markFailed(ctx, tx, id, attempts, sendErr)
	}
	retryAt := time.Now().Add(webhookRetryBackoff(attempts))
	if _, err := tx.Exec(ctx, `
		UPDATE outbox
		SET attempts = $2, last_error = $3, next_retry_at = $4
		WHERE id = $1`, id, attempts, sendErr.Error(), retryAt); err != nil {
		return fmt.Errorf("webhook: record failure for row %d: %w", id, err)
	}
	return nil
}

func (d *WebhookDispatcher) markFailed(ctx context.Context, tx pgx.Tx, id int64, attempts int, sendErr error) error {
	if _, err := tx.Exec(ctx, `
		UPDATE outbox
		SET status = 'failed', attempts = $2, last_error = $3
		WHERE id = $1`, id, attempts, sendErr.Error()); err != nil {
		return fmt.Errorf("webhook: mark row %d failed: %w", id, err)
	}
	return nil
}

// webhookRetryBackoff doubles from webhookBackoffBase, capped at
// webhookBackoffMax; the overflow guard keeps huge counts from wrapping.
func webhookRetryBackoff(attempts int) time.Duration {
	d := webhookBackoffBase << (attempts - 1)
	if d <= 0 || d > webhookBackoffMax {
		return webhookBackoffMax
	}
	return d
}
