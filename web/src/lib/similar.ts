// Duplicate detection on issue create (C8T7): "possible duplicates"
// suggestions as the title is typed. Thin client for
// GET .../issues/similar?q= — the ranking, threshold (> 0.3) and
// working-set scope all live server-side; this module owns the request
// shape, the debounce timing, and the minimum-length gate (titles under
// 3 chars carry too few trigrams to be meaningful, and the frontend never
// sends them).

import { api } from "./api";

export interface SimilarIssue {
  id: string;
  display_id: string;
  name: string;
  state: string;
  similarity: number;
}

/** Debounce before hitting the server while the title is being typed. */
export const SIMILAR_DEBOUNCE_MS = 400;

/** Minimum title length before suggestions are fetched (backend rejects
 *  blank q with 400; 1-2 chars are trigram noise anyway). */
export const SIMILAR_MIN_TITLE_LEN = 3;

// similarIssuesPath builds the C8T7 endpoint path for a project.
export function similarIssuesPath(
  slug: string,
  identifier: string,
): string {
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/issues/similar`;
}

// shouldFetchSimilar gates the suggestion query: blank or sub-trigram
// titles return no suggestions and make no request.
export function shouldFetchSimilar(title: string): boolean {
  return title.trim().length >= SIMILAR_MIN_TITLE_LEN;
}

// fetchSimilarIssues queries the top-5 duplicate candidates for a title.
// Callers gate with shouldFetchSimilar first; a blank q is a 400.
export function fetchSimilarIssues(
  slug: string,
  identifier: string,
  q: string,
): Promise<SimilarIssue[]> {
  const p = new URLSearchParams({ q: q.trim() });
  return api.get<SimilarIssue[]>(`${similarIssuesPath(slug, identifier)}?${p}`);
}
