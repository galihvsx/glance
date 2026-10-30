// teamspaces: workspace teamspace client + sidebar grouping (C89T1).
//
// Contract (C89T0 backend — spec, not re-implemented here):
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
// The envelope is {"teamspaces": [...]} — [] never null.

import { api } from "./api";
import type { Project, Teamspace } from "./types";

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

/** List the workspace's teamspaces, ordered by name (any role may read). */
export function fetchTeamspaces(slug: string): Promise<Teamspace[]> {
  return api
    .get<{ teamspaces: Teamspace[] }>(base(slug))
    .then((r) => r.teamspaces ?? []);
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
 * header: the header menu is the only rename/delete surface.        */
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
