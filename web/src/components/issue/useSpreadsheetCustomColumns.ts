import { useCallback, useState } from "react";

/**
 * C9T4: per-project spreadsheet custom-field column visibility, persisted
 * to localStorage. Default: every column OFF — the user opts in per field
 * in the spreadsheet's Display popover.
 *
 * Shape is a sparse map of enabled field ids only; ids of deleted fields
 * are ignored when resolving (see resolveVisibleCustomFields), and unknown
 * keys in stored JSON are dropped on load so one bad write can't pin a
 * phantom column.
 */
function storageKey(slug: string, identifier: string): string {
  return `glance:spreadsheet-cf:${slug}:${identifier}`;
}

function load(slug: string, identifier: string): Record<string, true> {
  try {
    const raw = localStorage.getItem(storageKey(slug, identifier));
    if (!raw) return {};
    const parsed: unknown = JSON.parse(raw);
    if (typeof parsed !== "object" || parsed === null) return {};
    return Object.fromEntries(
      Object.entries(parsed as Record<string, unknown>)
        .filter(([, v]) => v === true)
        .map(([k]) => [k, true as const]),
    );
  } catch {
    return {};
  }
}

export function useSpreadsheetCustomColumns(
  slug: string,
  identifier: string,
) {
  const key = `${slug}\0${identifier}`;
  // Adjust-during-render keeps visibility in sync when the route params
  // change without a remount (project switcher) — no effect needed.
  const [cached, setCached] = useState(() => ({
    key,
    visible: load(slug, identifier),
  }));
  if (cached.key !== key) {
    setCached({ key, visible: load(slug, identifier) });
  }
  const visible = cached.visible;

  const toggle = useCallback(
    (fieldId: string) => {
      setCached((prev) => {
        const next: Record<string, true> = { ...prev.visible };
        if (next[fieldId]) delete next[fieldId];
        else next[fieldId] = true;
        try {
          localStorage.setItem(storageKey(slug, identifier), JSON.stringify(next));
        } catch {
          // Storage full / private mode — still applies for the session.
        }
        return { ...prev, visible: next };
      });
    },
    [slug, identifier],
  );

  const reset = useCallback(() => {
    setCached((prev) => ({ ...prev, visible: {} }));
    try {
      localStorage.removeItem(storageKey(slug, identifier));
    } catch {
      // Ignore — in-memory state is already cleared.
    }
  }, [slug, identifier]);

  return { visible, toggle, reset };
}
