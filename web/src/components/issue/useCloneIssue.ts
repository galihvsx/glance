import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { api } from "../../lib/api";
import type { Issue } from "../../lib/types";

/** API path for the C8T4 clone endpoint. */
export function cloneIssuePath(
  slug: string,
  identifier: string,
  uuid: string,
): string {
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/issues/${encodeURIComponent(uuid)}/clone`;
}

/**
 * useCloneIssue: POST the C8T4 clone endpoint and navigate to the new
 * issue. Cloning is cheap and reversible (a fresh issue), so no confirm
 * dialog. Returns the pending state and any error for the caller to
 * surface next to the menu that triggered it.
 */
export function useCloneIssue(slug: string, identifier: string) {
  const navigate = useNavigate();
  const [cloning, setCloning] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function cloneIssue(uuid: string): Promise<void> {
    if (cloning) return;
    setCloning(true);
    setError(null);
    try {
      const created = await api.post<Issue>(
        cloneIssuePath(slug, identifier, uuid),
      );
      navigate(
        `/w/${encodeURIComponent(slug)}/p/${encodeURIComponent(identifier)}/i/${created.id}`,
      );
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to clone issue");
    } finally {
      setCloning(false);
    }
  }

  return { cloneIssue, cloning, error };
}
