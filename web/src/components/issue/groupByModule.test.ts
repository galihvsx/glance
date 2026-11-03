// C131T0: unit tests for groupIssuesByModule — the pure grouping
// behind the issue list's Group-by-Modules mode. Semantics under test
// come from the cycle-131 plan, mirroring groupByLabel (C130T0):
// multi-appearance, Ungrouped-last, empty-Ungrouped-hidden,
// first-appearance order, and name from the row
// (first-appearance row wins on disagreement) — with NO color:
// NamedRef carries id + name only, so groups carry no color field.
import { describe, expect, it } from "vitest";
import {
  UNGROUPED_MODULE_KEY,
  groupIssuesByModule,
  modulesOf,
} from "./groupByModule";
import type { Issue } from "../../lib/types";

function mkIssue(
  id: string,
  modules: { id: string; name: string }[],
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
    project_structure: { modules },
  } as Issue;
}

const CORE = { id: "m-core", name: "Core" };
const WEB = { id: "m-web", name: "Web" };

describe("groupIssuesByModule", () => {
  it("groups issues by their modules, keyed by module id", () => {
    const issues = [
      mkIssue("1", [CORE]),
      mkIssue("2", [WEB]),
      mkIssue("3", [CORE]),
    ];
    const groups = groupIssuesByModule(issues);
    expect(groups.map((g) => g.title)).toEqual(["Core", "Web"]);
    const core = groups.find((g) => g.key === "m-core")!;
    expect(core.issues.map((i) => i.id)).toEqual(["1", "3"]);
    const web = groups.find((g) => g.key === "m-web")!;
    expect(web.issues.map((i) => i.id)).toEqual(["2"]);
  });

  it("multi-appearance: an issue with N modules appears under EVERY module group", () => {
    const issues = [mkIssue("1", [CORE, WEB])];
    const groups = groupIssuesByModule(issues);
    expect(groups).toHaveLength(2);
    for (const g of groups) {
      expect(g.issues.map((i) => i.id)).toEqual(["1"]);
    }
  });

  it("keeps first-appearance order across the input list", () => {
    const issues = [
      mkIssue("1", [WEB]),
      mkIssue("2", [CORE]),
      mkIssue("3", [WEB, CORE]),
    ];
    const groups = groupIssuesByModule(issues);
    expect(groups.map((g) => g.key)).toEqual(["m-web", "m-core"]);
  });

  it("uses the module name from the row as the group title", () => {
    const issues = [mkIssue("1", [{ id: "m-x", name: "Docs" }])];
    const groups = groupIssuesByModule(issues);
    expect(groups).toHaveLength(1);
    expect(groups[0].title).toBe("Docs");
  });

  it("first-appearance row wins when rows disagree on a module's name", () => {
    const issues = [
      mkIssue("1", [{ id: "m-x", name: "Docs" }]),
      mkIssue("2", [{ id: "m-x", name: "Documentation" }]),
    ];
    const groups = groupIssuesByModule(issues);
    expect(groups).toHaveLength(1);
    expect(groups[0].title).toBe("Docs");
    expect(groups[0].issues.map((i) => i.id)).toEqual(["1", "2"]);
  });

  it("carries NO color field on any group — modules have none", () => {
    const issues = [mkIssue("1", [CORE]), mkIssue("2", [])];
    const groups = groupIssuesByModule(issues);
    for (const g of groups) {
      expect(g).not.toHaveProperty("color");
    }
  });

  it("sends issues with no modules into Ungrouped, last", () => {
    const issues = [
      mkIssue("1", []),
      mkIssue("2", [WEB]),
      mkIssue("3", [CORE]),
      mkIssue("4", []),
    ];
    const groups = groupIssuesByModule(issues);
    expect(groups.map((g) => g.key)).toEqual([
      "m-web",
      "m-core",
      UNGROUPED_MODULE_KEY,
    ]);
    const ungrouped = groups[groups.length - 1];
    expect(ungrouped.title).toBe("Ungrouped");
    expect(ungrouped.isUngrouped).toBe(true);
    expect(ungrouped.issues.map((i) => i.id)).toEqual(["1", "4"]);
  });

  it("hides the Ungrouped group when it would be empty", () => {
    const issues = [mkIssue("1", [CORE]), mkIssue("2", [WEB])];
    const groups = groupIssuesByModule(issues);
    expect(groups.map((g) => g.key)).not.toContain(UNGROUPED_MODULE_KEY);
    expect(groups).toHaveLength(2);
  });

  it("returns no groups for an empty list", () => {
    expect(groupIssuesByModule([])).toEqual([]);
  });

  it("modulesOf returns [] for rows without project_structure, never crashes", () => {
    const issue = mkIssue("1", []);
    delete (issue as Partial<Issue>).project_structure;
    expect(modulesOf(issue)).toEqual([]);
    expect(groupIssuesByModule([issue])[0].isUngrouped).toBe(true);
  });

  it("modulesOf returns [] for rows with project_structure but no modules field", () => {
    const issue = mkIssue("1", []);
    const bare = { ...issue, project_structure: {} } as unknown as Issue;
    expect(modulesOf(bare)).toEqual([]);
  });
});
