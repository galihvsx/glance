package auth

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"glance/internal/config"
)

// IsBootstrapAdminEmail reports whether email appears on the instance's
// admin bootstrap list (GLANCE_ADMIN_EMAILS → config.AdminEmails,
// lowercased by config). A newly registered matching user starts as
// admin; this never demotes anyone. The comparison is case-insensitive
// for belt-and-braces even though config already lowercases.
func IsBootstrapAdminEmail(cfg *config.Config, email string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	for _, e := range cfg.AdminEmails {
		if e == email {
			return true
		}
	}
	return false
}

// SeedAdminEmails applies GLANCE_ADMIN_EMAILS at boot: every matching
// user row is promoted to is_admin=true. Idempotent (only flips
// is_admin=false rows) and never demotes. Returns the number of rows
// promoted, so the boot log can say something concrete.
func SeedAdminEmails(ctx context.Context, pool *pgxpool.Pool, emails []string) (int64, error) {
	if len(emails) == 0 {
		return 0, nil
	}
	// emails are pre-lowercased by config; lower(email) makes the match
	// explicit regardless of the CITEXT column's own case folding.
	tag, err := pool.Exec(ctx, `
		UPDATE users
		SET is_admin = true, updated_at = now()
		WHERE is_admin = false AND lower(email) = ANY($1)`,
		emails)
	if err != nil {
		return 0, fmt.Errorf("auth: seed admin emails: %w", err)
	}
	return tag.RowsAffected(), nil
}

// provisionUserTx auto-provisions the user on first login (spec §7):
// INSERT by email, or touch last_login_at on conflict. Callers must pass
// a lowercased, trimmed email.
//
// C5T0 admin seeding: a NEW row gets is_admin=true when the users table
// is empty (first user on the instance) or the email is on
// GLANCE_ADMIN_EMAILS (IsBootstrapAdminEmail). The NOT EXISTS subquery
// runs inside the INSERT, so the emptiness check is atomic with the
// insert in this transaction. The ON CONFLICT branch never touches
// is_admin — registration never demotes (or promotes) an existing user.
//
// mergeProfile controls the conflict branch: the OAuth path merges the
// provider's name/avatar into empty profile fields; the OTP path only
// touches last_login_at (it carries no profile data).
func provisionUserTx(ctx context.Context, tx pgx.Tx, cfg *config.Config, email string, name, avatarURL *string, mergeProfile bool) (userID string, isActive bool, err error) {
	conflict := `DO UPDATE SET last_login_at = now(), updated_at = now()`
	if mergeProfile {
		conflict = `DO UPDATE SET
			last_login_at = now(),
			updated_at = now(),
			name = COALESCE(NULLIF(users.name, ''), EXCLUDED.name),
			avatar_url = COALESCE(NULLIF(users.avatar_url, ''), EXCLUDED.avatar_url)`
	}
	query := `
		INSERT INTO users (email, name, avatar_url, is_admin)
		VALUES ($1, $2, $3, NOT EXISTS (SELECT 1 FROM users) OR $4)
		ON CONFLICT (email) ` + conflict + `
		RETURNING id, is_active`
	err = tx.QueryRow(ctx, query,
		email, nullIfEmpty(name), nullIfEmpty(avatarURL),
		IsBootstrapAdminEmail(cfg, email),
	).Scan(&userID, &isActive)
	if err != nil {
		return "", false, fmt.Errorf("auth: provision user: %w", err)
	}
	return userID, isActive, nil
}

// nullIfEmpty maps an empty/missing profile value to NULL so the
// INSERT stores SQL NULL (matching the pre-C5T0 behavior of
// nullString("") → nil in the OAuth path and plain-OTP rows).
func nullIfEmpty(s *string) any {
	if s == nil || *s == "" {
		return nil
	}
	return *s
}

// strPtr returns nil for an empty string, else a pointer to it — the
// *string dual of nullString for provisionUserTx callers.
func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
