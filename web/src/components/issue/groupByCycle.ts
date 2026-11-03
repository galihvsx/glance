// groupByCycle (C132T1): pure grouping logic for the issue list's
// Group-by-Cycles display mode. Kept framework-free so it is cheaply
// unit-testable; Issues.tsx wires it into the existing grouping path.
// Mirrors groupByModule.ts (C131T0) with cycles substituted, EXCEPT
// for one deliberate difference: cycles are SINGLE-APPEARANCE.
// Issue.project_structure.cycle is NamedRef | null — exactly ONE
// cycle per issue (the server resolves the alphabetically-first cycle
// name among the issue's cycle_issues rows) — so an issue appears
// under exactly one cycle group or Ungrouped, never under several.
//
// Decided semantics (cycle-132 plan, mirroring groupByModule.ts):
// - SINGLE-APPEARANCE RULE: an issue appears under exactly ONE
//   cycle group — its displayed cycle — or Ungrouped. There is no
//   second appearance to track.
// - No status-based exclusion concept exists for cycles from row
//   data: the row's NamedRef carries no status (Cycle.status lives
//   on the Cycle type, not the row), so completed cycles group like
//   any other cycle this cycle — do not invent an exclusion.
// - "Ungrouped" (issues with no cycle) is ALWAYS the last group
//   and is HIDDEN when empty (callers only get this entry when it has
//   members — same contract as groupByEpic / groupByAssignee /
//   groupByLabel / groupByModule).
// - Group title = the cycle name from the row (never a fabricated
//   name). Cycles ride the row as project_structure.cycle
//   (NamedRef | null), so no cycle lookup and no extra fetch are
//   needed. When rows disagree on one cycle's name, the
//   FIRST-appearance row wins — groups are keyed by cycle id and the
//   first row's value is kept, never reconciled across rows.
// - Cycle groups carry NO color field: NamedRef carries id + name
//   only, so no color slot exists to fill — same rule as modules.

import type { Issue, NamedRef } from "../../lib/types";

export interface CycleIssueGroup {
  key: string;
  title: string;
  issues: Issue[];
  isUngrouped: boolean;
}

export const UNGROUPED_CYCLE_KEY = "cycle:ungrouped";

/** The displayed cycle on one issue row (defensive: rows without the
 *  field, or without project_structure, group as ungrouped, never
 *  crash). Null covers both a null cycle and a missing field. */
export function cycleOf(issue: Issue): NamedRef | null {
  return issue.project_structure?.cycle ?? null;
}

/** Group issue rows by displayed cycle. Cycle groups keep
 *  first-appearance order across the input list; Ungrouped is last. */
export function groupIssuesByCycle(issues: Issue[]): CycleIssueGroup[] {
  const byCycle = new Map<string, { name: string; issues: Issue[] }>();
  const ungrouped: Issue[] = [];
  for (const issue of issues) {
    const cycle = cycleOf(issue);
    if (cycle === null) {
      ungrouped.push(issue);
      continue;
    }
    // Single-appearance: the issue is appended to exactly ONE group.
    let group = byCycle.get(cycle.id);
    if (!group) {
      group = { name: cycle.name, issues: [] };
      byCycle.set(cycle.id, group);
    }
    group.issues.push(issue);
  }
  const groups: CycleIssueGroup[] = [];
  for (const [id, g] of byCycle) {
    groups.push({
      key: id,
      title: g.name,
      issues: g.issues,
      isUngrouped: false,
    });
  }
  // Ungrouped is always last and hidden when empty (callers only get
  // this entry when it has members).
  if (ungrouped.length > 0) {
    groups.push({
      key: UNGROUPED_CYCLE_KEY,
      title: "Ungrouped",
      issues: ungrouped,
      isUngrouped: true,
    });
  }
  return groups;
}
