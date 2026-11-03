// C123T0: unit tests for the shared sub-group helper — the effective
// rule (never equals the primary dimension) and section building for
// state / priority / epic / none. Show-empty filtering is a call-site
// concern and is NOT tested here (the board tests cover it).
// C136T1: adds assignees sections (via groupByAssignee, verbatim).

import { describe, expect, it } from "vitest";
import {
  UNGROUPED_STATE_KEY,
  buildSubGroupSections,
  effectiveSubGroupBy,
} from "./subGroup";
import { UNGROUPED_ASSIGNEE_KEY } from "./groupByAssignee";
import { UNGROUPED_EPIC_KEY } from "./groupByEpic";
import { UNGROUPED_LABEL_KEY } from "./groupByLabel";
import { UNKNOWN_CREATOR_KEY } from "./groupByCreatedBy";
import { UNKNOWN_STATE_GROUP_KEY } from "./groupByStateGroup";
import type { Issue, IssueState } from "../../lib/types";
import {
  DEFAULT_DISPLAY_SETTINGS,
  type DisplaySettings,
} from "./useDisplaySettings";

const states: IssueState[] = [
  {
    id: "s-done",
    project_id: "p",
    name: "Done",
    group: "completed",
    color: "#4ade80",
    sequence: 2,
    created_at: "",
    updated_at: "",
  },
  {
    id: "s-todo",
    project_id: "p",
    name: "Todo",
    group: "unstarted",
    color: "#888",
    sequence: 1,
    created_at: "",
    updated_at: "",
  },
];
const stateById = new Map(states.map((s) => [s.id, s] as const));

function mkIssue(
  id: string,
  stateId: string,
  priority: number,
  epics: { id: string; name: string; archived_at?: string | null }[] = [],
  assignees: { id: string; name: string }[] = [],
  labels: { id: string; name: string; color: string }[] = [],
  createdBy = "u",
): Issue {
  return {
    id,
    project_id: "p",
    sequence_id: 1,
    display_id: `ENG-${id}`,
    name: `issue ${id}`,
    priority,
    state_id: stateId,
    sort_order: 0,
    is_draft: false,
    created_by: createdBy,
    created_at: "",
    updated_at: "",
    assignees,
    labels,
    epics,
  } as Issue;
}

const EPIC_A = { id: "epic-a", name: "Alpha" };
const EPIC_B = { id: "epic-b", name: "Beta" };
const EPIC_ARCHIVED = {
  id: "epic-old",
  name: "Old",
  archived_at: "2026-05-01T00:00:00Z",
};

function settingsWith(
  groupBy: DisplaySettings["groupBy"],
  subGroupBy: DisplaySettings["subGroupBy"],
): DisplaySettings {
  return { ...DEFAULT_DISPLAY_SETTINGS, groupBy, subGroupBy };
}

describe("effectiveSubGroupBy", () => {
  it("passes a stored sub-group through when it differs from groupBy", () => {
    expect(
      effectiveSubGroupBy(settingsWith("state", "priority")),
    ).toBe("priority");
    expect(effectiveSubGroupBy(settingsWith("priority", "epic"))).toBe(
      "epic",
    );
  });

  it("renders same-as-primary as none", () => {
    expect(effectiveSubGroupBy(settingsWith("state", "state"))).toBe(
      "none",
    );
    expect(effectiveSubGroupBy(settingsWith("priority", "priority"))).toBe(
      "none",
    );
    expect(effectiveSubGroupBy(settingsWith("epic", "epic"))).toBe("none");
    expect(effectiveSubGroupBy(settingsWith("assignees", "assignees"))).toBe(
      "none",
    );
    expect(effectiveSubGroupBy(settingsWith("labels", "labels"))).toBe(
      "none",
    );
  });

  it("passes assignees through when it differs from groupBy", () => {
    expect(effectiveSubGroupBy(settingsWith("state", "assignees"))).toBe(
      "assignees",
    );
  });

  it("passes labels through when it differs from groupBy (C137T1)", () => {
    expect(effectiveSubGroupBy(settingsWith("state", "labels"))).toBe(
      "labels",
    );
  });

  it("passes createdBy through when it differs from groupBy (C138T1)", () => {
    expect(effectiveSubGroupBy(settingsWith("state", "createdBy"))).toBe(
      "createdBy",
    );
  });

  it("renders createdBy same-as-primary as none (C138T1)", () => {
    expect(
      effectiveSubGroupBy(settingsWith("createdBy", "createdBy")),
    ).toBe("none");
  });

  it("passes stateGroups through when it differs from groupBy (C139T1)", () => {
    expect(effectiveSubGroupBy(settingsWith("state", "stateGroups"))).toBe(
      "stateGroups",
    );
  });

  it("renders stateGroups same-as-primary as none (C139T1)", () => {
    expect(
      effectiveSubGroupBy(settingsWith("stateGroups", "stateGroups")),
    ).toBe("none");
  });

  it("renders none when groupBy is none, whatever is stored", () => {
    expect(effectiveSubGroupBy(settingsWith("none", "state"))).toBe("none");
    expect(effectiveSubGroupBy(settingsWith("none", "epic"))).toBe("none");
  });

  it("renders an unknown stored value as none", () => {
    const settings = settingsWith("state", "bogus" as never);
    expect(effectiveSubGroupBy(settings)).toBe("none");
  });
});

describe("buildSubGroupSections", () => {
  it("state: one section per state in workflow sequence order, empty included", () => {
    const issues = [mkIssue("1", "s-done", 3), mkIssue("2", "s-todo", 3)];
    const sections = buildSubGroupSections(issues, "state", {
      states,
      stateById,
    });
    // Input states are out of order (Done seq 2 first) — output is
    // sequence order: Todo, then Done.
    expect(sections.map((s) => s.title)).toEqual(["Todo", "Done"]);
    expect(sections[0].issues.map((i) => i.id)).toEqual(["2"]);
    expect(sections[1].issues.map((i) => i.id)).toEqual(["1"]);
    expect(sections[0].color).toBe("#888");
    expect(sections[1].stateGroup).toBe("completed");
  });

  it("state: issues with an unknown state land in Ungrouped last", () => {
    const issues = [
      mkIssue("1", "s-missing", 3),
      mkIssue("2", "s-todo", 3),
    ];
    const sections = buildSubGroupSections(issues, "state", {
      states,
      stateById,
    });
    expect(sections.map((s) => s.key)).toEqual([
      "s-todo",
      "s-done",
      UNGROUPED_STATE_KEY,
    ]);
    expect(sections[2].title).toBe("Ungrouped");
    expect(sections[2].issues.map((i) => i.id)).toEqual(["1"]);
    // The empty Done section is still present (call site filters it).
    expect(sections[1].issues).toEqual([]);
  });

  it("priority: sections in 4,3,2,1,0 order with priorityLabel titles", () => {
    const issues = [
      mkIssue("1", "s-todo", 0),
      mkIssue("2", "s-todo", 4),
      mkIssue("3", "s-todo", 4),
      mkIssue("4", "s-todo", 2),
    ];
    const sections = buildSubGroupSections(issues, "priority", {
      states,
      stateById,
    });
    expect(sections.map((s) => s.title)).toEqual([
      "Urgent",
      "High",
      "Medium",
      "Low",
      "None",
    ]);
    expect(sections.map((s) => s.issues.map((i) => i.id))).toEqual([
      ["2", "3"],
      [],
      ["4"],
      [],
      ["1"],
    ]);
  });

  it("epic: multi-appearance via groupByEpic, Ungrouped last", () => {
    const issues = [
      mkIssue("1", "s-todo", 3, [EPIC_A, EPIC_B]),
      mkIssue("2", "s-todo", 3, []),
      mkIssue("3", "s-todo", 3, [EPIC_A]),
    ];
    const sections = buildSubGroupSections(issues, "epic", {
      states,
      stateById,
    });
    expect(sections.map((s) => s.key)).toEqual([
      "epic-a",
      "epic-b",
      UNGROUPED_EPIC_KEY,
    ]);
    expect(sections[0].issues.map((i) => i.id)).toEqual(["1", "3"]);
    expect(sections[1].issues.map((i) => i.id)).toEqual(["1"]);
    expect(sections[2].issues.map((i) => i.id)).toEqual(["2"]);
    expect(sections[0].byGroup).toEqual({ unstarted: 2 });
  });

  it("epic: archived-only membership lands in Ungrouped", () => {
    const issues = [mkIssue("1", "s-todo", 3, [EPIC_ARCHIVED])];
    const sections = buildSubGroupSections(issues, "epic", {
      states,
      stateById,
    });
    expect(sections.map((s) => s.key)).toEqual([UNGROUPED_EPIC_KEY]);
  });

  it("assignees: multi-appearance via groupByAssignee, Ungrouped last, row names only", () => {
    const ALICE = { id: "u-alice", name: "Alice" };
    const BOB = { id: "u-bob", name: "Bob" };
    const issues = [
      mkIssue("1", "s-todo", 3, [], [ALICE, BOB]),
      mkIssue("2", "s-todo", 3, [], []),
      mkIssue("3", "s-todo", 3, [], [ALICE]),
    ];
    const sections = buildSubGroupSections(issues, "assignees", {
      states,
      stateById,
    });
    expect(sections.map((s) => s.key)).toEqual([
      "u-alice",
      "u-bob",
      UNGROUPED_ASSIGNEE_KEY,
    ]);
    // Titles come from the row, never fabricated.
    expect(sections.map((s) => s.title)).toEqual([
      "Alice",
      "Bob",
      "Ungrouped",
    ]);
    expect(sections[0].issues.map((i) => i.id)).toEqual(["1", "3"]);
    expect(sections[1].issues.map((i) => i.id)).toEqual(["1"]);
    expect(sections[2].issues.map((i) => i.id)).toEqual(["2"]);
    // Drop-target fields: member sections carry the id, Ungrouped
    // carries the flag and no target (assignee-ungroup via
    // toHeaderTarget).
    expect(sections[0].targetAssigneeId).toBe("u-alice");
    expect(sections[0].isUngroupedAssignee).toBe(false);
    expect(sections[1].targetAssigneeId).toBe("u-bob");
    expect(sections[2].targetAssigneeId).toBeUndefined();
    expect(sections[2].isUngroupedAssignee).toBe(true);
  });

  it("assignees: Ungrouped hidden when empty (no unassigned issues)", () => {
    const issues = [
      mkIssue("1", "s-todo", 3, [], [{ id: "u-alice", name: "Alice" }]),
    ];
    const sections = buildSubGroupSections(issues, "assignees", {
      states,
      stateById,
    });
    expect(sections.map((s) => s.key)).toEqual(["u-alice"]);
  });

  it("labels: multi-appearance via groupByLabel, Ungrouped last, row names/colors only (C137T1)", () => {
    const BUG = { id: "l-bug", name: "Bug", color: "#e22718" };
    const FEAT = { id: "l-feat", name: "Feature", color: "#0066b1" };
    const issues = [
      mkIssue("1", "s-todo", 3, [], [], [BUG, FEAT]),
      mkIssue("2", "s-todo", 3, [], [], []),
      mkIssue("3", "s-todo", 3, [], [], [BUG]),
    ];
    const sections = buildSubGroupSections(issues, "labels", {
      states,
      stateById,
    });
    expect(sections.map((s) => s.key)).toEqual([
      "l-bug",
      "l-feat",
      UNGROUPED_LABEL_KEY,
    ]);
    // Titles and colors come from the row, never fabricated.
    expect(sections.map((s) => s.title)).toEqual([
      "Bug",
      "Feature",
      "Ungrouped",
    ]);
    expect(sections[0].color).toBe("#e22718");
    expect(sections[1].color).toBe("#0066b1");
    expect(sections[2].color).toBeUndefined();
    expect(sections[0].issues.map((i) => i.id)).toEqual(["1", "3"]);
    expect(sections[1].issues.map((i) => i.id)).toEqual(["1"]);
    expect(sections[2].issues.map((i) => i.id)).toEqual(["2"]);
    // Drop-target fields: member sections carry the id, Ungrouped
    // carries the flag and no target (label-ungroup via
    // toHeaderTarget).
    expect(sections[0].targetLabelId).toBe("l-bug");
    expect(sections[0].isUngroupedLabel).toBe(false);
    expect(sections[1].targetLabelId).toBe("l-feat");
    expect(sections[2].targetLabelId).toBeUndefined();
    expect(sections[2].isUngroupedLabel).toBe(true);
  });

  it("labels: Ungrouped hidden when empty (no unlabeled issues)", () => {
    const issues = [
      mkIssue("1", "s-todo", 3, [], [], [
        { id: "l-bug", name: "Bug", color: "#e22718" },
      ]),
    ];
    const sections = buildSubGroupSections(issues, "labels", {
      states,
      stateById,
    });
    expect(sections.map((s) => s.key)).toEqual(["l-bug"]);
  });

  it("labels: first-appearance row wins on a shared label's name/color", () => {
    const issues = [
      mkIssue("1", "s-todo", 3, [], [], [
        { id: "l-bug", name: "Bug", color: "#e22718" },
      ]),
      mkIssue("2", "s-todo", 3, [], [], [
        { id: "l-bug", name: "Renamed", color: "#000000" },
      ]),
    ];
    const sections = buildSubGroupSections(issues, "labels", {
      states,
      stateById,
    });
    expect(sections).toHaveLength(1);
    expect(sections[0].title).toBe("Bug");
    expect(sections[0].color).toBe("#e22718");
    expect(sections[0].issues.map((i) => i.id)).toEqual(["1", "2"]);
  });

  it("none: a single section carrying all issues unchanged", () => {
    const issues = [mkIssue("1", "s-todo", 3), mkIssue("2", "s-done", 1)];
    const sections = buildSubGroupSections(issues, "none", {
      states,
      stateById,
    });
    expect(sections).toHaveLength(1);
    expect(sections[0].issues).toBe(issues);
  });

  it("createdBy: single-appearance via groupByCreatedBy, Unknown last, no color, no target fields (C138T1)", () => {
    const membersById = new Map([
      ["u-alice", { name: "Alice", email: "alice@x.com" }],
      ["u-bob", { name: null, email: "bob@x.com" }],
    ]);
    const issues = [
      mkIssue("1", "s-todo", 3, [], [], [], "u-alice"),
      mkIssue("2", "s-todo", 3, [], [], [], "u-gone"),
      mkIssue("3", "s-todo", 3, [], [], [], "u-bob"),
      mkIssue("4", "s-todo", 3, [], [], [], "u-alice"),
    ];
    const sections = buildSubGroupSections(issues, "createdBy", {
      states,
      stateById,
      membersById,
    });
    expect(sections.map((s) => s.key)).toEqual([
      "u-alice",
      "u-bob",
      UNKNOWN_CREATOR_KEY,
    ]);
    // Labels come from the members map (name ?? email), never a raw id.
    expect(sections.map((s) => s.title)).toEqual([
      "Alice",
      "bob@x.com",
      "Unknown",
    ]);
    // Single-appearance: each issue under exactly one section.
    expect(sections[0].issues.map((i) => i.id)).toEqual(["1", "4"]);
    expect(sections[1].issues.map((i) => i.id)).toEqual(["3"]);
    expect(sections[2].issues.map((i) => i.id)).toEqual(["2"]);
    // No color (members carry none) and NO target fields: created_by
    // is immutable, so the headers are never droppable (C127T0 rule).
    for (const s of sections) {
      expect(s.color).toBeUndefined();
      expect(s.targetStateId).toBeUndefined();
      expect(s.targetPriority).toBeUndefined();
      expect(s.targetEpicId).toBeUndefined();
      expect(s.targetAssigneeId).toBeUndefined();
      expect(s.targetLabelId).toBeUndefined();
      expect(s.isUngroupedEpic).toBeUndefined();
      expect(s.isUngroupedAssignee).toBeUndefined();
      expect(s.isUngroupedLabel).toBeUndefined();
    }
  });

  it("createdBy: Unknown hidden when empty (every creator matches a member)", () => {
    const membersById = new Map([
      ["u-alice", { name: "Alice", email: "alice@x.com" }],
    ]);
    const issues = [mkIssue("1", "s-todo", 3, [], [], [], "u-alice")];
    const sections = buildSubGroupSections(issues, "createdBy", {
      states,
      stateById,
      membersById,
    });
    expect(sections.map((s) => s.key)).toEqual(["u-alice"]);
  });

  it("createdBy: member-miss collapses to Unknown, never a raw id label", () => {
    const membersById = new Map<string, { name: string; email: string }>();
    const issues = [mkIssue("1", "s-todo", 3, [], [], [], "u-gone")];
    const sections = buildSubGroupSections(issues, "createdBy", {
      states,
      stateById,
      membersById,
    });
    expect(sections.map((s) => s.key)).toEqual([UNKNOWN_CREATOR_KEY]);
    expect(sections.map((s) => s.title)).toEqual(["Unknown"]);
  });

  it("createdBy: missing membersById reads as no matches — all Unknown, no crash", () => {
    const issues = [
      mkIssue("1", "s-todo", 3, [], [], [], "u-alice"),
      mkIssue("2", "s-todo", 3, [], [], [], ""),
    ];
    const sections = buildSubGroupSections(issues, "createdBy", {
      states,
      stateById,
    });
    expect(sections.map((s) => s.key)).toEqual([UNKNOWN_CREATOR_KEY]);
    expect(sections[0].issues.map((i) => i.id)).toEqual(["1", "2"]);
  });

  // C139T1: state-group sub-sections. One state per canonical group
  // plus a defensive non-canonical group; the map is state id ->
  // group string, exactly what Issues.tsx builds for the primary
  // state-groups path.
  const sgStates: IssueState[] = [
    { id: "s-tri", project_id: "p", name: "Triage me", group: "triage", color: "#888", sequence: 1, created_at: "", updated_at: "" },
    { id: "s-bac", project_id: "p", name: "Backlog me", group: "backlog", color: "#888", sequence: 2, created_at: "", updated_at: "" },
    { id: "s-uns", project_id: "p", name: "Todo", group: "unstarted", color: "#888", sequence: 3, created_at: "", updated_at: "" },
    { id: "s-sta", project_id: "p", name: "Doing", group: "started", color: "#888", sequence: 4, created_at: "", updated_at: "" },
    { id: "s-com", project_id: "p", name: "Done", group: "completed", color: "#888", sequence: 5, created_at: "", updated_at: "" },
    { id: "s-can", project_id: "p", name: "Wontfix", group: "cancelled", color: "#888", sequence: 6, created_at: "", updated_at: "" },
    { id: "s-rev", project_id: "p", name: "In review", group: "review", color: "#888", sequence: 7, created_at: "", updated_at: "" },
  ];
  const sgStateById = new Map(sgStates.map((s) => [s.id, s] as const));
  const sgMap = new Map(sgStates.map((s) => [s.id, s.group] as const));

  it("stateGroups: canonical order regardless of issue order, single-appearance (C139T1)", () => {
    // Issues arrive in reverse canonical order; each row lands
    // under exactly one section.
    const issues = [
      mkIssue("1", "s-can", 3),
      mkIssue("2", "s-com", 3),
      mkIssue("3", "s-sta", 3),
      mkIssue("4", "s-uns", 3),
      mkIssue("5", "s-bac", 3),
      mkIssue("6", "s-tri", 3),
    ];
    const sections = buildSubGroupSections(issues, "stateGroups", {
      states: sgStates,
      stateById: sgStateById,
      stateGroupById: sgMap,
    });
    expect(sections.map((s) => s.key)).toEqual([
      "stategroup:triage",
      "stategroup:backlog",
      "stategroup:unstarted",
      "stategroup:started",
      "stategroup:completed",
      "stategroup:cancelled",
    ]);
    expect(sections.map((s) => s.title)).toEqual([
      "Triage",
      "Backlog",
      "Unstarted",
      "Started",
      "Completed",
      "Cancelled",
    ]);
    expect(sections.map((s) => s.issues.map((i) => i.id))).toEqual([
      ["6"],
      ["5"],
      ["4"],
      ["3"],
      ["2"],
      ["1"],
    ]);
    // Single-appearance: every input row appears exactly once.
    expect(sections.flatMap((s) => s.issues).map((i) => i.id).sort()).toEqual(
      ["1", "2", "3", "4", "5", "6"],
    );
    // NO color, NO breakdown bar, NO stateGroup field, NO target
    // fields: a group holds several states, so the headers are never
    // droppable (C127T0 rule).
    for (const s of sections) {
      expect(s.color).toBeUndefined();
      expect(s.byGroup).toBeUndefined();
      expect(s.stateGroup).toBeUndefined();
      expect(s.targetStateId).toBeUndefined();
      expect(s.targetPriority).toBeUndefined();
      expect(s.targetEpicId).toBeUndefined();
      expect(s.targetAssigneeId).toBeUndefined();
      expect(s.targetLabelId).toBeUndefined();
      expect(s.isUngroupedEpic).toBeUndefined();
      expect(s.isUngroupedAssignee).toBeUndefined();
      expect(s.isUngroupedLabel).toBeUndefined();
    }
  });

  it("stateGroups: includeEmpty synthesizes all six canonical groups, Unknown hidden when empty (C139T1)", () => {
    const sections = buildSubGroupSections([], "stateGroups", {
      states: sgStates,
      stateById: sgStateById,
      stateGroupById: sgMap,
    });
    expect(sections.map((s) => s.key)).toEqual([
      "stategroup:triage",
      "stategroup:backlog",
      "stategroup:unstarted",
      "stategroup:started",
      "stategroup:completed",
      "stategroup:cancelled",
    ]);
    for (const s of sections) expect(s.issues).toEqual([]);
  });

  it("stateGroups: Unknown last, hidden when empty (C139T1)", () => {
    const issues = [
      mkIssue("1", "s-missing", 3),
      mkIssue("2", "s-tri", 3),
      mkIssue("3", "", 3),
    ];
    const sections = buildSubGroupSections(issues, "stateGroups", {
      states: sgStates,
      stateById: sgStateById,
      stateGroupById: sgMap,
    });
    expect(sections.at(-1)?.key).toBe(UNKNOWN_STATE_GROUP_KEY);
    expect(sections.at(-1)?.title).toBe("Unknown");
    expect(sections.at(-1)?.issues.map((i) => i.id)).toEqual(["1", "3"]);
    // No Unknown section at all when every issue resolves.
    const resolved = buildSubGroupSections(
      [mkIssue("1", "s-tri", 3)],
      "stateGroups",
      { states: sgStates, stateById: sgStateById, stateGroupById: sgMap },
    );
    expect(resolved.map((s) => s.key)).not.toContain(UNKNOWN_STATE_GROUP_KEY);
  });

  it("stateGroups: non-canonical group after the six and before Unknown (C139T1)", () => {
    const issues = [
      mkIssue("1", "s-rev", 3),
      mkIssue("2", "s-missing", 3),
      mkIssue("3", "s-tri", 3),
    ];
    const sections = buildSubGroupSections(issues, "stateGroups", {
      states: sgStates,
      stateById: sgStateById,
      stateGroupById: sgMap,
    });
    const keys = sections.map((s) => s.key);
    const idx = (k: string) => keys.indexOf(k);
    // Canonical six first (empty ones synthesized), then the
    // defensive group labelled with the backend's own string, then
    // Unknown last.
    expect(idx("stategroup:review")).toBeGreaterThan(
      idx("stategroup:cancelled"),
    );
    expect(idx("stategroup:review")).toBeLessThan(idx(UNKNOWN_STATE_GROUP_KEY));
    expect(sections[idx("stategroup:review")].title).toBe("review");
    expect(sections[idx("stategroup:review")].issues.map((i) => i.id)).toEqual([
      "1",
    ]);
  });

  it("stateGroups: missing map reads as no matches — all issues Unknown, no crash, never a raw id label (C139T1)", () => {
    const issues = [mkIssue("1", "s-tri", 3), mkIssue("2", "", 3)];
    const sections = buildSubGroupSections(issues, "stateGroups", {
      states: sgStates,
      stateById: sgStateById,
    });
    // includeEmptyGroups synthesizes the six canonical sections
    // (empty — the call site filters them when showEmptyGroups is
    // off); every issue still lands in Unknown, and only there.
    expect(sections.map((s) => s.key)).toEqual([
      "stategroup:triage",
      "stategroup:backlog",
      "stategroup:unstarted",
      "stategroup:started",
      "stategroup:completed",
      "stategroup:cancelled",
      UNKNOWN_STATE_GROUP_KEY,
    ]);
    for (const s of sections.slice(0, 6)) expect(s.issues).toEqual([]);
    expect(sections[6].issues.map((i) => i.id)).toEqual(["1", "2"]);
    expect(sections[6].title).toBe("Unknown");
  });

  it("stateGroups: degenerate under primary state — exactly one non-empty section, its own group (C139T1)", () => {
    // One primary state group's rows all share one state, hence one
    // group; the other five canonical sections synthesize empty and
    // the call site filters them when showEmptyGroups is off.
    const issues = [mkIssue("1", "s-tri", 3), mkIssue("2", "s-tri", 3)];
    const sections = buildSubGroupSections(issues, "stateGroups", {
      states: sgStates,
      stateById: sgStateById,
      stateGroupById: sgMap,
    });
    const nonEmpty = sections.filter((s) => s.issues.length > 0);
    expect(nonEmpty).toHaveLength(1);
    expect(nonEmpty[0].key).toBe("stategroup:triage");
    expect(nonEmpty[0].issues.map((i) => i.id)).toEqual(["1", "2"]);
  });
});
