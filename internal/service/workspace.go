// Package service holds glance's domain logic: one file per aggregate,
// raw pgx SQL, sentinel errors the API layer maps to HTTP statuses.
package service

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Workspace membership roles. Higher wins; only RoleAdmin (20) manages
// members or renames the workspace. Levels are spaced (not 1/2/3) so a
// future intermediate role slots in without renumbering.
const (
	RoleGuest  = 5
	RoleMember = 15
	RoleAdmin  = 20
)

var (
	ErrNotFound     = errors.New("service: not found")
	ErrForbidden    = errors.New("service: forbidden")
	ErrSlugConflict = errors.New("service: slug already taken")
	ErrInvalidSlug  = errors.New("service: invalid slug")
	ErrInvalidRole  = errors.New("service: invalid role")
	// ErrLastAdmin guards the lockout: the final admin of a workspace can
	// neither be removed nor demoted. Without it an admin could orphan a
	// workspace with no one able to manage it.
	ErrLastAdmin = errors.New("service: cannot remove or demote the last admin")
	// ErrNameRequired is returned when a workspace name is empty.
	ErrNameRequired = errors.New("service: name is required")
)

// Workspace is a tenant: a named container addressed by its slug.
type Workspace struct {
	ID        string    `json:"id"`
	Slug      string    `json:"slug"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// WorkspaceMembership pairs a workspace with the caller's role in it, so
// list views can gate admin-only UI without a second request per row.
type WorkspaceMembership struct {
	Workspace
	Role int `json:"role"`
}

// slugRe enforces the slug contract from the brief: lowercase alphanumeric
// plus hyphens, no leading/trailing/double hyphens, max 64 chars.
var slugRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func validSlug(s string) bool {
	return len(s) >= 1 && len(s) <= 64 && slugRe.MatchString(s)
}

func validRole(r int) bool {
	return r == RoleGuest || r == RoleMember || r == RoleAdmin
}

// isUniqueViolation reports Postgres 23505 (unique constraint). Used to
// translate a slug race into ErrSlugConflict instead of a 500.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// CreateWorkspace inserts the workspace and its first member (the creator
// as admin) in one transaction — a workspace without an admin must never
// exist, even briefly.
func CreateWorkspace(ctx context.Context, pool *pgxpool.Pool, name, slug, creatorID string) (*Workspace, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrNameRequired
	}
	if !validSlug(slug) {
		return nil, ErrInvalidSlug
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var ws Workspace
	err = tx.QueryRow(ctx,
		`INSERT INTO workspaces (slug, name) VALUES ($1, $2)
		 RETURNING id::text, slug::text, name, created_at, updated_at`,
		slug, name).Scan(&ws.ID, &ws.Slug, &ws.Name, &ws.CreatedAt, &ws.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrSlugConflict
		}
		return nil, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role)
		 VALUES ($1::uuid, $2::uuid, $3)`,
		ws.ID, creatorID, RoleAdmin); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &ws, nil
}

// ListWorkspaces returns every workspace the user belongs to with their
// role in each, ordered by name. Non-members' workspaces never appear —
// the join is the filter.
func ListWorkspaces(ctx context.Context, pool *pgxpool.Pool, userID string) ([]WorkspaceMembership, error) {
	rows, err := pool.Query(ctx,
		`SELECT w.id::text, w.slug::text, w.name, w.created_at, w.updated_at, m.role
		 FROM workspaces w
		 JOIN workspace_members m ON m.workspace_id = w.id
		 WHERE m.user_id = $1::uuid
		 ORDER BY w.name`,
		userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []WorkspaceMembership{}
	for rows.Next() {
		var wm WorkspaceMembership
		if err := rows.Scan(&wm.ID, &wm.Slug, &wm.Name, &wm.CreatedAt, &wm.UpdatedAt, &wm.Role); err != nil {
			return nil, err
		}
		out = append(out, wm)
	}
	return out, rows.Err()
}

// GetWorkspace returns the workspace plus the caller's role. A missing
// workspace and a non-member both yield ErrNotFound: the endpoint must
// never reveal that a slug exists to someone outside it.
func GetWorkspace(ctx context.Context, pool *pgxpool.Pool, slug, userID string) (*Workspace, int, error) {
	var ws Workspace
	var role int
	err := pool.QueryRow(ctx,
		`SELECT w.id::text, w.slug::text, w.name, w.created_at, w.updated_at, m.role
		 FROM workspaces w
		 JOIN workspace_members m ON m.workspace_id = w.id
		 WHERE w.slug = $1 AND m.user_id = $2::uuid`,
		slug, userID).Scan(&ws.ID, &ws.Slug, &ws.Name, &ws.CreatedAt, &ws.UpdatedAt, &role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, 0, ErrNotFound
		}
		return nil, 0, err
	}
	return &ws, role, nil
}

// UpdateWorkspace renames the workspace. Admin only. The role check and
// the rename run in one transaction so a concurrent demotion cannot slip
// between them.
func UpdateWorkspace(ctx context.Context, pool *pgxpool.Pool, slug, userID, name string) (*Workspace, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrNameRequired
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var ws Workspace
	var role int
	err = tx.QueryRow(ctx,
		`SELECT w.id::text, w.slug::text, w.name, w.created_at, w.updated_at, m.role
		 FROM workspaces w
		 JOIN workspace_members m ON m.workspace_id = w.id
		 WHERE w.slug = $1 AND m.user_id = $2::uuid`,
		slug, userID).Scan(&ws.ID, &ws.Slug, &ws.Name, &ws.CreatedAt, &ws.UpdatedAt, &role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if role != RoleAdmin {
		return nil, ErrForbidden
	}
	err = tx.QueryRow(ctx,
		`UPDATE workspaces SET name = $1, updated_at = now()
		 WHERE id = $2::uuid
		 RETURNING id::text, slug::text, name, created_at, updated_at`,
		name, ws.ID).Scan(&ws.ID, &ws.Slug, &ws.Name, &ws.CreatedAt, &ws.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &ws, nil
}

// workspaceIDForActor scans the id+role row produced by the caller's
// actor-resolution query; non-members get ErrNotFound (see GetWorkspace).
func workspaceIDForActor(row pgx.Row) (string, int, error) {
	var wsID string
	var role int
	if err := row.Scan(&wsID, &role); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", 0, ErrNotFound
		}
		return "", 0, err
	}
	return wsID, role, nil
}

// adminCount returns the number of admins in the workspace. Read inside
// the caller's transaction so the check-and-mutate pair is atomic.
func adminCount(ctx context.Context, tx pgx.Tx, wsID string) (int, error) {
	var n int
	err := tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM workspace_members WHERE workspace_id = $1::uuid AND role = $2`,
		wsID, RoleAdmin).Scan(&n)
	return n, err
}

// UpsertMember adds a member or changes their role. Only an admin may call
// it. Demoting the last admin is refused (ErrLastAdmin).
func UpsertMember(ctx context.Context, pool *pgxpool.Pool, slug, actorID, targetUserID string, role int) error {
	if !validRole(role) {
		return ErrInvalidRole
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	wsID, actorRole, err := workspaceIDForActor(
		tx.QueryRow(ctx,
			`SELECT w.id::text, m.role
			 FROM workspaces w
			 JOIN workspace_members m ON m.workspace_id = w.id
			 WHERE w.slug = $1 AND m.user_id = $2::uuid`,
			slug, actorID))
	if err != nil {
		return err
	}
	if actorRole != RoleAdmin {
		return ErrForbidden
	}

	// Lockout guard: demoting an admin below 20 when they are the last one.
	var oldRole int
	rowErr := tx.QueryRow(ctx,
		`SELECT role FROM workspace_members WHERE workspace_id = $1::uuid AND user_id = $2::uuid`,
		wsID, targetUserID).Scan(&oldRole)
	if rowErr != nil && !errors.Is(rowErr, pgx.ErrNoRows) {
		return rowErr
	}
	if rowErr == nil && oldRole == RoleAdmin && role != RoleAdmin {
		n, err := adminCount(ctx, tx, wsID)
		if err != nil {
			return err
		}
		if n <= 1 {
			return ErrLastAdmin
		}
	}

	// The target must be a real user — otherwise the FK would 500. Map it
	// to ErrNotFound instead.
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id = $1::uuid)`,
		targetUserID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role)
		 VALUES ($1::uuid, $2::uuid, $3)
		 ON CONFLICT (workspace_id, user_id) DO UPDATE SET role = EXCLUDED.role`,
		wsID, targetUserID, role)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RemoveMember drops a membership. Only an admin may call it; removing the
// last admin is refused (ErrLastAdmin). Removing a non-member is ErrNotFound.
func RemoveMember(ctx context.Context, pool *pgxpool.Pool, slug, actorID, targetUserID string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	wsID, actorRole, err := workspaceIDForActor(
		tx.QueryRow(ctx,
			`SELECT w.id::text, m.role
			 FROM workspaces w
			 JOIN workspace_members m ON m.workspace_id = w.id
			 WHERE w.slug = $1 AND m.user_id = $2::uuid`,
			slug, actorID))
	if err != nil {
		return err
	}
	if actorRole != RoleAdmin {
		return ErrForbidden
	}

	var targetRole int
	err = tx.QueryRow(ctx,
		`SELECT role FROM workspace_members WHERE workspace_id = $1::uuid AND user_id = $2::uuid`,
		wsID, targetUserID).Scan(&targetRole)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if targetRole == RoleAdmin {
		n, err := adminCount(ctx, tx, wsID)
		if err != nil {
			return err
		}
		if n <= 1 {
			return ErrLastAdmin
		}
	}

	if _, err := tx.Exec(ctx,
		`DELETE FROM workspace_members WHERE workspace_id = $1::uuid AND user_id = $2::uuid`,
		wsID, targetUserID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
