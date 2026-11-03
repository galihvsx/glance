// C132T2: unit tests for the board's Group-by-Cycles pure helpers
// (boardCycle.ts), mirroring boardModule.test.ts. Covers the plan's
// rulings: column grouping, single-appearance, Ungrouped-last/
// hidden-when-empty, compound id parse/round-trip, the drop-plan noop
// matrix and move-plan shape, the move executor's add-before-remove
// order and failure honesty (add-failure aborts before remove,
// remove-failure surfaces after a successful add), quick-add→attach,
// and expand-to-full-form carry via T1's newIssueCycleId bridge.
// The one deliberate difference from modules: drops MOVE (add then
// remove), they do not attach-only — single-value display would
// misdisplay an attach-only card under the wrong cycle. Cycles have
// NO color, so no color field is asserted anywhere (and none is carried).

import { describe, expect, it, vi } from "vitest";
import {
  CYCLE_CARD_PREFIX,
  CYCLE_COLUMN_PREFIX,
  CYCLE_UNGROUPED_COLUMN_KEY,
  buildCycleBoardGroups,
  cycleCardDragId,
  cycleDragColumnKey,
  cycleDragIssueId,
  cycleDropColumnKey,
  executeCycleMove,
  planBoardCycleQuickExpand,
  planCycleDrop,
  planCycleQuickAdd,
} from "./boardCycle";
import type { Issue } from "../../lib/types";

function mkIssue(
  id: string,
  cycle: { id: string; name: string } | null,
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
    epics: [],
    project_structure: { cycle, modules: [] },
  } as Issue;
}

const SPRINT1 = { id: "c-sprint-1", name: "Sprint 1" };
const SPRINT2 = { id: "c-sprint-2", name: "Sprint 2" };

describe("buildCycleBoardGroups", () => {
  it("builds one column per cycle, first-appearance order, with the state stamp and NO color field", () => {
    const issues = [
      mkIssue("1", SPRINT1),
      mkIssue("2", SPRINT2),
      mkIssue("3", SPRINT1),
    ];
    const groups = buildCycleBoardGroups(issues, "s-backlog");
    expect(groups.map((g) => g.title)).toEqual(["Sprint 1", "Sprint 2"]);
    expect(groups[0].droppableId).toBe(
      `${CYCLE_COLUMN_PREFIX}c-sprint-1`,
    );
    expect(groups[0].columnKey).toBe("c-sprint-1");
    expect(groups[0].cycleId).toBe("c-sprint-1");
    expect("color" in groups[0]).toBe(false);
    expect(groups[0].issues.map((i) => i.id)).toEqual(["1", "3"]);
    expect(groups[0].newStateId).toBe("s-backlog");
  });

  it("single-appearance: an issue appears in exactly ONE cycle column", () => {
    const issues = [mkIssue("1", SPRINT1), mkIssue("2", SPRINT2)];
    const groups = buildCycleBoardGroups(issues);
    expect(groups).toHaveLength(2);
    expect(groups.flatMap((g) => g.issues.map((i) => i.id))).toEqual([
      "1",
      "2",
    ]);
  });

  it("Ungrouped is always last and hidden when empty", () => {
    const issues = [mkIssue("1", null), mkIssue("2", SPRINT1)];
    const withUngrouped = buildCycleBoardGroups(issues);
    expect(withUngrouped.map((g) => g.title)).toEqual([
      "Sprint 1",
      "Ungrouped",
    ]);
    const ungrouped = withUngrouped.find(
      (g) => g.columnKey === CYCLE_UNGROUPED_COLUMN_KEY,
    )!;
    expect(ungrouped.droppableId).toBe(`${CYCLE_COLUMN_PREFIX}ungrouped`);
    expect(ungrouped.cycleId).toBeNull();
    expect(ungrouped.issues.map((i) => i.id)).toEqual(["1"]);

    const noUngrouped = buildCycleBoardGroups([mkIssue("2", SPRINT1)]);
    expect(noUngrouped.map((g) => g.title)).toEqual(["Sprint 1"]);
    expect(
      noUngrouped.some((g) => g.columnKey === CYCLE_UNGROUPED_COLUMN_KEY),
    ).toBe(false);
  });

  it("rows without project_structure group as Ungrouped, never crash", () => {
    const bare = mkIssue("1", SPRINT1);
    delete (bare as Partial<Issue>).project_structure;
    const groups = buildCycleBoardGroups([bare]);
    expect(groups.map((g) => g.title)).toEqual(["Ungrouped"]);
  });

  it("show-empty-groups synthesizes nothing: no cycle column without a loaded issue", () => {
    expect(buildCycleBoardGroups([])).toEqual([]);
  });
});

describe("cycleDropColumnKey", () => {
  it("resolves column droppables and compound card ids", () => {
    expect(cycleDropColumnKey(`${CYCLE_COLUMN_PREFIX}c-sprint-1`)).toBe(
      "c-sprint-1",
    );
    expect(cycleDropColumnKey(`${CYCLE_COLUMN_PREFIX}ungrouped`)).toBe(
      "ungrouped",
    );
    expect(
      cycleDropColumnKey(cycleCardDragId("c-sprint-1", "issue-1")),
    ).toBe("c-sprint-1");
    expect(
      cycleDropColumnKey(
        cycleCardDragId(CYCLE_UNGROUPED_COLUMN_KEY, "issue-1"),
      ),
    ).toBe("ungrouped");
  });

  it("card-prefix check precedes column-prefix check (card ids start with cycle: too)", () => {
    // A card id is also a valid-looking column droppable prefix match
    // ("cycle:cycle-card:..." would mis-resolve without the precedence).
    expect(
      cycleDropColumnKey("cycle-card:c-sprint-1:issue-1"),
    ).toBe("c-sprint-1");
  });

  it("returns null for non-cycle drop targets", () => {
    expect(cycleDropColumnKey("state-uuid")).toBeNull();
    expect(cycleDropColumnKey("priority:3")).toBeNull();
    expect(cycleDropColumnKey("epic:epic-a")).toBeNull();
    expect(cycleDropColumnKey("assignee:u-alice")).toBeNull();
    expect(cycleDropColumnKey("label:l-bug")).toBeNull();
    expect(cycleDropColumnKey("module:m-auth")).toBeNull();
    expect(cycleDropColumnKey("issue-1")).toBeNull();
  });
});

describe("cycleDragIssueId / cycleDragColumnKey", () => {
  it("round-trips compound card ids: issue id via last colon, column key via prefix", () => {
    const id = cycleCardDragId("c-sprint-1", "issue-1");
    expect(id.startsWith(CYCLE_CARD_PREFIX)).toBe(true);
    expect(cycleDragIssueId(id)).toBe("issue-1");
    expect(cycleDragColumnKey(id)).toBe("c-sprint-1");
  });

  it("is identity / null for plain (state/priority mode) ids", () => {
    expect(cycleDragIssueId("issue-1")).toBe("issue-1");
    expect(cycleDragColumnKey("issue-1")).toBeNull();
    expect(cycleDragColumnKey("module-card:m-auth:issue-1")).toBeNull();
  });
});

describe("planCycleDrop", () => {
  it("drop into a different cycle column plans a MOVE with the source cycle", () => {
    const plan = planCycleDrop("c-sprint-2", mkIssue("1", SPRINT1));
    expect(plan).toEqual({
      kind: "move",
      cycleId: "c-sprint-2",
      sourceCycleId: "c-sprint-1",
    });
  });

  it("drop from Ungrouped plans a MOVE with a null source (add-only, nothing to remove)", () => {
    const plan = planCycleDrop("c-sprint-1", mkIssue("1", null));
    expect(plan).toEqual({
      kind: "move",
      cycleId: "c-sprint-1",
      sourceCycleId: null,
    });
  });

  it("drop into Ungrouped is a deliberate no-op (board removal is out of scope)", () => {
    const plan = planCycleDrop(
      CYCLE_UNGROUPED_COLUMN_KEY,
      mkIssue("1", SPRINT1),
    );
    expect(plan).toEqual({ kind: "noop", reason: "ungrouped" });
  });

  it("cross-column drop onto the already-member cycle is a no-op (idempotent)", () => {
    const plan = planCycleDrop("c-sprint-1", mkIssue("1", SPRINT1));
    expect(plan).toEqual({ kind: "noop", reason: "already-member" });
  });

  it("move, not attach: the plan names BOTH the target and the source to remove — never attach-only", () => {
    const plan = planCycleDrop("c-sprint-2", mkIssue("1", SPRINT1));
    expect(plan.kind).toBe("move");
    if (plan.kind === "move") {
      expect(Object.keys(plan).sort()).toEqual([
        "cycleId",
        "kind",
        "sourceCycleId",
      ]);
      expect(plan.sourceCycleId).toBe("c-sprint-1");
    }
  });

  it("move plans never change state: no state field anywhere on the plan", () => {
    const plan = planCycleDrop("c-sprint-2", mkIssue("1", SPRINT1));
    expect("stateId" in plan).toBe(false);
    expect("state_id" in plan).toBe(false);
  });
});

describe("executeCycleMove", () => {
  it("MOVE calls POST then DELETE, in order, with {issue_ids} bodies", async () => {
    const order: string[] = [];
    const deps = {
      addToCycle: vi.fn(async () => {
        order.push("add");
      }),
      removeFromCycle: vi.fn(async () => {
        order.push("remove");
      }),
    };
    const result = await executeCycleMove(
      deps,
      { kind: "move", cycleId: "c-sprint-2", sourceCycleId: "c-sprint-1" },
      "issue-1",
    );
    expect(result).toEqual({ ok: true });
    expect(order).toEqual(["add", "remove"]);
    expect(deps.addToCycle).toHaveBeenCalledWith("c-sprint-2", "issue-1");
    expect(deps.removeFromCycle).toHaveBeenCalledWith(
      "c-sprint-1",
      "issue-1",
    );
  });

  it("Ungrouped source is add-only: no DELETE call", async () => {
    const deps = {
      addToCycle: vi.fn(async () => {}),
      removeFromCycle: vi.fn(async () => {}),
    };
    const result = await executeCycleMove(
      deps,
      { kind: "move", cycleId: "c-sprint-1", sourceCycleId: null },
      "issue-1",
    );
    expect(result).toEqual({ ok: true });
    expect(deps.removeFromCycle).not.toHaveBeenCalled();
  });

  it("add failure aborts BEFORE remove (issue untouched)", async () => {
    const addError = new Error("add failed");
    const deps = {
      addToCycle: vi.fn(async () => {
        throw addError;
      }),
      removeFromCycle: vi.fn(async () => {}),
    };
    const result = await executeCycleMove(
      deps,
      { kind: "move", cycleId: "c-sprint-2", sourceCycleId: "c-sprint-1" },
      "issue-1",
    );
    expect(result).toEqual({ ok: false, stage: "add", error: addError });
    expect(deps.removeFromCycle).not.toHaveBeenCalled();
  });

  it("remove failure after a successful add surfaces honestly as stage remove", async () => {
    const removeError = new Error("remove failed");
    const deps = {
      addToCycle: vi.fn(async () => {}),
      removeFromCycle: vi.fn(async () => {
        throw removeError;
      }),
    };
    const result = await executeCycleMove(
      deps,
      { kind: "move", cycleId: "c-sprint-2", sourceCycleId: "c-sprint-1" },
      "issue-1",
    );
    expect(result).toEqual({
      ok: false,
      stage: "remove",
      error: removeError,
    });
    expect(deps.addToCycle).toHaveBeenCalledTimes(1);
    expect(deps.removeFromCycle).toHaveBeenCalledTimes(1);
  });
});

describe("planCycleQuickAdd", () => {
  it("cycle column quick-add creates AND attaches to the cycle", () => {
    const plan = planCycleQuickAdd({
      newStateId: "s-backlog",
      cycleId: "c-sprint-1",
    });
    expect(plan).toEqual({
      stateId: "s-backlog",
      attachCycleId: "c-sprint-1",
    });
  });

  it("Ungrouped quick-add is a plain create (no attach)", () => {
    const plan = planCycleQuickAdd({
      newStateId: "s-backlog",
      cycleId: null,
    });
    expect(plan).toEqual({ stateId: "s-backlog", attachCycleId: null });
  });
});

describe("planBoardCycleQuickExpand", () => {
  it("cycle column expand carries title + state + cycle via T1's newIssueCycleId bridge", () => {
    expect(
      planBoardCycleQuickExpand(
        { newStateId: "s-backlog", cycleId: "c-sprint-1" },
        "New thing",
      ),
    ).toEqual({
      newIssue: true,
      newIssueName: "New thing",
      newIssueStateId: "s-backlog",
      newIssueCycleId: "c-sprint-1",
    });
  });

  it("Ungrouped column expand carries title + state only (no cycle)", () => {
    const state = planBoardCycleQuickExpand(
      { newStateId: "s-backlog", cycleId: null },
      "New thing",
    );
    expect(state.newIssueCycleId).toBeUndefined();
    expect(state.newIssueStateId).toBe("s-backlog");
  });

  it("priority carry still passes through alongside the cycle rule", () => {
    expect(
      planBoardCycleQuickExpand(
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
