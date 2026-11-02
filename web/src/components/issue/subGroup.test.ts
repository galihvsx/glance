// C123T1: sub-group helper contract tests (state / priority / epic
// sections + the effective rule). The helper file itself is a T1
// contract placeholder for T0's real helper — these tests pin the
// contract, so the merge check can re-run them against T0's version.
import { describe, expect, it } from "vitest";
import {
  buildSubGroupSections,
  effectiveSubGroupBy,
  type SubGroupContext,
} from "./subGroup";
import type { Issue, IssueState } from "../../lib/types";

function mkState(
  id: string,
  name: string,
  sequence: number,
  group = "unstarted",
): IssueState {
  return {
    id,
    project_id: "p",
    name,
    group,
    color: "#888",
    sequence,
    created_at: "",
    updated_at: "",
  };
}

function mkIssue(over: Partial<Issue> & { id: string }): Issue {
  return {
    project_id: "p",
    sequence_id: 1,
    display_id: "ENG-1",
    name: "test",
    priority: 0,
    state_id: "s-todo",
    sort_order: 0,
    is_draft: false,
    created_by: "u",
    created_at: "",
    updated_at: "",
    assignees: [],
    labels: [],
    ...over,
  };
}

const states = [
  mkState("s-done", "Done", 3, "completed"),
  mkState("s-todo", "Todo", 1),
  mkState("s-doing", "Doing", 2, "started"),
];
const ctx: SubGroupContext = {
  states,
  stateById: new Map(states.map((s) => [s.id, s])),
};

describe("effectiveSubGroupBy (C123T1)", () => {
  it("returns the stored dimension when it differs from groupBy", () => {
    expect(
      effectiveSubGroupBy({ groupBy: "state", subGroupBy: "priority" }),
    ).toBe("priority");
    expect(effectiveSubGroupBy({ groupBy: "priority", subGroupBy: "epic" })).toBe(
      "epic",
    );
  });

  it("renders same-as-primary as none", () => {
    expect(effectiveSubGroupBy({ groupBy: "state", subGroupBy: "state" })).toBe(
      "none",
    );
    expect(
      effectiveSubGroupBy({ groupBy: "priority", subGroupBy: "priority" }),
    ).toBe("none");
    expect(effectiveSubGroupBy({ groupBy: "epic", subGroupBy: "epic" })).toBe(
      "none",
    );
  });

  it("ignores the sub-group when groupBy is none", () => {
    expect(effectiveSubGroupBy({ groupBy: "none", subGroupBy: "state" })).toBe(
      "none",
    );
  });

  it("defaults absent or unknown stored values to none", () => {
    expect(effectiveSubGroupBy({ groupBy: "state" })).toBe("none");
    expect(
      effectiveSubGroupBy({
        groupBy: "state",
        subGroupBy: "labels" as never,
      }),
    ).toBe("none");
  });
});

describe("buildSubGroupSections (C123T1)", () => {
  it("none: a single section carrying all issues unchanged", () => {
    const issues = [
      mkIssue({ id: "a", state_id: "s-done" }),
      mkIssue({ id: "b", state_id: "s-todo" }),
    ];
    const secs = buildSubGroupSections(issues, "none", ctx);
    expect(secs).toHaveLength(1);
    expect(secs[0].issues.map((i) => i.id)).toEqual(["a", "b"]);
  });

  it("state: sections in workflow sequence order, empty kept for the call site", () => {
    const issues = [
      mkIssue({ id: "a", state_id: "s-done" }),
      mkIssue({ id: "b", state_id: "s-todo" }),
    ];
    const secs = buildSubGroupSections(issues, "state", ctx);
    expect(secs.map((s) => s.title)).toEqual(["Todo", "Doing", "Done"]);
    expect(secs[0].issues.map((i) => i.id)).toEqual(["b"]);
    expect(secs[1].issues).toEqual([]);
    expect(secs[2].issues.map((i) => i.id)).toEqual(["a"]);
    expect(secs[0].stateGroup).toBe("unstarted");
    expect(secs[2].stateGroup).toBe("completed");
  });

  it("state: unknown state_id lands in a trailing Ungrouped section", () => {
    const issues = [mkIssue({ id: "a", state_id: "s-gone" })];
    const secs = buildSubGroupSections(issues, "state", ctx);
    expect(secs[secs.length - 1].title).toBe("Ungrouped");
    expect(secs[secs.length - 1].issues.map((i) => i.id)).toEqual(["a"]);
  });

  it("priority: sections in 4,3,2,1,0 order with priority labels", () => {
    const issues = [
      mkIssue({ id: "a", priority: 0 }),
      mkIssue({ id: "b", priority: 4 }),
      mkIssue({ id: "c", priority: 2 }),
    ];
    const secs = buildSubGroupSections(issues, "priority", ctx);
    expect(secs.map((s) => s.title)).toEqual([
      "Urgent",
      "High",
      "Medium",
      "Low",
      "None",
    ]);
    expect(secs[0].issues.map((i) => i.id)).toEqual(["b"]);
    expect(secs[2].issues.map((i) => i.id)).toEqual(["c"]);
    expect(secs[4].issues.map((i) => i.id)).toEqual(["a"]);
  });

  it("epic: multi-appearance, archived excluded, Ungrouped last", () => {
    const issues = [
      mkIssue({
        id: "a",
        epics: [
          { id: "e-1", name: "Auth" },
          { id: "e-2", name: "Checkout" },
        ],
      }),
      mkIssue({
        id: "b",
        epics: [{ id: "e-1", name: "Auth", archived_at: "2026-01-01" }],
      }),
      mkIssue({ id: "c" }),
    ];
    const secs = buildSubGroupSections(issues, "epic", ctx);
    expect(secs.map((s) => s.title)).toEqual(["Auth", "Checkout", "Ungrouped"]);
    expect(secs[0].issues.map((i) => i.id)).toEqual(["a"]);
    expect(secs[1].issues.map((i) => i.id)).toEqual(["a"]);
    expect(secs[2].issues.map((i) => i.id)).toEqual(["b", "c"]);
  });
});
