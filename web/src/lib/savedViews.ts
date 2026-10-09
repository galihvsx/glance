// Saved views client (C9T2): per-user named filter+display presets on a
// project, persisted server-side at /api/v1/projects/{id}/views.
// Same-origin cookie auth via the shared api wrapper.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";
import type { IssueFilters } from "./filters";
import type { DisplaySettings } from "../components/issue/useDisplaySettings";

/** One saved-view row as the backend returns it. */
export interface IssueViewRow {
  id: string;
  name: string;
  /** Opaque IssueFilters object (web/src/lib/filters.ts). */
  filters: IssueFilters;
  display: DisplaySettings | null;
  is_default: boolean;
  /** True once the owner shares the view with the project (C10T1):
   *  the row then appears in every project member's list. */
  shared: boolean;
  /** Owner user id — the share/unshare toggle is owner-only. */
  owner_id: string;
  /** Display name of the owner (name, falling back to email). */
  owner_name: string;
  created_at: string;
}

export const savedViewKeys = {
  all: (projectId: string) => ["saved-views", projectId] as const,
};

async function fetchViews(projectId: string): Promise<IssueViewRow[]> {
  const data = await api.get<{ views: IssueViewRow[] }>(
    `/api/v1/projects/${projectId}/views`,
  );
  return data.views ?? [];
}

/** Hook: the caller's saved views on the project. Disabled (empty list)
 *  until projectId is known. Errors surface via the query's error — the
 *  consumer (useSavedViews) toasts them honestly. */
export function useIssueViews(projectId: string) {
  return useQuery({
    queryKey: savedViewKeys.all(projectId),
    queryFn: () => fetchViews(projectId),
    enabled: projectId !== "",
  });
}

export interface CreateViewInput {
  name: string;
  filters: IssueFilters;
  display: DisplaySettings | null;
}

export interface UpdateViewInput {
  name?: string;
  is_default?: boolean;
  /** Toggle project-wide visibility (C10T1). Owner-only: the backend
   *  403s a non-owner. */
  shared?: boolean;
}

/** POST a view. Throws ApiError (409 on a duplicate name). */
export async function createIssueView(
  projectId: string,
  input: CreateViewInput,
): Promise<IssueViewRow> {
  return api.post<IssueViewRow>(`/api/v1/projects/${projectId}/views`, {
    name: input.name,
    filters: input.filters,
    display: input.display,
  });
}

/** PATCH a view (rename and/or flip the default flag). Throws ApiError
 *  (404 unknown view, 409 name clash). */
export async function updateIssueView(
  projectId: string,
  viewId: string,
  patch: UpdateViewInput,
): Promise<IssueViewRow> {
  return api.patch<IssueViewRow>(
    `/api/v1/projects/${projectId}/views/${viewId}`,
    patch,
  );
}

/** DELETE a view. Idempotent — resolves on 204 either way. */
export async function deleteIssueView(
  projectId: string,
  viewId: string,
): Promise<void> {
  await api.del(`/api/v1/projects/${projectId}/views/${viewId}`);
}

/** Invalidate the views cache after a mutation. */
export function useInvalidateIssueViews() {
  const queryClient = useQueryClient();
  return (projectId: string) =>
    queryClient.invalidateQueries({
      queryKey: savedViewKeys.all(projectId),
    });
}

/** Mutation hooks: list/create/update/delete, each invalidating the views
 *  cache on success so the menu re-renders from the server. */
export function useCreateIssueView(projectId: string) {
  const invalidate = useInvalidateIssueViews();
  return useMutation({
    mutationFn: (input: CreateViewInput) => createIssueView(projectId, input),
    onSuccess: () => invalidate(projectId),
  });
}

export function useUpdateIssueView(projectId: string) {
  const invalidate = useInvalidateIssueViews();
  return useMutation({
    mutationFn: ({ viewId, patch }: { viewId: string; patch: UpdateViewInput }) =>
      updateIssueView(projectId, viewId, patch),
    onSuccess: () => invalidate(projectId),
  });
}

export function useDeleteIssueView(projectId: string) {
  const invalidate = useInvalidateIssueViews();
  return useMutation({
    mutationFn: (viewId: string) => deleteIssueView(projectId, viewId),
    onSuccess: () => invalidate(projectId),
  });
}
