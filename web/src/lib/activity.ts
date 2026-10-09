// Activity feed formatting (C5T7): pure helpers that turn the raw
// /activity rows into human-readable feed text. The backend ships
// issue_activities values as raw JSONB-as-text; everything displayable
// is derived here, including the honest "only field changes with audit
// rows appear" caveat (stated in the page footnote, not the data).

import { PRIORITY_LABELS } from "./types";

export interface ActivityEntry {
  at: string;
  actor: string;
  issue_uuid: string;
  issue_identifier: string;
  field: string;
  old: string | null;
  new: string | null;
}

const FIELD_LABELS: Record<string, string> = {
  name: "name",
  description: "description",
  priority: "priority",
  state_id: "state",
  parent_id: "parent",
  sort_order: "sort order",
  start_date: "start date",
  target_date: "target date",
  estimate_point_id: "estimate",
  is_draft: "draft",
};

/** Human label for an audit field name. Unknown fields pass through
 *  unchanged rather than being hidden. */
export function fieldLabel(field: string): string {
  return FIELD_LABELS[field] ?? field;
}

/** Unwrap one raw JSONB-as-text value from the API. SQL NULL, JSON
 *  null, and missing values all become null ("unset"); JSON strings
 *  lose their quotes; everything else passes through as-is. */
export function scalarText(raw: string | null | undefined): string | null {
  if (raw === null || raw === undefined) return null;
  const t = raw.trim();
  if (t === "null" || t === "") return null;
  if (t.length >= 2 && t.startsWith('"') && t.endsWith('"')) {
    try {
      return JSON.parse(t) as string;
    } catch {
      return t.slice(1, -1);
    }
  }
  return t;
}

/** Display text for one side of a change. Returns null only when the
 *  value is unset (cleared on the new side, never-set on the old side)
 *  or not meaningfully inline-renderable (description JSON docs,
 *  _created snapshots). */
export function formatValue(
  field: string,
  raw: string | null | undefined,
  stateById: Map<string, string>,
): string | null {
  const v = scalarText(raw);
  if (v === null) return null;
  switch (field) {
    case "state_id":
      return stateById.get(v) ?? v.slice(0, 8);
    case "priority": {
      const n = parseInt(v, 10);
      return Number.isNaN(n) ? v : (PRIORITY_LABELS[n] ?? v);
    }
    case "description":
      return null; // a JSON doc — the row says "edited the description"
    case "parent_id":
      return v.slice(0, 8);
    case "is_draft":
      return v === "true" ? "draft" : "not a draft";
    case "_created":
    case "_deleted":
      return null; // snapshot payloads, not inline values
    default:
      return v;
  }
}

export type ChangeKind = "created" | "deleted" | "edited" | "set" | "cleared" | "changed";

/** Classify + render a feed row into sentence parts. `oldText` /
 *  `newText` are null for created/deleted/edited rows. */
export function describeChange(
  e: ActivityEntry,
  stateById: Map<string, string>,
): { kind: ChangeKind; label: string; oldText: string | null; newText: string | null } {
  if (e.field === "_created") {
    return { kind: "created", label: "", oldText: null, newText: null };
  }
  if (e.field === "_deleted") {
    return { kind: "deleted", label: "", oldText: null, newText: null };
  }
  if (e.field === "description") {
    return { kind: "edited", label: "the description", oldText: null, newText: null };
  }
  const label = fieldLabel(e.field);
  const oldText = formatValue(e.field, e.old, stateById);
  const newText = formatValue(e.field, e.new, stateById);
  if (oldText === null && newText !== null) {
    return { kind: "set", label, oldText, newText };
  }
  if (newText === null) {
    return { kind: "cleared", label, oldText, newText };
  }
  return { kind: "changed", label, oldText, newText };
}

/** Case-insensitive feed filter: matches issue identifier, actor, or
 *  the field label. Empty query matches everything. */
export function matchesActivityFilter(
  e: ActivityEntry,
  query: string,
): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  return (
    e.issue_identifier.toLowerCase().includes(q) ||
    e.actor.toLowerCase().includes(q) ||
    fieldLabel(e.field).toLowerCase().includes(q)
  );
}
