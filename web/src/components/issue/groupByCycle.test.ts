// C132T1: unit tests for groupIssuesByCycle — the pure grouping
// behind the issue list's Group-by-Cycles mode. Semantics under test
// come from the cycle-132 plan, mirroring groupByModule (C131T0)
// with the single-appearance difference: an issue appears under
// exactly ONE cycle group or Ungrouped; Ungrouped-last,
// empty-Ungrouped-hidden, first-appearance order, and name from the
// row (first-appearance row wins on disagreement) — with NO color:
// NamedRef carries id + name only, so groups carry no color field.
import { describe, expect, it } from "vitest";
import {
  UNGROUPED_CYCLE_KEY,
  cycleOf,
  groupIssuesByCycle,
} from "./groupByCycle";
import type { Issue } from "../../lib/types";

function mkIssue(
  id: string,
  cycle: { id: string; name: string } | null,
): Issue {
  return {
    id,
    project_id: "proj-1",
    sequence_id: 1,
    display_id: `ENG-${id}`,
    name: `issue ${id}`,
    priority: 0,
    state_id: "s-unstarted",
    sort_order: 0,
    is_draft: false,
    created_by: "u",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    assignees: [],
    labels: [],
    project_structure: { cycle, modules: [] },
  } as Issue;
}

const SPRINT_1 = { id: "c-1", name: "Sprint 1" };
const SPRINT_2 = { id: "c-2", name: "Sprint 2" };

describe("groupIssuesByCycle", () => {
  it("groups issues by their displayed cycle, keyed by cycle id", () => {
    const issues = [
      mkIssue("1", SPRINT_1),
      mkIssue("2", SPRINT_2),
      mkIssue("3", SPRINT_1),
    ];
    const groups = groupIssuesByCycle(issues);
    expect(groups.map((g) => g.title)).toEqual(["Sprint 1", "Sprint 2"]);
    const s1 = groups.find((g) => g.key === "c-1")!;
    expect(s1.issues.map((i) => i.id)).toEqual(["1", "3"]);
    const s2 = groups.find((g) => g.key === "c-2")!;
    expect(s2.issues.map((i) => i.id)).toEqual(["2"]);
  });

  it("single-appearance: every issue appears in exactly one group", () => {
    const issues = [
      mkIssue("1", SPRINT_1),
      mkIssue("2", SPRINT_2),
      mkIssue("3", null),
      mkIssue("4", SPRINT_1),
    ];
    const groups = groupIssuesByCycle(issues);
    const appearances = groups.flatMap((g) => g.issues.map((i) => i.id));
    expect(appearances.sort()).toEqual(["1", "2", "3", "4"]);
    expect(new Set(appearances).size).toBe(4);
  });

  it("keeps first-appearance order across the input list", () => {
    const issues = [
      mkIssue("1", SPRINT_2),
      mkIssue("2", SPRINT_1),
      mkIssue("3", SPRINT_2),
    ];
    const groups = groupIssuesByCycle(issues);
    expect(groups.map((g) => g.key)).toEqual(["c-2", "c-1"]);
  });

  it("uses the cycle name from the row as the group title", () => {
    const issues = [mkIssue("1", { id: "c-x", name: "Sprint X" })];
    const groups = groupIssuesByCycle(issues);
    expect(groups).toHaveLength(1);
    expect(groups[0].title).toBe("Sprint X");
  });

  it("first-appearance row wins when rows disagree on a cycle's name", () => {
    const issues = [
      mkIssue("1", { id: "c-x", name: "Sprint X" }),
      mkIssue("2", { id: "c-x", name: "Sprint Ex" }),
    ];
    const groups = groupIssuesByCycle(issues);
    expect(groups).toHaveLength(1);
    expect(groups[0].title).toBe("Sprint X");
    expect(groups[0].issues.map((i) => i.id)).toEqual(["1", "2"]);
  });

  it("carries NO color field on any group — cycles have none", () => {
    const issues = [mkIssue("1", SPRINT_1), mkIssue("2", null)];
    const groups = groupIssuesByCycle(issues);
    for (const g of groups) {
      expect(g).not.toHaveProperty("color");
    }
  });

  it("sends issues with no cycle into Ungrouped, last", () => {
    const issues = [
      mkIssue("1", null),
      mkIssue("2", SPRINT_2),
      mkIssue("3", SPRINT_1),
      mkIssue("4", null),
    ];
    const groups = groupIssuesByCycle(issues);
    expect(groups.map((g) => g.key)).toEqual([
      "c-2",
      "c-1",
      UNGROUPED_CYCLE_KEY,
    ]);
    const ungrouped = groups[groups.length - 1];
    expect(ungrouped.title).toBe("Ungrouped");
    expect(ungrouped.isUngrouped).toBe(true);
    expect(ungrouped.issues.map((i) => i.id)).toEqual(["1", "4"]);
  });

  it("hides the Ungrouped group when it would be empty", () => {
    const issues = [mkIssue("1", SPRINT_1), mkIssue("2", SPRINT_2)];
    const groups = groupIssuesByCycle(issues);
    expect(groups.map((g) => g.key)).not.toContain(UNGROUPED_CYCLE_KEY);
    expect(groups).toHaveLength(2);
  });

  it("returns no groups for an empty list", () => {
    expect(groupIssuesByCycle([])).toEqual([]);
  });

  it("cycleOf returns null for a null cycle, never crashes", () => {
    expect(cycleOf(mkIssue("1", null))).toBeNull();
    expect(groupIssuesByCycle([mkIssue("1", null)])[0].isUngrouped).toBe(
      true,
    );
  });

  it("rows with a missing project_structure group as Ungrouped", () => {
    const issue = mkIssue("1", SPRINT_1);
    delete (issue as Partial<Issue>).project_structure;
    expect(cycleOf(issue)).toBeNull();
    expect(groupIssuesByCycle([issue])[0].isUngrouped).toBe(true);
  });

  it("rows with project_structure but no cycle field group as Ungrouped", () => {
    const issue = mkIssue("1", SPRINT_1);
    const bare = { ...issue, project_structure: {} } as unknown as Issue;
    expect(cycleOf(bare)).toBeNull();
    expect(groupIssuesByCycle([bare])[0].isUngrouped).toBe(true);
  });
});
