// C135T1: unit tests for groupIssuesByStateGroup — the pure
// grouping behind the issue list's Group-by-State-groups mode.
// Semantics under test come from the cycle-135 plan, mirroring
// groupByCreatedBy (C134T1) with the state-group roll-up
// substituted: an issue appears under exactly ONE group (its
// state's group); the canonical six groups always emit in
// backend-fixed order (triage, backlog, unstarted, started,
// completed, cancelled); states with no loaded state collapse
// into ONE "Unknown" group, last, hidden when empty; a raw state
// id is NEVER a group label; "Show empty groups" synthesizes all
// six canonical groups, while Unknown still renders only when it
// has rows.
import { describe, expect, it } from "vitest";
import {
  STATE_GROUP_ORDER,
  UNKNOWN_STATE_GROUP_KEY,
  groupIssuesByStateGroup,
  stateGroupKey,
  stateGroupOf,
  type StateGroupIssueGroup,
} from "./groupByStateGroup";
import type { Issue } from "../../lib/types";

function mkIssue(id: string, stateId: string): Issue {
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
    created_by: "u-alice",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    assignees: [],
    labels: [],
  } as Issue;
}

// state id -> group string, as Issues.tsx builds it from the loaded
// project states (sortedStates carry sequence + group).
const STATE_GROUPS = new Map<string, string>([
  ["s-triage", "triage"],
  ["s-backlog", "backlog"],
  ["s-todo", "unstarted"],
  ["s-doing", "started"],
  ["s-done", "completed"],
  ["s-dead", "cancelled"],
]);

describe("groupIssuesByStateGroup", () => {
  it("groups issues by their state's group, in canonical order regardless of input order", () => {
    const issues = [
      mkIssue("1", "s-done"),
      mkIssue("2", "s-todo"),
      mkIssue("3", "s-doing"),
      mkIssue("4", "s-todo"),
    ];
    const groups = groupIssuesByStateGroup(issues, STATE_GROUPS);
    expect(groups.map((g) => g.key)).toEqual([
      "stategroup:unstarted",
      "stategroup:started",
      "stategroup:completed",
    ]);
    expect(groups.map((g) => g.title)).toEqual([
      "Unstarted",
      "Started",
      "Completed",
    ]);
    expect(groups[0].issues.map((i) => i.id)).toEqual(["2", "4"]);
  });

  it("single-appearance: every issue appears in exactly one group", () => {
    const issues = [
      mkIssue("1", "s-todo"),
      mkIssue("2", "s-done"),
      mkIssue("3", "s-gone"),
      mkIssue("4", "s-todo"),
    ];
    const groups = groupIssuesByStateGroup(issues, STATE_GROUPS);
    const appearances = groups.flatMap((g) => g.issues.map((i) => i.id));
    expect(appearances.sort()).toEqual(["1", "2", "3", "4"]);
    expect(new Set(appearances).size).toBe(4);
  });

  it("emits all six canonical groups in backend-fixed order", () => {
    expect([...STATE_GROUP_ORDER]).toEqual([
      "triage",
      "backlog",
      "unstarted",
      "started",
      "completed",
      "cancelled",
    ]);
    const issues = [
      mkIssue("1", "s-dead"),
      mkIssue("2", "s-triage"),
      mkIssue("3", "s-backlog"),
      mkIssue("4", "s-todo"),
      mkIssue("5", "s-doing"),
      mkIssue("6", "s-done"),
    ];
    const groups = groupIssuesByStateGroup(issues, STATE_GROUPS);
    expect(groups.map((g) => g.key)).toEqual(
      STATE_GROUP_ORDER.map((g) => stateGroupKey(g)),
    );
  });

  it("collapses all unresolved states into ONE Unknown group, last", () => {
    const issues = [
      mkIssue("1", "s-gone"),
      mkIssue("2", "s-todo"),
      mkIssue("3", "s-also-gone"),
      mkIssue("4", "s-done"),
    ];
    const groups = groupIssuesByStateGroup(issues, STATE_GROUPS);
    expect(groups.map((g) => g.key)).toEqual([
      "stategroup:unstarted",
      "stategroup:completed",
      UNKNOWN_STATE_GROUP_KEY,
    ]);
    const unknown = groups[groups.length - 1];
    expect(unknown.title).toBe("Unknown");
    expect(unknown.isUnknown).toBe(true);
    expect(unknown.group).toBe("unknown");
    expect(unknown.issues.map((i) => i.id)).toEqual(["1", "3"]);
  });

  it("NEVER renders a raw state id as a group label", () => {
    const issues = [mkIssue("1", "s-todo"), mkIssue("2", "s-gone")];
    const groups = groupIssuesByStateGroup(issues, STATE_GROUPS);
    for (const g of groups) {
      expect(g.title).not.toBe("s-todo");
      expect(g.title).not.toBe("s-gone");
    }
    expect(groups.map((g) => g.title)).toEqual(["Unstarted", "Unknown"]);
  });

  it("hides the Unknown group when it would be empty", () => {
    const issues = [mkIssue("1", "s-todo"), mkIssue("2", "s-done")];
    const groups = groupIssuesByStateGroup(issues, STATE_GROUPS);
    expect(groups.map((g) => g.key)).not.toContain(UNKNOWN_STATE_GROUP_KEY);
    expect(groups).toHaveLength(2);
  });

  it("carries NO color field on any group — state groups have none", () => {
    const issues = [mkIssue("1", "s-todo"), mkIssue("2", "s-gone")];
    const groups = groupIssuesByStateGroup(issues, STATE_GROUPS);
    for (const g of groups) {
      expect(g).not.toHaveProperty("color");
    }
  });

  it("carries NO breakdown field on any group", () => {
    const issues = [mkIssue("1", "s-todo")];
    const groups = groupIssuesByStateGroup(issues, STATE_GROUPS);
    for (const g of groups) {
      expect(g).not.toHaveProperty("byGroup");
    }
  });

  it("returns no groups for an empty list without includeEmptyGroups", () => {
    expect(groupIssuesByStateGroup([], STATE_GROUPS)).toEqual([]);
  });

  it("synthesizes all six canonical groups with includeEmptyGroups", () => {
    const groups = groupIssuesByStateGroup([], STATE_GROUPS, {
      includeEmptyGroups: true,
    });
    expect(groups.map((g) => g.key)).toEqual(
      STATE_GROUP_ORDER.map((g) => stateGroupKey(g)),
    );
    expect(groups.map((g) => g.title)).toEqual([
      "Triage",
      "Backlog",
      "Unstarted",
      "Started",
      "Completed",
      "Cancelled",
    ]);
    for (const g of groups) expect(g.issues).toEqual([]);
  });

  it("Unknown still renders only when it has rows, even with includeEmptyGroups", () => {
    const withRows = groupIssuesByStateGroup([mkIssue("1", "s-gone")], STATE_GROUPS, {
      includeEmptyGroups: true,
    });
    expect(withRows.map((g) => g.key)).toEqual([
      ...STATE_GROUP_ORDER.map((g) => stateGroupKey(g)),
      UNKNOWN_STATE_GROUP_KEY,
    ]);
    const withoutRows = groupIssuesByStateGroup([mkIssue("1", "s-todo")], STATE_GROUPS, {
      includeEmptyGroups: true,
    });
    expect(withoutRows.map((g) => g.key)).not.toContain(
      UNKNOWN_STATE_GROUP_KEY,
    );
    expect(withoutRows).toHaveLength(6);
  });

  it("rows with a missing/empty state_id group as Unknown, never crash", () => {
    const missing = mkIssue("1", "s-todo");
    (missing as Partial<Issue>).state_id = undefined as unknown as string;
    const empty = mkIssue("2", "");
    const groups = groupIssuesByStateGroup([missing, empty], STATE_GROUPS);
    expect(groups).toHaveLength(1);
    expect(groups[0].isUnknown).toBe(true);
    expect(groups[0].issues.map((i) => i.id)).toEqual(["1", "2"]);
  });

  it("a non-canonical group string forms its own group after the canonical six, before Unknown (defensive only)", () => {
    const lookup = new Map([...STATE_GROUPS, ["s-weird", "weird"]]);
    const issues = [
      mkIssue("1", "s-weird"),
      mkIssue("2", "s-todo"),
      mkIssue("3", "s-gone"),
    ];
    const groups = groupIssuesByStateGroup(issues, lookup);
    expect(groups.map((g) => g.key)).toEqual([
      "stategroup:unstarted",
      "stategroup:weird",
      UNKNOWN_STATE_GROUP_KEY,
    ]);
    expect(groups[1].issues.map((i) => i.id)).toEqual(["1"]);
  });

  it("stateGroupOf returns the group, or null when unresolved", () => {
    expect(stateGroupOf(mkIssue("1", "s-doing"), STATE_GROUPS)).toBe(
      "started",
    );
    expect(stateGroupOf(mkIssue("2", "s-gone"), STATE_GROUPS)).toBeNull();
    expect(stateGroupOf(mkIssue("3", ""), STATE_GROUPS)).toBeNull();
  });

  it("exposes the group slug on each group for the page's quick-add default", () => {
    const groups: StateGroupIssueGroup[] = groupIssuesByStateGroup(
      [mkIssue("1", "s-doing")],
      STATE_GROUPS,
    );
    expect(groups[0].group).toBe("started");
  });
});
