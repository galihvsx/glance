// spreadsheetDrag (C148T1): pure spreadsheet cross-group drag helpers.
// Board boardEpic.ts precedent — framework-free so the drop rules are
// cheaply unit-testable; Spreadsheet.tsx wires these into its dnd-kit
// DndContext + patchMutation paths.
//
// Id scheme (Board compound-id precedent: group key + issue id, so a
// multi-appearance issue gets one draggable id per appearance):
//   drag source:  `spreadsheet-drag:<groupKey>:<issueId>`
//   group header: `spreadsheet-group:<groupKey>` (droppable)
//   row:          `spreadsheet-row-drop:<groupKey>:<issueId>` (droppable)
// Issue ids are UUIDs (no colons), so the LAST colon always separates
// the issue id — group keys that themselves contain a colon
// (`priority:4`, `state:ungrouped`) survive the split. Sub-group
// headers get no droppable id at all (C147 rule 7 preserved): a drop
// over one resolves to no target, never to the sub-dimension.

import { dropSortOrder, type SortableRow } from "../../lib/sortOrder";
import {
  allowedNextStateIds,
  isTransitionAllowed,
  type Workflow,
} from "../../lib/workflow";
import type { Issue } from "../../lib/types";
import { UNGROUPED_ASSIGNEE_KEY } from "./groupByAssignee";
import { cycleOf, UNGROUPED_CYCLE_KEY } from "./groupByCycle";
import { UNGROUPED_EPIC_KEY } from "./groupByEpic";
import { UNGROUPED_LABEL_KEY } from "./groupByLabel";
import { modulesOf, UNGROUPED_MODULE_KEY } from "./groupByModule";
import { releaseOf, UNGROUPED_RELEASE_KEY } from "./groupByRelease";

/** Draggable id prefix for spreadsheet row handles. */
export const SPREADSHEET_DRAG_PREFIX = "spreadsheet-drag:";
/** Droppable id prefix for primary group headers. */
export const SPREADSHEET_GROUP_DROPPABLE_PREFIX = "spreadsheet-group:";
/** Droppable id prefix for top-level rows (over targets). */
export const SPREADSHEET_ROW_DROPPABLE_PREFIX = "spreadsheet-row-drop:";
/** State-group key for issues whose state_id resolves to no loaded state. */
export const SPREADSHEET_STATE_UNGROUPED_KEY = "state:ungrouped";

/** Draggable id for one appearance of an issue in a primary group. */
export function spreadsheetDragId(
  groupKey: string,
  issueId: string,
): string {
  return `${SPREADSHEET_DRAG_PREFIX}${groupKey}:${issueId}`;
}

/** Droppable id for a primary group header. */
export function spreadsheetGroupDroppableId(groupKey: string): string {
  return `${SPREADSHEET_GROUP_DROPPABLE_PREFIX}${groupKey}`;
}

/** Droppable id for one appearance of a top-level row. */
export function spreadsheetRowDroppableId(
  groupKey: string,
  issueId: string,
): string {
  return `${SPREADSHEET_ROW_DROPPABLE_PREFIX}${groupKey}:${issueId}`;
}

/** Issue id behind a spreadsheet drag id. Identity for non-spreadsheet
 *  ids, so it is safe to call on every active id. */
export function spreadsheetDragIssueId(activeId: string): string {
  if (!activeId.startsWith(SPREADSHEET_DRAG_PREFIX)) return activeId;
  const rest = activeId.slice(SPREADSHEET_DRAG_PREFIX.length);
  const sep = rest.lastIndexOf(":");
  return sep === -1 ? activeId : rest.slice(sep + 1);
}

/** Source primary-group key behind a spreadsheet drag id, or null for
 *  non-spreadsheet ids (never a valid drag source). */
export function spreadsheetDragGroupKey(activeId: string): string | null {
  if (!activeId.startsWith(SPREADSHEET_DRAG_PREFIX)) return null;
  const rest = activeId.slice(SPREADSHEET_DRAG_PREFIX.length);
  const sep = rest.lastIndexOf(":");
  return sep === -1 ? null : rest.slice(0, sep);
}

/** Issue id behind a spreadsheet row droppable id, or null for
 *  non-row ids. Same LAST-colon split as the drag/group helpers, so
 *  colon-bearing group keys (`priority:4`, `state:ungrouped`)
 *  survive. */
export function spreadsheetRowDropIssueId(overId: string): string | null {
  if (!overId.startsWith(SPREADSHEET_ROW_DROPPABLE_PREFIX)) return null;
  const rest = overId.slice(SPREADSHEET_ROW_DROPPABLE_PREFIX.length);
  const sep = rest.lastIndexOf(":");
  return sep === -1 ? null : rest.slice(sep + 1);
}

/** Primary-group key a drop target resolves to: a primary group header
 *  id, or a row id (the row's primary group — Board over=card
 *  precedent). Null for anything else: sub-group headers carry no
 *  droppable id, so a drop over one never resolves here. */
export function spreadsheetOverGroupKey(overId: string): string | null {
  if (overId.startsWith(SPREADSHEET_GROUP_DROPPABLE_PREFIX)) {
    return overId.slice(SPREADSHEET_GROUP_DROPPABLE_PREFIX.length);
  }
  if (overId.startsWith(SPREADSHEET_ROW_DROPPABLE_PREFIX)) {
    const rest = overId.slice(SPREADSHEET_ROW_DROPPABLE_PREFIX.length);
    const sep = rest.lastIndexOf(":");
    return sep === -1 ? null : rest.slice(0, sep);
  }
  return null;
}

export type SpreadsheetAttachDimension =
  | "epic"
  | "assignees"
  | "labels"
  | "modules";

export type SpreadsheetDropPlan =
  | {
      kind: "patch";
      body: { state_id: string } | { priority: number };
    }
  | {
      kind: "attach";
      dimension: SpreadsheetAttachDimension;
      /** Target member id (the group key — non-Ungrouped group keys
       *  ARE the member ids, Board column-key precedent). */
      memberId: string;
    }
  | {
      kind: "move-cycle";
      cycleId: string;
      /** Row-derived source (null for an Ungrouped source = add-only
       *  move, Board moveToCycle precedent). */
      sourceCycleId: string | null;
    }
  | {
      kind: "move-release";
      releaseId: string;
    }
  | {
      kind: "blocked";
      /** Permitted next-state names for Board's "Move blocked" toast. */
      permittedNames: string[];
    }
  | {
      kind: "noop";
      reason:
        | "same-group"
        | "created-by"
        | "state-group"
        | "ungrouped-state"
        | "ungrouped"
        | "already-member"
        | "unknown-target"
        | "unknown-dimension";
    };

/** Cross-group drop semantics, mirroring Board's onDragEnd verbatim
 *  per dimension (cycle plan rule 6). Same-group is a no-op here
 *  (C148 rule 4) — the one C152T1 exception, a same-group row drop
 *  in the reorder-eligible configuration (state grouping, no column
 *  sort, no sub-group, visual-filter mode), never reaches this
 *  function: the handler routes it through planSpreadsheetReorder
 *  first, so every non-eligible path keeps this no-op byte-for-byte.
 *  State moves run under the C68 transition
 *  guard via Board's own isTransitionAllowed helper — a blocked move
 *  PATCHes nothing and the caller surfaces Board's error path (the
 *  "Move blocked" toast naming permitted next states). A null workflow
 *  (still loading) does not block: the server enforces, exactly as
 *  Board's hint-only dropBlocked does before its PATCH. createdBy and
 *  stateGroups are deliberate no-ops (Board: no single target exists
 *  to move to). Membership dimensions mirror Board's plan*Drop
 *  functions exactly, including their Ungrouped-target rule: every
 *  dimension's Ungrouped drop is a deliberate no-op (detach-from-all
 *  would be destructive — verified against planEpicDrop,
 *  planAssigneeDrop, planLabelDrop, planModuleDrop, planCycleDrop,
 *  planReleaseDrop, all of which return noop "ungrouped").
 *  Spreadsheet group keys differ from Board column keys only for
 *  Ungrouped (spreadsheet: `epic:ungrouped` / `assignee:ungrouped` /
 *  `label:ungrouped` / `module:ungrouped` / `cycle:ungrouped` /
 *  `release:ungrouped` from the groupBy* builders; Board: bare
 *  `ungrouped`) — non-Ungrouped group keys are the member ids, so
 *  the attach/move target is the group key itself. Already-member
 *  drops are no-ops (Board's already-attached / already-assigned /
 *  already-labeled / already-member reasons, unified here as
 *  "already-member"); the caller issues no membership call. Cycles
 *  move add-then-remove (Board executeCycleMove pipeline) and
 *  releases move via a single assign (Board executeReleaseMove —
 *  no DELETE, ever). */
export function planSpreadsheetDrop(args: {
  groupBy: string;
  sourceGroupKey: string;
  targetGroupKey: string;
  issue: Issue;
  workflow: Workflow | null;
  stateNames: Map<string, string>;
}): SpreadsheetDropPlan {
  const { groupBy, sourceGroupKey, targetGroupKey, issue, workflow } = args;
  if (sourceGroupKey === targetGroupKey) {
    return { kind: "noop", reason: "same-group" };
  }
  switch (groupBy) {
    case "state": {
      if (targetGroupKey === SPREADSHEET_STATE_UNGROUPED_KEY) {
        // No target state exists to PATCH to — Board has no Ungrouped
        // state column, so this mirrors "no column" as a no-op rather
        // than inventing a state.
        return { kind: "noop", reason: "ungrouped-state" };
      }
      if (
        workflow !== null &&
        !isTransitionAllowed(workflow, issue.state_id, targetGroupKey)
      ) {
        const allowed = allowedNextStateIds(workflow, issue.state_id) ?? [];
        return {
          kind: "blocked",
          permittedNames: allowed.map(
            (id) => args.stateNames.get(id) ?? id,
          ),
        };
      }
      return { kind: "patch", body: { state_id: targetGroupKey } };
    }
    case "priority": {
      const m = /^priority:(-?\d+)$/.exec(targetGroupKey);
      if (m === null) {
        return { kind: "noop", reason: "unknown-target" };
      }
      return { kind: "patch", body: { priority: Number(m[1]) } };
    }
    case "createdBy":
      return { kind: "noop", reason: "created-by" };
    case "stateGroups":
      return { kind: "noop", reason: "state-group" };
    case "epic": {
      // Board planEpicDrop: Ungrouped no-op; already-attached
      // (live, non-archived epic membership) no-op; else attach.
      if (targetGroupKey === UNGROUPED_EPIC_KEY) {
        return { kind: "noop", reason: "ungrouped" };
      }
      const already = (issue.epics ?? []).some(
        (e) => e.id === targetGroupKey && !e.archived_at,
      );
      if (already) return { kind: "noop", reason: "already-member" };
      return { kind: "attach", dimension: "epic", memberId: targetGroupKey };
    }
    case "assignees": {
      // Board planAssigneeDrop: Ungrouped no-op; already-assigned
      // no-op; else attach (multi-assignee aware, never detaches).
      if (targetGroupKey === UNGROUPED_ASSIGNEE_KEY) {
        return { kind: "noop", reason: "ungrouped" };
      }
      const already = (issue.assignees ?? []).some(
        (a) => a.id === targetGroupKey,
      );
      if (already) return { kind: "noop", reason: "already-member" };
      return {
        kind: "attach",
        dimension: "assignees",
        memberId: targetGroupKey,
      };
    }
    case "labels": {
      // Board planLabelDrop: Ungrouped no-op; already-labeled
      // no-op; else attach (multi-label aware, never detaches).
      if (targetGroupKey === UNGROUPED_LABEL_KEY) {
        return { kind: "noop", reason: "ungrouped" };
      }
      const already = (issue.labels ?? []).some(
        (l) => l.id === targetGroupKey,
      );
      if (already) return { kind: "noop", reason: "already-member" };
      return {
        kind: "attach",
        dimension: "labels",
        memberId: targetGroupKey,
      };
    }
    case "modules": {
      // Board planModuleDrop: Ungrouped no-op; already-member
      // no-op; else attach (multi-module aware, never detaches).
      if (targetGroupKey === UNGROUPED_MODULE_KEY) {
        return { kind: "noop", reason: "ungrouped" };
      }
      const already = modulesOf(issue).some(
        (m) => m.id === targetGroupKey,
      );
      if (already) return { kind: "noop", reason: "already-member" };
      return {
        kind: "attach",
        dimension: "modules",
        memberId: targetGroupKey,
      };
    }
    case "cycles": {
      // Board planCycleDrop: Ungrouped no-op; already-member
      // no-op; else MOVE (add target first, then remove source —
      // the deliberate divergence from attach-only, because the
      // single-value display would misdisplay an attach-only card
      // under the wrong cycle). Source is row-derived: an Ungrouped
      // source is an add-only move (sourceCycleId null).
      if (targetGroupKey === UNGROUPED_CYCLE_KEY) {
        return { kind: "noop", reason: "ungrouped" };
      }
      const current = cycleOf(issue);
      if (current?.id === targetGroupKey) {
        return { kind: "noop", reason: "already-member" };
      }
      return {
        kind: "move-cycle",
        cycleId: targetGroupKey,
        sourceCycleId: current?.id ?? null,
      };
    }
    case "releases": {
      // Board planReleaseDrop: Ungrouped no-op; already-member
      // no-op; else MOVE via a single assign (assign overwrites
      // release_id — no DELETE of the source, ever).
      if (targetGroupKey === UNGROUPED_RELEASE_KEY) {
        return { kind: "noop", reason: "ungrouped" };
      }
      const current = releaseOf(issue);
      if (current?.id === targetGroupKey) {
        return { kind: "noop", reason: "already-member" };
      }
      return { kind: "move-release", releaseId: targetGroupKey };
    }
    default:
      return { kind: "noop", reason: "unknown-dimension" };
  }
}

/** C152T1: within-state-group reorder plan. The caller guarantees
 *  the eligibility conditions (grouped by state, no column sort, no
 *  sub-group, visual-filter mode, top-level rows in one real state
 *  group, canEdit) — this planner only computes the write, so it
 *  stays framework-free and unit-testable like planSpreadsheetDrop.
 *  `rows` is the group's sort_order-ordered top-level sequence
 *  (filtered to the state defensively: a wider list still plans
 *  against the state column, never a foreign row). destIndex follows
 *  the dnd-kit sortable convention via dropSortOrder: the hovered
 *  row's index in the group's sequence is the position the dragged
 *  row occupies AFTER the move, i.e. dragging down onto a row lands
 *  after it, dragging up onto a row lands before it. A null
 *  sort_order is NOT a value to write: the gap collapsed below
 *  REBALANCE_GAP, so the caller rebalances the column, refetches,
 *  and retries once (Board pattern verbatim, never a silent second
 *  write without a refetch). */
export type SpreadsheetReorderPlan =
  | {
      kind: "noop";
      reason: "same-position" | "unknown-target";
    }
  | {
      kind: "reorder";
      /** Null = collapsed gap: rebalance + refetch + retry once. */
      sort_order: number | null;
    };

/** C152T2: insertion-indicator side for a reorder-eligible hover,
 *  derived from indices alone — never from pointer coordinates — so
 *  the line always agrees with the T1 planner. `rows` is the group's
 *  sort_order-ordered top-level sequence (same input the planner
 *  sees). BELOW the hovered row when the source sits above it
 *  (sortable convention: dragging down lands after), ABOVE it when
 *  the source sits below it; null when the hover is not a move at
 *  all (source row itself, or either row missing from the sequence)
 *  — a no-op drop gets no line. */
export function spreadsheetReorderIndicatorSide(args: {
  rows: SortableRow[];
  sourceId: string;
  hoveredId: string;
}): "above" | "below" | null {
  const { rows, sourceId, hoveredId } = args;
  const sourceIdx = rows.findIndex((r) => r.id === sourceId);
  const hoverIdx = rows.findIndex((r) => r.id === hoveredId);
  if (sourceIdx === -1 || hoverIdx === -1) return null;
  if (sourceIdx === hoverIdx) return null;
  return sourceIdx < hoverIdx ? "below" : "above";
}

/** C152T2: move-to-end plan for a same-group header drop — the
 *  row-drop planner's destIndex = group length case, with no target
 *  row. The source already last is a same-position no-op; otherwise
 *  dropSortOrder's same-state clamp lands the row after the
 *  group's last row, i.e. prev + SORT_ORDER_STEP (or null on a
 *  collapsed gap, same rebalance-retry contract as
 *  planSpreadsheetReorder). */
export function planSpreadsheetReorderToEnd(args: {
  rows: SortableRow[];
  sourceId: string;
  stateId: string;
}): SpreadsheetReorderPlan {
  const { rows, sourceId, stateId } = args;
  const col = rows.filter((r) => r.state_id === stateId);
  const sourceIdx = col.findIndex((r) => r.id === sourceId);
  if (sourceIdx === -1) {
    return { kind: "noop", reason: "unknown-target" };
  }
  if (sourceIdx === col.length - 1) {
    return { kind: "noop", reason: "same-position" };
  }
  return {
    kind: "reorder",
    sort_order: dropSortOrder(col, sourceId, stateId, stateId, col.length),
  };
}

export function planSpreadsheetReorder(args: {
  rows: SortableRow[];
  sourceId: string;
  targetRowId: string;
  stateId: string;
}): SpreadsheetReorderPlan {
  const { rows, sourceId, targetRowId, stateId } = args;
  const col = rows.filter((r) => r.state_id === stateId);
  const sourceIdx = col.findIndex((r) => r.id === sourceId);
  const destIndex = col.findIndex((r) => r.id === targetRowId);
  if (sourceIdx === -1 || destIndex === -1) {
    return { kind: "noop", reason: "unknown-target" };
  }
  // arrayMove-after-removal (dropSortOrder's same-state branch)
  // lands the dragged row at exactly destIndex (clamped), so a
  // final position equal to the source index is a no-move — which,
  // under this convention, is exactly the source-row drop.
  const finalPos = Math.max(0, Math.min(destIndex, col.length - 1));
  if (finalPos === sourceIdx) {
    return { kind: "noop", reason: "same-position" };
  }
  return {
    kind: "reorder",
    sort_order: dropSortOrder(col, sourceId, stateId, stateId, destIndex),
  };
}
