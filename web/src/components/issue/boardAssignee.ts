// boardAssignee (C129T1): pure board-side helpers for Group-by-Assignees
// mode. Board.tsx wires these into its existing dnd-kit + quick-add paths.
// Grouping semantics (multi-appearance, Ungrouped-last-hidden-when-empty,
// first-appearance order, names from the row) come from groupByAssignee.ts
// (C129T0) — see that module's header for the decided semantics. Kept
// framework-free so the drop/quick-add/count rules are cheaply
// unit-testable. Mirrors boardEpic.ts with assignees substituted; the
// one deliberate difference is the attach endpoint (the assignee
// membership endpoint, POST .../issues/:uuid/assignees/:userID, not the
// epic bulk-attach endpoint).

import { groupIssuesByAssignee } from "./groupByAssignee";
import type { Issue } from "../../lib/types";

/** Droppable id prefix for assignee columns: `assignee:<memberId>`. */
export const ASSIGNEE_COLUMN_PREFIX = "assignee:";
/** Sortable id prefix for cards in assignee mode:
 *  `assignee-card:<memberId>:<issueId>`. Compound ids disambiguate
 *  multi-appearance cards — the same issue id renders in several
 *  columns, so a drop onto a card must resolve WHICH column's copy was
 *  hit. Issue ids are UUIDs (no colons), so the LAST colon always
 *  separates the issue id (same rule as epic). */
export const ASSIGNEE_CARD_PREFIX = "assignee-card:";
/** Column key for the Ungrouped column (never a real member id). */
export const ASSIGNEE_UNGROUPED_COLUMN_KEY = "ungrouped";

export interface AssigneeBoardGroup {
  /** dnd-kit droppable id for the column. */
  droppableId: string;
  /** "ungrouped" for the Ungrouped column, else the member's id. */
  columnKey: string;
  title: string;
  /** Member id, or null for the Ungrouped column. */
  assigneeId: string | null;
  issues: Issue[];
  /** State stamped onto quick-added issues (mirrors the epic columns:
   *  the first sorted state). */
  newStateId?: string;
}

/** Assignee columns from the loaded issue rows: one column per member
 *  (first-appearance order), Ungrouped last and only when non-empty.
 *  "Show empty groups" synthesizes nothing here: columns derive from
 *  loaded issues only (same boundary as T0's list grouping — there is
 *  no members-list fetch to populate empty columns). */
export function buildAssigneeBoardGroups(
  issues: Issue[],
  firstStateId?: string,
): AssigneeBoardGroup[] {
  return groupIssuesByAssignee(issues).map((g) => {
    const ungrouped = g.isUngrouped;
    const columnKey = ungrouped ? ASSIGNEE_UNGROUPED_COLUMN_KEY : g.key;
    return {
      droppableId: `${ASSIGNEE_COLUMN_PREFIX}${columnKey}`,
      columnKey,
      title: g.title,
      assigneeId: ungrouped ? null : g.key,
      issues: g.issues,
      newStateId: firstStateId,
    };
  });
}

/** Sortable id for a card in an assignee column. */
export function assigneeCardDragId(columnKey: string, issueId: string): string {
  return `${ASSIGNEE_CARD_PREFIX}${columnKey}:${issueId}`;
}

/** Resolve a dnd-kit `over` id to the target assignee column's key.
 *  Handles both the column droppable (`assignee:<key>`) and a card
 *  inside a column (`assignee-card:<key>:<issueId>`). Returns null for
 *  non-assignee ids (state/priority/epic droppables, plain issue ids
 *  from sub-group-section unwrapping, unknown draggables). */
export function assigneeDropColumnKey(overId: string): string | null {
  if (overId.startsWith(ASSIGNEE_CARD_PREFIX)) {
    const rest = overId.slice(ASSIGNEE_CARD_PREFIX.length);
    const sep = rest.lastIndexOf(":");
    return sep === -1 ? null : rest.slice(0, sep);
  }
  if (overId.startsWith(ASSIGNEE_COLUMN_PREFIX)) {
    return overId.slice(ASSIGNEE_COLUMN_PREFIX.length);
  }
  return null;
}

/** Issue id behind an assignee-mode sortable id. Identity for
 *  non-assignee ids, so it is safe to call on every active id. */
export function assigneeDragIssueId(activeId: string): string {
  if (!activeId.startsWith(ASSIGNEE_CARD_PREFIX)) return activeId;
  const rest = activeId.slice(ASSIGNEE_CARD_PREFIX.length);
  const sep = rest.lastIndexOf(":");
  return sep === -1 ? activeId : rest.slice(sep + 1);
}

/** Member id behind an assignee-mode sortable id (the source column of
 *  a dragged card). Null for non-assignee ids and for malformed
 *  compound ids — callers treat null as "source column unknown" (the
 *  sub-group-section case, where cards carry subgroup ids instead). */
export function assigneeDragColumnKey(activeId: string): string | null {
  if (!activeId.startsWith(ASSIGNEE_CARD_PREFIX)) return null;
  const rest = activeId.slice(ASSIGNEE_CARD_PREFIX.length);
  const sep = rest.lastIndexOf(":");
  return sep === -1 ? null : rest.slice(0, sep);
}

export type AssigneeDropPlan =
  | { kind: "attach"; memberId: string }
  | { kind: "noop"; reason: "ungrouped" | "already-assigned" };

/** Cross-column drop semantics (mirrors board epic): dropping a card
 *  into an assignee column ATTACHES the issue to that member —
 *  idempotent (a second drop is a no-op) and multi-assignee aware
 *  (other memberships are never touched, never detached). Dropping into
 *  Ungrouped is a deliberate no-op: detach-from-all would be
 *  destructive. Assignee drops never change state, so no C68 workflow
 *  transition guard applies. NOTE: this plan is for cross-column
 *  drops only — a drag whose source column IS the target column is a
 *  within-column reorder (Board.tsx resolves that from the dragged
 *  card's compound id before consulting this plan), not an
 *  already-assigned no-op. */
export function planAssigneeDrop(
  columnKey: string,
  issue: Issue,
): AssigneeDropPlan {
  if (columnKey === ASSIGNEE_UNGROUPED_COLUMN_KEY) {
    return { kind: "noop", reason: "ungrouped" };
  }
  const already = (issue.assignees ?? []).some((a) => a.id === columnKey);
  if (already) return { kind: "noop", reason: "already-assigned" };
  return { kind: "attach", memberId: columnKey };
}

/** Quick-add plan (mirrors board epic): an assignee column's quick-add
 *  creates the issue (with the column's state stamp) AND attaches it to
 *  the column's member; Ungrouped quick-add is a plain create. */
export function planAssigneeQuickAdd(group: {
  newStateId?: string;
  assigneeId?: string | null;
}): { stateId?: string; attachAssigneeId: string | null } {
  return {
    stateId: group.newStateId,
    attachAssigneeId: group.assigneeId ?? null,
  };
}

/** Expand-to-full-form state for a board column's quick-add (the router
 *  state Board.tsx navigates to the list page with). Member columns
 *  carry the member id so the list page's create form can attach it
 *  after create (its dialog has no assignee field — see Issues.tsx's
 *  pendingAssigneeRef); Ungrouped columns carry no member, so the
 *  expand keeps title + column state only. */
export function planBoardQuickExpand(
  group: {
    newStateId?: string;
    newPriority?: number;
    assigneeId?: string | null;
  },
  name: string,
): {
  newIssue: true;
  newIssueName: string;
  newIssueStateId?: string;
  newIssuePriority?: number;
  newIssueAssigneeId?: string;
} {
  return {
    newIssue: true,
    newIssueName: name,
    ...(group.newStateId ? { newIssueStateId: group.newStateId } : {}),
    ...(group.newPriority !== undefined
      ? { newIssuePriority: group.newPriority }
      : {}),
    ...(group.assigneeId ? { newIssueAssigneeId: group.assigneeId } : {}),
  };
}
