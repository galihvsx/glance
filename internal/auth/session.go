package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
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
