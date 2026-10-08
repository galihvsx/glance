package auth

// is_active enforcement tests (T9 follow-up): a deactivated user must not
// hold valid sessions and must not be able to log in again.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestDeactivatedUserSessionRejected(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)

	email := uniqueEmail("deact-session")
	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email, is_active) VALUES ($1, false) RETURNING id`,
		strings.ToLower(email)).Scan(&userID); err != nil {
		t.Fatalf("insert inactive user: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	token, err := CreateSessionTx(ctx, tx, userID, "test-agent", "127.0.0.1")
	if err != nil {
		t.Fatalf("mint session: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if _, err := AuthenticateSession(ctx, pool, token); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("AuthenticateSession on deactivated user: err = %v, want ErrSessionInvalid", err)
	}
}

func TestDeactivatedUserCannotVerifyOTP(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	cfg := testConfig()

	email := uniqueEmail("deact-otp")
	if _, err := pool.Exec(ctx,
		`INSERT INTO users (email, is_active) VALUES ($1, false)`,
		strings.ToLower(email)); err != nil {
		t.Fatalf("insert inactive user: %v", err)
	}
	insertTestCode(t, pool, email, "123456", 0)

	if _, err := VerifyOTP(ctx, pool, cfg, email, "123456", "test-agent", uniqueIP()); !errors.Is(err, ErrUserDeactivated) {
		t.Fatalf("VerifyOTP for deactivated user: err = %v, want ErrUserDeactivated", err)
	}
}
