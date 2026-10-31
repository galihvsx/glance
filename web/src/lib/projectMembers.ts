// Project members client (C92T1, T0 contract C92T0):
//   GET    /api/v1/workspaces/:slug/projects/:identifier/members
//   POST   .../members {user_id, role} -> 200 member (upsert, no role
//          overwrite on duplicate — change-role is the update path)
//   PATCH  .../members/:user_id {role} -> 200 member; demoting the
//          last direct admin is 409
//   DELETE .../members/:user_id -> 200 {ok}; removing the last direct
//          admin is 409; removing a non-member is idempotent OK.
// Management requires effective project Admin (20); the server is
// the gate — the UI only hides controls it knows the caller lacks.
// The roster lists DIRECT members only: teamspace-derived access is
// not a row here, so the Members section derives those rows from the
// linked teamspace's roster (see ProjectMembersSection) and marks
// them read-only. The envelope is {"members": [...]} — [] never null.

import { api } from "./api";
import type { ProjectMember } from "./types";

function base(slug: string, identifier: string): string {
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/members`;
}

/** react-query keys for a project's direct-member roster. */
export function projectMemberKeys(slug: string, identifier: string) {
  return {
    list: ["projects", slug, identifier, "members"] as const,
  };
}

/** List the project's direct roster, ordered by user name. Any
 *  caller with effective access may read; a private project the
 *  caller cannot reach is 404 (the shared no-oracle shape — the UI
 *  must never render a "private" hint from it). */
export function fetchProjectMembers(
  slug: string,
  identifier: string,
): Promise<ProjectMember[]> {
  return api
    .get<{ members: ProjectMember[] }>(base(slug, identifier))
    .then((r) => r.members ?? []);
}

/** Add a workspace member to the project. Target must already be a
 *  workspace member (else 400); role must be on the ladder. */
export function addProjectMember(
  slug: string,
  identifier: string,
  userId: string,
  role: number,
): Promise<ProjectMember> {
  return api.post<ProjectMember>(base(slug, identifier), {
    user_id: userId,
    role,
  });
}

/** Change a direct member's role. */
export function changeProjectMemberRole(
  slug: string,
  identifier: string,
  userId: string,
  role: number,
): Promise<ProjectMember> {
  return api.patch<ProjectMember>(
    `${base(slug, identifier)}/${encodeURIComponent(userId)}`,
    { role },
  );
}

/** Remove a direct member. */
export function removeProjectMember(
  slug: string,
  identifier: string,
  userId: string,
): Promise<{ ok: boolean }> {
  return api.del<{ ok: boolean }>(
    `${base(slug, identifier)}/${encodeURIComponent(userId)}`,
  );
}
