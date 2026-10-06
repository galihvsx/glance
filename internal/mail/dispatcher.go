package mail

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// dispatchBatchSize caps how many rows one Run claims.
	dispatchBatchSize = 25
	// maxDeliveryAttempts: after this many failed attempts the row is
	// marked failed instead of being retried forever.
	maxDeliveryAttempts = 10
	// retryBackoffBase is the delay before the first retry; it doubles
	// with each attempt.
	retryBackoffBase = 5 * time.Second
	// retryBackoffMax caps the retry delay.
	retryBackoffMax = time.Hour
)

// emailEventPrefix scopes this dispatcher to email events. The outbox table
// is shared (a webhook dispatcher will later claim "webhook.*"); each
// dispatcher only touches its own namespace.
const emailEventPrefix = "email."

// Dispatcher delivers due outbox rows through a Sender. Run performs a
// single pass; the long-running loop that calls it belongs to the
// application wiring (a later task).
type Dispatcher struct {
	pool   *pgxpool.Pool
	sender Sender
}

// NewDispatcher builds a Dispatcher that delivers due email rows using sender.
func NewDispatcher(pool *pgxpool.Pool, sender Sender) *Dispatcher {
	return &Dispatcher{pool: pool, sender: sender}
}

type outboxRow struct {
	id       int64
	event    string
	payload  []byte
	attempts int
}

// Run performs a single dispatch pass: claim due rows with FOR UPDATE SKIP
// LOCKED, deliver each, and mark it done or schedule a retry with
// exponential backoff in next_retry_at. Per-message failures are recorded
// on the row; Run only returns an error when the database itself fails.
func (d *Dispatcher) Run(ctx context.Context) error {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("mail: begin dispatch tx: %w", err)
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
		SELECT id, event, payload, attempts
		FROM outbox
		WHERE status = 'pending'
		  AND next_retry_at <= now()
		  AND event LIKE $2
		ORDER BY id
		LIMIT $1
		FOR UPDATE SKIP LOCKED`, dispatchBatchSize, emailEventPrefix+"%")
	if err != nil {
		return fmt.Errorf("mail: claim due rows: %w", err)
	}
	var claimed []outboxRow
	for rows.Next() {
		var r outboxRow
		if err := rows.Scan(&r.id, &r.event, &r.payload, &r.attempts); err != nil {
			rows.Close()
			return fmt.Errorf("mail: scan claimed row: %w", err)
		}
		claimed = append(claimed, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("mail: iterate claimed rows: %w", err)
	}

	for _, r := range claimed {
		if err := d.deliver(ctx, tx, r); err != nil {
			return err // tx rolls back via defer
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("mail: commit dispatch tx: %w", err)
	}
	return nil
}

// deliver sends one claimed row and records the outcome on it.
func (d *Dispatcher) deliver(ctx context.Context, tx pgx.Tx, r outboxRow) error {
	var msg Message
	if err := json.Unmarshal(r.payload, &msg); err != nil {
		// Poison row: it will never decode, so fail it permanently
		// instead of retrying forever.
		return d.markFailed(ctx, tx, r.id, r.attempts, fmt.Errorf("mail: bad payload: %w", err))
	}
	if err := d.sender.Send(ctx, msg); err != nil {
		return d.recordFailure(ctx, tx, r.id, r.attempts, err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE outbox SET status = 'done', processed_at = now() WHERE id = $1`, r.id); err != nil {
		return fmt.Errorf("mail: mark row %d done: %w", r.id, err)
	}
	return nil
}

// recordFailure bumps the attempt counter; the row stays pending with a
// backoff in next_retry_at, or is marked failed once it exhausts its
// attempts.
func (d *Dispatcher) recordFailure(ctx context.Context, tx pgx.Tx, id int64, attempts int, sendErr error) error {
	attempts++
	if attempts >= maxDeliveryAttempts {
		return d.markFailed(ctx, tx, id, attempts, sendErr)
	}
	retryAt := time.Now().Add(retryBackoff(attempts))
	if _, err := tx.Exec(ctx, `
		UPDATE outbox
		SET attempts = $2, last_error = $3, next_retry_at = $4
		WHERE id = $1`, id, attempts, sendErr.Error(), retryAt); err != nil {
		return fmt.Errorf("mail: record failure for row %d: %w", id, err)
	}
	return nil
}

func (d *Dispatcher) markFailed(ctx context.Context, tx pgx.Tx, id int64, attempts int, sendErr error) error {
	if _, err := tx.Exec(ctx, `
		UPDATE outbox
		SET status = 'failed', attempts = $2, last_error = $3
		WHERE id = $1`, id, attempts, sendErr.Error()); err != nil {
		return fmt.Errorf("mail: mark row %d failed: %w", id, err)
	}
	return nil
}

// retryBackoff returns the delay before the next attempt; attempts is the
// 1-based post-increment count. Doubling with a cap; the overflow guard
// keeps huge attempt counts from wrapping negative.
func retryBackoff(attempts int) time.Duration {
	d := retryBackoffBase << (attempts - 1)
	if d <= 0 || d > retryBackoffMax {
		return retryBackoffMax
	}
	return d
}
