import { useEffect } from "react";

/** One entry in the "recently visited issues" list. */
export interface RecentIssue {
  id: string;
  name: string;
  display_id: string;
  /** Workspace slug — needed to build the detail URL. */
  slug: string;
  /** Project identifier — needed to build the detail URL. */
  identifier: string;
  visited_at: string;
}

const STORAGE_KEY = "glance:recents";
const MAX_RECENTS = 5;

function load(): RecentIssue[] {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return [];
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    return parsed.filter(
      (e): e is RecentIssue =>
        typeof e === "object" &&
        e !== null &&
        typeof (e as RecentIssue).id === "string" &&
        typeof (e as RecentIssue).slug === "string" &&
        typeof (e as RecentIssue).identifier === "string",
    );
  } catch {
    return [];
  }
}

/** Read the recents list (most recent first). Pure localStorage. */
export function readRecentIssues(): RecentIssue[] {
  return load();
}

/**
 * Record a visit: moves the issue to the front, dedupes by id,
 * keeps at most MAX_RECENTS entries.
 */
export function recordRecentIssue(entry: Omit<RecentIssue, "visited_at">): void {
  try {
    const next = [
      { ...entry, visited_at: new Date().toISOString() },
      ...load().filter((e) => e.id !== entry.id),
    ].slice(0, MAX_RECENTS);
    localStorage.setItem(STORAGE_KEY, JSON.stringify(next));
  } catch {
    // Storage full or unavailable — recents are best-effort.
  }
}

/**
 * Hook for the shared issue-detail body: records a recent visit once the
 * issue loads. Covers both the full page and the peek drawer since they
 * share IssueDetailContent.
 */
export function useRecordRecentIssue(
  issue:
    | { id: string; name: string; display_id: string } | null | undefined,
  slug: string,
  identifier: string,
): void {
  const issueId = issue?.id;
  useEffect(() => {
    if (!issueId || !issue) return;
    recordRecentIssue({
      id: issue.id,
      name: issue.name,
      display_id: issue.display_id,
      slug,
      identifier,
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [issueId]);
}
