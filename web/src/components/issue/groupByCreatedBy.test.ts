// C134T1: unit tests for groupIssuesByCreatedBy — the pure grouping
// behind the issue list's Group-by-Created-by mode. Semantics under
// test come from the cycle-134 plan, mirroring groupByRelease (C133T1)
// with creator ids substituted: an issue appears under exactly ONE
// creator group; known groups keep first-appearance order; creators
// with no workspace member collapse into ONE "Unknown" group, last,
// hidden when empty; labels resolve as name ?? email from the members
// map — a raw user id is NEVER a group label.
import { describe, expect, it } from "vitest";
import {
  UNKNOWN_CREATOR_KEY,
  creatorLabel,
  groupIssuesByCreatedBy,
  type CreatorLookupEntry,
} from "./groupByCreatedBy";
import type { Issue } from "../../lib/types";

function mkIssue(id: string, createdBy: string): Issue {
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
    created_by: createdBy,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    assignees: [],
    labels: [],
  } as Issue;
}

const MEMBERS = new Map<string, CreatorLookupEntry>([
  ["u-alice", { name: "Alice", email: "alice@x.com" }],
  // Name null on the wire → label falls back to email.
  ["u-bob", { name: null, email: "bob@x.com" }],
]);

describe("groupIssuesByCreatedBy", () => {
  it("groups issues by creator id, labelled from the members map", () => {
    const issues = [
      mkIssue("1", "u-alice"),
      mkIssue("2", "u-bob"),
      mkIssue("3", "u-alice"),
    ];
    const groups = groupIssuesByCreatedBy(issues, MEMBERS);
    expect(groups.map((g) => g.title)).toEqual(["Alice", "bob@x.com"]);
    const alice = groups.find((g) => g.key === "u-alice")!;
    expect(alice.issues.map((i) => i.id)).toEqual(["1", "3"]);
    const bob = groups.find((g) => g.key === "u-bob")!;
    expect(bob.issues.map((i) => i.id)).toEqual(["2"]);
  });

  it("single-appearance: every issue appears in exactly one group", () => {
    const issues = [
      mkIssue("1", "u-alice"),
      mkIssue("2", "u-bob"),
      mkIssue("3", "u-gone"),
      mkIssue("4", "u-alice"),
    ];
    const groups = groupIssuesByCreatedBy(issues, MEMBERS);
    const appearances = groups.flatMap((g) => g.issues.map((i) => i.id));
    expect(appearances.sort()).toEqual(["1", "2", "3", "4"]);
    expect(new Set(appearances).size).toBe(4);
  });

  it("keeps first-appearance order across the input list", () => {
    const issues = [
      mkIssue("1", "u-bob"),
      mkIssue("2", "u-alice"),
      mkIssue("3", "u-bob"),
    ];
    const groups = groupIssuesByCreatedBy(issues, MEMBERS);
    expect(groups.map((g) => g.key)).toEqual(["u-bob", "u-alice"]);
  });

  it("labels by name when present, email when name is null", () => {
    expect(creatorLabel("u-alice", MEMBERS)).toBe("Alice");
    expect(creatorLabel("u-bob", MEMBERS)).toBe("bob@x.com");
  });

  it("NEVER renders a raw user id as a group label", () => {
    const issues = [mkIssue("1", "u-alice"), mkIssue("2", "u-gone")];
    const groups = groupIssuesByCreatedBy(issues, MEMBERS);
    for (const g of groups) {
      expect(g.title).not.toBe("u-alice");
      expect(g.title).not.toBe("u-gone");
    }
    expect(groups.map((g) => g.title)).toEqual(["Alice", "Unknown"]);
  });

  it("collapses all unresolved creators into ONE Unknown group, last", () => {
    const issues = [
      mkIssue("1", "u-gone"),
      mkIssue("2", "u-alice"),
      mkIssue("3", "u-also-gone"),
      mkIssue("4", "u-bob"),
    ];
    const groups = groupIssuesByCreatedBy(issues, MEMBERS);
    expect(groups.map((g) => g.key)).toEqual([
      "u-alice",
      "u-bob",
      UNKNOWN_CREATOR_KEY,
    ]);
    const unknown = groups[groups.length - 1];
    expect(unknown.title).toBe("Unknown");
    expect(unknown.isUnknown).toBe(true);
    expect(unknown.issues.map((i) => i.id)).toEqual(["1", "3"]);
  });

  it("hides the Unknown group when it would be empty", () => {
    const issues = [mkIssue("1", "u-alice"), mkIssue("2", "u-bob")];
    const groups = groupIssuesByCreatedBy(issues, MEMBERS);
    expect(groups.map((g) => g.key)).not.toContain(UNKNOWN_CREATOR_KEY);
    expect(groups).toHaveLength(2);
  });

  it("carries NO color field on any group — creators have none", () => {
    const issues = [mkIssue("1", "u-alice"), mkIssue("2", "u-gone")];
    const groups = groupIssuesByCreatedBy(issues, MEMBERS);
    for (const g of groups) {
      expect(g).not.toHaveProperty("color");
    }
  });

  it("returns no groups for an empty list", () => {
    expect(groupIssuesByCreatedBy([], MEMBERS)).toEqual([]);
  });

  it("rows with a missing/empty created_by group as Unknown, never crash", () => {
    const missing = mkIssue("1", "u-alice");
    (missing as Partial<Issue>).created_by = undefined as unknown as string;
    const empty = mkIssue("2", "");
    const groups = groupIssuesByCreatedBy([missing, empty], MEMBERS);
    expect(groups).toHaveLength(1);
    expect(groups[0].isUnknown).toBe(true);
    expect(groups[0].issues.map((i) => i.id)).toEqual(["1", "2"]);
  });

  it("creatorLabel returns null for an unresolved creator", () => {
    expect(creatorLabel("u-gone", MEMBERS)).toBeNull();
  });
});
