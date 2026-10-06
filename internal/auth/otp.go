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
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	netmail "net/mail"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
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
	// otpMaxAttempts is the number of wrong guesses a code tolerates before
	// it is burned (spec §7).
	otpMaxAttempts = 5
	// sessionTTL is the session lifetime (spec §7: 30-day expiry).
	sessionTTL = 30 * 24 * time.Hour
	// otpEvent is the outbox event for the OTP email. The dispatcher only
	// claims the "email.*" namespace (spec §4 decision #6), so this name is
	// load-bearing — anything else would never be delivered.
	otpEvent = "email.otp"
)

// ErrRateLimited is returned when an OTP request exceeds its rate limit.
var ErrRateLimited = errors.New("auth: OTP rate limit exceeded")

// ErrInvalidCode is returned when an OTP verify fails for ANY reason —
// unknown email, expired code, consumed code, or wrong code. Callers must
// map it to a single generic 401 so the endpoint never reveals which case
// hit (no enumeration).
var ErrInvalidCode = errors.New("auth: invalid or expired code")

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

// VerifyOTP handles the verify half of passwordless login (spec §7): it
// checks the code against the newest outstanding, unexpired code for the
// email, and on success auto-provisions the user, creates a session, and
// consumes the code. It returns the opaque session token (hex of 32 random
// bytes); only the token's SHA-256 hash is stored.
//
// Candidate selection: Task 6 allows MULTIPLE outstanding codes per email,
// so the candidate is the NEWEST unconsumed, unexpired code (FOR UPDATE
// locks it, which also makes concurrent attempt counting atomic). On
// success, the matched code is consumed AND every other outstanding code
// for the email is invalidated — an older code must never stay valid after
// a newer one succeeded (code-confusion bypass).
//
// Every failure mode — unknown email, malformed email, expired code,
// consumed code, wrong code, burned code — returns ErrInvalidCode, so the
// caller can answer with one generic 401 (no enumeration).
func VerifyOTP(ctx context.Context, pool *pgxpool.Pool, cfg *config.Config, email, code, userAgent, ip string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if _, err := netmail.ParseAddress(email); err != nil {
		return "", ErrInvalidCode
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("auth: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var codeID int64
	var codeHash string
	err = tx.QueryRow(ctx, `
		SELECT id, code_hash FROM otp_codes
		WHERE email = $1 AND consumed_at IS NULL AND expires_at > now()
		ORDER BY created_at DESC, id DESC
		LIMIT 1 FOR UPDATE`,
		email,
	).Scan(&codeID, &codeHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrInvalidCode
		}
		return "", fmt.Errorf("auth: lookup otp code: %w", err)
	}

	// Constant-time comparison against the stored hash (spec §7). A corrupt
	// stored hash can never match.
	want, decErr := hex.DecodeString(codeHash)
	sum := sha256.Sum256([]byte(cfg.OTPPepper + code))
	if decErr != nil || subtle.ConstantTimeCompare(want, sum[:]) != 1 {
		// Wrong code: count the attempt atomically (the FOR UPDATE row
		// lock serializes concurrent verifies — no lost updates) and burn
		// the code on the 5th wrong attempt. The increment MUST be
		// committed, so this path commits before returning.
		if _, err := tx.Exec(ctx, `
			UPDATE otp_codes
			SET attempts = attempts + 1,
			    consumed_at = CASE WHEN attempts + 1 >= $2 THEN now() ELSE consumed_at END
			WHERE id = $1`,
			codeID, otpMaxAttempts); err != nil {
			return "", fmt.Errorf("auth: record attempt: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return "", fmt.Errorf("auth: commit attempt: %w", err)
		}
		return "", ErrInvalidCode
	}

	// Success: consume the matched code…
	if _, err := tx.Exec(ctx,
		`UPDATE otp_codes SET consumed_at = now() WHERE id = $1`, codeID); err != nil {
		return "", fmt.Errorf("auth: consume code: %w", err)
	}
	// …and invalidate every other outstanding code for this email.
	if _, err := tx.Exec(ctx,
		`UPDATE otp_codes SET consumed_at = now()
		 WHERE email = $1 AND consumed_at IS NULL AND id <> $2`,
		email, codeID); err != nil {
		return "", fmt.Errorf("auth: invalidate superseded codes: %w", err)
	}

	// Auto-provision the user on first successful verify (spec §7).
	var userID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO users (email) VALUES ($1)
		ON CONFLICT (email) DO UPDATE SET last_login_at = now(), updated_at = now()
		RETURNING id`,
		email,
	).Scan(&userID); err != nil {
		return "", fmt.Errorf("auth: provision user: %w", err)
	}

	// Opaque 32-byte session token (spec §7); only its SHA-256 hash is
	// stored — the plain token never touches the database.
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

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("auth: commit: %w", err)
	}
	return token, nil
}
