// boardCycle (C132T2): pure board-side helpers for Group-by-Cycles
// mode. Board.tsx wires these into its existing dnd-kit + quick-add paths.
// Grouping semantics (single-appearance, Ungrouped-last-hidden-when-empty,
// first-appearance order, names from the row) come from
// groupByCycle.ts (C132T1) — see that module's header for the decided
// semantics. Kept framework-free so the drop/quick-add/count rules are
// cheaply unit-testable. Mirrors boardModule.ts with cycles substituted,
// EXCEPT for one deliberate divergence, forced by the row data (cycle-132
// plan theme section, not a taste call): board cycle drops MOVE, they do
// not attach-only. Cycle display is single-value — the server resolves
// exactly ONE displayed cycle per issue (first-by-name) — so an
// attach-only drop would leave the card displayed under the wrong cycle
// (whichever of the old and new cycles sorts first by name, not the drop
// target). A cross-column drop therefore adds the target cycle FIRST,
// then removes the source cycle (T0's DELETE endpoint), the same order
// as the list move pipeline. Dropping into Ungrouped stays a deliberate
// no-op like every other board dimension (removal to no-cycle on the
// board is out of scope; the list Ungrouped header does the removal).
// Cycles have NO color (NamedRef carries id + name only), so cycle
// columns carry no color field at all — no invented color, no
// hash-derived color, no lookup. The compound card-id shape is kept for
// uniformity with the other board helpers even though single-appearance
// means no card ever appears twice.

import { cycleOf, groupIssuesByCycle } from "./groupByCycle";
import type { Issue } from "../../lib/types";

/** Droppable id prefix for cycle columns: `cycle:<cycleId>`. */
export const CYCLE_COLUMN_PREFIX = "cycle:";
/** Sortable id prefix for cards in cycle mode:
 *  `cycle-card:<cycleId>:<issueId>`. Compound ids are kept for uniformity
 *  with the other board helpers (single-appearance means no card ever
 *  appears twice, but the drop target still resolves WHICH column's card
 *  was hit). Issue ids are UUIDs (no colons), so the LAST colon always
 *  separates the issue id (same rule as labels/modules). */
export const CYCLE_CARD_PREFIX = "cycle-card:";
/** Column key for the Ungrouped column (never a real cycle id). */
export const CYCLE_UNGROUPED_COLUMN_KEY = "ungrouped";

export interface CycleBoardGroup {
  /** dnd-kit droppable id for the column. */
  droppableId: string;
  /** "ungrouped" for the Ungrouped column, else the cycle's id. */
  columnKey: string;
  title: string;
  /** Cycle id, or null for the Ungrouped column. */
  cycleId: string | null;
  issues: Issue[];
  /** State stamped onto quick-added issues (mirrors the module columns:
   *  the first sorted state). */
  newStateId?: string;
}

/** Cycle columns from the loaded issue rows: one column per cycle
 *  (first-appearance order), Ungrouped last and only when non-empty.
 *  "Show empty groups" synthesizes nothing here: columns derive from
 *  loaded issues only (same boundary as T1's list grouping — there is
 *  no cycles-list fetch to populate empty columns). */
export function buildCycleBoardGroups(
  issues: Issue[],
  firstStateId?: string,
): CycleBoardGroup[] {
  return groupIssuesByCycle(issues).map((g) => {
    const ungrouped = g.isUngrouped;
    const columnKey = ungrouped ? CYCLE_UNGROUPED_COLUMN_KEY : g.key;
    return {
      droppableId: `${CYCLE_COLUMN_PREFIX}${columnKey}`,
      columnKey,
      title: g.title,
      cycleId: ungrouped ? null : g.key,
      issues: g.issues,
      newStateId: firstStateId,
    };
  });
}

/** Sortable id for a card in a cycle column. */
export function cycleCardDragId(columnKey: string, issueId: string): string {
  return `${CYCLE_CARD_PREFIX}${columnKey}:${issueId}`;
}

/** Resolve a dnd-kit `over` id to the target cycle column's key.
 *  Handles both the column droppable (`cycle:<key>`) and a card
 *  inside a column (`cycle-card:<key>:<issueId>`). The card-prefix
 *  check MUST precede the column-prefix check: card ids start with
 *  `cycle:` too. Returns null for non-cycle ids (state/priority/epic/
 *  assignee/label/module droppables, plain issue ids from
 *  sub-group-section unwrapping, unknown draggables). */
export function cycleDropColumnKey(overId: string): string | null {
  if (overId.startsWith(CYCLE_CARD_PREFIX)) {
    const rest = overId.slice(CYCLE_CARD_PREFIX.length);
    const sep = rest.lastIndexOf(":");
    return sep === -1 ? null : rest.slice(0, sep);
  }
  if (overId.startsWith(CYCLE_COLUMN_PREFIX)) {
    return overId.slice(CYCLE_COLUMN_PREFIX.length);
  }
  return null;
}

/** Issue id behind a cycle-mode sortable id. Identity for
 *  non-cycle ids, so it is safe to call on every active id. */
export function cycleDragIssueId(activeId: string): string {
  if (!activeId.startsWith(CYCLE_CARD_PREFIX)) return activeId;
  const rest = activeId.slice(CYCLE_CARD_PREFIX.length);
  const sep = rest.lastIndexOf(":");
  return sep === -1 ? activeId : rest.slice(sep + 1);
}

/** Cycle id behind a cycle-mode sortable id (the source column of
 *  a dragged card). Null for non-cycle ids and for malformed
 *  compound ids — callers treat null as "source column unknown" (the
 *  sub-group-section case, where cards carry subgroup ids instead)
 *  and fall back to the dragged row's displayed cycle. */
export function cycleDragColumnKey(activeId: string): string | null {
  if (!activeId.startsWith(CYCLE_CARD_PREFIX)) return null;
  const rest = activeId.slice(CYCLE_CARD_PREFIX.length);
  const sep = rest.lastIndexOf(":");
  return sep === -1 ? null : rest.slice(0, sep);
}

export type CycleDropPlan =
  | {
      kind: "move";
      /** Target cycle id (the drop column). */
      cycleId: string;
      /** Source cycle id (the dragged issue's displayed cycle), or
       *  null when the issue is Ungrouped — then the move is an
       *  add-only (nothing to remove). Never equals cycleId: the
       *  already-member case plans a no-op instead. */
      sourceCycleId: string | null;
    }
  | { kind: "noop"; reason: "ungrouped" | "already-member" };

/** Cross-column drop semantics (the deliberate C132 divergence from
 *  the label/module attach-only precedent): dropping a card into a
 *  cycle column MOVES the issue to that cycle — add target first,
 *  then remove source — because the single-value display would
 *  otherwise show the card under the wrong cycle. Dropping into
 *  Ungrouped is a deliberate no-op: removal to no-cycle on the board
 *  is out of scope. Cycle drops never change state, so no C68 workflow
 *  transition guard applies (the guard path in Board.tsx is
 *  state-grouping-only — verified, see dropBlocked). NOTE: this plan
 *  is for cross-column drops only — a drag whose source column IS the
 *  target column is a within-column reorder (Board.tsx resolves that
 *  from the dragged card's compound id before consulting this plan),
 *  not an already-member no-op (C131T1 trap). */
export function planCycleDrop(
  columnKey: string,
  issue: Issue,
): CycleDropPlan {
  if (columnKey === CYCLE_UNGROUPED_COLUMN_KEY) {
    return { kind: "noop", reason: "ungrouped" };
  }
  const current = cycleOf(issue);
  if (current?.id === columnKey) {
    return { kind: "noop", reason: "already-member" };
  }
  return {
    kind: "move",
    cycleId: columnKey,
    sourceCycleId: current?.id ?? null,
  };
}

/** Membership endpoints for a cycle move, injected so the ordering
 *  and failure semantics are unit-testable without a dnd-kit drag
 *  (no board-level pointer-drag simulation exists in this repo —
 *  same boundary as the label/module drop tests). Board.tsx passes
 *  the real api calls (POST/DELETE .../cycles/:id/issues with
 *  {issue_ids}). */
export interface CycleMoveDeps {
  /** POST .../cycles/{cycleId}/issues {issue_ids: [id]} (idempotent). */
  addToCycle: (cycleId: string, issueId: string) => Promise<unknown>;
  /** DELETE .../cycles/{cycleId}/issues {issue_ids: [id]} (idempotent, T0). */
  removeFromCycle: (cycleId: string, issueId: string) => Promise<unknown>;
}

export type CycleMoveResult =
  | { ok: true }
  | { ok: false; stage: "add" | "remove"; error: unknown };

/** Execute a cycle move: add target FIRST, then remove source (only
 *  when the plan names one — an Ungrouped source is an add-only move).
 *  An add failure aborts BEFORE any remove (the issue is untouched).
 *  A remove failure after a successful add surfaces honestly as
 *  stage "remove" (the issue is now in both cycles — the caller must
 *  surface that, never hide it, and invalidate so the board shows
 *  server state). Never retries silently, never changes state, never
 *  touches sort_order. */
export async function executeCycleMove(
  deps: CycleMoveDeps,
  plan: Extract<CycleDropPlan, { kind: "move" }>,
  issueId: string,
): Promise<CycleMoveResult> {
  try {
    await deps.addToCycle(plan.cycleId, issueId);
  } catch (error) {
    return { ok: false, stage: "add", error };
  }
  if (plan.sourceCycleId !== null) {
    try {
      await deps.removeFromCycle(plan.sourceCycleId, issueId);
    } catch (error) {
      return { ok: false, stage: "remove", error };
    }
  }
  return { ok: true };
}

/** Quick-add plan (mirrors board modules): a cycle column's quick-add
 *  creates the issue (with the column's state stamp) AND attaches it to
 *  the column's cycle; Ungrouped quick-add is a plain create. */
export function planCycleQuickAdd(group: {
  newStateId?: string;
  cycleId?: string | null;
}): { stateId?: string; attachCycleId: string | null } {
  return {
    stateId: group.newStateId,
    attachCycleId: group.cycleId ?? null,
  };
}

/** Expand-to-full-form state for a board cycle column's quick-add (the
 *  router state Board.tsx navigates to the list page with). Cycle
 *  columns carry the cycle id so the list page can attach it after
 *  create — the create dialog has no cycle field, so it rides
 *  pendingCycleRef exactly like the module carry (see Issues.tsx's
 *  createMutation onSuccess, C132T1: T1 built the newIssueCycleId
 *  bridge for T2's board expand, so NO list-page edits are needed in
 *  T2); Ungrouped columns carry no cycle, so the expand keeps title +
 *  column state only. */
export function planBoardCycleQuickExpand(
  group: {
    newStateId?: string;
    newPriority?: number;
    cycleId?: string | null;
  },
  name: string,
): {
  newIssue: true;
  newIssueName: string;
  newIssueStateId?: string;
  newIssuePriority?: number;
  newIssueCycleId?: string;
} {
  return {
    newIssue: true,
    newIssueName: name,
    ...(group.newStateId ? { newIssueStateId: group.newStateId } : {}),
    ...(group.newPriority !== undefined
      ? { newIssuePriority: group.newPriority }
      : {}),
    ...(group.cycleId ? { newIssueCycleId: group.cycleId } : {}),
  };
}
