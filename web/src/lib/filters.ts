import { useCallback, useMemo } from "react";
import { useSearchParams } from "react-router-dom";

/**
 * Shared issue-filter state (C2T5). One module drives the list view, the
 * board, and the spreadsheet — and C2T6 (saved views) persists this exact
 * shape.
 *
 * URL QUERY PARAM SCHEMA
 * ----------------------
 * The entire filter state lives in the URL query string, so it round-trips:
 * reload keeps the filters, and a pasted URL reproduces them exactly.
 *
 *   q               free text, trimmed ("" = off)
 *   state           state UUID, single ("" = all states)
 *   priority        comma-separated ints 0-4, e.g. ?priority=1,3 ("" = all)
 *   label           comma-separated label UUIDs ("" = all)
 *   assignee        comma-separated user UUIDs; the value "none" matches
 *                   issues with no assignees, e.g. ?assignee=none or
 *                   ?assignee=<uuid>,none
 *   estimate        comma-separated estimate_point UUIDs; "none" matches
 *                   issues with no estimate point
 *   created_after   YYYY-MM-DD, inclusive
 *   created_before  YYYY-MM-DD, inclusive of the whole day
 *   updated_after   YYYY-MM-DD, inclusive
 *   updated_before  YYYY-MM-DD, inclusive of the whole day
 *   due_after       YYYY-MM-DD, inclusive (matches target_date)
 *   due_before      YYYY-MM-DD, inclusive of the whole day
 *   subscribed      "1" = only issues the current user subscribed to
 *
 * The list API takes the same params except dates, which it wants as
 * RFC3339 (existing `updated_after` convention). toApiParams() converts:
 * *_after  -> YYYY-MM-DDT00:00:00Z (inclusive start of day)
 * *_before -> (day+1)T00:00:00Z   (exclusive start of next day, so the
 *             named day is fully included)
 *
 * AI / PQL code mode is deliberately out of scope (lean thesis).
 */

export interface IssueFilters {
  q: string;
  state: string;
  priorities: number[];
  labels: string[];
  /** User UUIDs; "none" = unassigned. */
  assignees: string[];
  /** Estimate-point UUIDs; "none" = no estimate. */
  estimates: string[];
  createdAfter: string;
  createdBefore: string;
  updatedAfter: string;
  updatedBefore: string;
  dueAfter: string;
  dueBefore: string;
  subscribed: boolean;
}

export const EMPTY_FILTERS: IssueFilters = {
  q: "",
  state: "",
  priorities: [],
  labels: [],
  assignees: [],
  estimates: [],
  createdAfter: "",
  createdBefore: "",
  updatedAfter: "",
  updatedBefore: "",
  dueAfter: "",
  dueBefore: "",
  subscribed: false,
};

const DATE_RE = /^\d{4}-\d{2}-\d{2}$/;

function splitIds(v: string | null): string[] {
  if (!v) return [];
  return v
    .split(",")
    .map((s) => s.trim())
    .filter((s) => s !== "");
}

function splitPriorities(v: string | null): number[] {
  return splitIds(v)
    .map((s) => Number(s))
    .filter((n) => Number.isInteger(n) && n >= 0 && n <= 4);
}

function validDate(v: string | null): string {
  if (!v || !DATE_RE.test(v)) return "";
  // Reject impossible dates ("2026-13-99" passes the regex); toApiParams
  // would throw on these.
  const d = new Date(`${v}T00:00:00Z`);
  if (Number.isNaN(d.getTime())) return "";
  const [y, m, day] = v.split("-").map(Number);
  if (
    d.getUTCFullYear() !== y ||
    d.getUTCMonth() + 1 !== m ||
    d.getUTCDate() !== day
  ) {
    return "";
  }
  return v;
}

/** Parse the URL query string into filter state. Unknown/invalid values
 *  are dropped (never throw on a pasted URL). */
export function parseFilters(search: URLSearchParams): IssueFilters {
  return {
    q: (search.get("q") ?? "").trim(),
    state: search.get("state") ?? "",
    priorities: splitPriorities(search.get("priority")),
    labels: splitIds(search.get("label")),
    assignees: splitIds(search.get("assignee")),
    estimates: splitIds(search.get("estimate")),
    createdAfter: validDate(search.get("created_after")),
    createdBefore: validDate(search.get("created_before")),
    updatedAfter: validDate(search.get("updated_after")),
    updatedBefore: validDate(search.get("updated_before")),
    dueAfter: validDate(search.get("due_after")),
    dueBefore: validDate(search.get("due_before")),
    subscribed: search.get("subscribed") === "1",
  };
}

/** Serialize filter state back to URL query params (inverse of parseFilters). */
export function serializeFilters(f: IssueFilters): URLSearchParams {
  const p = new URLSearchParams();
  if (f.q.trim()) p.set("q", f.q.trim());
  if (f.state) p.set("state", f.state);
  if (f.priorities.length > 0)
    p.set("priority", [...f.priorities].sort((a, b) => a - b).join(","));
  if (f.labels.length > 0) p.set("label", f.labels.join(","));
  if (f.assignees.length > 0) p.set("assignee", f.assignees.join(","));
  if (f.estimates.length > 0) p.set("estimate", f.estimates.join(","));
  if (f.createdAfter) p.set("created_after", f.createdAfter);
  if (f.createdBefore) p.set("created_before", f.createdBefore);
  if (f.updatedAfter) p.set("updated_after", f.updatedAfter);
  if (f.updatedBefore) p.set("updated_before", f.updatedBefore);
  if (f.dueAfter) p.set("due_after", f.dueAfter);
  if (f.dueBefore) p.set("due_before", f.dueBefore);
  if (f.subscribed) p.set("subscribed", "1");
  return p;
}

function toRfc3339Start(day: string): string {
  return `${day}T00:00:00Z`;
}

function toRfc3339EndExclusive(day: string): string {
  const d = new Date(`${day}T00:00:00Z`);
  d.setUTCDate(d.getUTCDate() + 1);
  return d.toISOString().replace(/\.\d{3}Z$/, "Z");
}

/** Build the list-API query params from filter state (dates → RFC3339). */
export function toApiParams(f: IssueFilters): URLSearchParams {
  const p = serializeFilters(f);
  // serializeFilters wrote YYYY-MM-DD; the API wants RFC3339.
  for (const key of [
    "created_after",
    "updated_after",
    "due_after",
  ] as const) {
    const v = p.get(key);
    if (v) p.set(key, toRfc3339Start(v));
  }
  for (const key of [
    "created_before",
    "updated_before",
    "due_before",
  ] as const) {
    const v = p.get(key);
    if (v) p.set(key, toRfc3339EndExclusive(v));
  }
  return p;
}

/** Number of active filter dimensions (for the filter button badge). */
export function activeFilterCount(f: IssueFilters): number {
  let n = 0;
  if (f.q.trim()) n++;
  if (f.state) n++;
  if (f.priorities.length > 0) n++;
  if (f.labels.length > 0) n++;
  if (f.assignees.length > 0) n++;
  if (f.estimates.length > 0) n++;
  if (f.createdAfter || f.createdBefore) n++;
  if (f.updatedAfter || f.updatedBefore) n++;
  if (f.dueAfter || f.dueBefore) n++;
  if (f.subscribed) n++;
  return n;
}

/**
 * URL-backed filter state. Reads from the location search string and
 * writes back with replace:true (filter tweaks shouldn't spam history).
 * Every consumer on the same route shares one state for free.
 */
export function useIssueFilters(): [
  IssueFilters,
  (patch: Partial<IssueFilters>) => void,
  () => void,
] {
  const [searchParams, setSearchParams] = useSearchParams();

  const filters = useMemo(
    () => parseFilters(searchParams),
    [searchParams],
  );

  const setFilters = useCallback(
    (patch: Partial<IssueFilters>) => {
      const next = serializeFilters({ ...parseFilters(searchParams), ...patch });
      setSearchParams(next, { replace: true });
    },
    [searchParams, setSearchParams],
  );

  const clearFilters = useCallback(() => {
    const keep = new URLSearchParams();
    // Preserve non-filter params (e.g. ?peek=) when clearing.
    for (const [k, v] of searchParams) {
      if (!isFilterParam(k)) keep.set(k, v);
    }
    setSearchParams(keep, { replace: true });
  }, [searchParams, setSearchParams]);

  return [filters, setFilters, clearFilters];
}

const FILTER_PARAM_NAMES = new Set([
  "q",
  "state",
  "priority",
  "label",
  "assignee",
  "estimate",
  "created_after",
  "created_before",
  "updated_after",
  "updated_before",
  "due_after",
  "due_before",
  "subscribed",
]);

/** True for the URL params that belong to the filter schema (C2T6 uses this
 *  to preserve non-filter params like ?peek= when applying a saved view). */
export function isFilterParam(k: string): boolean {
  return FILTER_PARAM_NAMES.has(k);
}
