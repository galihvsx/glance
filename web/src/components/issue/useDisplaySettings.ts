import { useCallback, useState } from "react";

/** Which issue attributes render on list rows / board cards. */
export interface DisplayFields {
  /** State badge on list rows (board columns already imply state). */
  state: boolean;
  priority: boolean;
  labels: boolean;
  assignees: boolean;
  /** Start/target date line on cards. */
  dates: boolean;
}

export type GroupBy = "state" | "priority" | "none";

/** Whitelisted ?order_by= values the backend accepts (internal/service/issue.go). */
export type OrderBy =
  | "-updated_at"
  | "updated_at"
  | "-created_at"
  | "created_at"
  | "-priority"
  | "priority"
  | "sequence_id"
  | "-sequence_id";

export interface DisplaySettings {
  fields: DisplayFields;
  groupBy: GroupBy;
  orderBy: OrderBy;
  showEmptyGroups: boolean;
}

export const DEFAULT_DISPLAY_SETTINGS: DisplaySettings = {
  fields: { state: true, priority: true, labels: true, assignees: true, dates: true },
  groupBy: "state",
  orderBy: "-updated_at",
  showEmptyGroups: true,
};

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
  return `glance:display:${slug}:${identifier}`;
}

function load(slug: string, identifier: string): DisplaySettings {
  try {
    const raw = localStorage.getItem(storageKey(slug, identifier));
    if (!raw) return DEFAULT_DISPLAY_SETTINGS;
    const p = JSON.parse(raw) as Partial<DisplaySettings>;
    return {
      fields: { ...DEFAULT_DISPLAY_SETTINGS.fields, ...(p.fields ?? {}) },
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
  } catch {
    return DEFAULT_DISPLAY_SETTINGS;
  }
}

/** Per-project display settings (fields, grouping, ordering), persisted to
 *  localStorage. Changes apply live — there is no Apply step. */
export function useDisplaySettings(slug: string, identifier: string) {
  const key = `${slug}\0${identifier}`;
  // Adjust-during-render keeps settings in sync when the route params
  // change without a remount (project switcher) — no effect needed.
  const [cached, setCached] = useState(() => ({
    key,
    settings: load(slug, identifier),
  }));
  if (cached.key !== key) {
    setCached({ key, settings: load(slug, identifier) });
  }
  const settings = cached.settings;

  const persist = useCallback(
    (next: DisplaySettings) => {
      try {
        localStorage.setItem(
          storageKey(slug, identifier),
          JSON.stringify(next),
        );
      } catch {
        // Storage full / private mode — settings still apply for the session.
      }
    },
    [slug, identifier],
  );

  const update = useCallback(
    (patch: Partial<DisplaySettings>) => {
      setCached((prev) => {
        const next = { ...prev.settings, ...patch };
        persist(next);
        return { ...prev, settings: next };
      });
    },
    [persist],
  );

  const updateFields = useCallback(
    (patch: Partial<DisplayFields>) => {
      setCached((prev) => {
        const next = {
          ...prev.settings,
          fields: { ...prev.settings.fields, ...patch },
        };
        persist(next);
        return { ...prev, settings: next };
      });
    },
    [persist],
  );

  const reset = useCallback(() => {
    setCached((prev) => ({ ...prev, settings: DEFAULT_DISPLAY_SETTINGS }));
    persist(DEFAULT_DISPLAY_SETTINGS);
  }, [persist]);

  return { settings, update, updateFields, reset };
}

/** "Jan 5 → Jan 12", "Jan 12", or "" when neither date is set. */
export function formatIssueDateRange(
  startDate?: string | null,
  targetDate?: string | null,
): string {
  const fmt = (d: string) =>
    new Date(d).toLocaleDateString(undefined, {
      month: "short",
      day: "numeric",
    });
  if (startDate && targetDate) return `${fmt(startDate)} → ${fmt(targetDate)}`;
  if (targetDate) return fmt(targetDate);
  if (startDate) return fmt(startDate);
  return "";
}
