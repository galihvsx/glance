package auth

// User profile management (C6T7): the authenticated user may update their
// own display name. Email is identity (OTP/OAuth) and is never editable
// here.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrInvalidName is returned when a display-name update is empty or too long.
var ErrInvalidName = errors.New("auth: invalid name")

// maxUserNameLen bounds the display name (users.name is TEXT; the cap is a
// UI sanity limit, not a schema constraint).
const maxUserNameLen = 100

// UpdateUserName sets the user's display name and returns the refreshed
// user row. Name is trimmed; empty or >100 chars is ErrInvalidName.
func UpdateUserName(ctx context.Context, pool *pgxpool.Pool, userID, name string) (*User, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxUserNameLen {
		return nil, ErrInvalidName
	}
	var u User
	err := pool.QueryRow(ctx, `
		UPDATE users SET name = $2, updated_at = now()
		 WHERE id = $1::uuid
		RETURNING id::text, email, name, avatar_url, is_active, is_admin,
		          last_login_at, created_at, updated_at`,
		userID, name).Scan(
		&u.ID, &u.Email, &u.Name, &u.AvatarURL,
		&u.IsActive, &u.IsAdmin, &u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("auth: update user name: %w", err)
	}
	return &u, nil
}
