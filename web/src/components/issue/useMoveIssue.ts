import { useState } from "react";
import { api } from "../../lib/api";
import type { Issue } from "../../lib/types";

/** The move report returned by POST .../issues/{uuid}/move. */
export interface MoveIssueResult {
  issue: Issue;
  new_display_id: string;
  state_remapped: boolean;
  labels_dropped: string[];
  estimate_dropped: boolean;
  memberships_dropped: { cycles: number; modules: number; releases: number };
  custom_values_dropped: number;
}

/** API path for the C24T0 move endpoint. */
export function moveIssuePath(
  slug: string,
  identifier: string,
  uuid: string,
): string {
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/issues/${encodeURIComponent(uuid)}/move`;
}

/**
 * useMoveIssue: POST the C24T0 move endpoint. Returns the pending state
 * and any error for the caller to surface; the caller owns the
 * post-move toast + navigation because only it knows the dialog and
 * routing context.
 */
export function useMoveIssue(slug: string, identifier: string) {
  const [moving, setMoving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function moveIssue(
    uuid: string,
    targetIdentifier: string,
  ): Promise<MoveIssueResult> {
    if (moving) throw new Error("move already in progress");
    setMoving(true);
    setError(null);
    try {
      return await api.post<MoveIssueResult>(moveIssuePath(slug, identifier, uuid), {
        target_project_identifier: targetIdentifier,
      });
    } catch (e) {
      const msg = e instanceof Error ? e.message : "Failed to move issue";
      setError(msg);
      throw e;
    } finally {
      setMoving(false);
    }
  }

  return { moveIssue, moving, error };
}
