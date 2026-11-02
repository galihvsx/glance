// @vitest-environment jsdom
// C121T1: DisplayFields gains 8 new toggles (all default true) and the
// load() merge gives old stored 5-key settings the new keys as true —
// no migration, no reset.

import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import {
  DEFAULT_DISPLAY_SETTINGS,
  useDisplaySettings,
  type DisplayFields,
} from "./useDisplaySettings";

const KEY = "glance:display:acme:ENG";

const NEW_KEYS: (keyof DisplayFields)[] = [
  "id",
  "subitems",
  "modules",
  "cycle",
  "release",
  "attachments",
  "links",
  "dependencies",
];

beforeEach(() => {
  localStorage.clear();
});

describe("useDisplaySettings (C121T1)", () => {
  it("defaults all 13 fields to true", () => {
    const { result } = renderHook(() => useDisplaySettings("acme", "ENG"));
    expect(Object.keys(result.current.settings.fields)).toHaveLength(13);
    for (const k of Object.keys(
      DEFAULT_DISPLAY_SETTINGS.fields,
    ) as (keyof DisplayFields)[]) {
      expect(result.current.settings.fields[k]).toBe(true);
    }
  });

  it("gives old stored 5-key settings the 8 new keys as true", () => {
    localStorage.setItem(
      KEY,
      JSON.stringify({
        fields: {
          state: false,
          priority: true,
          labels: false,
          assignees: true,
          dates: false,
        },
        groupBy: "state",
        orderBy: "-updated_at",
        showEmptyGroups: true,
      }),
    );
    const { result } = renderHook(() => useDisplaySettings("acme", "ENG"));
    // Stored values survive…
    expect(result.current.settings.fields.state).toBe(false);
    expect(result.current.settings.fields.labels).toBe(false);
    expect(result.current.settings.fields.dates).toBe(false);
    // …and every new key defaults to true.
    for (const k of NEW_KEYS) {
      expect(result.current.settings.fields[k]).toBe(true);
    }
  });

  it("keeps a stored new-key false (a user-switched-off field stays off)", () => {
    localStorage.setItem(
      KEY,
      JSON.stringify({
        fields: {
          state: true,
          priority: true,
          labels: true,
          assignees: true,
          dates: true,
          id: false,
          attachments: false,
        },
        groupBy: "state",
        orderBy: "-updated_at",
        showEmptyGroups: true,
      }),
    );
    const { result } = renderHook(() => useDisplaySettings("acme", "ENG"));
    expect(result.current.settings.fields.id).toBe(false);
    expect(result.current.settings.fields.attachments).toBe(false);
    expect(result.current.settings.fields.subitems).toBe(true);
  });

  it("persists a new-field toggle via updateFields", () => {
    const { result } = renderHook(() => useDisplaySettings("acme", "ENG"));
    act(() => {
      result.current.updateFields({ cycle: false });
    });
    expect(result.current.settings.fields.cycle).toBe(false);
    const stored = JSON.parse(
      localStorage.getItem(KEY) ?? "{}",
    ) as { fields: DisplayFields };
    expect(stored.fields.cycle).toBe(false);
    expect(stored.fields.release).toBe(true);
  });
});
