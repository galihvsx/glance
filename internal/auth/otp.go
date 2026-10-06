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
	// otpVerifyMaxPerEmail / otpVerifyMaxPerIP bound the verify path.
	//
	// Task 28 decision (deferred product call from the Task 7 review): the
	// per-email verify budget is DELIBERATELY aligned with the per-code
	// attempt budget (otpMaxAttempts = 5) and the request budget
	// (otpMaxPerEmail = 5).
	//
	// TRUE INVARIANT (round-2 correction — the round-1 comment's
	// "mathematically impossible" claim was false): the verify:email:
	// budget is consumed if and only if the guess is evaluated against a
	// LIVE code row (the newest unconsumed, unexpired code for the email,
	// locked FOR UPDATE). The budget check sits AFTER the code lookup, so:
	//
	//   - Squat guesses — verify calls for an email with no live code row
	//     (nothing requested, expired, already consumed/burned) — return
	//     ErrInvalidCode WITHOUT consuming the email's budget. Five squat
	//     guesses/hr against a victim's email spend only the attacker's
	//     own verify:ip: budget; the victim's subsequent request + correct
	//     verify succeeds (pinned by
	//     TestVerifySquatGuessesDoNotConsumeEmailBudget). The round-1
	//     placement (budget check before the lookup) made this a cheap,
	//     repeatable login DoS — the 20→5 alignment alone did not fix it.
	//
	//   - Guesses that DO reach a live row consume one unit of the email
	//     budget AND one code attempt (spec §7's 5-attempt burn is
	//     untouched). Guesses rejected as over-budget return BEFORE the
	//     attempt increment, so they never consume code attempts (pinned by
	//     TestVerifyBudgetExhaustionDoesNotBurnFreshCode).
	//
	// NOT guaranteed by this mechanism: 5 wrong guesses against a LIVE
	// code still burn the code (spec §7 mandates the burn) and spend the
	// email's 5/hr verify budget, so the email cannot verify again until
	// the fixed window resets (worst case < 1h). The attacker's entire
	// hourly email budget buys at most one burned code — this is the
	// accepted round-1 tradeoff, and a later verify attempt surfaces
	// ErrRateLimited (429) rather than failing silently.
	//
	// Considered and rejected: dropping the 5-attempt burn (spec §7
	// mandates it); binding burns to the request IP (mobile/CGNAT IP churn
	// would lock out legitimate users).
	//
	// UX tradeoff, documented: a user who mistypes 5 codes in an hour is
	// locked out of verify until the fixed window resets (worst case < 1h).
	// Acceptable for a login endpoint; the alternative is a login DoS.
	otpVerifyMaxPerEmail = 5
	otpVerifyMaxPerIP    = 20
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

// rateQuerier is satisfied by *pgxpool.Pool and pgx.Tx — the budget
// check can run either outside a transaction (pool) or inside one (tx).
type rateQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// checkRateLimit applies a fixed-window counter from the rate_limits table.
// The increment is a single atomic statement, safe across instances.
// When run on a tx, the increment commits or rolls back with the tx —
// callers must account for that (a rolled-back increment is as if the
// check never ran, which is exactly right for rejected guesses).
func checkRateLimit(ctx context.Context, db rateQuerier, key string, limit int) error {
	var count int
	err := db.QueryRow(ctx, `
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

	// The request budget is keyed per (email, IP): an attacker burning
	// requests for a victim's email from their own IP spends only their
	// own (email, IP) bucket — the victim's bucket from their own IP is
	// untouched, so the victim can always request a fresh code (round-2
	// fix; pinned by TestRequestOTPPerEmailIPBucketIsolation). The
	// per-IP bucket (20/hr) remains the global anti-spam shield.
	if err := checkRateLimit(ctx, pool, "otp:emailip:"+email+":"+ip, otpMaxPerEmail); err != nil {
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

	// Throttle the verify path (Task 7 review fix). The verify:ip: budget
	// is the cheap pre-lookup shield: it bounds total guess volume per IP
	// (20/hr) before we touch the database.
	//
	// The verify:email: budget is deliberately NOT checked here. It is
	// consumed only when the guess reaches a live code row (see below) —
	// checking it before the lookup let attackers squat the victim's
	// entire budget with guesses against no live code (round-2 fix).
	// Key namespaces are distinct from the request endpoint ("verify:"
	// vs "otp:") so the two budgets never eat each other. Checked here —
	// inside VerifyOTP rather than the handler — so every caller gets the
	// protection.
	if err := checkRateLimit(ctx, pool, "verify:ip:"+ip, otpVerifyMaxPerIP); err != nil {
		return "", err
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
			// No live code row (nothing requested, expired, or already
			// consumed/burned): the guess is invalid, and — critically —
			// the email's verify budget is NOT consumed. This is the
			// round-2 fix: budget squatting is impossible because there
			// is no budget to squat without a live row.
			return "", ErrInvalidCode
		}
		return "", fmt.Errorf("auth: lookup otp code: %w", err)
	}

	// The guess reached a live code row: NOW consume the email's verify
	// budget. Over-budget guesses return here — after the lookup but
	// before the hash comparison and the attempt increment — so they never
	// consume code attempts.
	//
	// The check runs on the tx, not the pool: VerifyOTP already holds a
	// tx connection here (FOR UPDATE row lock), and acquiring a second
	// pool connection while holding the first deadlocks under pool
	// pressure (pgxpool default MaxConns is small; N concurrent verifies
	// would each hold one conn while waiting for another). On the tx the
	// increment is atomic with the attempt update — both commit or both
	// roll back — and a rejected (rolled-back) guess simply doesn't
	// consume budget, which is the desired semantics.
	if err := checkRateLimit(ctx, tx, "verify:email:"+email, otpVerifyMaxPerEmail); err != nil {
		return "", err
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

	// Session creation is shared with the OAuth callback (internal/auth/session.go)
	// so both login paths mint tokens identically.
	token, err := CreateSessionTx(ctx, tx, userID, userAgent, ip)
	if err != nil {
		return "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("auth: commit: %w", err)
	}
	return token, nil
}
