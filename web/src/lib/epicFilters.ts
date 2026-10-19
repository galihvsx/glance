import { useCallback, useMemo } from "react";
import { useSearchParams } from "react-router-dom";

/**
 * Epic list filter state (C74T1), mirroring the lib/initiativeFilters.ts
 * anatomy: the entire filter state lives in the URL query string so it
 * round-trips — reload keeps the filters, and a pasted URL reproduces
 * them exactly.
 *
 * URL QUERY PARAM SCHEMA
 * ----------------------
 *   status        repeatable, values active|archived, e.g.
 *                 ?status=active&status=archived ("" = archived hidden,
 *                 the backend default)
 *   q             free text, trimmed ("" = off)
 *   created_by    member UUID ("" = any)
 *   created_from  YYYY-MM-DD, inclusive
 *   created_to    YYYY-MM-DD, inclusive of the whole day
 *   initiative_id linked-initiative UUID ("" = any)
 *   sort          newest | oldest | name | most_issues ("" = newest)
 *
 * The epics list API (C74T0) wants sort/order as separate params, so
 * toEpicApiParams() expands the UI sort option:
 *   newest      -> sort=created_at&order=desc
 *   oldest      -> sort=created_at&order=asc
 *   name        -> sort=name&order=asc
 *   most_issues -> sort=issue_count&order=desc
 */

export const EPIC_STATUSES = ["active", "archived"] as const;
export type EpicStatus = (typeof EPIC_STATUSES)[number];

export const EPIC_SORT_OPTIONS = [
  "newest",
  "oldest",
  "name",
  "most_issues",
] as const;
export type EpicSortOption = (typeof EPIC_SORT_OPTIONS)[number];

export interface EpicFilters {
  statuses: EpicStatus[];
  q: string;
  createdBy: string;
  createdAfter: string;
  createdBefore: string;
  initiativeId: string;
  sort: EpicSortOption;
}

export const EMPTY_EPIC_FILTERS: EpicFilters = {
  statuses: [],
  q: "",
  createdBy: "",
  createdAfter: "",
  createdBefore: "",
  initiativeId: "",
  sort: "newest",
};

export const EPIC_SORT_LABELS: Record<EpicSortOption, string> = {
  newest: "Newest",
  oldest: "Oldest",
  name: "Name A–Z",
  most_issues: "Most issues",
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

function validStatuses(v: string[]): EpicStatus[] {
  const seen = new Set<EpicStatus>();
  for (const s of v) {
    if ((s === "active" || s === "archived") && !seen.has(s)) {
      seen.add(s);
    }
  }
  // Canonical vocabulary order so the URL is stable regardless of click order.
  return EPIC_STATUSES.filter((s) => seen.has(s));
}

function validSort(v: string | null): EpicSortOption {
  return EPIC_SORT_OPTIONS.includes(v as EpicSortOption)
    ? (v as EpicSortOption)
    : "newest";
}

/** Parse the URL query string into filter state. Unknown/invalid values
 *  are dropped (never throw on a pasted URL). */
export function parseEpicFilters(search: URLSearchParams): EpicFilters {
  return {
    statuses: validStatuses(search.getAll("status")),
    q: (search.get("q") ?? "").trim(),
    createdBy: search.get("created_by") ?? "",
    createdAfter: validDate(search.get("created_from")),
    createdBefore: validDate(search.get("created_to")),
    initiativeId: search.get("initiative_id") ?? "",
    sort: validSort(search.get("sort")),
  };
}

/** Serialize filter state back to URL query params (inverse of
 *  parseEpicFilters). */
export function serializeEpicFilters(f: EpicFilters): URLSearchParams {
  const p = new URLSearchParams();
  for (const s of validStatuses(f.statuses)) p.append("status", s);
  if (f.q.trim()) p.set("q", f.q.trim());
  if (f.createdBy) p.set("created_by", f.createdBy);
  if (f.createdAfter) p.set("created_from", f.createdAfter);
  if (f.createdBefore) p.set("created_to", f.createdBefore);
  if (f.initiativeId) p.set("initiative_id", f.initiativeId);
  if (f.sort !== "newest") p.set("sort", f.sort);
  return p;
}

const SORT_API_MAP: Record<EpicSortOption, { sort: string; order: string }> = {
  newest: { sort: "created_at", order: "desc" },
  oldest: { sort: "created_at", order: "asc" },
  name: { sort: "name", order: "asc" },
  most_issues: { sort: "issue_count", order: "desc" },
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
export function toEpicApiParams(f: EpicFilters): URLSearchParams {
  const p = new URLSearchParams();
  for (const s of validStatuses(f.statuses)) p.append("status", s);
  if (f.q.trim()) p.set("q", f.q.trim());
  if (f.createdBy) p.set("created_by", f.createdBy);
  if (f.createdAfter) p.set("created_from", toRfc3339Start(f.createdAfter));
  if (f.createdBefore) p.set("created_to", toRfc3339EndNextDay(f.createdBefore));
  if (f.initiativeId) p.set("initiative_id", f.initiativeId);
  const { sort, order } = SORT_API_MAP[f.sort];
  p.set("sort", sort);
  p.set("order", order);
  return p;
}

/** Number of active filter dimensions (for the filter button badge). */
export function epicActiveFilterCount(f: EpicFilters): number {
  let n = 0;
  if (f.statuses.length > 0) n++;
  if (f.q.trim()) n++;
  if (f.createdBy) n++;
  if (f.createdAfter || f.createdBefore) n++;
  if (f.initiativeId) n++;
  if (f.sort !== "newest") n++;
  return n;
}

/**
 * URL-backed filter state. Reads from the location search string and
 * writes back with replace:true (filter tweaks shouldn't spam history).
 */
export function useEpicFilters(): [
  EpicFilters,
  (patch: Partial<EpicFilters>) => void,
  () => void,
] {
  const [searchParams, setSearchParams] = useSearchParams();

  const filters = useMemo(() => parseEpicFilters(searchParams), [searchParams]);

  const setFilters = useCallback(
    (patch: Partial<EpicFilters>) => {
      const next = serializeEpicFilters({
        ...parseEpicFilters(searchParams),
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
      if (!isEpicFilterParam(k)) keep.set(k, v);
    }
    setSearchParams(keep, { replace: true });
  }, [searchParams, setSearchParams]);

  return [filters, setFilters, clearFilters];
}

const EPIC_FILTER_PARAM_NAMES = new Set([
  "status",
  "q",
  "created_by",
  "created_from",
  "created_to",
  "initiative_id",
  "sort",
]);

/** True for the URL params that belong to the epic filter schema. */
export function isEpicFilterParam(k: string): boolean {
  return EPIC_FILTER_PARAM_NAMES.has(k);
}
