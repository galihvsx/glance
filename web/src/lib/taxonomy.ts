// taxonomy: project settings client (C6T2) — labels, states, estimates.
//
// Contract (verified 2026-10-09 against the backend):
//   Labels    POST/GET /labels, PATCH/DELETE /labels/:id   (workspace-scoped rows)
//   States    GET /states, POST /states, PATCH/DELETE /states/:id
//             (C6T2 backend additions; DELETE takes ?reassign_to=:id)
//   Estimates POST/GET /estimates, DELETE /estimates/:id,
//             POST /estimates/:id/points
// All nested under /api/v1/workspaces/:slug/projects/:identifier.
// Mutations are member (15)+; the server is the gate (403).

import { api } from "./api";

/** A workspace label (subset of the backend Label shape). */
export interface TaxLabel {
  id: string;
  name: string;
  color: string;
}

/** A project state (backend State shape). */
export interface TaxState {
  id: string;
  project_id: string;
  name: string;
  group: string;
  color: string;
  sequence: number;
}

/** One selectable value of an estimate scale. */
export interface EstimatePoint {
  id: string;
  key: string;
  value: number;
  description?: string | null;
}

/** An estimate scale with its points. */
export interface Estimate {
  id: string;
  project_id: string;
  name: string;
  points: EstimatePoint[];
}

/** The six state groups, in kanban display order (backend CHECK vocabulary). */
export const STATE_GROUPS = [
  "triage",
  "backlog",
  "unstarted",
  "started",
  "completed",
  "cancelled",
] as const;
export type StateGroup = (typeof STATE_GROUPS)[number];

export function groupLabel(group: string): string {
  switch (group) {
    case "triage":
      return "Triage";
    case "backlog":
      return "Backlog";
    case "unstarted":
      return "To do";
    case "started":
      return "In progress";
    case "completed":
      return "Done";
    case "cancelled":
      return "Cancelled";
    default:
      return group;
  }
}

/** Client-side hex color check mirroring the backend's labelColorRe. */
export function isValidHexColor(color: string): boolean {
  return /^#[0-9a-fA-F]{6}$/.test(color.trim());
}

function base(slug: string, identifier: string): string {
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;
}

export function taxonomyKeys(slug: string, identifier: string) {
  const b = `${slug}/${identifier}`;
  return {
    labels: ["taxonomy", b, "labels"] as const,
    states: ["taxonomy", b, "states"] as const,
    estimates: ["taxonomy", b, "estimates"] as const,
  };
}

// ---------------------------------------------------------------- labels

export function fetchLabels(slug: string, identifier: string): Promise<TaxLabel[]> {
  return api
    .get<{ labels: TaxLabel[] }>(`${base(slug, identifier)}/labels`)
    .then((r) => r.labels ?? []);
}

export function createLabel(
  slug: string,
  identifier: string,
  input: { name: string; color?: string },
): Promise<TaxLabel> {
  return api.post<TaxLabel>(`${base(slug, identifier)}/labels`, input);
}

export function updateLabel(
  slug: string,
  identifier: string,
  id: string,
  input: { name?: string; color?: string },
): Promise<TaxLabel> {
  return api.patch<TaxLabel>(`${base(slug, identifier)}/labels/${id}`, input);
}

export function deleteLabel(slug: string, identifier: string, id: string): Promise<void> {
  return api.del<void>(`${base(slug, identifier)}/labels/${id}`);
}

// ---------------------------------------------------------------- states

export function fetchStates(slug: string, identifier: string): Promise<TaxState[]> {
  return api
    .get<{ states: TaxState[] }>(`${base(slug, identifier)}/states`)
    .then((r) => r.states ?? []);
}

export function createState(
  slug: string,
  identifier: string,
  input: { name: string; group: string; color?: string },
): Promise<TaxState> {
  return api.post<TaxState>(`${base(slug, identifier)}/states`, input);
}

export function updateState(
  slug: string,
  identifier: string,
  id: string,
  input: { name?: string; group?: string; color?: string },
): Promise<TaxState> {
  return api.patch<TaxState>(`${base(slug, identifier)}/states/${id}`, input);
}

/**
 * Delete a state. When the server reports 409 (state in use), call again
 * with reassignTo to move its issues first.
 */
export function deleteState(
  slug: string,
  identifier: string,
  id: string,
  reassignTo?: string,
): Promise<void> {
  const q = reassignTo ? `?reassign_to=${encodeURIComponent(reassignTo)}` : "";
  return api.del<void>(`${base(slug, identifier)}/states/${id}${q}`);
}

// ------------------------------------------------------------- estimates

export function fetchEstimates(slug: string, identifier: string): Promise<Estimate[]> {
  return api
    .get<{ estimates: Estimate[] }>(`${base(slug, identifier)}/estimates`)
    .then((r) => r.estimates ?? []);
}

export function createEstimate(
  slug: string,
  identifier: string,
  input: { name: string; points: { key: string; value: number; description?: string }[] },
): Promise<Estimate> {
  return api.post<Estimate>(`${base(slug, identifier)}/estimates`, input);
}

export function deleteEstimate(slug: string, identifier: string, id: string): Promise<void> {
  return api.del<void>(`${base(slug, identifier)}/estimates/${id}`);
}

export function addEstimatePoints(
  slug: string,
  identifier: string,
  id: string,
  points: { key: string; value: number; description?: string }[],
): Promise<Estimate> {
  return api.post<Estimate>(`${base(slug, identifier)}/estimates/${id}/points`, { points });
}
