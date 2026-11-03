// C133T1: unit tests for groupIssuesByRelease — the pure grouping
// behind the issue list's Group-by-Releases mode. Semantics under test
// come from the cycle-133 plan, mirroring groupByCycle (C132T1)
// with releases substituted: an issue appears under exactly ONE
// release group or Ungrouped; Ungrouped-last, empty-Ungrouped-hidden,
// first-appearance order, and name from the row (first-appearance row
// wins on disagreement) — with NO color: NamedRef carries id + name
// only, so groups carry no color field. No status-based exclusion:
// a released release groups like any other (the row carries no status).
import { describe, expect, it } from "vitest";
import {
  UNGROUPED_RELEASE_KEY,
  groupIssuesByRelease,
  releaseOf,
} from "./groupByRelease";
import type { Issue } from "../../lib/types";

function mkIssue(
  id: string,
  release: { id: string; name: string } | null,
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
    project_structure: { release, modules: [] },
  } as Issue;
}

const REL_1 = { id: "r-1", name: "v1.0" };
const REL_2 = { id: "r-2", name: "v2.0" };

describe("groupIssuesByRelease", () => {
  it("groups issues by their displayed release, keyed by release id", () => {
    const issues = [
      mkIssue("1", REL_1),
      mkIssue("2", REL_2),
      mkIssue("3", REL_1),
    ];
    const groups = groupIssuesByRelease(issues);
    expect(groups.map((g) => g.title)).toEqual(["v1.0", "v2.0"]);
    const r1 = groups.find((g) => g.key === "r-1")!;
    expect(r1.issues.map((i) => i.id)).toEqual(["1", "3"]);
    const r2 = groups.find((g) => g.key === "r-2")!;
    expect(r2.issues.map((i) => i.id)).toEqual(["2"]);
  });

  it("single-appearance: every issue appears in exactly one group", () => {
    const issues = [
      mkIssue("1", REL_1),
      mkIssue("2", REL_2),
      mkIssue("3", null),
      mkIssue("4", REL_1),
    ];
    const groups = groupIssuesByRelease(issues);
    const appearances = groups.flatMap((g) => g.issues.map((i) => i.id));
    expect(appearances.sort()).toEqual(["1", "2", "3", "4"]);
    expect(new Set(appearances).size).toBe(4);
  });

  it("keeps first-appearance order across the input list", () => {
    const issues = [
      mkIssue("1", REL_2),
      mkIssue("2", REL_1),
      mkIssue("3", REL_2),
    ];
    const groups = groupIssuesByRelease(issues);
    expect(groups.map((g) => g.key)).toEqual(["r-2", "r-1"]);
  });

  it("uses the release name from the row as the group title", () => {
    const issues = [mkIssue("1", { id: "r-x", name: "v9.9" })];
    const groups = groupIssuesByRelease(issues);
    expect(groups).toHaveLength(1);
    expect(groups[0].title).toBe("v9.9");
  });

  it("first-appearance row wins when rows disagree on a release's name", () => {
    const issues = [
      mkIssue("1", { id: "r-x", name: "v9.9" }),
      mkIssue("2", { id: "r-x", name: "v9.9b" }),
    ];
    const groups = groupIssuesByRelease(issues);
    expect(groups).toHaveLength(1);
    expect(groups[0].title).toBe("v9.9");
    expect(groups[0].issues.map((i) => i.id)).toEqual(["1", "2"]);
  });

  it("carries NO color field on any group — releases have none", () => {
    const issues = [mkIssue("1", REL_1), mkIssue("2", null)];
    const groups = groupIssuesByRelease(issues);
    for (const g of groups) {
      expect(g).not.toHaveProperty("color");
    }
  });

  it("sends issues with no release into Ungrouped, last", () => {
    const issues = [
      mkIssue("1", null),
      mkIssue("2", REL_2),
      mkIssue("3", REL_1),
      mkIssue("4", null),
    ];
    const groups = groupIssuesByRelease(issues);
    expect(groups.map((g) => g.key)).toEqual([
      "r-2",
      "r-1",
      UNGROUPED_RELEASE_KEY,
    ]);
    const ungrouped = groups[groups.length - 1];
    expect(ungrouped.title).toBe("Ungrouped");
    expect(ungrouped.isUngrouped).toBe(true);
    expect(ungrouped.issues.map((i) => i.id)).toEqual(["1", "4"]);
  });

  it("hides the Ungrouped group when it would be empty", () => {
    const issues = [mkIssue("1", REL_1), mkIssue("2", REL_2)];
    const groups = groupIssuesByRelease(issues);
    expect(groups.map((g) => g.key)).not.toContain(UNGROUPED_RELEASE_KEY);
    expect(groups).toHaveLength(2);
  });

  it("returns no groups for an empty list", () => {
    expect(groupIssuesByRelease([])).toEqual([]);
  });

  it("releaseOf returns null for a null release, never crashes", () => {
    expect(releaseOf(mkIssue("1", null))).toBeNull();
    expect(groupIssuesByRelease([mkIssue("1", null)])[0].isUngrouped).toBe(
      true,
    );
  });

  it("rows with a missing project_structure group as Ungrouped", () => {
    const issue = mkIssue("1", REL_1);
    delete (issue as Partial<Issue>).project_structure;
    expect(releaseOf(issue)).toBeNull();
    expect(groupIssuesByRelease([issue])[0].isUngrouped).toBe(true);
  });

  it("rows with project_structure but no release field group as Ungrouped", () => {
    const issue = mkIssue("1", REL_1);
    const bare = { ...issue, project_structure: {} } as unknown as Issue;
    expect(releaseOf(bare)).toBeNull();
    expect(groupIssuesByRelease([bare])[0].isUngrouped).toBe(true);
  });

  it("rows with null vs missing project_structure both group as Ungrouped", () => {
    const withNull = mkIssue("1", null);
    const missing = mkIssue("2", REL_1);
    delete (missing as Partial<Issue>).project_structure;
    const groups = groupIssuesByRelease([withNull, missing]);
    expect(groups).toHaveLength(1);
    expect(groups[0].isUngrouped).toBe(true);
    expect(groups[0].issues.map((i) => i.id)).toEqual(["1", "2"]);
  });
});
