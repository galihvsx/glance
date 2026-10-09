import { describe, expect, it } from "vitest";
import {
  isTypingTarget,
  resolveKeyPress,
  SHORTCUT_CONTEXTS,
  SHORTCUT_REGISTRY,
  type ShortcutAction,
  type ShortcutContext,
} from "./shortcuts";

function fakeEl(tagName: string, isContentEditable = false): EventTarget {
  return { tagName, isContentEditable } as unknown as EventTarget;
}

describe("isTypingTarget", () => {
  it("detects text-entry elements", () => {
    expect(isTypingTarget(fakeEl("INPUT"))).toBe(true);
    expect(isTypingTarget(fakeEl("TEXTAREA"))).toBe(true);
    expect(isTypingTarget(fakeEl("SELECT"))).toBe(true);
    // tiptap and other rich editors
    expect(isTypingTarget(fakeEl("DIV", true))).toBe(true);
  });
  it("ignores everything else", () => {
    expect(isTypingTarget(fakeEl("DIV"))).toBe(false);
    expect(isTypingTarget(fakeEl("BODY"))).toBe(false);
    expect(isTypingTarget(fakeEl("BUTTON"))).toBe(false);
    expect(isTypingTarget(null)).toBe(false);
    expect(isTypingTarget({} as EventTarget)).toBe(false);
  });
});

describe("resolveKeyPress", () => {
  it("maps single keys to actions", () => {
    expect(resolveKeyPress("c", null)).toEqual({
      action: "new-issue",
      pendingPrefix: null,
    });
    expect(resolveKeyPress("/", null)).toEqual({
      action: "focus-search",
      pendingPrefix: null,
    });
    expect(resolveKeyPress("j", null)).toEqual({
      action: "next-item",
      pendingPrefix: null,
    });
    expect(resolveKeyPress("k", null)).toEqual({
      action: "prev-item",
      pendingPrefix: null,
    });
    expect(resolveKeyPress("?", null)).toEqual({
      action: "open-cheatsheet",
      pendingPrefix: null,
    });
  });

  it("ignores unknown keys", () => {
    expect(resolveKeyPress("x", null)).toEqual({
      action: null,
      pendingPrefix: null,
    });
    expect(resolveKeyPress("Enter", null)).toEqual({
      action: null,
      pendingPrefix: null,
    });
  });

  it("starts a g-chord on 'g'", () => {
    expect(resolveKeyPress("g", null)).toEqual({
      action: null,
      pendingPrefix: "g",
    });
  });

  it("resolves g-chords case-insensitively", () => {
    expect(resolveKeyPress("m", "g")).toEqual({
      action: "goto-mywork",
      pendingPrefix: null,
    });
    expect(resolveKeyPress("H", "g")).toEqual({
      action: "goto-home",
      pendingPrefix: null,
    });
    expect(resolveKeyPress("n", "g")).toEqual({
      action: "open-notifications",
      pendingPrefix: null,
    });
    expect(resolveKeyPress("N", "g")).toEqual({
      action: "open-notifications",
      pendingPrefix: null,
    });
  });

  it("does not map plain b — toggle-sidebar is a mod chord only", () => {
    expect(resolveKeyPress("b", null)).toEqual({
      action: null,
      pendingPrefix: null,
    });
    expect(resolveKeyPress("B", null)).toEqual({
      action: null,
      pendingPrefix: null,
    });
  });

  it("falls through to normal keys when the chord is unknown", () => {
    // g then c: not a chord, so c still creates an issue.
    expect(resolveKeyPress("c", "g")).toEqual({
      action: "new-issue",
      pendingPrefix: null,
    });
    // g then g: re-arms the prefix.
    expect(resolveKeyPress("g", "g")).toEqual({
      action: null,
      pendingPrefix: "g",
    });
  });
});

describe("SHORTCUT_REGISTRY", () => {
  const actions: ShortcutAction[] = [
    "new-issue",
    "focus-search",
    "next-item",
    "prev-item",
    "open-selected",
    "toggle-palette",
    "open-cheatsheet",
    "close-topmost",
    "goto-home",
    "goto-mywork",
    "toggle-sidebar",
    "open-notifications",
  ];

  it("documents every shortcut action exactly once", () => {
    const seen = new Set<string>();
    for (const def of SHORTCUT_REGISTRY) {
      expect(seen.has(def.action)).toBe(false);
      seen.add(def.action);
    }
    expect([...seen].sort()).toEqual([...actions].sort());
  });

  it("has non-empty keys, description and contexts for every entry", () => {
    for (const def of SHORTCUT_REGISTRY) {
      expect(def.keys.length).toBeGreaterThan(0);
      expect(def.keys.every((k) => k.trim().length > 0)).toBe(true);
      expect(def.description.trim().length).toBeGreaterThan(0);
      expect(def.contexts.length).toBeGreaterThan(0);
    }
  });

  it("only uses known contexts", () => {
    const known = new Set<ShortcutContext>(SHORTCUT_CONTEXTS);
    for (const def of SHORTCUT_REGISTRY) {
      for (const ctx of def.contexts) {
        expect(known.has(ctx)).toBe(true);
      }
    }
  });

  it("covers every context group with at least one shortcut", () => {
    for (const ctx of SHORTCUT_CONTEXTS) {
      const defs = SHORTCUT_REGISTRY.filter((d) =>
        d.contexts.includes(ctx),
      );
      expect(defs.length, `context "${ctx}"`).toBeGreaterThan(0);
    }
  });

  it("marks chords with more than one key", () => {
    for (const def of SHORTCUT_REGISTRY) {
      if (def.chord) expect(def.keys.length).toBeGreaterThan(1);
      else expect(def.keys.length).toBeGreaterThanOrEqual(1);
    }
  });

  it("documents the app-shell keymap rows", () => {
    const byAction = new Map(SHORTCUT_REGISTRY.map((d) => [d.action, d]));
    const toggleSidebar = byAction.get("toggle-sidebar");
    expect(toggleSidebar?.keys).toEqual(["Ctrl/⌘", "B"]);
    expect(toggleSidebar?.chord).toBeFalsy();
    expect(toggleSidebar?.contexts).toContain("Global");
    const openNotifications = byAction.get("open-notifications");
    expect(openNotifications?.keys).toEqual(["g", "n"]);
    expect(openNotifications?.chord).toBe(true);
    expect(openNotifications?.contexts).toContain("Global");
  });
});
