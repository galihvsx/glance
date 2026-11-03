// C129T0: unit tests for groupIssuesByAssignee — the pure grouping
// behind the issue list's Group-by-Assignees mode. Semantics under test
// come from the cycle-129 plan: multi-appearance, Ungrouped-last,
// empty-Ungrouped-hidden, first-appearance order, and names from the row.
import { describe, expect, it } from "vitest";
import {
  UNGROUPED_ASSIGNEE_KEY,
  assigneesOf,
  groupIssuesByAssignee,
} from "./groupByAssignee";
import type { Issue } from "../../lib/types";

function mkIssue(
  id: string,
  assignees: { id: string; name: string }[],
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
    assignees,
    labels: [],
  } as Issue;
}

const ALICE = { id: "u-alice", name: "Alice" };
const BOB = { id: "u-bob", name: "Bob" };

describe("groupIssuesByAssignee", () => {
  it("groups issues by their assignees, keyed by assignee id", () => {
    const issues = [
      mkIssue("1", [ALICE]),
      mkIssue("2", [BOB]),
      mkIssue("3", [ALICE]),
    ];
    const groups = groupIssuesByAssignee(issues);
    expect(groups.map((g) => g.title)).toEqual(["Alice", "Bob"]);
    const alice = groups.find((g) => g.key === "u-alice")!;
    expect(alice.issues.map((i) => i.id)).toEqual(["1", "3"]);
    const bob = groups.find((g) => g.key === "u-bob")!;
    expect(bob.issues.map((i) => i.id)).toEqual(["2"]);
  });

  it("multi-appearance: an issue with N assignees appears under EVERY assignee group", () => {
    const issues = [mkIssue("1", [ALICE, BOB])];
    const groups = groupIssuesByAssignee(issues);
    expect(groups).toHaveLength(2);
    for (const g of groups) {
      expect(g.issues.map((i) => i.id)).toEqual(["1"]);
    }
  });

  it("keeps first-appearance order across the input list", () => {
    const issues = [
      mkIssue("1", [BOB]),
      mkIssue("2", [ALICE]),
      mkIssue("3", [BOB, ALICE]),
    ];
    const groups = groupIssuesByAssignee(issues);
    expect(groups.map((g) => g.key)).toEqual(["u-bob", "u-alice"]);
  });

  it("uses the assignee name from the row as the group title", () => {
    const issues = [mkIssue("1", [{ id: "u-x", name: "Xena Prince" }])];
    const groups = groupIssuesByAssignee(issues);
    expect(groups).toHaveLength(1);
    expect(groups[0].title).toBe("Xena Prince");
  });

  it("sends issues with no assignees into Ungrouped, last", () => {
    const issues = [
      mkIssue("1", []),
      mkIssue("2", [BOB]),
      mkIssue("3", [ALICE]),
      mkIssue("4", []),
    ];
    const groups = groupIssuesByAssignee(issues);
    expect(groups.map((g) => g.key)).toEqual([
      "u-bob",
      "u-alice",
      UNGROUPED_ASSIGNEE_KEY,
    ]);
    const ungrouped = groups[groups.length - 1];
    expect(ungrouped.title).toBe("Ungrouped");
    expect(ungrouped.isUngrouped).toBe(true);
    expect(ungrouped.issues.map((i) => i.id)).toEqual(["1", "4"]);
  });

  it("hides the Ungrouped group when it would be empty", () => {
    const issues = [mkIssue("1", [ALICE]), mkIssue("2", [BOB])];
    const groups = groupIssuesByAssignee(issues);
    expect(groups.map((g) => g.key)).not.toContain(UNGROUPED_ASSIGNEE_KEY);
    expect(groups).toHaveLength(2);
  });

  it("returns no groups for an empty list", () => {
    expect(groupIssuesByAssignee([])).toEqual([]);
  });

  it("assigneesOf returns [] for rows without the field, never crashes", () => {
    const issue = mkIssue("1", []);
    delete (issue as Partial<Issue>).assignees;
    expect(assigneesOf(issue)).toEqual([]);
    expect(groupIssuesByAssignee([issue])[0].isUngrouped).toBe(true);
  });
});
