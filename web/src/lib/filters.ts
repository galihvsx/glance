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
 *   parent          repeatable and comma-separated issue UUIDs (OR-matched);
 *                   "none" matches top-level issues (parent_id IS NULL),
 *                   e.g. ?parent=<uuid>,none. Malformed values are dropped.
 *   epic            repeatable and comma-separated epic UUIDs (OR-matched,
 *                   via the epic_issues junction); "none" matches issues in
 *                   no epic. Malformed values are dropped.
 *   cycles          repeatable and comma-separated cycle UUIDs (OR-matched,
 *                   via the cycle_issues junction); "none" matches issues in
 *                   no cycle. Malformed values are dropped. (C116T1: the
 *                   visual-filter plural — never the singular cycle=
 *                   scope param.)
 *   modules         repeatable and comma-separated module UUIDs (OR-matched,
 *                   via the module_issues junction); "none" matches issues in
 *                   no module. Malformed values are dropped. (C116T1: the
 *                   visual-filter plural — never the singular module=
 *                   scope param.)
 *   releases        repeatable and comma-separated release UUIDs (OR-matched,
 *                   via the direct release_id column); "none" matches issues
 *                   in no release. Malformed values are dropped. (C117T1:
 *                   the visual-filter plural — never a singular release=
 *                   scope param, which does not exist on the list API.)
 *   created_by      repeatable and comma-separated author user UUIDs
 *                   (OR-matched, via the direct created_by column). NO
 *                   "none" value: created_by is NOT NULL, so there are
 *                   no authorless issues and a none row would be a lie.
 *                   Malformed values (including "none") are dropped.
 *                   (C118T1.)
 *   state_group     repeatable and comma-separated state-group slugs
 *                   (OR-matched, via the issue's state group). Closed
 *                   vocabulary, Plane's five only: backlog, unstarted,
 *                   started, completed, cancelled. NO "none" value:
 *                   every issue has a state and every state has a
 *                   group, so there are no groupless issues. Matching
 *                   is case-insensitive (normalized to lowercase);
 *                   unknown values (including "triage" and "none")
 *                   are dropped so a pasted URL can never 400 the
 *                   list. (C119T1.)
 *   created_after   YYYY-MM-DD, inclusive
 *   created_before  YYYY-MM-DD, inclusive of the whole day
 *   updated_after   YYYY-MM-DD, inclusive
 *   updated_before  YYYY-MM-DD, inclusive of the whole day
 *   due_after       YYYY-MM-DD, inclusive (matches target_date)
 *   due_before      YYYY-MM-DD, inclusive of the whole day
 *   due_date_empty  "1" = only issues with no due date (target_date IS NULL).
 *                   C80T1: mutually exclusive with due_after/due_before —
 *                   the filter panel clears the range when the chip is on
 *                   and clears the chip when a range is picked, so the
 *                   list API's 400 (range + empty combined) is unreachable
 *                   from the UI.
 *   subscribed      "1" = only issues the current user subscribed to
 *   archived        "1" = include archived issues (default: hidden)
 *   pql             Plane Query Language predicate (C91T1). Mutually
 *                   exclusive with every visual dimension above: when
 *                   non-blank, the list request sends ONLY pql (plus
 *                   pagination/ordering) — see lib/pql.ts. The visual
 *                   selection is parked in memory while PQL mode is on
 *                   and restored when switching back (Issues page).
 *
 * The list API takes the same params except dates, which it wants as
 * RFC3339 (existing `updated_after` convention). toApiParams() converts:
 * *_after  -> YYYY-MM-DDT00:00:00Z (inclusive start of day)
 * *_before -> (day+1)T00:00:00Z   (exclusive start of next day, so the
 *             named day is fully included)
 *
 * NOTE: toApiParams() serializes whatever the filter state carries,
 * including pql when set — callers that must honor PQL exclusivity
 * (the Issues list request) branch on a non-blank pql BEFORE calling
 * it and use buildPqlRequestParams() instead (lib/pql.ts).
 *
 * AI text-to-PQL generation is deliberately out of scope (C91 plan:
 * deterministic language only; the AI on-ramp is galih's call).
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
  /** Parent issue UUIDs (OR-matched); "none" = top-level issues. */
  parents: string[];
  /** Epic UUIDs via the epic_issues junction (OR-matched); "none" = no epic. */
  epics: string[];
  /** Cycle UUIDs via the cycle_issues junction (OR-matched); "none" = no cycle. */
  cycles: string[];
  /** Module UUIDs via the module_issues junction (OR-matched); "none" = no module. */
  modules: string[];
  /** Release UUIDs via the direct release_id column (OR-matched); "none" = no release. */
  releases: string[];
  /** Author user UUIDs via the direct created_by column (OR-matched).
   *  NO "none" value: created_by is NOT NULL, so every issue has
   *  exactly one author. */
  createdBy: string[];
  /** State-group slugs via the issue's state group (OR-matched).
   *  Closed vocabulary, Plane's five only: backlog, unstarted,
   *  started, completed, cancelled. NO "none" value: every issue
   *  has a state and every state has a group. (C119T1.) */
  stateGroups: string[];
  createdAfter: string;
  createdBefore: string;
  updatedAfter: string;
  updatedBefore: string;
  dueAfter: string;
  dueBefore: string;
  /** "No due date" chip (C80T1): target_date IS NULL. Mutually exclusive
   *  with dueAfter/dueBefore — see the URL schema comment above. */
  dueDateEmpty: boolean;
  subscribed: boolean;
  /** Include archived issues; default false hides them. */
  archived: boolean;
  /**
   * Applied PQL query (C91T1), carried as the URL `pql` param and in
   * saved-view payloads. Optional so views saved before C91 (which
   * have no such key) keep loading unchanged — absent means "".
   * Non-blank means PQL mode: the list request sends only this.
   */
  pql?: string;
}

export const EMPTY_FILTERS: IssueFilters = {
  q: "",
  state: "",
  priorities: [],
  labels: [],
  assignees: [],
  estimates: [],
  parents: [],
  epics: [],
  cycles: [],
  modules: [],
  releases: [],
  createdBy: [],
  stateGroups: [],
  createdAfter: "",
  createdBefore: "",
  updatedAfter: "",
  updatedBefore: "",
  dueAfter: "",
  dueBefore: "",
  dueDateEmpty: false,
  subscribed: false,
  archived: false,
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

const UUID_RE =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/** Split the parent=/epic=/cycles=/modules=/releases=/created_by= params:
 *  repeatable AND comma-separated (the backend accepts both forms).
 *  Only the "none" sentinel and well-formed UUIDs survive — garbage is
 *  dropped so a pasted URL can never 400 the list (the list API 400s
 *  malformed UUIDs). Pass allowNone=false for created_by: the column
 *  is NOT NULL, so "none" is meaningless there and is dropped too. */
function splitRepeatIds(values: string[], allowNone = true): string[] {
  const out: string[] = [];
  for (const v of values) {
    for (const part of v.split(",")) {
      const s = part.trim();
      if (!s) continue;
      if ((allowNone && s === "none") || UUID_RE.test(s)) out.push(s);
    }
  }
  return out;
}

/** Plane's five filterable state groups (C119T1; the backend's
 *  webhookFilterStateGroups vocabulary, NOT the states CHECK
 *  constraint — "triage" is deliberately not filterable). */
const STATE_GROUP_VOCABULARY = new Set([
  "backlog",
  "unstarted",
  "started",
  "completed",
  "cancelled",
]);

/** Split the state_group= param: repeatable AND comma-separated
 *  (the backend accepts both forms). Matching is case-insensitive
 *  (normalized to lowercase); anything outside the five-group
 *  vocabulary — including "triage" and "none" — is dropped so a
 *  pasted URL can never 400 the list. */
function splitStateGroups(values: string[]): string[] {
  const out: string[] = [];
  for (const v of values) {
    for (const part of v.split(",")) {
      const s = part.trim().toLowerCase();
      if (s && STATE_GROUP_VOCABULARY.has(s)) out.push(s);
    }
  }
  return out;
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
    parents: splitRepeatIds(search.getAll("parent")),
    epics: splitRepeatIds(search.getAll("epic")),
    cycles: splitRepeatIds(search.getAll("cycles")),
    modules: splitRepeatIds(search.getAll("modules")),
    releases: splitRepeatIds(search.getAll("releases")),
    createdBy: splitRepeatIds(search.getAll("created_by"), false),
    stateGroups: splitStateGroups(search.getAll("state_group")),
    createdAfter: validDate(search.get("created_after")),
    createdBefore: validDate(search.get("created_before")),
    updatedAfter: validDate(search.get("updated_after")),
    updatedBefore: validDate(search.get("updated_before")),
    dueAfter: validDate(search.get("due_after")),
    dueBefore: validDate(search.get("due_before")),
    // The backend accepts "1"/"true"; we always write "1".
    dueDateEmpty:
      search.get("due_date_empty") === "1" ||
      search.get("due_date_empty") === "true",
    subscribed: search.get("subscribed") === "1",
    archived: search.get("archived") === "1",
    // Trimmed; a blank pql is absent (the backend treats it as such),
    // so "pql=" in a pasted URL never activates PQL mode. Absent means
    // the key is undefined — parse/serialize round-trips stay exact
    // against pre-C91 fixtures that carry no pql key at all.
    pql: (search.get("pql") ?? "").trim() || undefined,
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
  if (f.parents.length > 0) p.set("parent", f.parents.join(","));
  if (f.epics.length > 0) p.set("epic", f.epics.join(","));
  if (f.cycles.length > 0) p.set("cycles", f.cycles.join(","));
  if (f.modules.length > 0) p.set("modules", f.modules.join(","));
  if (f.releases.length > 0) p.set("releases", f.releases.join(","));
  if (f.createdBy.length > 0) p.set("created_by", f.createdBy.join(","));
  if (f.stateGroups.length > 0)
    p.set("state_group", f.stateGroups.join(","));
  if (f.createdAfter) p.set("created_after", f.createdAfter);
  if (f.createdBefore) p.set("created_before", f.createdBefore);
  if (f.updatedAfter) p.set("updated_after", f.updatedAfter);
  if (f.updatedBefore) p.set("updated_before", f.updatedBefore);
  if (f.dueAfter) p.set("due_after", f.dueAfter);
  if (f.dueBefore) p.set("due_before", f.dueBefore);
  if (f.dueDateEmpty) p.set("due_date_empty", "1");
  if (f.subscribed) p.set("subscribed", "1");
  if (f.archived) p.set("archived", "1");
  // PQL rides along as its own param; blank means absent. Callers
  // that must honor exclusivity branch on it before sending (see the
  // NOTE on toApiParams and lib/pql.ts buildPqlRequestParams).
  if (f.pql?.trim()) p.set("pql", f.pql.trim());
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
  if (f.parents.length > 0) n++;
  if (f.epics.length > 0) n++;
  if (f.cycles.length > 0) n++;
  if (f.modules.length > 0) n++;
  if (f.releases.length > 0) n++;
  if (f.createdBy.length > 0) n++;
  if (f.stateGroups.length > 0) n++;
  if (f.createdAfter || f.createdBefore) n++;
  if (f.updatedAfter || f.updatedBefore) n++;
  if (f.dueAfter || f.dueBefore || f.dueDateEmpty) n++;
  if (f.subscribed) n++;
  if (f.archived) n++;
  // A PQL query is one active filter dimension, whatever it contains.
  if (f.pql?.trim()) n++;
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
  "parent",
  "epic",
  "cycles",
  "modules",
  "releases",
  "created_by",
  "state_group",
  "created_after",
  "created_before",
  "updated_after",
  "updated_before",
  "due_after",
  "due_before",
  "due_date_empty",
  "subscribed",
  "archived",
  "pql",
]);

/** True for the URL params that belong to the filter schema (C2T6 uses this
 *  to preserve non-filter params like ?peek= when applying a saved view). */
export function isFilterParam(k: string): boolean {
  return FILTER_PARAM_NAMES.has(k);
}
