// C134T2: unit tests for the board's Group-by-Created-by pure helpers
// (boardCreatedBy.ts), mirroring boardRelease.test.ts. Covers the plan's
// rulings: creator columns in first-appearance order, single-appearance,
// Unknown-last/hidden-when-empty, labels from the members map (name ??
// email, NEVER a raw user id), compound id parse/round-trip, and the
// drop-plan matrix — cross-column drops are ALWAYS a deliberate no-op
// (created_by is immutable; there is no move pipeline to test, no
// executor, and no quick-add plan — those helpers deliberately do not
// exist, and the disabled quick-add is covered at the page level in
// BoardGroupByCreatedBy.test.tsx). Within-column reorder is covered
// by the applyCreatedByReorder pipeline mirroring (same stack math as
// the release path), not by a simulated drag.

import { describe, expect, it } from "vitest";
import {
  CREATEDBY_CARD_PREFIX,
  CREATEDBY_COLUMN_PREFIX,
  CREATEDBY_UNKNOWN_COLUMN_KEY,
  buildCreatedByBoardGroups,
  createdByCardDragId,
  createdByDragColumnKey,
  createdByDragIssueId,
  createdByDropColumnKey,
  planCreatedByDrop,
} from "./boardCreatedBy";
import type { CreatorLookupEntry } from "./groupByCreatedBy";
import type { Issue } from "../../lib/types";

function mkIssue(id: string, createdBy: string, stateId = "s-unstarted"): Issue {
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
    created_by: createdBy,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    assignees: [],
    labels: [],
    epics: [],
    project_structure: { release: null, modules: [] },
  } as Issue;
}

const MEMBERS = new Map<string, CreatorLookupEntry>([
  ["u-alice", { name: "Alice", email: "alice@x.com" }],
  ["u-bob", { name: null, email: "bob@x.com" }],
  ["u-carol", { name: "Carol", email: "carol@x.com" }],
]);

describe("buildCreatedByBoardGroups", () => {
  it("builds one column per creator, first-appearance order, with the state stamp and NO color field", () => {
    const issues = [
      mkIssue("1", "u-alice"),
      mkIssue("2", "u-bob"),
      mkIssue("3", "u-alice"),
    ];
    const groups = buildCreatedByBoardGroups(issues, MEMBERS, "s-backlog");
    expect(groups.map((g) => g.title)).toEqual(["Alice", "bob@x.com"]);
    expect(groups[0].droppableId).toBe(`${CREATEDBY_COLUMN_PREFIX}u-alice`);
    expect(groups[0].columnKey).toBe("u-alice");
    expect(groups[0].creatorId).toBe("u-alice");
    expect("color" in groups[0]).toBe(false);
    expect(groups[0].issues.map((i) => i.id)).toEqual(["1", "3"]);
    expect(groups[0].newStateId).toBe("s-backlog");
  });

  it("single-appearance: an issue appears in exactly ONE creator column", () => {
    const issues = [mkIssue("1", "u-alice"), mkIssue("2", "u-bob")];
    const groups = buildCreatedByBoardGroups(issues, MEMBERS);
    expect(groups).toHaveLength(2);
    expect(groups.flatMap((g) => g.issues.map((i) => i.id))).toEqual([
      "1",
      "2",
    ]);
  });

  it("label is name ?? email: a member with no name falls back to email, never the id", () => {
    const groups = buildCreatedByBoardGroups([mkIssue("1", "u-bob")], MEMBERS);
    expect(groups.map((g) => g.title)).toEqual(["bob@x.com"]);
    expect(groups[0].title).not.toBe("u-bob");
  });

  it("Unknown is always last and hidden when empty", () => {
    const issues = [mkIssue("1", "u-gone"), mkIssue("2", "u-alice")];
    const withUnknown = buildCreatedByBoardGroups(issues, MEMBERS);
    expect(withUnknown.map((g) => g.title)).toEqual(["Alice", "Unknown"]);
    const unknown = withUnknown.find(
      (g) => g.columnKey === CREATEDBY_UNKNOWN_COLUMN_KEY,
    )!;
    expect(unknown.droppableId).toBe(`${CREATEDBY_COLUMN_PREFIX}unknown`);
    expect(unknown.creatorId).toBeNull();
    expect(unknown.issues.map((i) => i.id)).toEqual(["1"]);

    const noUnknown = buildCreatedByBoardGroups(
      [mkIssue("2", "u-alice")],
      MEMBERS,
    );
    expect(noUnknown.map((g) => g.title)).toEqual(["Alice"]);
    expect(
      noUnknown.some((g) => g.columnKey === CREATEDBY_UNKNOWN_COLUMN_KEY),
    ).toBe(false);
  });

  it("all unresolved creators collapse into ONE Unknown column, last even when first-appearing", () => {
    const issues = [
      mkIssue("1", "u-gone"),
      mkIssue("2", "u-alice"),
      mkIssue("3", "u-also-gone"),
    ];
    const groups = buildCreatedByBoardGroups(issues, MEMBERS);
    expect(groups.map((g) => g.title)).toEqual(["Alice", "Unknown"]);
    expect(groups[1].issues.map((i) => i.id)).toEqual(["1", "3"]);
  });

  it("NEVER renders a raw user id as a column label", () => {
    const issues = [
      mkIssue("1", "u-alice"),
      mkIssue("2", "u-bob"),
      mkIssue("3", "u-gone"),
    ];
    const groups = buildCreatedByBoardGroups(issues, MEMBERS);
    for (const g of groups) {
      expect(["u-alice", "u-bob", "u-gone"]).not.toContain(g.title);
    }
    expect(groups.map((g) => g.title)).toEqual([
      "Alice",
      "bob@x.com",
      "Unknown",
    ]);
  });

  it("rows with an empty created_by group as Unknown, never crash", () => {
    const groups = buildCreatedByBoardGroups([mkIssue("1", "")], MEMBERS);
    expect(groups.map((g) => g.title)).toEqual(["Unknown"]);
  });

  it("show-empty-groups synthesizes nothing: no creator column without a loaded issue", () => {
    expect(buildCreatedByBoardGroups([], MEMBERS)).toEqual([]);
  });
});

describe("createdByDropColumnKey", () => {
  it("resolves column droppables and compound card ids", () => {
    expect(createdByDropColumnKey(`${CREATEDBY_COLUMN_PREFIX}u-alice`)).toBe(
      "u-alice",
    );
    expect(
      createdByDropColumnKey(`${CREATEDBY_COLUMN_PREFIX}unknown`),
    ).toBe("unknown");
    expect(
      createdByDropColumnKey(createdByCardDragId("u-alice", "issue-1")),
    ).toBe("u-alice");
    expect(
      createdByDropColumnKey(
        createdByCardDragId(CREATEDBY_UNKNOWN_COLUMN_KEY, "issue-1"),
      ),
    ).toBe("unknown");
  });

  it("card-prefix check precedes column-prefix check (card ids start with createdby: too)", () => {
    expect(createdByDropColumnKey("createdby-card:u-alice:issue-1")).toBe(
      "u-alice",
    );
  });

  it("returns null for non-created-by drop targets", () => {
    expect(createdByDropColumnKey("state-uuid")).toBeNull();
    expect(createdByDropColumnKey("priority:3")).toBeNull();
    expect(createdByDropColumnKey("epic:epic-a")).toBeNull();
    expect(createdByDropColumnKey("assignee:u-alice")).toBeNull();
    expect(createdByDropColumnKey("label:l-bug")).toBeNull();
    expect(createdByDropColumnKey("module:m-auth")).toBeNull();
    expect(createdByDropColumnKey("cycle:c-sprint-1")).toBeNull();
    expect(createdByDropColumnKey("release:r-v1")).toBeNull();
    expect(createdByDropColumnKey("issue-1")).toBeNull();
  });
});

describe("createdByDragIssueId / createdByDragColumnKey", () => {
  it("round-trips compound card ids: issue id via last colon, column key via prefix", () => {
    const id = createdByCardDragId("u-alice", "issue-1");
    expect(id.startsWith(CREATEDBY_CARD_PREFIX)).toBe(true);
    expect(createdByDragIssueId(id)).toBe("issue-1");
    expect(createdByDragColumnKey(id)).toBe("u-alice");
  });

  it("is identity / null for plain (state/priority mode) ids", () => {
    expect(createdByDragIssueId("issue-1")).toBe("issue-1");
    expect(createdByDragColumnKey("issue-1")).toBeNull();
    expect(createdByDragColumnKey("release-card:r-v1:issue-1")).toBeNull();
  });
});

describe("planCreatedByDrop", () => {
  it("cross-column drop is a deliberate no-op: created_by is immutable, no move exists", () => {
    const plan = planCreatedByDrop("u-bob", mkIssue("1", "u-alice"));
    expect(plan).toEqual({ kind: "noop", reason: "immutable" });
  });

  it("drop into Unknown is a deliberate no-op (there is no un-create operation)", () => {
    const plan = planCreatedByDrop(
      CREATEDBY_UNKNOWN_COLUMN_KEY,
      mkIssue("1", "u-alice"),
    );
    expect(plan).toEqual({ kind: "noop", reason: "immutable" });
  });

  it("drop onto the issue's own creator column is a no-op (already-member, never a move)", () => {
    const plan = planCreatedByDrop("u-alice", mkIssue("1", "u-alice"));
    expect(plan).toEqual({ kind: "noop", reason: "already-member" });
  });

  it("drop plans NEVER name a move: no move kind, no target creator, no state field anywhere", () => {
    for (const target of ["u-bob", CREATEDBY_UNKNOWN_COLUMN_KEY, "u-alice"]) {
      const plan = planCreatedByDrop(target, mkIssue("1", "u-alice"));
      expect(plan.kind).toBe("noop");
      expect("creatorId" in plan).toBe(false);
      expect("stateId" in plan).toBe(false);
      expect("state_id" in plan).toBe(false);
      expect(Object.keys(plan).sort()).toEqual(["kind", "reason"]);
    }
  });
});
