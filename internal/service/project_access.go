package service

// Project ACL (C92T0, Plane parity): effective project access.
//
// The tenancy boundary is still workspace membership — a non-member
// gets ErrNotFound everywhere, exactly as before. Inside a workspace,
// access to one project is resolved in this exact order (the cycle 92
// plan's design rule 2; direct membership is checked first):
//
//  1. Workspace Admin (20) -> full access, always, unchanged.
//  2. Direct project_members row -> that role (admin 20 / member 15 /
//     guest 5, mirroring the workspace ladder).
//  3. Member of the project's linked teamspace
//     (projects.teamspace_id, the single C89 link) -> Member-level
//     (15) Contributor access.
//  4. Project public -> the workspace role applies as today
//     (member 15+ writes, guest 5 read paths).
//  5. Project private + none of the above -> ErrNotFound (shared
//     404, no existence oracle: a private project the caller cannot
//     reach is indistinguishable from a missing workspace).
//
// Teamspace-derived access follows the LIVE link: moving a project to
// another teamspace changes derived access immediately (documented
// behavior, not a bug) — the resolver reads projects.teamspace_id on
// every call, never a snapshot.
//
// Every project-scoped resolver routes through here: resolveIssueProject
// (used by 27 service files) delegates to effectiveAccessForProject,
// and the dedicated resolvers that used to duplicate the
// workspace+project lookup (resolveAutomationProject,
// resolveScheduleProject) delegate to it too. All SQL is
// parameterized; no user input is ever interpolated.

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// Visibility values for projects.visibility (migration 000078).
const (
	VisibilityPublic  = "public"
	VisibilityPrivate = "private"
)

// effectiveAccessForProject applies the access order to one project.
// wsRole is the caller's workspace role. It returns the effective
// role, or -1 when the project is private and none of the earlier
// branches grant access (the caller maps -1 to ErrNotFound). A
// missing project is ErrProjectNotFound.
func effectiveAccessForProject(ctx context.Context, q queryRower, wsID string, wsRole int, projectID, actorID string) (int, error) {
	var visibility string
	var teamspaceID *string
	var directRole *int
	var teamspaceMember bool
	err := q.QueryRow(ctx,
		`SELECT p.visibility, p.teamspace_id::text,
		        (SELECT pm.role FROM project_members pm
		          WHERE pm.project_id = p.id AND pm.user_id = $2::uuid),
		        EXISTS(SELECT 1 FROM teamspace_members tm
		                WHERE tm.teamspace_id = p.teamspace_id AND tm.user_id = $2::uuid)
		   FROM projects p
		  WHERE p.id = $1::uuid AND p.workspace_id = $3::uuid`,
		projectID, actorID, wsID).Scan(
		&visibility, &teamspaceID, &directRole, &teamspaceMember)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrProjectNotFound
		}
		return 0, err
	}
	_ = teamspaceID
	return applyAccessOrder(wsRole, directRole, teamspaceMember, visibility), nil
}

// applyAccessOrder is the pure order function, separated so the
// branch matrix is unit-testable without a database: admin override,
// direct-first ordering, teamspace-derived, public fallback,
// private 404 (returned as -1; callers map it to ErrNotFound).
func applyAccessOrder(wsRole int, directRole *int, teamspaceMember bool, visibility string) int {
	if wsRole == RoleAdmin {
		return RoleAdmin
	}
	if directRole != nil {
		return *directRole
	}
	if teamspaceMember {
		return RoleMember
	}
	if visibility == VisibilityPublic {
		return wsRole
	}
	return -1
}

// EffectiveProjectAccess resolves (workspace slug or ID, project
// identifier or ID, actor) to the project plus the caller's effective
// role, implementing the access order above. A bad slug/ID or a
// non-member caller is ErrNotFound (the tenancy boundary); a
// confirmed member asking for a missing project gets
// ErrProjectNotFound; a private project the caller cannot reach is
// ErrNotFound (shared 404, no existence oracle). Identifiers are
// normalized the same way inserts are (uppercased); a UUID-shaped
// project reference matches by id, and a UUID-shaped workspace
// reference matches by id, so both addressing forms work.
func EffectiveProjectAccess(ctx context.Context, q queryRower, wsSlugOrID, projectRef, actorID string) (*Project, int, error) {
	wsID, wsRole, err := resolveWorkspaceForActorRef(ctx, q, wsSlugOrID, actorID)
	if err != nil {
		return nil, 0, err
	}
	projectID, err := resolveProjectIDRef(ctx, q, wsID, projectRef)
	if err != nil {
		return nil, 0, err
	}
	role, err := effectiveAccessForProject(ctx, q, wsID, wsRole, projectID, actorID)
	if err != nil {
		return nil, 0, err
	}
	if role < 0 {
		return nil, 0, ErrNotFound
	}
	p, err := scanProject(q.QueryRow(ctx,
		`SELECT `+projectColumnsP+` FROM projects p
		  WHERE p.id = $1::uuid AND p.workspace_id = $2::uuid`,
		projectID, wsID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, 0, ErrProjectNotFound
		}
		return nil, 0, err
	}
	return p, role, nil
}

// resolveWorkspaceForActorRef resolves (workspaceID, role) for the
// caller from either a slug or a UUID id — the same tenancy query
// workspaceIDForActor wraps, keyed by id when the reference is
// UUID-shaped. Non-members (and unknown slugs/ids) surface
// ErrNotFound.
func resolveWorkspaceForActorRef(ctx context.Context, q queryRower, wsRef, actorID string) (string, int, error) {
	if isUUIDFormat(wsRef) {
		return workspaceIDForActor(q.QueryRow(ctx,
			`SELECT w.id::text, m.role
			 FROM workspaces w
			 JOIN workspace_members m ON m.workspace_id = w.id
			 WHERE w.id = $1::uuid AND m.user_id = $2::uuid`,
			wsRef, actorID))
	}
	return workspaceIDForActor(q.QueryRow(ctx,
		`SELECT w.id::text, m.role
		 FROM workspaces w
		 JOIN workspace_members m ON m.workspace_id = w.id
		 WHERE w.slug = $1 AND m.user_id = $2::uuid`,
		wsRef, actorID))
}

// resolveProjectIDRef resolves a project reference of the workspace
// to its id: UUID-shaped references match by id, anything else is
// normalized as an identifier (uppercased, 1-12 letters/digits) and
// matched by identifier. Unknown references are ErrProjectNotFound;
// malformed identifiers are ErrInvalidIdentifier.
func resolveProjectIDRef(ctx context.Context, q queryRower, wsID, projectRef string) (string, error) {
	if isUUIDFormat(projectRef) {
		var id string
		err := q.QueryRow(ctx,
			`SELECT p.id::text FROM projects p
			  WHERE p.id = $1::uuid AND p.workspace_id = $2::uuid`,
			projectRef, wsID).Scan(&id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return "", ErrProjectNotFound
			}
			return "", err
		}
		return id, nil
	}
	ident, err := normalizeIdentifier(projectRef)
	if err != nil {
		return "", err
	}
	var id string
	err = q.QueryRow(ctx,
		`SELECT p.id::text FROM projects p
		  WHERE p.workspace_id = $1::uuid AND p.identifier = $2`,
		wsID, ident).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrProjectNotFound
		}
		return "", err
	}
	return id, nil
}

// effectiveRoleForProjectID is the project-ID-addressed gate for
// resolvers that already hold (wsID, projectID, actorID) without a
// slug/identifier — resolveEpicWorkspace (epic -> project) and
// resolveScheduleProject (wsID + projectID). It reuses the same
// order: workspace Admin, direct row, teamspace-derived, public
// fallback, private 404. A missing project is ErrProjectNotFound;
// a private project the caller cannot reach is ErrNotFound; a
// non-workspace-member is ErrNotFound.
func effectiveRoleForProjectID(ctx context.Context, q queryRower, wsID, projectID, actorID string) (wsRole, effRole int, err error) {
	err = q.QueryRow(ctx,
		`SELECT m.role FROM workspace_members m
		  WHERE m.workspace_id = $1::uuid AND m.user_id = $2::uuid`,
		wsID, actorID).Scan(&wsRole)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, 0, ErrNotFound
		}
		return 0, 0, err
	}
	effRole, err = effectiveAccessForProject(ctx, q, wsID, wsRole, projectID, actorID)
	if err != nil {
		return 0, 0, err
	}
	if effRole < 0 {
		return 0, 0, ErrNotFound
	}
	return wsRole, effRole, nil
}
