import { useCallback, useState } from "react";
import {
  DEFAULT_DISPLAY_SETTINGS,
  type DisplayFields,
  type DisplaySettings,
  type GroupBy,
  type OrderBy,
} from "./useDisplaySettings";

/** A named per-project saved view: a verbatim filter query string plus the
 *  display preset that was active when it was saved. */
export interface SavedView {
  id: string;
  name: string;
  /** Verbatim output of serializeFilters(filters).toString(). */
  filterQuery: string;
  /** Null when saved from a page without display settings (spreadsheet). */
  display: DisplaySettings | null;
  isDefault: boolean;
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

function storageKey(slug: string, identifier: string): string {
  return `glance:views:${slug}:${identifier}`;
}

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

/** Exported for unit tests. */
export function sanitizeView(raw: unknown): SavedView | null {
  if (!raw || typeof raw !== "object") return null;
  const v = raw as Partial<SavedView>;
  if (typeof v.id !== "string" || !v.id) return null;
  if (typeof v.name !== "string" || !v.name.trim()) return null;
  if (typeof v.filterQuery !== "string") return null;
  // Garbage query strings are dropped, never throw on a stored value.
  try {
    new URLSearchParams(v.filterQuery);
  } catch {
    return null;
  }
  return {
    id: v.id,
    name: v.name.trim().slice(0, MAX_NAME_LEN),
    filterQuery: v.filterQuery,
    display: sanitizeDisplay(v.display),
    isDefault: v.isDefault === true,
  };
}

function load(slug: string, identifier: string): SavedView[] {
  try {
    const raw = localStorage.getItem(storageKey(slug, identifier));
    if (!raw) return [];
    const arr = JSON.parse(raw);
    if (!Array.isArray(arr)) return [];
    const views = arr
      .map(sanitizeView)
      .filter((v): v is SavedView => v !== null)
      .slice(0, MAX_VIEWS);
    // At most one default survives a corrupted store.
    let seenDefault = false;
    for (const v of views) {
      if (v.isDefault) {
        if (seenDefault) v.isDefault = false;
        seenDefault = true;
      }
    }
    return views;
  } catch {
    return [];
  }
}

function newId(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
}

/** Per-project saved views, persisted to localStorage.
 *
 *  Storage choice: localStorage, not the backend. There is no
 *  user-preferences / saved-view store server-side, and a migration +
 *  CRUD endpoints for what is inherently per-device UI state would be
 *  over-engineering for v1 of this feature (same call the display panel
 *  made in cycle 1). If views ever need to roam across devices, add a
 *  backend store then — the SavedView shape is JSON-ready. */
export function useSavedViews(slug: string, identifier: string) {
  const key = `${slug}\0${identifier}`;
  // Same adjust-during-render pattern as useDisplaySettings: keeps views in
  // sync when the route params change without a remount.
  const [cached, setCached] = useState(() => ({
    key,
    views: load(slug, identifier),
  }));
  if (cached.key !== key) {
    setCached({ key, views: load(slug, identifier) });
  }
  const views = cached.views;

  const persist = useCallback(
    (next: SavedView[]) => {
      setCached((prev) => ({ ...prev, views: next }));
      try {
        localStorage.setItem(
          storageKey(slug, identifier),
          JSON.stringify(next),
        );
      } catch {
        // Storage full / private mode — views still work for the session.
      }
    },
    [slug, identifier],
  );

  /** Returns the created view, or null when the name is blank/taken or the
   *  per-project cap is reached. */
  const createView = useCallback(
    (
      name: string,
      filterQuery: string,
      display: DisplaySettings | null,
    ): SavedView | null => {
      const clean = name.trim().slice(0, MAX_NAME_LEN);
      if (!clean || views.length >= MAX_VIEWS) return null;
      if (
        views.some((v) => v.name.toLowerCase() === clean.toLowerCase())
      ) {
        return null;
      }
      const view: SavedView = {
        id: newId(),
        name: clean,
        filterQuery,
        display: sanitizeDisplay(display),
        isDefault: false,
      };
      persist([...views, view]);
      return view;
    },
    [views, persist],
  );

  /** Returns false when the name is blank or taken by another view. */
  const renameView = useCallback(
    (id: string, name: string): boolean => {
      const clean = name.trim().slice(0, MAX_NAME_LEN);
      if (!clean) return false;
      if (
        views.some(
          (v) => v.id !== id && v.name.toLowerCase() === clean.toLowerCase(),
        )
      ) {
        return false;
      }
      if (!views.some((v) => v.id === id)) return false;
      persist(views.map((v) => (v.id === id ? { ...v, name: clean } : v)));
      return true;
    },
    [views, persist],
  );

  const deleteView = useCallback(
    (id: string) => {
      if (views.some((v) => v.id === id)) {
        persist(views.filter((v) => v.id !== id));
      }
    },
    [views, persist],
  );

  const setDefaultView = useCallback(
    (id: string | null) => {
      persist(views.map((v) => ({ ...v, isDefault: v.id === id })));
    },
    [views, persist],
  );

  return { views, createView, renameView, deleteView, setDefaultView };
}
