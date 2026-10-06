package mail

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

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
	// mailClaimLease is how long a claimed row stays 'delivering' before
	// a crashed pass's rows become reclaimable by the next pass.
	mailClaimLease = 5 * time.Minute
)

// emailEventPrefix scopes this dispatcher to email events. The outbox table
// is shared (a webhook dispatcher will later claim "webhook.*"); each
// dispatcher only touches its own namespace.
const emailEventPrefix = "email."

// Dispatcher delivers due outbox rows through a Sender. Run performs a
// single pass; the long-running loop that calls it belongs to the
// application wiring (a later task).
type Dispatcher struct {
	pool     *pgxpool.Pool
	sender   Sender
	inFlight atomic.Bool
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

// Run performs a single dispatch pass. The claim tx is short — rows are
// locked (FOR UPDATE SKIP LOCKED), marked 'delivering' with a lease, and
// the tx COMMITS before any SMTP happens. Deliveries run outside the tx so
// a slow SMTP host can never hold row locks or pool connections; outcome
// recording is single-statement writes. A single-flight guard skips ticks
// that arrive while a pass is still running, so a wedged pass can't pile
// up concurrent runs and exhaust the pool.
func (d *Dispatcher) Run(ctx context.Context) error {
	if !d.inFlight.CompareAndSwap(false, true) {
		return nil // previous pass still in flight; skip this tick
	}
	defer d.inFlight.Store(false)

	claimed, err := d.claimDue(ctx)
	if err != nil {
		return err
	}
	for _, r := range claimed {
		if err := d.deliverRow(ctx, r); err != nil {
			return err // database write failed; row stays 'delivering' until the lease expires
		}
	}
	return nil
}

// claimDue claims due email rows inside ONE short tx: rows are locked
// (FOR UPDATE SKIP LOCKED — safe across dispatcher instances), marked
// 'delivering' with a lease, and the tx commits BEFORE any SMTP happens.
// Rows stuck 'delivering' past their lease (a crashed pass) are reclaimed
// here; deliveries stay at-least-once.
func (d *Dispatcher) claimDue(ctx context.Context) ([]outboxRow, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("mail: begin claim tx: %w", err)
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
		SELECT id, event, payload, attempts
		FROM outbox
		WHERE event LIKE $2
		  AND next_retry_at <= now()
		  AND (status = 'pending' OR status = 'delivering')
		ORDER BY id
		LIMIT $1
		FOR UPDATE SKIP LOCKED`, dispatchBatchSize, emailEventPrefix+"%")
	if err != nil {
		return nil, fmt.Errorf("mail: claim due rows: %w", err)
	}
	var claimed []outboxRow
	for rows.Next() {
		var r outboxRow
		if err := rows.Scan(&r.id, &r.event, &r.payload, &r.attempts); err != nil {
			rows.Close()
			return nil, fmt.Errorf("mail: scan claimed row: %w", err)
		}
		claimed = append(claimed, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("mail: iterate claimed rows: %w", err)
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
			ids, time.Now().Add(mailClaimLease)); err != nil {
			return nil, fmt.Errorf("mail: mark rows delivering: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("mail: commit claim tx: %w", err)
	}
	return claimed, nil
}

// deliverRow sends one claimed row and records the outcome with short
// single-statement writes — no transaction spans the SMTP call. Per-message
// failures are recorded on the row; deliverRow only returns an error when
// the database itself fails.
func (d *Dispatcher) deliverRow(ctx context.Context, r outboxRow) error {
	var msg Message
	if err := json.Unmarshal(r.payload, &msg); err != nil {
		// Poison row: it will never decode, so fail it permanently
		// instead of retrying forever.
		return d.markFailed(ctx, r.id, r.attempts, fmt.Errorf("mail: bad payload: %w", err))
	}
	if err := d.sender.Send(ctx, msg); err != nil {
		return d.recordFailure(ctx, r.id, r.attempts, err)
	}
	if _, err := d.pool.Exec(ctx,
		`UPDATE outbox SET status = 'done', processed_at = now() WHERE id = $1`, r.id); err != nil {
		return fmt.Errorf("mail: mark row %d done: %w", r.id, err)
	}
	return nil
}

// recordFailure bumps the attempt counter; the row goes back to 'pending'
// with a backoff in next_retry_at, or is marked failed once it exhausts
// its attempts. Single-statement write — no tx spans the SMTP call.
func (d *Dispatcher) recordFailure(ctx context.Context, id int64, attempts int, sendErr error) error {
	attempts++
	if attempts >= maxDeliveryAttempts {
		return d.markFailed(ctx, id, attempts, sendErr)
	}
	retryAt := time.Now().Add(retryBackoff(attempts))
	if _, err := d.pool.Exec(ctx, `
		UPDATE outbox
		SET status = 'pending', attempts = $2, last_error = $3, next_retry_at = $4
		WHERE id = $1`, id, attempts, sendErr.Error(), retryAt); err != nil {
		return fmt.Errorf("mail: record failure for row %d: %w", id, err)
	}
	return nil
}

func (d *Dispatcher) markFailed(ctx context.Context, id int64, attempts int, sendErr error) error {
	if _, err := d.pool.Exec(ctx, `
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
