import { useCallback, useEffect, useMemo, useRef } from "react";
import {
  DEFAULT_DISPLAY_SETTINGS,
  type DisplayFields,
  type DisplaySettings,
  type GroupBy,
  type OrderBy,
} from "./useDisplaySettings";
import { EMPTY_FILTERS, type IssueFilters } from "../../lib/filters";
import {
  useCreateIssueView,
  useDeleteIssueView,
  useIssueViews,
  useUpdateIssueView,
  type IssueViewRow,
} from "../../lib/savedViews";
import { toast } from "../ui/toast";

import { useAuth } from "../../lib/auth";

/** A named per-project saved view: an IssueFilters object plus the
 *  display preset that was active when it was saved. Persisted
 *  server-side (C9T2) so views roam across devices. A view may be
 *  shared with the project by its owner (C10T1): shared rows are
 *  read-only for everyone but the owner. */
export interface SavedView {
  id: string;
  name: string;
  /** The IssueFilters object, stored opaquely by the backend. */
  filters: IssueFilters;
  /** Null when saved from a page without display settings (spreadsheet). */
  display: DisplaySettings | null;
  isDefault: boolean;
  /** True once the owner shares the view with the project. */
  shared: boolean;
  /** Owner user id — owner-only actions gate on this. */
  ownerId: string;
  /** Display name of the owner, for the "Shared" section. */
  ownerName: string;
}

const MAX_VIEWS = 50;
const MAX_NAME_LEN = 64;

const GROUP_BYS: GroupBy[] = ["state", "priority", "none"];
const ORDER_BYS: OrderBy[] = [
  "-updated_at",
  "updated_at",
  "-created_at",
  "created_at",
  "-priority",
  "priority",
  "sequence_id",
  "-sequence_id",
];

function sanitizeDisplay(raw: unknown): DisplaySettings | null {
  if (raw === null || raw === undefined) return null;
  if (typeof raw !== "object") return null;
  const p = raw as Partial<DisplaySettings>;
  const fields: DisplayFields = {
    ...DEFAULT_DISPLAY_SETTINGS.fields,
    ...((p.fields as Partial<DisplayFields> | undefined) ?? {}),
  };
  for (const k of Object.keys(fields) as (keyof DisplayFields)[]) {
    if (typeof fields[k] !== "boolean") fields[k] = true;
  }
  return {
    fields,
    groupBy: GROUP_BYS.includes(p.groupBy as GroupBy)
      ? (p.groupBy as GroupBy)
      : DEFAULT_DISPLAY_SETTINGS.groupBy,
    orderBy: ORDER_BYS.includes(p.orderBy as OrderBy)
      ? (p.orderBy as OrderBy)
      : DEFAULT_DISPLAY_SETTINGS.orderBy,
    showEmptyGroups:
      typeof p.showEmptyGroups === "boolean"
        ? p.showEmptyGroups
        : DEFAULT_DISPLAY_SETTINGS.showEmptyGroups,
  };
}

function sanitizeFilters(raw: unknown): IssueFilters | null {
  if (raw === null || raw === undefined || typeof raw !== "object") {
    return null;
  }
  // The backend stores filters opaquely; be defensive on read and merge
  // over EMPTY_FILTERS so a partial row can never break the menu.
  const p = raw as Partial<IssueFilters>;
  return {
    ...EMPTY_FILTERS,
    ...Object.fromEntries(
      Object.entries(p).filter(([, v]) => v !== undefined),
    ),
  } as IssueFilters;
}

/** Validate one backend row into a SavedView. Exported for unit tests.
 *  Garbage rows are dropped, never throw. */
export function sanitizeView(raw: unknown): SavedView | null {
  if (!raw || typeof raw !== "object") return null;
  const v = raw as Partial<IssueViewRow> & Partial<SavedView>;
  if (typeof v.id !== "string" || !v.id) return null;
  if (typeof v.name !== "string" || !v.name.trim()) return null;
  const filters = sanitizeFilters(v.filters);
  if (!filters) return null;
  const isDefault = v.is_default === true || v.isDefault === true;
  return {
    id: v.id,
    name: v.name.trim().slice(0, MAX_NAME_LEN),
    filters,
    display: sanitizeDisplay(v.display),
    isDefault,
    shared: v.shared === true,
    ownerId: typeof v.owner_id === "string" ? v.owner_id : "",
    ownerName: typeof v.owner_name === "string" ? v.owner_name : "",
  };
}

function toSavedView(row: IssueViewRow): SavedView | null {
  return sanitizeView(row);
}

/** Per-project saved views, persisted to the backend (C9T2).
 *
 *  On API failure the hook shows an honest error toast — there is no
 *  silent fallback (the old localStorage path was dropped entirely).
 *  Validation behavior is kept from the localStorage implementation:
 *  blank names are rejected inline, the 50-view cap is enforced
 *  client-side, and duplicate names surface as the backend's 409. */
export function useSavedViews(projectId: string) {
  const { user, loading: authLoading } = useAuth();
  const currentUserId = user?.id ?? "";
  const query = useIssueViews(projectId);
  const createMutation = useCreateIssueView(projectId);
  const updateMutation = useUpdateIssueView(projectId);
  const deleteMutation = useDeleteIssueView(projectId);

  // Honest error toast on load failure — exactly once per failure, reset
  // when the query recovers.
  const toasted = useRef(false);
  useEffect(() => {
    if (query.error && !toasted.current) {
      toasted.current = true;
      toast.add({
        title: "Could not load saved views",
        type: "error",
      });
    } else if (!query.error) {
      toasted.current = false;
    }
  }, [query.error]);

  const views = useMemo(
    () =>
      (query.data ?? [])
        .map(toSavedView)
        .filter((v): v is SavedView => v !== null)
        .slice(0, MAX_VIEWS),
    [query.data],
  );

  /** Views the current user owns (private or shared). */
  const ownViews = useMemo(
    () => views.filter((v) => v.ownerId === currentUserId),
    [views, currentUserId],
  );
  /** Views shared by other project members — read-only for this user. */
  const sharedViews = useMemo(
    () =>
      views.filter((v) => v.shared && v.ownerId !== currentUserId),
    [views, currentUserId],
  );

  /** Creates a view; returns it, or null when the name is blank or the
   *  per-project cap is reached. Throws ApiError on request failure
   *  (409 = duplicate name) so the caller can show an honest error. */
  const createView = useCallback(
    async (
      name: string,
      filters: IssueFilters,
      display: DisplaySettings | null,
    ): Promise<SavedView | null> => {
      const clean = name.trim().slice(0, MAX_NAME_LEN);
      if (!clean || views.length >= MAX_VIEWS) return null;
      if (!projectId) throw new Error("project not loaded yet");
      const row = await createMutation.mutateAsync({
        name: clean,
        filters,
        display: sanitizeDisplay(display),
      });
      return toSavedView(row);
    },
    [views, projectId, createMutation],
  );

  /** Renames a view; returns false when the name is blank. Throws
   *  ApiError on request failure (409 = name taken by another view). */
  const renameView = useCallback(
    async (id: string, name: string): Promise<boolean> => {
      const clean = name.trim().slice(0, MAX_NAME_LEN);
      if (!clean) return false;
      if (!views.some((v) => v.id === id)) return false;
      if (!projectId) throw new Error("project not loaded yet");
      await updateMutation.mutateAsync({ viewId: id, patch: { name: clean } });
      return true;
    },
    [views, projectId, updateMutation],
  );

  /** Deletes a view. Throws ApiError on request failure. */
  const deleteView = useCallback(
    async (id: string): Promise<void> => {
      if (!projectId) throw new Error("project not loaded yet");
      await deleteMutation.mutateAsync(id);
    },
    [projectId, deleteMutation],
  );

  /** Sets (or clears, with null) the default view. Throws ApiError on
   *  request failure. */
  const setDefaultView = useCallback(
    async (id: string | null): Promise<void> => {
      if (!projectId) throw new Error("project not loaded yet");
      if (id === null) {
        const current = views.find((v) => v.isDefault);
        if (!current) return;
        await updateMutation.mutateAsync({
          viewId: current.id,
          patch: { is_default: false },
        });
        return;
      }
      await updateMutation.mutateAsync({
        viewId: id,
        patch: { is_default: true },
      });
    },
    [views, projectId, updateMutation],
  );

  /** Toggles project-wide sharing for an owned view (C10T1). Throws
   *  ApiError on request failure (403 = not the owner) so the caller can
   *  show an honest error. */
  const setViewShared = useCallback(
    async (id: string, shared: boolean): Promise<void> => {
      if (!projectId) throw new Error("project not loaded yet");
      if (!ownViews.some((v) => v.id === id))
        throw new Error("only the view owner can change sharing");
      await updateMutation.mutateAsync({ viewId: id, patch: { shared } });
    },
    [ownViews, projectId, updateMutation],
  );

  return {
    views,
    /** Views owned by the current user (private or shared). */
    ownViews,
    /** Views shared by other members — read-only for this user. */
    sharedViews,
    // Gated on auth too: the own/shared split keys on the current user
    // id, so the menu must not render (or auto-apply a default) before
    // it resolves.
    viewsLoading: query.isLoading || authLoading,
    createView,
    renameView,
    deleteView,
    setDefaultView,
    setViewShared,
  };
}
