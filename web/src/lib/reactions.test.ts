// Unit tests for the shared reaction helpers (lib/reactions.ts).
// These pin the client-side optimistic mirror so it never drifts from
// the issue/comment reaction POST/DELETE contract (C67T1).

import { describe, expect, it } from "vitest";
import { normalizeGroups, sortGroups, toggleGroup, toggleCommentNode } from "./reactions";
import type { CommentNode, ReactionGroup } from "./types";

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

  it("does not mutate the input array", () => {
    const before = [g("👍", 1, false), g("🎉", 3, false)];
    const out = sortGroups(before);
    expect(out).not.toBe(before);
    expect(before.map((x) => x.emoji)).toEqual(["👍", "🎉"]);
  });
});

describe("toggleGroup", () => {
  it("adds a fresh emoji as count 1 reacted", () => {
    expect(toggleGroup([], "🎉", true)).toEqual([g("🎉", 1, true)]);
  });

  it("increments an existing group and marks reacted", () => {
    expect(toggleGroup([g("🎉", 2, false)], "🎉", true)).toEqual([
      g("🎉", 3, true),
    ]);
  });

  it("add is a no-op when already reacted", () => {
    const before = [g("🎉", 2, true)];
    expect(toggleGroup(before, "🎉", true)).toBe(before);
  });

  it("remove decrements and clears reacted", () => {
    expect(toggleGroup([g("🎉", 3, true)], "🎉", false)).toEqual([
      g("🎉", 2, false),
    ]);
  });

  it("remove drops the group at zero", () => {
    expect(toggleGroup([g("🎉", 1, true), g("👍", 2, false)], "🎉", false)).toEqual([
      g("👍", 2, false),
    ]);
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

describe("normalizeGroups", () => {
  it("passes well-formed groups through", () => {
    expect(normalizeGroups([g("🎉", 2, true), g("👍", 1, false)])).toEqual([
      g("🎉", 2, true),
      g("👍", 1, false),
    ]);
  });

  it("returns [] for non-array payloads (null, objects, strings)", () => {
    expect(normalizeGroups(null)).toEqual([]);
    expect(normalizeGroups(undefined)).toEqual([]);
    expect(normalizeGroups("reactions")).toEqual([]);
    expect(normalizeGroups({ emoji: "🎉" })).toEqual([]);
  });

  it("drops malformed entries instead of crashing the row", () => {
    const junk = [
      null,
      "🎉",
      42,
      { emoji: "🎉" }, // missing count
      { emoji: "", count: 2 }, // empty emoji
      { count: 2, reacted: true }, // missing emoji
      { emoji: "🎉", count: 0, reacted: true }, // zero count
      { emoji: "🎉", count: -1, reacted: true }, // negative count
      { emoji: "🎉", count: NaN, reacted: true },
      { emoji: "🎉", count: "two" },
      g("👍", 1, false), // the one good entry
    ];
    expect(normalizeGroups(junk)).toEqual([g("👍", 1, false)]);
  });

  it("defaults reacted to false when the flag is not a boolean", () => {
    expect(normalizeGroups([{ emoji: "🎉", count: 2, reacted: "yes" }])).toEqual([
      g("🎉", 2, false),
    ]);
  });

  it("floors fractional counts", () => {
    expect(normalizeGroups([{ emoji: "🎉", count: 2.9, reacted: false }])).toEqual([
      g("🎉", 2, false),
    ]);
  });
});

function node(id: string, over: Partial<CommentNode> = {}): CommentNode {
  return {
    id,
    issue_id: "issue-1",
    actor: { id: "u", name: "U", email: "u@x.com" },
    content: null,
    created_at: "2026-10-19T00:00:00Z",
    updated_at: "2026-10-19T00:00:00Z",
    reactions: [],
    replies: [],
    ...over,
  };
}

describe("toggleCommentNode", () => {
  it("toggles the matching root node only", () => {
    const roots = [
      node("a", { reactions: [g("🎉", 1, false)] }),
      node("b", { reactions: [g("👍", 2, false)] }),
    ];
    const out = toggleCommentNode(roots, "b", "👍", true);
    expect(out[0].reactions).toEqual([g("🎉", 1, false)]);
    expect(out[1].reactions).toEqual([g("👍", 3, true)]);
  });

  it("toggles a nested reply node", () => {
    const roots = [
      node("a", {
        replies: [node("a-1", { reactions: [g("🎉", 1, true)] })],
      }),
    ];
    const out = toggleCommentNode(roots, "a-1", "🎉", false);
    expect(out[0].replies[0].reactions).toEqual([]);
  });

  it("returns the input unchanged when the id is unknown", () => {
    const roots = [node("a")];
    expect(toggleCommentNode(roots, "zzz", "🎉", true)).toBe(roots);
  });
});
