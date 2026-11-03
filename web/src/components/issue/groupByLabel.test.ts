// C130T0: unit tests for groupIssuesByLabel — the pure grouping
// behind the issue list's Group-by-Labels mode. Semantics under test
// come from the cycle-130 plan, mirroring groupByAssignee (C129T0):
// multi-appearance, Ungrouped-last, empty-Ungrouped-hidden,
// first-appearance order, and name AND color from the row
// (first-appearance row wins on disagreement).
import { describe, expect, it } from "vitest";
import {
  UNGROUPED_LABEL_KEY,
  groupIssuesByLabel,
  labelsOf,
} from "./groupByLabel";
import type { Issue } from "../../lib/types";

function mkIssue(
  id: string,
  labels: { id: string; name: string; color: string }[],
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
    labels,
  } as Issue;
}

const BUG = { id: "l-bug", name: "Bug", color: "#e22718" };
const FEAT = { id: "l-feat", name: "Feature", color: "#0066b1" };

describe("groupIssuesByLabel", () => {
  it("groups issues by their labels, keyed by label id", () => {
    const issues = [
      mkIssue("1", [BUG]),
      mkIssue("2", [FEAT]),
      mkIssue("3", [BUG]),
    ];
    const groups = groupIssuesByLabel(issues);
    expect(groups.map((g) => g.title)).toEqual(["Bug", "Feature"]);
    const bug = groups.find((g) => g.key === "l-bug")!;
    expect(bug.issues.map((i) => i.id)).toEqual(["1", "3"]);
    const feat = groups.find((g) => g.key === "l-feat")!;
    expect(feat.issues.map((i) => i.id)).toEqual(["2"]);
  });

  it("multi-appearance: an issue with N labels appears under EVERY label group", () => {
    const issues = [mkIssue("1", [BUG, FEAT])];
    const groups = groupIssuesByLabel(issues);
    expect(groups).toHaveLength(2);
    for (const g of groups) {
      expect(g.issues.map((i) => i.id)).toEqual(["1"]);
    }
  });

  it("keeps first-appearance order across the input list", () => {
    const issues = [
      mkIssue("1", [FEAT]),
      mkIssue("2", [BUG]),
      mkIssue("3", [FEAT, BUG]),
    ];
    const groups = groupIssuesByLabel(issues);
    expect(groups.map((g) => g.key)).toEqual(["l-feat", "l-bug"]);
  });

  it("uses the label name AND color from the row as the group title/color", () => {
    const issues = [
      mkIssue("1", [{ id: "l-x", name: "Docs", color: "#1c69d4" }]),
    ];
    const groups = groupIssuesByLabel(issues);
    expect(groups).toHaveLength(1);
    expect(groups[0].title).toBe("Docs");
    expect(groups[0].color).toBe("#1c69d4");
  });

  it("first-appearance row wins when rows disagree on a label's name/color", () => {
    const issues = [
      mkIssue("1", [{ id: "l-x", name: "Docs", color: "#111111" }]),
      mkIssue("2", [{ id: "l-x", name: "Documentation", color: "#222222" }]),
    ];
    const groups = groupIssuesByLabel(issues);
    expect(groups).toHaveLength(1);
    expect(groups[0].title).toBe("Docs");
    expect(groups[0].color).toBe("#111111");
    expect(groups[0].issues.map((i) => i.id)).toEqual(["1", "2"]);
  });

  it("sends issues with no labels into Ungrouped, last", () => {
    const issues = [
      mkIssue("1", []),
      mkIssue("2", [FEAT]),
      mkIssue("3", [BUG]),
      mkIssue("4", []),
    ];
    const groups = groupIssuesByLabel(issues);
    expect(groups.map((g) => g.key)).toEqual([
      "l-feat",
      "l-bug",
      UNGROUPED_LABEL_KEY,
    ]);
    const ungrouped = groups[groups.length - 1];
    expect(ungrouped.title).toBe("Ungrouped");
    expect(ungrouped.isUngrouped).toBe(true);
    expect(ungrouped.color).toBeUndefined();
    expect(ungrouped.issues.map((i) => i.id)).toEqual(["1", "4"]);
  });

  it("hides the Ungrouped group when it would be empty", () => {
    const issues = [mkIssue("1", [BUG]), mkIssue("2", [FEAT])];
    const groups = groupIssuesByLabel(issues);
    expect(groups.map((g) => g.key)).not.toContain(UNGROUPED_LABEL_KEY);
    expect(groups).toHaveLength(2);
  });

  it("returns no groups for an empty list", () => {
    expect(groupIssuesByLabel([])).toEqual([]);
  });

  it("labelsOf returns [] for rows without the field, never crashes", () => {
    const issue = mkIssue("1", []);
    delete (issue as Partial<Issue>).labels;
    expect(labelsOf(issue)).toEqual([]);
    expect(groupIssuesByLabel([issue])[0].isUngrouped).toBe(true);
  });
});
