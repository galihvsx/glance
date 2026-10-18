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
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
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
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	URL         string `json:"url"`
	// Secret is the HMAC signing secret. It is stored in PLAINTEXT by
	// design: the dispatcher recomputes HMAC-SHA256 over every delivery
	// body, which needs the raw key — a hash would be unusable for
	// signing. It is never serialized in list/get/update responses
	// (json:"-"): the only response that carries it is the 201 create
	// response (WebhookCreateResponse), shown once at creation time, so
	// routine GETs can't leak it into logs, devtools, or caches.
	Secret    string    `json:"-"`
	Events    []string  `json:"events"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// LastDeliveryAt / LastDeliveryStatus / LastError record the most
	// recent delivery attempt (C57T2). All three are NULL until the
	// first attempt. Pointer fields WITHOUT omitempty, so the keys are
	// present-but-null in list/get/create responses — the same null
	// convention as AdminUser.Name. The dispatcher writes them
	// alongside each outbox outcome write; the status update never
	// touches updated_at (delivery outcomes are operational metadata,
	// not a user edit of the webhook).
	LastDeliveryAt     *time.Time `json:"last_delivery_at"`
	LastDeliveryStatus *string    `json:"last_delivery_status"`
	LastError          *string    `json:"last_error"`
}

// WebhookCreateResponse is the POST 201 response shape: the single
// response that reveals the signing secret (show-on-create). Every other
// webhook response omits it.
type WebhookCreateResponse struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	URL         string    `json:"url"`
	Secret      string    `json:"secret"`
	Events      []string  `json:"events"`
	Active      bool      `json:"active"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	// Delivery outcome fields (C57T2): all NULL on a new webhook — it
	// has never delivered.
	LastDeliveryAt     *time.Time `json:"last_delivery_at"`
	LastDeliveryStatus *string    `json:"last_delivery_status"`
	LastError          *string    `json:"last_error"`
}

// CreateResponse renders the show-on-create shape for a new webhook.
func (w *Webhook) CreateResponse() *WebhookCreateResponse {
	return &WebhookCreateResponse{
		ID:                 w.ID,
		WorkspaceID:        w.WorkspaceID,
		URL:                w.URL,
		Secret:             w.Secret,
		Events:             w.Events,
		Active:             w.Active,
		CreatedAt:          w.CreatedAt,
		UpdatedAt:          w.UpdatedAt,
		LastDeliveryAt:     w.LastDeliveryAt,
		LastDeliveryStatus: w.LastDeliveryStatus,
		LastError:          w.LastError,
	}
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
		&w.Active, &w.CreatedAt, &w.UpdatedAt,
		&w.LastDeliveryAt, &w.LastDeliveryStatus, &w.LastError); err != nil {
		return nil, err
	}
	w.Events = events
	return &w, nil
}

const webhookColumns = `id::text, workspace_id::text, url, secret, events, active, created_at, updated_at, last_delivery_at, last_delivery_status, last_error`

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
	// C37T0: the insert and its audit row share one tx.
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	w, err := scanWebhook(tx.QueryRow(ctx,
		`INSERT INTO webhooks (workspace_id, url, secret, events, active)
		 VALUES ($1::uuid, $2, $3, $4, $5)
		 RETURNING `+webhookColumns,
		wsID, strings.TrimSpace(in.URL), secret, events, active))
	if err != nil {
		return nil, err
	}
	// Meta carries the webhook host ONLY — never the full URL (URLs
	// may embed tokens in query strings) and never the secret.
	if err := recordWorkspaceAuditInTx(ctx, tx, wsID, actorID,
		WorkspaceAuditIntegrationWebhookCreated, "webhook", w.ID,
		map[string]any{"host": webhookHost(w.URL)}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
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
		// C6T5: empty-string secret = regenerate (an empty HMAC secret
		// signs nothing — storing it would silently disable signing).
		secret := *patch.Secret
		if secret == "" {
			var err error
			secret, err = randomWebhookSecret()
			if err != nil {
				return nil, err
			}
		}
		add("secret", "", secret)
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
	// C37T0: the delete and its audit row share one tx. The URL is
	// read first so the audit meta can carry the host only.
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var rawURL string
	if err := tx.QueryRow(ctx,
		`SELECT url FROM webhooks WHERE id = $1::uuid AND workspace_id = $2::uuid`,
		id, wsID).Scan(&rawURL); err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return ErrWebhookNotFound
		}
		return err
	}
	tag, err := tx.Exec(ctx,
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
	if err := recordWorkspaceAuditInTx(ctx, tx, wsID, actorID,
		WorkspaceAuditIntegrationWebhookDeleted, "webhook", id,
		map[string]any{"host": webhookHost(rawURL)}); err != nil {
		return err
	}
	return tx.Commit(ctx)
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
	// webhookPerWebhookCap caps how many rows the GLOBAL (unscoped)
	// claim takes per webhook per pass. Without it, one webhook's due
	// backlog fills the whole batch and every other webhook's rows
	// starve behind it (head-of-line blocking). The scoped claim stays
	// uncapped. Fairness wins over single-webhook throughput: the
	// flooded webhook still drains every pass, just at most cap rows
	// per tick.
	webhookPerWebhookCap = 5
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
	// webhookClaimLease bounds how long a claimed row may sit
	// 'delivering' before another pass reclaims it (crashed-pass
	// recovery). Well above the worst-case pass (batch × timeout).
	webhookClaimLease = 5 * time.Minute
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
	// inFlight is the single-flight guard: a tick that fires while a
	// previous pass is still delivering skips instead of piling up
	// another claim + another long-lived pass.
	inFlight atomic.Bool
	// lookupIP resolves webhook target hostnames at delivery time, so the
	// SSRF guard sees what the dial would see. Stubbed in tests.
	lookupIP func(ctx context.Context, host string) ([]net.IP, error)
	// allowPrivateTargets disables the SSRF guard. Tests only: the
	// httptest delivery targets are loopback by construction. Never set
	// in production code.
	allowPrivateTargets bool
}

// noRedirect is the CheckRedirect for webhook delivery: redirects are
// NEVER followed. A webhook target that 3xx-redirects is a
// misconfiguration (receivers must answer 2xx); following it would let a
// compromised or malicious endpoint bounce the signed delivery at an
// arbitrary URL — including one the SSRF guard never vetted (the guard
// only checks the original target). The 3xx surfaces as a non-2xx and
// goes through the normal recordFailure → backoff → failed path.
func noRedirect(_ *http.Request, _ []*http.Request) error {
	return http.ErrUseLastResponse
}

// NewWebhookDispatcher builds a dispatcher with a 10s per-attempt timeout.
func NewWebhookDispatcher(pool *pgxpool.Pool) *WebhookDispatcher {
	return &WebhookDispatcher{
		pool: pool,
		client: &http.Client{
			Timeout:       webhookHTTPTimeout,
			CheckRedirect: noRedirect,
		},
		lookupIP: func(ctx context.Context, host string) ([]net.IP, error) {
			addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			ips := make([]net.IP, 0, len(addrs))
			for _, a := range addrs {
				ips = append(ips, a.IP)
			}
			return ips, nil
		},
	}
}

type webhookOutboxRow struct {
	id       int64
	payload  []byte
	attempts int
}

// Run performs one dispatch pass: claim due rows, commit the claim,
// deliver each row OUTSIDE any transaction, and record each outcome with
// a short single-statement write. The claim tx never spans slow HTTP, so
// a black-holing endpoint holds no pool connection. A tick arriving while
// a pass is in flight is skipped (single-flight). Per-row outcome-write
// failures are collected and returned after the remaining rows deliver;
// only database failures surface as Run errors.
func (d *WebhookDispatcher) Run(ctx context.Context) error {
	return d.run(ctx, "")
}

// runScoped performs one dispatch pass restricted to rows whose payload
// names webhookID. Test-only isolation: the shared test DB accumulates
// due rows from other tests, and an unscoped Run claims the oldest
// webhookBatchSize rows globally, so a test's own row could starve
// behind other tests' leftovers. Production (cmd/glance/main.go,
// tickers) keeps calling Run, which is unscoped.
func (d *WebhookDispatcher) runScoped(ctx context.Context, webhookID string) error {
	return d.run(ctx, webhookID)
}

func (d *WebhookDispatcher) run(ctx context.Context, webhookID string) error {
	if !d.inFlight.CompareAndSwap(false, true) {
		return nil // previous pass still in flight; skip this tick
	}
	defer d.inFlight.Store(false)

	claimed, err := d.claimDue(ctx, webhookID)
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

// claimDue claims due webhook rows inside ONE short tx: rows are locked
// (FOR UPDATE SKIP LOCKED — safe across dispatcher instances), marked
// 'delivering' with a lease, and the tx commits BEFORE any HTTP happens.
// Rows stuck 'delivering' past their lease (a crashed pass) are reclaimed
// here; deliveries stay at-least-once. A non-empty webhookID scopes the
// claim to rows whose payload names that webhook (payload->>'webhook_id'
// = $3); an empty webhookID claims globally, which is the production
// behavior, capped at webhookPerWebhookCap rows per webhook per pass
// (fairness: one webhook's backlog cannot starve the others). Ordering
// is unchanged either way.
func (d *WebhookDispatcher) claimDue(ctx context.Context, webhookID string) ([]webhookOutboxRow, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("webhook: begin claim tx: %w", err)
	}
	defer tx.Rollback(ctx)

	query := `
		SELECT id, payload, attempts
		FROM outbox
		WHERE event LIKE $2
		  AND next_retry_at <= now()
		  AND (status = 'pending' OR status = 'delivering')`
	args := []any{webhookBatchSize, webhookEventPrefix + "%"}
	if webhookID != "" {
		query += `
		  AND payload->>'webhook_id' = $3`
		args = append(args, webhookID)
	} else {
		// Global claim: at most webhookPerWebhookCap rows per webhook
		// per pass (ROW_NUMBER over the same due filter, oldest first
		// within each webhook). The due filter is duplicated inside
		// the subquery on purpose: Postgres rejects FOR UPDATE on a
		// query whose FROM contains a window function ("FOR UPDATE is
		// not allowed with window functions"), so the ranking lives in
		// an IN-subquery and the outer query keeps its original shape
		// — same due filter, same ORDER BY id, same LIMIT, same
		// FOR UPDATE SKIP LOCKED on outbox rows. The two copies of the
		// filter must stay in sync. The scoped path above is untouched.
		query += `
		  AND id IN (
			SELECT id FROM (
				SELECT id,
					ROW_NUMBER() OVER (PARTITION BY payload->>'webhook_id' ORDER BY id) AS rn
				FROM outbox
				WHERE event LIKE $2
				  AND next_retry_at <= now()
				  AND (status = 'pending' OR status = 'delivering')
			) ranked
			WHERE rn <= $3
		  )`
		args = append(args, webhookPerWebhookCap)
	}
	query += `
		ORDER BY id
		LIMIT $1
		FOR UPDATE SKIP LOCKED`
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("webhook: claim due rows: %w", err)
	}
	var claimed []webhookOutboxRow
	for rows.Next() {
		var r webhookOutboxRow
		if err := rows.Scan(&r.id, &r.payload, &r.attempts); err != nil {
			rows.Close()
			return nil, fmt.Errorf("webhook: scan claimed row: %w", err)
		}
		claimed = append(claimed, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("webhook: iterate claimed rows: %w", err)
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
			ids, time.Now().Add(webhookClaimLease)); err != nil {
			return nil, fmt.Errorf("webhook: mark rows delivering: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("webhook: commit claim tx: %w", err)
	}
	return claimed, nil
}

// deliverRow POSTs one claimed row and records the outcome with short
// single-statement writes — no transaction spans the HTTP call.
func (d *WebhookDispatcher) deliverRow(ctx context.Context, r webhookOutboxRow) error {
	var wd webhookDelivery
	if err := json.Unmarshal(r.payload, &wd); err != nil {
		// Poison row: never decodes, fail permanently. No webhook id
		// is recoverable from the payload — no status update.
		return d.markFailed(ctx, r.id, r.attempts, fmt.Errorf("webhook: bad payload: %w", err), "")
	}
	if wd.URL == "" || wd.Event == "" {
		return d.markFailed(ctx, r.id, r.attempts, errors.New("webhook: missing url or event"), wd.WebhookID)
	}
	// A webhook deleted or deactivated after enqueue must not fire:
	// mark the row done (skip), not failed. No delivery was attempted,
	// so no delivery status is written either (empty webhook id).
	var active bool
	if err := d.pool.QueryRow(ctx,
		`SELECT active FROM webhooks WHERE id = $1::uuid`, wd.WebhookID).Scan(&active); err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return d.markDone(ctx, r.id, "")
		}
		return fmt.Errorf("webhook: lookup webhook: %w", err)
	}
	if !active {
		return d.markDone(ctx, r.id, "")
	}

	body, err := json.Marshal(map[string]any{
		"event":        wd.Event,
		"data":         wd.Data,
		"delivered_at": time.Now().UTC(),
	})
	if err != nil {
		return d.markFailed(ctx, r.id, r.attempts, fmt.Errorf("webhook: marshal body: %w", err), wd.WebhookID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, wd.URL, bytes.NewReader(body))
	if err != nil {
		return d.markFailed(ctx, r.id, r.attempts, fmt.Errorf("webhook: build request: %w", err), wd.WebhookID)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Glance-Event", wd.Event)
	req.Header.Set("X-Glance-Delivery", fmt.Sprintf("%d", r.id))
	req.Header.Set("X-Glance-Signature", "sha256="+webhookSignature(wd.Secret, body))

	// SSRF guard (Task 28): resolve the target at delivery time and refuse
	// to dial non-public addresses — webhook URLs are admin-controlled, but
	// v1 must not ship dialable-metadata-endpoint webhooks (169.254.169.254
	// is the classic target; a string check on the URL would miss DNS
	// indirection entirely).
	var resp *http.Response
	if d.allowPrivateTargets {
		resp, err = d.client.Do(req) // tests only: httptest targets are loopback
	} else {
		var ips []net.IP
		ips, err = d.guardTarget(ctx, wd.URL)
		if err != nil {
			if errors.Is(err, errWebhookTargetBlocked) {
				// Permanently unsafe: a target resolving to private
				// space will never become safe — fail the row instead
				// of retrying it forever.
				return d.markFailed(ctx, r.id, r.attempts, err, wd.WebhookID)
			}
			// DNS failure: transient — backoff and retry.
			return d.recordFailure(ctx, r.id, r.attempts, err, wd.WebhookID)
		}
		resp, err = d.doPinned(ctx, req, ips)
	}
	if err != nil {
		return d.recordFailure(ctx, r.id, r.attempts, err, wd.WebhookID)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return d.recordFailure(ctx, r.id, r.attempts,
			fmt.Errorf("webhook: %s returned status %d", wd.URL, resp.StatusCode), wd.WebhookID)
	}
	return d.markDone(ctx, r.id, wd.WebhookID)
}

// errWebhookTargetBlocked marks a delivery refused by the SSRF guard. It
// is matched with errors.Is so the caller can fail the row permanently
// (a target resolving to private space will never become safe) instead of
// retrying it on backoff forever.
var errWebhookTargetBlocked = errors.New("webhook: target resolves to a non-public IP (SSRF guard)")

// webhookCGNAT is the shared address space (RFC 6598) used by carrier-
// grade NAT: not public-routable, and internal in many deployments. Go's
// net.IP.IsPrivate does not cover it, so it gets an explicit entry.
var webhookCGNAT = func() *net.IPNet {
	_, n, _ := net.ParseCIDR("100.64.0.0/10")
	return n
}()

// webhookTargetBlocked reports whether ip must never receive a webhook
// delivery: private nets, CGNAT shared space, loopback, link-local (cloud
// metadata lives at 169.254.169.254), multicast, and unspecified. ANY
// blocked address in a hostname's answer set blocks the whole target
// (fail closed on mixed DNS answers).
func webhookTargetBlocked(ip net.IP) bool {
	return ip.IsPrivate() || webhookCGNAT.Contains(ip) || ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified()
}

// guardTarget applies the SSRF guard to rawURL: it resolves the host at
// delivery time (not a string check on the URL — DNS indirection would
// defeat that) and rejects non-public targets. On success it returns the
// validated IPs, which the caller must dial directly (doPinned) to close
// the DNS-rebinding TOCTOU between resolution and connect.
func (d *WebhookDispatcher) guardTarget(ctx context.Context, rawURL string) ([]net.IP, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Hostname() == "" {
		return nil, fmt.Errorf("%w: unparseable url %q", errWebhookTargetBlocked, rawURL)
	}
	host := u.Hostname()
	var ips []net.IP
	if lit := net.ParseIP(host); lit != nil {
		// Literal IP: no DNS involved, nothing to rebind.
		ips = []net.IP{lit}
	} else {
		ips, err = d.lookupIP(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("webhook: DNS resolution failed for %q: %w", host, err)
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("webhook: DNS resolution returned no addresses for %q", host)
		}
	}
	for _, ip := range ips {
		if webhookTargetBlocked(ip) {
			return nil, fmt.Errorf("%w: %q -> %s", errWebhookTargetBlocked, host, ip.String())
		}
	}
	return ips, nil
}

// doPinned POSTs req dialing ONLY the guard-validated IPs. The stock
// http.Client would re-resolve the hostname at dial time, reopening the
// DNS-rebinding TOCTOU the guard just closed; pinning the dial to the
// checked addresses keeps resolve and connect on the same answer set.
func (d *WebhookDispatcher) doPinned(ctx context.Context, req *http.Request, ips []net.IP) (*http.Response, error) {
	if len(ips) == 0 {
		return nil, errors.New("webhook: no validated IPs to dial")
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			_, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			var dialer net.Dialer
			var firstErr error
			for _, ip := range ips {
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if err == nil {
					return conn, nil
				}
				if firstErr == nil {
					firstErr = err
				}
			}
			return nil, firstErr
		},
	}
	return (&http.Client{
		Timeout:       webhookHTTPTimeout,
		Transport:     transport,
		CheckRedirect: noRedirect,
	}).Do(req)
}

// webhookSignature is the hex HMAC-SHA256 of the body under the secret.
func webhookSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// Outcome writes are single statements on the pool (short-lived, no tx
// spans the HTTP call). A failed outcome write leaves the row
// 'delivering' with its lease — a later pass reclaims and redelivers
// (at-least-once is preserved).
//
// Each outcome write also records the last-delivery outcome on the
// webhook row (C57T2) via updateWebhookDeliveryStatus, in the same step
// as the outbox outcome write. webhookID is the payload's webhook id;
// "" means "no webhook to attribute" (skip paths, poison rows) and
// writes no status. The outbox write runs FIRST: the outbox row is the
// source of truth for delivery, and a webhook-status failure surfaces
// as a Run error without un-delivering an already-recorded outcome —
// the next delivery for that webhook corrects the display field.
func (d *WebhookDispatcher) markDone(ctx context.Context, id int64, webhookID string) error {
	if _, err := d.pool.Exec(ctx,
		`UPDATE outbox SET status = 'done', processed_at = now() WHERE id = $1`, id); err != nil {
		return fmt.Errorf("webhook: mark row %d done: %w", id, err)
	}
	return d.updateWebhookDeliveryStatus(ctx, webhookID, "delivered", nil)
}

// recordFailure bumps attempts; the row goes back to pending with
// backoff, or is marked failed once it exhausts webhookMaxAttempts.
func (d *WebhookDispatcher) recordFailure(ctx context.Context, id int64, attempts int, sendErr error, webhookID string) error {
	attempts++
	if attempts >= webhookMaxAttempts {
		return d.markFailed(ctx, id, attempts, sendErr, webhookID)
	}
	retryAt := time.Now().Add(webhookRetryBackoff(attempts))
	if _, err := d.pool.Exec(ctx, `
		UPDATE outbox
		SET status = 'pending', attempts = $2, last_error = $3, next_retry_at = $4
		WHERE id = $1`, id, attempts, sendErr.Error(), retryAt); err != nil {
		return fmt.Errorf("webhook: record failure for row %d: %w", id, err)
	}
	errStr := sendErr.Error()
	return d.updateWebhookDeliveryStatus(ctx, webhookID, "retrying", &errStr)
}

func (d *WebhookDispatcher) markFailed(ctx context.Context, id int64, attempts int, sendErr error, webhookID string) error {
	if _, err := d.pool.Exec(ctx, `
		UPDATE outbox
		SET status = 'failed', attempts = $2, last_error = $3,
		    processed_at = now()
		WHERE id = $1`, id, attempts, sendErr.Error()); err != nil {
		return fmt.Errorf("webhook: mark row %d failed: %w", id, err)
	}
	errStr := sendErr.Error()
	return d.updateWebhookDeliveryStatus(ctx, webhookID, "failed", &errStr)
}

// updateWebhookDeliveryStatus records the last-delivery outcome on the
// webhook row: last_delivery_at = now(), last_delivery_status =
// status, last_error = lastErr (nil → NULL, the delivered case).
// updated_at is NOT touched — delivery outcomes are operational
// metadata, not a user edit of the webhook.
//
// A missing row (the webhook was deleted between the deliverRow lookup
// and this write) updates zero rows and is a silent no-op, NOT an
// error — the UPDATE's WHERE only matches by id, so zero rows affected
// can only mean the row is gone. A genuine failure on an EXISTING row
// surfaces like other outcome-write failures.
func (d *WebhookDispatcher) updateWebhookDeliveryStatus(ctx context.Context, webhookID, status string, lastErr *string) error {
	if webhookID == "" {
		return nil
	}
	// A malformed id can never match a webhook row: treat it like a
	// missing row (silent no-op) instead of letting the $1::uuid cast
	// below fail. This matters on the pre-lookup path, where deliverRow
	// calls markFailed with the payload's raw webhook_id before any
	// lookup has validated it.
	if _, err := uuid.Parse(webhookID); err != nil {
		return nil
	}
	var errArg any
	if lastErr != nil {
		errArg = *lastErr
	}
	if _, err := d.pool.Exec(ctx,
		`UPDATE webhooks
		    SET last_delivery_at = now(), last_delivery_status = $2, last_error = $3
		  WHERE id = $1::uuid`, webhookID, status, errArg); err != nil {
		return fmt.Errorf("webhook: update delivery status for webhook %s: %w", webhookID, err)
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
