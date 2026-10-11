// automations: project settings client (C11T1) — workflow automation rules.
//
// Contract (verified against the backend):
//   GET/POST .../automations, PATCH/DELETE .../automations/:ruleID
// nested under /api/v1/workspaces/:slug/projects/:identifier.
// Reads need member (15)+; writes need member (15)+; the server is the
// gate (403). Max 25 rules per project (409 beyond).

import { api } from "./api";
import { PRIORITY_LABELS } from "./types";

/** Trigger: issue.state_changed with optional from/to state filters;
 *  issue.created (fires on creation, no filters in v1). C16T0 adds the
 *  field events: issue.assigned / issue.unassigned (no filters in v1),
 *  issue.labels_changed (optional label_ids filter), issue.priority_changed
 *  (optional from_priorities / to_priorities filters, 0-4),
 *  issue.due_date_changed, issue.estimate_changed, issue.comment_added
 *  (no filters). C16T1 adds the scheduled triggers, evaluated by the
 *  automation-schedule ticker instead of by issue events: issue.due_soon
 *  (optional window_hours filter, default 48), issue.overdue (no filters),
 *  issue.stale (optional stale_days filter, default 7),
 *  cycle.ending_soon (no filters). Filters that don't belong to a trigger
 *  type are rejected by the server rather than silently ignored. */
export interface AutomationTrigger {
  type:
    | "issue.state_changed"
    | "issue.created"
    | "issue.assigned"
    | "issue.unassigned"
    | "issue.labels_changed"
    | "issue.priority_changed"
    | "issue.due_date_changed"
    | "issue.estimate_changed"
    | "issue.comment_added"
    | "issue.due_soon"
    | "issue.overdue"
    | "issue.stale"
    | "cycle.ending_soon";
  from_states: string[] | null;
  to_states: string[] | null;
  /** issue.labels_changed only: fire only when a listed label is added
   *  or removed; null/empty matches any label change. */
  label_ids?: string[] | null;
  /** issue.priority_changed only: 0-4 (none/low/medium/high/urgent). */
  from_priorities?: number[] | null;
  /** issue.priority_changed only: 0-4 (none/low/medium/high/urgent). */
  to_priorities?: number[] | null;
  /** issue.due_soon only: hours from now; null = server default (48). */
  window_hours?: number | null;
  /** issue.stale only: days with no update; null = server default (7). */
  stale_days?: number | null;
}

/** One ordered action (C16T2 adds remove_label, unassign, set_estimate,
 *  set_due_date, move_to_cycle, move_to_module, add_watcher). */
export interface AutomationAction {
  type:
    | "assign"
    | "add_label"
    | "add_comment"
    | "set_priority"
    | "set_state"
    | "remove_label"
    | "unassign"
    | "set_estimate"
    | "set_due_date"
    | "move_to_cycle"
    | "move_to_module"
    | "add_watcher";
  user_id?: string;
  label_id?: string;
  body?: string;
  /** set_priority: 0-4 (None/Low/Medium/High/Urgent). */
  priority?: number;
  /** set_state: target state UUID in this project. */
  state_id?: string;
  /** move_to_cycle: target cycle UUID in this project. */
  cycle_id?: string;
  /** move_to_module: target module UUID in this project. */
  module_id?: string;
  /** set_estimate: estimate-point UUID on one of this project's scales. */
  estimate?: string;
  /** set_due_date: absolute ISO date "YYYY-MM-DD" or relative "+Nd". */
  due_date?: string;
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
  type: AutomationAction["type"];
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
  labels: { id: string; name: string }[] = [],
): string {
  const labelName = (id: string) =>
    labels.find((l) => l.id === id)?.name ?? "unknown label";
  const pri = (p: number) => PRIORITY_LABELS[p] ?? `P${p}`;
  switch (trigger.type) {
    case "issue.created":
      return "When an issue is created";
    case "issue.assigned":
      return "When a user is assigned";
    case "issue.unassigned":
      return "When an assignee is removed";
    case "issue.labels_changed": {
      const ids = trigger.label_ids ?? [];
      return ids.length
        ? `When label ${ids.map(labelName).join(", ")} is added or removed`
        : "When any label is added or removed";
    }
    case "issue.priority_changed": {
      const from =
        trigger.from_priorities?.length != null && trigger.from_priorities.length > 0
          ? trigger.from_priorities.map(pri).join(", ")
          : "any priority";
      const to =
        trigger.to_priorities?.length != null && trigger.to_priorities.length > 0
          ? trigger.to_priorities.map(pri).join(", ")
          : "any priority";
      return `When priority changes from ${from} → ${to}`;
    }
    case "issue.due_date_changed":
      return "When the due date changes";
    case "issue.estimate_changed":
      return "When the estimate changes";
    case "issue.comment_added":
      return "When a comment is added";
    case "issue.due_soon":
      return `When due within ${trigger.window_hours ?? 48}h (checked on a schedule)`;
    case "issue.overdue":
      return "When overdue (checked on a schedule)";
    case "issue.stale":
      return `When untouched for ${trigger.stale_days ?? 7}d (checked on a schedule)`;
    case "cycle.ending_soon":
      return "When attached to a cycle ending within 72h (checked on a schedule)";
    case "issue.state_changed": {
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
  }
}

export interface ActionLookups {
  cycles?: { id: string; name: string }[];
  modules?: { id: string; name: string }[];
  /** Flattened estimate points across all scales, with their scale name. */
  estimatePoints?: { id: string; key: string; scaleName: string }[];
}

/** Human summary of one action for the rule list. */
export function describeAction(
  action: AutomationAction,
  users: { id: string; name?: string | null; email: string }[],
  labels: { id: string; name: string }[],
  states: { id: string; name: string }[] = [],
  lookups: ActionLookups = {},
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
    case "remove_label": {
      const l = labels.find((x) => x.id === action.label_id);
      return `Remove label ${l?.name || "unknown label"}`;
    }
    case "unassign":
      return "Remove all assignees";
    case "set_estimate": {
      const p = (lookups.estimatePoints ?? []).find(
        (x) => x.id === action.estimate,
      );
      return `Set estimate to ${p ? `${p.scaleName} ${p.key}` : "unknown estimate"}`;
    }
    case "set_due_date": {
      const raw = action.due_date ?? "";
      return raw.startsWith("+")
        ? `Set due date ${raw.slice(1)}d after the rule fires`
        : `Set due date to ${raw || "unknown date"}`;
    }
    case "move_to_cycle": {
      const c = (lookups.cycles ?? []).find((x) => x.id === action.cycle_id);
      return `Move to cycle ${c?.name || "unknown cycle"}`;
    }
    case "move_to_module": {
      const m = (lookups.modules ?? []).find((x) => x.id === action.module_id);
      return `Move to module ${m?.name || "unknown module"}`;
    }
    case "add_watcher": {
      const u = users.find((x) => x.id === action.user_id);
      return `Add watcher ${u?.name || u?.email || "unknown user"}`;
    }
  }
}
