import type { InfiniteData } from "@tanstack/react-query";
import type { IssueListResult, SubtaskRow } from "../../lib/types";
import { stackDropSortOrder } from "../../lib/sortOrder";

/**
 * List-view drag-to-reorder for nested subtask rows (C141T1) — the
 * C126 applyListDrop pipeline adapted to one parent appearance's
 * sibling stack. The page component owns transport (api +
 * queryClient); this module owns the rules, so the pipeline is
 * unit-testable without dnd-kit or a rendered page.
 *
 * Gate (plan §Gate rules, verified by read against
 * splitNestedIssues.ts: nestedByParent is the ONLY nested-row source
 * on the list's render site and it sorts by sort_order ASC always,
 * regardless of orderBy, PQL mode, or grouping — and the refetch
 * path re-runs the same split, so the new sibling order reproduces
 * after invalidate): nested drag is enabled when showSubWorkItems is
 * on AND the parent appearance has >= 2 nested rows, and NOTHING
 * ELSE. The C126 rationale for its gate (dragging under another
 * order fights the sort on refetch) does not apply to nested stacks,
 * whose order never comes from the list sort.
 *
 * Drop pipeline: stackDropSortOrder (shared with C126 — no duplicated
 * stack math) → optimistic reorder + PATCH {sort_order} with
 * state_id UNCHANGED (a nested reorder never reassigns state and
 * never re-parents) → invalidate ["issues", slug, identifier].
 * A collapsed gap rebalances, refetches, and retries once — then an
 * honest error, never a bogus value. The rebalance targets are the
 * DISTINCT non-null states among the drop's two neighbors (the C128
 * mixed-state recovery, verbatim): a nested stack may mix states, so
 * rebalancing only the dragged child's state cannot re-open a
 * mixed-state gap. When the drop has no non-null neighbor state, the
 * dragged child's own state is the fallback — which is exactly the
 * plan's "the child's state" call in the common same-state case.
 * When NO state exists at all (a null-state child between null-state
 * neighbors — and a null-state child has no rebalance target of its
 * own), midpoint-null is an honest error with no retry and no write.
 *
 * Optimistic subtlety (nested-only): the list renders nested rows
 * sorted by sort_order ASC, NOT in subtask-array order, so merely
 * reordering the parent's subtasks array would change nothing on
 * screen. The optimistic rewrite therefore ALSO writes the dragged
 * row's new sort_order value into the cached parent — the split sort
 * then displays the new sibling order immediately, and the
 * invalidate + refetch after PATCH replaces it with the server's
 * order. On PATCH failure the snapshot restores both.
 */

/** C141T1 gate: showSubWorkItems on + a stack worth reordering. */
export function isNestedDragEnabled(
  showSubWorkItems: boolean,
  nestedRowCount: number,
): boolean {
  return showSubWorkItems && nestedRowCount >= 2;
}

/**
 * Redistribute a reordered nested stack back into the infinite-query
 * pages (C126 reorderInfinitePages precedent, nested shape): the
 * parent issue's subtasks array is rewritten in `orderedIds` order
 * with the dragged row's sort_order set to its new value (see the
 * module header for why the value write is load-bearing here). Every
 * other issue — and every other page — is untouched. Returns the
 * input unchanged when the parent is missing or the id sets differ
 * (defensive: never drop or duplicate a row — the pipeline always
 * passes a permutation of the stack, so a mismatch is a bug, not a
 * partial reorder to apply).
 */
export function reorderNestedInPages(
  data: InfiniteData<IssueListResult>,
  parentId: string,
  orderedIds: string[],
  draggedId: string,
  sortOrder: number,
): InfiniteData<IssueListResult> {
  let foundParent = false;
  let setsMatch = true;
  const pages = data.pages.map((p) => {
    const results = p.results.map((issue) => {
      if (issue.id !== parentId) return issue;
      foundParent = true;
      const subtasks = issue.subtasks ?? [];
      const byId = new Map(subtasks.map((row) => [row.id, row]));
      if (
        subtasks.length !== orderedIds.length ||
        !orderedIds.every((id) => byId.has(id))
      ) {
        setsMatch = false;
        return issue;
      }
      const ordered = orderedIds
        .map((id) => byId.get(id))
        .filter((r): r is SubtaskRow => r !== undefined)
        .map((row) =>
          row.id === draggedId ? { ...row, sort_order: sortOrder } : row,
        );
      return { ...issue, subtasks: ordered };
    });
    return { ...p, results };
  });
  if (!foundParent || !setsMatch) return data;
  return { ...data, pages };
}

/**
 * Distinct non-null state_ids of the drop's two neighbors in the
 * post-move stack order (C126 dropNeighborStateIds precedent, nested
 * shape: SubtaskRow.state_id is nullable, so null neighbors contribute
 * nothing). Returns [] only when the drop has no non-null neighbor
 * at all — then the caller falls back to the dragged row's own state,
 * or errors honestly when that is null too.
 */
export function nestedDropNeighborStateIds(
  stack: SubtaskRow[],
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
    const stateId =
      neighborId === undefined ? undefined : byId.get(neighborId)?.state_id;
    if (stateId !== null && stateId !== undefined && !states.includes(stateId)) {
      states.push(stateId);
    }
  }
  return states;
}

/** Transport + cache hooks the page wires to api/queryClient. */
export interface NestedListDropDeps {
  /** The parent issue whose subtasks array holds the stack. */
  parentId: string;
  /** Current infinite-query snapshot (for the optimistic revert). */
  getData: () => InfiniteData<IssueListResult> | undefined;
  setData: (data: InfiniteData<IssueListResult>) => void;
  /**
   * PATCH /issues/{id}. The pipeline builds the body: `{sort_order}`
   * ONLY — a nested reorder never reassigns state and never
   * re-parents, so `state_id` and `parent_id` must never appear here.
   */
  patch: (id: string, body: { sort_order: number }) => Promise<unknown>;
  /** POST .../issues/rebalance — body built here, `{state_id}` only. */
  rebalance: (body: { state_id: string }) => Promise<unknown>;
  /**
   * Refetch the loaded pages after a rebalance (the page invalidates
   * first, then refetches) and return the fresh sibling stack for the
   * same parent, sorted by sort_order ASC — the nestedByParent order.
   */
  refetchStack: () => Promise<SubtaskRow[]>;
  /** Invalidate ["issues", slug, identifier] so the displayed order is
   *  the server's order after a successful PATCH. */
  invalidate: () => Promise<unknown>;
  /** Surface an honest error the list's existing Alert way. */
  onError: (message: string) => void;
}

/**
 * Full nested drop pipeline (C126 applyListDrop precedent, nested
 * adapter): stackDropSortOrder → optimistic reorder + PATCH
 * {sort_order} → invalidate. A collapsed gap rebalances the
 * distinct neighbor states (fallback: the dragged child's state),
 * refetches, and retries the drop once — then gives up with an
 * honest error, never writing a bogus value. Any rebalance failure
 * aborts and surfaces honestly, writing nothing. A PATCH failure
 * reverts the optimistic order (snapshot restore) and surfaces the
 * error. A no-op drop writes nothing and touches no cache.
 */
export async function applyNestedListDrop(
  stack: SubtaskRow[],
  activeId: string,
  overId: string,
  deps: NestedListDropDeps,
): Promise<void> {
  const dragged = stack.find((i) => i.id === activeId);
  if (!dragged) return;
  const attempt = async (rows: SubtaskRow[]): Promise<boolean> => {
    const drop = stackDropSortOrder(rows, activeId, overId);
    if (!drop.moved || drop.sortOrder === null) return false;
    const snapshot = deps.getData();
    if (snapshot) {
      deps.setData(
        reorderNestedInPages(
          snapshot,
          deps.parentId,
          drop.orderedIds,
          activeId,
          drop.sortOrder,
        ),
      );
    }
    try {
      await deps.patch(activeId, { sort_order: drop.sortOrder });
    } catch (e) {
      // Revert the optimistic order, then surface the error.
      if (snapshot) deps.setData(snapshot);
      deps.onError(
        e instanceof Error ? e.message : "Failed to reorder subtask",
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
  // Gap collapsed: rebalance each DISTINCT non-null state among the
  // drop's two neighbors, sequential — one call when they share a
  // state (the common same-state case, and exactly the plan's
  // dragged-child-state call when the child shares its neighbors'
  // state), two when they differ. Fallback: the dragged row's own
  // state. No state at all (null-state child, null-state neighbors)
  // is an honest error with no retry — there is no rebalance target.
  const neighborStates = nestedDropNeighborStateIds(stack, drop0, activeId);
  const statesToRebalance =
    neighborStates.length > 0
      ? neighborStates
      : dragged.state_id
        ? [dragged.state_id]
        : [];
  if (statesToRebalance.length === 0) {
    deps.onError(
      "Couldn't place the subtask: it has no state to rebalance.",
    );
    return;
  }
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
  const freshStack = await deps.refetchStack();
  const retried = await attempt(freshStack);
  if (!retried) {
    deps.onError("Couldn't place the subtask even after rebalancing.");
  }
}
