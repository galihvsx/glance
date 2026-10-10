package auth

// Calendar feed tokens (C15T3): opaque per-user secrets that authenticate
// the calendar.ics subscription feeds via the ?token= query parameter (or
// Authorization: Bearer) — external calendar apps cannot present the
// session cookie, and the gl_ API-token middleware path is deliberately
// not reused: a feed token must never inherit API-token scope semantics
// or mint anything. Storage discipline mirrors api_tokens (Task 29):
// only the SHA-256 hash is persisted, the plaintext is shown exactly
// once at creation, and validation failures are indistinguishable.

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// FeedTokenPrefix marks a calendar feed token. It intentionally does NOT
// share TokenPrefix ("gl_"): a feed secret presented as a Bearer token to
// a RequireAuth route must not be mistaken for an API token.
const FeedTokenPrefix = "glcal_"

// ErrFeedTokenInvalid is returned for unknown, revoked, or inactive-owner
// feed tokens. Handlers map every case to a single generic 401 (no
// enumeration).
var ErrFeedTokenInvalid = errors.New("auth: invalid calendar feed token")

// feedTokenRateLimitBudget is the per-token budget for feed fetches,
// keyed by token hash in the shared rate_limits table (same fixed-window
// mechanism as API tokens). Calendar apps poll on 15–60 minute
// schedules; 600/minute is generous headroom against polling storms.
const feedTokenRateLimitBudget = 600

var feedTokenRateLimitWindow = time.Minute

// generateFeedToken mints a random feed secret: "glcal_" + base64url(32
// bytes) from crypto/rand. The hash column is UNIQUE — an insert
// collision surfaces as an error the caller may retry.
func generateFeedToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("auth: generate feed token: %w", err)
	}
	return FeedTokenPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

// CreatedFeedToken is the 201 response of POST /api/v1/me/calendar-token:
// the plaintext, shown exactly once. There is no endpoint that returns
// the plaintext again.
type CreatedFeedToken struct {
	Token     string    `json:"token"`
	CreatedAt time.Time `json:"created_at"`
}

// FeedTokenStatus is the safe-to-serialize view of the user's live feed
// token: timestamps only, never the secret or its hash. Active is false
// when the user has no live token.
type FeedTokenStatus struct {
	Active     bool       `json:"active"`
	CreatedAt  *time.Time `json:"created_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// CreateFeedToken mints a feed token for userID. Regenerate semantics:
// any existing live token is revoked first, so at most one live token
// per user exists (the partial unique index makes this race-safe). The
// plaintext is returned once in CreatedFeedToken.Token — callers must not
// persist it anywhere else.
func CreateFeedToken(ctx context.Context, pool *pgxpool.Pool, userID string) (*CreatedFeedToken, error) {
	plaintext, err := generateFeedToken()
	if err != nil {
		return nil, err
	}
	// Revoke-then-insert must run as two statements in one transaction:
	// a single-statement CTE keeps the old live row visible to the
	// INSERT's partial-unique-index check (same snapshot) and violates
	// idx_calendar_feed_tokens_live_user.
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("auth: create feed token: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		UPDATE calendar_feed_tokens
		SET revoked_at = now()
		WHERE user_id = $1::uuid AND revoked_at IS NULL`, userID); err != nil {
		return nil, fmt.Errorf("auth: create feed token: %w", err)
	}
	var created CreatedFeedToken
	if err := tx.QueryRow(ctx, `
		INSERT INTO calendar_feed_tokens (user_id, token_hash)
		VALUES ($1::uuid, $2)
		RETURNING created_at`,
		userID, hashToken(plaintext),
	).Scan(&created.CreatedAt); err != nil {
		return nil, fmt.Errorf("auth: create feed token: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("auth: create feed token: %w", err)
	}
	created.Token = plaintext
	return &created, nil
}

// GetFeedTokenStatus reports whether userID has a live feed token, with
// its timestamps. It never returns the secret or its hash.
func GetFeedTokenStatus(ctx context.Context, pool *pgxpool.Pool, userID string) (*FeedTokenStatus, error) {
	var status FeedTokenStatus
	err := pool.QueryRow(ctx, `
		SELECT created_at, last_used_at
		FROM calendar_feed_tokens
		WHERE user_id = $1::uuid AND revoked_at IS NULL`,
		userID,
	).Scan(&status.CreatedAt, &status.LastUsedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &FeedTokenStatus{Active: false}, nil
		}
		return nil, fmt.Errorf("auth: feed token status: %w", err)
	}
	status.Active = true
	return &status, nil
}

// RevokeFeedToken revokes userID's live feed token. It reports whether a
// row was actually revoked; false means no live token existed — callers
// map that to 404.
func RevokeFeedToken(ctx context.Context, pool *pgxpool.Pool, userID string) (bool, error) {
	tag, err := pool.Exec(ctx, `
		UPDATE calendar_feed_tokens SET revoked_at = now()
		WHERE user_id = $1::uuid AND revoked_at IS NULL`,
		userID)
	if err != nil {
		return false, fmt.Errorf("auth: revoke feed token: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// AuthenticateFeedToken validates a raw feed secret: prefix check, hash
// lookup, revoked rejection, owner-active check, per-token rate limit,
// and a bounded last_used_at refresh. Any validation failure returns
// ErrFeedTokenInvalid; budget exhaustion returns ErrTokenRateLimited.
func AuthenticateFeedToken(ctx context.Context, pool *pgxpool.Pool, token string) (*User, error) {
	if token == "" || len(token) <= len(FeedTokenPrefix) || token[:len(FeedTokenPrefix)] != FeedTokenPrefix {
		return nil, ErrFeedTokenInvalid
	}
	hash := hashToken(token)
	var (
		user     User
		lastUsed *time.Time
	)
	err := pool.QueryRow(ctx, `
		SELECT u.id, u.email, u.name, u.avatar_url,
		       u.is_active, u.is_admin, u.last_login_at, u.created_at, u.updated_at,
		       t.last_used_at
		FROM calendar_feed_tokens t JOIN users u ON u.id = t.user_id
		WHERE t.token_hash = $1
		  AND t.revoked_at IS NULL
		  AND u.is_active`,
		hash,
	).Scan(
		&user.ID, &user.Email, &user.Name, &user.AvatarURL,
		&user.IsActive, &user.IsAdmin, &user.LastLoginAt, &user.CreatedAt, &user.UpdatedAt,
		&lastUsed,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrFeedTokenInvalid
		}
		return nil, fmt.Errorf("auth: authenticate feed token: %w", err)
	}

	// Per-token rate limit, keyed by hash (the only token identifier the
	// server ever sees twice). Checked after auth so unknown tokens never
	// reach the counter.
	if err := checkTokenRateLimit(ctx, pool, "feed:"+hash, feedTokenRateLimitBudget, feedTokenRateLimitWindow); err != nil {
		return nil, err
	}

	// Bounded last_used_at refresh: write at most once per interval.
	if lastUsed == nil || time.Since(*lastUsed) > lastUsedRefreshInterval {
		if _, err := pool.Exec(ctx,
			`UPDATE calendar_feed_tokens SET last_used_at = now() WHERE token_hash = $1`, hash); err != nil {
			return nil, fmt.Errorf("auth: refresh feed token last_used_at: %w", err)
		}
	}

	return &user, nil
}
