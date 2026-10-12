// Unit tests for the optimistic page-reaction grouping (C24T3).
// The server is the authority; these pin the client's optimistic
// mirror so it never drifts from the POST/DELETE contract.

import { describe, expect, it } from "vitest";
import { sortGroups, toggleGroup } from "./PageReactions";
import type { ReactionGroup } from "../../lib/types";

const g = (emoji: string, count: number, reacted: boolean): ReactionGroup => ({
  emoji,
  count,
  reacted,
});

describe("sortGroups", () => {
  it("orders by count desc, then emoji code-unit order", () => {
    // '👍' (U+1F44D) < '🔥' (U+1F525) by UTF-16 code unit.
    const out = sortGroups([g("👍", 2, false), g("🎉", 5, false), g("🔥", 2, false)]);
    expect(out.map((x) => x.emoji)).toEqual(["🎉", "👍", "🔥"]);
  });
});

describe("toggleGroup", () => {
  it("adds a fresh emoji as count 1 reacted", () => {
    const out = toggleGroup([], "🎉", true);
    expect(out).toEqual([g("🎉", 1, true)]);
  });

  it("increments an existing group and marks reacted", () => {
    const out = toggleGroup([g("🎉", 2, false)], "🎉", true);
    expect(out).toEqual([g("🎉", 3, true)]);
  });

  it("add is a no-op when already reacted", () => {
    const before = [g("🎉", 2, true)];
    expect(toggleGroup(before, "🎉", true)).toBe(before);
  });

  it("remove decrements and clears reacted", () => {
    const out = toggleGroup([g("🎉", 3, true)], "🎉", false);
    expect(out).toEqual([g("🎉", 2, false)]);
  });

  it("remove drops the group at zero", () => {
    const out = toggleGroup([g("🎉", 1, true), g("👍", 2, false)], "🎉", false);
    expect(out).toEqual([g("👍", 2, false)]);
  });

  it("remove is a no-op when not reacted", () => {
    const before = [g("🎉", 2, false)];
    expect(toggleGroup(before, "🎉", false)).toBe(before);
  });

  it("keeps server ordering after a toggle", () => {
    const out = toggleGroup([g("👍", 3, false), g("🎉", 1, false)], "🎉", true);
    expect(out.map((x) => x.emoji)).toEqual(["👍", "🎉"]);
    expect(out[1]).toEqual(g("🎉", 2, true));
  });
});
