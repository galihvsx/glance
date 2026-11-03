// C135T2: unit tests for the board's Group-by-State-groups pure helpers
// (boardStateGroup.ts), mirroring boardCreatedBy.test.ts. Covers the
// plan's rulings: six group columns in canonical backend-fixed order,
// single-appearance, Unknown-last/hidden-when-empty, "Show empty
// groups" synthesizes all six canonical columns (closed set) while
// Unknown still renders only when it has rows, labels from the
// canonical set (NEVER a raw state id), compound id parse/round-trip,
// quick-add default state = first-by-sequence state in the group
// (undefined for Unknown and for stateless groups), and the drop-plan
// matrix — cross-column drops are ALWAYS a deliberate no-op (a group
// holds several states, so no single target state exists; there is no
// move pipeline to test and no executor). Within-column reorder is
// covered by the applyStateGroupReorder pipeline mirroring (same stack
// math as the created-by path), not by a simulated drag.

import { describe, expect, it } from "vitest";
import {
  STATEGROUP_CARD_PREFIX,
  STATEGROUP_COLUMN_PREFIX,
  STATEGROUP_UNKNOWN_COLUMN_KEY,
  buildStateGroupBoardGroups,
  planStateGroupDrop,
  stateGroupCardDragId,
  stateGroupDragColumnKey,
  stateGroupDragIssueId,
  stateGroupDropColumnKey,
} from "./boardStateGroup";
import type { Issue, IssueState } from "../../lib/types";

function mkState(
  id: string,
  group: string,
  sequence: number,
): IssueState {
  return {
    id,
    project_id: "proj-1",
    name: id,
    group,
    color: "#888",
    sequence,
    created_at: "",
    updated_at: "",
  };
}

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
    epics: [],
    project_structure: { release: null, modules: [] },
  } as Issue;
}

// Sequence-sorted, as Board.tsx's `states` memo is (the builder's
// first-by-sequence rule reads array order, so the fixture must honor
// the contract). The unstarted group has TWO states so the
// first-by-sequence rule is observable: s-todo-early (2) precedes
// s-todo-late (3).
const STATES = [
  mkState("s-triage", "triage", 1),
  mkState("s-todo-early", "unstarted", 2),
  mkState("s-todo-late", "unstarted", 3),
  mkState("s-done", "completed", 4),
];

const STATE_GROUP_BY_ID = new Map(
  STATES.map((s) => [s.id, s.group] as const),
);

describe("buildStateGroupBoardGroups", () => {
  it("builds columns in canonical backend-fixed order, not first-appearance order, with NO color field", () => {
    const issues = [
      mkIssue("1", "s-done"),
      mkIssue("2", "s-todo-late"),
      mkIssue("3", "s-triage"),
    ];
    const groups = buildStateGroupBoardGroups(
      issues,
      STATE_GROUP_BY_ID,
      STATES,
    );
    expect(groups.map((g) => g.title)).toEqual([
      "Triage",
      "Unstarted",
      "Completed",
    ]);
    expect(groups[0].droppableId).toBe(`${STATEGROUP_COLUMN_PREFIX}triage`);
    expect(groups[0].columnKey).toBe("triage");
    expect(groups[0].stateGroup).toBe("triage");
    expect("color" in groups[0]).toBe(false);
    expect(groups[1].issues.map((i) => i.id)).toEqual(["2"]);
    expect(groups[2].issues.map((i) => i.id)).toEqual(["1"]);
  });

  it("single-appearance: an issue appears in exactly ONE group column", () => {
    const issues = [mkIssue("1", "s-todo-early"), mkIssue("2", "s-done")];
    const groups = buildStateGroupBoardGroups(
      issues,
      STATE_GROUP_BY_ID,
      STATES,
    );
    expect(groups).toHaveLength(2);
    expect(groups.flatMap((g) => g.issues.map((i) => i.id))).toEqual([
      "1",
      "2",
    ]);
  });

  it("Unknown is always last and hidden when empty", () => {
    const issues = [mkIssue("1", "s-gone"), mkIssue("2", "s-todo-early")];
    const withUnknown = buildStateGroupBoardGroups(
      issues,
      STATE_GROUP_BY_ID,
      STATES,
    );
    expect(withUnknown.map((g) => g.title)).toEqual([
      "Unstarted",
      "Unknown",
    ]);
    const unknown = withUnknown.find(
      (g) => g.columnKey === STATEGROUP_UNKNOWN_COLUMN_KEY,
    )!;
    expect(unknown.droppableId).toBe(
      `${STATEGROUP_COLUMN_PREFIX}unknown`,
    );
    expect(unknown.stateGroup).toBeNull();
    expect(unknown.issues.map((i) => i.id)).toEqual(["1"]);

    const noUnknown = buildStateGroupBoardGroups(
      [mkIssue("2", "s-todo-early")],
      STATE_GROUP_BY_ID,
      STATES,
    );
    expect(noUnknown.map((g) => g.title)).toEqual(["Unstarted"]);
    expect(
      noUnknown.some((g) => g.columnKey === STATEGROUP_UNKNOWN_COLUMN_KEY),
    ).toBe(false);
  });

  it("all unresolved states collapse into ONE Unknown column, last even when first-appearing", () => {
    const issues = [
      mkIssue("1", "s-gone"),
      mkIssue("2", "s-todo-early"),
      mkIssue("3", "s-also-gone"),
    ];
    const groups = buildStateGroupBoardGroups(
      issues,
      STATE_GROUP_BY_ID,
      STATES,
    );
    expect(groups.map((g) => g.title)).toEqual(["Unstarted", "Unknown"]);
    expect(groups[1].issues.map((i) => i.id)).toEqual(["1", "3"]);
  });

  it("NEVER renders a raw state id as a column label", () => {
    const issues = [
      mkIssue("1", "s-todo-early"),
      mkIssue("2", "s-done"),
      mkIssue("3", "s-gone"),
    ];
    const groups = buildStateGroupBoardGroups(
      issues,
      STATE_GROUP_BY_ID,
      STATES,
    );
    for (const g of groups) {
      expect(["s-todo-early", "s-done", "s-gone"]).not.toContain(g.title);
    }
    expect(groups.map((g) => g.title)).toEqual([
      "Unstarted",
      "Completed",
      "Unknown",
    ]);
  });

  it("rows with an empty state_id group as Unknown, never crash", () => {
    const groups = buildStateGroupBoardGroups(
      [mkIssue("1", "")],
      STATE_GROUP_BY_ID,
      STATES,
    );
    expect(groups.map((g) => g.title)).toEqual(["Unknown"]);
  });

  it("show-empty-groups synthesizes all six canonical columns; Unknown still only when it has rows", () => {
    const groups = buildStateGroupBoardGroups(
      [mkIssue("1", "s-todo-early")],
      STATE_GROUP_BY_ID,
      STATES,
      { includeEmptyGroups: true },
    );
    expect(groups.map((g) => g.title)).toEqual([
      "Triage",
      "Backlog",
      "Unstarted",
      "Started",
      "Completed",
      "Cancelled",
    ]);
    expect(groups.find((g) => g.title === "Unstarted")!.issues).toHaveLength(
      1,
    );
    expect(groups.find((g) => g.title === "Triage")!.issues).toHaveLength(0);
    expect(groups.some((g) => g.title === "Unknown")).toBe(false);

    const withUnknown = buildStateGroupBoardGroups(
      [mkIssue("1", "s-gone")],
      STATE_GROUP_BY_ID,
      STATES,
      { includeEmptyGroups: true },
    );
    expect(withUnknown.map((g) => g.title)).toEqual([
      "Triage",
      "Backlog",
      "Unstarted",
      "Started",
      "Completed",
      "Cancelled",
      "Unknown",
    ]);
  });

  it("show-empty-groups off synthesizes nothing: no column without a loaded issue", () => {
    expect(
      buildStateGroupBoardGroups([], STATE_GROUP_BY_ID, STATES),
    ).toEqual([]);
    expect(
      buildStateGroupBoardGroups([], STATE_GROUP_BY_ID, STATES, {
        includeEmptyGroups: false,
      }),
    ).toEqual([]);
  });

  it("quick-add default state is the first-by-sequence state in the group", () => {
    const groups = buildStateGroupBoardGroups(
      [mkIssue("1", "s-todo-late")],
      STATE_GROUP_BY_ID,
      STATES,
    );
    // The issue sits in s-todo-late, but the column's quick-add
    // defaults to s-todo-early (sequence 2 < 3) — the new card stays
    // in the column either way, and the stamp is sequence-first, not
    // the issue's own state.
    expect(groups[0].newStateId).toBe("s-todo-early");
    const completed = buildStateGroupBoardGroups(
      [mkIssue("2", "s-done")],
      STATE_GROUP_BY_ID,
      STATES,
    );
    expect(completed[0].newStateId).toBe("s-done");
  });

  it("quick-add default is undefined for the Unknown column and for groups with no states at all", () => {
    const groups = buildStateGroupBoardGroups(
      [mkIssue("1", "s-gone"), mkIssue("2", "s-todo-early")],
      STATE_GROUP_BY_ID,
      STATES,
      { includeEmptyGroups: true },
    );
    const unknown = groups.find(
      (g) => g.columnKey === STATEGROUP_UNKNOWN_COLUMN_KEY,
    )!;
    expect(unknown.newStateId).toBeUndefined();
    // Backlog has no state in this fixture: synthesized empty column,
    // no state to default to, so quick-add is disabled (never
    // fabricated).
    const backlog = groups.find((g) => g.columnKey === "backlog")!;
    expect(backlog.issues).toHaveLength(0);
    expect(backlog.newStateId).toBeUndefined();
  });
});

describe("stateGroupDropColumnKey", () => {
  it("resolves column droppables and compound card ids", () => {
    expect(
      stateGroupDropColumnKey(`${STATEGROUP_COLUMN_PREFIX}unstarted`),
    ).toBe("unstarted");
    expect(
      stateGroupDropColumnKey(`${STATEGROUP_COLUMN_PREFIX}unknown`),
    ).toBe("unknown");
    expect(
      stateGroupDropColumnKey(
        stateGroupCardDragId("unstarted", "issue-1"),
      ),
    ).toBe("unstarted");
    expect(
      stateGroupDropColumnKey(
        stateGroupCardDragId(STATEGROUP_UNKNOWN_COLUMN_KEY, "issue-1"),
      ),
    ).toBe("unknown");
  });

  it("card-prefix check precedes column-prefix check (card ids start with stategroup: too)", () => {
    expect(
      stateGroupDropColumnKey("stategroup-card:started:issue-1"),
    ).toBe("started");
  });

  it("returns null for non-state-group drop targets", () => {
    expect(stateGroupDropColumnKey("state-uuid")).toBeNull();
    expect(stateGroupDropColumnKey("priority:3")).toBeNull();
    expect(stateGroupDropColumnKey("epic:epic-a")).toBeNull();
    expect(stateGroupDropColumnKey("assignee:u-alice")).toBeNull();
    expect(stateGroupDropColumnKey("label:l-bug")).toBeNull();
    expect(stateGroupDropColumnKey("module:m-auth")).toBeNull();
    expect(stateGroupDropColumnKey("cycle:c-sprint-1")).toBeNull();
    expect(stateGroupDropColumnKey("release:r-v1")).toBeNull();
    expect(stateGroupDropColumnKey("createdby:u-alice")).toBeNull();
    expect(stateGroupDropColumnKey("issue-1")).toBeNull();
  });
});

describe("stateGroupDragIssueId / stateGroupDragColumnKey", () => {
  it("round-trips compound card ids: issue id via last colon, column key via prefix", () => {
    const id = stateGroupCardDragId("unstarted", "issue-1");
    expect(id.startsWith(STATEGROUP_CARD_PREFIX)).toBe(true);
    expect(stateGroupDragIssueId(id)).toBe("issue-1");
    expect(stateGroupDragColumnKey(id)).toBe("unstarted");
  });

  it("is identity / null for plain (state/priority mode) ids", () => {
    expect(stateGroupDragIssueId("issue-1")).toBe("issue-1");
    expect(stateGroupDragColumnKey("issue-1")).toBeNull();
    expect(stateGroupDragColumnKey("createdby-card:u-alice:issue-1")).toBeNull();
  });
});

describe("planStateGroupDrop", () => {
  it("cross-column drop is a deliberate no-op: a group holds several states, so no single target state exists", () => {
    const plan = planStateGroupDrop(
      "completed",
      mkIssue("1", "s-todo-early"),
      STATE_GROUP_BY_ID,
    );
    expect(plan).toEqual({ kind: "noop", reason: "ambiguous-target" });
  });

  it("drop into Unknown is a deliberate no-op (there is no un-state operation)", () => {
    const plan = planStateGroupDrop(
      STATEGROUP_UNKNOWN_COLUMN_KEY,
      mkIssue("1", "s-todo-early"),
      STATE_GROUP_BY_ID,
    );
    expect(plan).toEqual({ kind: "noop", reason: "ambiguous-target" });
  });

  it("drop onto the issue's own group column is a no-op (already-member, never a move)", () => {
    const plan = planStateGroupDrop(
      "unstarted",
      mkIssue("1", "s-todo-late"),
      STATE_GROUP_BY_ID,
    );
    expect(plan).toEqual({ kind: "noop", reason: "already-member" });
  });

  it("drop of an Unknown-column issue onto Unknown is a no-op (already-member)", () => {
    const plan = planStateGroupDrop(
      STATEGROUP_UNKNOWN_COLUMN_KEY,
      mkIssue("1", "s-gone"),
      STATE_GROUP_BY_ID,
    );
    expect(plan).toEqual({ kind: "noop", reason: "already-member" });
  });

  it("drop plans NEVER name a move: no move kind, no target state, no state field anywhere", () => {
    for (const target of [
      "completed",
      STATEGROUP_UNKNOWN_COLUMN_KEY,
      "unstarted",
    ]) {
      const plan = planStateGroupDrop(
        target,
        mkIssue("1", "s-todo-early"),
        STATE_GROUP_BY_ID,
      );
      expect(plan.kind).toBe("noop");
      expect("stateId" in plan).toBe(false);
      expect("state_id" in plan).toBe(false);
      expect("targetStateId" in plan).toBe(false);
      expect(Object.keys(plan).sort()).toEqual(["kind", "reason"]);
    }
  });
});
