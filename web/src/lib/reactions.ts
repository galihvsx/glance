// Shared reaction helpers (C67T1), extracted from PageReactions so the
// issue- and comment-level surfaces reuse one copy of the anatomy.
//
// These pin the client's optimistic mirror so it never drifts from the
// issue/comment reaction POST/DELETE contract:
//   POST .../reactions           with JSON body {emoji}   (add)
//   DELETE .../reactions?emoji=  (remove)
// The server is the authority; toggles refetch/invalidate on settle.

import type { CommentNode, ReactionGroup } from "./types";

/** Server order: count DESC, then emoji ASC (UTF-16 code-unit order). */
export function sortGroups(groups: ReactionGroup[]): ReactionGroup[] {
  // The emoji tiebreak uses code-unit order (deterministic; the DB
  // collation may differ in exotic cases, but ties among equal counts
  // are cosmetic and the next refetch settles them to the server truth).
  return [...groups].sort(
    (a, b) =>
      b.count - a.count ||
      (a.emoji < b.emoji ? -1 : a.emoji > b.emoji ? 1 : 0),
  );
}

/** Apply one toggle optimistically, mirroring the server's grouping. */
export function toggleGroup(
  groups: ReactionGroup[],
  emoji: string,
  add: boolean,
): ReactionGroup[] {
  const existing = groups.find((g) => g.emoji === emoji);
  if (add) {
    if (existing?.reacted) return groups;
    if (existing) {
      return sortGroups(
        groups.map((g) =>
          g.emoji === emoji
            ? { ...g, count: g.count + 1, reacted: true }
            : g,
        ),
      );
    }
    return sortGroups([...groups, { emoji, count: 1, reacted: true }]);
  }
  if (!existing?.reacted) return groups;
  const next = groups
    .map((g) =>
      g.emoji === emoji
        ? { ...g, count: g.count - 1, reacted: false }
        : g,
    )
    .filter((g) => g.count > 0);
  return sortGroups(next);
}

/**
 * Defensive normalization of a reactions payload: the backend
 * guarantees `reactions: []` (never null) on comments, but the row must
 * never crash on a malformed entry — drop it instead. `reacted` is
 * optional in practice (older pins omit it); anything non-boolean reads
 * as false so a bad flag can never fake the caller's own reaction.
 */
export function normalizeGroups(input: unknown): ReactionGroup[] {
  if (!Array.isArray(input)) return [];
  const out: ReactionGroup[] = [];
  for (const raw of input) {
    if (raw === null || typeof raw !== "object") continue;
    const { emoji, count, reacted } = raw as {
      emoji?: unknown;
      count?: unknown;
      reacted?: unknown;
    };
    if (typeof emoji !== "string" || emoji.length === 0) continue;
    if (typeof count !== "number" || !Number.isFinite(count)) continue;
    const n = Math.floor(count);
    if (n <= 0) continue;
    out.push({ emoji, count: n, reacted: reacted === true });
  }
  return out;
}

/**
 * Apply one toggle to the comment node with `id` anywhere in the tree
 * (roots or nested replies), returning a new tree. Returns the input
 * unchanged (same reference) when no node matches.
 */
export function toggleCommentNode(
  roots: CommentNode[],
  id: string,
  emoji: string,
  add: boolean,
): CommentNode[] {
  let changed = false;
  const next = roots.map((node) => {
    if (node.id === id) {
      changed = true;
      return {
        ...node,
        reactions: toggleGroup(node.reactions ?? [], emoji, add),
      };
    }
    if (node.replies.length > 0) {
      const replies = toggleCommentNode(node.replies, id, emoji, add);
      if (replies !== node.replies) {
        changed = true;
        return { ...node, replies };
      }
    }
    return node;
  });
  return changed ? next : roots;
}
