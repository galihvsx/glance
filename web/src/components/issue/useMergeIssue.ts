import { useState } from "react";
import { api } from "../../lib/api";
import type { Issue } from "../../lib/types";

/** The merge result returned by POST .../issues/{uuid}/merge. */
export interface MergeIssueResult {
  merged: boolean;
  already_merged: boolean;
  issue: Issue;
  into_display_id: string;
}

/** API path for the C25T3 duplicate-merge endpoint. */
export function mergeIssuePath(
  slug: string,
  identifier: string,
  uuid: string,
): string {
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/issues/${encodeURIComponent(uuid)}/merge`;
}

/**
 * useMergeIssue: POST the C25T3 duplicate-merge endpoint. intoUuid is the
 * surviving issue's UUID. Returns the pending state and any error for the
 * caller to surface; the caller owns the toast + refetch because only it
 * knows the dialog and routing context.
 */
export function useMergeIssue(slug: string, identifier: string) {
  const [merging, setMerging] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function mergeIssue(
    uuid: string,
    intoUuid: string,
  ): Promise<MergeIssueResult> {
    if (merging) throw new Error("merge already in progress");
    setMerging(true);
    setError(null);
    try {
      return await api.post<MergeIssueResult>(
        mergeIssuePath(slug, identifier, uuid),
        { into_uuid: intoUuid },
      );
    } catch (e) {
      const msg = e instanceof Error ? e.message : "Failed to merge issue";
      setError(msg);
      throw e;
    } finally {
      setMerging(false);
    }
  }

  return { mergeIssue, merging, error };
}
