import { useCallback, useState } from "react";

/**
 * C145T4: per-project "Show sub-work items" toggle for the roadmap,
 * persisted to its OWN localStorage key — deliberately NOT shared
 * with useDisplaySettings (the C143 spreadsheet precedent): that
 * hook is per-PROJECT, not per-view, so sharing it would flip
 * list/board rendering from the roadmap page, a cross-view side
 * effect, not parity. Default ON, matching
 * DEFAULT_DISPLAY_SETTINGS.showSubWorkItems.
 *
 * Stored shape is a bare JSON boolean; anything else (absent,
 * corrupt, wrong type) falls back to the default ON.
 */
function storageKey(slug: string, identifier: string): string {
  return `glance:roadmap-sub-work-items:${slug}:${identifier}`;
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

export function useRoadmapSubWorkItems(slug: string, identifier: string) {
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
        localStorage.setItem(storageKey(slug, identifier), JSON.stringify(next));
      } catch {
        // Storage full / private mode — still applies for the session.
      }
      return { ...prev, show: next };
    });
  }, [slug, identifier]);

  const reset = useCallback(() => {
    setCached((prev) => ({ ...prev, show: true }));
    try {
      localStorage.removeItem(storageKey(slug, identifier));
    } catch {
      // Ignore — in-memory state is already reset.
    }
  }, [slug, identifier]);

  return { showSubWorkItems, toggle, reset };
}
