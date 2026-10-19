import { useCallback, useMemo } from "react";
import { useSearchParams } from "react-router-dom";

/**
 * Initiative list filter state (C73T1), mirroring the lib/filters.ts
 * anatomy: the entire filter state lives in the URL query string so it
 * round-trips — reload keeps the filters, and a pasted URL reproduces
 * them exactly.
 *
 * URL QUERY PARAM SCHEMA
 * ----------------------
 *   status        repeatable, values active|completed|archived, e.g.
 *                 ?status=active&status=completed ("" = archived hidden,
 *                 the backend default)
 *   q             free text, trimmed ("" = off)
 *   created_by    member UUID ("" = any)
 *   created_from  YYYY-MM-DD, inclusive
 *   created_to    YYYY-MM-DD, inclusive of the whole day
 *   project_id    member-project UUID ("" = any)
 *   sort          newest | oldest | name | most_projects ("" = newest)
 *
 * The initiatives list API (C73T0) wants sort/order as separate params,
 * so toInitiativeApiParams() expands the UI sort option:
 *   newest        -> sort=created_at&order=desc
 *   oldest        -> sort=created_at&order=asc
 *   name          -> sort=name&order=asc
 *   most_projects -> sort=project_count&order=desc
 * Omitting both (the default) lets the backend default to created_at desc,
 * which is identical to "newest".
 */

export const INITIATIVE_STATUSES = ["active", "completed", "archived"] as const;
export type InitiativeStatus = (typeof INITIATIVE_STATUSES)[number];

export const INITIATIVE_SORT_OPTIONS = [
  "newest",
  "oldest",
  "name",
  "most_projects",
] as const;
export type InitiativeSortOption = (typeof INITIATIVE_SORT_OPTIONS)[number];

export interface InitiativeFilters {
  statuses: InitiativeStatus[];
  q: string;
  createdBy: string;
  createdAfter: string;
  createdBefore: string;
  projectId: string;
  sort: InitiativeSortOption;
}

export const EMPTY_INITIATIVE_FILTERS: InitiativeFilters = {
  statuses: [],
  q: "",
  createdBy: "",
  createdAfter: "",
  createdBefore: "",
  projectId: "",
  sort: "newest",
};

export const INITIATIVE_SORT_LABELS: Record<InitiativeSortOption, string> = {
  newest: "Newest",
  oldest: "Oldest",
  name: "Name A–Z",
  most_projects: "Most projects",
};

const DATE_RE = /^\d{4}-\d{2}-\d{2}$/;

function validDate(v: string | null): string {
  if (!v || !DATE_RE.test(v)) return "";
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

function validStatuses(v: string[]): InitiativeStatus[] {
  const seen = new Set<InitiativeStatus>();
  for (const s of v) {
    if (
      (s === "active" || s === "completed" || s === "archived") &&
      !seen.has(s)
    ) {
      seen.add(s);
    }
  }
  // Canonical vocabulary order so the URL is stable regardless of click order.
  return INITIATIVE_STATUSES.filter((s) => seen.has(s));
}

function validSort(v: string | null): InitiativeSortOption {
  return INITIATIVE_SORT_OPTIONS.includes(v as InitiativeSortOption)
    ? (v as InitiativeSortOption)
    : "newest";
}

/** Parse the URL query string into filter state. Unknown/invalid values
 *  are dropped (never throw on a pasted URL). */
export function parseInitiativeFilters(
  search: URLSearchParams,
): InitiativeFilters {
  return {
    statuses: validStatuses(search.getAll("status")),
    q: (search.get("q") ?? "").trim(),
    createdBy: search.get("created_by") ?? "",
    createdAfter: validDate(search.get("created_from")),
    createdBefore: validDate(search.get("created_to")),
    projectId: search.get("project_id") ?? "",
    sort: validSort(search.get("sort")),
  };
}

/** Serialize filter state back to URL query params (inverse of
 *  parseInitiativeFilters). */
export function serializeInitiativeFilters(
  f: InitiativeFilters,
): URLSearchParams {
  const p = new URLSearchParams();
  for (const s of validStatuses(f.statuses)) p.append("status", s);
  if (f.q.trim()) p.set("q", f.q.trim());
  if (f.createdBy) p.set("created_by", f.createdBy);
  if (f.createdAfter) p.set("created_from", f.createdAfter);
  if (f.createdBefore) p.set("created_to", f.createdBefore);
  if (f.projectId) p.set("project_id", f.projectId);
  if (f.sort !== "newest") p.set("sort", f.sort);
  return p;
}

const SORT_API_MAP: Record<InitiativeSortOption, { sort: string; order: string }> = {
  newest: { sort: "created_at", order: "desc" },
  oldest: { sort: "created_at", order: "asc" },
  name: { sort: "name", order: "asc" },
  most_projects: { sort: "project_count", order: "desc" },
};

function toRfc3339Start(day: string): string {
  return `${day}T00:00:00Z`;
}

function toRfc3339EndNextDay(day: string): string {
  const d = new Date(`${day}T00:00:00Z`);
  d.setUTCDate(d.getUTCDate() + 1);
  return d.toISOString().replace(/\.\d{3}Z$/, "Z");
}

/** Build the list-API query params from filter state. Dates go out as
 *  RFC3339: created_from is inclusive start-of-day, created_to the start
 *  of the next day so the named day is fully included. */
export function toInitiativeApiParams(
  f: InitiativeFilters,
): URLSearchParams {
  const p = new URLSearchParams();
  for (const s of validStatuses(f.statuses)) p.append("status", s);
  if (f.q.trim()) p.set("q", f.q.trim());
  if (f.createdBy) p.set("created_by", f.createdBy);
  if (f.createdAfter) p.set("created_from", toRfc3339Start(f.createdAfter));
  if (f.createdBefore) p.set("created_to", toRfc3339EndNextDay(f.createdBefore));
  if (f.projectId) p.set("project_id", f.projectId);
  const { sort, order } = SORT_API_MAP[f.sort];
  p.set("sort", sort);
  p.set("order", order);
  return p;
}

/** Number of active filter dimensions (for the filter button badge). */
export function initiativeActiveFilterCount(f: InitiativeFilters): number {
  let n = 0;
  if (f.statuses.length > 0) n++;
  if (f.q.trim()) n++;
  if (f.createdBy) n++;
  if (f.createdAfter || f.createdBefore) n++;
  if (f.projectId) n++;
  if (f.sort !== "newest") n++;
  return n;
}

/**
 * URL-backed filter state. Reads from the location search string and
 * writes back with replace:true (filter tweaks shouldn't spam history).
 */
export function useInitiativeFilters(): [
  InitiativeFilters,
  (patch: Partial<InitiativeFilters>) => void,
  () => void,
] {
  const [searchParams, setSearchParams] = useSearchParams();

  const filters = useMemo(
    () => parseInitiativeFilters(searchParams),
    [searchParams],
  );

  const setFilters = useCallback(
    (patch: Partial<InitiativeFilters>) => {
      const next = serializeInitiativeFilters({
        ...parseInitiativeFilters(searchParams),
        ...patch,
      });
      setSearchParams(next, { replace: true });
    },
    [searchParams, setSearchParams],
  );

  const clearFilters = useCallback(() => {
    const keep = new URLSearchParams();
    // Preserve non-filter params when clearing.
    for (const [k, v] of searchParams) {
      if (!isInitiativeFilterParam(k)) keep.set(k, v);
    }
    setSearchParams(keep, { replace: true });
  }, [searchParams, setSearchParams]);

  return [filters, setFilters, clearFilters];
}

const INITIATIVE_FILTER_PARAM_NAMES = new Set([
  "status",
  "q",
  "created_by",
  "created_from",
  "created_to",
  "project_id",
  "sort",
]);

/** True for the URL params that belong to the initiative filter schema. */
export function isInitiativeFilterParam(k: string): boolean {
  return INITIATIVE_FILTER_PARAM_NAMES.has(k);
}
