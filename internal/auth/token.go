package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TokenPrefix marks a glance API token. The middleware routes any
// Authorization: Bearer value carrying this prefix into token auth;
// other Bearer values fall back to session-cookie auth.
const TokenPrefix = "gl_"

// Scope vocabulary (Task 29). Minimal by design: read suffices for GETs,
// write is required for mutating routes, and write implies read.
const (
	ScopeRead  = "read"
	ScopeWrite = "write"
)

// validScopes is the closed vocabulary POST /api/v1/tokens validates
// against. There is deliberately no "admin" — tokens inherit the owner's
// workspace roles, never a global privilege.
var validScopes = map[string]bool{ScopeRead: true, ScopeWrite: true}

// tokenRateLimitBudget is the default per-token budget: 600 requests per
// minute, keyed by token hash in the shared rate_limits table (the same
// fixed-window mechanism as the OTP limiter). A token's rate_limit column
// may override this in the future; NULL means this default.
const tokenRateLimitBudget = 600

var tokenRateLimitWindow = time.Minute

// ErrTokenInvalid is returned when an API token is unknown, revoked, or
// expired. Callers must map it to a single generic 401 so the endpoint
// never reveals which case hit (no enumeration).
var ErrTokenInvalid = errors.New("auth: invalid api token")

// ErrTokenRateLimited is returned when a token exceeds its per-token
// budget. Callers map it to 429.
var ErrTokenRateLimited = errors.New("auth: api token rate limit exceeded")

// ErrInvalidScope is returned when token creation names a scope outside
// the vocabulary.
var ErrInvalidScope = errors.New("auth: invalid scope")

// lastUsedRefreshInterval bounds the last_used_at write, mirroring the
// session last_seen_at discipline: the column is only rewritten when older
// than this, so the hot middleware path costs one read plus the rate-limit
// upsert, and no token-row write on most requests.
const lastUsedRefreshInterval = 5 * time.Minute

// GenerateToken mints a random token: "gl_" + base64url(32 bytes).
// The 32 bytes come from crypto/rand; collisions are not checked because
// token_hash is UNIQUE — an insert collision surfaces as an error the
// caller may retry.
func GenerateToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("auth: generate token: %w", err)
	}
	return TokenPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

// hashToken returns the SHA-256 hex of the raw token — the value stored in
// api_tokens.token_hash. The plaintext never touches the database.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Token is the safe-to-serialize view of an API token: no hash, no
// plaintext. It is what GET /api/v1/tokens returns.
type Token struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Scopes     []string   `json:"scopes"`
	ExpiresAt  *time.Time `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

// CreatedToken is the 201 response of POST /api/v1/tokens: the Token view
// plus the plaintext, shown exactly once. There is no endpoint that
// returns the plaintext again.
type CreatedToken struct {
	Token
	Plaintext string `json:"token"`
}

// TokenAuth is the outcome of validating a bearer token: the user to
// inject into the request context plus the token row that produced it
// (id and scopes, for scope enforcement and auditing).
type TokenAuth struct {
	User    *User
	TokenID string
	Scopes  []string
}

// HasScope reports whether the token grants scope. Write implies read;
// unknown scopes never match.
func (t *TokenAuth) HasScope(scope string) bool {
	for _, s := range t.Scopes {
		if s == scope {
			return true
		}
		if s == ScopeWrite && scope == ScopeRead {
			return true
		}
	}
	return false
}

// validateScopes rejects empty or unknown scope lists.
func validateScopes(scopes []string) error {
	if len(scopes) == 0 {
		return fmt.Errorf("%w: at least one scope is required", ErrInvalidScope)
	}
	for _, s := range scopes {
		if !validScopes[s] {
			return fmt.Errorf("%w: %q", ErrInvalidScope, s)
		}
	}
	return nil
}

// CreateToken mints a token for userID and stores only its hash. name must
// be non-empty; scopes must come from the vocabulary; expiresAt may be nil
// (no expiry) or a future time. The plaintext is returned once in
// CreatedToken.Plaintext — callers must not persist it anywhere else.
func CreateToken(ctx context.Context, pool *pgxpool.Pool, userID, name string, scopes []string, expiresAt *time.Time) (*CreatedToken, error) {
	if name == "" {
		return nil, fmt.Errorf("%w: name is required", ErrInvalidScope)
	}
	if err := validateScopes(scopes); err != nil {
		return nil, err
	}
	if expiresAt != nil && !expiresAt.After(time.Now()) {
		return nil, fmt.Errorf("%w: expires_at must be in the future", ErrInvalidScope)
	}
	plaintext, err := GenerateToken()
	if err != nil {
		return nil, err
	}
	var created CreatedToken
	err = pool.QueryRow(ctx, `
		INSERT INTO api_tokens (user_id, name, token_hash, scopes, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id::text, name, scopes, expires_at, last_used_at, created_at`,
		userID, name, hashToken(plaintext), scopes, expiresAt,
	).Scan(
		&created.ID, &created.Name, &created.Scopes,
		&created.ExpiresAt, &created.LastUsedAt, &created.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("auth: create token: %w", err)
	}
	created.Plaintext = plaintext
	return &created, nil
}

// AuthenticateToken validates a raw bearer token: prefix check, hash
// lookup, revoked/expiry rejection, user load, per-token rate limit, and
// a bounded last_used_at refresh. Any validation failure returns
// ErrTokenInvalid; budget exhaustion returns ErrTokenRateLimited.
func AuthenticateToken(ctx context.Context, pool *pgxpool.Pool, token string) (*TokenAuth, error) {
	if token == "" || len(token) <= len(TokenPrefix) || token[:len(TokenPrefix)] != TokenPrefix {
		return nil, ErrTokenInvalid
	}
	hash := hashToken(token)
	var (
		user      User
		auth      TokenAuth
		lastUsed  *time.Time
		rateLimit *int
	)
	err := pool.QueryRow(ctx, `
		SELECT t.id, t.scopes, t.last_used_at, t.rate_limit,
		       u.id, u.email, u.name, u.avatar_url,
		       u.is_active, u.is_admin, u.last_login_at, u.created_at, u.updated_at
		FROM api_tokens t JOIN users u ON u.id = t.user_id
		WHERE t.token_hash = $1
		  AND t.revoked_at IS NULL
		  AND (t.expires_at IS NULL OR t.expires_at > now())
		  AND u.is_active`,
		hash,
	).Scan(
		&auth.TokenID, &auth.Scopes, &lastUsed, &rateLimit,
		&user.ID, &user.Email, &user.Name, &user.AvatarURL,
		&user.IsActive, &user.IsAdmin, &user.LastLoginAt, &user.CreatedAt, &user.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTokenInvalid
		}
		return nil, fmt.Errorf("auth: authenticate token: %w", err)
	}

	// Per-token rate limit, keyed by hash (the only token identifier the
	// server ever sees twice). Checked after auth so attackers cannot burn
	// someone else's budget with a guessed prefix — unknown tokens never
	// reach the counter.
	budget := tokenRateLimitBudget
	if rateLimit != nil && *rateLimit > 0 {
		budget = *rateLimit
	}
	if err := checkTokenRateLimit(ctx, pool, "token:"+hash, budget, tokenRateLimitWindow); err != nil {
		return nil, err
	}

	// Bounded last_used_at refresh: write at most once per interval.
	if lastUsed == nil || time.Since(*lastUsed) > lastUsedRefreshInterval {
		if _, err := pool.Exec(ctx,
			`UPDATE api_tokens SET last_used_at = now() WHERE id = $1::uuid`, auth.TokenID); err != nil {
			return nil, fmt.Errorf("auth: refresh token last_used_at: %w", err)
		}
	}

	auth.User = &user
	return &auth, nil
}

// ListTokens returns the user's live tokens, newest first — without hashes
// or plaintext. Revoked tokens are excluded.
func ListTokens(ctx context.Context, pool *pgxpool.Pool, userID string) ([]Token, error) {
	rows, err := pool.Query(ctx, `
		SELECT id::text, name, scopes, expires_at, last_used_at, created_at
		FROM api_tokens
		WHERE user_id = $1::uuid AND revoked_at IS NULL
		ORDER BY created_at DESC`,
		userID)
	if err != nil {
		return nil, fmt.Errorf("auth: list tokens: %w", err)
	}
	defer rows.Close()
	tokens := []Token{}
	for rows.Next() {
		var t Token
		if err := rows.Scan(&t.ID, &t.Name, &t.Scopes, &t.ExpiresAt, &t.LastUsedAt, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("auth: list tokens: %w", err)
		}
		tokens = append(tokens, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("auth: list tokens: %w", err)
	}
	return tokens, nil
}

// RevokeToken marks a token revoked, but only if it belongs to userID.
// It reports whether a row was actually revoked; false means the token
// does not exist, is already revoked, or belongs to someone else —
// callers map that to 404 without distinguishing the cases.
func RevokeToken(ctx context.Context, pool *pgxpool.Pool, userID, tokenID string) (bool, error) {
	tag, err := pool.Exec(ctx, `
		UPDATE api_tokens SET revoked_at = now()
		WHERE id = $1::uuid AND user_id = $2::uuid AND revoked_at IS NULL`,
		tokenID, userID)
	if err != nil {
		return false, fmt.Errorf("auth: revoke token: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// checkTokenRateLimit applies a fixed-window counter from the shared
// rate_limits table — the same atomic single-statement mechanism as the
// OTP limiter, parameterized by window so token budgets stay independent.
func checkTokenRateLimit(ctx context.Context, pool *pgxpool.Pool, key string, limit int, window time.Duration) error {
	var count int
	err := pool.QueryRow(ctx, `
		INSERT INTO rate_limits (key, window_start, count)
		VALUES ($1, now(), 1)
		ON CONFLICT (key) DO UPDATE SET
			window_start = CASE
				WHEN rate_limits.window_start <= now() - ($2 * INTERVAL '1 second')
				THEN now() ELSE rate_limits.window_start END,
			count = CASE
				WHEN rate_limits.window_start <= now() - ($2 * INTERVAL '1 second')
				THEN 1 ELSE rate_limits.count + 1 END
		RETURNING count`,
		key, int(window.Seconds()),
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("auth: token rate limit check: %w", err)
	}
	if count > limit {
		return ErrTokenRateLimited
	}
	return nil
}
