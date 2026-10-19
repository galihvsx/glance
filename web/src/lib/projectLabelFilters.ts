// projectLabelFilters: URL-backed label filter for the Projects page
// (C76T1), mirroring the lib/filters.ts anatomy — the entire filter state
// lives in the URL query string so it round-trips (reload keeps it, a
// pasted URL reproduces it).
//
// URL QUERY PARAM SCHEMA
// ----------------------
//   label   repeatable project-label UUID, e.g. ?label=<id>&label=<id>
//           (comma-separated values are accepted too, matching the
//           backend ?label= discipline)
//
// OR semantics across the selected ids. Malformed values in a pasted URL
// are dropped (never throw, never 400 the list — the backend 400s
// malformed/unknown UUIDs, so only well-formed UUIDs are sent).

import { useCallback, useMemo } from "react";
import { useSearchParams } from "react-router-dom";

export const PROJECT_LABEL_FILTER_PARAM = "label";

const UUID_RE =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/** True for a well-formed UUID — the only values the backend accepts. */
export function isProjectLabelId(v: string): boolean {
  return UUID_RE.test(v);
}

/**
 * Parse the URL query string into the selected label ids. Unknown /
 * malformed values are dropped (never throw on a pasted URL). Order is
 * first-seen, duplicates removed.
 */
export function parseProjectLabelIds(search: URLSearchParams): string[] {
  const seen = new Set<string>();
  for (const v of search.getAll(PROJECT_LABEL_FILTER_PARAM)) {
    for (const part of v.split(",")) {
      const s = part.trim();
      if (s && isProjectLabelId(s) && !seen.has(s)) seen.add(s);
    }
  }
  return [...seen];
}

/** Serialize the selected ids back to URL query params (inverse of
 *  parseProjectLabelIds). */
export function serializeProjectLabelIds(ids: string[]): URLSearchParams {
  const p = new URLSearchParams();
  for (const id of ids) {
    if (isProjectLabelId(id)) p.append(PROJECT_LABEL_FILTER_PARAM, id);
  }
  return p;
}

/** Build the projects-list API query string from the selected ids
 *  (repeatable ?label=, OR semantics). */
export function projectLabelApiParams(ids: string[]): string {
  return serializeProjectLabelIds(ids).toString();
}

/**
 * URL-backed label filter state for the Projects page. Reads from the
 * location search string and writes back with replace:true (filter tweaks
 * shouldn't spam history). Non-label params are preserved on write and
 * clear.
 */
export function useProjectLabelFilters(): [
  string[],
  (ids: string[]) => void,
  () => void,
] {
  const [searchParams, setSearchParams] = useSearchParams();

  const ids = useMemo(() => parseProjectLabelIds(searchParams), [searchParams]);

  const setIds = useCallback(
    (next: string[]) => {
      const keep = new URLSearchParams();
      for (const [k, v] of searchParams) {
        if (k !== PROJECT_LABEL_FILTER_PARAM) keep.append(k, v);
      }
      for (const id of next) {
        if (isProjectLabelId(id)) keep.append(PROJECT_LABEL_FILTER_PARAM, id);
      }
      setSearchParams(keep, { replace: true });
    },
    [searchParams, setSearchParams],
  );

  const clearIds = useCallback(() => {
    const keep = new URLSearchParams();
    for (const [k, v] of searchParams) {
      if (k !== PROJECT_LABEL_FILTER_PARAM) keep.append(k, v);
    }
    setSearchParams(keep, { replace: true });
  }, [searchParams, setSearchParams]);

  return [ids, setIds, clearIds];
}
