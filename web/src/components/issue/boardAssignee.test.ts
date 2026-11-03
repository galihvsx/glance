// C129T1: unit tests for the board's Group-by-Assignees pure helpers
// (boardAssignee.ts), mirroring boardEpic.test.ts. Covers the plan's
// rulings: column grouping, multi-appearance, Ungrouped-last/
// hidden-when-empty, compound id parse/round-trip, the drop-plan noop
// matrix, quick-add→attach, and expand-to-full-form carry.
import { describe, expect, it } from "vitest";
import {
  ASSIGNEE_CARD_PREFIX,
  ASSIGNEE_COLUMN_PREFIX,
  ASSIGNEE_UNGROUPED_COLUMN_KEY,
  assigneeCardDragId,
  assigneeDragColumnKey,
  assigneeDragIssueId,
  assigneeDropColumnKey,
  buildAssigneeBoardGroups,
  planAssigneeDrop,
  planAssigneeQuickAdd,
  planBoardQuickExpand,
} from "./boardAssignee";
import type { Issue } from "../../lib/types";

function mkIssue(
  id: string,
  assignees: { id: string; name: string }[],
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
    assignees,
    labels: [],
    epics: [],
  } as Issue;
}

const ALICE = { id: "u-alice", name: "Alice" };
const BOB = { id: "u-bob", name: "Bob" };

describe("buildAssigneeBoardGroups", () => {
  it("builds one column per member, first-appearance order, with the state stamp", () => {
    const issues = [
      mkIssue("1", [ALICE]),
      mkIssue("2", [BOB]),
      mkIssue("3", [ALICE]),
    ];
    const groups = buildAssigneeBoardGroups(issues, "s-backlog");
    expect(groups.map((g) => g.title)).toEqual(["Alice", "Bob"]);
    expect(groups[0].droppableId).toBe(`${ASSIGNEE_COLUMN_PREFIX}u-alice`);
    expect(groups[0].columnKey).toBe("u-alice");
    expect(groups[0].assigneeId).toBe("u-alice");
    expect(groups[0].issues.map((i) => i.id)).toEqual(["1", "3"]);
    expect(groups[0].newStateId).toBe("s-backlog");
  });

  it("multi-appearance: an issue with N assignees appears in EVERY assignee column", () => {
    const issues = [mkIssue("1", [ALICE, BOB])];
    const groups = buildAssigneeBoardGroups(issues);
    expect(groups).toHaveLength(2);
    expect(groups[0].issues.map((i) => i.id)).toEqual(["1"]);
    expect(groups[1].issues.map((i) => i.id)).toEqual(["1"]);
  });

  it("Ungrouped is always last and hidden when empty", () => {
    const issues = [mkIssue("1", []), mkIssue("2", [ALICE])];
    const withUngrouped = buildAssigneeBoardGroups(issues);
    expect(withUngrouped.map((g) => g.title)).toEqual(["Alice", "Ungrouped"]);
    const ungrouped = withUngrouped.find(
      (g) => g.columnKey === ASSIGNEE_UNGROUPED_COLUMN_KEY,
    )!;
    expect(ungrouped.droppableId).toBe(
      `${ASSIGNEE_COLUMN_PREFIX}ungrouped`,
    );
    expect(ungrouped.assigneeId).toBeNull();
    expect(ungrouped.issues.map((i) => i.id)).toEqual(["1"]);

    const noUngrouped = buildAssigneeBoardGroups([mkIssue("2", [ALICE])]);
    expect(noUngrouped.map((g) => g.title)).toEqual(["Alice"]);
    expect(
      noUngrouped.some(
        (g) => g.columnKey === ASSIGNEE_UNGROUPED_COLUMN_KEY,
      ),
    ).toBe(false);
  });

  it("show-empty-groups synthesizes nothing: no member column without a loaded issue", () => {
    // A member with no loaded issue simply has no column — the builder
    // takes only what the issue rows carry.
    expect(buildAssigneeBoardGroups([])).toEqual([]);
  });
});

describe("assigneeDropColumnKey", () => {
  it("resolves column droppables and compound card ids", () => {
    expect(assigneeDropColumnKey(`${ASSIGNEE_COLUMN_PREFIX}u-alice`)).toBe(
      "u-alice",
    );
    expect(assigneeDropColumnKey(`${ASSIGNEE_COLUMN_PREFIX}ungrouped`)).toBe(
      "ungrouped",
    );
    expect(
      assigneeDropColumnKey(assigneeCardDragId("u-alice", "issue-1")),
    ).toBe("u-alice");
    expect(
      assigneeDropColumnKey(
        assigneeCardDragId(ASSIGNEE_UNGROUPED_COLUMN_KEY, "issue-1"),
      ),
    ).toBe("ungrouped");
  });

  it("returns null for non-assignee drop targets", () => {
    expect(assigneeDropColumnKey("state-uuid")).toBeNull();
    expect(assigneeDropColumnKey("priority:3")).toBeNull();
    expect(assigneeDropColumnKey("epic:epic-a")).toBeNull();
    expect(assigneeDropColumnKey("issue-1")).toBeNull();
  });
});

describe("assigneeDragIssueId / assigneeDragColumnKey", () => {
  it("round-trips compound card ids: issue id via last colon, column key via prefix", () => {
    const id = assigneeCardDragId("u-alice", "issue-1");
    expect(id.startsWith(ASSIGNEE_CARD_PREFIX)).toBe(true);
    expect(assigneeDragIssueId(id)).toBe("issue-1");
    expect(assigneeDragColumnKey(id)).toBe("u-alice");
  });

  it("is identity / null for plain (state/priority mode) ids", () => {
    expect(assigneeDragIssueId("issue-1")).toBe("issue-1");
    expect(assigneeDragColumnKey("issue-1")).toBeNull();
    expect(assigneeDragColumnKey("epic-card:epic-a:issue-1")).toBeNull();
  });
});

describe("planAssigneeDrop", () => {
  it("drop into a member column plans an attach", () => {
    const plan = planAssigneeDrop("u-alice", mkIssue("1", [BOB]));
    expect(plan).toEqual({ kind: "attach", memberId: "u-alice" });
  });

  it("drop into Ungrouped is a deliberate no-op (detach-from-all is destructive)", () => {
    const plan = planAssigneeDrop(
      ASSIGNEE_UNGROUPED_COLUMN_KEY,
      mkIssue("1", [ALICE]),
    );
    expect(plan).toEqual({ kind: "noop", reason: "ungrouped" });
  });

  it("cross-column drop onto an already-assigned member is a no-op (idempotent)", () => {
    const plan = planAssigneeDrop("u-alice", mkIssue("1", [ALICE, BOB]));
    expect(plan).toEqual({ kind: "noop", reason: "already-assigned" });
  });

  it("multi-assignee aware: dropping a multi-assignee issue into a NEW member attaches without detaching", () => {
    const plan = planAssigneeDrop("u-bob", mkIssue("1", [ALICE]));
    expect(plan).toEqual({ kind: "attach", memberId: "u-bob" });
  });

  it("attach-only: the plan never names a detach — no other membership is touched", () => {
    const plan = planAssigneeDrop("u-bob", mkIssue("1", [ALICE]));
    expect(plan.kind).toBe("attach");
    if (plan.kind === "attach") {
      expect(Object.keys(plan).sort()).toEqual(["kind", "memberId"]);
    }
  });
});

describe("planAssigneeQuickAdd", () => {
  it("member column quick-add creates AND attaches to the member", () => {
    const plan = planAssigneeQuickAdd({
      newStateId: "s-backlog",
      assigneeId: "u-alice",
    });
    expect(plan).toEqual({ stateId: "s-backlog", attachAssigneeId: "u-alice" });
  });

  it("Ungrouped quick-add is a plain create (no attach)", () => {
    const plan = planAssigneeQuickAdd({
      newStateId: "s-backlog",
      assigneeId: null,
    });
    expect(plan).toEqual({ stateId: "s-backlog", attachAssigneeId: null });
  });

  it("state/priority columns are unaffected (no attach)", () => {
    const plan = planAssigneeQuickAdd({ newStateId: "s-1" });
    expect(plan).toEqual({ stateId: "s-1", attachAssigneeId: null });
  });
});

describe("planBoardQuickExpand", () => {
  it("member column expand carries title + state + member", () => {
    expect(
      planBoardQuickExpand(
        { newStateId: "s-backlog", assigneeId: "u-alice" },
        "New thing",
      ),
    ).toEqual({
      newIssue: true,
      newIssueName: "New thing",
      newIssueStateId: "s-backlog",
      newIssueAssigneeId: "u-alice",
    });
  });

  it("Ungrouped column expand carries title + state only (no member)", () => {
    const state = planBoardQuickExpand(
      { newStateId: "s-backlog", assigneeId: null },
      "New thing",
    );
    expect(state.newIssueAssigneeId).toBeUndefined();
    expect(state.newIssueStateId).toBe("s-backlog");
  });

  it("priority columns keep their priority carry alongside the assignee rule", () => {
    expect(
      planBoardQuickExpand(
        { newStateId: "s-backlog", newPriority: 3 },
        "New thing",
      ),
    ).toEqual({
      newIssue: true,
      newIssueName: "New thing",
      newIssueStateId: "s-backlog",
      newIssuePriority: 3,
    });
  });
});
