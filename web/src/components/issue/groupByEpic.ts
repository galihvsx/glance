// groupByEpic (C72T0): pure grouping logic for the issue list's
// Group-by-Epic display mode. Kept framework-free so it is cheaply
// unit-testable; Issues.tsx wires it into the existing grouping path.
//
// Decided semantics (cycle-72 plan):
// - MULTI-APPEARANCE RULE (deliberate): an issue attached to N LIVE
//   epics appears under EVERY epic group. This is the coherent
//   extension of C71's "multi-epic first-class, never enforce a single
//   parent" ruling — dropping one side's visibility would silently hide
//   work from an epic's view.
// - ARCHIVED EXCLUSION: archived epics are excluded from grouping
//   (coherent with C71's hidden-from-default-list rule). An issue whose
//   only epics are archived lands in Ungrouped.
// - "Ungrouped" (issues with no live epic membership) is ALWAYS the
//   last group and is HIDDEN when empty.
// - Progress counts come from the caller-supplied state-group resolver —
//   computed client-side from the already-loaded members, NO extra
//   fetches (the C70/C71 derived-data discipline).

import { EPIC_GROUP_ORDER } from "../initiatives/stateGroups";
import type { EpicRef, Issue } from "../../lib/types";

export interface EpicIssueGroup {
  key: string;
  title: string;
  issues: Issue[];
  /** Member counts per state group (only EPIC_GROUP_ORDER groups feed
   *  the breakdown bar; triage members — which never live in epics per
   *  C71 — would simply be uncounted by the bar). */
  byGroup: Record<string, number>;
  isUngrouped: boolean;
}

export const UNGROUPED_EPIC_KEY = "epic:ungrouped";

/** Live (non-archived) epic memberships for one issue row. */
export function liveEpics(issue: Issue): EpicRef[] {
  return (issue.epics ?? []).filter((e) => !e.archived_at);
}

function countByGroup(
  issues: Issue[],
  stateGroupOf: (issue: Issue) => string | undefined,
): Record<string, number> {
  const byGroup: Record<string, number> = {};
  for (const issue of issues) {
    const g = stateGroupOf(issue);
    if (g !== undefined && EPIC_GROUP_ORDER.includes(g)) {
      byGroup[g] = (byGroup[g] ?? 0) + 1;
    }
  }
  return byGroup;
}

/** Group issue rows by live epic membership. Epic groups keep
 *  first-appearance order across the input list; Ungrouped is last. */
export function groupIssuesByEpic(
  issues: Issue[],
  stateGroupOf: (issue: Issue) => string | undefined,
): EpicIssueGroup[] {
  const byEpic = new Map<string, { name: string; issues: Issue[] }>();
  const ungrouped: Issue[] = [];
  for (const issue of issues) {
    const live = liveEpics(issue);
    if (live.length === 0) {
      ungrouped.push(issue);
      continue;
    }
    // Multi-appearance: the same issue is appended to EVERY live
    // epic's group — see the module comment above.
    for (const epic of live) {
      let group = byEpic.get(epic.id);
      if (!group) {
        group = { name: epic.name, issues: [] };
        byEpic.set(epic.id, group);
      }
      group.issues.push(issue);
    }
  }
  const groups: EpicIssueGroup[] = [];
  for (const [id, g] of byEpic) {
    groups.push({
      key: id,
      title: g.name,
      issues: g.issues,
      byGroup: countByGroup(g.issues, stateGroupOf),
      isUngrouped: false,
    });
  }
  // Ungrouped is always last and hidden when empty (callers only get
  // this entry when it has members).
  if (ungrouped.length > 0) {
    groups.push({
      key: UNGROUPED_EPIC_KEY,
      title: "Ungrouped",
      issues: ungrouped,
      byGroup: countByGroup(ungrouped, stateGroupOf),
      isUngrouped: true,
    });
  }
  return groups;
}
