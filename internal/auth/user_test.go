package auth

// UpdateUserName tests (C6T7): display-name update trims, validates, and
// returns the refreshed user row. Real test database, no skips.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestUpdateUserName(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	email := uniqueEmail("profile-name")
	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email, name) VALUES ($1, $2) RETURNING id::text`,
		email, "Old Name").Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	// Happy path: trims and returns the refreshed row.
	u, err := UpdateUserName(ctx, pool, userID, "  New Name  ")
	if err != nil {
		t.Fatalf("UpdateUserName: %v", err)
	}
	if u.Name == nil || *u.Name != "New Name" {
		t.Fatalf("name = %v, want New Name", u.Name)
	}
	if u.Email != email {
		t.Fatalf("email = %q, want %q (must be untouched)", u.Email, email)
	}

	// Empty / whitespace → invalid.
	if _, err := UpdateUserName(ctx, pool, userID, "   "); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("blank name: err = %v, want ErrInvalidName", err)
	}
	// Over 100 chars → invalid.
	if _, err := UpdateUserName(ctx, pool, userID, strings.Repeat("x", 101)); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("long name: err = %v, want ErrInvalidName", err)
	}
	// Exactly 100 chars → fine.
	if _, err := UpdateUserName(ctx, pool, userID, strings.Repeat("y", 100)); err != nil {
		t.Fatalf("100-char name: %v", err)
	}
}
