// boardStateGroup (C135T2): pure board-side helpers for Group-by-State-groups
// mode. Board.tsx wires these into its existing dnd-kit + quick-add paths.
// Grouping semantics (single-appearance, canonical-six order, Unknown
// last and hidden when empty, "Show empty groups" synthesizes all six)
// come from groupByStateGroup.ts (C135T1) — see that module's header
// for the decided semantics. Kept framework-free so the drop/quick-add/
// count rules are cheaply unit-testable. Mirrors boardCreatedBy.ts
// with state groups substituted, EXCEPT for one deliberate divergence,
// forced by the data model (cycle-135 plan theme section, not a taste
// call): there is NO move pipeline at all for state groups. A group
// holds several states, so a cross-group drop has no single target
// state — picking "first state by sequence in the target group" as a
// silent move target would change an issue's workflow state on a drag
// the user meant as a regrouping, which is a behavior change, not a
// display transfer. So a board cross-column drop is a deliberate
// no-op (it mirrors the board created-by no-op precedent), and the
// groups carry NO target* fields. Within-column drags reorder by
// `sort_order` only, via the column's own card sequence (same stack
// math as the created-by path). Per-column quick-add IS enabled
// (unlike created-by): the default state is the first-by-sequence
// state in the group, so the new card STAYS in the column; a column
// with no states at all (possible under "Show empty groups"
// synthesis, or the Unknown column whose states are not loaded) has
// newStateId undefined, and Board.tsx renders its quick-add disabled
// via the dedicated disableQuickAdd prop (never via `disabled`, which
// would also kill card dragging — C134 lesson). State-group columns
// carry no color field: groups have no color — same rule as
// cycles/modules/releases/creators (name + count, no dot, no
// breakdown bar).

import { groupIssuesByStateGroup, stateGroupOf } from "./groupByStateGroup";
import type { Issue, IssueState } from "../../lib/types";

/** Droppable id prefix for state-group columns: `stategroup:<group>`. */
export const STATEGROUP_COLUMN_PREFIX = "stategroup:";
/** Sortable id prefix for cards in state-group mode:
 *  `stategroup-card:<group>:<issueId>`. Compound ids are kept for
 *  uniformity with the other board helpers even though
 *  single-appearance means no card ever appears twice, but the drop
 *  target still resolves WHICH column's card was hit. Issue ids are
 *  UUIDs (no colons) and group slugs are backend-fixed lowercase
 *  words (no colons), so the LAST colon always separates the issue
 *  id (same rule as cycles/labels/modules). */
export const STATEGROUP_CARD_PREFIX = "stategroup-card:";
/** Column key for the Unknown column (never a real group slug).
 *  groupIssuesByStateGroup keys the Unknown group
 *  "stategroup:unknown" internally; the board maps it to this short
 *  key so the droppable stays `stategroup:unknown` instead of
 *  doubling the prefix. */
export const STATEGROUP_UNKNOWN_COLUMN_KEY = "unknown";

export interface StateGroupBoardGroup {
  /** dnd-kit droppable id for the column (`stategroup:<group>`). */
  droppableId: string;
  /** Group slug ("triage", …, "cancelled", a defensive non-canonical
   *  group string) or "unknown" for the Unknown column. */
  columnKey: string;
  title: string;
  /** Group slug, or null for the Unknown column. */
  stateGroup: string | null;
  issues: Issue[];
  /** State stamped onto quick-added issues: the first-by-sequence
   *  state in the group (callers pass states sequence-sorted, as
   *  Board.tsx's `states` memo is). Undefined for the Unknown column
   *  and for groups with no states at all — Board.tsx renders those
   *  columns' quick-add disabled. */
  newStateId?: string;
}

/** State-group columns from the loaded issue rows: the six canonical
 *  groups in backend-fixed order, Unknown last and only when
 *  non-empty. With { includeEmptyGroups: true } ("Show empty groups"
 *  on), all six canonical groups are synthesized even with zero rows
 *  (closed set — unlike creators, where columns derive from loaded
 *  issues only); Unknown still renders only when it has rows, in
 *  both modes. States load with the page, so unlike created-by there
 *  is no pending-lookup flash risk and no resolution gate: unresolved
 *  states honestly collapse into Unknown via groupIssuesByStateGroup.
 *  A raw state id is NEVER a column label. */
export function buildStateGroupBoardGroups(
  issues: Issue[],
  stateGroupById: ReadonlyMap<string, string>,
  sortedStates: IssueState[],
  options?: { includeEmptyGroups?: boolean },
): StateGroupBoardGroup[] {
  return groupIssuesByStateGroup(issues, stateGroupById, options).map((g) => {
    const unknown = g.isUnknown;
    const columnKey = unknown ? STATEGROUP_UNKNOWN_COLUMN_KEY : g.group;
    return {
      droppableId: g.key,
      columnKey,
      title: g.title,
      stateGroup: unknown ? null : g.group,
      issues: g.issues,
      newStateId: unknown
        ? undefined
        : sortedStates.find((s) => s.group === g.group)?.id,
    };
  });
}

/** Sortable id for a card in a state-group column. */
export function stateGroupCardDragId(
  columnKey: string,
  issueId: string,
): string {
  return `${STATEGROUP_CARD_PREFIX}${columnKey}:${issueId}`;
}

/** Resolve a dnd-kit `over` id to the target state-group column's key.
 *  Handles both the column droppable (`stategroup:<key>`) and a card
 *  inside a column (`stategroup-card:<key>:<issueId>`). The card-prefix
 *  check MUST precede the column-prefix check: card ids start with
 *  `stategroup:` too. Returns null for non-state-group ids (state/
 *  priority/epic/assignee/label/module/cycle/release/created-by
 *  droppables, plain issue ids from sub-group-section unwrapping,
 *  unknown draggables). Note the Unknown column key ("unknown") is
 *  NOT the T1 group key ("stategroup:unknown") — see
 *  STATEGROUP_UNKNOWN_COLUMN_KEY. */
export function stateGroupDropColumnKey(overId: string): string | null {
  if (overId.startsWith(STATEGROUP_CARD_PREFIX)) {
    const rest = overId.slice(STATEGROUP_CARD_PREFIX.length);
    const sep = rest.lastIndexOf(":");
    return sep === -1 ? null : rest.slice(0, sep);
  }
  if (overId.startsWith(STATEGROUP_COLUMN_PREFIX)) {
    return overId.slice(STATEGROUP_COLUMN_PREFIX.length);
  }
  return null;
}

/** Issue id behind a state-group-mode sortable id. Identity for
 *  non-state-group ids, so it is safe to call on every active id. */
export function stateGroupDragIssueId(activeId: string): string {
  if (!activeId.startsWith(STATEGROUP_CARD_PREFIX)) return activeId;
  const rest = activeId.slice(STATEGROUP_CARD_PREFIX.length);
  const sep = rest.lastIndexOf(":");
  return sep === -1 ? activeId : rest.slice(sep + 1);
}

/** State-group column key behind a state-group-mode sortable id (the
 *  source column of a dragged card). Null for non-state-group ids and
 *  for malformed compound ids — callers treat null as "source column
 *  unknown" (the sub-group-section case, where cards carry subgroup
 *  ids instead) and fall back to the drop plan, which is a no-op
 *  anyway. */
export function stateGroupDragColumnKey(activeId: string): string | null {
  if (!activeId.startsWith(STATEGROUP_CARD_PREFIX)) return null;
  const rest = activeId.slice(STATEGROUP_CARD_PREFIX.length);
  const sep = rest.lastIndexOf(":");
  return sep === -1 ? null : rest.slice(0, sep);
}

export type StateGroupDropPlan =
  | { kind: "noop"; reason: "ambiguous-target" | "already-member" };

/** Cross-column drop semantics (the deliberate C135 divergence from
 *  every other board dimension except created-by): dropping a card
 *  into another state group's column is a NO-OP, always — a group
 *  holds several states, so there is no single target state to move
 *  to, and inventing one would silently change the issue's workflow
 *  state. No state change, no membership call, and no sort PATCH
 *  that implies a move is ever issued. Dropping into Unknown is the
 *  same no-op (there is no "un-state" operation either). A drag
 *  whose source column IS the target column never reaches this plan:
 *  Board.tsx resolves it as a within-column reorder (sort_order
 *  only) from the dragged card's compound id before consulting the
 *  plan (C131T1 trap). The already-member reason covers the
 *  sub-group-section case, where the source column is unknown and
 *  the plan is the only guard: dropping a card onto its own group's
 *  column through a subgroup card is still a no-op, never a move.
 *  State-group drops never change state, so no C68 workflow
 *  transition guard applies (the guard path in Board.tsx is
 *  state-grouping-only — verified, see dropBlocked). */
export function planStateGroupDrop(
  columnKey: string,
  issue: Issue,
  stateGroupById: ReadonlyMap<string, string>,
): StateGroupDropPlan {
  const current = stateGroupOf(issue, stateGroupById);
  if (
    current === columnKey ||
    (current === null && columnKey === STATEGROUP_UNKNOWN_COLUMN_KEY)
  ) {
    return { kind: "noop", reason: "already-member" };
  }
  return { kind: "noop", reason: "ambiguous-target" };
}

// Deliberately NO move executor and NO quick-add / quick-expand plans
// in this module: a state-group move has no endpoint to call (no
// single target state exists), and a state-group column's quick-add
// needs no attach — Board.tsx's generic quickCreate path stamps the
// column's newStateId (first-by-sequence state in the group), which
// keeps the new card in the column, and the default quick-expand
// carries title + that state. Adding either helper would imply a
// pipeline the data model forbids or a bridge that already exists.
