// @vitest-environment jsdom
// C141T1: list-view nested subtask drag-to-reorder pipeline — gate,
// optimistic nested rewrite, drop math (top/middle/bottom), and the
// drop flow itself: PATCH carries ONLY {sort_order} (never state_id
// or parent_id), PATCH failure reverts the optimistic order, and a
// collapsed gap rebalances + retries once before an honest error.
// The rebalance targets are the DISTINCT non-null neighbor states
// (C128 mixed-state recovery): a nested stack may mix states, so
// rebalancing only the dragged child's state cannot re-open a
// mixed-state gap. A null-state child still retries when its
// neighbors carry states; only a stack with NO state at all errors
// without a retry.
import type { InfiniteData } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import type { Issue, IssueListResult, SubtaskRow } from "../../lib/types";
import { REBALANCE_GAP } from "../../lib/sortOrder";
import {
  applyNestedListDrop,
  isNestedDragEnabled,
  nestedDropNeighborStateIds,
  reorderNestedInPages,
  type NestedListDropDeps,
} from "./listNestedReorder";

function mkChild(
  id: string,
  sortOrder: number,
  stateId: string | null = "s-todo",
): SubtaskRow {
  return {
    id,
    display_id: `ENG-${id}`,
    title: `Child ${id}`,
    state_id: stateId,
    priority: null,
    assignee_ids: [],
    sort_order: sortOrder,
  };
}

function mkIssue(
  id: string,
  sortOrder: number,
  extra: Partial<Issue> = {},
): Issue {
  return {
    id,
    project_id: "p",
    sequence_id: 1,
    display_id: `ENG-${id}`,
    name: `Issue ${id}`,
    priority: 0,
    state_id: "s-todo",
    sort_order: sortOrder,
    is_draft: false,
    created_by: "u-1",
    created_at: "",
    updated_at: "",
    assignees: [],
    labels: [],
    ...extra,
  };
}

function pagesOf(flat: Issue[]): InfiniteData<IssueListResult> {
  return {
    pages: [{ results: flat, next_cursor: undefined }],
    pageParams: [undefined],
  };
}

function parentSubtasks(
  data: InfiniteData<IssueListResult>,
  parentId: string,
): SubtaskRow[] {
  const parent = data.pages
    .flatMap((p) => p.results)
    .find((i) => i.id === parentId);
  return parent?.subtasks ?? [];
}

describe("isNestedDragEnabled", () => {
  it("enables with showSubWorkItems on and >= 2 rows", () => {
    expect(isNestedDragEnabled(true, 2)).toBe(true);
    expect(isNestedDragEnabled(true, 5)).toBe(true);
  });
  it("disables with showSubWorkItems off, however many rows", () => {
    expect(isNestedDragEnabled(false, 2)).toBe(false);
    expect(isNestedDragEnabled(false, 5)).toBe(false);
  });
  it("disables for 1-row and empty stacks — nowhere to drop", () => {
    expect(isNestedDragEnabled(true, 1)).toBe(false);
    expect(isNestedDragEnabled(true, 0)).toBe(false);
  });
  it("is order-independent by construction: no orderBy/PQL/grouping input exists", () => {
    // The gate takes only the toggle + the row count — there is no
    // parameter through which orderBy, PQL mode, or grouping could
    // disable it. The order-independence proof is the read behind
    // this: nestedByParent sorts by sort_order ASC on every render
    // site, so a nested drop is always exact.
    expect(isNestedDragEnabled.length).toBe(2);
  });
});

describe("reorderNestedInPages", () => {
  const stack = [mkChild("c1", 1024), mkChild("c2", 2048), mkChild("c3", 3072)];
  const other = mkIssue("i-other", 512, {
    subtasks: [mkChild("x1", 1024)],
  });
  const parent = mkIssue("i-p", 1024, { subtasks: stack });
  const data = pagesOf([parent, other]);

  it("rewrites the parent's stack in the new order AND the dragged row's new value", () => {
    // Drag c1 over c3: [c2, c3, c1] at 4096. The value write is
    // load-bearing: the list renders nested rows sorted by
    // sort_order ASC, not in subtask-array order, so a bare array
    // reorder would change nothing on screen.
    const next = reorderNestedInPages(
      data,
      "i-p",
      ["c2", "c3", "c1"],
      "c1",
      4096,
    );
    expect(parentSubtasks(next, "i-p").map((r) => r.id)).toEqual([
      "c2",
      "c3",
      "c1",
    ]);
    expect(parentSubtasks(next, "i-p")).toEqual([
      expect.objectContaining({ id: "c2", sort_order: 2048 }),
      expect.objectContaining({ id: "c3", sort_order: 3072 }),
      expect.objectContaining({ id: "c1", sort_order: 4096 }),
    ]);
    // The other parent's stack is untouched.
    expect(parentSubtasks(next, "i-other").map((r) => r.id)).toEqual(["x1"]);
    // The original data is untouched (no mutation of the snapshot).
    expect(parentSubtasks(data, "i-p").map((r) => r.id)).toEqual([
      "c1",
      "c2",
      "c3",
    ]);
  });

  it("refuses to drop or duplicate rows when the id sets differ", () => {
    expect(reorderNestedInPages(data, "i-p", ["c1"], "c1", 512)).toBe(data);
    expect(
      reorderNestedInPages(data, "i-p", ["c1", "c2", "nope"], "c1", 512),
    ).toBe(data);
  });

  it("returns the input unchanged when the parent is missing", () => {
    expect(
      reorderNestedInPages(data, "i-gone", ["c1", "c2"], "c1", 512),
    ).toBe(data);
  });
});

describe("nestedDropNeighborStateIds", () => {
  const stack = [
    mkChild("c1", 1024, "s-todo"),
    mkChild("c2", 2048, "s-doing"),
    mkChild("c3", 3072, "s-todo"),
  ];
  it("returns both distinct neighbor states for a middle drop", () => {
    expect(
      nestedDropNeighborStateIds(stack, { orderedIds: ["c1", "c3", "c2"] }, "c3"),
    ).toEqual(["s-todo", "s-doing"]);
  });
  it("returns the single neighbor for top/bottom drops", () => {
    expect(
      nestedDropNeighborStateIds(stack, { orderedIds: ["c3", "c1", "c2"] }, "c3"),
    ).toEqual(["s-todo"]);
    expect(
      nestedDropNeighborStateIds(stack, { orderedIds: ["c1", "c2", "c3"] }, "c3"),
    ).toEqual(["s-doing"]);
  });
  it("skips null-state neighbors and returns [] for an unknown id", () => {
    const nulls = [mkChild("n1", 1024, null), mkChild("n2", 2048, null)];
    expect(
      nestedDropNeighborStateIds(nulls, { orderedIds: ["n2", "n1"] }, "n2"),
    ).toEqual([]);
    expect(
      nestedDropNeighborStateIds(stack, { orderedIds: ["c1", "c2"] }, "nope"),
    ).toEqual([]);
  });
});

function stubDeps(
  data: InfiniteData<IssueListResult>,
  over: Partial<NestedListDropDeps> = {},
): {
  deps: NestedListDropDeps;
  calls: Record<string, unknown[]>;
  getCurrent: () => InfiniteData<IssueListResult>;
} {
  let current = data;
  const calls: Record<string, unknown[]> = {
    setData: [],
    patch: [],
    rebalance: [],
    invalidate: [],
    errors: [],
  };
  const deps: NestedListDropDeps = {
    parentId: "i-p",
    getData: () => current,
    setData: (d) => {
      current = d;
      calls.setData.push(
        parentSubtasks(d, "i-p").map((r) => [r.id, r.sort_order]),
      );
    },
    patch: async (id, body) => {
      calls.patch.push([id, body]);
      if (over.patch) return over.patch(id, body);
    },
    rebalance: async (body) => {
      calls.rebalance.push(body);
      if (over.rebalance) return over.rebalance(body);
    },
    refetchStack: over.refetchStack ?? (async () => []),
    invalidate: async () => {
      calls.invalidate.push(true);
      if (over.invalidate) return over.invalidate();
    },
    onError: (m) => {
      calls.errors.push(m);
    },
  };
  return { deps, calls, getCurrent: () => current };
}

describe("applyNestedListDrop", () => {
  const stack = [
    mkChild("c1", 1024, "s-todo"),
    mkChild("c2", 2048, "s-todo"),
    mkChild("c3", 3072, "s-doing"),
  ];
  const dataOf = (rows: SubtaskRow[]) =>
    pagesOf([mkIssue("i-p", 1024, { subtasks: rows })]);

  it("PATCHes ONLY {sort_order} (no state_id, no parent_id) and invalidates after the optimistic reorder", async () => {
    const data = dataOf(stack);
    const { deps, calls, getCurrent } = stubDeps(data);
    // Drag c1 over c3: final [c2, c3, c1] → after 3072.
    await applyNestedListDrop(stack, "c1", "c3", deps);
    expect(calls.patch).toEqual([["c1", { sort_order: 4096 }]]);
    expect(Object.keys((calls.patch[0] as unknown[])[1] as object)).toEqual([
      "sort_order",
    ]);
    // Optimistic write carries the new order AND the new value…
    expect(calls.setData[0]).toEqual([
      ["c2", 2048],
      ["c3", 3072],
      ["c1", 4096],
    ]);
    // …and the final state is still the optimistic order (server
    // order arrives via the invalidation refetch).
    expect(parentSubtasks(getCurrent(), "i-p").map((r) => r.id)).toEqual([
      "c2",
      "c3",
      "c1",
    ]);
    expect(calls.invalidate).toHaveLength(1);
    expect(calls.errors).toHaveLength(0);
  });

  it("computes the middle midpoint for a between-neighbors drop", async () => {
    const data = dataOf(stack);
    const { deps, calls } = stubDeps(data);
    // Drag c3 over c2: final [c1, c3, c2] → midpoint(1024, 2048).
    await applyNestedListDrop(stack, "c3", "c2", deps);
    expect(calls.patch).toEqual([["c3", { sort_order: 1536 }]]);
  });

  it("computes the top-of-stack value for a drop on the first row", async () => {
    const data = dataOf(stack);
    const { deps, calls } = stubDeps(data);
    // Drag c3 over c1: final [c3, c1, c2] → next - STEP.
    await applyNestedListDrop(stack, "c3", "c1", deps);
    expect(calls.patch).toEqual([["c3", { sort_order: 0 }]]);
  });

  it("reverts the optimistic order and surfaces the error when PATCH fails", async () => {
    const data = dataOf(stack);
    const { deps, calls, getCurrent } = stubDeps(data, {
      patch: async () => {
        throw new Error("PATCH exploded");
      },
    });
    await applyNestedListDrop(stack, "c1", "c3", deps);
    expect(calls.patch).toHaveLength(1);
    // Optimistic write, then snapshot restore (order AND values).
    expect(calls.setData).toHaveLength(2);
    expect(parentSubtasks(getCurrent(), "i-p")).toEqual(stack);
    expect(calls.errors).toEqual(["PATCH exploded"]);
    expect(calls.invalidate).toHaveLength(0);
  });

  it("rebalances the drop neighbors' shared state and retries once on a collapsed gap", async () => {
    const collapsed = [
      mkChild("a", 1, "s-todo"),
      mkChild("b", 1 + REBALANCE_GAP / 2, "s-todo"),
      mkChild("c", 5, "s-todo"),
      mkChild("d", 6, "s-todo"),
    ];
    const data = dataOf(collapsed);
    const fresh = [
      mkChild("a", 1024, "s-todo"),
      mkChild("b", 2048, "s-todo"),
      mkChild("c", 3072, "s-todo"),
      mkChild("d", 4096, "s-todo"),
    ];
    const { deps, calls } = stubDeps(data, {
      refetchStack: async () => fresh,
    });
    // Drag d over b: first attempt lands between collapsed a/b → null.
    await applyNestedListDrop(collapsed, "d", "b", deps);
    expect(calls.rebalance).toEqual([{ state_id: "s-todo" }]);
    // Retry against the fresh stack: [a, d, b, c] → midpoint(1024, 2048).
    expect(calls.patch).toEqual([["d", { sort_order: 1536 }]]);
    expect(calls.invalidate).toHaveLength(1);
    expect(calls.errors).toHaveLength(0);
  });

  it("rebalances BOTH distinct neighbor states, in order, for a mixed-state gap", async () => {
    const collapsed = [
      mkChild("a", 1, "s-todo"),
      mkChild("b", 1 + REBALANCE_GAP / 2, "s-doing"),
      mkChild("c", 5, "s-todo"),
      mkChild("d", 6, "s-todo"),
    ];
    const data = dataOf(collapsed);
    const fresh = [
      mkChild("a", 1024, "s-todo"),
      mkChild("b", 2048, "s-doing"),
      mkChild("c", 3072, "s-todo"),
      mkChild("d", 4096, "s-todo"),
    ];
    const { deps, calls } = stubDeps(data, {
      refetchStack: async () => fresh,
    });
    // Rebalancing s-todo alone could never re-open the a/b gap —
    // only both neighbor states can.
    await applyNestedListDrop(collapsed, "d", "b", deps);
    expect(calls.rebalance).toEqual([
      { state_id: "s-todo" },
      { state_id: "s-doing" },
    ]);
    expect(calls.patch).toEqual([["d", { sort_order: 1536 }]]);
  });

  it("retries via neighbor states for a null-state child, and errors honestly when no state exists at all", async () => {
    const collapsed = [
      mkChild("a", 1, "s-todo"),
      mkChild("b", 1 + REBALANCE_GAP / 2, "s-todo"),
      mkChild("c", 5, "s-todo"),
      mkChild("d", 6, null),
    ];
    const data = dataOf(collapsed);
    const fresh = [
      mkChild("a", 1024, "s-todo"),
      mkChild("b", 2048, "s-todo"),
      mkChild("c", 3072, "s-todo"),
      mkChild("d", 4096, null),
    ];
    const { deps, calls } = stubDeps(data, {
      refetchStack: async () => fresh,
    });
    // The child has no state of its own, but its neighbors do — the
    // neighbor-state rebalance still re-opens the gap.
    await applyNestedListDrop(collapsed, "d", "b", deps);
    expect(calls.rebalance).toEqual([{ state_id: "s-todo" }]);
    expect(calls.patch).toEqual([["d", { sort_order: 1536 }]]);

    // All-null stack: no rebalance target anywhere → honest error,
    // no retry, no write.
    const allNull = [
      mkChild("a", 1, null),
      mkChild("b", 1 + REBALANCE_GAP / 2, null),
      mkChild("c", 5, null),
      mkChild("d", 6, null),
    ];
    const { deps: deps2, calls: calls2 } = stubDeps(dataOf(allNull));
    await applyNestedListDrop(allNull, "d", "b", deps2);
    expect(calls2.rebalance).toHaveLength(0);
    expect(calls2.patch).toHaveLength(0);
    expect(calls2.errors).toEqual([
      "Couldn't place the subtask: it has no state to rebalance.",
    ]);
  });

  it("surfaces an honest error and writes nothing when rebalance fails", async () => {
    const collapsed = [
      mkChild("a", 1, "s-todo"),
      mkChild("b", 1 + REBALANCE_GAP / 2, "s-todo"),
      mkChild("c", 5, "s-todo"),
      mkChild("d", 6, "s-todo"),
    ];
    const data = dataOf(collapsed);
    const { deps, calls } = stubDeps(data, {
      rebalance: async () => {
        throw new Error("rebalance down");
      },
    });
    await applyNestedListDrop(collapsed, "d", "b", deps);
    expect(calls.rebalance).toHaveLength(1);
    expect(calls.patch).toHaveLength(0);
    expect(calls.setData).toHaveLength(0);
    expect(calls.errors).toEqual(["rebalance down"]);
  });

  it("gives up honestly when the gap is still collapsed after rebalance", async () => {
    const collapsed = [
      mkChild("a", 1, "s-todo"),
      mkChild("b", 1 + REBALANCE_GAP / 2, "s-todo"),
      mkChild("c", 5, "s-todo"),
      mkChild("d", 6, "s-todo"),
    ];
    const data = dataOf(collapsed);
    const { deps, calls } = stubDeps(data, {
      refetchStack: async () => collapsed,
    });
    await applyNestedListDrop(collapsed, "d", "b", deps);
    expect(calls.rebalance).toHaveLength(1);
    expect(calls.patch).toHaveLength(0);
    expect(calls.errors).toEqual([
      "Couldn't place the subtask even after rebalancing.",
    ]);
  });

  it("writes nothing for a no-op drop or an unknown dragged row", async () => {
    const data = dataOf(stack);
    const { deps, calls } = stubDeps(data);
    await applyNestedListDrop(stack, "c1", "c1", deps);
    await applyNestedListDrop(stack, "nope", "c2", deps);
    expect(calls.patch).toHaveLength(0);
    expect(calls.rebalance).toHaveLength(0);
    expect(calls.setData).toHaveLength(0);
    expect(calls.errors).toHaveLength(0);
  });
});
