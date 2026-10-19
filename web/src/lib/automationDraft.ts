// automationDraft: the rule-builder draft model for AutomationsSection
// (C16T4). Pure, framework-free: converts between the builder's draft
// state and the backend AutomationTrigger / AutomationAction shapes, and
// holds the display labels. Validation errors come back as plain
// strings so the UI can show honest copy instead of dropping input.

import { PRIORITY_LABELS } from "./types";
import type { AutomationAction, AutomationRule, AutomationRuleInput, AutomationTrigger } from "./automations";

export type TriggerType = AutomationTrigger["type"];
export type ActionType = AutomationAction["type"];

/** Backend defaults (mirror internal/service/automation_schedule.go). */
export const DEFAULT_WINDOW_HOURS = 48;
export const DEFAULT_STALE_DAYS = 7;

export const TRIGGER_LABELS: Record<TriggerType, string> = {
  "issue.state_changed": "Issue state changes",
  "issue.created": "Issue created",
  "issue.assigned": "User is assigned",
  "issue.unassigned": "Assignee is removed",
  "issue.labels_changed": "Labels change",
  "issue.priority_changed": "Priority changes",
  "issue.due_date_changed": "Due date changes",
  "issue.estimate_changed": "Estimate changes",
  "issue.comment_added": "Comment is added",
  "issue.due_soon": "Due soon (scheduled)",
  "issue.overdue": "Overdue (scheduled)",
  "issue.stale": "Goes stale (scheduled)",
  "cycle.ending_soon": "Cycle ending soon (scheduled)",
};

/** Trigger kinds that are evaluated on a ticker, not by issue events. */
export const SCHEDULED_TRIGGER_TYPES: TriggerType[] = [
  "issue.due_soon",
  "issue.overdue",
  "issue.stale",
  "cycle.ending_soon",
];

export const ACTION_LABELS: Record<ActionType, string> = {
  assign: "Assign user",
  add_label: "Add label",
  add_comment: "Post comment",
  set_priority: "Set priority",
  set_state: "Set state",
  remove_label: "Remove label",
  unassign: "Remove all assignees",
  set_estimate: "Set estimate",
  set_due_date: "Set due date",
  move_to_cycle: "Move to cycle",
  move_to_module: "Move to module",
  add_watcher: "Add watcher",
  send_webhook: "Send webhook",
};

export const PRIORITY_OPTIONS = PRIORITY_LABELS.map((label, value) => ({
  value,
  label,
}));

export interface TriggerDraft {
  type: TriggerType;
  fromState: string; // "" = any
  toState: string; // "" = any
  labelIds: string[]; // labels_changed filter (empty = any label change)
  fromPriority: string; // "" = any, else "0".."4"
  toPriority: string; // "" = any, else "0".."4"
  windowHours: string; // due_soon filter ("" = server default 48)
  staleDays: string; // stale filter ("" = server default 7)
}

export const emptyTriggerDraft = (): TriggerDraft => ({
  type: "issue.state_changed",
  fromState: "",
  toState: "",
  labelIds: [],
  fromPriority: "",
  toPriority: "",
  windowHours: "",
  staleDays: "",
});

export type DueDateMode = "absolute" | "relative";

/** send_webhook payload_template vocabulary (mirrors the T0 backend
 *  template contract in internal/service/automation_sendwebhook.go).
 *  Placeholders substitute the JSON encoding of the value — never
 *  quoted in the template. */
export interface TemplateVariable {
  placeholder: string;
  description: string;
}

export const SEND_WEBHOOK_TEMPLATE_VARS: TemplateVariable[] = [
  { placeholder: "{{issue.id}}", description: "issue UUID" },
  { placeholder: "{{issue.identifier}}", description: "display id, e.g. ENG-123" },
  { placeholder: "{{issue.title}}", description: "issue title" },
  { placeholder: "{{issue.state}}", description: "state name" },
  { placeholder: "{{issue.priority}}", description: "priority, 0–4" },
  { placeholder: "{{issue.labels}}", description: "label names (array)" },
  { placeholder: "{{issue.assignees}}", description: "assignee display names (array)" },
  { placeholder: "{{rule.id}}", description: "rule UUID" },
  { placeholder: "{{trigger}}", description: "trigger type" },
  { placeholder: "{{fired_at}}", description: "firing time, RFC3339 UTC" },
];

/** The placeholder the API puts in send_webhook header values on reads.
 *  It is rejected at write time, so the UI must never round-trip it. */
export const SEND_WEBHOOK_MASKED_VALUE = "********";

/** Transport-owned header names the backend rejects (case-insensitive). */
export const SEND_WEBHOOK_BLOCKED_HEADERS = ["host", "content-length"];

export interface DraftAction {
  type: ActionType;
  user_id: string;
  label_id: string;
  body: string;
  priority: number; // set_priority target (0-4)
  state_id: string; // set_state target ("" = unset)
  cycle_id: string; // move_to_cycle target ("" = unset)
  module_id: string; // move_to_module target ("" = unset)
  estimate: string; // set_estimate target: point UUID ("" = unset)
  dueDateMode: DueDateMode; // set_due_date mode
  dueDate: string; // set_due_date absolute: YYYY-MM-DD
  dueDateOffset: string; // set_due_date relative: days ("" = unset)
  webhookUrl: string; // send_webhook target ("" = unset)
  webhookMethod: "POST" | "PUT"; // send_webhook HTTP method
  /** send_webhook header rows. Values are write-only: on edit the names
   *  load from the API with empty values; rows without a value are not
   *  sent, so re-enter a value to keep the header. */
  webhookHeaders: { name: string; value: string }[];
  webhookPayloadTemplate: string; // send_webhook JSON template ("" = default envelope)
}

export const emptyDraftAction = (): DraftAction => ({
  type: "add_comment",
  user_id: "",
  label_id: "",
  body: "",
  priority: 2,
  state_id: "",
  cycle_id: "",
  module_id: "",
  estimate: "",
  dueDateMode: "absolute",
  dueDate: "",
  dueDateOffset: "",
  webhookUrl: "",
  webhookMethod: "POST",
  webhookHeaders: [],
  webhookPayloadTemplate: "",
});

export interface RuleDraft {
  name: string;
  trigger: TriggerDraft;
  actions: DraftAction[];
}

export const emptyRuleDraft = (): RuleDraft => ({
  name: "",
  trigger: emptyTriggerDraft(),
  actions: [emptyDraftAction()],
});

const validPriorityStr = (s: string): boolean =>
  /^[0-4]$/.test(s.trim());

/** Mirror the backend's send_webhook URL rule: absolute URL with an
 *  http or https scheme and a host. */
const validWebhookUrl = (s: string): boolean => {
  try {
    const u = new URL(s);
    return (u.protocol === "http:" || u.protocol === "https:") && !!u.host;
  } catch {
    return false;
  }
};

/** Parse a positive-integer window/days input; "" means "server default". */
function parsePositive(s: string): number | null | "invalid" {
  const t = s.trim();
  if (!t) return null;
  if (!/^\d+$/.test(t)) return "invalid";
  const n = parseInt(t, 10);
  return n > 0 ? n : "invalid";
}

function triggerToInput(
  t: TriggerDraft,
): { ok: true; trigger: AutomationTrigger } | { ok: false; error: string } {
  const base = {
    type: t.type,
    from_states: null as string[] | null,
    to_states: null as string[] | null,
  };
  switch (t.type) {
    case "issue.state_changed":
      return {
        ok: true,
        trigger: {
          ...base,
          from_states: t.fromState ? [t.fromState] : null,
          to_states: t.toState ? [t.toState] : null,
        },
      };
    case "issue.labels_changed":
      return {
        ok: true,
        trigger: {
          ...base,
          label_ids: t.labelIds.length ? t.labelIds : null,
        },
      };
    case "issue.priority_changed": {
      if (t.fromPriority && !validPriorityStr(t.fromPriority))
        return { ok: false, error: "Pick a valid “from” priority." };
      if (t.toPriority && !validPriorityStr(t.toPriority))
        return { ok: false, error: "Pick a valid “to” priority." };
      return {
        ok: true,
        trigger: {
          ...base,
          from_priorities: t.fromPriority ? [Number(t.fromPriority)] : null,
          to_priorities: t.toPriority ? [Number(t.toPriority)] : null,
        },
      };
    }
    case "issue.due_soon": {
      const w = parsePositive(t.windowHours);
      if (w === "invalid")
        return {
          ok: false,
          error: "“Due within” must be a whole number of hours, at least 1.",
        };
      return { ok: true, trigger: { ...base, window_hours: w } };
    }
    case "issue.stale": {
      const d = parsePositive(t.staleDays);
      if (d === "invalid")
        return {
          ok: false,
          error: "“Untouched for” must be a whole number of days, at least 1.",
        };
      return { ok: true, trigger: { ...base, stale_days: d } };
    }
    // The rest take no filters in v1; the server rejects any.
    default:
      return { ok: true, trigger: base };
  }
}

function actionToInput(
  a: DraftAction,
  index: number,
): { ok: true; action: AutomationAction } | { ok: false; error: string } {
  const label = `Action ${index + 1}`;
  switch (a.type) {
    case "assign":
      if (!a.user_id)
        return { ok: false, error: `${label}: pick a user to assign.` };
      return { ok: true, action: { type: "assign", user_id: a.user_id } };
    case "add_label":
      if (!a.label_id)
        return { ok: false, error: `${label}: pick a label to add.` };
      return { ok: true, action: { type: "add_label", label_id: a.label_id } };
    case "add_comment": {
      const body = a.body.trim();
      if (!body)
        return { ok: false, error: `${label}: comment text can't be empty.` };
      return { ok: true, action: { type: "add_comment", body } };
    }
    case "set_priority":
      return { ok: true, action: { type: "set_priority", priority: a.priority } };
    case "set_state":
      if (!a.state_id)
        return { ok: false, error: `${label}: pick a state.` };
      return { ok: true, action: { type: "set_state", state_id: a.state_id } };
    case "remove_label":
      if (!a.label_id)
        return { ok: false, error: `${label}: pick a label to remove.` };
      return { ok: true, action: { type: "remove_label", label_id: a.label_id } };
    case "unassign":
      return { ok: true, action: { type: "unassign" } };
    case "set_estimate":
      if (!a.estimate)
        return { ok: false, error: `${label}: pick an estimate point.` };
      return { ok: true, action: { type: "set_estimate", estimate: a.estimate } };
    case "set_due_date":
      if (a.dueDateMode === "absolute") {
        if (!/^\d{4}-\d{2}-\d{2}$/.test(a.dueDate.trim()))
          return {
            ok: false,
            error: `${label}: pick a calendar date for the due date.`,
          };
        return {
          ok: true,
          action: { type: "set_due_date", due_date: a.dueDate.trim() },
        };
      }
      {
        const n = parsePositive(a.dueDateOffset);
        if (n === "invalid")
          return {
            ok: false,
            error: `${label}: the relative offset must be a whole number of days, at least 1.`,
          };
        // null can't happen here: "" would only be "invalid" if n==null... no:
        // parsePositive returns null for "". Treat empty as invalid too.
        if (n === null)
          return {
            ok: false,
            error: `${label}: enter how many days after the rule fires.`,
          };
        return {
          ok: true,
          action: { type: "set_due_date", due_date: `+${n}d` },
        };
      }
    case "move_to_cycle":
      if (!a.cycle_id)
        return { ok: false, error: `${label}: pick a cycle.` };
      return {
        ok: true,
        action: { type: "move_to_cycle", cycle_id: a.cycle_id },
      };
    case "move_to_module":
      if (!a.module_id)
        return { ok: false, error: `${label}: pick a module.` };
      return {
        ok: true,
        action: { type: "move_to_module", module_id: a.module_id },
      };
    case "add_watcher":
      if (!a.user_id)
        return { ok: false, error: `${label}: pick a user to watch.` };
      return { ok: true, action: { type: "add_watcher", user_id: a.user_id } };
    case "send_webhook": {
      const url = a.webhookUrl.trim();
      if (!url)
        return { ok: false, error: `${label}: enter a target URL.` };
      if (!validWebhookUrl(url))
        return {
          ok: false,
          error: `${label}: the URL must be an absolute http(s) URL.`,
        };
      if (a.webhookMethod !== "POST" && a.webhookMethod !== "PUT")
        return { ok: false, error: `${label}: pick POST or PUT.` };
      const headers: Record<string, string> = {};
      for (const row of a.webhookHeaders) {
        const name = row.name.trim();
        const value = row.value;
        if (!name && !value) continue; // untouched blank row
        if (!name)
          return {
            ok: false,
            error: `${label}: every header row needs a name.`,
          };
        if (SEND_WEBHOOK_BLOCKED_HEADERS.includes(name.toLowerCase()))
          return {
            ok: false,
            error: `${label}: the “${name}” header is managed by glance and can't be set.`,
          };
        if (value === SEND_WEBHOOK_MASKED_VALUE)
          return {
            ok: false,
            error: `${label}: the “${name}” header value can't be the placeholder “********” — re-enter the real value.`,
          };
        // Name-only rows (e.g. names echoed back on edit with no value
        // re-entered) are dropped: PATCH replaces headers wholesale, so
        // a header is kept only by re-sending its value.
        if (!value) continue;
        headers[name] = value;
      }
      const template = a.webhookPayloadTemplate.trim();
      const action: AutomationAction = {
        type: "send_webhook",
        url,
        method: a.webhookMethod,
        headers,
      };
      // Blank template = the backend's default automation.webhook
      // envelope; the template echoes verbatim otherwise.
      if (template) action.payload_template = template;
      return { ok: true, action };
    }
  }
}

/** Convert a builder draft to the API input; null-ish failure returns
 *  { ok: false } with a message fit for a toast. */
export function draftToInput(
  d: RuleDraft,
): { ok: true; input: AutomationRuleInput } | { ok: false; error: string } {
  const name = d.name.trim();
  if (!name) return { ok: false, error: "Give the rule a name." };
  if (d.actions.length === 0)
    return { ok: false, error: "Add at least one action." };
  const t = triggerToInput(d.trigger);
  if (!t.ok) return t;
  const actions: AutomationAction[] = [];
  for (let i = 0; i < d.actions.length; i++) {
    const a = actionToInput(d.actions[i], i);
    if (!a.ok) return a;
    actions.push(a.action);
  }
  return { ok: true, input: { name, trigger: t.trigger, actions } };
}

/** Load a stored rule back into the builder. */
export function draftFromRule(rule: AutomationRule): RuleDraft {
  const t = rule.trigger;
  const trigger: TriggerDraft = {
    type: t.type,
    fromState: t.from_states?.[0] ?? "",
    toState: t.to_states?.[0] ?? "",
    labelIds: t.label_ids ?? [],
    fromPriority:
      t.from_priorities?.length ? String(t.from_priorities[0]) : "",
    toPriority: t.to_priorities?.length ? String(t.to_priorities[0]) : "",
    windowHours: t.window_hours != null ? String(t.window_hours) : "",
    staleDays: t.stale_days != null ? String(t.stale_days) : "",
  };
  return {
    name: rule.name,
    trigger,
    actions: rule.actions.map((a) => ({
      type: a.type,
      user_id: a.user_id ?? "",
      label_id: a.label_id ?? "",
      body: a.body ?? "",
      priority: a.priority ?? 2,
      state_id: a.state_id ?? "",
      cycle_id: a.cycle_id ?? "",
      module_id: a.module_id ?? "",
      estimate: a.estimate ?? "",
      dueDateMode: (a.due_date ?? "").startsWith("+")
        ? "relative"
        : "absolute",
      dueDate: (a.due_date ?? "").startsWith("+") ? "" : (a.due_date ?? ""),
      dueDateOffset: (a.due_date ?? "").startsWith("+")
        ? (a.due_date ?? "").slice(1, -1)
        : "",
      // send_webhook: header values come back masked ("********") — load
      // the names only, with empty values, and never round-trip the mask.
      webhookUrl: a.url ?? "",
      webhookMethod:
        String(a.method ?? "POST").trim().toUpperCase() === "PUT"
          ? "PUT"
          : "POST",
      webhookHeaders: Object.keys(a.headers ?? {}).map((name) => ({
        name,
        value: "",
      })),
      webhookPayloadTemplate: a.payload_template ?? "",
    })),
  };
}
