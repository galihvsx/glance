package service

// Project members (C92T0, migration 000078): direct per-project
// membership mirroring the workspace ladder — admin (20) / member
// (15) / guest (5). Direct membership is checked FIRST in the
// effective-access order (project_access.go), ahead of
// teamspace-derived access and the public fallback, so a direct guest
// row keeps a workspace member read-only on that project even when
// they hold a teamspace seat, and a direct admin row grants project
// admin powers without workspace admin powers.
//
// Authz (design rule 3):
//   - Reading the roster: any caller with effective access to the
//     project (a private project the caller cannot reach is
//     ErrNotFound, never the roster).
//   - Managing membership (add / remove / change role): effective
//     project Admin (20) — workspace Admin or direct project Admin —
//     only. Anyone else gets ErrForbidden on a project they can
//     reach, ErrNotFound on a private project they cannot.
//   - Add target must already be a workspace member (same rule as
//     teamspace adds, C90): anything else — unknown id, malformed
//     id, non-member — is ErrProjectTargetNotMember (400), one error
//     for all three cases, so no existence oracle.
//   - A project Admin may not remove themselves if they are the last
//     project Admin (ErrLastProjectAdmin, 409): the same guard shape
//     as the workspace last-admin guards. Removing the last admin
//     via another admin's remove call is likewise refused — the
//     guard counts remaining project admins, not just self-removal.
//     (Workspace Admins always retain access regardless, so a
//     project with zero direct admins is still administrable —
//     but the guard keeps the direct-admin roster non-empty by
//     construction, matching the workspace invariant.)
//   - Roles are validated with the workspace ladder (validRole):
//     only 5 / 10 / 15 / 20 pass — note the project ladder names
//     admin/member/guest, but the stored values are the workspace
//     ladder values, so viewer (10) is storable and behaves as a
//     read role below member; the API documents admin/member/guest
//     and the change-role path accepts any valid ladder value.
//   - Duplicate adds are idempotent OK and return the existing row
//     (the teamspace add precedent); removing a non-member is
//     idempotent OK (the teamspace remove precedent).

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrProjectTargetNotMember is returned by AddProjectMember when
	// the target user_id is not a workspace member — including when
	// it matches no user at all or is malformed. One error for all
	// cases, so the caller never learns whether an arbitrary user id
	// exists. The handler maps it to 400 bad_request.
	ErrProjectTargetNotMember = errors.New("service: target user is not a workspace member")
	// ErrProjectMemberNotFound is returned by ChangeProjectMemberRole
	// when the target holds no direct membership row. Removes are
	// idempotent OK and do not use this error.
	ErrProjectMemberNotFound = errors.New("service: project member not found")
	// ErrLastProjectAdmin is returned when a remove or demotion
	// would leave the project with zero direct admins.
	ErrLastProjectAdmin = errors.New("service: cannot remove the last project admin")
	// ErrProjectMemberRoleInvalid is returned by Add/Change when the
	// role is not on the workspace ladder.
	ErrProjectMemberRoleInvalid = errors.New("service: invalid project member role")
)

// ProjectMember is one direct project member with their user
// identity, for the members roster. Role is the stored ladder value.
type ProjectMember struct {
	UserID string  `json:"user_id"`
	Name   *string `json:"name,omitempty"`
	Email  string  `json:"email"`
	Role   int     `json:"role"`
}

// resolveProjectMemberScope resolves (wsID, projectID, effectiveRole)
// for a project-members operation: tenancy first (bad slug /
// non-member -> ErrNotFound), then the project (missing ->
// ErrProjectNotFound), then effective access (private + no access ->
// ErrNotFound). Management callers additionally require
// effectiveRole == RoleAdmin.
func resolveProjectMemberScope(ctx context.Context, q queryRower, wsSlug, identifier, actorID string) (wsID, projectID string, effRole int, err error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return "", "", 0, err
	}
	wsID, wsRole, err := workspaceIDForActor(q.QueryRow(ctx,
		`SELECT w.id::text, m.role
		 FROM workspaces w
		 JOIN workspace_members m ON m.workspace_id = w.id
		 WHERE w.slug = $1 AND m.user_id = $2::uuid`,
		wsSlug, actorID))
	if err != nil {
		return "", "", 0, err
	}
	if err := q.QueryRow(ctx,
		`SELECT p.id::text FROM projects p
		 WHERE p.workspace_id = $1::uuid AND p.identifier = $2`,
		wsID, ident).Scan(&projectID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", 0, ErrProjectNotFound
		}
		return "", "", 0, err
	}
	effRole, err = effectiveAccessForProject(ctx, q, wsID, wsRole, projectID, actorID)
	if err != nil {
		return "", "", 0, err
	}
	if effRole < 0 {
		return "", "", 0, ErrNotFound
	}
	return wsID, projectID, effRole, nil
}

// ListProjectMembers returns the project's direct roster ordered by
// user name (the workspace listMembers precedent). Any caller with
// effective access may read; a private project the caller cannot
// reach is ErrNotFound, never the roster. The result is never nil.
func ListProjectMembers(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string) ([]ProjectMember, error) {
	_, projectID, _, err := resolveProjectMemberScope(ctx, pool, wsSlug, identifier, actorID)
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT u.id::text, u.name, u.email, pm.role
		 FROM project_members pm
		 JOIN users u ON u.id = pm.user_id
		 WHERE pm.project_id = $1::uuid
		 ORDER BY COALESCE(u.name, u.email)`,
		projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProjectMember{}
	for rows.Next() {
		var m ProjectMember
		if err := rows.Scan(&m.UserID, &m.Name, &m.Email, &m.Role); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AddProjectMember adds the target user to the project with the
// given role. Effective project Admin (20) only. The target must be
// a workspace member (else ErrProjectTargetNotMember); the role must
// be on the ladder (else ErrProjectMemberRoleInvalid). Duplicate
// adds are idempotent OK and return the existing row — the role is
// NOT overwritten by a duplicate add (change-role is the update path).
func AddProjectMember(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, targetUserID string, role int) (*ProjectMember, error) {
	wsID, projectID, effRole, err := resolveProjectMemberScope(ctx, pool, wsSlug, identifier, actorID)
	if err != nil {
		return nil, err
	}
	if effRole != RoleAdmin {
		return nil, ErrForbidden
	}
	if !validRole(role) {
		return nil, ErrProjectMemberRoleInvalid
	}
	if !isUUIDFormat(targetUserID) {
		return nil, ErrProjectTargetNotMember
	}
	var isWSMember bool
	err = pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM workspace_members
		 WHERE workspace_id = $1::uuid AND user_id = $2::uuid)`,
		wsID, targetUserID).Scan(&isWSMember)
	if err != nil {
		return nil, err
	}
	if !isWSMember {
		return nil, ErrProjectTargetNotMember
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO project_members (project_id, user_id, role)
		 VALUES ($1::uuid, $2::uuid, $3)
		 ON CONFLICT (project_id, user_id) DO NOTHING`,
		projectID, targetUserID, role); err != nil {
		return nil, err
	}
	var member ProjectMember
	err = pool.QueryRow(ctx,
		`SELECT u.id::text, u.name, u.email, pm.role
		 FROM project_members pm
		 JOIN users u ON u.id = pm.user_id
		 WHERE pm.project_id = $1::uuid AND pm.user_id = $2::uuid`,
		projectID, targetUserID).Scan(&member.UserID, &member.Name, &member.Email, &member.Role)
	if err != nil {
		return nil, err
	}
	return &member, nil
}

// RemoveProjectMember removes the target user's direct membership.
// Effective project Admin (20) only. Removing a non-member is
// idempotent OK. Refused with ErrLastProjectAdmin when the target is
// a direct admin and no other direct admin would remain.
func RemoveProjectMember(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, targetUserID string) error {
	_, projectID, effRole, err := resolveProjectMemberScope(ctx, pool, wsSlug, identifier, actorID)
	if err != nil {
		return err
	}
	if effRole != RoleAdmin {
		return ErrForbidden
	}
	if !isUUIDFormat(targetUserID) {
		return ErrProjectTargetNotMember
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Lock the project's membership rows so two concurrent removes
	// cannot each observe one remaining admin and both proceed.
	if _, err := tx.Exec(ctx,
		`SELECT 1 FROM project_members WHERE project_id = $1::uuid FOR UPDATE`,
		projectID); err != nil {
		return err
	}
	var targetRole *int
	err = tx.QueryRow(ctx,
		`SELECT role FROM project_members
		  WHERE project_id = $1::uuid AND user_id = $2::uuid`,
		projectID, targetUserID).Scan(&targetRole)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if targetRole != nil && *targetRole == RoleAdmin {
		var admins int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM project_members
			  WHERE project_id = $1::uuid AND role = $2`,
			projectID, RoleAdmin).Scan(&admins); err != nil {
			return err
		}
		if admins <= 1 {
			return ErrLastProjectAdmin
		}
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM project_members
		  WHERE project_id = $1::uuid AND user_id = $2::uuid`,
		projectID, targetUserID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ChangeProjectMemberRole sets a direct member's role. Effective
// project Admin (20) only. The target must hold a direct membership
// row (else ErrProjectMemberNotFound); the role must be on the
// ladder. Demoting the last direct admin is refused with
// ErrLastProjectAdmin.
func ChangeProjectMemberRole(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, targetUserID string, role int) (*ProjectMember, error) {
	_, projectID, effRole, err := resolveProjectMemberScope(ctx, pool, wsSlug, identifier, actorID)
	if err != nil {
		return nil, err
	}
	if effRole != RoleAdmin {
		return nil, ErrForbidden
	}
	if !validRole(role) {
		return nil, ErrProjectMemberRoleInvalid
	}
	if !isUUIDFormat(targetUserID) {
		return nil, ErrProjectTargetNotMember
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		`SELECT 1 FROM project_members WHERE project_id = $1::uuid FOR UPDATE`,
		projectID); err != nil {
		return nil, err
	}
	var currentRole int
	err = tx.QueryRow(ctx,
		`SELECT role FROM project_members
		  WHERE project_id = $1::uuid AND user_id = $2::uuid`,
		projectID, targetUserID).Scan(&currentRole)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrProjectMemberNotFound
		}
		return nil, err
	}
	if currentRole == RoleAdmin && role != RoleAdmin {
		var admins int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM project_members
			  WHERE project_id = $1::uuid AND role = $2`,
			projectID, RoleAdmin).Scan(&admins); err != nil {
			return nil, err
		}
		if admins <= 1 {
			return nil, ErrLastProjectAdmin
		}
	}
	var member ProjectMember
	err = tx.QueryRow(ctx,
		`UPDATE project_members SET role = $3
		  WHERE project_id = $1::uuid AND user_id = $2::uuid
		  RETURNING user_id::text, role`,
		projectID, targetUserID, role).Scan(&member.UserID, &member.Role)
	if err != nil {
		return nil, err
	}
	err = tx.QueryRow(ctx,
		`SELECT u.name, u.email FROM users u WHERE u.id = $1::uuid`,
		targetUserID).Scan(&member.Name, &member.Email)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &member, nil
}
