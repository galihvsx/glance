// projectLabels: workspace project-label taxonomy client (C76T1).
//
// Contract (C76T0 backend — spec, not re-implemented here):
//   Taxonomy   POST/GET /api/v1/workspaces/:slug/project-labels,
//              PATCH/DELETE /api/v1/workspaces/:slug/project-labels/:id
//              (workspace-level; members-only writes, guests read-only;
//              409 duplicate name, 400 bad hex)
//   Assignment POST/DELETE
//              /api/v1/workspaces/:slug/projects/:identifier/project-labels/:labelId
//              (204; duplicate assign is a deliberate 409, unassign is
//              idempotent)
// The envelope is {"project_labels": [...]} — [] never null.

import { api } from "./api";
import type { ProjectLabel } from "./types";

function base(slug: string): string {
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/project-labels`;
}

function assignBase(slug: string, identifier: string): string {
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/project-labels`;
}

/** react-query keys for the workspace project-label taxonomy. */
export function projectLabelKeys(slug: string) {
  return {
    labels: ["project-labels", slug] as const,
  };
}

/** List the workspace's project labels (any role may read). */
export function fetchProjectLabels(slug: string): Promise<ProjectLabel[]> {
  return api
    .get<{ project_labels: ProjectLabel[] }>(base(slug))
    .then((r) => r.project_labels ?? []);
}

/** Create a project label. Member (15)+; the server is the gate. */
export function createProjectLabel(
  slug: string,
  input: { name: string; color?: string },
): Promise<ProjectLabel> {
  return api.post<ProjectLabel>(base(slug), input);
}

/** Rename / recolor a project label (partial semantics). Member (15)+. */
export function updateProjectLabel(
  slug: string,
  id: string,
  input: { name?: string; color?: string },
): Promise<ProjectLabel> {
  return api.patch<ProjectLabel>(`${base(slug)}/${encodeURIComponent(id)}`, input);
}

/**
 * Delete a project label. Only the junction rows are removed — projects
 * are untouched. Member (15)+.
 */
export function deleteProjectLabel(slug: string, id: string): Promise<void> {
  return api.del<void>(`${base(slug)}/${encodeURIComponent(id)}`);
}

/** Assign a project label to a project (204; duplicate assign 409s). */
export function assignProjectLabel(
  slug: string,
  identifier: string,
  labelId: string,
): Promise<void> {
  return api.post<void>(
    `${assignBase(slug, identifier)}/${encodeURIComponent(labelId)}`,
  );
}

/** Remove a project label from a project (idempotent 204). */
export function unassignProjectLabel(
  slug: string,
  identifier: string,
  labelId: string,
): Promise<void> {
  return api.del<void>(
    `${assignBase(slug, identifier)}/${encodeURIComponent(labelId)}`,
  );
}
