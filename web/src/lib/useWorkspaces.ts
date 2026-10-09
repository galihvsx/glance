// Shared workspace-list query (onboarding gate + wizard).
//
// GET /api/v1/workspaces returns the wrapped shape {"workspaces": [...]};
// this hook unwraps it so callers work with the plain array.

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";
import type { Workspace } from "./types";

export const workspacesKey = ["workspaces"] as const;

export function useWorkspaces() {
  return useQuery({
    queryKey: workspacesKey,
    queryFn: () =>
      api
        .get<{ workspaces: Workspace[] }>("/api/v1/workspaces")
        .then((d) => d.workspaces),
  });
}

/** Seed the cache after creating a workspace so gates see it instantly
 *  (no refetch round-trip, no redirect loop). */
export function useSeedWorkspaces() {
  const queryClient = useQueryClient();
  return (workspaces: Workspace[]) =>
    queryClient.setQueryData<Workspace[]>(workspacesKey, workspaces);
}
