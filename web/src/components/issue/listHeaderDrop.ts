import { SORT_ORDER_STEP } from "../../lib/sortOrder";
import type { Issue } from "../../lib/types";
import { assigneesOf } from "./groupByAssignee";
import { liveEpics } from "./groupByEpic";
import { labelsOf } from "./groupByLabel";

/**
 * List-view group-header drop targets (C127T0, extended C129T0 with
 * assignee targets mirroring the epic ones exactly, C130T0 with
 * label targets mirroring the assignee ones exactly). The page component owns
 * transport (api + queryClient); this module owns the rules, so the
 * reassignment pipeline is unit-testable without dnd-kit or a rendered
 * page — the listReorder.ts (C126) split, applied to the second drag
 * mode.
 *
 * Two modes coexist under every admitted grouping (drop on a row =
 * C126/C128 reorder, drop on a header = reassign): since C128, row
 * drops reorder within their stack under priority/epic grouping too,
 * exactly as they already did under state grouping. A header drop
 * changes EXACTLY that header's dimension and nothing else, and a
 * reorder PATCHes only {sort_order} — neither mode ever touches the
 * other's dimension.
 */

/** A group/sub-group header's reassignment target — explicit fields,
 *  never a parsed key string (the page maps group/section fields to
 *  this once, at render time). */
export type HeaderTarget =
  | { kind: "state"; stateId: string }
  | { kind: "priority"; priority: number }
  | { kind: "epic"; epicId: string }
  | { kind: "epic-ungrouped" }
  | { kind: "assignee"; assigneeId: string }
  | { kind: "assignee-ungrouped" }
  | { kind: "label"; labelId: string }
  | { kind: "label-ungrouped" };

/** Explicit target-value fields carried on IssueGroup / SubGroupSection
 *  (C127T0, extended C129T0, C130T0). A section with none of these names no assignable value —
 *  today only the state-Ungrouped sub-header — and is never droppable. */
export interface HeaderTargetFields {
  targetStateId?: string;
  targetPriority?: number;
  targetEpicId?: string;
  isUngroupedEpic?: boolean;
  targetAssigneeId?: string;
  isUngroupedAssignee?: boolean;
  targetLabelId?: string;
  isUngroupedLabel?: boolean;
}

/** Map a group/section's target fields to its header target, or null
 *  when the header names no assignable value (state-Ungrouped). */
export function toHeaderTarget(
  fields: HeaderTargetFields,
): HeaderTarget | null {
  if (fields.isUngroupedEpic) return { kind: "epic-ungrouped" };
  if (fields.isUngroupedAssignee) return { kind: "assignee-ungrouped" };
  if (fields.isUngroupedLabel) return { kind: "label-ungrouped" };
  if (fields.targetStateId !== undefined)
    return { kind: "state", stateId: fields.targetStateId };
  if (fields.targetPriority !== undefined)
    return { kind: "priority", priority: fields.targetPriority };
  if (fields.targetEpicId !== undefined)
    return { kind: "epic", epicId: fields.targetEpicId };
  if (fields.targetAssigneeId !== undefined)
    return { kind: "assignee", assigneeId: fields.targetAssigneeId };
  if (fields.targetLabelId !== undefined)
    return { kind: "label", labelId: fields.targetLabelId };
  return null;
}

/**
 * Reassign-drag enable rule (plan §Scope): grouping is active (the
 * page's effective grouping — a grouping skipped by its single-value
 * filter renders flat, so there are no headers to drop on anyway)
 * and not PQL mode. Unlike C126's reorder gate, orderBy is ANY value:
 * a reassignment persists regardless of display order; the appended
 * sort_order only affects Manual-rank display.
 */
export function isListReassignEnabled(args: {
  pqlMode: boolean;
  groupingActive: boolean;
}): boolean {
  return args.groupingActive && !args.pqlMode;
}

/**
 * dnd-kit id for a header droppable. Section-scoped: the same target
 * value can head several sections at once (e.g. a state sub-header
 * under every priority group), so the section key is part of the id —
 * otherwise two droppables would share one id in the list-wide
 * context and the drop could resolve to the wrong section's rows.
 * The `header:` prefix keeps header ids disjoint from row sortable
 * ids by construction (those start with `row:`).
 */
export function headerDropId(
  sectionKey: string,
  target: HeaderTarget,
): string {
  switch (target.kind) {
    case "state":
      return `header:${sectionKey}:state:${target.stateId}`;
    case "priority":
      return `header:${sectionKey}:priority:${target.priority}`;
    case "epic":
      return `header:${sectionKey}:epic:${target.epicId}`;
    case "epic-ungrouped":
      return `header:${sectionKey}:epic:ungrouped`;
    case "assignee":
      return `header:${sectionKey}:assignee:${target.assigneeId}`;
    case "assignee-ungrouped":
      return `header:${sectionKey}:assignee:ungrouped`;
    case "label":
      return `header:${sectionKey}:label:${target.labelId}`;
    case "label-ungrouped":
      return `header:${sectionKey}:label:ungrouped`;
  }
}

/**
 * dnd-kit sortable id for a row inside one stack. Stack-scoped: the
 * same row multi-appears across epic sections, so the stack key is
 * part of the id (board's subgroup-card precedent) — otherwise two
 * sortable items would share one id in the list-wide context.
 */
export function listSortableId(stackKey: string, rowId: string): string {
  return `row:${stackKey}:${rowId}`;
}

/**
 * The appended sort_order for a header drop: max(target section's
 * loaded rows' sort_order) + SORT_ORDER_STEP; when the target section
 * has no loaded rows, the dragged row's current sort_order (a single
 * row's position is exact regardless of value). Append can never
 * collapse a gap (no `next` bound), so no rebalance path is needed.
 * Pagination caveat (plan): rows on not-yet-loaded pages keep their
 * values; a bottom append among unloaded values may shift on refetch.
 */
export function bottomSortOrder(
  targetRows: Issue[],
  dragged: Issue,
): number {
  if (targetRows.length === 0) return dragged.sort_order;
  return Math.max(...targetRows.map((r) => r.sort_order)) + SORT_ORDER_STEP;
}

/** Exact call sequence for one header drop (null = silent no-op). */
export type ListHeaderDropPlan =
  | { type: "patch"; body: { state_id: string; sort_order: number } }
  | { type: "patch"; body: { priority: number; sort_order: number } }
  | {
      type: "epic-move";
      addEpicId: string;
      /** Null when the dragged appearance came from Ungrouped or
       *  from a non-epic grouping (nothing to remove). */
      removeEpicId: string | null;
      sortOrder: number;
    }
  | { type: "epic-ungroup"; removeEpicIds: string[] }
  | {
      type: "assignee-move";
      addAssigneeId: string;
      /** Null when the dragged appearance came from Ungrouped or
       *  from a non-assignee grouping (nothing to remove). */
      removeAssigneeId: string | null;
      sortOrder: number;
    }
  | { type: "assignee-ungroup"; removeAssigneeIds: string[] }
  | {
      type: "label-move";
      addLabelId: string;
      /** Null when the dragged appearance came from Ungrouped or
       *  from a non-label grouping (nothing to remove). */
      removeLabelId: string | null;
      sortOrder: number;
    }
  | { type: "label-ungroup"; removeLabelIds: string[] };

/**
 * Resolve a header drop to its exact call sequence (C127T0). All
 * payload decisions live here, not inline in the page:
 * - state/priority: one PATCH carrying exactly that dimension +
 *   bottom-appended sort_order; no-op when already that value.
 * - epic: add-before-remove + sort PATCH; no-op when already a
 *   member of the target epic (multi-epic rows included — membership,
 *   not appearance, decides). Remove runs only when the dragged
 *   appearance came from an epic group different from the target.
 * - epic Ungrouped: remove from ALL live epics (archived memberships
 *   are not live, per groupByEpic), NO sort_order PATCH — the row's
 *   position in the Ungrouped stack follows its existing value.
 *   This is the one case where "move" means remove-all rather than
 *   remove-source: remove-source-only would leave a multi-epic row
 *   invisible in Ungrouped after the drop.
 * - assignee (C129T0): mirrors the epic case exactly — add-before-
 *   remove + sort PATCH; no-op when already assigned to the target
 *   member (multi-assignee rows included — membership, not
 *   appearance, decides). Remove runs only when the dragged
 *   appearance came from an assignee group different from the target.
 * - assignee Ungrouped: remove from ALL current assignees, NO
 *   sort_order PATCH (same rationale as epic Ungrouped).
 * - label (C130T0): mirrors the assignee case exactly — add-before-
 *   remove + sort PATCH; no-op when already labeled with the target
 *   label (multi-label rows included — membership, not
 *   appearance, decides). Remove runs only when the dragged
 *   appearance came from a label group different from the target.
 * - label Ungrouped: remove from ALL current labels, NO
 *   sort_order PATCH (same rationale as epic/assignee Ungrouped).
 */
export function resolveListHeaderDrop(args: {
  dragged: Issue;
  /** Epic group the dragged appearance came from, or null (non-epic
   *  grouping, or an Ungrouped appearance). */
  sourceEpicId: string | null;
  /** Assignee group the dragged appearance came from, or null
   *  (non-assignee grouping, or an Ungrouped appearance). */
  sourceAssigneeId?: string | null;
  /** Label group the dragged appearance came from, or null
   *  (non-label grouping, or an Ungrouped appearance). */
  sourceLabelId?: string | null;
  target: HeaderTarget;
  /** The header section's loaded rows (for the bottom-append math). */
  targetRows: Issue[];
}): ListHeaderDropPlan | null {
  const {
    dragged,
    sourceEpicId,
    sourceAssigneeId,
    sourceLabelId,
    target,
    targetRows,
  } = args;
  switch (target.kind) {
    case "state":
      if (dragged.state_id === target.stateId) return null;
      return {
        type: "patch",
        body: {
          state_id: target.stateId,
          sort_order: bottomSortOrder(targetRows, dragged),
        },
      };
    case "priority":
      if (dragged.priority === target.priority) return null;
      return {
        type: "patch",
        body: {
          priority: target.priority,
          sort_order: bottomSortOrder(targetRows, dragged),
        },
      };
    case "epic": {
      if (liveEpics(dragged).some((e) => e.id === target.epicId)) return null;
      return {
        type: "epic-move",
        addEpicId: target.epicId,
        removeEpicId:
          sourceEpicId !== null && sourceEpicId !== target.epicId
            ? sourceEpicId
            : null,
        sortOrder: bottomSortOrder(targetRows, dragged),
      };
    }
    case "epic-ungrouped": {
      const live = liveEpics(dragged);
      if (live.length === 0) return null;
      return { type: "epic-ungroup", removeEpicIds: live.map((e) => e.id) };
    }
    case "assignee": {
      if (assigneesOf(dragged).some((a) => a.id === target.assigneeId))
        return null;
      return {
        type: "assignee-move",
        addAssigneeId: target.assigneeId,
        removeAssigneeId:
          sourceAssigneeId != null && sourceAssigneeId !== target.assigneeId
            ? sourceAssigneeId
            : null,
        sortOrder: bottomSortOrder(targetRows, dragged),
      };
    }
    case "assignee-ungrouped": {
      const current = assigneesOf(dragged);
      if (current.length === 0) return null;
      return {
        type: "assignee-ungroup",
        removeAssigneeIds: current.map((a) => a.id),
      };
    }
    case "label": {
      if (labelsOf(dragged).some((l) => l.id === target.labelId)) return null;
      return {
        type: "label-move",
        addLabelId: target.labelId,
        removeLabelId:
          sourceLabelId != null && sourceLabelId !== target.labelId
            ? sourceLabelId
            : null,
        sortOrder: bottomSortOrder(targetRows, dragged),
      };
    }
    case "label-ungrouped": {
      const current = labelsOf(dragged);
      if (current.length === 0) return null;
      return {
        type: "label-ungroup",
        removeLabelIds: current.map((l) => l.id),
      };
    }
  }
}

/** Transport + cache hooks the page wires to api/queryClient. */
export interface ListHeaderDropDeps {
  /** PATCH /issues/{id} — the executor builds the body from the plan. */
  patch: (
    id: string,
    body: { state_id?: string; priority?: number; sort_order: number },
  ) => Promise<unknown>;
  /** POST .../epics/{epicId}/issues {issue_ids: [id]} (idempotent). */
  addToEpic: (epicId: string, issueId: string) => Promise<unknown>;
  /** DELETE .../epics/{epicId}/issues {issue_ids: [id]} (idempotent). */
  removeFromEpic: (epicId: string, issueId: string) => Promise<unknown>;
  /** POST .../issues/{id}/assignees/{userId} (idempotent, no body). */
  addToAssignee: (assigneeId: string, issueId: string) => Promise<unknown>;
  /** DELETE .../issues/{id}/assignees/{userId} (idempotent, no body). */
  removeFromAssignee: (
    assigneeId: string,
    issueId: string,
  ) => Promise<unknown>;
  /** POST .../issues/{id}/labels/{labelId} (idempotent, no body). */
  addToLabel: (labelId: string, issueId: string) => Promise<unknown>;
  /** DELETE .../issues/{id}/labels/{labelId} (idempotent, no body). */
  removeFromLabel: (labelId: string, issueId: string) => Promise<unknown>;
  /** Invalidate ["issues", slug, identifier] so the moved row appears
   *  in its new group from the server's state, not local optimism. */
  invalidate: () => Promise<unknown>;
  /** Surface an honest error the list's existing Alert way. */
  onError: (message: string) => void;
}

/**
 * Execute a resolved header-drop plan (C127T0, extended C129T0,
 * C130T0). Call order is the contract: epic/assignee/label add
 * BEFORE remove, sort PATCH last. Any failure aborts the remaining
 * calls and surfaces honestly — a remove failure after a successful
 * add leaves the row in both epics / assigned to both members /
 * labeled with both labels, reported, never hidden or silently
 * retried. Invalidation runs after success AND after failure so
 * the list shows the server's state either way. No optimistic
 * group-move rendering: the page waits for refetch (the simpler
 * correct choice the plan allows).
 */
export async function executeListHeaderDrop(
  issueId: string,
  plan: ListHeaderDropPlan,
  deps: ListHeaderDropDeps,
): Promise<void> {
  try {
    if (plan.type === "patch") {
      await deps.patch(issueId, plan.body);
    } else if (plan.type === "epic-move") {
      await deps.addToEpic(plan.addEpicId, issueId);
      if (plan.removeEpicId !== null) {
        await deps.removeFromEpic(plan.removeEpicId, issueId);
      }
      await deps.patch(issueId, { sort_order: plan.sortOrder });
    } else if (plan.type === "assignee-move") {
      await deps.addToAssignee(plan.addAssigneeId, issueId);
      if (plan.removeAssigneeId !== null) {
        await deps.removeFromAssignee(plan.removeAssigneeId, issueId);
      }
      await deps.patch(issueId, { sort_order: plan.sortOrder });
    } else if (plan.type === "label-move") {
      await deps.addToLabel(plan.addLabelId, issueId);
      if (plan.removeLabelId !== null) {
        await deps.removeFromLabel(plan.removeLabelId, issueId);
      }
      await deps.patch(issueId, { sort_order: plan.sortOrder });
    } else if (plan.type === "label-ungroup") {
      for (const labelId of plan.removeLabelIds) {
        await deps.removeFromLabel(labelId, issueId);
      }
    } else if (plan.type === "assignee-ungroup") {
      for (const assigneeId of plan.removeAssigneeIds) {
        await deps.removeFromAssignee(assigneeId, issueId);
      }
    } else {
      for (const epicId of plan.removeEpicIds) {
        await deps.removeFromEpic(epicId, issueId);
      }
    }
  } catch (e) {
    deps.onError(e instanceof Error ? e.message : "Failed to move issue");
    await deps.invalidate();
    return;
  }
  await deps.invalidate();
}

/** One rendered row stack: its rows, and the epic/assignee/label
 *  group its appearances belong to (null unless epic-/assignee-/
 *  label-grouped). */
export interface ListDragStackRef {
  stackKey: string;
  rows: Issue[];
  sourceEpicId: string | null;
  sourceAssigneeId: string | null;
  sourceLabelId: string | null;
}

/** One draggable row appearance, as the page's drop maps record it. */
export interface ListDragRowRef {
  rowId: string;
  issue: Issue;
  stack: ListDragStackRef;
}

/** One droppable header, as the page's drop maps record it. */
export interface ListHeaderDropInfo {
  target: HeaderTarget;
  targetRows: Issue[];
}

/**
 * Route one list drag-end to the right pipeline (C127T0). The page
 * builds both maps at render time from the same group/section objects
 * the layout renders, so dispatch reads maps, never parses an id:
 * - over a header id → the reassignment pipeline (never the reorder
 *   pipeline), regardless of the reorder gate;
 * - over a row id in the SAME stack → the C126/C128 reorder
 *   pipeline, and only when the reorder gate holds (non-Manual
 *   order or PQL mode: a row drop is a silent no-op);
 * - over a row id in another stack → silent no-op (never a reorder);
 * - no over, same-id, or unknown ids → silent no-op.
 */
export function dispatchListDragEnd(args: {
  activeSortableId: string;
  overId: string | null;
  sortableMap: Map<string, ListDragRowRef>;
  headerMap: Map<string, ListHeaderDropInfo>;
  /** C126 reorder gate — when false, drops on rows are silent no-ops. */
  reorderEnabled: boolean;
  onRowDrop: (
    stack: Issue[],
    activeRowId: string,
    overRowId: string,
  ) => void;
  onHeaderDrop: (
    dragged: ListDragRowRef,
    info: ListHeaderDropInfo,
  ) => void;
}): void {
  const { activeSortableId, overId } = args;
  if (overId === null || overId === activeSortableId) return;
  const active = args.sortableMap.get(activeSortableId);
  if (!active) return;
  const header = args.headerMap.get(overId);
  if (header) {
    args.onHeaderDrop(active, header);
    return;
  }
  const over = args.sortableMap.get(overId);
  if (!over) return;
  if (!args.reorderEnabled) return;
  if (over.stack.stackKey !== active.stack.stackKey) return;
  args.onRowDrop(active.stack.rows, active.rowId, over.rowId);
}
