package service

// Teamspaces (C89T0, membership + visibility C90T0): workspace-scoped
// groups that organize projects in the sidebar (Plane "teamspaces"
// parity).
//
// Teamspaces own no issues and no projects: deleting a teamspace only
// ungroups its projects (projects.teamspace_id ON DELETE SET NULL,
// migration 000076) — deleting a teamspace must never delete projects.
// Deleting a teamspace DOES remove its memberships (teamspace_members
// teamspace_id FK ON DELETE CASCADE, migration 000077).
//
// Visibility (C90T0, Plane parity): workspace Admins see every
// teamspace and bypass membership; any other workspace role sees only
// the teamspaces they belong to. Migration 000077 backfills one
// membership row per (existing teamspace x current workspace member),
// so existing workspaces keep exactly their cycle-89 visibility until
// an Admin removes someone. Membership gates teamspace visibility
// ONLY — it is not project ACL (glance has no project-level
// membership; the Projects page stays the browse-all surface and
// direct project links keep working).
//
// Conventions (mirror initiatives, the closest workspace-scoped
// grouping entity, and project_labels for name rules):
//   - Tenancy: every op resolves (workspaceID, role) via
//     resolveTeamspaceWorkspace. A bad slug or non-member caller
//     surfaces ErrNotFound ("workspace not found") — the tenancy
//     boundary. A bad teamspace id surfaces ErrTeamspaceNotFound;
//     a malformed one surfaces ErrInvalidTeamspaceID (400, never a
//     ::uuid 500).
//   - Roles: any member (guest 5+) may read; mutations need member
//     (15)+ — the initiative pattern, unchanged (the plan's audit
//     found no reason to differ).
//   - Names: trimmed, non-empty (ErrNameRequired); duplicate name in
//     the workspace is ErrTeamspaceConflict (409) via the plain
//     UNIQUE(workspace_id, name) — the initiatives/project_labels
//     precedent; the codebase has no case-insensitive name-uniqueness
//     precedent, so "Alpha" and "alpha" are distinct names.
//   - No-op rename to the same name is not an error: it returns the
//     existing row untouched (the project_label convention).
//   - ListTeamspaces is ordered by name; each entry carries its LIVE
//     project count (archived projects excluded — the sidebar header
//     shows "Teamspace · N" over the live rows the sidebar renders)
//     and its member_count, both from the same query, not N+1. No
//     is_member flag: the caller is either an Admin (sees all) or a
//     member of every row returned, so the flag would be
//     constant-true and carry no information.
//   - Membership management is Admin (20) only — the workspace
//     UpsertMember precedent (actorRole != RoleAdmin → ErrForbidden).
//     Adding a target who is not a workspace member is
//     ErrTeamspaceTargetNotMember (400): the same error whether the
//     user id matches nobody or a non-member, so the caller never
//     learns whether an arbitrary user id exists. Duplicate adds and
//     removes of non-members are idempotent OK (the workspace
//     RemoveMember precedent errors ErrMemberNotFound on a non-member
//     — deliberately NOT followed here: DELETE is idempotent by
//     nature and a stale roster row must not 404 the remove button).
//     Removing the last member is allowed: an Admin can always see
//     the teamspace and re-add.
//   - ListTeamspaceMembers is readable by teamspace members and by
//     workspace Admins; a non-member non-admin gets
//     ErrTeamspaceNotFound — never the roster, never a leak of the
//     teamspace's existence. Ordered by user name, the workspace
//     listMembers precedent (ORDER BY COALESCE(u.name, u.email)).
//   - DeleteTeamspace returns the count of projects ungrouped by the
//     delete — ALL rows the FK SET NULL touches, including archived
//     ones (an archived project keeps its grouping until its
//     teamspace is deleted, then it too is ungrouped). The two counts
//     can therefore differ by exactly the archived-assigned rows;
//     that is deliberate, not a bug.
//   - No workspace audit-log rows and no broadcast events: the audit
//     found initiatives and project_labels — the grouping-entity
//     precedents — emit neither, so teamspaces emit neither rather
//     than inventing a new convention.

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrTeamspaceNotFound is returned when the teamspace id matches
	// nothing in the workspace. On this path the workspace is confirmed
	// and the caller is a member.
	ErrTeamspaceNotFound = errors.New("service: teamspace not found")
	// ErrTeamspaceConflict is returned when the teamspace name is
	// already taken in the workspace (UNIQUE(workspace_id, name)).
	ErrTeamspaceConflict = errors.New("service: teamspace name already exists")
	// ErrInvalidTeamspaceID is returned when a teamspace id is not a
	// syntactically valid UUID. The handler maps it to 400 bad_request.
	ErrInvalidTeamspaceID = errors.New("service: invalid teamspace id")
	// ErrInvalidTeamspace is returned by UpdateProject when the
	// teamspace_id names no teamspace of the workspace — unknown,
	// malformed, or foreign-workspace. All three are an honest 400
	// (never a silent cross-workspace link, never a 404 that would
	// leak which teamspaces exist in other workspaces).
	ErrInvalidTeamspace = errors.New("service: invalid teamspace")
	// ErrTeamspaceTargetNotMember is returned by AddTeamspaceMember
	// when the target user_id is not a workspace member — including
	// when it matches no user at all, and when it is malformed. One
	// error for all three cases, so the caller never learns whether
	// an arbitrary user id exists. The handler maps it to 400
	// bad_request (the caller supplied an invalid target for this
	// operation; the workspace and teamspace are both confirmed).
	ErrTeamspaceTargetNotMember = errors.New("service: target user is not a workspace member")
)

// TeamspaceMember is one teamspace member with their user identity,
// for the members roster. No workspace role is carried: the roster is
// about teamspace belonging, and the workspace role is available from
// the workspace members endpoint.
type TeamspaceMember struct {
	ID    string  `json:"id"`
	Name  *string `json:"name,omitempty"`
	Email string  `json:"email"`
}

// Teamspace is one workspace-scoped project group.
type Teamspace struct {
	ID           string    `json:"id"`
	WorkspaceID  string    `json:"workspace_id"`
	Name         string    `json:"name"`
	ProjectCount int64     `json:"project_count"`
	MemberCount  int64     `json:"member_count"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// TeamspacePatch is a partial teamspace update: nil fields are
// untouched. Only Name exists today; an empty patch is
// ErrNothingToUpdate.
type TeamspacePatch struct {
	Name *string
}

// resolveTeamspaceWorkspace resolves (workspaceID, role) for the caller.
// Guests may read; callers pass needMember to enforce member (15)+.
// Non-members (and bad slugs) surface ErrNotFound — the tenancy boundary.
func resolveTeamspaceWorkspace(ctx context.Context, q queryRower, wsSlug, actorID string, needMember bool) (wsID string, role int, err error) {
	wsID, role, err = workspaceIDForActor(q.QueryRow(ctx,
		`SELECT w.id::text, m.role
		 FROM workspaces w
		 JOIN workspace_members m ON m.workspace_id = w.id
		 WHERE w.slug = $1 AND m.user_id = $2::uuid`,
		wsSlug, actorID))
	if err != nil {
		return "", 0, err
	}
	if needMember && role < RoleMember {
		return "", 0, ErrForbidden
	}
	return wsID, role, nil
}

// teamspaceColumns is the shared SELECT list; the counted variant adds
// the live project count (archived projects excluded — see the package
// doc) and the member count, both from the same query, never N+1.
const teamspaceColumns = `id::text, workspace_id::text, name, created_at, updated_at`

const teamspaceColumnsCounted = teamspaceColumns + `,
	(SELECT COUNT(*) FROM projects p WHERE p.teamspace_id = teamspaces.id AND p.archived_at IS NULL),
	(SELECT COUNT(*) FROM teamspace_members m WHERE m.teamspace_id = teamspaces.id)`

func scanTeamspaceCounted(row pgx.Row) (Teamspace, error) {
	var ts Teamspace
	err := row.Scan(
		&ts.ID, &ts.WorkspaceID, &ts.Name, &ts.CreatedAt, &ts.UpdatedAt, &ts.ProjectCount, &ts.MemberCount)
	if err != nil {
		return Teamspace{}, err
	}
	return ts, nil
}

// resolveTeamspace loads a teamspace of the workspace. Malformed ids
// match nothing — 400, not a 500 from the ::uuid cast.
func resolveTeamspace(ctx context.Context, q queryRower, wsID, teamspaceID string) (Teamspace, error) {
	if !isUUIDFormat(teamspaceID) {
		return Teamspace{}, ErrInvalidTeamspaceID
	}
	ts, err := scanTeamspaceCounted(q.QueryRow(ctx,
		`SELECT `+teamspaceColumnsCounted+`
		 FROM teamspaces WHERE id = $1::uuid AND workspace_id = $2::uuid`,
		teamspaceID, wsID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Teamspace{}, ErrTeamspaceNotFound
		}
		return Teamspace{}, err
	}
	return ts, nil
}

// CreateTeamspace creates a teamspace in the workspace. Member (15)+.
// The creator is auto-added as a member in the same transaction:
// without this, a Member who creates a teamspace would create
// something invisible to themselves under the visibility filter.
func CreateTeamspace(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID, name string) (*Teamspace, error) {
	wsID, _, err := resolveTeamspaceWorkspace(ctx, pool, wsSlug, actorID, true)
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrNameRequired
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	ts, err := scanTeamspaceCounted(tx.QueryRow(ctx,
		`INSERT INTO teamspaces (workspace_id, name)
		 VALUES ($1::uuid, $2)
		 RETURNING `+teamspaceColumnsCounted,
		wsID, name))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrTeamspaceConflict
		}
		return nil, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO teamspace_members (teamspace_id, user_id)
		 VALUES ($1::uuid, $2::uuid)`,
		ts.ID, actorID); err != nil {
		return nil, err
	}
	// Re-read inside the tx so the returned row carries the creator
	// membership (the INSERT ... RETURNING above ran before it).
	ts, err = resolveTeamspace(ctx, tx, wsID, ts.ID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &ts, nil
}

// ListTeamspaces returns the workspace's teamspaces ordered by name,
// each with its live project count and member count. Any role may
// read; the result is never nil. Visibility (C90T0): Admins see all
// teamspaces; any other role sees only teamspaces where they hold a
// membership row.
func ListTeamspaces(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID string) ([]Teamspace, error) {
	wsID, role, err := resolveTeamspaceWorkspace(ctx, pool, wsSlug, actorID, false)
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT `+teamspaceColumnsCounted+`
		 FROM teamspaces
		 WHERE workspace_id = $1::uuid
		   AND ($3 OR EXISTS (
		     SELECT 1 FROM teamspace_members tm
		     WHERE tm.teamspace_id = teamspaces.id AND tm.user_id = $2::uuid))
		 ORDER BY name, id`,
		wsID, actorID, role == RoleAdmin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Teamspace{}
	for rows.Next() {
		ts, err := scanTeamspaceCounted(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ts)
	}
	return out, rows.Err()
}

// isTeamspaceMember reports whether userID holds a membership row for
// the teamspace.
func isTeamspaceMember(ctx context.Context, q queryRower, teamspaceID, userID string) (bool, error) {
	var exists bool
	err := q.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM teamspace_members
		 WHERE teamspace_id = $1::uuid AND user_id = $2::uuid)`,
		teamspaceID, userID).Scan(&exists)
	return exists, err
}

// resolveTeamspaceMemberWorkspace resolves (workspaceID, role) for an
// Admin-only membership mutation: the tenancy boundary first
// (bad slug / non-member → ErrNotFound), then the Admin gate.
func resolveTeamspaceMemberWorkspace(ctx context.Context, q queryRower, wsSlug, actorID string) (string, error) {
	wsID, role, err := resolveTeamspaceWorkspace(ctx, q, wsSlug, actorID, false)
	if err != nil {
		return "", err
	}
	if role != RoleAdmin {
		return "", ErrForbidden
	}
	return wsID, nil
}

// AddTeamspaceMember adds the target user to the teamspace. Admin (20)
// only. The target must be a workspace member — otherwise
// ErrTeamspaceTargetNotMember (400), the same error whether the user
// id matches nobody, a non-member, or is malformed, so no existence
// oracle. Duplicate adds are idempotent OK and return the existing
// row (the workspace upsertMember precedent, not a 409). A
// foreign-workspace teamspace id is ErrTeamspaceNotFound.
func AddTeamspaceMember(ctx context.Context, pool *pgxpool.Pool, wsSlug, teamspaceID, actorID, targetUserID string) (*TeamspaceMember, error) {
	wsID, err := resolveTeamspaceMemberWorkspace(ctx, pool, wsSlug, actorID)
	if err != nil {
		return nil, err
	}
	if _, err := resolveTeamspace(ctx, pool, wsID, teamspaceID); err != nil {
		return nil, err
	}
	if !isUUIDFormat(targetUserID) {
		return nil, ErrTeamspaceTargetNotMember
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
		return nil, ErrTeamspaceTargetNotMember
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO teamspace_members (teamspace_id, user_id)
		 VALUES ($1::uuid, $2::uuid)
		 ON CONFLICT (teamspace_id, user_id) DO NOTHING`,
		teamspaceID, targetUserID); err != nil {
		return nil, err
	}
	var member TeamspaceMember
	err = pool.QueryRow(ctx,
		`SELECT u.id::text, u.name, u.email
		 FROM teamspace_members tm
		 JOIN users u ON u.id = tm.user_id
		 WHERE tm.teamspace_id = $1::uuid AND tm.user_id = $2::uuid`,
		teamspaceID, targetUserID).Scan(&member.ID, &member.Name, &member.Email)
	if err != nil {
		return nil, err
	}
	return &member, nil
}

// RemoveTeamspaceMember removes the target user from the teamspace.
// Admin (20) only. Removing a non-member is idempotent OK: a DELETE
// that touches zero rows is still a success, so a stale roster row
// never 404s the remove button.
func RemoveTeamspaceMember(ctx context.Context, pool *pgxpool.Pool, wsSlug, teamspaceID, actorID, targetUserID string) error {
	wsID, err := resolveTeamspaceMemberWorkspace(ctx, pool, wsSlug, actorID)
	if err != nil {
		return err
	}
	if _, err := resolveTeamspace(ctx, pool, wsID, teamspaceID); err != nil {
		return err
	}
	if !isUUIDFormat(targetUserID) {
		return ErrTeamspaceTargetNotMember
	}
	_, err = pool.Exec(ctx,
		`DELETE FROM teamspace_members
		 WHERE teamspace_id = $1::uuid AND user_id = $2::uuid`,
		teamspaceID, targetUserID)
	return err
}

// ListTeamspaceMembers returns the teamspace's roster ordered by user
// name (the workspace listMembers precedent). Teamspace members and
// workspace Admins may read; a non-member non-admin gets
// ErrTeamspaceNotFound — never the roster, never a leak of the
// teamspace's existence. The result is never nil.
func ListTeamspaceMembers(ctx context.Context, pool *pgxpool.Pool, wsSlug, teamspaceID, actorID string) ([]TeamspaceMember, error) {
	wsID, role, err := resolveTeamspaceWorkspace(ctx, pool, wsSlug, actorID, false)
	if err != nil {
		return nil, err
	}
	if _, err := resolveTeamspace(ctx, pool, wsID, teamspaceID); err != nil {
		return nil, err
	}
	if role != RoleAdmin {
		member, err := isTeamspaceMember(ctx, pool, teamspaceID, actorID)
		if err != nil {
			return nil, err
		}
		if !member {
			return nil, ErrTeamspaceNotFound
		}
	}
	rows, err := pool.Query(ctx,
		`SELECT u.id::text, u.name, u.email
		 FROM teamspace_members tm
		 JOIN users u ON u.id = tm.user_id
		 WHERE tm.teamspace_id = $1::uuid
		 ORDER BY COALESCE(u.name, u.email)`,
		teamspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TeamspaceMember{}
	for rows.Next() {
		var m TeamspaceMember
		if err := rows.Scan(&m.ID, &m.Name, &m.Email); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// UpdateTeamspace applies a partial teamspace update. Member (15)+.
// Renaming to the same name is a no-op success returning the existing
// row; renaming to an existing name is ErrTeamspaceConflict; an empty
// patch is ErrNothingToUpdate.
func UpdateTeamspace(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID, teamspaceID string, patch TeamspacePatch) (*Teamspace, error) {
	wsID, _, err := resolveTeamspaceWorkspace(ctx, pool, wsSlug, actorID, true)
	if err != nil {
		return nil, err
	}
	old, err := resolveTeamspace(ctx, pool, wsID, teamspaceID)
	if err != nil {
		return nil, err
	}
	if patch.Name == nil {
		return nil, ErrNothingToUpdate
	}
	name := strings.TrimSpace(*patch.Name)
	if name == "" {
		return nil, ErrNameRequired
	}
	if name == old.Name {
		return &old, nil
	}
	updated, err := scanTeamspaceCounted(pool.QueryRow(ctx,
		`UPDATE teamspaces SET name = $1, updated_at = now()
		 WHERE id = $2::uuid AND workspace_id = $3::uuid
		 RETURNING `+teamspaceColumnsCounted,
		name, teamspaceID, wsID))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrTeamspaceConflict
		}
		return nil, err
	}
	return &updated, nil
}

// DeleteTeamspace deletes the teamspace row only. The FK ON DELETE SET
// NULL ungroups its projects — projects are never deleted — and the
// returned count is exactly the number of projects ungrouped
// (including archived ones). Member (15)+. Never 500 when the
// teamspace has projects.
func DeleteTeamspace(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID, teamspaceID string) (int64, error) {
	wsID, _, err := resolveTeamspaceWorkspace(ctx, pool, wsSlug, actorID, true)
	if err != nil {
		return 0, err
	}
	if _, err := resolveTeamspace(ctx, pool, wsID, teamspaceID); err != nil {
		return 0, err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var count int64
	if err := tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM projects WHERE teamspace_id = $1::uuid`,
		teamspaceID).Scan(&count); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM teamspaces WHERE id = $1::uuid AND workspace_id = $2::uuid`,
		teamspaceID, wsID); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return count, nil
}

// resolveProjectTeamspace validates that teamspaceID names a teamspace
// of the workspace: malformed, unknown, or foreign-workspace are all
// ErrInvalidTeamspace (400) — the UpdateProject contract never leaks
// cross-workspace existence via distinct statuses.
func resolveProjectTeamspace(ctx context.Context, q queryRower, wsID, teamspaceID string) error {
	if !isUUIDFormat(teamspaceID) {
		return ErrInvalidTeamspace
	}
	var owner string
	err := q.QueryRow(ctx,
		`SELECT workspace_id::text FROM teamspaces WHERE id = $1::uuid`, teamspaceID).Scan(&owner)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidTeamspace
		}
		return err
	}
	if owner != wsID {
		return ErrInvalidTeamspace
	}
	return nil
}
