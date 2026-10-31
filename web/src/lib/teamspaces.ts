// teamspaces: workspace teamspace client + sidebar grouping (C89T1,
// membership + visibility in C90T1).
//
// Contract (C89T0 backend + C90T0 membership — spec, not
// re-implemented here):
//   GET/POST /api/v1/workspaces/:slug/teamspaces,
//   PATCH/DELETE /api/v1/workspaces/:slug/teamspaces/:id
//   (workspace-level; members-only writes, guests read-only;
//   409 duplicate name, 400 bad UUID / foreign teamspace on move)
//   DELETE returns {"id", "ungrouped_projects"} — the count of ALL
//   projects the FK ungrouped, including archived ones. The sidebar
//   list counts (below) exclude archived projects by construction, so
//   the two counts differ by exactly the archived-assigned rows.
//   The delete confirm shows the LIVE count (the only count knowable
//   before the delete runs); it equals the delete-response count
//   whenever no archived project is assigned to the teamspace.
//   POST/GET /api/v1/workspaces/:slug/teamspaces/:id/members and
//   DELETE .../members/:user_id (C90T0, Admin-only writes; roster
//   reads for teamspace members + Admins, 404 otherwise). Roster
//   items are {id, name?, email} — no role field.
//   GET .../teamspaces is visibility-filtered server-side (C90T0):
//   Admins see every teamspace, other roles see only the teamspaces
//   they belong to. The sidebar must therefore never expand this
//   list client-side — what the server did not return is not visible.
// The envelope is {"teamspaces": [...]} — [] never null.

import { api } from "./api";
import type { Project, Teamspace, TeamspaceMember, WorkspaceMember } from "./types";

/** The synthetic group key for projects with no teamspace. */
export const UNGROUPED_KEY = "ungrouped";

function base(slug: string): string {
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/teamspaces`;
}

/** react-query keys for the workspace teamspace list. */
export function teamspaceKeys(slug: string) {
  return {
    list: ["teamspaces", slug] as const,
  };
}

/** react-query keys for a teamspace's member roster. */
export function teamspaceMemberKeys(slug: string, teamspaceId: string) {
  return {
    list: ["teamspaces", slug, teamspaceId, "members"] as const,
  };
}

/** react-query key for the workspace member list (add-picker source). */
export function workspaceMembersKey(slug: string) {
  return ["workspaces", slug, "members"] as const;
}

/** List the workspace's teamspaces, ordered by name (any role may read).
 *  The server filters by visibility (C90T0): Admins get every teamspace,
 *  other roles get only the teamspaces they belong to. */
export function fetchTeamspaces(slug: string): Promise<Teamspace[]> {
  return api
    .get<{ teamspaces: Teamspace[] }>(base(slug))
    .then((r) => r.teamspaces ?? []);
}

/** List a teamspace's roster. Teamspace members + Admins may read;
 *  a non-member non-admin gets a 404 (the server never leaks the
 *  roster). The envelope is {"members": [...]} — [] never null. */
export function fetchTeamspaceMembers(
  slug: string,
  teamspaceId: string,
): Promise<TeamspaceMember[]> {
  return api
    .get<{ members: TeamspaceMember[] }>(
      `${base(slug)}/${encodeURIComponent(teamspaceId)}/members`,
    )
    .then((r) => r.members ?? []);
}

/** Add a workspace member to a teamspace. Admin-only; the server is
 *  the gate. Re-adding an existing member is idempotent OK. */
export function addTeamspaceMember(
  slug: string,
  teamspaceId: string,
  userId: string,
): Promise<TeamspaceMember> {
  return api.post<TeamspaceMember>(
    `${base(slug)}/${encodeURIComponent(teamspaceId)}/members`,
    { user_id: userId },
  );
}

/** Remove a member from a teamspace. Admin-only; removing a
 *  non-member is idempotent OK. */
export function removeTeamspaceMember(
  slug: string,
  teamspaceId: string,
  userId: string,
): Promise<{ ok: boolean }> {
  return api.del<{ ok: boolean }>(
    `${base(slug)}/${encodeURIComponent(teamspaceId)}/members/${encodeURIComponent(userId)}`,
  );
}

/** Set a teamspace's Lead (C92T0). Workspace Admin (20) only; the
 *  target must be a teamspace member (else 400). Setting a new Lead
 *  replaces the old one. 200 with the teamspace JSON (including
 *  lead_user_id). */
export function setTeamspaceLead(
  slug: string,
  teamspaceId: string,
  userId: string,
): Promise<Teamspace> {
  return api.post<Teamspace>(
    `${base(slug)}/${encodeURIComponent(teamspaceId)}/lead`,
    { user_id: userId },
  );
}

/** Clear a teamspace's Lead (C92T0). Workspace Admin (20) only;
 *  clearing when no Lead is set is idempotent OK. 200 with the
 *  teamspace JSON. */
export function clearTeamspaceLead(
  slug: string,
  teamspaceId: string,
): Promise<Teamspace> {
  return api.del<Teamspace>(
    `${base(slug)}/${encodeURIComponent(teamspaceId)}/lead`,
  );
}

/** List the workspace's members (any member may read) — the add-picker
 *  source. The envelope is {"members": [...]} — [] never null. */
export function fetchWorkspaceMembers(
  slug: string,
): Promise<WorkspaceMember[]> {
  return api
    .get<{ members: WorkspaceMember[] }>(
      `/api/v1/workspaces/${encodeURIComponent(slug)}/members`,
    )
    .then((r) => r.members ?? []);
}

/** Create a teamspace. Member (15)+; the server is the gate. */
export function createTeamspace(
  slug: string,
  input: { name: string },
): Promise<Teamspace> {
  return api.post<Teamspace>(base(slug), input);
}

/** Rename a teamspace (partial semantics). Member (15)+. */
export function updateTeamspace(
  slug: string,
  id: string,
  input: { name: string },
): Promise<Teamspace> {
  return api.patch<Teamspace>(`${base(slug)}/${encodeURIComponent(id)}`, input);
}

/** Delete a teamspace. Projects are never deleted — their
 *  teamspace_id is SET NULL by the FK. Returns the ungrouped count. */
export function deleteTeamspace(
  slug: string,
  id: string,
): Promise<{ id: string; ungrouped_projects: number }> {
  return api.del<{ id: string; ungrouped_projects: number }>(
    `${base(slug)}/${encodeURIComponent(id)}`,
  );
}

/** Move a project to a teamspace, or ungroup it with explicit null.
 *  The T0 PATCH tri-state: absent = unchanged, null = ungroup. */
export function moveProjectToTeamspace(
  slug: string,
  identifier: string,
  teamspaceId: string | null,
): Promise<Project> {
  return api.patch<Project>(
    `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`,
    { teamspace_id: teamspaceId },
  );
}

/* ------------------------------------------------------------------ */
/* Sidebar grouping: one group per teamspace (in list order, which the
 * server orders by name) plus "Ungrouped" always last. Archived
 * projects are excluded — the sidebar renders live rows only, and the
 * group counts are the live counts from this same list (never the
 * server's project_count, which the sidebar does not need to trust).
 * A project whose teamspace_id names no known teamspace falls back to
 * Ungrouped rather than vanishing. Empty teamspaces still get a
 * header: the header menu is the only rename/delete surface.
 * Visibility (C90T1): with excludeAbsent, a project whose teamspace
 * is absent from the (server-visibility-filtered) list is EXCLUDED
 * instead of falling back to Ungrouped — for a non-admin the absent
 * id is a teamspace they cannot see, and its projects must not leak
 * into the sidebar under a false Ungrouped label. The sidebar enables
 * excludeAbsent only for non-admins: for an Admin the server list is
 * complete, so an absent id is genuinely unknown/stale and keeps the
 * C89 fallback. The client cannot distinguish hidden from stale, so
 * the option is role-gated at the call-site, never here.           */
/* ------------------------------------------------------------------ */

export interface ProjectGroup {
  /** Teamspace id, or UNGROUPED_KEY for the ungrouped group. */
  key: string;
  teamspace: Teamspace | null;
  projects: Project[];
}

export function groupProjects(
  projects: Project[],
  teamspaces: Teamspace[],
  options?: { excludeAbsent?: boolean },
): ProjectGroup[] {
  const live = projects.filter((p) => !p.archived_at);
  const byId = new Map(teamspaces.map((t) => [t.id, t]));
  const groups: ProjectGroup[] = teamspaces.map((t) => ({
    key: t.id,
    teamspace: t,
    projects: [],
  }));
  const ungrouped: ProjectGroup = {
    key: UNGROUPED_KEY,
    teamspace: null,
    projects: [],
  };
  for (const p of live) {
    const t = p.teamspace_id ? byId.get(p.teamspace_id) : undefined;
    if (t) {
      groups.find((g) => g.key === t.id)!.projects.push(p);
    } else if (p.teamspace_id && options?.excludeAbsent) {
      // Hidden teamspace (non-admin view): drop the row from the
      // sidebar entirely. The project stays reachable via the Projects
      // page and direct links — the sidebar is navigation, not access
      // control, and this cycle gates teamspace visibility only.
      continue;
    } else {
      ungrouped.projects.push(p);
    }
  }
  // Ungrouped renders only when it has rows.
  if (ungrouped.projects.length > 0) {
    groups.push(ungrouped);
  }
  return groups;
}
