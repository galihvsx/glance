// boardModule (C131T1): pure board-side helpers for Group-by-Modules
// mode. Board.tsx wires these into its existing dnd-kit + quick-add paths.
// Grouping semantics (multi-appearance, Ungrouped-last-hidden-when-empty,
// first-appearance order, names from the row) come from
// groupByModule.ts (C131T0) — see that module's header for the decided
// semantics. Kept framework-free so the drop/quick-add/count rules are
// cheaply unit-testable. Mirrors boardLabel.ts with modules substituted;
// the deliberate differences are the attach endpoint (the module
// membership endpoint, POST .../modules/:moduleID/issues with
// {issue_ids}, not the label membership endpoint) and the absence of
// color: modules have NO color (NamedRef carries id + name only), so
// module columns carry no color field at all — no invented color, no
// hash-derived color, no lookup.

import { groupIssuesByModule, modulesOf } from "./groupByModule";
import type { Issue } from "../../lib/types";

/** Droppable id prefix for module columns: `module:<moduleId>`. */
export const MODULE_COLUMN_PREFIX = "module:";
/** Sortable id prefix for cards in module mode:
 *  `module-card:<moduleId>:<issueId>`. Compound ids disambiguate
 *  multi-appearance cards — the same issue id renders in several
 *  columns, so a drop onto a card must resolve WHICH column's copy was
 *  hit. Issue ids are UUIDs (no colons), so the LAST colon always
 *  separates the issue id (same rule as labels). */
export const MODULE_CARD_PREFIX = "module-card:";
/** Column key for the Ungrouped column (never a real module id). */
export const MODULE_UNGROUPED_COLUMN_KEY = "ungrouped";

export interface ModuleBoardGroup {
  /** dnd-kit droppable id for the column. */
  droppableId: string;
  /** "ungrouped" for the Ungrouped column, else the module's id. */
  columnKey: string;
  title: string;
  /** Module id, or null for the Ungrouped column. */
  moduleId: string | null;
  issues: Issue[];
  /** State stamped onto quick-added issues (mirrors the label columns:
   *  the first sorted state). */
  newStateId?: string;
}

/** Module columns from the loaded issue rows: one column per module
 *  (first-appearance order), Ungrouped last and only when non-empty.
 *  "Show empty groups" synthesizes nothing here: columns derive from
 *  loaded issues only (same boundary as T0's list grouping — there is
 *  no modules-list fetch to populate empty columns). */
export function buildModuleBoardGroups(
  issues: Issue[],
  firstStateId?: string,
): ModuleBoardGroup[] {
  return groupIssuesByModule(issues).map((g) => {
    const ungrouped = g.isUngrouped;
    const columnKey = ungrouped ? MODULE_UNGROUPED_COLUMN_KEY : g.key;
    return {
      droppableId: `${MODULE_COLUMN_PREFIX}${columnKey}`,
      columnKey,
      title: g.title,
      moduleId: ungrouped ? null : g.key,
      issues: g.issues,
      newStateId: firstStateId,
    };
  });
}

/** Sortable id for a card in a module column. */
export function moduleCardDragId(columnKey: string, issueId: string): string {
  return `${MODULE_CARD_PREFIX}${columnKey}:${issueId}`;
}

/** Resolve a dnd-kit `over` id to the target module column's key.
 *  Handles both the column droppable (`module:<key>`) and a card
 *  inside a column (`module-card:<key>:<issueId>`). Returns null for
 *  non-module ids (state/priority/epic/assignee/label droppables,
 *  plain issue ids from sub-group-section unwrapping, unknown
 *  draggables). */
export function moduleDropColumnKey(overId: string): string | null {
  if (overId.startsWith(MODULE_CARD_PREFIX)) {
    const rest = overId.slice(MODULE_CARD_PREFIX.length);
    const sep = rest.lastIndexOf(":");
    return sep === -1 ? null : rest.slice(0, sep);
  }
  if (overId.startsWith(MODULE_COLUMN_PREFIX)) {
    return overId.slice(MODULE_COLUMN_PREFIX.length);
  }
  return null;
}

/** Issue id behind a module-mode sortable id. Identity for
 *  non-module ids, so it is safe to call on every active id. */
export function moduleDragIssueId(activeId: string): string {
  if (!activeId.startsWith(MODULE_CARD_PREFIX)) return activeId;
  const rest = activeId.slice(MODULE_CARD_PREFIX.length);
  const sep = rest.lastIndexOf(":");
  return sep === -1 ? activeId : rest.slice(sep + 1);
}

/** Module id behind a module-mode sortable id (the source column of
 *  a dragged card). Null for non-module ids and for malformed
 *  compound ids — callers treat null as "source column unknown" (the
 *  sub-group-section case, where cards carry subgroup ids instead). */
export function moduleDragColumnKey(activeId: string): string | null {
  if (!activeId.startsWith(MODULE_CARD_PREFIX)) return null;
  const rest = activeId.slice(MODULE_CARD_PREFIX.length);
  const sep = rest.lastIndexOf(":");
  return sep === -1 ? null : rest.slice(0, sep);
}

export type ModuleDropPlan =
  | { kind: "attach"; moduleId: string }
  | { kind: "noop"; reason: "ungrouped" | "already-member" };

/** Cross-column drop semantics (mirrors board labels): dropping a
 *  card into a module column ATTACHES the issue to that module —
 *  idempotent (a second drop is a no-op) and multi-module aware
 *  (other memberships are never touched, never detached). Dropping into
 *  Ungrouped is a deliberate no-op: detach-from-all would be
 *  destructive. Module drops never change state, so no C68 workflow
 *  transition guard applies. NOTE: this plan is for cross-column
 *  drops only — a drag whose source column IS the target column is a
 *  within-column reorder (Board.tsx resolves that from the dragged
 *  card's compound id before consulting this plan), not an
 *  already-member no-op. */
export function planModuleDrop(
  columnKey: string,
  issue: Issue,
): ModuleDropPlan {
  if (columnKey === MODULE_UNGROUPED_COLUMN_KEY) {
    return { kind: "noop", reason: "ungrouped" };
  }
  const already = modulesOf(issue).some((m) => m.id === columnKey);
  if (already) return { kind: "noop", reason: "already-member" };
  return { kind: "attach", moduleId: columnKey };
}

/** Quick-add plan (mirrors board labels): a module column's quick-add
 *  creates the issue (with the column's state stamp) AND attaches it to
 *  the column's module; Ungrouped quick-add is a plain create. */
export function planModuleQuickAdd(group: {
  newStateId?: string;
  moduleId?: string | null;
}): { stateId?: string; attachModuleId: string | null } {
  return {
    stateId: group.newStateId,
    attachModuleId: group.moduleId ?? null,
  };
}

/** Expand-to-full-form state for a board module column's quick-add (the
 *  router state Board.tsx navigates to the list page with). Module
 *  columns carry the module id so the list page can attach it after
 *  create — the create dialog has no module field, so it rides
 *  pendingModuleRef exactly like the label carry (see Issues.tsx's
 *  createMutation onSuccess, C131T0); Ungrouped columns carry no module,
 *  so the expand keeps title + column state only. */
export function planBoardModuleQuickExpand(
  group: {
    newStateId?: string;
    newPriority?: number;
    moduleId?: string | null;
  },
  name: string,
): {
  newIssue: true;
  newIssueName: string;
  newIssueStateId?: string;
  newIssuePriority?: number;
  newIssueModuleId?: string;
} {
  return {
    newIssue: true,
    newIssueName: name,
    ...(group.newStateId ? { newIssueStateId: group.newStateId } : {}),
    ...(group.newPriority !== undefined
      ? { newIssuePriority: group.newPriority }
      : {}),
    ...(group.moduleId ? { newIssueModuleId: group.moduleId } : {}),
  };
}
