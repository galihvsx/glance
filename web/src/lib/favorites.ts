// Favorites client (C7T4): star/unstar issues and projects, list the
// caller's stars. Same-origin cookie auth via the shared api wrapper.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";

export type FavoritableType = "issue" | "project";

export interface FavoriteIssue {
  id: string;
  display_id: string;
  name: string;
  workspace_slug: string;
  project_id: string;
  project_identifier: string;
  project_name: string;
  starred_at: string;
}

export interface FavoriteProject {
  id: string;
  identifier: string;
  name: string;
  workspace_slug: string;
  starred_at: string;
}

export interface FavoritesList {
  issues: FavoriteIssue[];
  projects: FavoriteProject[];
}

export const favoriteKeys = {
  all: ["favorites"] as const,
};

export async function fetchFavorites(): Promise<FavoritesList> {
  const data = await api.get<FavoritesList>("/api/v1/favorites");
  return {
    issues: data.issues ?? [],
    projects: data.projects ?? [],
  };
}

/** Star a target. Resolves when the star exists (201 new, 200 already). */
export async function starFavorite(
  type: FavoritableType,
  id: string,
): Promise<unknown> {
  return api.post("/api/v1/favorites", { type, id });
}

/** Unstar a target. Idempotent — never-starred is a no-op. */
export async function unstarFavorite(
  type: FavoritableType,
  id: string,
): Promise<void> {
  await api.del("/api/v1/favorites", { type, id });
}

export function useFavorites() {
  return useQuery({
    queryKey: favoriteKeys.all,
    queryFn: fetchFavorites,
  });
}

/** Reads the shared favorites cache to answer "is this starred?" */
export function useIsStarred(
  type: FavoritableType,
  id: string,
): boolean {
  const { data } = useFavorites();
  return isStarred(data, type, id);
}

/** Toggle mutation: flips the star and invalidates the favorites cache. */
export function useToggleFavorite() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      type,
      id,
      starred,
    }: {
      type: FavoritableType;
      id: string;
      starred: boolean;
    }) => (starred ? unstarFavorite(type, id) : starFavorite(type, id)),
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: favoriteKeys.all });
    },
  });
}

/** Pure: is the target in the (possibly undefined) favorites list? */
export function isStarred(
  list: FavoritesList | undefined,
  type: FavoritableType,
  id: string,
): boolean {
  if (!list) return false;
  const haystack = type === "issue" ? list.issues : list.projects;
  return haystack.some((f) => f.id === id);
}

/** Pure: deep link to a starred issue. */
export function issueFavoriteHref(f: FavoriteIssue): string {
  return `/w/${encodeURIComponent(f.workspace_slug)}/p/${encodeURIComponent(f.project_identifier)}/i/${encodeURIComponent(f.id)}`;
}

/** Pure: deep link to a starred project. */
export function projectFavoriteHref(f: FavoriteProject): string {
  return `/w/${encodeURIComponent(f.workspace_slug)}/p/${encodeURIComponent(f.identifier)}`;
}
