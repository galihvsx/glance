// subGroup (C123T0): shared second-grouping-dimension logic, consumed
// by the board (T0) and the list view (T1). The sub-group dimension
// NEVER equals the active primary dimension — effectiveSubGroupBy is
// the single place that rule lives. Sections are VISUAL grouping only:
// drag semantics stay primary-dimension-only (see Board.tsx).
//
// Show-empty filtering happens at the call site, not here: state and
// priority sections are returned even when empty, so a caller with
// showEmptyGroups on renders them and a caller with it off drops them.
// Epic sections come from the existing groupByEpic helper (never
// reimplemented here): multi-appearance, archived exclusion, and
// Ungrouped-last are inherited verbatim. Assignee sections come from
// the existing groupByAssignee helper the same way (C136T1):
// multi-appearance, first-appearance order, and Ungrouped-last
// (hidden when empty) are inherited verbatim. Label sections come
// from the existing groupByLabel helper the same way (C137T1):
// multi-appearance, first-appearance order, and Ungrouped-last
// (hidden when empty) are inherited verbatim.

import { groupIssuesByAssignee } from "./groupByAssignee";
import { groupIssuesByEpic } from "./groupByEpic";
import { groupIssuesByLabel } from "./groupByLabel";
import type {
  DisplaySettings,
  SubGroupBy,
} from "./useDisplaySettings";
import { priorityLabel, type Issue, type IssueState } from "../../lib/types";

export interface SubGroupSection {
  key: string;
  title: string;
  /** State color for state sections (drives the sub-header dot). */
  color?: string;
  /** Workflow group (e.g. "completed") for state sections. */
  stateGroup?: string;
  /** Per-state-group member counts for epic sections (from
   *  groupByEpic, for the shared breakdown bar). */
  byGroup?: Record<string, number>;
  issues: Issue[];
  /** C127T0: explicit drop-target values for this section's header
   *  (see listHeaderDrop.toHeaderTarget). A section with none of
   *  these names no assignable value — today only the state-Ungrouped
   *  section — and its header is never droppable. */
  targetStateId?: string;
  targetPriority?: number;
  targetEpicId?: string;
  isUngroupedEpic?: boolean;
  targetAssigneeId?: string;
  isUngroupedAssignee?: boolean;
  targetLabelId?: string;
  isUngroupedLabel?: boolean;
}

const SUB_GROUP_BYS: SubGroupBy[] = [
  "none",
  "state",
  "priority",
  "epic",
  "assignees",
  "labels",
];

/** Ungrouped section key for state sub-grouping (issues whose state
 *  is missing from the loaded states). Epic Ungrouped reuses
 *  groupByEpic's key ("epic:ungrouped"). */
export const UNGROUPED_STATE_KEY = "state:ungrouped";

/** The sub-group dimension that actually renders: stored subGroupBy,
 *  except it never equals the active primary dimension (same value
 *  renders as "none"), and a "none" primary group means flat layout.
 *  Unknown/absent stored values also render as "none". */
export function effectiveSubGroupBy(settings: DisplaySettings): SubGroupBy {
  const stored = SUB_GROUP_BYS.includes(settings.subGroupBy)
    ? settings.subGroupBy
    : "none";
  if (settings.groupBy === "none") return "none";
  if (stored === settings.groupBy) return "none";
  return stored;
}

/** Split one primary group's issues into sub-group sections.
 *  - state: one section per state in workflow sequence order
 *    (empty sections included — the call site filters them when
 *    showEmptyGroups is off); issues whose state is not among the
 *    loaded states land in an "Ungrouped" section last.
 *  - priority: sections in 4,3,2,1,0 order with priorityLabel titles
 *    (empty sections included, same call-site rule).
 *  - epic: multi-appearance via groupByEpic (an issue with N live
 *    epics appears under every epic sub-section; Ungrouped last,
 *    hidden when empty by groupByEpic itself).
 *  - assignees: multi-appearance via groupByAssignee (an issue with
 *    N assignees appears under every assignee sub-section; Ungrouped
 *    last, hidden when empty by groupByAssignee itself).
 *  - labels: multi-appearance via groupByLabel (an issue with N
 *    labels appears under every label sub-section; Ungrouped last,
 *    hidden when empty by groupByLabel itself). Label sections carry
 *    the group color from the row (first-appearance row wins).
 *  - none: a single section carrying all issues unchanged. */
export function buildSubGroupSections(
  issues: Issue[],
  subGroupBy: SubGroupBy,
  opts: { states: IssueState[]; stateById: Map<string, IssueState> },
): SubGroupSection[] {
  const { states, stateById } = opts;
  if (subGroupBy === "none") {
    return [{ key: "all", title: "All", issues }];
  }
  if (subGroupBy === "state") {
    const ordered = [...states].sort((a, b) => a.sequence - b.sequence);
    const orderedIds = new Set(ordered.map((s) => s.id));
    const byState = new Map<string, Issue[]>();
    const ungrouped: Issue[] = [];
    // States known to stateById but absent from `states` (defensive:
    // callers pass both from the same list) get their own section
    // rather than silently dropping their issues.
    const extra: IssueState[] = [];
    for (const issue of issues) {
      const st = stateById.get(issue.state_id);
      if (!st) {
        ungrouped.push(issue);
        continue;
      }
      if (!orderedIds.has(st.id) && !extra.some((e) => e.id === st.id)) {
        extra.push(st);
      }
      const bucket = byState.get(st.id) ?? [];
      bucket.push(issue);
      byState.set(st.id, bucket);
    }
    const sections: SubGroupSection[] = [...ordered, ...extra].map((s) => ({
      key: s.id,
      title: s.name,
      color: s.color,
      stateGroup: s.group,
      issues: byState.get(s.id) ?? [],
      targetStateId: s.id,
    }));
    if (ungrouped.length > 0) {
      sections.push({
        key: UNGROUPED_STATE_KEY,
        title: "Ungrouped",
        issues: ungrouped,
      });
    }
    return sections;
  }
  if (subGroupBy === "priority") {
    return [4, 3, 2, 1, 0].map((p) => ({
      key: `priority:${p}`,
      title: priorityLabel(p),
      issues: issues.filter((i) => i.priority === p),
      targetPriority: p,
    }));
  }
  // assignees: reuse groupByAssignee — multi-appearance,
  // first-appearance order, Ungrouped last (hidden when empty).
  // The assignee id IS the key verbatim; the target field is mapped
  // once here, never parsed from the key at drop time. Ungrouped
  // headers carry no target field — they resolve to assignee-ungroup
  // via toHeaderTarget, never to a member.
  if (subGroupBy === "assignees") {
    return groupIssuesByAssignee(issues).map((g) => ({
      key: g.key,
      title: g.title,
      issues: g.issues,
      targetAssigneeId: g.isUngrouped ? undefined : g.key,
      isUngroupedAssignee: g.isUngrouped,
    }));
  }
  // labels: reuse groupByLabel — multi-appearance,
  // first-appearance order, Ungrouped last (hidden when empty).
  // The label id IS the key verbatim; the target field is mapped
  // once here, never parsed from the key at drop time. Ungrouped
  // headers carry no target field — they resolve to label-ungroup
  // via toHeaderTarget, never to a member.
  if (subGroupBy === "labels") {
    return groupIssuesByLabel(issues).map((g) => ({
      key: g.key,
      title: g.title,
      color: g.color,
      issues: g.issues,
      targetLabelId: g.isUngrouped ? undefined : g.key,
      isUngroupedLabel: g.isUngrouped,
    }));
  }
  // epic: reuse groupByEpic — multi-appearance, archived exclusion,
  // Ungrouped last (hidden when empty). The epic id IS the key
  // verbatim; the target field is mapped once here, never parsed
  // from the key at drop time.
  return groupIssuesByEpic(
    issues,
    (i) => stateById.get(i.state_id)?.group,
  ).map((g) => ({
    key: g.key,
    title: g.title,
    byGroup: g.byGroup,
    issues: g.issues,
    targetEpicId: g.isUngrouped ? undefined : g.key,
    isUngroupedEpic: g.isUngrouped,
  }));
}
