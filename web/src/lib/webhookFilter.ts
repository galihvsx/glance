// Webhook event filters (C69T1).
//
// Client-side model for the per-webhook condition lists the C69T0
// backend stores in webhooks.filter (NULL/empty = no filter → deliver
// everything). All functions here are pure so the editor stays thin and
// the vocabulary is pinned by webhookFilter.test.ts (backend drift in
// internal/service/webhook_filter.go fails loudly).
//
// Field vocabulary (backend `WebhookFilterField*`): priority,
// state_group, state_id, assignee_id, label_id, project_id,
// target_date. state_id is deliberately UI-omitted (cycle-69 plan): it
// is not browsable, so exposing it here would only invite raw-UUID
// paste. Ops: eq (scalar), in (non-empty array). All conditions are
// AND-ed; assignee_id/label_id match with ANY semantics (Plane).
//
// Backend validation (mirrored by validateWebhookCondition below):
// priority ∈ none|low|medium|high|urgent (case-insensitive); state_group
// ∈ backlog|unstarted|started|completed|cancelled (case-insensitive —
// Plane's vocabulary, NOT glance's states."group" CHECK: glance's own
// "triage" group is rejected by the backend); UUIDs for the *_id fields;
// target_date is strict YYYY-MM-DD.
//
// Wire tri-state on PATCH (internal/api/notify_handler.go):
//   absent key        → untouched
//   {filter: null}    → clear the filter
//   {filter: [...]}   → replace
// JSON.stringify drops undefined keys but keeps null, so the client
// builds the payload with explicit undefined/null — see buildFilterPatch.

import type { WebhookUpdateBody } from "./webhooks";

export type WebhookFilterOp = "eq" | "in";

/** One {field, op, value} filter row. value is a scalar for eq and a
 *  non-empty string array for in (the backend's shape after JSON
 *  decoding). */
export interface WebhookCondition {
  field: string;
  op: WebhookFilterOp;
  value: string | string[];
}

/** A webhook's event filter: conditions AND-ed together. */
export type WebhookFilter = WebhookCondition[];

/** The fields the UI exposes. state_id is deliberately omitted. */
export const WEBHOOK_FILTER_FIELDS = [
  { value: "priority", label: "Priority" },
  { value: "state_group", label: "State group" },
  { value: "assignee_id", label: "Assignee" },
  { value: "label_id", label: "Label" },
  { value: "project_id", label: "Project" },
  { value: "target_date", label: "Target date" },
] as const;

export const WEBHOOK_FILTER_OPS: ReadonlyArray<{
  value: WebhookFilterOp;
  label: string;
}> = [
  { value: "eq", label: "is" },
  { value: "in", label: "is one of" },
];

/** Closed priority vocabulary (backend webhookFilterPriorities). */
export const PRIORITY_OPTIONS = [
  { value: "none", label: "None" },
  { value: "low", label: "Low" },
  { value: "medium", label: "Medium" },
  { value: "high", label: "High" },
  { value: "urgent", label: "Urgent" },
] as const;

/** Closed state_group vocabulary (backend webhookFilterStateGroups).
 *  Mirrors Plane's 5 groups — "triage" is NOT here because the backend
 *  rejects it (recorded scope decision, cycle-69 plan). */
export const STATE_GROUP_OPTIONS = [
  { value: "backlog", label: "Backlog" },
  { value: "unstarted", label: "To do" },
  { value: "started", label: "In progress" },
  { value: "completed", label: "Done" },
  { value: "cancelled", label: "Cancelled" },
] as const;

/** Fields whose values are entity UUIDs (picker-backed). */
export function isUuidField(field: string): boolean {
  return (
    field === "assignee_id" || field === "label_id" || field === "project_id"
  );
}

/** Fields with a closed option list (picker-backed, no free text). */
export function isOptionField(field: string): boolean {
  return field === "priority" || field === "state_group";
}

/** Options for a closed-vocabulary field, or null for the others. */
export function fieldOptions(
  field: string,
): ReadonlyArray<{ value: string; label: string }> | null {
  if (field === "priority") return PRIORITY_OPTIONS;
  if (field === "state_group") return STATE_GROUP_OPTIONS;
  return null;
}

const UUID_RE =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const DATE_RE = /^\d{4}-\d{2}-\d{2}$/;

/** Mirrors backend validateWebhookCondition (400 messages). Returns an
 *  error message or null when the condition is well-formed. */
export function validateWebhookCondition(c: WebhookCondition): string | null {
  if (!WEBHOOK_FILTER_FIELDS.some((f) => f.value === c.field)) {
    return `Unknown field "${c.field}".`;
  }
  if (c.op !== "eq" && c.op !== "in") {
    return `Unknown op "${c.op}".`;
  }
  const values = c.op === "in" ? c.value : [c.value];
  if (!Array.isArray(values)) {
    return `Op "${c.op}" requires ${c.op === "in" ? "an array" : "a scalar"} value.`;
  }
  if (c.op === "in" && (values as unknown[]).length === 0) {
    return 'Op "in" requires a non-empty value list.';
  }
  for (const v of values as unknown[]) {
    if (typeof v !== "string" || v.trim() === "") {
      return "Filter values must be non-empty strings.";
    }
    const s = v as string;
    switch (c.field) {
      case "priority":
        if (!PRIORITY_OPTIONS.some((o) => o.value === s.toLowerCase())) {
          return `Priority must be one of none|low|medium|high|urgent, got "${s}".`;
        }
        break;
      case "state_group":
        if (!STATE_GROUP_OPTIONS.some((o) => o.value === s.toLowerCase())) {
          return `State group must be one of backlog|unstarted|started|completed|cancelled, got "${s}".`;
        }
        break;
      case "assignee_id":
      case "label_id":
      case "project_id":
        if (!UUID_RE.test(s)) {
          return `${fieldLabel(c.field)} must be a UUID, got "${s}".`;
        }
        break;
      case "target_date":
        if (!DATE_RE.test(s)) {
          return `Target date must be an ISO-8601 date (YYYY-MM-DD), got "${s}".`;
        }
        break;
    }
  }
  return null;
}

function fieldLabel(field: string): string {
  return WEBHOOK_FILTER_FIELDS.find((f) => f.value === field)?.label ?? field;
}

/** Name lookups the editor resolves UUID values through for summaries. */
export interface LookupMaps {
  projects: Map<string, string>;
  members: Map<string, string>;
  labels: Map<string, string>;
}

function lookupName(field: string, id: string, lookups: LookupMaps): string {
  if (field === "assignee_id") return lookups.members.get(id) ?? id;
  if (field === "label_id") return lookups.labels.get(id) ?? id;
  if (field === "project_id") return lookups.projects.get(id) ?? id;
  return id;
}

function optionLabel(field: string, value: string): string {
  const opts = fieldOptions(field);
  return opts?.find((o) => o.value === value.toLowerCase())?.label ?? value;
}

function displayValue(
  field: string,
  value: string,
  lookups: LookupMaps,
): string {
  if (isUuidField(field)) return lookupName(field, value, lookups);
  if (isOptionField(field)) return optionLabel(field, value);
  return value;
}

/** Human-readable summary of one condition for the row chip —
 *  e.g. "Priority is High", "Assignee is one of: Alice, Bob". */
export function conditionSummary(
  c: WebhookCondition,
  lookups: LookupMaps,
): string {
  const label = fieldLabel(c.field);
  const values = (Array.isArray(c.value) ? c.value : [c.value]).map((v) =>
    displayValue(c.field, v, lookups),
  );
  if (c.op === "in") {
    return `${label} is one of: ${values.join(", ")}`;
  }
  return `${label} is ${values[0] ?? ""}`;
}

/** "N filters" / "No filters" badge text. */
export function filterSummaryText(
  filter: WebhookFilter | null | undefined,
): string {
  const n = filter?.length ?? 0;
  return n === 0 ? "No filters" : `${n} filter${n === 1 ? "" : "s"}`;
}

/** Builds the PATCH body honoring the backend's tri-state:
 *  - undefined (untouched) → key omitted entirely
 *  - null (explicit clear) → {filter: null}
 *  - array → {filter: [...]} (replace; backend validates)
 *  JSON.stringify drops the undefined key but preserves the null, so
 *  the three states survive serialization. */
export function buildFilterPatch(
  filter: WebhookFilter | null | undefined,
): WebhookUpdateBody {
  if (filter === undefined) return {};
  return { filter };
}
