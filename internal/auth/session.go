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
	ID        string  `json:"id"`
	Email     string  `json:"email"`
	Name      *string `json:"name"`
	AvatarURL *string `json:"avatar_url"`
	IsActive  bool    `json:"is_active"`
	// IsAdmin marks instance administrators (000025, C5T0): the only
	// principals allowed behind /api/v1/admin/*. Serialized on /me so
	// the SPA can gate the admin UI without a second round-trip.
	IsAdmin     bool       `json:"is_admin"`
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
var ErrUserDeactivated = errors.New("auth: user account is deactivated")

// lastSeenRefreshInterval bounds the last_seen_at write: the column is
// only rewritten when it is older than this, so the hot path costs one
// read and zero writes — AuthenticateSession issues the UPDATE only for
// stale sessions (see below).
const lastSeenRefreshInterval = 5 * time.Minute

// AuthenticateSession validates a raw session token: it looks the session
// up by SHA-256(token), rejects revoked and expired rows, loads the user,
// and refreshes last_seen_at when stale. The refresh is a separate UPDATE
// issued only when the value is older than lastSeenRefreshInterval, so
// fresh sessions cost a single read and no write at all — no row lock, no
// new tuple version, no WAL on the hottest middleware path. Concurrent
// stale refreshes are idempotent (each writes now()), so the read-then-
// write split is race-safe. Any validation failure returns
// ErrSessionInvalid — callers must not distinguish the cases.
func AuthenticateSession(ctx context.Context, pool *pgxpool.Pool, token string) (AuthResult, error) {
	if token == "" {
		return AuthResult{}, ErrSessionInvalid
	}
	sum := sha256.Sum256([]byte(token))
	var (
		user       User
		sessionID  string
		lastSeenAt time.Time
	)
	err := pool.QueryRow(ctx, `
		SELECT s.id, s.last_seen_at, u.id, u.email, u.name, u.avatar_url,
		       u.is_active, u.is_admin, u.last_login_at, u.created_at, u.updated_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1
		  AND s.revoked_at IS NULL
		  AND s.expires_at > now()`,
		hex.EncodeToString(sum[:]),
	).Scan(
		&sessionID, &lastSeenAt,
		&user.ID, &user.Email, &user.Name, &user.AvatarURL,
		&user.IsActive, &user.IsAdmin, &user.LastLoginAt, &user.CreatedAt, &user.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AuthResult{}, ErrSessionInvalid
		}
		return AuthResult{}, fmt.Errorf("auth: authenticate session: %w", err)
	}
	// A deactivated user keeps no valid sessions: the session dies with the
	// account. Indistinguishable from an invalid session (no account-state
	// oracle for token holders).
	if !user.IsActive {
		return AuthResult{}, ErrSessionInvalid
	}
	if time.Since(lastSeenAt) > lastSeenRefreshInterval {
		if _, err := pool.Exec(ctx,
			`UPDATE sessions SET last_seen_at = now() WHERE id = $1`,
			sessionID); err != nil {
			return AuthResult{}, fmt.Errorf("auth: refresh last_seen_at: %w", err)
		}
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
