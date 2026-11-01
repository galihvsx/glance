// Saved views client (C9T2, generalized C113T1): per-user named
// filter+display presets on a project, or (C113) PQL presets on a
// workspace — persisted server-side at /api/v1/projects/{id}/views
// or /api/v1/workspaces/{slug}/views. Same-origin cookie auth via
// the shared api wrapper.
import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseMutationResult,
  type UseQueryResult,
} from "@tanstack/react-query";
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
  /** True once the owner shares the view with the project (C10T1) or
   *  workspace (C113T1): the row then appears in every member's list. */
  shared: boolean;
  /** Owner user id — the share/unshare toggle is owner-only. */
  owner_id: string;
  /** Display name of the owner (name, falling back to email). */
  owner_name: string;
  created_at: string;
}

/** Which scope a view belongs to. Project scope takes a project UUID
 *  (the pre-C113 shape, kept working unchanged); workspace scope
 *  takes a workspace slug (C113T1, the C113T0 API contract). Exactly
 *  one of the two — never both — mirroring the backend's
 *  exactly-one-scope row. */
export type SavedViewScope =
  | { projectId: string }
  | { workspaceSlug: string };

export function isWorkspaceScope(
  scope: SavedViewScope,
): scope is { workspaceSlug: string } {
  return "workspaceSlug" in scope;
}

/** The views collection URL for a scope (no trailing slash). */
export function scopeViewsBasePath(scope: SavedViewScope): string {
  return isWorkspaceScope(scope)
    ? `/api/v1/workspaces/${encodeURIComponent(scope.workspaceSlug)}/views`
    : `/api/v1/projects/${scope.projectId}/views`;
}

/** The scope's identity for query keys and "loaded yet" gates: the
 *  project UUID, or the workspace slug. Empty means "not resolved". */
export function scopeId(scope: SavedViewScope): string {
  return isWorkspaceScope(scope) ? scope.workspaceSlug : scope.projectId;
}

/** Accept the legacy bare-project-UUID form as project scope, so
 *  pre-C113 call sites keep compiling and behaving unchanged. */
function normalizeScope(
  projectIdOrScope: string | SavedViewScope,
): SavedViewScope {
  return typeof projectIdOrScope === "string"
    ? { projectId: projectIdOrScope }
    : projectIdOrScope;
}

export const savedViewKeys = {
  all: (projectId: string) => ["saved-views", projectId] as const,
  /** Workspace-scope key (C113T1): the "ws" segment keeps it
   *  disjoint from project keys, which are bare UUIDs. */
  ws: (slug: string) => ["saved-views", "ws", slug] as const,
  forScope: (scope: SavedViewScope) =>
    isWorkspaceScope(scope)
      ? savedViewKeys.ws(scope.workspaceSlug)
      : savedViewKeys.all(scope.projectId),
};

async function fetchViewsForScope(
  scope: SavedViewScope,
): Promise<IssueViewRow[]> {
  const data = await api.get<{ views: IssueViewRow[] }>(
    scopeViewsBasePath(scope),
  );
  return data.views ?? [];
}

/** Hook: the caller's saved views in the scope. Disabled (empty list)
 *  until the scope identity is known. Errors surface via the query's
 *  error — the consumer (useSavedViews) toasts them honestly. The
 *  bare-string form is the legacy project-scope call, kept so
 *  pre-C113 call sites work unchanged. */
export function useIssueViews(projectId: string): UseQueryResult<IssueViewRow[], Error>;
export function useIssueViews(scope: SavedViewScope): UseQueryResult<IssueViewRow[], Error>;
export function useIssueViews(projectIdOrScope: string | SavedViewScope) {
  const scope = normalizeScope(projectIdOrScope);
  return useQuery({
    queryKey: savedViewKeys.forScope(scope),
    queryFn: () => fetchViewsForScope(scope),
    enabled: scopeId(scope) !== "",
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
  /** Toggle scope-wide visibility (C10T1). Owner-only: the backend
   *  403s a non-owner. */
  shared?: boolean;
}

/** POST a view. Throws ApiError (409 on a duplicate name). The
 *  bare-string form is the legacy project-scope call. */
export async function createIssueView(
  projectId: string,
  input: CreateViewInput,
): Promise<IssueViewRow>;
export async function createIssueView(
  scope: SavedViewScope,
  input: CreateViewInput,
): Promise<IssueViewRow>;
export async function createIssueView(
  projectIdOrScope: string | SavedViewScope,
  input: CreateViewInput,
): Promise<IssueViewRow> {
  const scope = normalizeScope(projectIdOrScope);
  return api.post<IssueViewRow>(scopeViewsBasePath(scope), {
    name: input.name,
    filters: input.filters,
    display: input.display,
  });
}

/** PATCH a view (rename and/or flip the default flag). Throws ApiError
 *  (404 unknown view, 409 name clash). The bare-string form is the
 *  legacy project-scope call. */
export async function updateIssueView(
  projectId: string,
  viewId: string,
  patch: UpdateViewInput,
): Promise<IssueViewRow>;
export async function updateIssueView(
  scope: SavedViewScope,
  viewId: string,
  patch: UpdateViewInput,
): Promise<IssueViewRow>;
export async function updateIssueView(
  projectIdOrScope: string | SavedViewScope,
  viewId: string,
  patch: UpdateViewInput,
): Promise<IssueViewRow> {
  const scope = normalizeScope(projectIdOrScope);
  return api.patch<IssueViewRow>(
    `${scopeViewsBasePath(scope)}/${viewId}`,
    patch,
  );
}

/** DELETE a view. Idempotent — resolves on 204 either way. The
 *  bare-string form is the legacy project-scope call. */
export async function deleteIssueView(
  projectId: string,
  viewId: string,
): Promise<void>;
export async function deleteIssueView(
  scope: SavedViewScope,
  viewId: string,
): Promise<void>;
export async function deleteIssueView(
  projectIdOrScope: string | SavedViewScope,
  viewId: string,
): Promise<void> {
  const scope = normalizeScope(projectIdOrScope);
  await api.del(`${scopeViewsBasePath(scope)}/${viewId}`);
}

/** Invalidate the views cache after a mutation. The bare-string form
 *  is the legacy project-scope call. */
export function useInvalidateIssueViews() {
  const queryClient = useQueryClient();
  return (projectIdOrScope: string | SavedViewScope) =>
    queryClient.invalidateQueries({
      queryKey: savedViewKeys.forScope(normalizeScope(projectIdOrScope)),
    });
}

/** Mutation hooks: list/create/update/delete, each invalidating the views
 *  cache on success so the menu re-renders from the server. The
 *  bare-string form is the legacy project-scope call. */
export function useCreateIssueView(projectId: string): UseMutationResult<IssueViewRow, Error, CreateViewInput>;
export function useCreateIssueView(scope: SavedViewScope): UseMutationResult<IssueViewRow, Error, CreateViewInput>;
export function useCreateIssueView(projectIdOrScope: string | SavedViewScope) {
  const scope = normalizeScope(projectIdOrScope);
  const invalidate = useInvalidateIssueViews();
  return useMutation({
    mutationFn: (input: CreateViewInput) => createIssueView(scope, input),
    onSuccess: () => invalidate(scope),
  });
}

export function useUpdateIssueView(projectId: string): UseMutationResult<IssueViewRow, Error, { viewId: string; patch: UpdateViewInput }>;
export function useUpdateIssueView(scope: SavedViewScope): UseMutationResult<IssueViewRow, Error, { viewId: string; patch: UpdateViewInput }>;
export function useUpdateIssueView(projectIdOrScope: string | SavedViewScope) {
  const scope = normalizeScope(projectIdOrScope);
  const invalidate = useInvalidateIssueViews();
  return useMutation({
    mutationFn: ({ viewId, patch }: { viewId: string; patch: UpdateViewInput }) =>
      updateIssueView(scope, viewId, patch),
    onSuccess: () => invalidate(scope),
  });
}

export function useDeleteIssueView(projectId: string): UseMutationResult<void, Error, string>;
export function useDeleteIssueView(scope: SavedViewScope): UseMutationResult<void, Error, string>;
export function useDeleteIssueView(projectIdOrScope: string | SavedViewScope) {
  const scope = normalizeScope(projectIdOrScope);
  const invalidate = useInvalidateIssueViews();
  return useMutation({
    mutationFn: (viewId: string) => deleteIssueView(scope, viewId),
    onSuccess: () => invalidate(scope),
  });
}
