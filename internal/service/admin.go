package service

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrAdminSelfDemote guards the lockout: an admin cannot revoke their
	// own instance-admin flag. Without it a misclick could leave the
	// instance with admins only via GLANCE_ADMIN_EMAILS re-seeding.
	ErrAdminSelfDemote = errors.New("service: cannot demote your own admin status")
	// ErrAdminSelfDeactivate is the same guard for deactivation: killing
	// your own account mid-session would brick the instance's admin
	// access the same way.
	ErrAdminSelfDeactivate = errors.New("service: cannot deactivate your own account")
	// ErrAdminConfirmMismatch is returned when the typed workspace-name
	// confirmation does not exactly match the workspace being deleted.
	ErrAdminConfirmMismatch = errors.New("service: workspace name confirmation does not match")
)

// AdminStats is the instance-wide overview served by GET /api/v1/admin/stats.
type AdminStats struct {
	Users           int64 `json:"users"`
	Workspaces      int64 `json:"workspaces"`
	Projects        int64 `json:"projects"`
	Issues          int64 `json:"issues"`
	AttachmentBytes int64 `json:"attachment_bytes"`
}

// GetAdminStats returns instance-wide counts. One query with scalar
// subselects — the tables are small (instance admin traffic) and a
// single round-trip keeps the handler trivial.
func GetAdminStats(ctx context.Context, pool *pgxpool.Pool) (*AdminStats, error) {
	var s AdminStats
	err := pool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM users),
			(SELECT COUNT(*) FROM workspaces),
			(SELECT COUNT(*) FROM projects),
			(SELECT COUNT(*) FROM issues),
			(SELECT COALESCE(SUM(size_bytes), 0) FROM attachments)`,
	).Scan(&s.Users, &s.Workspaces, &s.Projects, &s.Issues, &s.AttachmentBytes)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// AdminUser is the instance-level user view for GET /api/v1/admin/users.
// workspace_count is the number of workspaces the user is a member of.
type AdminUser struct {
	ID             string    `json:"id"`
	Email          string    `json:"email"`
	Name           *string   `json:"name"`
	IsAdmin        bool      `json:"is_admin"`
	IsActive       bool      `json:"is_active"`
	WorkspaceCount int64     `json:"workspace_count"`
	CreatedAt      time.Time `json:"created_at"`
}

// ListAdminUsers returns users newest-last (created_at, id), paginated,
// plus the total row count for the page envelope.
func ListAdminUsers(ctx context.Context, pool *pgxpool.Pool, limit, offset int) ([]AdminUser, int64, error) {
	if limit < 1 {
		limit = 25
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	var total int64
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := pool.Query(ctx, `
		SELECT u.id::text, u.email, u.name, u.is_admin, u.is_active,
		       COUNT(m.workspace_id) AS workspace_count, u.created_at
		FROM users u
		LEFT JOIN workspace_members m ON m.user_id = u.id
		GROUP BY u.id
		ORDER BY u.created_at, u.id
		LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	users := []AdminUser{}
	for rows.Next() {
		var u AdminUser
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.IsAdmin, &u.IsActive,
			&u.WorkspaceCount, &u.CreatedAt); err != nil {
			return nil, 0, err
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return users, total, nil
}

// SetUserAdmin flips a user's instance-admin flag. Self-demotion is
// refused (ErrAdminSelfDemote); demoting the last remaining admin is
// refused (ErrLastAdmin — an instance with zero admins can only recover
// via GLANCE_ADMIN_EMAILS re-seeding, so the API refuses to create that
// state). Promotion is unrestricted. Unknown user → ErrUserNotFound.
// On success one audit row (user.role_changed) is recorded inside the
// same transaction as the flag flip.
func SetUserAdmin(ctx context.Context, pool *pgxpool.Pool, actorID, targetID string, isAdmin bool, ip string) error {
	if targetID == actorID && !isAdmin {
		return ErrAdminSelfDemote
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id = $1::uuid)`,
		targetID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrUserNotFound
	}
	if !isAdmin {
		var otherAdmins int
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM users WHERE is_admin AND id <> $1::uuid`,
			targetID).Scan(&otherAdmins); err != nil {
			return err
		}
		if otherAdmins == 0 {
			return ErrLastAdmin
		}
	}
	if _, err := tx.Exec(ctx,
		`UPDATE users SET is_admin = $2, updated_at = now() WHERE id = $1::uuid`,
		targetID, isAdmin); err != nil {
		return err
	}
	if err := recordAuditTx(ctx, tx, AuditInfo{ActorID: actorID, IP: ip},
		AuditActionUserRoleChanged, "user", targetID, "",
		map[string]any{"is_admin": isAdmin}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DeactivateUser disables an account: the user cannot log in again
// (VerifyOTP/CompleteOAuthLogin reject is_active=false; AuthenticateSession
// and token auth reject it too) and every live session is revoked here,
// so deactivation takes effect immediately — no waiting for expiry.
// Self-deactivation is refused (ErrAdminSelfDeactivate): see
// ErrAdminSelfDemote for the rationale. On success one audit row
// (user.deactivated) is recorded inside the same transaction.
func DeactivateUser(ctx context.Context, pool *pgxpool.Pool, actorID, targetID string, ip string) error {
	if targetID == actorID {
		return ErrAdminSelfDeactivate
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx,
		`UPDATE users SET is_active = false, updated_at = now()
		 WHERE id = $1::uuid AND is_active`,
		targetID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		// Either unknown or already deactivated: distinguish so the
		// handler can answer 404 vs 200-idempotent.
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id = $1::uuid)`,
			targetID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrUserNotFound
		}
	}
	// Revoke every live session — deactivation must not wait for the
	// 30-day session TTL. Idempotent: already-revoked rows are skipped.
	if _, err := tx.Exec(ctx,
		`UPDATE sessions SET revoked_at = now()
		 WHERE user_id = $1::uuid AND revoked_at IS NULL`,
		targetID); err != nil {
		return err
	}
	if err := recordAuditTx(ctx, tx, AuditInfo{ActorID: actorID, IP: ip},
		AuditActionUserDeactivated, "user", targetID, "", nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ReactivateUser re-enables a deactivated account. Idempotent: reactivating
// an active account is a no-op. Unknown user → ErrUserNotFound. (Sessions
// revoked at deactivation stay revoked — reactivation does not resurrect
// old tokens; the user logs in fresh.) One audit row (user.reactivated)
// is recorded inside the same transaction as the flip.
func ReactivateUser(ctx context.Context, pool *pgxpool.Pool, actorID, targetID string, ip string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx,
		`UPDATE users SET is_active = true, updated_at = now()
		 WHERE id = $1::uuid`,
		targetID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	if err := recordAuditTx(ctx, tx, AuditInfo{ActorID: actorID, IP: ip},
		AuditActionUserReactivated, "user", targetID, "", nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// AdminWorkspace is the instance-level workspace usage view for
// GET /api/v1/admin/workspaces.
type AdminWorkspace struct {
	ID           string    `json:"id"`
	Slug         string    `json:"slug"`
	Name         string    `json:"name"`
	MemberCount  int64     `json:"member_count"`
	ProjectCount int64     `json:"project_count"`
	IssueCount   int64     `json:"issue_count"`
	CreatedAt    time.Time `json:"created_at"`
}

// ListAdminWorkspaces returns every workspace with usage counts,
// paginated, plus the total row count for the page envelope.
func ListAdminWorkspaces(ctx context.Context, pool *pgxpool.Pool, limit, offset int) ([]AdminWorkspace, int64, error) {
	if limit < 1 {
		limit = 25
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	var total int64
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM workspaces`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := pool.Query(ctx, `
		SELECT w.id::text, w.slug, w.name,
		       (SELECT COUNT(*) FROM workspace_members m WHERE m.workspace_id = w.id),
		       (SELECT COUNT(*) FROM projects p WHERE p.workspace_id = w.id),
		       (SELECT COUNT(*) FROM issues i
		          JOIN projects p ON p.id = i.project_id
		         WHERE p.workspace_id = w.id),
		       w.created_at
		FROM workspaces w
		ORDER BY w.created_at, w.id
		LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []AdminWorkspace{}
	for rows.Next() {
		var w AdminWorkspace
		if err := rows.Scan(&w.ID, &w.Slug, &w.Name, &w.MemberCount,
			&w.ProjectCount, &w.IssueCount, &w.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// DeleteWorkspaceAsAdmin deletes a workspace by id or slug without any
// membership check — the caller is an instance admin, which outranks
// tenancy. The typed confirmation (confirmName must exactly equal the
// workspace's name) is enforced here, not just client-side: DELETE is
// destructive and cascades (projects, issues, members, attachments and
// everything under them via the FK graph). One audit row
// (workspace.deleted) is recorded inside the same transaction, before
// the DELETE — the audit row's workspace_id FK goes NULL on the delete
// (ON DELETE SET NULL) while entity_id keeps the workspace id as text.
func DeleteWorkspaceAsAdmin(ctx context.Context, pool *pgxpool.Pool, actorID, idOrSlug, confirmName string, ip string) error {
	// Resolve by id or slug without casting the input to uuid first —
	// a slug is never a valid uuid, and $1::uuid would error on it.
	var wsID, name string
	err := pool.QueryRow(ctx,
		`SELECT id::text, name FROM workspaces
		 WHERE id::text = $1 OR slug = $1
		 LIMIT 1`,
		idOrSlug,
	).Scan(&wsID, &name)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if confirmName != name {
		return ErrAdminConfirmMismatch
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := recordAuditTx(ctx, tx, AuditInfo{ActorID: actorID, IP: ip},
		AuditActionWorkspaceDeleted, "workspace", wsID, wsID,
		map[string]any{"name": name, "slug": idOrSlug}); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM workspaces WHERE id = $1::uuid`, wsID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
