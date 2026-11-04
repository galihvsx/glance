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

import {
  allowedNextStateIds,
  isTransitionAllowed,
  type Workflow,
} from "../../lib/workflow";
import type { Issue } from "../../lib/types";

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

export type SpreadsheetDropPlan =
  | {
      kind: "patch";
      body: { state_id: string } | { priority: number };
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
        | "pending-dimension"
        | "unknown-target"
        | "unknown-dimension";
    };

/** Cross-group drop semantics, mirroring Board's onDragEnd verbatim
 *  per dimension (cycle plan rule 6). Same-group is always a no-op,
 *  never a reorder (rule 4). State moves run under the C68 transition
 *  guard via Board's own isTransitionAllowed helper — a blocked move
 *  PATCHes nothing and the caller surfaces Board's error path (the
 *  "Move blocked" toast naming permitted next states). A null workflow
 *  (still loading) does not block: the server enforces, exactly as
 *  Board's hint-only dropBlocked does before its PATCH. createdBy and
 *  stateGroups are deliberate no-ops (Board: no single target exists
 *  to move to). The membership dimensions (epic, assignees, labels,
 *  modules, cycles, releases) resolve here as pending-dimension no-ops
 *  in T1 — the dispatch shape lands now so T2 only adds their
 *  membership branches, never a new dispatch. */
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
    case "epic":
    case "assignees":
    case "labels":
    case "modules":
    case "cycles":
    case "releases":
      // C148T1: dispatch shape only — T2 adds the membership branch
      // per dimension, verified against Board's branch.
      return { kind: "noop", reason: "pending-dimension" };
    default:
      return { kind: "noop", reason: "unknown-dimension" };
  }
}
