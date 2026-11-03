import type { InfiniteData } from "@tanstack/react-query";
import type { Issue, IssueListResult } from "../../lib/types";
import { stackDropSortOrder } from "../../lib/sortOrder";
import type { GroupBy, OrderBy } from "./useDisplaySettings";

/**
 * List-view drag-to-reorder pipeline (C126T0). The page component owns
 * transport (api + queryClient); this module owns the rules, so the
 * pipeline is unit-testable without dnd-kit or a rendered page.
 *
 * Enable rule (plan §Scope, C128T0, extended C129T0): drag exists ONLY when the visual
 * stack order equals a sort_order sequence — Manual rank order, visual
 * mode (no PQL ORDER BY winning server-side), and an EFFECTIVE
 * grouping of none, state, priority, epic, or assignees. The caller passes the
 * effective grouping, not the raw setting: a priority/state grouping
 * skipped by its single-value filter renders flat, so it counts as
 * flat here. Priority/epic/assignee grouping mixes states inside one stack
 * (and epic/assignee multi-appears rows), but the reorder pipeline is
 * stack-local and grouping-agnostic — the stack's rows are resolved
 * through the page's listDragMaps appearance → issue mapping, and a
 * reorder PATCHes only {sort_order}, never a group dimension — so
 * those groupings are admitted exactly like state. sort_order is
 * GLOBAL per issue (one column): a reorder in any stack changes
 * Manual-rank display everywhere that row appears; that is C126's
 * already-accepted semantics, not a per-group rank model (no such
 * model exists and this cycle adds none).
 */
export function isListDragEnabled(args: {
  orderBy: OrderBy;
  pqlMode: boolean;
  /** Effective primary grouping after the page's group-skip rules. */
  groupBy: GroupBy;
}): boolean {
  return (
    args.orderBy === "sort_order" &&
    !args.pqlMode &&
    (args.groupBy === "none" ||
      args.groupBy === "state" ||
      args.groupBy === "priority" ||
      args.groupBy === "epic" ||
      args.groupBy === "assignees")
  );
}

/**
 * Apply a stack's new order to the flat loaded list (C126T0). The
 * stack is a filtered view of the flat list, so the dragged row is
 * spliced right after its new stack predecessor (or before its new
 * stack successor when it lands first) — consecutive stack rows have
 * no same-stack rows between them in flat order, so the filtered stack
 * order comes out exactly as `orderedStackIds`. Rows outside the
 * stack keep their relative order. Returns the flat list unchanged
 * when the anchor row is missing (defensive: never lose a row).
 */
export function applyStackOrderToFlat(
  flat: Issue[],
  orderedStackIds: string[],
  draggedId: string,
): Issue[] {
  const pos = orderedStackIds.indexOf(draggedId);
  if (pos === -1) return flat;
  const without = flat.filter((i) => i.id !== draggedId);
  const dragged = flat.find((i) => i.id === draggedId);
  if (!dragged) return flat;
  let anchorIdx: number;
  if (pos === 0) {
    // Lands first: insert before the new successor in flat order.
    const succ = without.findIndex((i) => i.id === orderedStackIds[1]);
    anchorIdx = succ === -1 ? 0 : succ;
    return [
      ...without.slice(0, anchorIdx),
      dragged,
      ...without.slice(anchorIdx),
    ];
  }
  const pred = without.findIndex((i) => i.id === orderedStackIds[pos - 1]);
  anchorIdx = pred === -1 ? without.length : pred + 1;
  return [
    ...without.slice(0, anchorIdx),
    dragged,
    ...without.slice(anchorIdx),
  ];
}

/**
 * Redistribute a reordered flat list back into the infinite-query
 * pages, preserving each page's size and cursor (C126T0). The reorder
 * is optimistic-only — the invalidation right after the PATCH refetches
 * the server's page boundaries — so page sizes are kept, not cursors
 * recomputed. Returns the input unchanged when the id sets differ
 * (defensive: never drop or duplicate a row).
 */
export function reorderInfinitePages(
  data: InfiniteData<IssueListResult>,
  newFlat: Issue[],
): InfiniteData<IssueListResult> {
  const oldFlat = data.pages.flatMap((p) => p.results);
  if (oldFlat.length !== newFlat.length) return data;
  const oldIds = new Set(oldFlat.map((i) => i.id));
  if (!newFlat.every((i) => oldIds.has(i.id))) return data;
  let cursor = 0;
  const pages = data.pages.map((p) => {
    const results = newFlat.slice(cursor, cursor + p.results.length);
    cursor += p.results.length;
    return { ...p, results };
  });
  return { ...data, pages };
}

/** Transport + cache hooks the page wires to api/queryClient. */
export interface ListDropDeps {
  /** Current infinite-query snapshot (for the optimistic revert). */
  getData: () => InfiniteData<IssueListResult> | undefined;
  setData: (data: InfiniteData<IssueListResult>) => void;
  /**
   * PATCH /issues/{id}. The pipeline builds the body: `{sort_order}`
   * ONLY — a list reorder never reassigns state, unlike a board
   * cross-column drop, so `state_id` must never appear here.
   */
  patch: (id: string, body: { sort_order: number }) => Promise<unknown>;
  /** POST .../issues/rebalance — body built here, `{state_id}` only. */
  rebalance: (body: { state_id: string }) => Promise<unknown>;
  /**
   * Refetch the loaded pages after a rebalance (the page invalidates
   * first, then refetches) and return the fresh flat list. The
   * pipeline re-derives the stack from it by the original stack's ids.
   */
  refetchFlat: () => Promise<Issue[]>;
  /** Invalidate ["issues", slug, identifier] so the displayed order is
   *  the server's order after a successful PATCH. */
  invalidate: () => Promise<unknown>;
  /** Surface an honest error the list's existing Alert way. */
  onError: (message: string) => void;
}

/**
 * Distinct state_ids of the drop's two neighbors in the post-move
 * stack order (C128T0). The neighbors are the rows the dragged row
 * lands between; under state grouping both are the dragged row's own
 * state, under priority/epic grouping they can be in different
 * states. Returns [] only when the drop has no neighbors at all
 * (top/bottom append of a lone row) — an append never collapses, so
 * that case never reaches the rebalance path.
 */
export function dropNeighborStateIds(
  stack: Issue[],
  drop: { orderedIds: string[] },
  activeId: string,
): string[] {
  const pos = drop.orderedIds.indexOf(activeId);
  if (pos === -1) return [];
  const byId = new Map(stack.map((i) => [i.id, i]));
  const states: string[] = [];
  for (const neighborId of [
    pos > 0 ? drop.orderedIds[pos - 1] : undefined,
    pos < drop.orderedIds.length - 1
      ? drop.orderedIds[pos + 1]
      : undefined,
  ]) {
    const stateId = neighborId === undefined ? undefined : byId.get(neighborId)?.state_id;
    if (stateId !== undefined && !states.includes(stateId)) {
      states.push(stateId);
    }
  }
  return states;
}

/**
 * Full list drop pipeline (C126T0, board's applyDrop precedent):
 * stackDropSortOrder → optimistic reorder + PATCH {sort_order} →
 * invalidate. A collapsed gap rebalances, refetches, and retries the
 * drop once — then gives up with an honest error, never writing a
 * bogus value. Which states get rebalanced is the C128T0 binding
 * story: the DISTINCT states among the drop's two neighbors,
 * sequential, one call when they share a state (under state grouping
 * that is exactly the C126 dragged-row-state call — the stack's
 * neighbors are all in the dragged row's state there, so the C126
 * path is untouched). Two calls when the neighbors differ; the
 * retry happens exactly once, and if the gap is STILL collapsed
 * after it, the pipeline surfaces an honest error and writes
 * NOTHING — no second retry, no retry loop, no stack re-space via
 * N PATCHes. A PATCH failure reverts the optimistic order (snapshot
 * restore) and surfaces the error. A no-op drop writes nothing and
 * touches no cache.
 */
export async function applyListDrop(
  stack: Issue[],
  activeId: string,
  overId: string,
  deps: ListDropDeps,
): Promise<void> {
  const dragged = stack.find((i) => i.id === activeId);
  if (!dragged) return;
  const attempt = async (rows: Issue[]): Promise<boolean> => {
    const drop = stackDropSortOrder(rows, activeId, overId);
    if (!drop.moved || drop.sortOrder === null) return false;
    const snapshot = deps.getData();
    const flat = snapshot?.pages.flatMap((p) => p.results) ?? rows;
    const newFlat = applyStackOrderToFlat(flat, drop.orderedIds, activeId);
    if (snapshot) deps.setData(reorderInfinitePages(snapshot, newFlat));
    try {
      await deps.patch(activeId, { sort_order: drop.sortOrder });
    } catch (e) {
      // Revert the optimistic order, then surface the error.
      if (snapshot) deps.setData(snapshot);
      deps.onError(
        e instanceof Error ? e.message : "Failed to reorder issue",
      );
      return true; // handled — do not fall through to rebalance
    }
    await deps.invalidate();
    return true;
  };

  // First attempt against the displayed stack.
  const drop0 = stackDropSortOrder(stack, activeId, overId);
  if (!drop0.moved) return;
  if (drop0.sortOrder !== null) {
    await attempt(stack);
    return;
  }
  // Gap collapsed (C128T0): rebalance each DISTINCT state among the
  // drop's two neighbors, sequential — one call when they share a
  // state, two when they differ. Rebalancing a state re-spaces only
  // that state's rows, so rebalancing the dragged row's state alone
  // cannot re-open a mixed-state gap. Any rebalance failure aborts
  // the remaining calls and surfaces honestly, writing nothing.
  // Defensive fallback (unreachable in practice: an append never
  // collapses, so a collapsed drop always has two neighbors): the
  // dragged row's own state, preserving the C126 behavior.
  const neighborStates = dropNeighborStateIds(stack, drop0, activeId);
  const statesToRebalance =
    neighborStates.length > 0 ? neighborStates : [dragged.state_id];
  try {
    for (const stateId of statesToRebalance) {
      await deps.rebalance({ state_id: stateId });
    }
  } catch (e) {
    deps.onError(
      e instanceof Error ? e.message : "Failed to rebalance column",
    );
    return;
  }
  // Refetch, retry once against the fresh stack (same membership,
  // re-spaced values). attempt() itself never rebalances, so this
  // is the single permitted retry; a still-collapsed gap falls
  // through to the honest error below.
  const freshFlat = await deps.refetchFlat();
  const stackIds = new Set(stack.map((i) => i.id));
  const freshStack = freshFlat.filter((i) => stackIds.has(i.id));
  const retried = await attempt(freshStack);
  if (!retried) {
    deps.onError("Couldn't place the issue even after rebalancing.");
  }
}
