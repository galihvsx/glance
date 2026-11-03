// groupByModule (C131T0): pure grouping logic for the issue list's
// Group-by-Modules display mode. Kept framework-free so it is cheaply
// unit-testable; Issues.tsx wires it into the existing grouping path.
// Mirrors groupByLabel.ts (C130T0) with modules substituted, EXCEPT
// for one deliberate difference: modules have NO color. NamedRef
// carries id + name only, so the group carries no color field at all
// — no invented color, no hash-derived color, no lookup.
//
// Decided semantics (cycle-131 plan, mirroring groupByLabel.ts):
// - MULTI-APPEARANCE RULE (deliberate): an issue carrying N modules
//   appears under EVERY module's group. Dropping one side's
//   visibility would silently hide work from a module's view.
// - No archived-exclusion concept exists for modules from row data:
//   the row's NamedRef carries no status (unlike epics, whose
//   EpicRef carries archived_at), so archived modules group like any
//   other module this cycle — do not invent an exclusion.
// - "Ungrouped" (issues with no modules) is ALWAYS the last group
//   and is HIDDEN when empty (callers only get this entry when it has
//   members — same contract as groupByEpic / groupByAssignee /
//   groupByLabel).
// - Group title = the module name from the row (never a fabricated
//   name). Modules ride the row as project_structure.modules
//   (NamedRef[]), so no module lookup and no extra fetch are needed.
//   When rows disagree on one module's name, the FIRST-appearance
//   row wins — groups are keyed by module id and the first row's
//   value is kept, never reconciled across rows.

import type { Issue, NamedRef } from "../../lib/types";

export interface ModuleIssueGroup {
  key: string;
  title: string;
  issues: Issue[];
  isUngrouped: boolean;
}

export const UNGROUPED_MODULE_KEY = "module:ungrouped";

/** Modules on one issue row (defensive: rows without the field, or
 *  without project_structure, group as ungrouped, never crash). */
export function modulesOf(issue: Issue): NamedRef[] {
  return issue.project_structure?.modules ?? [];
}

/** Group issue rows by module membership. Module groups keep
 *  first-appearance order across the input list; Ungrouped is last. */
export function groupIssuesByModule(issues: Issue[]): ModuleIssueGroup[] {
  const byModule = new Map<string, { name: string; issues: Issue[] }>();
  const ungrouped: Issue[] = [];
  for (const issue of issues) {
    const modules = modulesOf(issue);
    if (modules.length === 0) {
      ungrouped.push(issue);
      continue;
    }
    // Multi-appearance: the same issue is appended to EVERY
    // module's group — see the module comment above.
    for (const module of modules) {
      let group = byModule.get(module.id);
      if (!group) {
        group = { name: module.name, issues: [] };
        byModule.set(module.id, group);
      }
      group.issues.push(issue);
    }
  }
  const groups: ModuleIssueGroup[] = [];
  for (const [id, g] of byModule) {
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
      key: UNGROUPED_MODULE_KEY,
      title: "Ungrouped",
      issues: ungrouped,
      isUngrouped: true,
    });
  }
  return groups;
}
