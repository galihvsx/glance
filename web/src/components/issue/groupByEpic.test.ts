// C72T0: unit tests for groupIssuesByEpic — the pure grouping behind the
// issue list's Group-by-Epic mode. Semantics under test come from the
// cycle-72 plan: multi-appearance, archived exclusion, Ungrouped-last,
// empty-Ungrouped-hidden, and the 5-segment progress math.
import { describe, expect, it } from "vitest";
import {
  UNGROUPED_EPIC_KEY,
  groupIssuesByEpic,
  liveEpics,
} from "./groupByEpic";
import { doneCount } from "../initiatives/stateGroups";
import type { Issue } from "../../lib/types";

function mkIssue(
  id: string,
  epics: { id: string; name: string; archived_at?: string | null }[],
  stateId = "s-unstarted",
): Issue {
  return {
    id,
    project_id: "proj-1",
    sequence_id: 1,
    display_id: `ENG-${id}`,
    name: `issue ${id}`,
    priority: 0,
    state_id: stateId,
    sort_order: 0,
    is_draft: false,
    created_by: "u",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    assignees: [],
    labels: [],
    epics,
  } as Issue;
}

const STATE_GROUPS: Record<string, string> = {
  "s-backlog": "backlog",
  "s-unstarted": "unstarted",
  "s-started": "started",
  "s-completed": "completed",
  "s-cancelled": "cancelled",
};
const groupOf = (i: Issue) => STATE_GROUPS[i.state_id];

const EPIC_A = { id: "epic-a", name: "Alpha" };
const EPIC_B = { id: "epic-b", name: "Beta" };
const EPIC_ARCHIVED = {
  id: "epic-old",
  name: "Old",
  archived_at: "2026-05-01T00:00:00Z",
};

describe("groupIssuesByEpic", () => {
  it("groups issues by their live epic membership", () => {
    const issues = [
      mkIssue("1", [EPIC_A]),
      mkIssue("2", [EPIC_B]),
      mkIssue("3", [EPIC_A]),
    ];
    const groups = groupIssuesByEpic(issues, groupOf);
    expect(groups.map((g) => g.title)).toEqual(["Alpha", "Beta"]);
    const alpha = groups.find((g) => g.key === "epic-a")!;
    expect(alpha.issues.map((i) => i.id)).toEqual(["1", "3"]);
    const beta = groups.find((g) => g.key === "epic-b")!;
    expect(beta.issues.map((i) => i.id)).toEqual(["2"]);
  });

  it("multi-appearance: an issue with N live epics appears under EVERY epic group", () => {
    const issues = [mkIssue("1", [EPIC_A, EPIC_B])];
    const groups = groupIssuesByEpic(issues, groupOf);
    expect(groups).toHaveLength(2);
    for (const g of groups) {
      expect(g.issues.map((i) => i.id)).toEqual(["1"]);
    }
  });

  it("excludes archived epics from grouping", () => {
    const issues = [mkIssue("1", [EPIC_A, EPIC_ARCHIVED])];
    const groups = groupIssuesByEpic(issues, groupOf);
    expect(groups.map((g) => g.key)).toEqual(["epic-a"]);
    expect(groups[0].issues).toHaveLength(1);
  });

  it("sends issues whose only epics are archived into Ungrouped", () => {
    const issues = [mkIssue("1", [EPIC_ARCHIVED])];
    const groups = groupIssuesByEpic(issues, groupOf);
    expect(groups).toHaveLength(1);
    expect(groups[0].key).toBe(UNGROUPED_EPIC_KEY);
    expect(groups[0].title).toBe("Ungrouped");
    expect(groups[0].issues.map((i) => i.id)).toEqual(["1"]);
  });

  it("puts Ungrouped last, after all epic groups", () => {
    const issues = [
      mkIssue("1", []),
      mkIssue("2", [EPIC_B]),
      mkIssue("3", [EPIC_A]),
      mkIssue("4", []),
    ];
    const groups = groupIssuesByEpic(issues, groupOf);
    expect(groups.map((g) => g.key)).toEqual([
      "epic-b",
      "epic-a",
      UNGROUPED_EPIC_KEY,
    ]);
    const ungrouped = groups[groups.length - 1];
    expect(ungrouped.issues.map((i) => i.id)).toEqual(["1", "4"]);
  });

  it("hides the Ungrouped group when it would be empty", () => {
    const issues = [mkIssue("1", [EPIC_A]), mkIssue("2", [EPIC_B])];
    const groups = groupIssuesByEpic(issues, groupOf);
    expect(groups.map((g) => g.key)).not.toContain(UNGROUPED_EPIC_KEY);
    expect(groups).toHaveLength(2);
  });

  it("computes per-state-group progress counts for the header bar", () => {
    const issues = [
      mkIssue("1", [EPIC_A], "s-completed"),
      mkIssue("2", [EPIC_A], "s-completed"),
      mkIssue("3", [EPIC_A], "s-started"),
      mkIssue("4", [EPIC_A], "s-backlog"),
      mkIssue("5", [EPIC_A], "s-cancelled"),
      mkIssue("6", [EPIC_A], "s-unstarted"),
    ];
    const groups = groupIssuesByEpic(issues, groupOf);
    const alpha = groups.find((g) => g.key === "epic-a")!;
    expect(alpha.byGroup).toEqual({
      completed: 2,
      started: 1,
      backlog: 1,
      cancelled: 1,
      unstarted: 1,
    });
  });

  it("counts done as completed + cancelled (the C71 ruling)", () => {
    const issues = [
      mkIssue("1", [EPIC_A], "s-completed"),
      mkIssue("2", [EPIC_A], "s-cancelled"),
      mkIssue("3", [EPIC_A], "s-started"),
    ];
    const groups = groupIssuesByEpic(issues, groupOf);
    const alpha = groups.find((g) => g.key === "epic-a")!;
    expect(doneCount(alpha.byGroup)).toBe(2);
  });

  it("counts multi-appearance members in every group's progress", () => {
    const issues = [
      mkIssue("1", [EPIC_A, EPIC_B], "s-completed"),
      mkIssue("2", [EPIC_A], "s-started"),
    ];
    const groups = groupIssuesByEpic(issues, groupOf);
    const alpha = groups.find((g) => g.key === "epic-a")!;
    const beta = groups.find((g) => g.key === "epic-b")!;
    expect(alpha.byGroup).toEqual({ completed: 1, started: 1 });
    expect(beta.byGroup).toEqual({ completed: 1 });
  });

  it("liveEpics drops archived refs but keeps live ones", () => {
    const issue = mkIssue("1", [EPIC_A, EPIC_ARCHIVED]);
    expect(liveEpics(issue).map((e) => e.id)).toEqual(["epic-a"]);
  });
});
