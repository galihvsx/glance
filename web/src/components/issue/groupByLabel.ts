// groupByLabel (C130T0): pure grouping logic for the issue list's
// Group-by-Labels display mode. Kept framework-free so it is cheaply
// unit-testable; Issues.tsx wires it into the existing grouping path.
// Mirrors groupByAssignee.ts (C129T0) with labels substituted.
//
// Decided semantics (cycle-130 plan, mirroring groupByAssignee.ts):
// - MULTI-APPEARANCE RULE (deliberate): an issue carrying N labels
//   appears under EVERY label's group. Dropping one side's
//   visibility would silently hide work from a person's view.
// - No archived-exclusion concept exists for labels (do not invent
//   one): every label on the row groups it.
// - "Ungrouped" (issues with no labels) is ALWAYS the last group
//   and is HIDDEN when empty (callers only get this entry when it has
//   members — same contract as groupByEpic / groupByAssignee).
// - Group title = the label name from the row (never a fabricated
//   name); group color = the label color from the row. Names AND
//   colors ride the row as IssueLabel {id, name, color}, so no
//   label lookup and no extra fetch are needed. When rows disagree
//   on one label's name/color, the FIRST-appearance row wins —
//   groups are keyed by label id and the first row's values are
//   kept, never reconciled across rows.

import type { Issue, IssueLabel } from "../../lib/types";

export interface LabelIssueGroup {
  key: string;
  title: string;
  /** Label color from the row; absent for the Ungrouped group. */
  color?: string;
  issues: Issue[];
  isUngrouped: boolean;
}

export const UNGROUPED_LABEL_KEY = "label:ungrouped";

/** Labels on one issue row (defensive: rows without the field
 *  group as unlabeled, never crash). */
export function labelsOf(issue: Issue): IssueLabel[] {
  return issue.labels ?? [];
}

/** Group issue rows by label membership. Label groups keep
 *  first-appearance order across the input list; Ungrouped is last. */
export function groupIssuesByLabel(issues: Issue[]): LabelIssueGroup[] {
  const byLabel = new Map<
    string,
    { name: string; color: string; issues: Issue[] }
  >();
  const ungrouped: Issue[] = [];
  for (const issue of issues) {
    const labels = labelsOf(issue);
    if (labels.length === 0) {
      ungrouped.push(issue);
      continue;
    }
    // Multi-appearance: the same issue is appended to EVERY
    // label's group — see the module comment above.
    for (const label of labels) {
      let group = byLabel.get(label.id);
      if (!group) {
        group = { name: label.name, color: label.color, issues: [] };
        byLabel.set(label.id, group);
      }
      group.issues.push(issue);
    }
  }
  const groups: LabelIssueGroup[] = [];
  for (const [id, g] of byLabel) {
    groups.push({
      key: id,
      title: g.name,
      color: g.color,
      issues: g.issues,
      isUngrouped: false,
    });
  }
  // Ungrouped is always last and hidden when empty (callers only get
  // this entry when it has members).
  if (ungrouped.length > 0) {
    groups.push({
      key: UNGROUPED_LABEL_KEY,
      title: "Ungrouped",
      issues: ungrouped,
      isUngrouped: true,
    });
  }
  return groups;
}
