// boardCreatedBy (C134T2): pure board-side helpers for Group-by-Created-by
// mode. Board.tsx wires these into its existing dnd-kit + quick-add paths.
// Grouping semantics (single-appearance, Unknown-last-hidden-when-empty,
// first-appearance order, labels from a members lookup map) come from
// groupByCreatedBy.ts (C134T1) — see that module's header for the decided
// semantics. Kept framework-free so the drop/quick-add/count rules are
// cheaply unit-testable. Mirrors boardRelease.ts with creators substituted,
// EXCEPT for one deliberate divergence, forced by the data model
// (cycle-134 plan theme section, not a taste call): there is NO move
// pipeline at all for created-by. `created_by` is immutable — no
// endpoint reassigns a creator — so a board cross-column drop is a
// deliberate no-op (it mirrors the board Ungrouped no-op precedent:
// removal to no-creator on the board is meaningless, since every issue
// has a creator), and per-column quick-add is DISABLED (the create form
// has no creator picker and `created_by` is server-set to the actor,
// so a row quick-added inside another creator's column would
// immediately leave that column — misleading; the global add stays
// available). Expand-to-full-form therefore carries nothing for
// created-by: there is deliberately NO planCreatedByQuickAdd /
// planBoardCreatedByQuickExpand in this module, and Board.tsx renders
// creator columns with quick-add disabled rather than planning one.
// Within-column drags reorder by `sort_order` only, via the column's
// own card sequence (same stack math as the release path).
// Creator columns carry no color field: members carry no color —
// same rule as cycles/modules/releases (name + count, no dot).

import {
  groupIssuesByCreatedBy,
  type CreatorLookupEntry,
} from "./groupByCreatedBy";
import type { Issue } from "../../lib/types";

/** Droppable id prefix for creator columns: `createdby:<columnKey>`. */
export const CREATEDBY_COLUMN_PREFIX = "createdby:";
/** Sortable id prefix for cards in created-by mode:
 *  `createdby-card:<columnKey>:<issueId>`. Compound ids are kept for uniformity
 *  with the other board helpers even though single-appearance means no
 *  card ever appears twice, but the drop target still resolves WHICH
 *  column's card was hit. Issue ids are UUIDs (no colons), so the LAST
 *  colon always separates the issue id (same rule as cycles/labels/modules).
 *  Creator column keys are user ids (UUIDs, no colons) or "unknown" (see
 *  below), so the last-colon rule holds for the column key too. */
export const CREATEDBY_CARD_PREFIX = "createdby-card:";
/** Column key for the Unknown column (never a real user id — user ids
 *  are UUIDs). groupIssuesByCreatedBy keys the Unknown group
 *  "createdby:unknown" internally; the board maps it to this short key
 *  so the droppable stays `createdby:unknown` instead of doubling the
 *  prefix. */
export const CREATEDBY_UNKNOWN_COLUMN_KEY = "unknown";

export interface CreatedByBoardGroup {
  /** dnd-kit droppable id for the column. */
  droppableId: string;
  /** "unknown" for the Unknown column, else the creator's user id. */
  columnKey: string;
  title: string;
  /** Creator user id, or null for the Unknown column. */
  creatorId: string | null;
  issues: Issue[];
  /** State stamped onto quick-added issues (mirrors the release columns:
   *  the first sorted state). Carried for shape uniformity only —
   *  creator columns render with quick-add DISABLED, so this stamp is
   *  never consumed under created-by grouping. */
  newStateId?: string;
}

/** Creator columns from the loaded issue rows: one column per creator
 *  (first-appearance order), Unknown last and only when non-empty.
 *  "Show empty groups" synthesizes nothing here: columns derive from
 *  loaded issues only (same boundary as T1's list grouping — there is
 *  no creators-list fetch to populate empty columns). The label for a
 *  creator comes from the members lookup map passed in (`name ??
 *  email`); a raw user id is NEVER a column title — unresolved
 *  creators collapse into the single Unknown column via
 *  groupIssuesByCreatedBy. Callers gate on members resolution (see
 *  Board.tsx): while the members query is pending they pass no groups
 *  at all, so this builder never runs on an empty map at first paint
 *  and never flashes every column as "Unknown". */
export function buildCreatedByBoardGroups(
  issues: Issue[],
  membersById: ReadonlyMap<string, CreatorLookupEntry>,
  firstStateId?: string,
): CreatedByBoardGroup[] {
  return groupIssuesByCreatedBy(issues, membersById).map((g) => {
    const unknown = g.isUnknown;
    const columnKey = unknown
      ? CREATEDBY_UNKNOWN_COLUMN_KEY
      : g.key;
    return {
      droppableId: `${CREATEDBY_COLUMN_PREFIX}${columnKey}`,
      columnKey,
      title: g.title,
      creatorId: unknown ? null : g.key,
      issues: g.issues,
      newStateId: firstStateId,
    };
  });
}

/** Sortable id for a card in a creator column. */
export function createdByCardDragId(columnKey: string, issueId: string): string {
  return `${CREATEDBY_CARD_PREFIX}${columnKey}:${issueId}`;
}

/** Resolve a dnd-kit `over` id to the target creator column's key.
 *  Handles both the column droppable (`createdby:<key>`) and a card
 *  inside a column (`createdby-card:<key>:<issueId>`). The card-prefix
 *  check MUST precede the column-prefix check: card ids start with
 *  `createdby:` too. Returns null for non-created-by ids (state/
 *  priority/epic/assignee/label/module/cycle/release droppables, plain
 *  issue ids from sub-group-section unwrapping, unknown draggables).
 *  Note the Unknown column key ("unknown") is NOT the T1 group key
 *  ("createdby:unknown") — see CREATEDBY_UNKNOWN_COLUMN_KEY. */
export function createdByDropColumnKey(overId: string): string | null {
  if (overId.startsWith(CREATEDBY_CARD_PREFIX)) {
    const rest = overId.slice(CREATEDBY_CARD_PREFIX.length);
    const sep = rest.lastIndexOf(":");
    return sep === -1 ? null : rest.slice(0, sep);
  }
  if (overId.startsWith(CREATEDBY_COLUMN_PREFIX)) {
    return overId.slice(CREATEDBY_COLUMN_PREFIX.length);
  }
  return null;
}

/** Issue id behind a created-by-mode sortable id. Identity for
 *  non-created-by ids, so it is safe to call on every active id. */
export function createdByDragIssueId(activeId: string): string {
  if (!activeId.startsWith(CREATEDBY_CARD_PREFIX)) return activeId;
  const rest = activeId.slice(CREATEDBY_CARD_PREFIX.length);
  const sep = rest.lastIndexOf(":");
  return sep === -1 ? activeId : rest.slice(sep + 1);
}

/** Creator column key behind a created-by-mode sortable id (the source
 *  column of a dragged card). Null for non-created-by ids and for
 *  malformed compound ids — callers treat null as "source column
 *  unknown" (the sub-group-section case, where cards carry subgroup ids
 *  instead) and fall back to the drop plan, which is a no-op anyway. */
export function createdByDragColumnKey(activeId: string): string | null {
  if (!activeId.startsWith(CREATEDBY_CARD_PREFIX)) return null;
  const rest = activeId.slice(CREATEDBY_CARD_PREFIX.length);
  const sep = rest.lastIndexOf(":");
  return sep === -1 ? null : rest.slice(0, sep);
}

export type CreatedByDropPlan =
  | { kind: "noop"; reason: "immutable" | "already-member" };

/** Cross-column drop semantics (the deliberate C134 divergence from
 *  every other board dimension): dropping a card into another creator's
 *  column is a NO-OP, always — `created_by` is immutable and no
 *  endpoint reassigns a creator, so there is no move to plan, no
 *  membership call to issue, and no sort PATCH that implies a move.
 *  Dropping into Unknown is the same no-op (there is no "un-create"
 *  operation either). A drag whose source column IS the target column
 *  never reaches this plan: Board.tsx resolves it as a within-column
 *  reorder (sort_order only) from the dragged card's compound id
 *  before consulting the plan (C131T1 trap). The already-member
 *  reason covers the sub-group-section case, where the source column
 *  is unknown and the plan is the only guard: dropping a card onto its
 *  own creator's column through a subgroup card is still a no-op, never
 *  a move. Created-by drops never change state, so no C68 workflow
 *  transition guard applies (the guard path in Board.tsx is
 *  state-grouping-only — verified, see dropBlocked). */
export function planCreatedByDrop(
  columnKey: string,
  issue: Issue,
): CreatedByDropPlan {
  if (columnKey === issue.created_by) {
    return { kind: "noop", reason: "already-member" };
  }
  return { kind: "noop", reason: "immutable" };
}

// Deliberately NO move executor and NO quick-add / quick-expand plans
// in this module: a created-by move has no endpoint to call, and a
// creator-column quick-add is disabled at the BoardColumn level (see
// Board.tsx's disableQuickAdd), not planned. Adding either helper
// would imply a pipeline the data model forbids.
