// Workflow transition controls client (C68T1). Backend contract on main
// (C68T0):
//   GET/PUT /api/v1/workspaces/{slug}/projects/{identifier}/workflow
//     → {states, transitions: [{from_state_id, to_state_id}]}
//   denied moves → 409 code "state_transition_not_allowed",
//     details {"allowed": [state-id…]} naming permitted next-state IDs.
//   A project with zero transition rows is unrestricted (any move allowed).

import { useQuery } from "@tanstack/react-query";
import { api, ApiError } from "./api";
import type { IssueState } from "./types";

/** One allowed directed state move. */
export interface WorkflowTransition {
  from_state_id: string;
  to_state_id: string;
}

/** A project's transition governance: its states plus the allowlist. */
export interface Workflow {
  states: IssueState[];
  transitions: WorkflowTransition[];
}

function workflowPath(slug: string, identifier: string): string {
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/workflow`;
}

export function getWorkflow(slug: string, identifier: string): Promise<Workflow> {
  return api.get<Workflow>(workflowPath(slug, identifier));
}

export function setWorkflow(
  slug: string,
  identifier: string,
  transitions: WorkflowTransition[],
): Promise<Workflow> {
  return api.put<Workflow>(workflowPath(slug, identifier), { transitions });
}

/** React Query hook for the project's workflow. */
export function useWorkflow(slug: string, identifier: string, enabled = true) {
  return useQuery({
    queryKey: ["workflow", slug, identifier],
    queryFn: () => getWorkflow(slug, identifier),
    enabled,
  });
}

/** Allowed next-state IDs from `fromStateId`, or null when the project is
 *  unrestricted (zero transition rows). An empty array means the from-state
 *  has no outgoing rules — every move away from it is denied. */
export function allowedNextStateIds(
  workflow: Workflow,
  fromStateId: string,
): string[] | null {
  if (workflow.transitions.length === 0) return null;
  return workflow.transitions
    .filter((t) => t.from_state_id === fromStateId)
    .map((t) => t.to_state_id);
}

/** Client-side move legality check (hint only — the server enforces). */
export function isTransitionAllowed(
  workflow: Workflow,
  fromStateId: string,
  toStateId: string,
): boolean {
  if (fromStateId === toStateId) return true;
  const allowed = allowedNextStateIds(workflow, fromStateId);
  if (allowed === null) return true;
  return allowed.includes(toStateId);
}

/** Builds the "move denied" message from a 409 state_transition_not_allowed
 *  ApiError, naming the permitted next states. Returns null for any other
 *  error shape. */
export function stateTransitionConflictMessage(
  e: unknown,
  stateNames: Map<string, string>,
): string | null {
  if (!(e instanceof ApiError) || e.code !== "state_transition_not_allowed") {
    return null;
  }
  const details = e.details as { allowed?: unknown } | undefined;
  const allowed = Array.isArray(details?.allowed)
    ? (details.allowed as unknown[]).filter(
        (a): a is string => typeof a === "string",
      )
    : [];
  const base = "State transition not allowed by the project's workflow.";
  if (allowed.length === 0) return base;
  const named = allowed.map((id) => stateNames.get(id) ?? id);
  return `${base} Permitted next states: ${named.join(", ")}`;
}
