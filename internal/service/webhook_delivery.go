package service

// Webhook delivery history + manual redelivery (C59T0).
//
// The dispatcher writes one webhook_deliveries row per attempt
// (delivered / retrying / failed) alongside each outbox outcome.
// This file is the read + redeliver surface over that history:
// ListWebhookDeliveries (admin-only, workspace-scoped, newest-first,
// cursor paging) and RedeliverWebhookDelivery (failed rows only).
//
// Redelivery enqueues a FRESH outbox row (attempts = 0, pending,
// next_retry_at = now) built from the CURRENT webhook row (current
// URL + current secret — if the admin changed the URL since the
// failure, the redelivery goes to the current URL) plus the history
// row's event + data. The original history row is untouched (it
// remains the record of the failed attempt); the redelivery's own
// attempts create NEW history rows, correlated via the new outbox
// row id.

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"glance/internal/mail"
)

var (
	// ErrWebhookDeliveryNotFound is returned for unknown delivery ids
	// (scoped to the webhook — a delivery under another webhook reads
	// as not found, never leaking cross-webhook existence).
	ErrWebhookDeliveryNotFound = errors.New("service: webhook delivery not found")
	// ErrDeliveryNotRedeliverable is returned when a delivery cannot
	// be redelivered: its status is not failed, or its webhook is
	// inactive. The handler maps it to 409 Conflict.
	ErrDeliveryNotRedeliverable = errors.New("service: webhook delivery is not redeliverable")
)

// WebhookDelivery is one webhook_deliveries row as exposed to the
// list endpoint: the log view. It deliberately carries NO data —
// payloads can be large; the list is the log view, not the payload
// view — and NO outbox_row_id (internal correlation).
type WebhookDelivery struct {
	ID          int64     `json:"id"`
	WebhookID   string    `json:"webhook_id"`
	Event       string    `json:"event"`
	Status      string    `json:"status"`
	HTTPStatus  *int      `json:"http_status"`
	Error       *string   `json:"error"`
	DurationMs  int       `json:"duration_ms"`
	AttemptedAt time.Time `json:"attempted_at"`
}

func scanWebhookDelivery(row pgx.Row) (*WebhookDelivery, error) {
	var d WebhookDelivery
	if err := row.Scan(&d.ID, &d.WebhookID, &d.Event, &d.Status,
		&d.HTTPStatus, &d.Error, &d.DurationMs, &d.AttemptedAt); err != nil {
		return nil, err
	}
	return &d, nil
}

const webhookDeliveryColumns = `id, webhook_id::text, event, status, http_status, error, duration_ms, attempted_at`

// webhookDeliveryLimit clamps the list page size: <= 0 means the
// default (25), above 100 caps at 100 (same max as notifications).
// The handler validates explicitly and returns 400 for an invalid
// limit; this clamp is the service-level backstop.
func webhookDeliveryLimit(limit int) int {
	if limit <= 0 {
		return 25
	}
	if limit > 100 {
		return 100
	}
	return limit
}

// ListWebhookDeliveries returns one webhook's delivery history,
// newest first (attempted_at DESC, id DESC). beforeID is a cursor =
// a delivery id; when > 0 only rows strictly older than that id
// (id < beforeID) are returned. Admin-only, workspace-scoped: a
// webhook that does not belong to the :slug workspace reads as
// ErrWebhookNotFound.
func ListWebhookDeliveries(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID, webhookID string, limit int, beforeID int64) ([]*WebhookDelivery, error) {
	wsID, err := resolveWebhookAdmin(ctx, pool, wsSlug, actorID)
	if err != nil {
		return nil, err
	}
	// Webhook must exist in this workspace — otherwise 404, and the
	// list query below can never leak another workspace's history.
	var exists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM webhooks WHERE id = $1::uuid AND workspace_id = $2::uuid)`,
		webhookID, wsID).Scan(&exists); err != nil {
		if isInvalidUUID(err) {
			return nil, ErrWebhookNotFound
		}
		return nil, err
	}
	if !exists {
		return nil, ErrWebhookNotFound
	}
	query := `SELECT ` + webhookDeliveryColumns + ` FROM webhook_deliveries
		  WHERE webhook_id = $1::uuid`
	args := []any{webhookID}
	if beforeID > 0 {
		query += ` AND id < $2`
		args = append(args, beforeID)
	}
	query += ` ORDER BY attempted_at DESC, id DESC LIMIT $` + strconv.Itoa(len(args)+1)
	args = append(args, webhookDeliveryLimit(limit))
	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*WebhookDelivery{}
	for rows.Next() {
		d, err := scanWebhookDelivery(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// RedeliverWebhookDelivery enqueues a fresh outbox row for a failed
// delivery. 404 when the webhook or the delivery is not found (or
// the delivery belongs to another webhook); 409 when the delivery
// is not failed or the webhook is inactive. Only failed entries are
// redeliverable: retrying will retry automatically (redelivering it
// would double-deliver); delivered needs no redelivery.
func RedeliverWebhookDelivery(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID, webhookID string, deliveryID int64) error {
	wsID, err := resolveWebhookAdmin(ctx, pool, wsSlug, actorID)
	if err != nil {
		return err
	}
	// Current webhook row: URL + secret are re-read at redelivery
	// time, never from history (history never stores them).
	var webhook struct {
		url    string
		secret string
		active bool
	}
	if err := pool.QueryRow(ctx,
		`SELECT url, secret, active FROM webhooks
		  WHERE id = $1::uuid AND workspace_id = $2::uuid`,
		webhookID, wsID).Scan(&webhook.url, &webhook.secret, &webhook.active); err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return ErrWebhookNotFound
		}
		return err
	}
	if !webhook.active {
		// The dispatcher would skip the row anyway; refusing upfront
		// beats a silent no-op enqueue.
		return ErrDeliveryNotRedeliverable
	}
	var event string
	var dataBytes []byte
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT event, data, status FROM webhook_deliveries
		  WHERE id = $1 AND webhook_id = $2::uuid`,
		deliveryID, webhookID).Scan(&event, &dataBytes, &status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return ErrWebhookDeliveryNotFound
		}
		return err
	}
	if status != "failed" {
		return ErrDeliveryNotRedeliverable
	}
	var data map[string]any
	if err := json.Unmarshal(dataBytes, &data); err != nil {
		return err
	}
	// Fresh outbox row: attempts 0, pending, due now (mail.Enqueue
	// stamps those defaults). The original history row is untouched.
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := mail.Enqueue(ctx, tx, WebhookDeliverEvent, webhookDelivery{
		WebhookID: webhookID,
		URL:       webhook.url,
		Secret:    webhook.secret,
		Event:     event,
		Data:      data,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
