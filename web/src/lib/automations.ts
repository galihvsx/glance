// automations: project settings client (C11T1) — workflow automation rules.
//
// Contract (verified against the backend):
//   GET/POST .../automations, PATCH/DELETE .../automations/:ruleID
// nested under /api/v1/workspaces/:slug/projects/:identifier.
// Reads need member (15)+; writes need member (15)+; the server is the
// gate (403). Max 25 rules per project (409 beyond).

import { api } from "./api";
import { PRIORITY_LABELS } from "./types";

/** Trigger: issue.state_changed with optional from/to state filters, or
 *  issue.created (C12T2 — fires on creation, no filters in v1). */
export interface AutomationTrigger {
  type: "issue.state_changed" | "issue.created";
  from_states: string[] | null;
  to_states: string[] | null;
}

/** One ordered action. */
export interface AutomationAction {
  type: "assign" | "add_label" | "add_comment" | "set_priority" | "set_state";
  user_id?: string;
  label_id?: string;
  body?: string;
  /** set_priority: 0-4 (None/Low/Medium/High/Urgent). */
  priority?: number;
  /** set_state: target state UUID in this project. */
  state_id?: string;
}

/** A stored automation rule (backend AutomationRule shape). */
export interface AutomationRule {
  id: string;
  project_id: string;
  name: string;
  trigger: AutomationTrigger;
  actions: AutomationAction[];
  enabled: boolean;
  created_by: string;
  created_at: string;
  updated_at: string;
}

export interface AutomationRuleInput {
  name: string;
  trigger: AutomationTrigger;
  actions: AutomationAction[];
}

export interface AutomationRulePatch {
  name?: string;
  enabled?: boolean;
  trigger?: AutomationTrigger;
  actions?: AutomationAction[];
}

function base(slug: string, identifier: string): string {
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/automations`;
}

export function automationKeys(slug: string, identifier: string) {
  return {
    rules: ["automations", `${slug}/${identifier}`, "rules"] as const,
    runs: ["automations", `${slug}/${identifier}`, "runs"] as const,
  };
}

export function fetchAutomationRules(
  slug: string,
  identifier: string,
): Promise<AutomationRule[]> {
  return api
    .get<{ automations: AutomationRule[] }>(base(slug, identifier))
    .then((r) => r.automations ?? []);
}

export function createAutomationRule(
  slug: string,
  identifier: string,
  input: AutomationRuleInput,
): Promise<AutomationRule> {
  return api.post<AutomationRule>(base(slug, identifier), input);
}

export function updateAutomationRule(
  slug: string,
  identifier: string,
  id: string,
  input: AutomationRulePatch,
): Promise<AutomationRule> {
  return api.patch<AutomationRule>(`${base(slug, identifier)}/${id}`, input);
}

export function deleteAutomationRule(
  slug: string,
  identifier: string,
  id: string,
): Promise<void> {
  return api.del<void>(`${base(slug, identifier)}/${id}`);
}

/** One fired action's recorded outcome (backend AutomationActionResult). */
export interface AutomationActionResult {
  type: "assign" | "add_label" | "add_comment" | "set_priority" | "set_state";
  ok: boolean;
  error?: string;
}

/** One automation rule firing (backend AutomationRun shape). */
export interface AutomationRun {
  id: string;
  rule_id: string;
  rule_name: string;
  issue_id: string;
  issue_display_id: string | null;
  trigger_type: string;
  fired_at: string;
  actions: AutomationActionResult[];
}

export interface AutomationRunFilter {
  rule_id?: string;
  issue_id?: string;
  limit?: number;
}

export function fetchAutomationRuns(
  slug: string,
  identifier: string,
  filter: AutomationRunFilter = {},
): Promise<AutomationRun[]> {
  const params = new URLSearchParams();
  if (filter.rule_id) params.set("rule_id", filter.rule_id);
  if (filter.issue_id) params.set("issue_id", filter.issue_id);
  if (filter.limit != null) params.set("limit", String(filter.limit));
  const qs = params.toString();
  return api
    .get<{ runs: AutomationRun[] }>(
      `${base(slug, identifier)}/runs${qs ? `?${qs}` : ""}`,
    )
    .then((r) => r.runs ?? []);
}

/** Human summary of a trigger for the rule list. */
export function describeTrigger(
  trigger: AutomationTrigger,
  states: { id: string; name: string }[],
): string {
  if (trigger.type === "issue.created") {
    return "When an issue is created";
  }
  const nameOf = (id: string) =>
    states.find((s) => s.id === id)?.name ?? "unknown state";
  const from =
    trigger.from_states?.length
      ? trigger.from_states.map(nameOf).join(", ")
      : "any state";
  const to =
    trigger.to_states?.length
      ? trigger.to_states.map(nameOf).join(", ")
      : "any state";
  return `When issue moves from ${from} → ${to}`;
}

/** Human summary of one action for the rule list. */
export function describeAction(
  action: AutomationAction,
  users: { id: string; name?: string | null; email: string }[],
  labels: { id: string; name: string }[],
  states: { id: string; name: string }[] = [],
): string {
  switch (action.type) {
    case "assign": {
      const u = users.find((x) => x.id === action.user_id);
      return `Assign to ${u?.name || u?.email || "unknown user"}`;
    }
    case "add_label": {
      const l = labels.find((x) => x.id === action.label_id);
      return `Add label ${l?.name || "unknown label"}`;
    }
    case "add_comment":
      return `Post comment: “${(action.body ?? "").slice(0, 60)}${
        (action.body ?? "").length > 60 ? "…" : ""
      }”`;
    case "set_priority":
      return `Set priority to ${PRIORITY_LABELS[action.priority ?? 0] ?? `P${action.priority}`}`;
    case "set_state": {
      const s = states.find((x) => x.id === action.state_id);
      return `Move to ${s?.name || "unknown state"}`;
    }
  }
}
