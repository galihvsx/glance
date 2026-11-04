import { useCallback, useState } from "react";
import type {
  GroupBy,
  SubGroupBy,
} from "./useDisplaySettings";

/**
 * C147T1: per-project spreadsheet grouping settings, persisted to
 * their OWN localStorage key (the useSpreadsheetSubWorkItems
 * pattern, never useDisplaySettings: that hook is per-PROJECT, not
 * per-view, so sharing it would flip list/board rendering from the
 * spreadsheet page).
 *
 * Stored shape is a JSON object { groupBy, subGroupBy }; defaults
 * are none/none, and anything else (absent, corrupt, wrong type,
 * unknown dimension) falls back per-field to "none" — so a stored
 * value from a newer/older dimension set never breaks the grid.
 *
 * T1 wires groupBy only; subGroupBy stays "none" until T2 (the
 * field exists now so T2 touches no storage shape).
 */
function storageKey(slug: string, identifier: string): string {
  return `glance:spreadsheet-grouping:${slug}:${identifier}`;
}

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
  "createdBy",
  "stateGroups",
  "cycles",
  "modules",
  "releases",
];

export interface SpreadsheetGrouping {
  groupBy: GroupBy;
  subGroupBy: SubGroupBy;
}

const DEFAULT_GROUPING: SpreadsheetGrouping = {
  groupBy: "none",
  subGroupBy: "none",
};

function load(slug: string, identifier: string): SpreadsheetGrouping {
  try {
    const raw = localStorage.getItem(storageKey(slug, identifier));
    if (!raw) return DEFAULT_GROUPING;
    const p = JSON.parse(raw) as Partial<SpreadsheetGrouping>;
    return {
      groupBy: GROUP_BYS.includes(p.groupBy as GroupBy)
        ? (p.groupBy as GroupBy)
        : DEFAULT_GROUPING.groupBy,
      subGroupBy: SUB_GROUP_BYS.includes(p.subGroupBy as SubGroupBy)
        ? (p.subGroupBy as SubGroupBy)
        : DEFAULT_GROUPING.subGroupBy,
    };
  } catch {
    return DEFAULT_GROUPING;
  }
}

export function useSpreadsheetGrouping(slug: string, identifier: string) {
  const key = `${slug}\0${identifier}`;
  // Adjust-during-render keeps the settings in sync when the route
  // params change without a remount (project switcher) — no effect
  // needed. Same pattern as useSpreadsheetSubWorkItems.
  const [cached, setCached] = useState(() => ({
    key,
    grouping: load(slug, identifier),
  }));
  if (cached.key !== key) {
    setCached({ key, grouping: load(slug, identifier) });
  }
  const { groupBy, subGroupBy } = cached.grouping;

  const setGrouping = useCallback(
    (patch: Partial<SpreadsheetGrouping>) => {
      setCached((prev) => {
        const next = { ...prev.grouping, ...patch };
        try {
          localStorage.setItem(
            storageKey(slug, identifier),
            JSON.stringify(next),
          );
        } catch {
          // Storage full / private mode — still applies for the session.
        }
        return { ...prev, grouping: next };
      });
    },
    [slug, identifier],
  );

  const setGroupBy = useCallback(
    (next: GroupBy) => setGrouping({ groupBy: next }),
    [setGrouping],
  );

  const setSubGroupBy = useCallback(
    (next: SubGroupBy) => setGrouping({ subGroupBy: next }),
    [setGrouping],
  );

  const reset = useCallback(() => {
    setCached((prev) => ({ ...prev, grouping: DEFAULT_GROUPING }));
    try {
      localStorage.removeItem(storageKey(slug, identifier));
    } catch {
      // Ignore — in-memory state is already reset.
    }
  }, [slug, identifier]);

  return { groupBy, subGroupBy, setGroupBy, setSubGroupBy, reset };
}
