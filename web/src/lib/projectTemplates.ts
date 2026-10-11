// Project templates client (C17T1) — workspace settings CRUD + the
// template picker in the new-project dialog.
//
// Contract (backend internal/service/project_template.go +
// internal/api/project_template_handler.go):
//   GET/POST  /api/v1/workspaces/:slug/templates
//             list -> {templates: [...]}, create -> 201
//   GET/PATCH/DELETE .../templates/:id
//   ProjectTemplate JSON: {id, workspace_id, name, description, payload,
//   created_by?, created_at, updated_at}
//   payload: {states[]:{name,group,color}, labels[]:{name,color},
//   modules[]:{name,description}, estimate_scale?:{name,points[]:{key,value,description}}}
// Reads: member (15)+; mutations: admin (20), server-gated.

import { api } from "./api";

/** One kanban column seeded by a project template. */
export interface ProjectTemplateState {
  name: string;
  group: string;
  color: string;
}

/** One workspace label seeded by a project template. */
export interface ProjectTemplateLabel {
  name: string;
  color: string;
}

/** One project module seeded by a project template. */
export interface ProjectTemplateModule {
  name: string;
  description: string;
}

/** One selectable value of the template's estimate scale. */
export interface ProjectTemplateEstimatePoint {
  key: string;
  value: number;
  description: string;
}

/** The validated payload document of a project template. */
export interface ProjectTemplatePayload {
  states: ProjectTemplateState[];
  labels: ProjectTemplateLabel[];
  modules: ProjectTemplateModule[];
  estimate_scale?: {
    name: string;
    points: ProjectTemplateEstimatePoint[];
  } | null;
}

/** One workspace project template (backend ProjectTemplate shape). */
export interface ProjectTemplate {
  id: string;
  workspace_id: string;
  name: string;
  description: string;
  payload: ProjectTemplatePayload;
  created_by?: string | null;
  created_at: string;
  updated_at: string;
}

function base(slug: string): string {
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/templates`;
}

export function projectTemplateKeys(slug: string) {
  return {
    templates: ["project-templates", slug] as const,
  };
}

export function fetchProjectTemplates(
  slug: string,
): Promise<ProjectTemplate[]> {
  return api
    .get<{ templates: ProjectTemplate[] }>(base(slug))
    .then((r) => r.templates ?? []);
}

export function createProjectTemplate(
  slug: string,
  input: {
    name: string;
    description?: string;
    payload?: ProjectTemplatePayload;
  },
): Promise<ProjectTemplate> {
  return api.post<ProjectTemplate>(base(slug), input);
}

export function updateProjectTemplate(
  slug: string,
  id: string,
  input: {
    name?: string;
    description?: string;
    payload?: ProjectTemplatePayload;
  },
): Promise<ProjectTemplate> {
  return api.patch<ProjectTemplate>(`${base(slug)}/${id}`, input);
}

export function deleteProjectTemplate(
  slug: string,
  id: string,
): Promise<void> {
  return api.del<void>(`${base(slug)}/${id}`);
}

/** Empty payload: a template that seeds nothing beyond the defaults. */
export function emptyProjectTemplatePayload(): ProjectTemplatePayload {
  return { states: [], labels: [], modules: [] };
}

/** The state groups the server accepts (mirrors validStateGroups). */
export const PROJECT_TEMPLATE_STATE_GROUPS = [
  "triage",
  "backlog",
  "unstarted",
  "started",
  "completed",
  "cancelled",
] as const;

/** One-line human summary of what a template seeds, for pickers. */
export function summarizeProjectTemplatePayload(
  p: ProjectTemplatePayload | null | undefined,
): string {
  if (!p) return "No seeds";
  const parts: string[] = [];
  if (p.states?.length) parts.push(`${p.states.length} states`);
  if (p.labels?.length) parts.push(`${p.labels.length} labels`);
  if (p.modules?.length) parts.push(`${p.modules.length} modules`);
  if (p.estimate_scale?.name)
    parts.push(`estimate: ${p.estimate_scale.name}`);
  return parts.length ? parts.join(" · ") : "Defaults only";
}
