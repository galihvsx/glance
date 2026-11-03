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
// (hidden when empty) are inherited verbatim. Creator sections come
// from the existing groupByCreatedBy helper the same way (C138T1):
// single-appearance, first-appearance order, and Unknown-last
// (hidden when empty) are inherited verbatim. State-group sections
// come from the existing groupByStateGroup helper the same way
// (C139T1): single-appearance, backend-fixed canonical order
// (triage, backlog, unstarted, started, completed, cancelled), and
// Unknown-last (hidden when empty) are inherited verbatim, with
// empty canonical groups synthesized via { includeEmptyGroups: true }
// (the call site filters them when showEmptyGroups is off).
// State-group sections carry NO color, NO breakdown bar, and NO
// target fields: a group holds several states, so a cross-group drop
// has no single target state and the headers are never droppable.
// Cycle sections come from the existing groupByCycle helper the same
// way (C140T1): single-appearance, first-appearance order, and
// Ungrouped-last (hidden when empty) are inherited verbatim. Cycle
// sections carry NO color (NamedRef carries id + name only) and NO
// breakdown bar; they DO carry target fields (targetCycleId /
// isUngroupedCycle), so list sub-headers are droppable with the
// existing cycle-move / cycle-ungroup pipeline, while board
// sub-sections stay visual-only (the board renderer ignores target
// fields, as it does for epic/assignee/label today).
// Module sections come from the existing groupByModule helper the
// same way (C140T1): multi-appearance, first-appearance order, and
// Ungrouped-last (hidden when empty) are inherited verbatim. Module
// sections carry NO color (NamedRef carries id + name only) and NO
// breakdown bar; they DO carry target fields (targetModuleId /
// isUngroupedModule), so list sub-headers are droppable with the
// existing module-move / module-ungroup pipeline. The list page
// records the module sub-section as the dragged stack's source
// module (C137 label precedent) — without it a move from a module
// sub-section would add the target without removing the source
// appearance, turning a move into a copy.
// Release sections come from the existing groupByRelease helper the
// same way (C140T1): single-appearance, first-appearance order, and
// Ungrouped-last (hidden when empty) are inherited verbatim.
// Release sections carry NO color (NamedRef carries id + name only)
// and NO breakdown bar; they DO carry target fields
// (targetReleaseId / isUngroupedRelease), so list sub-headers are
// droppable with the existing single-assign release-move /
// release-ungroup pipeline (the move overwrites release_id, so no
// remove side exists and no source field is needed for it).

import { groupIssuesByAssignee } from "./groupByAssignee";
import {
  groupIssuesByCreatedBy,
  type CreatorLookupEntry,
} from "./groupByCreatedBy";
import { groupIssuesByCycle } from "./groupByCycle";
import { groupIssuesByEpic } from "./groupByEpic";
import { groupIssuesByLabel } from "./groupByLabel";
import { groupIssuesByModule } from "./groupByModule";
import { groupIssuesByRelease } from "./groupByRelease";
import { groupIssuesByStateGroup } from "./groupByStateGroup";
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
  targetModuleId?: string;
  isUngroupedModule?: boolean;
  targetCycleId?: string;
  isUngroupedCycle?: boolean;
  targetReleaseId?: string;
  isUngroupedRelease?: boolean;
}

const SUB_GROUP_BYS: SubGroupBy[] = [
  "none",
  "state",
  "priority",
  "epic",
  "assignees",
  "labels",
  "createdBy",
  "stateGroups",
  "cycles",
  "modules",
  "releases",
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
 *  - createdBy: single-appearance via groupByCreatedBy (an issue
 *    appears under exactly one creator section; Unknown last,
 *    hidden when empty by groupByCreatedBy itself). Creator sections
 *    carry NO color (members carry none) and NO target fields:
 *    created_by is immutable, so the headers are never droppable.
 *    A missing membersById reads as "no member matches" — every
 *    issue collapses to Unknown, never a raw id label.
 *  - stateGroups: single-appearance via groupByStateGroup (an issue
 *    appears under exactly one state-group section; canonical order
 *    triage → cancelled regardless of issue order, Unknown last,
 *    hidden when empty by groupByStateGroup itself; empty canonical
 *    groups synthesized via { includeEmptyGroups: true } and
 *    filtered by the call site when showEmptyGroups is off).
 *    State-group sections carry NO color (groups have none), NO
 *    breakdown bar, NO stateGroup field (a section IS a group, not
 *    a state in one), and NO target fields: a group holds several
 *    states, so a cross-group drop has no single target state and
 *    the headers are never droppable. A missing stateGroupById
 *    reads as "no state matches" — every issue collapses to
 *    Unknown, never a raw state id label.
 *  - cycles: single-appearance via groupByCycle (an issue appears
 *    under exactly one cycle section; Ungrouped last, hidden when
 *    empty by groupByCycle itself). Cycle sections carry NO color
 *    (NamedRef carries id + name only) and NO breakdown bar, but
 *    DO carry target fields (targetCycleId / isUngroupedCycle):
 *    the list sub-headers are droppable with the existing
 *    cycle-move / cycle-ungroup pipeline. A missing
 *    project_structure reads as no cycle — Ungrouped, never a
 *    crash, never a raw id label.
 *  - modules: multi-appearance via groupByModule (an issue with N
 *    modules appears under every module sub-section; Ungrouped
 *    last, hidden when empty by groupByModule itself). Module
 *    sections carry NO color (NamedRef carries id + name only)
 *    and NO breakdown bar, but DO carry target fields
 *    (targetModuleId / isUngroupedModule): the list sub-headers
 *    are droppable with the existing module-move /
 *    module-ungroup pipeline. A missing project_structure reads
 *    as no modules — Ungrouped, never a crash.
 *  - releases: single-appearance via groupByRelease (an issue
 *    appears under exactly one release section; Ungrouped last,
 *    hidden when empty by groupByRelease itself). Release sections
 *    carry NO color (NamedRef carries id + name only) and NO
 *    breakdown bar, but DO carry target fields (targetReleaseId
 *    / isUngroupedRelease): the list sub-headers are droppable
 *    with the existing single-assign release-move /
 *    release-ungroup pipeline. A missing project_structure reads
 *    as no release — Ungrouped, never a crash.
 *  - none: a single section carrying all issues unchanged. */
export function buildSubGroupSections(
  issues: Issue[],
  subGroupBy: SubGroupBy,
  opts: {
    states: IssueState[];
    stateById: Map<string, IssueState>;
    membersById?: ReadonlyMap<string, CreatorLookupEntry>;
    stateGroupById?: ReadonlyMap<string, string>;
  },
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
  // createdBy: reuse groupByCreatedBy — single-appearance,
  // first-appearance order, Unknown last (hidden when empty). The
  // group key and title pass through verbatim (creator id; Unknown
  // key "createdby:unknown"); NO color, NO target fields — the
  // C127T0 rule then yields a non-droppable header, and there is no
  // creator-move pipeline to wire (created_by is immutable).
  if (subGroupBy === "createdBy") {
    return groupIssuesByCreatedBy(
      issues,
      opts.membersById ?? new Map(),
    ).map((g) => ({
      key: g.key,
      title: g.title,
      issues: g.issues,
    }));
  }
  // stateGroups: reuse groupByStateGroup — single-appearance,
  // canonical order, Unknown last (hidden when empty), empty
  // canonical groups synthesized (call site filters them when
  // showEmptyGroups is off). The group key and title pass through
  // verbatim (stategroup:<group>; Unknown key
  // "stategroup:unknown"); NO color, NO breakdown bar, NO
  // stateGroup field, NO target fields — the C127T0 rule then
  // yields a non-droppable header, and there is no state-group-move
  // pipeline to wire (a group holds several states).
  if (subGroupBy === "stateGroups") {
    return groupIssuesByStateGroup(
      issues,
      opts.stateGroupById ?? new Map(),
      { includeEmptyGroups: true },
    ).map((g) => ({
      key: g.key,
      title: g.title,
      issues: g.issues,
    }));
  }
  // cycles: reuse groupByCycle — single-appearance,
  // first-appearance order, Ungrouped last (hidden when empty).
  // The cycle id IS the key verbatim; the target field is mapped
  // once here, never parsed from the key at drop time. Ungrouped
  // headers carry no target field — they resolve to cycle-ungroup
  // via toHeaderTarget, never to a cycle.
  if (subGroupBy === "cycles") {
    return groupIssuesByCycle(issues).map((g) => ({
      key: g.key,
      title: g.title,
      issues: g.issues,
      targetCycleId: g.isUngrouped ? undefined : g.key,
      isUngroupedCycle: g.isUngrouped,
    }));
  }
  // modules: reuse groupByModule — multi-appearance,
  // first-appearance order, Ungrouped last (hidden when empty).
  // The module id IS the key verbatim; the target field is mapped
  // once here, never parsed from the key at drop time. Ungrouped
  // headers carry no target field — they resolve to module-ungroup
  // via toHeaderTarget, never to a module.
  if (subGroupBy === "modules") {
    return groupIssuesByModule(issues).map((g) => ({
      key: g.key,
      title: g.title,
      issues: g.issues,
      targetModuleId: g.isUngrouped ? undefined : g.key,
      isUngroupedModule: g.isUngrouped,
    }));
  }
  // releases: reuse groupByRelease — single-appearance,
  // first-appearance order, Ungrouped last (hidden when empty).
  // The release id IS the key verbatim; the target field is mapped
  // once here, never parsed from the key at drop time. Ungrouped
  // headers carry no target field — they resolve to release-ungroup
  // via toHeaderTarget, never to a release.
  if (subGroupBy === "releases") {
    return groupIssuesByRelease(issues).map((g) => ({
      key: g.key,
      title: g.title,
      issues: g.issues,
      targetReleaseId: g.isUngrouped ? undefined : g.key,
      isUngroupedRelease: g.isUngrouped,
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
