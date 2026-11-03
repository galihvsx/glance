// boardRelease (C133T2): pure board-side helpers for Group-by-Releases
// mode. Board.tsx wires these into its existing dnd-kit + quick-add paths.
// Grouping semantics (single-appearance, Ungrouped-last-hidden-when-empty,
// first-appearance order, names from the row) come from
// groupByRelease.ts (C133T1) — see that module's header for the decided
// semantics. Kept framework-free so the drop/quick-add/count rules are
// cheaply unit-testable. Mirrors boardCycle.ts with releases substituted,
// EXCEPT for one deliberate divergence, forced by the data model
// (cycle-133 plan theme section, not a taste call): a release move is
// a SINGLE assign, not add-then-remove. Release membership is one
// column (`release_id`): POST .../releases/{target}/issues OVERWRITES
// it, so a second DELETE of the source would be a no-op at best (the
// row is no longer on the source) and misleading at worst. There is
// therefore no dual-state honesty case on a release move: assign
// failure means nothing changed; assign success means the row is
// exactly on the target. Dropping into Ungrouped stays a deliberate
// no-op like every other board dimension (removal to no-release on
// the board is out of scope; the list Ungrouped header does the
// removal). Releases have NO color (NamedRef carries id + name only),
// so release columns carry no color field at all — no invented color,
// no hash-derived color, no lookup. The compound card-id shape is kept
// for uniformity with the other board helpers even though
// single-appearance means no card ever appears twice.

import { groupIssuesByRelease, releaseOf } from "./groupByRelease";
import type { Issue } from "../../lib/types";

/** Droppable id prefix for release columns: `release:<releaseId>`. */
export const RELEASE_COLUMN_PREFIX = "release:";
/** Sortable id prefix for cards in release mode:
 *  `release-card:<releaseId>:<issueId>`. Compound ids are kept for uniformity
 *  with the other board helpers (single-appearance means no card ever
 *  appears twice, but the drop target still resolves WHICH column's card
 *  was hit). Issue ids are UUIDs (no colons), so the LAST colon always
 *  separates the issue id (same rule as cycles/labels/modules). */
export const RELEASE_CARD_PREFIX = "release-card:";
/** Column key for the Ungrouped column (never a real release id). */
export const RELEASE_UNGROUPED_COLUMN_KEY = "ungrouped";

export interface ReleaseBoardGroup {
  /** dnd-kit droppable id for the column. */
  droppableId: string;
  /** "ungrouped" for the Ungrouped column, else the release's id. */
  columnKey: string;
  title: string;
  /** Release id, or null for the Ungrouped column. */
  releaseId: string | null;
  issues: Issue[];
  /** State stamped onto quick-added issues (mirrors the cycle columns:
   *  the first sorted state). */
  newStateId?: string;
}

/** Release columns from the loaded issue rows: one column per release
 *  (first-appearance order), Ungrouped last and only when non-empty.
 *  "Show empty groups" synthesizes nothing here: columns derive from
 *  loaded issues only (same boundary as T1's list grouping — there is
 *  no releases-list fetch to populate empty columns). */
export function buildReleaseBoardGroups(
  issues: Issue[],
  firstStateId?: string,
): ReleaseBoardGroup[] {
  return groupIssuesByRelease(issues).map((g) => {
    const ungrouped = g.isUngrouped;
    const columnKey = ungrouped ? RELEASE_UNGROUPED_COLUMN_KEY : g.key;
    return {
      droppableId: `${RELEASE_COLUMN_PREFIX}${columnKey}`,
      columnKey,
      title: g.title,
      releaseId: ungrouped ? null : g.key,
      issues: g.issues,
      newStateId: firstStateId,
    };
  });
}

/** Sortable id for a card in a release column. */
export function releaseCardDragId(columnKey: string, issueId: string): string {
  return `${RELEASE_CARD_PREFIX}${columnKey}:${issueId}`;
}

/** Resolve a dnd-kit `over` id to the target release column's key.
 *  Handles both the column droppable (`release:<key>`) and a card
 *  inside a column (`release-card:<key>:<issueId>`). The card-prefix
 *  check MUST precede the column-prefix check: card ids start with
 *  `release:` too. Returns null for non-release ids (state/priority/epic/
 *  assignee/label/module/cycle droppables, plain issue ids from
 *  sub-group-section unwrapping, unknown draggables). */
export function releaseDropColumnKey(overId: string): string | null {
  if (overId.startsWith(RELEASE_CARD_PREFIX)) {
    const rest = overId.slice(RELEASE_CARD_PREFIX.length);
    const sep = rest.lastIndexOf(":");
    return sep === -1 ? null : rest.slice(0, sep);
  }
  if (overId.startsWith(RELEASE_COLUMN_PREFIX)) {
    return overId.slice(RELEASE_COLUMN_PREFIX.length);
  }
  return null;
}

/** Issue id behind a release-mode sortable id. Identity for
 *  non-release ids, so it is safe to call on every active id. */
export function releaseDragIssueId(activeId: string): string {
  if (!activeId.startsWith(RELEASE_CARD_PREFIX)) return activeId;
  const rest = activeId.slice(RELEASE_CARD_PREFIX.length);
  const sep = rest.lastIndexOf(":");
  return sep === -1 ? activeId : rest.slice(sep + 1);
}

/** Release id behind a release-mode sortable id (the source column of
 *  a dragged card). Null for non-release ids and for malformed
 *  compound ids — callers treat null as "source column unknown" (the
 *  sub-group-section case, where cards carry subgroup ids instead)
 *  and fall back to the dragged row's displayed release. */
export function releaseDragColumnKey(activeId: string): string | null {
  if (!activeId.startsWith(RELEASE_CARD_PREFIX)) return null;
  const rest = activeId.slice(RELEASE_CARD_PREFIX.length);
  const sep = rest.lastIndexOf(":");
  return sep === -1 ? null : rest.slice(0, sep);
}

export type ReleaseDropPlan =
  | {
      kind: "move";
      /** Target release id (the drop column). No source field: the
       *  single assign overwrites `release_id`, so no source
       *  removal is ever issued (see the module header). */
      releaseId: string;
    }
  | { kind: "noop"; reason: "ungrouped" | "already-member" };

/** Cross-column drop semantics (the deliberate C133 divergence from
 *  the cycle move pipeline): dropping a card into a release column
 *  MOVES the issue to that release via a SINGLE assign — assign
 *  overwrites `release_id`, so no DELETE of the source follows.
 *  Dropping into Ungrouped is a deliberate no-op: removal to
 *  no-release on the board is out of scope. Release drops never
 *  change state, so no C68 workflow transition guard applies (the
 *  guard path in Board.tsx is state-grouping-only — verified, see
 *  dropBlocked). NOTE: this plan is for cross-column drops only — a
 *  drag whose source column IS the target column is a within-column
 *  reorder (Board.tsx resolves that from the dragged card's compound
 *  id before consulting this plan), not an already-member no-op
 *  (C131T1 trap). */
export function planReleaseDrop(
  columnKey: string,
  issue: Issue,
): ReleaseDropPlan {
  if (columnKey === RELEASE_UNGROUPED_COLUMN_KEY) {
    return { kind: "noop", reason: "ungrouped" };
  }
  const current = releaseOf(issue);
  if (current?.id === columnKey) {
    return { kind: "noop", reason: "already-member" };
  }
  return { kind: "move", releaseId: columnKey };
}

/** Membership endpoint for a release move, injected so the ordering
 *  and failure semantics are unit-testable without a dnd-kit drag
 *  (no board-level pointer-drag simulation exists in this repo —
 *  same boundary as the cycle/module drop tests). Board.tsx passes
 *  the real api call (POST .../releases/:id/issues with
 *  {issue_ids}). There is deliberately NO remove dep: a release move
 *  issues no DELETE, ever. */
export interface ReleaseMoveDeps {
  /** POST .../releases/{releaseId}/issues {issue_ids: [id]} (idempotent,
   *  overwrites release_id). */
  assignToRelease: (releaseId: string, issueId: string) => Promise<unknown>;
}

export type ReleaseMoveResult =
  | { ok: true }
  | { ok: false; stage: "assign"; error: unknown };

/** Execute a release move: exactly ONE assign call. An assign failure
 *  means nothing changed (the row keeps its old release_id) — the
 *  caller surfaces it honestly and invalidates so the board shows
 *  server state. Never retries silently, never changes state, never
 *  touches sort_order, never issues a DELETE. */
export async function executeReleaseMove(
  deps: ReleaseMoveDeps,
  plan: Extract<ReleaseDropPlan, { kind: "move" }>,
  issueId: string,
): Promise<ReleaseMoveResult> {
  try {
    await deps.assignToRelease(plan.releaseId, issueId);
  } catch (error) {
    return { ok: false, stage: "assign", error };
  }
  return { ok: true };
}

/** Quick-add plan (mirrors board cycles): a release column's quick-add
 *  creates the issue (with the column's state stamp) AND assigns it to
 *  the column's release; Ungrouped quick-add is a plain create. */
export function planReleaseQuickAdd(group: {
  newStateId?: string;
  releaseId?: string | null;
}): { stateId?: string; attachReleaseId: string | null } {
  return {
    stateId: group.newStateId,
    attachReleaseId: group.releaseId ?? null,
  };
}

/** Expand-to-full-form state for a board release column's quick-add
 *  (the router state Board.tsx navigates to the list page with).
 *  Release columns carry the release id so the list page can attach it
 *  after create — the create dialog has no release field, so it rides
 *  pendingReleaseRef exactly like the cycle carry (see Issues.tsx's
 *  createMutation onSuccess, C133T1: T1 built the newIssueReleaseId
 *  bridge for T2's board expand, so NO list-page edits are needed in
 *  T2); Ungrouped columns carry no release, so the expand keeps title +
 *  column state only. */
export function planBoardReleaseQuickExpand(
  group: {
    newStateId?: string;
    newPriority?: number;
    releaseId?: string | null;
  },
  name: string,
): {
  newIssue: true;
  newIssueName: string;
  newIssueStateId?: string;
  newIssuePriority?: number;
  newIssueReleaseId?: string;
} {
  return {
    newIssue: true,
    newIssueName: name,
    ...(group.newStateId ? { newIssueStateId: group.newStateId } : {}),
    ...(group.newPriority !== undefined
      ? { newIssuePriority: group.newPriority }
      : {}),
    ...(group.releaseId ? { newIssueReleaseId: group.releaseId } : {}),
  };
}
