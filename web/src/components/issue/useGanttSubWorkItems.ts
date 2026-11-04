import { useCallback, useState } from "react";

/**
 * C145T3: per-project "Show sub-work items" toggle for the Gantt view,
 * persisted to its OWN localStorage key (the C143 spreadsheet pattern,
 * separate from useDisplaySettings). Default ON, matching the list/board
 * default (DEFAULT_DISPLAY_SETTINGS.showSubWorkItems is true) — and
 * deliberately NOT shared with useDisplaySettings: that hook is
 * per-PROJECT, not per-view, so sharing it would flip list/board
 * rendering from the Gantt page, a cross-view side effect, not parity.
 *
 * Stored shape is a bare JSON boolean; anything else (absent,
 * corrupt, wrong type) falls back to the default ON.
 */
function storageKey(slug: string, identifier: string): string {
  return `glance:gantt-sub-work-items:${slug}:${identifier}`;
}

function load(slug: string, identifier: string): boolean {
  try {
    const raw = localStorage.getItem(storageKey(slug, identifier));
    if (!raw) return true;
    const parsed: unknown = JSON.parse(raw);
    return typeof parsed === "boolean" ? parsed : true;
  } catch {
    return true;
  }
}

export function useGanttSubWorkItems(slug: string, identifier: string) {
  const key = `${slug}\0${identifier}`;
  // Adjust-during-render keeps the toggle in sync when the route params
  // change without a remount (project switcher) — no effect needed.
  const [cached, setCached] = useState(() => ({
    key,
    show: load(slug, identifier),
  }));
  if (cached.key !== key) {
    setCached({ key, show: load(slug, identifier) });
  }
  const showSubWorkItems = cached.show;

  const toggle = useCallback(() => {
    setCached((prev) => {
      const next = !prev.show;
      try {
        localStorage.setItem(
          storageKey(slug, identifier),
          JSON.stringify(next),
        );
      } catch {
        // Storage full / private mode — still applies for the session.
      }
      return { ...prev, show: next };
    });
  }, [slug, identifier]);

  return { showSubWorkItems, toggle };
}
