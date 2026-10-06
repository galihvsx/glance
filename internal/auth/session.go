package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CreateSessionTx mints an opaque 32-byte session token for userID inside tx
// and stores only its SHA-256 hash — the plain token never touches the
// database (spec §7: 30-day expiry). It runs inside the caller's transaction
// so session creation stays atomic with the login that produced it (OTP
// verify, OAuth callback).
func CreateSessionTx(ctx context.Context, tx pgx.Tx, userID, userAgent, ip string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("auth: generate session token: %w", err)
	}
	token := hex.EncodeToString(raw)
	tokenSum := sha256.Sum256([]byte(token))
	if _, err := tx.Exec(ctx, `
		INSERT INTO sessions (user_id, token_hash, user_agent, ip, expires_at)
		VALUES ($1, $2, $3, $4, $5)`,
		userID, hex.EncodeToString(tokenSum[:]), userAgent, ip, time.Now().Add(sessionTTL)); err != nil {
		return "", fmt.Errorf("auth: create session: %w", err)
	}
	return token, nil
}

// User is the authenticated principal. It is serialized directly by
// GET /api/v1/me, so its JSON shape is the API contract: nullable columns
// (name, avatar_url, last_login_at) surface as null, never as "".
type User struct {
	ID          string     `json:"id"`
	Email       string     `json:"email"`
	Name        *string    `json:"name"`
	AvatarURL   *string    `json:"avatar_url"`
	IsActive    bool       `json:"is_active"`
	LastLoginAt *time.Time `json:"last_login_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// AuthResult is the outcome of validating a session token: the user to
// inject into the request context plus the session row that produced it
// (needed for logout and self-revocation).
type AuthResult struct {
	User      *User
	SessionID string
}

// SessionInfo is the safe-to-serialize session view for the sessions list.
// The token hash is deliberately absent — it must never leave the server.
type SessionInfo struct {
	ID         string    `json:"id"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	IP         *string   `json:"ip"`
	UserAgent  *string   `json:"user_agent"`
}

// ErrSessionInvalid is returned when a session token is unknown, revoked,
// or expired. Callers must map it to a single generic 401 so the endpoint
// never reveals which case hit (no enumeration).
var ErrSessionInvalid = errors.New("auth: invalid session")

// lastSeenRefreshInterval bounds the last_seen_at write: the column is
// only rewritten when it is older than this, so hot requests skip the
// write entirely (the conditional UPDATE below is a no-op for them).
const lastSeenRefreshInterval = 5 * time.Minute

// AuthenticateSession validates a raw session token in a single round trip:
// it looks the session up by SHA-256(token), rejects revoked and expired
// rows, loads the user, and refreshes last_seen_at when stale. The refresh
// is a conditional CASE inside the same UPDATE, so fresh sessions cost no
// write. Any validation failure returns ErrSessionInvalid — callers must
// not distinguish the cases.
func AuthenticateSession(ctx context.Context, pool *pgxpool.Pool, token string) (AuthResult, error) {
	if token == "" {
		return AuthResult{}, ErrSessionInvalid
	}
	sum := sha256.Sum256([]byte(token))
	var (
		user      User
		sessionID string
	)
	err := pool.QueryRow(ctx, `
		WITH s AS (
			UPDATE sessions
			SET last_seen_at = CASE
				WHEN last_seen_at < now() - ($2 * INTERVAL '1 second')
				THEN now()
				ELSE last_seen_at
			END
			WHERE token_hash = $1
			  AND revoked_at IS NULL
			  AND expires_at > now()
			RETURNING id, user_id
		)
		SELECT s.id, u.id, u.email, u.name, u.avatar_url, u.is_active,
		       u.last_login_at, u.created_at, u.updated_at
		FROM s JOIN users u ON u.id = s.user_id`,
		hex.EncodeToString(sum[:]), lastSeenRefreshInterval.Seconds(),
	).Scan(
		&sessionID, &user.ID, &user.Email, &user.Name, &user.AvatarURL,
		&user.IsActive, &user.LastLoginAt, &user.CreatedAt, &user.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AuthResult{}, ErrSessionInvalid
		}
		return AuthResult{}, fmt.Errorf("auth: authenticate session: %w", err)
	}
	return AuthResult{User: &user, SessionID: sessionID}, nil
}

// RevokeSession marks a session revoked. It is idempotent: revoking an
// already-revoked or unknown session is a no-op, not an error.
func RevokeSession(ctx context.Context, pool *pgxpool.Pool, sessionID string) error {
	if _, err := pool.Exec(ctx,
		`UPDATE sessions SET revoked_at = now()
		 WHERE id = $1 AND revoked_at IS NULL`,
		sessionID); err != nil {
		return fmt.Errorf("auth: revoke session: %w", err)
	}
	return nil
}

// RevokeSessionForUser revokes one session, but only if it belongs to
// userID. It reports whether a row was actually revoked; false means the
// session does not exist, is already revoked, or belongs to someone else —
// callers map that to 404 without distinguishing the cases.
func RevokeSessionForUser(ctx context.Context, pool *pgxpool.Pool, userID, sessionID string) (bool, error) {
	tag, err := pool.Exec(ctx,
		`UPDATE sessions SET revoked_at = now()
		 WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`,
		sessionID, userID)
	if err != nil {
		return false, fmt.Errorf("auth: revoke session: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// ListSessions returns the user's active sessions, newest first. Revoked
// sessions are excluded — a settings page has no use for dead rows — and
// the token hash is never selected.
func ListSessions(ctx context.Context, pool *pgxpool.Pool, userID string) ([]SessionInfo, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, created_at, last_seen_at, ip, user_agent
		FROM sessions
		WHERE user_id = $1 AND revoked_at IS NULL
		ORDER BY created_at DESC`,
		userID)
	if err != nil {
		return nil, fmt.Errorf("auth: list sessions: %w", err)
	}
	defer rows.Close()
	sessions := []SessionInfo{}
	for rows.Next() {
		var s SessionInfo
		if err := rows.Scan(&s.ID, &s.CreatedAt, &s.LastSeenAt, &s.IP, &s.UserAgent); err != nil {
			return nil, fmt.Errorf("auth: list sessions: %w", err)
		}
		sessions = append(sessions, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("auth: list sessions: %w", err)
	}
	return sessions, nil
}
