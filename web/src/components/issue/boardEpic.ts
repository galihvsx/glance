// boardEpic (C72T1): pure board-side helpers for Group-by-Epic mode.
// Board.tsx wires these into its existing dnd-kit + quick-add paths.
// Grouping semantics (multi-appearance, archived exclusion,
// Ungrouped-last-hidden-when-empty) come from groupByEpic.ts — see that
// module's header for the decided semantics. Kept framework-free so the
// drop/quick-add/count rules are cheaply unit-testable.

import { groupIssuesByEpic } from "./groupByEpic";
import type { Issue } from "../../lib/types";

/** Droppable id prefix for epic columns: `epic:<columnKey>`. */
export const EPIC_COLUMN_PREFIX = "epic:";
/** Sortable id prefix for cards in epic mode: `epic-card:<columnKey>:<issueId>`.
 *  Compound ids disambiguate multi-appearance cards — the same issue id
 *  renders in several columns, so a drop onto a card must resolve WHICH
 *  column's copy was hit. Issue ids are UUIDs (no colons), so the LAST
 *  colon always separates the issue id. */
export const EPIC_CARD_PREFIX = "epic-card:";
/** Column key for the Ungrouped column (never a real epic id). */
export const UNGROUPED_COLUMN_KEY = "ungrouped";

export interface EpicBoardGroup {
  /** dnd-kit droppable id for the column. */
  droppableId: string;
  /** "ungrouped" for the Ungrouped column, else the live epic's id. */
  columnKey: string;
  title: string;
  /** Live epic id, or null for the Ungrouped column. */
  epicId: string | null;
  issues: Issue[];
  /** Per-state-group member counts for the shared EpicGroupBreakdown. */
  byGroup: Record<string, number>;
  /** State stamped onto quick-added issues (mirrors the priority-column
   *  convention: the first sorted state). */
  newStateId?: string;
}

/** Epic columns from the loaded issue rows: one column per LIVE epic
 *  (first-appearance order), Ungrouped last and only when non-empty. */
export function buildEpicBoardGroups(
  issues: Issue[],
  stateGroupOf: (issue: Issue) => string | undefined,
  firstStateId?: string,
): EpicBoardGroup[] {
  return groupIssuesByEpic(issues, stateGroupOf).map((g) => {
    const ungrouped = g.isUngrouped;
    const columnKey = ungrouped ? UNGROUPED_COLUMN_KEY : g.key;
    return {
      droppableId: `${EPIC_COLUMN_PREFIX}${columnKey}`,
      columnKey,
      title: g.title,
      epicId: ungrouped ? null : g.key,
      issues: g.issues,
      byGroup: g.byGroup,
      newStateId: firstStateId,
    };
  });
}

/** Sortable id for a card in an epic column. */
export function epicCardDragId(columnKey: string, issueId: string): string {
  return `${EPIC_CARD_PREFIX}${columnKey}:${issueId}`;
}

/** Resolve a dnd-kit `over` id to the target epic column's key. Handles
 *  both the column droppable (`epic:<key>`) and a card inside a column
 *  (`epic-card:<key>:<issueId>`). Returns null for non-epic ids
 *  (state/priority droppables, unknown draggables). */
export function epicDropColumnKey(overId: string): string | null {
  if (overId.startsWith(EPIC_CARD_PREFIX)) {
    const rest = overId.slice(EPIC_CARD_PREFIX.length);
    const sep = rest.lastIndexOf(":");
    return sep === -1 ? null : rest.slice(0, sep);
  }
  if (overId.startsWith(EPIC_COLUMN_PREFIX)) {
    return overId.slice(EPIC_COLUMN_PREFIX.length);
  }
  return null;
}

/** Issue id behind an epic-mode sortable id. Identity for non-epic ids,
 *  so it is safe to call on every active id. */
export function epicDragIssueId(activeId: string): string {
  if (!activeId.startsWith(EPIC_CARD_PREFIX)) return activeId;
  const rest = activeId.slice(EPIC_CARD_PREFIX.length);
  const sep = rest.lastIndexOf(":");
  return sep === -1 ? activeId : rest.slice(sep + 1);
}

export type EpicDropPlan =
  | { kind: "attach"; epicId: string }
  | { kind: "noop"; reason: "ungrouped" | "already-attached" };

/** Drop semantics (coordinator ruling 2): dropping a card into an epic
 *  column ATTACHES the issue to that epic — idempotent (a second drop is
 *  a no-op) and multi-epic aware (other memberships are never touched).
 *  Dropping into Ungrouped is a deliberate no-op: detach-from-all would
 *  be destructive. Epic drops never change state, so no C68 workflow
 *  transition guard applies. */
export function planEpicDrop(
  columnKey: string,
  issue: Issue,
): EpicDropPlan {
  if (columnKey === UNGROUPED_COLUMN_KEY) {
    return { kind: "noop", reason: "ungrouped" };
  }
  const already = (issue.epics ?? []).some(
    (e) => e.id === columnKey && !e.archived_at,
  );
  if (already) return { kind: "noop", reason: "already-attached" };
  return { kind: "attach", epicId: columnKey };
}

/** Quick-add plan (coordinator ruling 3): an epic column's quick-add
 *  creates the issue (with the column's state stamp) AND attaches it to
 *  the column's epic; Ungrouped quick-add is a plain create. */
export function planEpicQuickAdd(group: {
  newStateId?: string;
  epicId?: string | null;
}): { stateId?: string; attachEpicId: string | null } {
  return {
    stateId: group.newStateId,
    attachEpicId: group.epicId ?? null,
  };
}

/** PageHeader count under multi-appearance (coordinator ruling 4): the
 *  header shows UNIQUE issues, not column memberships. The board's
 *  `issues` rows are unique per issue already; the Set is belt-and-braces. */
export function uniqueIssueCount(issues: Issue[]): number {
  return new Set(issues.map((i) => i.id)).size;
}
