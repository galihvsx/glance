// groupByRelease (C133T1): pure grouping logic for the issue list's
// Group-by-Releases display mode. Kept framework-free so it is cheaply
// unit-testable; Issues.tsx wires it into the existing grouping path.
// Mirrors groupByCycle.ts (C132T1) with releases substituted — release
// is SINGLE-VALUE on the row, and more strictly so than cycle:
// Issue.project_structure.release is NamedRef | null, resolved
// server-side from the issue's own nullable release_id column — not
// a junction table, not a first-by-name pick — so an issue appears
// under exactly one release group or Ungrouped, never under several.
//
// Decided semantics (cycle-133 plan, mirroring groupByCycle.ts):
// - SINGLE-APPEARANCE RULE: an issue appears under exactly ONE
//   release group — its displayed release — or Ungrouped. There is no
//   second appearance to track.
// - No status-based exclusion concept exists for releases from row
//   data: the row's NamedRef carries no status (Release.status lives
//   on the Release type, not the row), so released releases group like
//   any other release this cycle — do not invent an exclusion.
// - "Ungrouped" (issues with no release) is ALWAYS the last group
//   and is HIDDEN when empty (callers only get this entry when it has
//   members — same contract as groupByCycle / groupByEpic /
//   groupByAssignee / groupByLabel / groupByModule).
// - Group title = the release name from the row (never a fabricated
//   name). Releases ride the row as project_structure.release
//   (NamedRef | null), so no release lookup and no extra fetch are
//   needed. When rows disagree on one release's name, the
//   FIRST-appearance row wins — groups are keyed by release id and the
//   first row's value is kept, never reconciled across rows.
// - Release groups carry NO color field: NamedRef carries id + name
//   only, so no color slot exists to fill — same rule as cycles/modules.

import type { Issue, NamedRef } from "../../lib/types";

export interface ReleaseIssueGroup {
  key: string;
  title: string;
  issues: Issue[];
  isUngrouped: boolean;
}

export const UNGROUPED_RELEASE_KEY = "release:ungrouped";

/** The displayed release on one issue row (defensive: rows without the
 *  field, or without project_structure, group as ungrouped, never
 *  crash). Null covers both a null release and a missing field. */
export function releaseOf(issue: Issue): NamedRef | null {
  return issue.project_structure?.release ?? null;
}

/** Group issue rows by displayed release. Release groups keep
 *  first-appearance order across the input list; Ungrouped is last. */
export function groupIssuesByRelease(issues: Issue[]): ReleaseIssueGroup[] {
  const byRelease = new Map<string, { name: string; issues: Issue[] }>();
  const ungrouped: Issue[] = [];
  for (const issue of issues) {
    const release = releaseOf(issue);
    if (release === null) {
      ungrouped.push(issue);
      continue;
    }
    // Single-appearance: the issue is appended to exactly ONE group.
    let group = byRelease.get(release.id);
    if (!group) {
      group = { name: release.name, issues: [] };
      byRelease.set(release.id, group);
    }
    group.issues.push(issue);
  }
  const groups: ReleaseIssueGroup[] = [];
  for (const [id, g] of byRelease) {
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
      key: UNGROUPED_RELEASE_KEY,
      title: "Ungrouped",
      issues: ungrouped,
      isUngrouped: true,
    });
  }
  return groups;
}
