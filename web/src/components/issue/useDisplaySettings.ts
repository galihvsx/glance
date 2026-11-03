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
  /** display_id badge on cards (dense-row ID column on list rows).
   *  Aria-labels keep the ID even when the badge is hidden. */
  id: boolean;
  /** Subtask progress bar on cards (only when the issue has direct
   *  subtasks; the dense list row has no slot for it). */
  subitems: boolean;
  /** Module chips from project_structure (guarded: nothing renders
   *  when the list fetch did not pass include_project_structure=1). */
  modules: boolean;
  /** Cycle chip from project_structure (guarded like modules). */
  cycle: boolean;
  /** Release chip from project_structure (guarded like modules). */
  release: boolean;
  /** Attachment count row, rendered only when the count is > 0. */
  attachments: boolean;
  /** Link count row, rendered only when the count is > 0. */
  links: boolean;
  /** Dependency count row (blocks + blocked-by, one count),
   *  rendered only when the count is > 0. */
  dependencies: boolean;
}

/** C72T0: "epic" groups list rows by live epic membership (multi-appearance:
 * an issue with N live epics shows under every epic group).
 * C129T0: "assignees" groups list rows by assignee membership
 * (multi-appearance: an issue with N assignees shows under every
 * assignee group; issues with none land in Ungrouped, last).
 * C130T0: "labels" groups list rows by label membership
 * (multi-appearance: an issue with N labels shows under every
 * label group; issues with none land in Ungrouped, last).
 * C131T0: "modules" groups list rows by module membership
 * (multi-appearance: an issue with N modules shows under every
 * module group; issues with none land in Ungrouped, last).
 * Modules carry no color — module group headers render name +
 * count with no color dot.
 * C132T1: "cycles" groups list rows by displayed cycle
 * (SINGLE-appearance: an issue carries exactly one displayed cycle
 * or none; issues with none land in Ungrouped, last).
 * Cycles carry no color — cycle group headers render name +
 * count with no color dot.
 * C133T1: "releases" groups list rows by displayed release
 * (SINGLE-appearance: an issue carries exactly one displayed release
 * or none; issues with none land in Ungrouped, last).
 * Releases carry no color — release group headers render name +
 * count with no color dot.
 * C134T1: "createdBy" groups list rows by creator (SINGLE-appearance:
 * an issue carries exactly one creator, Issue.created_by; creators
 * with no workspace member collapse into ONE "Unknown" group, last,
 * hidden when empty). Creator groups carry no color — headers render
 * name + count with no color dot. The creator NAME does not ride the
 * row: it resolves from the workspace members lookup (see
 * groupByCreatedBy.ts), never from a raw user id.
 * C135T1: "stateGroups" groups list rows by state group
 * (SINGLE-appearance: an issue carries exactly one state_id, and
 * every state carries exactly one group; the group resolves from a
 * state-by-id lookup, see groupByStateGroup.ts). The canonical six
 * groups emit in backend-fixed order (triage, backlog, unstarted,
 * started, completed, cancelled); states with no loaded state
 * collapse into ONE "Unknown" group, last, hidden when empty.
 * State-group headers render name + count with no color dot and
 * no breakdown bar. A group holds several states, so cross-group
 * drops have no single target state: headers are NOT drop targets
 * (no move pipeline), while within-group reorder by sort_order
 * only is allowed. */
export type GroupBy =
  | "state"
  | "priority"
  | "epic"
  | "assignees"
  | "labels"
  | "modules"
  | "cycles"
  | "releases"
  | "createdBy"
  | "stateGroups"
  | "none";

/** C123T0: second grouping dimension inside each primary group. Mirrors
 *  the primary dimensions; "none" is the default (opt-in, Plane-style). */
export type SubGroupBy =
  | "none"
  | "state"
  | "priority"
  | "epic"
  | "assignees"
  | "labels";

/** Whitelisted ?order_by= values the backend accepts (internal/service/issue.go). */
export type OrderBy =
  | "-updated_at"
  | "updated_at"
  | "-created_at"
  | "created_at"
  | "-priority"
  | "priority"
  | "sequence_id"
  | "-sequence_id"
  | "sort_order";

export interface DisplaySettings {
  fields: DisplayFields;
  groupBy: GroupBy;
  /** C123T0: second grouping dimension inside each primary group.
   *  The effective value never equals groupBy — see
   *  effectiveSubGroupBy in subGroup.ts. */
  subGroupBy: SubGroupBy;
  orderBy: OrderBy;
  showEmptyGroups: boolean;
  /** C122T1: board nests direct subtasks under their parent card
   *  (flat child cards hidden while nested). Default TRUE: Plane
   *  shows sub-items nested by default, and nothing a user sees
   *  disappears — children move under parents, same content. */
  showSubWorkItems: boolean;
}

export const DEFAULT_DISPLAY_SETTINGS: DisplaySettings = {
  fields: {
    state: true,
    priority: true,
    labels: true,
    assignees: true,
    dates: true,
    id: true,
    subitems: true,
    modules: true,
    cycle: true,
    release: true,
    attachments: true,
    links: true,
    dependencies: true,
  },
  groupBy: "state",
  subGroupBy: "none",
  orderBy: "-updated_at",
  showEmptyGroups: true,
  showSubWorkItems: true,
};

const GROUP_BYS: GroupBy[] = [
  "state",
  "priority",
  "epic",
  "assignees",
  "labels",
  "modules",
  "cycles",
  "releases",
  "createdBy",
  "stateGroups",
  "none",
];
const SUB_GROUP_BYS: SubGroupBy[] = [
  "none",
  "state",
  "priority",
  "epic",
  "assignees",
  "labels",
];
const ORDER_BYS: OrderBy[] = [
  "-updated_at",
  "updated_at",
  "-created_at",
  "created_at",
  "-priority",
  "priority",
  "sequence_id",
  "-sequence_id",
  "sort_order",
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
      // C123T0: pre-existing stored settings gain the key as "none" —
      // no migration, no reset (C121/C122 precedent). Unknown values
      // also fall back to "none".
      subGroupBy: SUB_GROUP_BYS.includes(p.subGroupBy as SubGroupBy)
        ? (p.subGroupBy as SubGroupBy)
        : DEFAULT_DISPLAY_SETTINGS.subGroupBy,
      orderBy: ORDER_BYS.includes(p.orderBy as OrderBy)
        ? (p.orderBy as OrderBy)
        : DEFAULT_DISPLAY_SETTINGS.orderBy,
      showEmptyGroups:
        typeof p.showEmptyGroups === "boolean"
          ? p.showEmptyGroups
          : DEFAULT_DISPLAY_SETTINGS.showEmptyGroups,
      // C122T1: pre-existing stored settings gain the key as true —
      // no migration, no reset (C121 precedent).
      showSubWorkItems:
        typeof p.showSubWorkItems === "boolean"
          ? p.showSubWorkItems
          : DEFAULT_DISPLAY_SETTINGS.showSubWorkItems,
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
