// sort_order midpoint math for the kanban board (Task 23).
//
// Issues within a state column are ordered by the float64 `sort_order`
// column. Dropping a card between two neighbors computes the midpoint;
// repeated drops halve the gap each time, so after enough drops between
// the same two neighbors the gap collapses below REBALANCE_GAP and the
// board must call POST .../issues/rebalance {state_id} (service.
// RebalanceSortOrder re-spaces the column to even 1024-steps), refetch,
// and retry the drop.
//
// SORT_ORDER_STEP must match the backend's rebalanceSpacing.

/** Even spacing the backend rebalance leaves between consecutive issues. */
export const SORT_ORDER_STEP = 1024;

/**
 * Gap below which a midpoint is considered collapsed. The caller must
 * rebalance the column first instead of writing the midpoint.
 */
export const REBALANCE_GAP = 1e-9;

/**
 * Compute the sort_order for a card dropped at a position whose current
 * neighbors (excluding the dragged card) are `prev` and `next`.
 *
 * @param prev sort_order of the card that will sit above the drop, or
 *             null when dropping at the very top of the column
 * @param next sort_order of the card that will sit below the drop, or
 *             null when dropping at the very bottom
 * @returns the midpoint to write, or null when the gap has collapsed
 *          below REBALANCE_GAP — the caller must rebalance first.
 */
export function midpoint(
  prev: number | null,
  next: number | null,
): number | null {
  if (prev === null && next === null) return SORT_ORDER_STEP;
  if (prev === null) return (next as number) - SORT_ORDER_STEP;
  if (next === null) return prev + SORT_ORDER_STEP;
  // A non-positive gap means the input wasn't sorted (or is already
  // collapsed): never write a bogus value, ask for a rebalance instead.
  if (next - prev < REBALANCE_GAP) return null;
  return (prev + next) / 2;
}

/** Minimal shape dropSortOrder needs from an issue row. */
export interface SortableRow {
  id: string;
  state_id: string;
  sort_order: number;
}

/**
 * Compute the sort_order for a card dropped at destIndex of the target
 * column. destIndex follows dnd-kit's sortable convention: the index of
 * the hovered card (or the column length when dropped on the column
 * itself), i.e. the position the card occupies AFTER the move.
 *
 * @returns the midpoint to write, or null when the gap has collapsed
 *          below REBALANCE_GAP — the caller must rebalance the target
 *          column first, then retry.
 */
export function dropSortOrder(
  all: SortableRow[],
  activeId: string,
  sourceStateId: string,
  destStateId: string,
  destIndex: number,
): number | null {
  const col = all.filter((i) => i.state_id === destStateId);
  let final: SortableRow[];
  let pos: number;
  if (sourceStateId === destStateId) {
    const oldIdx = col.findIndex((i) => i.id === activeId);
    if (oldIdx === -1) return null;
    // arrayMove semantics: the index applies after the removal.
    const idx = Math.max(0, Math.min(destIndex, col.length - 1));
    const without = [...col.slice(0, oldIdx), ...col.slice(oldIdx + 1)];
    final = [...without.slice(0, idx), col[oldIdx], ...without.slice(idx)];
    pos = idx;
  } else {
    const dragged = all.find((i) => i.id === activeId);
    if (!dragged) return null;
    const idx = Math.max(0, Math.min(destIndex, col.length));
    final = [...col.slice(0, idx), dragged, ...col.slice(idx)];
    pos = idx;
  }
  const prev = pos > 0 ? final[pos - 1].sort_order : null;
  const next = pos < final.length - 1 ? final[pos + 1].sort_order : null;
  return midpoint(prev, next);
}
