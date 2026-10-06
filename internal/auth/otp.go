// Package auth implements glance's passwordless authentication: email OTP
// request/verify, OAuth account linking, and opaque session tokens.
//
// This system is passwordless by design — no password columns, no password
// fields, anywhere in schema or code.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	netmail "net/mail"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"glance/internal/config"
	glancemail "glance/internal/mail"
)

const (
	// otpTTL is how long a requested code stays valid (spec §7).
	otpTTL = 10 * time.Minute
	// otpRateLimitWindow is the fixed window for OTP rate limits (spec §7).
	otpRateLimitWindow = time.Hour
	otpMaxPerEmail     = 5
	otpMaxPerIP        = 20
	// otpEvent is the outbox event for the OTP email. The dispatcher only
	// claims the "email.*" namespace (spec §4 decision #6), so this name is
	// load-bearing — anything else would never be delivered.
	otpEvent = "email.otp"
)

// ErrRateLimited is returned when an OTP request exceeds its rate limit.
var ErrRateLimited = errors.New("auth: OTP rate limit exceeded")

// checkRateLimit applies a fixed-window counter from the rate_limits table.
// The increment is a single atomic statement, safe across instances.
func checkRateLimit(ctx context.Context, pool *pgxpool.Pool, key string, limit int) error {
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
		key, int(otpRateLimitWindow.Seconds()),
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("auth: rate limit check: %w", err)
	}
	if count > limit {
		return ErrRateLimited
	}
	return nil
}

// generateCode returns a random 6-digit code ("000000"–"999999").
func generateCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", fmt.Errorf("auth: generate OTP code: %w", err)
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// hashCode hashes the code with SHA-256 and the configured pepper. The plain
// code is never stored.
func hashCode(pepper, code string) string {
	sum := sha256.Sum256([]byte(pepper + code))
	return hex.EncodeToString(sum[:])
}

// RequestOTP handles the OTP request half of passwordless login (spec §7):
// it rate-limits, stores a hashed 6-digit code with a 10-minute expiry, and
// enqueues the delivery email — all without revealing whether the email is
// known. Unknown emails do NOT create user rows (provisioning happens at
// verify time).
//
// The otp_codes insert and the outbox enqueue happen in ONE transaction, so
// the email can never be sent for a code that wasn't stored, and a stored
// code never loses its email.
func RequestOTP(ctx context.Context, pool *pgxpool.Pool, cfg *config.Config, email, ip string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if _, err := netmail.ParseAddress(email); err != nil {
		// No enumeration: malformed addresses get the same silent success.
		return nil
	}

	if err := checkRateLimit(ctx, pool, "otp:email:"+email, otpMaxPerEmail); err != nil {
		return err
	}
	if err := checkRateLimit(ctx, pool, "otp:ip:"+ip, otpMaxPerIP); err != nil {
		return err
	}

	code, err := generateCode()
	if err != nil {
		return err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("auth: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`INSERT INTO otp_codes (email, code_hash, expires_at) VALUES ($1, $2, $3)`,
		email, hashCode(cfg.OTPPepper, code), time.Now().Add(otpTTL)); err != nil {
		return fmt.Errorf("auth: insert otp code: %w", err)
	}

	// R6: event MUST be "email.otp" — the dispatcher only claims the
	// email.* namespace. Enqueued in the SAME tx as the otp_codes row.
	msg := glancemail.Message{
		To:      email,
		Subject: "Your glance login code",
		Body:    fmt.Sprintf("Your glance login code is %s. It expires in 10 minutes.", code),
	}
	if err := glancemail.Enqueue(ctx, tx, otpEvent, msg); err != nil {
		return fmt.Errorf("auth: enqueue otp email: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("auth: commit: %w", err)
	}
	return nil
}
