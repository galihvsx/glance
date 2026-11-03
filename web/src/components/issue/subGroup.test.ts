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
    created_by: "u",
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
});
