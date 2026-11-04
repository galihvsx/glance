// C142T1: board-nested drag rules — stack-scoped ids (one parse
// site), terminal dispatch resolution, the C141 drop pipeline on the
// board's flat Issue[] cache, and the nested overlay resolution.
// The live browser drag itself is NOT proven here (mocked tests
// cannot prove a dnd-kit drop landing); the page-level grip gating
// is covered by BoardNestedDrag.test.tsx, and the live browser drag
// remains the final verifier.

import { describe, expect, it, vi } from "vitest";
import type { Issue, SubtaskRow } from "../../lib/types";
import {
  applyBoardNestedDrop,
  boardNestedSortableId,
  isBoardNestedDragEnabled,
  parseBoardNestedId,
  reorderBoardNestedInIssues,
  resolveBoardNestedDrop,
  resolveBoardNestedOverlay,
  type BoardNestedDropDeps,
} from "./boardNestedReorder";

function row(
  id: string,
  sortOrder: number,
  stateId: string | null = "s-todo",
): SubtaskRow {
  return {
    id,
    display_id: id.toUpperCase(),
    title: `Row ${id}`,
    state_id: stateId,
    priority: null,
    assignee_ids: [],
    sort_order: sortOrder,
  };
}

function makeIssue(id: string, subtasks: SubtaskRow[] = []): Issue {
  return {
    id,
    project_id: "p-eng",
    sequence_id: 1,
    display_id: id.toUpperCase(),
    name: `Issue ${id}`,
    priority: 3,
    state_id: "s-todo",
    sort_order: 1024,
    is_draft: false,
    created_by: "u-1",
    created_at: "",
    updated_at: "",
    assignees: [],
    labels: [],
    subtasks,
  };
}

function deps(overrides: Partial<BoardNestedDropDeps> = {}): {
  d: BoardNestedDropDeps;
  patch: ReturnType<typeof vi.fn>;
  rebalance: ReturnType<typeof vi.fn>;
  refetchStack: ReturnType<typeof vi.fn>;
  invalidate: ReturnType<typeof vi.fn>;
  onError: ReturnType<typeof vi.fn>;
  setData: ReturnType<typeof vi.fn>;
} {
  const patch = vi.fn().mockResolvedValue(undefined);
  const rebalance = vi.fn().mockResolvedValue(undefined);
  const refetchStack = vi.fn().mockResolvedValue([]);
  const invalidate = vi.fn().mockResolvedValue(undefined);
  const onError = vi.fn();
  const setData = vi.fn();
  const d: BoardNestedDropDeps = {
    parentId: "p-1",
    getData: () => undefined,
    setData,
    patch,
    rebalance,
    refetchStack,
    invalidate,
    onError,
    ...overrides,
  };
  // Return the spies actually wired into d (overrides replace defaults).
  return {
    d,
    patch: d.patch as ReturnType<typeof vi.fn>,
    rebalance: d.rebalance as ReturnType<typeof vi.fn>,
    refetchStack: d.refetchStack as ReturnType<typeof vi.fn>,
    invalidate: d.invalidate as ReturnType<typeof vi.fn>,
    onError: d.onError as ReturnType<typeof vi.fn>,
    setData: d.setData as ReturnType<typeof vi.fn>,
  };
}

describe("boardNestedSortableId / parseBoardNestedId", () => {
  it("round-trips a plain-mode appearance key", () => {
    const id = boardNestedSortableId("p-1", "p-1", "c-1");
    expect(parseBoardNestedId(id)).toEqual({
      appearanceKey: "p-1",
      parentId: "p-1",
      childId: "c-1",
    });
  });

  it("round-trips compound appearance keys that contain colons", () => {
    // Epic-mode card id and sub-group-section card id shapes.
    for (const appearance of [
      "epic-card:e-9:p-1",
      "subgroup-card:epic::e-9:p-1",
      "assignee-card:u-7:p-1",
    ]) {
      const id = boardNestedSortableId(appearance, "p-1", "c-1");
      expect(parseBoardNestedId(id)).toEqual({
        appearanceKey: appearance,
        parentId: "p-1",
        childId: "c-1",
      });
    }
  });

  it("rejects plain issue ids, card compound ids, and malformed ids", () => {
    expect(parseBoardNestedId("c-1")).toBeNull();
    expect(parseBoardNestedId("epic-card:e-9:p-1")).toBeNull();
    expect(parseBoardNestedId("state:s-todo")).toBeNull();
    expect(parseBoardNestedId("board-nested:p-1")).toBeNull();
    expect(parseBoardNestedId("board-nested::p-1:c-1")).toBeNull();
    expect(parseBoardNestedId("board-nested:p-1:c-1")).toBeNull();
    expect(parseBoardNestedId("")).toBeNull();
  });
});

describe("resolveBoardNestedDrop", () => {
  const stackKey = "p-1";
  it("resolves a same-appearance sibling drop", () => {
    expect(
      resolveBoardNestedDrop(
        boardNestedSortableId(stackKey, "p-1", "c-1"),
        boardNestedSortableId(stackKey, "p-1", "c-2"),
      ),
    ).toEqual({ parentId: "p-1", activeChildId: "c-1", overChildId: "c-2" });
  });

  it("no-ops a cross-parent drop", () => {
    expect(
      resolveBoardNestedDrop(
        boardNestedSortableId(stackKey, "p-1", "c-1"),
        boardNestedSortableId(stackKey, "p-2", "c-9"),
      ),
    ).toBeNull();
  });

  it("no-ops a cross-appearance drop of the same parent", () => {
    expect(
      resolveBoardNestedDrop(
        boardNestedSortableId("epic-card:e-1:p-1", "p-1", "c-1"),
        boardNestedSortableId("epic-card:e-2:p-1", "p-1", "c-2"),
      ),
    ).toBeNull();
  });

  it("no-ops card-face, column-droppable, and non-nested overs", () => {
    const active = boardNestedSortableId(stackKey, "p-1", "c-1");
    expect(resolveBoardNestedDrop(active, "p-1")).toBeNull();
    expect(resolveBoardNestedDrop(active, "state:s-todo")).toBeNull();
    expect(
      resolveBoardNestedDrop(active, "subgroup-card:epic::e-9:p-1"),
    ).toBeNull();
    expect(resolveBoardNestedDrop("p-1", active)).toBeNull();
  });
});

describe("isBoardNestedDragEnabled", () => {
  it("requires toggle on, >= 2 rows, and canEdit", () => {
    expect(isBoardNestedDragEnabled(true, 3, true)).toBe(true);
    expect(isBoardNestedDragEnabled(false, 3, true)).toBe(false);
    expect(isBoardNestedDragEnabled(true, 1, true)).toBe(false);
    expect(isBoardNestedDragEnabled(true, 0, true)).toBe(false);
    expect(isBoardNestedDragEnabled(true, 3, false)).toBe(false);
  });
});

describe("resolveBoardNestedOverlay", () => {
  const nestedByParent = new Map([
    ["p-1", [row("c-1", 1024), row("c-2", 2048)]],
  ]);
  it("resolves a nested active id to its row", () => {
    expect(
      resolveBoardNestedOverlay(
        boardNestedSortableId("p-1", "p-1", "c-2"),
        nestedByParent,
      ),
    ).toEqual({ parentId: "p-1", row: row("c-2", 2048) });
  });

  it("returns null for card ids, unknown parents, and unknown children", () => {
    expect(resolveBoardNestedOverlay("p-1", nestedByParent)).toBeNull();
    expect(
      resolveBoardNestedOverlay(
        boardNestedSortableId("p-1", "p-9", "c-1"),
        nestedByParent,
      ),
    ).toBeNull();
    expect(
      resolveBoardNestedOverlay(
        boardNestedSortableId("p-1", "p-1", "c-9"),
        nestedByParent,
      ),
    ).toBeNull();
  });
});

describe("reorderBoardNestedInIssues", () => {
  it("reorders the parent's subtasks and writes the dragged sort_order", () => {
    const issues = [
      makeIssue("p-1", [row("c-1", 1024), row("c-2", 2048), row("c-3", 3072)]),
      makeIssue("p-2", [row("c-9", 1024)]),
    ];
    const next = reorderBoardNestedInIssues(
      issues,
      "p-1",
      ["c-2", "c-1", "c-3"],
      "c-1",
      2500,
    );
    expect(next).not.toBe(issues);
    expect(next[0].subtasks?.map((r) => r.id)).toEqual(["c-2", "c-1", "c-3"]);
    expect(next[0].subtasks?.find((r) => r.id === "c-1")?.sort_order).toBe(
      2500,
    );
    // Untouched rows keep their values; other parents untouched.
    expect(next[0].subtasks?.find((r) => r.id === "c-2")?.sort_order).toBe(
      2048,
    );
    expect(next[1]).toBe(issues[1]);
  });

  it("returns the input unchanged on missing parent or id mismatch", () => {
    const issues = [makeIssue("p-1", [row("c-1", 1024)])];
    expect(
      reorderBoardNestedInIssues(issues, "p-9", ["c-1"], "c-1", 5),
    ).toBe(issues);
    expect(
      reorderBoardNestedInIssues(issues, "p-1", ["c-1", "c-2"], "c-1", 5),
    ).toBe(issues);
  });
});

describe("applyBoardNestedDrop", () => {
  const stack = [row("c-1", 1024), row("c-2", 2048), row("c-3", 3072)];

  it("computes the midpoint on the sibling stack and PATCHes sort_order only", async () => {
    const { d, patch, setData, invalidate, onError } = deps({
      getData: () => [makeIssue("p-1", stack)],
    });
    // Drag c-1 down over c-2: [c-2, c-1, c-3] → midpoint(2048, 3072).
    await applyBoardNestedDrop(stack, "c-1", "c-2", d);
    expect(patch).toHaveBeenCalledTimes(1);
    expect(patch).toHaveBeenCalledWith("c-1", { sort_order: 2560 });
    // Body carries ONLY sort_order — never state_id, never parent_id.
    expect(Object.keys(patch.mock.calls[0][1])).toEqual(["sort_order"]);
    expect(invalidate).toHaveBeenCalledTimes(1);
    expect(onError).not.toHaveBeenCalled();
    // Optimistic write: reordered stack + dragged value written.
    expect(setData).toHaveBeenCalledTimes(1);
    const written = setData.mock.calls[0][0] as Issue[];
    expect(written[0].subtasks?.map((r) => r.id)).toEqual([
      "c-2",
      "c-1",
      "c-3",
    ]);
    expect(written[0].subtasks?.find((r) => r.id === "c-1")?.sort_order).toBe(
      2560,
    );
  });

  it("writes nothing for a no-op drop (active === over, unknown ids)", async () => {
    const { d, patch, setData, invalidate } = deps();
    await applyBoardNestedDrop(stack, "c-1", "c-1", d);
    await applyBoardNestedDrop(stack, "c-9", "c-2", d);
    await applyBoardNestedDrop(stack, "c-1", "c-9", d);
    expect(patch).not.toHaveBeenCalled();
    expect(setData).not.toHaveBeenCalled();
    expect(invalidate).not.toHaveBeenCalled();
  });

  it("reverts the optimistic write and surfaces the error on PATCH failure", async () => {
    const snapshot = [makeIssue("p-1", stack)];
    const { d, patch, setData, onError, invalidate } = deps({
      getData: () => snapshot,
    });
    patch.mockRejectedValueOnce(new Error("network down"));
    await applyBoardNestedDrop(stack, "c-1", "c-2", d);
    expect(setData).toHaveBeenCalledTimes(2);
    expect(setData.mock.calls[1][0]).toBe(snapshot);
    expect(onError).toHaveBeenCalledWith("network down");
    expect(invalidate).not.toHaveBeenCalled();
  });

  it("gap collapse: rebalances distinct neighbor states, refetches, retries once", async () => {
    // c-3 lands between a (0) and b (5e-10): gap < 1e-9 → collapse.
    const collapsed = [
      row("c-1", 0, "s-1"),
      row("c-2", 5e-10, "s-2"),
      row("c-3", 1000, "s-1"),
    ];
    const fresh = [
      row("c-1", 1024, "s-1"),
      row("c-2", 2048, "s-2"),
      row("c-3", 3072, "s-1"),
    ];
    const { d, patch, rebalance, refetchStack, onError } = deps({
      refetchStack: vi.fn().mockResolvedValue(fresh),
    });
    await applyBoardNestedDrop(collapsed, "c-3", "c-2", d);
    // Distinct neighbor states of the drop (s-1 above, s-2 below).
    expect(rebalance).toHaveBeenCalledTimes(2);
    expect(rebalance).toHaveBeenCalledWith({ state_id: "s-1" });
    expect(rebalance).toHaveBeenCalledWith({ state_id: "s-2" });
    expect(refetchStack).toHaveBeenCalledTimes(1);
    // Retry against the fresh stack: [c-1, c-3, c-2] → midpoint(1024, 2048).
    expect(patch).toHaveBeenCalledTimes(1);
    expect(patch).toHaveBeenCalledWith("c-3", { sort_order: 1536 });
    expect(onError).not.toHaveBeenCalled();
  });

  it("gap collapse with shared neighbor state rebalances once", async () => {
    const collapsed = [
      row("c-1", 0, "s-1"),
      row("c-2", 5e-10, "s-1"),
      row("c-3", 1000, "s-1"),
    ];
    const fresh = [
      row("c-1", 1024, "s-1"),
      row("c-2", 2048, "s-1"),
      row("c-3", 3072, "s-1"),
    ];
    const { d, rebalance, refetchStack } = deps({
      refetchStack: vi.fn().mockResolvedValue(fresh),
    });
    await applyBoardNestedDrop(collapsed, "c-3", "c-2", d);
    expect(rebalance).toHaveBeenCalledTimes(1);
    expect(rebalance).toHaveBeenCalledWith({ state_id: "s-1" });
    expect(refetchStack).toHaveBeenCalledTimes(1);
  });

  it("gap collapse falls back to the dragged child's state when neighbors are null", async () => {
    const collapsed = [
      row("c-1", 0, null),
      row("c-2", 5e-10, null),
      row("c-3", 1000, "s-9"),
    ];
    const fresh = [
      row("c-1", 1024, null),
      row("c-2", 2048, null),
      row("c-3", 3072, "s-9"),
    ];
    const { d, patch, rebalance, onError } = deps({
      refetchStack: vi.fn().mockResolvedValue(fresh),
    });
    await applyBoardNestedDrop(collapsed, "c-3", "c-2", d);
    expect(rebalance).toHaveBeenCalledTimes(1);
    expect(rebalance).toHaveBeenCalledWith({ state_id: "s-9" });
    expect(patch).toHaveBeenCalledTimes(1);
    expect(onError).not.toHaveBeenCalled();
  });

  it("null-state child between null-state neighbors errors honestly, no retry", async () => {
    const collapsed = [
      row("c-1", 0, null),
      row("c-2", 5e-10, null),
      row("c-3", 1000, null),
    ];
    const { d, patch, rebalance, refetchStack, onError } = deps();
    await applyBoardNestedDrop(collapsed, "c-3", "c-2", d);
    expect(rebalance).not.toHaveBeenCalled();
    expect(refetchStack).not.toHaveBeenCalled();
    expect(patch).not.toHaveBeenCalled();
    expect(onError).toHaveBeenCalledWith(
      "Couldn't place the subtask: it has no state to rebalance.",
    );
  });

  it("still-collapsed gap after rebalance errors honestly, never a bogus value", async () => {
    const collapsed = [
      row("c-1", 0, "s-1"),
      row("c-2", 5e-10, "s-1"),
      row("c-3", 1000, "s-1"),
    ];
    const { d, patch, onError } = deps({
      refetchStack: vi.fn().mockResolvedValue(collapsed),
    });
    await applyBoardNestedDrop(collapsed, "c-3", "c-2", d);
    expect(patch).not.toHaveBeenCalled();
    expect(onError).toHaveBeenCalledWith(
      "Couldn't place the subtask even after rebalancing.",
    );
  });

  it("rebalance failure aborts honestly, writing nothing", async () => {
    const collapsed = [
      row("c-1", 0, "s-1"),
      row("c-2", 5e-10, "s-1"),
      row("c-3", 1000, "s-1"),
    ];
    const { d, patch, rebalance, refetchStack, onError } = deps();
    rebalance.mockRejectedValueOnce(new Error("rebalance failed"));
    await applyBoardNestedDrop(collapsed, "c-3", "c-2", d);
    expect(patch).not.toHaveBeenCalled();
    expect(refetchStack).not.toHaveBeenCalled();
    expect(onError).toHaveBeenCalledWith("rebalance failed");
  });
});
