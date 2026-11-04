import type { Issue, SubtaskRow } from "../../lib/types";
import { stackDropSortOrder } from "../../lib/sortOrder";
import {
  isNestedDragEnabled,
  nestedDropNeighborStateIds,
} from "./listNestedReorder";

/**
 * Board-view drag-to-reorder for nested subtask rows (C142T1) — the
 * C141 list-nested pipeline adapted to the board's flat Issue[]
 * cache. The page component owns transport (api + queryClient);
 * this module owns the rules, so the pipeline is unit-testable
 * without dnd-kit or a rendered page.
 *
 * Stack-scoped nested ids: `board-nested:<appearanceKey>:<parentId>:
 * <childId>`, where appearanceKey is the parent card's sortable id
 * (its compound card drag id under epic/assignee/label/module/cycle/
 * release/created-by/state-group grouping or sub-group sections, its
 * plain issue id in state/priority mode) — the identity that
 * distinguishes one parent-card appearance from another, so a
 * multi-appearance parent's nested rows never collide. Appearance
 * keys may themselves contain colons (compound card ids do), so
 * parsing takes the LAST two colon-separated segments as parent/
 * child ids (both are UUIDs, colon-free) and rejoins the rest as the
 * appearance key — the round-trip holds for every grouping alike.
 * This parser is the ONLY place a board-nested id is ever parsed;
 * dispatch resolves stacks through the page's nested map, never by
 * re-splitting an id string elsewhere.
 *
 * Drop pipeline: resolveBoardNestedDrop (same appearanceKey +
 * parentId required, else a silent no-op) → stackDropSortOrder
 * (shared with C126/C141 — no duplicated stack math) → optimistic
 * reorder + PATCH {sort_order} with state_id UNCHANGED (a nested
 * reorder never reassigns state and never re-parents; nested rows
 * render in the PARENT's column even when the child's own state
 * differs, C122) → invalidate. A collapsed gap rebalances each
 * DISTINCT non-null neighbor state (the C128 mixed-state recovery,
 * verbatim from C141), refetches, and retries once — then an honest
 * error, never a bogus value. When the drop has no non-null neighbor
 * state, the dragged child's own state is the fallback; when NO
 * state exists at all (a null-state child between null-state
 * neighbors — and a null-state child has no rebalance target of its
 * own), midpoint-null is an honest error with no retry and no write.
 *
 * Optimistic subtlety (nested-only, same as C141): the board renders
 * nested rows sorted by sort_order ASC, NOT in subtask-array order,
 * so merely reordering the parent's subtasks array would change
 * nothing on screen. The optimistic rewrite therefore ALSO writes
 * the dragged row's new sort_order value into the cached parent —
 * the split sort then displays the new sibling order immediately,
 * and the invalidate + refetch after PATCH replaces it with the
 * server's order. On PATCH failure the snapshot restores both.
 */

const BOARD_NESTED_PREFIX = "board-nested:";

export interface BoardNestedId {
  appearanceKey: string;
  parentId: string;
  childId: string;
}

/** Build a stack-scoped dnd-kit id for one nested row appearance. */
export function boardNestedSortableId(
  appearanceKey: string,
  parentId: string,
  childId: string,
): string {
  return `${BOARD_NESTED_PREFIX}${appearanceKey}:${parentId}:${childId}`;
}

/** Inverse of boardNestedSortableId; null for any non-nested id. */
export function parseBoardNestedId(id: string): BoardNestedId | null {
  if (!id.startsWith(BOARD_NESTED_PREFIX)) return null;
  const parts = id.slice(BOARD_NESTED_PREFIX.length).split(":");
  if (parts.length < 3) return null;
  const childId = parts.pop() as string;
  const parentId = parts.pop() as string;
  const appearanceKey = parts.join(":");
  if (!appearanceKey || !parentId || !childId) return null;
  return { appearanceKey, parentId, childId };
}

/** C142T1 gate: C141's gate (toggle on + >= 2 rows) AND canEdit. */
export function isBoardNestedDragEnabled(
  showSubWorkItems: boolean,
  nestedRowCount: number,
  canEdit: boolean,
): boolean {
  return canEdit && isNestedDragEnabled(showSubWorkItems, nestedRowCount);
}

/** A nested drop that the terminal branch should handle, or null. */
export interface BoardNestedDrop {
  parentId: string;
  activeChildId: string;
  overChildId: string;
}

/**
 * Resolve a raw dnd-kit drag pair to a board-nested drop. Returns
 * null — a silent no-op for the caller, never a fall-through to the
 * card pipelines — unless BOTH ids parse as nested ids with the SAME
 * appearanceKey + parentId. Cross-parent, cross-appearance, card-face,
 * column-droppable, and sub-group-section overs all resolve to null
 * here (their ids either parse with a different parent/appearance
 * or do not parse as nested at all).
 */
export function resolveBoardNestedDrop(
  activeRaw: string,
  overRaw: string,
): BoardNestedDrop | null {
  const active = parseBoardNestedId(activeRaw);
  const over = parseBoardNestedId(overRaw);
  if (active === null || over === null) return null;
  if (
    active.appearanceKey !== over.appearanceKey ||
    active.parentId !== over.parentId
  ) {
    return null;
  }
  return {
    parentId: active.parentId,
    activeChildId: active.childId,
    overChildId: over.childId,
  };
}

/**
 * Resolve a drag-overlay active id to the nested row it depicts, or
 * null for a card drag (or any non-nested id). The page renders the
 * nested overlay face for a non-null result, the card face otherwise.
 */
export function resolveBoardNestedOverlay(
  activeRaw: string,
  nestedByParent: Map<string, SubtaskRow[]>,
): { parentId: string; row: SubtaskRow } | null {
  const parsed = parseBoardNestedId(activeRaw);
  if (parsed === null) return null;
  const row = (nestedByParent.get(parsed.parentId) ?? []).find(
    (r) => r.id === parsed.childId,
  );
  if (!row) return null;
  return { parentId: parsed.parentId, row };
}

/**
 * Redistribute a reordered nested stack back into the board's flat
 * Issue[] cache (C141 reorderNestedInPages precedent, flat shape):
 * the parent issue's subtasks array is rewritten in `orderedIds`
 * order with the dragged row's sort_order set to its new value (see
 * the module header for why the value write is load-bearing here).
 * Every other issue is untouched. Returns the input unchanged when
 * the parent is missing or the id sets differ (defensive: never drop
 * or duplicate a row — the pipeline always passes a permutation of
 * the stack, so a mismatch is a bug, not a partial reorder to apply).
 */
export function reorderBoardNestedInIssues(
  issues: Issue[],
  parentId: string,
  orderedIds: string[],
  draggedId: string,
  sortOrder: number,
): Issue[] {
  let foundParent = false;
  let setsMatch = true;
  const next = issues.map((issue) => {
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
  if (!foundParent || !setsMatch) return issues;
  return next;
}

/** Transport + cache hooks the page wires to api/queryClient. */
export interface BoardNestedDropDeps {
  /** The parent issue whose subtasks array holds the stack. */
  parentId: string;
  /** Current displayed-query snapshot (for the optimistic revert). */
  getData: () => Issue[] | undefined;
  setData: (data: Issue[]) => void;
  /**
   * PATCH /issues/{id}. The pipeline builds the body: `{sort_order}`
   * ONLY — a nested reorder never reassigns state and never
   * re-parents, so `state_id` and `parent_id` must never appear here.
   */
  patch: (id: string, body: { sort_order: number }) => Promise<unknown>;
  /** POST .../issues/rebalance — body built here, `{state_id}` only. */
  rebalance: (body: { state_id: string }) => Promise<unknown>;
  /**
   * Refetch the displayed query after a rebalance (the page
   * invalidates first, then refetches) and return the fresh sibling
   * stack for the same parent, sorted by sort_order ASC — the
   * nestedByParent order.
   */
  refetchStack: () => Promise<SubtaskRow[]>;
  /** Invalidate the board queries so the displayed order is the
   *  server's order after a successful PATCH. */
  invalidate: () => Promise<unknown>;
  /** Surface an honest error the board's existing Alert way. */
  onError: (message: string) => void;
}

/**
 * Full nested drop pipeline (C141 applyNestedListDrop precedent,
 * flat-cache adapter): stackDropSortOrder → optimistic reorder +
 * PATCH {sort_order} → invalidate. A collapsed gap rebalances the
 * distinct neighbor states (fallback: the dragged child's state),
 * refetches, and retries the drop once — then gives up with an
 * honest error, never writing a bogus value. Any rebalance failure
 * aborts and surfaces honestly, writing nothing. A PATCH failure
 * reverts the optimistic order (snapshot restore) and surfaces the
 * error. A no-op drop writes nothing and touches no cache.
 */
export async function applyBoardNestedDrop(
  stack: SubtaskRow[],
  activeId: string,
  overId: string,
  deps: BoardNestedDropDeps,
): Promise<void> {
  const dragged = stack.find((i) => i.id === activeId);
  if (!dragged) return;
  const attempt = async (rows: SubtaskRow[]): Promise<boolean> => {
    const drop = stackDropSortOrder(rows, activeId, overId);
    if (!drop.moved || drop.sortOrder === null) return false;
    const snapshot = deps.getData();
    if (snapshot) {
      deps.setData(
        reorderBoardNestedInIssues(
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
  // state (the common same-state case), two when they differ.
  // Fallback: the dragged row's own state. No state at all
  // (null-state child, null-state neighbors) is an honest error with
  // no retry — there is no rebalance target.
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
