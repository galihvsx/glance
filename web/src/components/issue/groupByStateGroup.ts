// groupByStateGroup (C135T1): pure grouping logic for the issue
// list's Group-by-State-groups display mode. Kept framework-free so
// it is cheaply unit-testable; Issues.tsx wires it into the existing
// grouping path. Mirrors groupByRelease.ts (C133T1) /
// groupByCreatedBy.ts (C134T1) shape, with the one honest twist
// the cycle-135 plan names: the group is a ROLL-UP, not a row
// field — the row carries state_id, and the group resolves through
// a state-by-id lookup (state id -> group string) passed in as an
// argument, mirroring how groupIssuesByEpic takes a state-group
// accessor in Issues.tsx today.
//
// Decided semantics (cycle-135 plan, mirroring groupByCreatedBy.ts):
// - SINGLE-APPEARANCE RULE: an issue appears under exactly ONE
//   state group — its state's group — never under several. Every
//   issue has exactly one state_id, and every state has exactly
//   one group, so one group per issue by construction.
// - The canonical group set and order are FIXED by the backend
//   itself (stateGroupOrder / snapshotGroups in
//   internal/service/dashboard.go, internal/service/cycle.go):
//   triage, backlog, unstarted, started, completed, cancelled.
//   Groups always emit in that order, never in first-appearance
//   order (unlike created-by, where the set is open-ended).
// - Group keys are `stategroup:<group>`; headers render name +
//   count with NO color dot (groups have no color) and NO
//   breakdown bar (the helper carries no byGroup).
// - Unresolved states (a state_id with no loaded state —
//   defensive only; states with issues cannot be deleted)
//   collapse into ONE "Unknown" group, ALWAYS last, HIDDEN when
//   empty (callers only get this entry when it has members —
//   same contract as groupByCreatedBy's Unknown). A raw state id
//   is NEVER a group label.
// - "Show empty groups" synthesizes all six canonical groups
//   (closed set — unlike created-by, where groups derive from
//   loaded issues only): pass { includeEmptyGroups: true } and
//   every canonical group is emitted even with zero rows. Unknown
//   still renders only when it has rows, in both modes.
// - Defensive-only: a state whose group string is outside the
//   canonical six (cannot happen from the backend today) forms
//   its own group, after the canonical six and before Unknown,
//   labelled with the backend's own group string — the row must
//   still appear exactly once, under a truthful label, rather
//   than being dropped or mislabelled Unknown.
// - NO move semantics live here: a group holds several states,
//   so a cross-group drop has no single target state — the page
//   sets no drop-target fields on these groups and the headers
//   are NOT drop targets (see Issues.tsx).

import type { Issue } from "../../lib/types";

export interface StateGroupIssueGroup {
  key: string;
  title: string;
  /** Canonical group slug ("triage", …, "cancelled"), the
   *  backend's own group string for a non-canonical group, or
   *  "unknown" for the Unknown group. The page maps this to the
   *  group's quick-add default state (first-by-sequence state in
   *  the group); Unknown has no such state. */
  group: string;
  issues: Issue[];
  isUnknown: boolean;
}

/** Backend-fixed canonical order (stateGroupOrder). */
export const STATE_GROUP_ORDER = [
  "triage",
  "backlog",
  "unstarted",
  "started",
  "completed",
  "cancelled",
] as const;

export type CanonicalStateGroup = (typeof STATE_GROUP_ORDER)[number];

export const STATE_GROUP_LABELS: Record<CanonicalStateGroup, string> = {
  triage: "Triage",
  backlog: "Backlog",
  unstarted: "Unstarted",
  started: "Started",
  completed: "Completed",
  cancelled: "Cancelled",
};

const CANONICAL_GROUPS = new Set<string>(STATE_GROUP_ORDER);

export const UNKNOWN_STATE_GROUP_KEY = "stategroup:unknown";

/** Key for one state group: `stategroup:<group>`. */
export function stateGroupKey(group: string): string {
  return `stategroup:${group}`;
}

/** The state group for one issue row (defensive: a row with a
 *  missing/empty state_id, or a state_id with no loaded state,
 *  resolves to null and groups as Unknown, never crashes, never
 *  labels by id). */
export function stateGroupOf(
  issue: Issue,
  stateGroupById: ReadonlyMap<string, string>,
): string | null {
  const stateId = issue.state_id;
  if (!stateId) return null;
  return stateGroupById.get(stateId) ?? null;
}

/** Group issue rows by state group. Canonical groups always emit
 *  in backend-fixed order; Unknown is last and hidden when empty.
 *  With { includeEmptyGroups: true }, all six canonical groups are
 *  emitted even when they hold no rows (Unknown still only when
 *  it has rows). */
export function groupIssuesByStateGroup(
  issues: Issue[],
  stateGroupById: ReadonlyMap<string, string>,
  options?: { includeEmptyGroups?: boolean },
): StateGroupIssueGroup[] {
  const includeEmptyGroups = options?.includeEmptyGroups ?? false;
  const byGroup = new Map<string, Issue[]>();
  for (const g of STATE_GROUP_ORDER) byGroup.set(g, []);
  const extraGroups = new Map<string, Issue[]>();
  const unknown: Issue[] = [];
  for (const issue of issues) {
    const group = stateGroupOf(issue, stateGroupById);
    if (group === null) {
      unknown.push(issue);
      continue;
    }
    // Single-appearance: the issue is appended to exactly ONE group.
    if (CANONICAL_GROUPS.has(group)) {
      byGroup.get(group)!.push(issue);
    } else {
      let bucket = extraGroups.get(group);
      if (!bucket) {
        bucket = [];
        extraGroups.set(group, bucket);
      }
      bucket.push(issue);
    }
  }
  const groups: StateGroupIssueGroup[] = [];
  for (const g of STATE_GROUP_ORDER) {
    const rows = byGroup.get(g)!;
    if (rows.length > 0 || includeEmptyGroups) {
      groups.push({
        key: stateGroupKey(g),
        title: STATE_GROUP_LABELS[g],
        group: g,
        issues: rows,
        isUnknown: false,
      });
    }
  }
  // Non-canonical groups (defensive only) keep first-appearance
  // order, after the canonical six.
  for (const [g, rows] of extraGroups) {
    groups.push({
      key: stateGroupKey(g),
      title: g,
      group: g,
      issues: rows,
      isUnknown: false,
    });
  }
  // Unknown is always last and hidden when empty (callers only get
  // this entry when it has members).
  if (unknown.length > 0) {
    groups.push({
      key: UNKNOWN_STATE_GROUP_KEY,
      title: "Unknown",
      group: "unknown",
      issues: unknown,
      isUnknown: true,
    });
  }
  return groups;
}
