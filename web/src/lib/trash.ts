// trash: Trash view client (C52T2).
//
// Backend contract (C52T0, verified against
// internal/api/issue_handler.go and internal/service/issue.go):
//   GET .../issues?deleted=1&per_page=100 -> {results: Issue[]}
//     DELETED-ONLY. This deliberately differs from ?archived=1, which
//     only *includes* archived rows: deleted rows are excluded
//     everywhere else, so the backend selects
//     deleted_at IS NOT NULL here, sorted deleted_at DESC. Each row
//     carries deleted_at (populated ONLY by this endpoint).
//   POST .../issues/:uuid/restore -> 200 + the restored Issue.
//     A live issue reads as 404 ("not in trash"), exactly as a
//     second DELETE reads as not-found.
//   POST .../issues/bulk-restore {ids: [...]} -> 200 +
//     {results: [{id, ok, error?}]} — per-item results mirroring
//     bulk-delete (C52T1). A live/unknown id is a per-item failure,
//     not a whole-batch failure.
//   "Deleted by": NOT AVAILABLE. There is no deleted_by column/field;
//     the row shows the deleted date only — the same documented
//     limitation as Archived's "Archived by".

import { api } from "./api";
import type { Issue, IssueListResult } from "./types";

function base(slug: string, identifier: string): string {
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/issues`;
}

/** One per-item outcome of a bulk-restore call (mirrors BulkItemResult). */
export interface BulkRestoreItem {
  id: string;
  ok: boolean;
  error?: string;
}

export function fetchDeleted(
  slug: string,
  identifier: string,
): Promise<Issue[]> {
  return api
    .get<IssueListResult>(`${base(slug, identifier)}?deleted=1&per_page=100`)
    .then((r) => r.results ?? []);
}

/**
 * Restore one deleted issue to the working set.
 * Backend: POST .../issues/:uuid/restore (C52T0). No request body.
 * Non-destructive, so the page issues it on a single click with no
 * confirm dialog.
 */
export function restoreIssue(
  slug: string,
  identifier: string,
  id: string,
): Promise<Issue> {
  return api.post<Issue>(
    `${base(slug, identifier)}/${encodeURIComponent(id)}/restore`,
  );
}

/**
 * Restore many deleted issues at once.
 * Backend: POST .../issues/bulk-restore {ids: [...]} (C52T1).
 */
export function bulkRestoreIssues(
  slug: string,
  identifier: string,
  ids: string[],
): Promise<BulkRestoreItem[]> {
  return api
    .post<{ results: BulkRestoreItem[] }>(
      `${base(slug, identifier)}/bulk-restore`,
      { ids },
    )
    .then((r) => r.results ?? []);
}

export function trashKey(slug: string, identifier: string) {
  return ["trash", slug, identifier] as const;
}
