// archived: Archived issues view client (C7T5).
//
// Backend contract (verified 2026-10-09 against internal/api/issue_handler.go
// and internal/service/issue.go — read-only, nothing written there):
//   GET .../issues?archived=1&per_page=100 -> {results: Issue[]}
//     The ?archived=1 param only *includes* archived rows (the default
//     working-set filter `archived_at IS NULL` is dropped) — there is NO
//     "archived only" filter on the backend, so this module filters to
//     `archived_at != null` client-side (see isArchived / fetchArchived).
//   DELETE .../issues/:uuid -> soft delete (deleted_at). There is no
//     hard-delete endpoint; "permanent delete" here means removed from the
//     project (soft-deleted on the backend), consistent with every other
//     delete in the app.
//   Unarchive: NOT AVAILABLE. The PATCH /issues/:uuid body has no
//     `archived_at` field and IssuePatch has none either; no dedicated
//     /archive or /unarchive route exists, and nothing in the backend
//     writes issues.archived_at. unarchiveIssue() below throws a
//     descriptive error until the coordinator's backend task adds the
//     endpoint — the Archived view renders the Unarchive button disabled
//     with a tooltip instead of pretending to work.
//   "Archived by": NOT AVAILABLE. There is no archived_by column/field;
//     the row shows the archived date only.

import { api } from "./api";
import type { Issue, IssueListResult } from "./types";

function base(slug: string, identifier: string): string {
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/issues`;
}

/** An issue is archived when the backend set archived_at on it. */
export function isArchived(issue: Issue): boolean {
  return issue.archived_at != null;
}

export function fetchArchived(
  slug: string,
  identifier: string,
): Promise<Issue[]> {
  return api
    .get<IssueListResult>(`${base(slug, identifier)}?archived=1&per_page=100`)
    .then((r) => (r.results ?? []).filter(isArchived));
}

/**
 * Restore an archived issue to the working set.
 * Backend gap (C7T5, verified 2026-10-09): no unarchive endpoint exists.
 * Throws until the backend adds one; callers should render this as a
 * disabled action rather than letting the throw reach the user silently.
 */
export function unarchiveIssue(
  _slug: string,
  _identifier: string,
  _id: string,
): Promise<Issue> {
  return Promise.reject(
    new Error(
      "Unarchiving is not supported by the backend yet (no archive/unarchive endpoint). Flagged for the coordinator.",
    ),
  );
}

export function deleteArchivedIssue(
  slug: string,
  identifier: string,
  id: string,
): Promise<void> {
  return api.del<void>(`${base(slug, identifier)}/${id}`);
}

export function archivedKey(slug: string, identifier: string) {
  return ["archived", slug, identifier] as const;
}
