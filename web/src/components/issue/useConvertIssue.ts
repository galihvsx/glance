import { useState } from "react";
import { api } from "../../lib/api";
import type { Issue } from "../../lib/types";

/** The conversion result returned by POST .../issues/{uuid}/convert. */
export interface ConvertIssueResult {
  converted: boolean;
  issue: Issue;
}

/** API path for the C24T1 convert endpoint. */
export function convertIssuePath(
  slug: string,
  identifier: string,
  uuid: string,
): string {
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/issues/${encodeURIComponent(uuid)}/convert`;
}

/**
 * useConvertIssue: POST the C24T1 convert endpoint. parentUuid set demotes
 * the issue to a sub-issue of that parent; null promotes it back to a
 * standalone issue. Returns the pending state and any error for the
 * caller to surface; the caller owns the toast + refetch because only it
 * knows the dialog and routing context.
 */
export function useConvertIssue(slug: string, identifier: string) {
  const [converting, setConverting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function convertIssue(
    uuid: string,
    parentUuid: string | null,
  ): Promise<ConvertIssueResult> {
    if (converting) throw new Error("convert already in progress");
    setConverting(true);
    setError(null);
    try {
      return await api.post<ConvertIssueResult>(
        convertIssuePath(slug, identifier, uuid),
        { parent_uuid: parentUuid },
      );
    } catch (e) {
      const msg = e instanceof Error ? e.message : "Failed to convert issue";
      setError(msg);
      throw e;
    } finally {
      setConverting(false);
    }
  }

  return { convertIssue, converting, error };
}
