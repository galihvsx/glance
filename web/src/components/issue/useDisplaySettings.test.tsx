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

describe("useDisplaySettings showSubWorkItems (C122T1)", () => {
  it("defaults showSubWorkItems to true", () => {
    const { result } = renderHook(() => useDisplaySettings("acme", "ENG"));
    expect(result.current.settings.showSubWorkItems).toBe(true);
    expect(DEFAULT_DISPLAY_SETTINGS.showSubWorkItems).toBe(true);
  });

  it("gives pre-existing stored settings the key as true (no migration)", () => {
    localStorage.setItem(
      KEY,
      JSON.stringify({
        fields: {
          state: true,
          priority: true,
          labels: true,
          assignees: true,
          dates: true,
        },
        groupBy: "state",
        orderBy: "-updated_at",
        showEmptyGroups: true,
      }),
    );
    const { result } = renderHook(() => useDisplaySettings("acme", "ENG"));
    expect(result.current.settings.showSubWorkItems).toBe(true);
  });

  it("keeps a stored false (a user-switched-off toggle stays off)", () => {
    localStorage.setItem(
      KEY,
      JSON.stringify({ showSubWorkItems: false }),
    );
    const { result } = renderHook(() => useDisplaySettings("acme", "ENG"));
    expect(result.current.settings.showSubWorkItems).toBe(false);
  });

  it("persists the toggle via update", () => {
    const { result } = renderHook(() => useDisplaySettings("acme", "ENG"));
    act(() => {
      result.current.update({ showSubWorkItems: false });
    });
    expect(result.current.settings.showSubWorkItems).toBe(false);
    const stored = JSON.parse(localStorage.getItem(KEY) ?? "{}") as {
      showSubWorkItems: boolean;
    };
    expect(stored.showSubWorkItems).toBe(false);
  });
});

describe("useDisplaySettings subGroupBy (C123T0)", () => {
  it("defaults subGroupBy to none", () => {
    const { result } = renderHook(() => useDisplaySettings("acme", "ENG"));
    expect(result.current.settings.subGroupBy).toBe("none");
    expect(DEFAULT_DISPLAY_SETTINGS.subGroupBy).toBe("none");
  });

  it("keeps a stored modules groupBy value (C131T0 whitelist)", () => {
    localStorage.setItem(KEY, JSON.stringify({ groupBy: "modules" }));
    const { result } = renderHook(() => useDisplaySettings("acme", "ENG"));
    expect(result.current.settings.groupBy).toBe("modules");
  });

  it("keeps a stored labels groupBy value (C130T0 whitelist)", () => {
    localStorage.setItem(KEY, JSON.stringify({ groupBy: "labels" }));
    const { result } = renderHook(() => useDisplaySettings("acme", "ENG"));
    expect(result.current.settings.groupBy).toBe("labels");
  });

  it("keeps a stored stateGroups groupBy value (C135T1 whitelist)", () => {
    localStorage.setItem(KEY, JSON.stringify({ groupBy: "stateGroups" }));
    const { result } = renderHook(() => useDisplaySettings("acme", "ENG"));
    expect(result.current.settings.groupBy).toBe("stateGroups");
  });

  it("gives pre-existing stored settings the key as none (no migration)", () => {
    localStorage.setItem(
      KEY,
      JSON.stringify({
        groupBy: "state",
        orderBy: "-updated_at",
        showEmptyGroups: true,
      }),
    );
    const { result } = renderHook(() => useDisplaySettings("acme", "ENG"));
    expect(result.current.settings.subGroupBy).toBe("none");
  });

  it("keeps a stored valid sub-group value", () => {
    localStorage.setItem(
      KEY,
      JSON.stringify({ groupBy: "state", subGroupBy: "priority" }),
    );
    const { result } = renderHook(() => useDisplaySettings("acme", "ENG"));
    expect(result.current.settings.subGroupBy).toBe("priority");
  });

  it("keeps a stored assignees sub-group value (C136T1 whitelist)", () => {
    localStorage.setItem(
      KEY,
      JSON.stringify({ groupBy: "state", subGroupBy: "assignees" }),
    );
    const { result } = renderHook(() => useDisplaySettings("acme", "ENG"));
    expect(result.current.settings.subGroupBy).toBe("assignees");
  });

  it("keeps a stored labels sub-group value (C137T1 whitelist)", () => {
    localStorage.setItem(
      KEY,
      JSON.stringify({ groupBy: "state", subGroupBy: "labels" }),
    );
    const { result } = renderHook(() => useDisplaySettings("acme", "ENG"));
    expect(result.current.settings.subGroupBy).toBe("labels");
  });

  it("keeps a stored createdBy sub-group value (C138T1 whitelist)", () => {
    localStorage.setItem(
      KEY,
      JSON.stringify({ groupBy: "state", subGroupBy: "createdBy" }),
    );
    const { result } = renderHook(() => useDisplaySettings("acme", "ENG"));
    expect(result.current.settings.subGroupBy).toBe("createdBy");
  });

  it("falls back to none for the singular label stored value (C137T1 literal)", () => {
    localStorage.setItem(
      KEY,
      JSON.stringify({ groupBy: "state", subGroupBy: "label" }),
    );
    const { result } = renderHook(() => useDisplaySettings("acme", "ENG"));
    expect(result.current.settings.subGroupBy).toBe("none");
  });

  it("falls back to none for an unknown stored value", () => {
    localStorage.setItem(
      KEY,
      JSON.stringify({ groupBy: "state", subGroupBy: "assignee" }),
    );
    const { result } = renderHook(() => useDisplaySettings("acme", "ENG"));
    expect(result.current.settings.subGroupBy).toBe("none");
  });

  it("persists the sub-group via update", () => {
    const { result } = renderHook(() => useDisplaySettings("acme", "ENG"));
    act(() => {
      result.current.update({ subGroupBy: "epic" });
    });
    expect(result.current.settings.subGroupBy).toBe("epic");
    const stored = JSON.parse(localStorage.getItem(KEY) ?? "{}") as {
      subGroupBy: string;
    };
    expect(stored.subGroupBy).toBe("epic");
  });
});

describe("useDisplaySettings orderBy sort_order (C125T0)", () => {
  it("keeps a stored sort_order value (Manual rank survives load)", () => {
    localStorage.setItem(
      KEY,
      JSON.stringify({ groupBy: "state", orderBy: "sort_order" }),
    );
    const { result } = renderHook(() => useDisplaySettings("acme", "ENG"));
    expect(result.current.settings.orderBy).toBe("sort_order");
  });

  it("falls back to the default for an unknown stored order value", () => {
    localStorage.setItem(
      KEY,
      JSON.stringify({ groupBy: "state", orderBy: "bogus" }),
    );
    const { result } = renderHook(() => useDisplaySettings("acme", "ENG"));
    expect(result.current.settings.orderBy).toBe(
      DEFAULT_DISPLAY_SETTINGS.orderBy,
    );
  });

  it("persists sort_order via update and a fresh hook loads it back", () => {
    const { result } = renderHook(() => useDisplaySettings("acme", "ENG"));
    act(() => {
      result.current.update({ orderBy: "sort_order" });
    });
    expect(result.current.settings.orderBy).toBe("sort_order");
    const stored = JSON.parse(localStorage.getItem(KEY) ?? "{}") as {
      orderBy: string;
    };
    expect(stored.orderBy).toBe("sort_order");
    // A fresh hook instance reads the stored value back (load round-trip).
    const { result: reloaded } = renderHook(() =>
      useDisplaySettings("acme", "ENG"),
    );
    expect(reloaded.current.settings.orderBy).toBe("sort_order");
  });
});
