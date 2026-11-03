// groupByAssignee (C129T0): pure grouping logic for the issue list's
// Group-by-Assignees display mode. Kept framework-free so it is cheaply
// unit-testable; Issues.tsx wires it into the existing grouping path.
//
// Decided semantics (cycle-129 plan, mirroring groupByEpic.ts with
// assignees substituted):
// - MULTI-APPEARANCE RULE (deliberate): an issue assigned to N people
//   appears under EVERY assignee's group. Dropping one side's
//   visibility would silently hide work from a person's view.
// - No archived-exclusion concept exists for assignees (do not invent
//   one): every assignee on the row groups it.
// - "Ungrouped" (issues with no assignees) is ALWAYS the last group
//   and is HIDDEN when empty (callers only get this entry when it has
//   members — same contract as groupByEpic).
// - Group title = the assignee name from the row (never a fabricated
//   name); names ride the row as IssueAssignee {id, name}, so no
//   member lookup and no extra fetch are needed.

import type { Issue, IssueAssignee } from "../../lib/types";

export interface AssigneeIssueGroup {
  key: string;
  title: string;
  issues: Issue[];
  isUngrouped: boolean;
}

export const UNGROUPED_ASSIGNEE_KEY = "assignee:ungrouped";

/** Assignees on one issue row (defensive: rows without the field
 *  group as unassigned, never crash). */
export function assigneesOf(issue: Issue): IssueAssignee[] {
  return issue.assignees ?? [];
}

/** Group issue rows by assignee membership. Assignee groups keep
 *  first-appearance order across the input list; Ungrouped is last. */
export function groupIssuesByAssignee(issues: Issue[]): AssigneeIssueGroup[] {
  const byAssignee = new Map<string, { name: string; issues: Issue[] }>();
  const ungrouped: Issue[] = [];
  for (const issue of issues) {
    const assignees = assigneesOf(issue);
    if (assignees.length === 0) {
      ungrouped.push(issue);
      continue;
    }
    // Multi-appearance: the same issue is appended to EVERY
    // assignee's group — see the module comment above.
    for (const assignee of assignees) {
      let group = byAssignee.get(assignee.id);
      if (!group) {
        group = { name: assignee.name, issues: [] };
        byAssignee.set(assignee.id, group);
      }
      group.issues.push(issue);
    }
  }
  const groups: AssigneeIssueGroup[] = [];
  for (const [id, g] of byAssignee) {
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
      key: UNGROUPED_ASSIGNEE_KEY,
      title: "Ungrouped",
      issues: ungrouped,
      isUngrouped: true,
    });
  }
  return groups;
}
