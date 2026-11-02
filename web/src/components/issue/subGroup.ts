// subGroup (C123T1 CONTRACT PLACEHOLDER — read before merging).
//
// T0 owns this file (settings key + shared helper) on branch
// cycle123/sub-group-board. This copy exists ONLY so the T1 list-view
// branch compiles and tests pass standalone while T0's branch is still
// at base: it implements the plan's FIXED CONTRACT exactly (same file
// path, same exported names/signatures), and the coordinator's merge
// check reconciles it with T0's real helper — adapting the T1 side
// only if names differ. NEVER ship this as a second divergent helper:
// at merge time, delete this placeholder in favour of T0's version.
//
// Contract (cycle123-plan.md):
// - SubGroupBy = "none" | "state" | "priority" | "epic", default "none".
// - effectiveSubGroupBy(settings): the sub-group dimension NEVER equals
//   the active primary dimension (stored == groupBy renders as "none");
//   groupBy "none" ignores the sub-group entirely.
// - buildSubGroupSections(issues, subGroupBy, { states, stateById }):
//   state in workflow sequence order; priority 4,3,2,1,0; epic
//   multi-appearance via the existing groupByEpic helper, Ungrouped
//   last; "none" = a single section carrying all issues unchanged.
//   Empty-section filtering stays at the call site (showEmptyGroups),
//   same rule as primary groups — so this helper returns empty
//   sections rather than dropping them (epic sections are the
//   exception: groupByEpic never emits an empty section, matching the
//   primary epic grouping).
//
// Row-preservation note: a sub-section stack must never drop a row
// the flat primary group would render, so issues whose sub-group value
// matches nothing (unknown state_id, out-of-range priority) land in a
// trailing "Ungrouped" section — the same missing-value semantics the
// plan states for epic memberships.

import { groupIssuesByEpic } from "./groupByEpic";
import { priorityLabel } from "../../lib/types";
import type { GroupBy } from "./useDisplaySettings";
import type { Issue, IssueState } from "../../lib/types";

export type SubGroupBy = "none" | "state" | "priority" | "epic";

const SUB_GROUP_BYS: SubGroupBy[] = ["none", "state", "priority", "epic"];

/** Settings shape the effective rule needs. subGroupBy is optional
 *  here only because the base useDisplaySettings does not carry the
 *  key yet (T0 adds it); after the merge it is always present. */
export interface SubGroupSettings {
  groupBy: GroupBy;
  subGroupBy?: SubGroupBy;
}

export interface SubGroupSection {
  key: string;
  title: string;
  color?: string;
  /** Workflow group for the header state-dot (state sections). */
  stateGroup?: string;
  /** Per-state-group member counts for the epic header progress bar
   *  (epic sections only, from groupByEpic). */
  byGroup?: Record<string, number>;
  issues: Issue[];
}

/** Binding effective rule: never equals the active primary dimension;
 *  groupBy "none" ignores the sub-group; unknown stored values fall
 *  back to "none" (the load() merge precedent — no reset). */
export function effectiveSubGroupBy(settings: SubGroupSettings): SubGroupBy {
  if (settings.groupBy === "none") return "none";
  const stored = settings.subGroupBy ?? "none";
  if (!SUB_GROUP_BYS.includes(stored)) return "none";
  if (stored === settings.groupBy) return "none";
  return stored;
}

export interface SubGroupContext {
  states: IssueState[];
  stateById: Map<string, IssueState>;
}

export function buildSubGroupSections(
  issues: Issue[],
  subGroupBy: SubGroupBy,
  ctx: SubGroupContext,
): SubGroupSection[] {
  if (subGroupBy === "none") {
    return [{ key: "all", title: "All", issues }];
  }
  if (subGroupBy === "state") {
    const sorted = [...ctx.states].sort((a, b) => a.sequence - b.sequence);
    const sections: SubGroupSection[] = sorted.map((s) => ({
      key: s.id,
      title: s.name,
      color: s.color,
      stateGroup: s.group,
      issues: issues.filter((i) => i.state_id === s.id),
    }));
    // Missing value (null/unknown state_id) → trailing Ungrouped,
    // so no row is dropped from the primary group's flat set.
    const ungrouped = issues.filter((i) => !ctx.stateById.has(i.state_id));
    if (ungrouped.length > 0) {
      sections.push({ key: "state:ungrouped", title: "Ungrouped", issues: ungrouped });
    }
    return sections;
  }
  if (subGroupBy === "priority") {
    const sections: SubGroupSection[] = [4, 3, 2, 1, 0].map((p) => ({
      key: `priority:${p}`,
      title: priorityLabel(p),
      issues: issues.filter((i) => i.priority === p),
    }));
    const ungrouped = issues.filter(
      (i) => !Number.isInteger(i.priority) || i.priority < 0 || i.priority > 4,
    );
    if (ungrouped.length > 0) {
      sections.push({ key: "priority:ungrouped", title: "Ungrouped", issues: ungrouped });
    }
    return sections;
  }
  // epic: multi-appearance via the existing helper (never reimplemented
  // here) — an issue with N live epics appears under every epic
  // sub-section; Ungrouped last; archived epics excluded by the helper.
  return groupIssuesByEpic(
    issues,
    (i) => ctx.stateById.get(i.state_id)?.group,
  ).map((g) => ({
    key: g.key,
    title: g.title,
    byGroup: g.byGroup,
    issues: g.issues,
  }));
}
