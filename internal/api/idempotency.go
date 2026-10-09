package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
)

// IdempotencyKeyHeader is the request header clients send for safe retries
// (spec §5). Honored on POST mutations: create issue, create comment,
// bulk-update, bulk-delete.
const IdempotencyKeyHeader = "Idempotency-Key"

// maxIdempotencyKeyLen caps the header value — a DoS guard on the stored key.
const maxIdempotencyKeyLen = 255

// idempotencyTTL is how long a completed key's response is replayed. Expired
// rows are deleted opportunistically on access (no cron in v1).
const idempotencyTTL = 24 * time.Hour

// idemExecutor runs the wrapped operation. It returns the HTTP status and
// the payload to serialize; err is a service-layer error for the caller's
// error mapper.
type idemExecutor func() (status int, payload any, err error)

// withIdempotency wraps a POST handler with Idempotency-Key support. The
// flow (kept deliberately OUTSIDE the operation's own transaction, so the
// key claim never deadlocks with the operation's locks):
//
//  1. No header (or empty) → execute directly, no key claimed.
//  2. INSERT the key row (user_id, idem_key) ... ON CONFLICT DO NOTHING:
//     the insert winner executes the operation, stores (status, body),
//     and answers. Only 2xx responses are stored — a failed execution
//     releases its key so a client retry re-executes instead of replaying
//     a stale error.
//  3. The insert loser SELECTs the row: completed → replay the stored
//     response byte-identical without executing; in_progress → 409
//     conflict ("request in progress"), never a duplicate execution.
//
// mapErr maps service errors to HTTP responses (e.g. issueError).
func withIdempotency(c *echo.Context, pool *pgxpool.Pool, endpoint string, mapErr func(*echo.Context, error) error, exec idemExecutor) error {
	key := c.Request().Header.Get(IdempotencyKeyHeader)
	if key == "" {
		status, payload, err := exec()
		if err != nil {
			return mapErr(c, err)
		}
		return writeJSONBlob(c, status, payload)
	}
	if len(key) > maxIdempotencyKeyLen {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "idempotency key too long", nil)
	}
	user := CurrentUser(c)
	if user == nil {
		// RequireAuth always runs before this; defensive only.
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	ctx := c.Request().Context()

	// Opportunistic TTL sweep: expired rows are garbage, never replayed.
	_, _ = pool.Exec(ctx,
		`DELETE FROM idempotency_keys WHERE created_at < now() - make_interval(secs => $1)`,
		int64(idempotencyTTL/time.Second))

	for range 3 {
		var keyID string
		err := pool.QueryRow(ctx,
			`INSERT INTO idempotency_keys (user_id, idem_key, endpoint, status)
			 VALUES ($1::uuid, $2, $3, 'in_progress')
			 ON CONFLICT (user_id, idem_key) DO NOTHING
			 RETURNING id::text`,
			user.ID, key, endpoint).Scan(&keyID)
		if err == nil {
			return runIdempotentWinner(c, pool, keyID, mapErr, exec)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return WriteInternalError(c)
		}

		// Insert loser: someone else owns this key.
		var status string
		var code *int
		var body []byte
		err = pool.QueryRow(ctx,
			`SELECT status, response_code, response_body FROM idempotency_keys
			 WHERE user_id = $1::uuid AND idem_key = $2`,
			user.ID, key).Scan(&status, &code, &body)
		if errors.Is(err, pgx.ErrNoRows) {
			// The owner released the key (its execution failed) between
			// our INSERT and SELECT — retry the claim.
			continue
		}
		if err != nil {
			return WriteInternalError(c)
		}
		if status == "completed" && code != nil {
			// Byte-identical replay: the stored body goes out untouched.
			return c.JSONBlob(*code, body)
		}
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "request in progress", nil)
	}
	return WriteInternalError(c)
}

// runIdempotentWinner executes the operation for the key-claim winner,
// stores the 2xx response, and answers. A failed execution releases the key
// so retries re-execute.
func runIdempotentWinner(c *echo.Context, pool *pgxpool.Pool, keyID string, mapErr func(*echo.Context, error) error, exec idemExecutor) error {
	ctx := c.Request().Context()
	release := func() {
		_, _ = pool.Exec(ctx, `DELETE FROM idempotency_keys WHERE id = $1::uuid`, keyID)
	}

	status, payload, err := exec()
	if err != nil {
		release()
		return mapErr(c, err)
	}
	if status < 200 || status >= 300 {
		// Only successful responses are cached; anything else releases
		// the key and answers directly.
		release()
		return writeJSONBlob(c, status, payload)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		release()
		return WriteInternalError(c)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE idempotency_keys
		 SET status = 'completed', response_code = $2, response_body = $3
		 WHERE id = $1::uuid`,
		keyID, status, string(body)); err != nil {
		release()
		return WriteInternalError(c)
	}
	return c.JSONBlob(status, body)
}

// writeJSONBlob serializes payload with encoding/json and writes it with
// the given status. The idempotent paths use this (instead of c.JSON) so
// the stored bytes and the served bytes come from the same serializer —
// the replay is byte-identical by construction.
func writeJSONBlob(c *echo.Context, status int, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return WriteInternalError(c)
	}
	return c.JSONBlob(status, body)
}
