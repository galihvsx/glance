// boardLabel (C130T1): pure board-side helpers for Group-by-Labels
// mode. Board.tsx wires these into its existing dnd-kit + quick-add paths.
// Grouping semantics (multi-appearance, Ungrouped-last-hidden-when-empty,
// first-appearance order, names + colors from the row) come from
// groupByLabel.ts (C130T0) — see that module's header for the decided
// semantics. Kept framework-free so the drop/quick-add/count rules are
// cheaply unit-testable. Mirrors boardAssignee.ts with labels substituted;
// the one deliberate difference is the attach endpoint (the label
// membership endpoint, POST .../issues/:uuid/labels/:labelID, not the
// assignee membership endpoint).

import { groupIssuesByLabel } from "./groupByLabel";
import type { Issue } from "../../lib/types";

/** Droppable id prefix for label columns: `label:<labelId>`. */
export const LABEL_COLUMN_PREFIX = "label:";
/** Sortable id prefix for cards in label mode:
 *  `label-card:<labelId>:<issueId>`. Compound ids disambiguate
 *  multi-appearance cards — the same issue id renders in several
 *  columns, so a drop onto a card must resolve WHICH column's copy was
 *  hit. Issue ids are UUIDs (no colons), so the LAST colon always
 *  separates the issue id (same rule as assignees). */
export const LABEL_CARD_PREFIX = "label-card:";
/** Column key for the Ungrouped column (never a real label id). */
export const LABEL_UNGROUPED_COLUMN_KEY = "ungrouped";

export interface LabelBoardGroup {
  /** dnd-kit droppable id for the column. */
  droppableId: string;
  /** "ungrouped" for the Ungrouped column, else the label's id. */
  columnKey: string;
  title: string;
  /** Label id, or null for the Ungrouped column. */
  labelId: string | null;
  /** Label color from the row (first-appearance row wins, per T0);
   *  absent for the Ungrouped column. */
  color?: string;
  issues: Issue[];
  /** State stamped onto quick-added issues (mirrors the assignee columns:
   *  the first sorted state). */
  newStateId?: string;
}

/** Label columns from the loaded issue rows: one column per label
 *  (first-appearance order), Ungrouped last and only when non-empty.
 *  "Show empty groups" synthesizes nothing here: columns derive from
 *  loaded issues only (same boundary as T0's list grouping — there is
 *  no labels-list fetch to populate empty columns). */
export function buildLabelBoardGroups(
  issues: Issue[],
  firstStateId?: string,
): LabelBoardGroup[] {
  return groupIssuesByLabel(issues).map((g) => {
    const ungrouped = g.isUngrouped;
    const columnKey = ungrouped ? LABEL_UNGROUPED_COLUMN_KEY : g.key;
    return {
      droppableId: `${LABEL_COLUMN_PREFIX}${columnKey}`,
      columnKey,
      title: g.title,
      labelId: ungrouped ? null : g.key,
      color: g.color,
      issues: g.issues,
      newStateId: firstStateId,
    };
  });
}

/** Sortable id for a card in a label column. */
export function labelCardDragId(columnKey: string, issueId: string): string {
  return `${LABEL_CARD_PREFIX}${columnKey}:${issueId}`;
}

/** Resolve a dnd-kit `over` id to the target label column's key.
 *  Handles both the column droppable (`label:<key>`) and a card
 *  inside a column (`label-card:<key>:<issueId>`). Returns null for
 *  non-label ids (state/priority/epic/assignee droppables, plain issue
 *  ids from sub-group-section unwrapping, unknown draggables). */
export function labelDropColumnKey(overId: string): string | null {
  if (overId.startsWith(LABEL_CARD_PREFIX)) {
    const rest = overId.slice(LABEL_CARD_PREFIX.length);
    const sep = rest.lastIndexOf(":");
    return sep === -1 ? null : rest.slice(0, sep);
  }
  if (overId.startsWith(LABEL_COLUMN_PREFIX)) {
    return overId.slice(LABEL_COLUMN_PREFIX.length);
  }
  return null;
}

/** Issue id behind a label-mode sortable id. Identity for
 *  non-label ids, so it is safe to call on every active id. */
export function labelDragIssueId(activeId: string): string {
  if (!activeId.startsWith(LABEL_CARD_PREFIX)) return activeId;
  const rest = activeId.slice(LABEL_CARD_PREFIX.length);
  const sep = rest.lastIndexOf(":");
  return sep === -1 ? activeId : rest.slice(sep + 1);
}

/** Label id behind a label-mode sortable id (the source column of
 *  a dragged card). Null for non-label ids and for malformed
 *  compound ids — callers treat null as "source column unknown" (the
 *  sub-group-section case, where cards carry subgroup ids instead). */
export function labelDragColumnKey(activeId: string): string | null {
  if (!activeId.startsWith(LABEL_CARD_PREFIX)) return null;
  const rest = activeId.slice(LABEL_CARD_PREFIX.length);
  const sep = rest.lastIndexOf(":");
  return sep === -1 ? null : rest.slice(0, sep);
}

export type LabelDropPlan =
  | { kind: "attach"; labelId: string }
  | { kind: "noop"; reason: "ungrouped" | "already-labeled" };

/** Cross-column drop semantics (mirrors board assignees): dropping a
 *  card into a label column ATTACHES the issue to that label —
 *  idempotent (a second drop is a no-op) and multi-label aware
 *  (other memberships are never touched, never detached). Dropping into
 *  Ungrouped is a deliberate no-op: detach-from-all would be
 *  destructive. Label drops never change state, so no C68 workflow
 *  transition guard applies. NOTE: this plan is for cross-column
 *  drops only — a drag whose source column IS the target column is a
 *  within-column reorder (Board.tsx resolves that from the dragged
 *  card's compound id before consulting this plan), not an
 *  already-labeled no-op. */
export function planLabelDrop(
  columnKey: string,
  issue: Issue,
): LabelDropPlan {
  if (columnKey === LABEL_UNGROUPED_COLUMN_KEY) {
    return { kind: "noop", reason: "ungrouped" };
  }
  const already = (issue.labels ?? []).some((l) => l.id === columnKey);
  if (already) return { kind: "noop", reason: "already-labeled" };
  return { kind: "attach", labelId: columnKey };
}

/** Quick-add plan (mirrors board assignees): a label column's quick-add
 *  creates the issue (with the column's state stamp) AND attaches it to
 *  the column's label; Ungrouped quick-add is a plain create. */
export function planLabelQuickAdd(group: {
  newStateId?: string;
  labelId?: string | null;
}): { stateId?: string; attachLabelId: string | null } {
  return {
    stateId: group.newStateId,
    attachLabelId: group.labelId ?? null,
  };
}

/** Expand-to-full-form state for a board label column's quick-add (the
 *  router state Board.tsx navigates to the list page with). Label
 *  columns carry the label id so the list page can attach it after
 *  create — the create dialog has no label field, so it rides
 *  pendingLabelRef exactly like the assignee carry (see Issues.tsx's
 *  createMutation onSuccess, C130T0); Ungrouped columns carry no label,
 *  so the expand keeps title + column state only. */
export function planBoardLabelQuickExpand(
  group: {
    newStateId?: string;
    newPriority?: number;
    labelId?: string | null;
  },
  name: string,
): {
  newIssue: true;
  newIssueName: string;
  newIssueStateId?: string;
  newIssuePriority?: number;
  newIssueLabelId?: string;
} {
  return {
    newIssue: true,
    newIssueName: name,
    ...(group.newStateId ? { newIssueStateId: group.newStateId } : {}),
    ...(group.newPriority !== undefined
      ? { newIssuePriority: group.newPriority }
      : {}),
    ...(group.labelId ? { newIssueLabelId: group.labelId } : {}),
  };
}
