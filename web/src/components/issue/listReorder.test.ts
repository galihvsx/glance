// @vitest-environment jsdom
// C126T0: list-view drag-to-reorder pipeline — enable rules, flat-list
// optimistic reorder, infinite-page refill, and the drop flow itself:
// PATCH carries ONLY {sort_order} (never state_id), PATCH failure
// reverts the optimistic order, and a collapsed gap rebalances +
// retries once before an honest error.
import type { InfiniteData } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import type { Issue, IssueListResult } from "../../lib/types";
import { REBALANCE_GAP } from "../../lib/sortOrder";
import {
  applyListDrop,
  applyStackOrderToFlat,
  isListDragEnabled,
  reorderInfinitePages,
  type ListDropDeps,
} from "./listReorder";
import type { GroupBy, OrderBy } from "./useDisplaySettings";

function mkIssue(
  id: string,
  sortOrder: number,
  stateId = "s-todo",
): Issue {
  return {
    id,
    project_id: "p",
    sequence_id: 1,
    display_id: `ENG-${id}`,
    name: `Issue ${id}`,
    priority: 0,
    state_id: stateId,
    sort_order: sortOrder,
    is_draft: false,
    created_by: "u-1",
    created_at: "",
    updated_at: "",
    assignees: [],
    labels: [],
  };
}

function pagesOf(flat: Issue[], sizes: number[]): InfiniteData<IssueListResult> {
  const pages: IssueListResult[] = [];
  let c = 0;
  for (const n of sizes) {
    pages.push({
      results: flat.slice(c, c + n),
      next_cursor: c + n < flat.length ? `cur-${c + n}` : undefined,
    });
    c += n;
  }
  return { pages, pageParams: sizes.map(() => undefined) };
}

describe("isListDragEnabled", () => {
  const base = { pqlMode: false, groupBy: "none" as GroupBy };
  it("enables only for Manual rank (sort_order) in visual mode", () => {
    expect(
      isListDragEnabled({ ...base, orderBy: "sort_order" }),
    ).toBe(true);
    const others: OrderBy[] = [
      "-updated_at",
      "updated_at",
      "-created_at",
      "created_at",
      "-priority",
      "priority",
      "sequence_id",
      "-sequence_id",
    ];
    for (const orderBy of others) {
      expect(isListDragEnabled({ ...base, orderBy })).toBe(false);
    }
  });
  it("disables in PQL mode even under Manual rank", () => {
    expect(
      isListDragEnabled({ ...base, orderBy: "sort_order", pqlMode: true }),
    ).toBe(false);
  });
  it("enables for flat and state grouping, disables priority/epic", () => {
    for (const groupBy of ["none", "state"] as GroupBy[]) {
      expect(
        isListDragEnabled({ ...base, orderBy: "sort_order", groupBy }),
      ).toBe(true);
    }
    for (const groupBy of ["priority", "epic"] as GroupBy[]) {
      expect(
        isListDragEnabled({ ...base, orderBy: "sort_order", groupBy }),
      ).toBe(false);
    }
  });
});

describe("applyStackOrderToFlat", () => {
  const flat = [
    mkIssue("a", 1024, "s-todo"),
    mkIssue("x", 1024, "s-doing"),
    mkIssue("b", 2048, "s-todo"),
    mkIssue("c", 3072, "s-todo"),
  ];
  it("splices the dragged row after its new stack predecessor, keeping other-stack rows in place", () => {
    // Stack [a, b, c] → [b, c, a]: a lands after c in flat order.
    const next = applyStackOrderToFlat(flat, ["b", "c", "a"], "a");
    expect(next.map((i) => i.id)).toEqual(["x", "b", "c", "a"]);
  });
  it("inserts before the successor when landing first", () => {
    // Stack [a, b, c] → [c, a, b]: c lands before a in flat order.
    const next = applyStackOrderToFlat(flat, ["c", "a", "b"], "c");
    expect(next.map((i) => i.id)).toEqual(["c", "a", "x", "b"]);
  });
  it("leaves the list unchanged for an unknown dragged id", () => {
    expect(applyStackOrderToFlat(flat, ["b", "c"], "nope")).toBe(flat);
  });
});

describe("reorderInfinitePages", () => {
  it("redistributes the new order preserving page sizes and cursors", () => {
    const flat = ["a", "b", "c", "d"].map((id, i) =>
      mkIssue(id, (i + 1) * 1024),
    );
    const data = pagesOf(flat, [3, 1]);
    const next = reorderInfinitePages(data, [flat[1], flat[2], flat[0], flat[3]]);
    expect(next.pages.map((p) => p.results.map((i) => i.id))).toEqual([
      ["b", "c", "a"],
      ["d"],
    ]);
    expect(next.pages[0].next_cursor).toBe("cur-3");
  });
  it("refuses to drop or duplicate rows when the id sets differ", () => {
    const flat = ["a", "b"].map((id, i) => mkIssue(id, (i + 1) * 1024));
    const data = pagesOf(flat, [2]);
    expect(reorderInfinitePages(data, [flat[0]])).toBe(data);
  });
});

function stubDeps(
  data: InfiniteData<IssueListResult>,
  over: Partial<ListDropDeps> = {},
): { deps: ListDropDeps; calls: Record<string, unknown[]>; getCurrent: () => InfiniteData<IssueListResult> } {
  let current = data;
  const calls: Record<string, unknown[]> = {
    setData: [],
    patch: [],
    rebalance: [],
    invalidate: [],
    errors: [],
  };
  const deps: ListDropDeps = {
    getData: () => current,
    setData: (d) => {
      current = d;
      calls.setData.push(d.pages.flatMap((p) => p.results.map((i) => i.id)));
    },
    patch: async (id, body) => {
      calls.patch.push([id, body]);
      if (over.patch) return over.patch(id, body);
    },
    rebalance: async (body) => {
      calls.rebalance.push(body);
      if (over.rebalance) return over.rebalance(body);
    },
    refetchFlat: over.refetchFlat ?? (async () => []),
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

describe("applyListDrop", () => {
  const stack = [
    mkIssue("a", 1024),
    mkIssue("b", 2048),
    mkIssue("c", 3072),
  ];

  it("PATCHes ONLY {sort_order} (no state_id) and invalidates after the optimistic reorder", async () => {
    const data = pagesOf(stack, [3]);
    const { deps, calls, getCurrent } = stubDeps(data);
    // Drag a over c: final [b, c, a] → after 3072.
    await applyListDrop(stack, "a", "c", deps);
    expect(calls.patch).toEqual([["a", { sort_order: 4096 }]]);
    // The PATCH body carries no state_id key at all.
    expect(Object.keys((calls.patch[0] as unknown[])[1] as object)).toEqual([
      "sort_order",
    ]);
    // Optimistic order was written before the PATCH…
    expect(calls.setData[0]).toEqual(["b", "c", "a"]);
    // …and the final state is still the optimistic order (server order
    // arrives via the invalidation refetch).
    expect(getCurrent().pages[0].results.map((i) => i.id)).toEqual([
      "b",
      "c",
      "a",
    ]);
    expect(calls.invalidate).toHaveLength(1);
    expect(calls.errors).toHaveLength(0);
  });

  it("reverts the optimistic order and surfaces the error when PATCH fails", async () => {
    const data = pagesOf(stack, [3]);
    const { deps, calls, getCurrent } = stubDeps(data, {
      patch: async () => {
        throw new Error("PATCH exploded");
      },
    });
    await applyListDrop(stack, "a", "c", deps);
    expect(calls.patch).toHaveLength(1);
    // Optimistic write, then snapshot restore.
    expect(calls.setData).toHaveLength(2);
    expect(getCurrent().pages[0].results.map((i) => i.id)).toEqual([
      "a",
      "b",
      "c",
    ]);
    expect(calls.errors).toEqual(["PATCH exploded"]);
    expect(calls.invalidate).toHaveLength(0);
  });

  it("rebalances the dragged row's state and retries once on a collapsed gap", async () => {
    const collapsed = [
      mkIssue("a", 1),
      mkIssue("b", 1 + REBALANCE_GAP / 2),
      mkIssue("c", 5),
      mkIssue("d", 6, "s-doing"),
    ];
    const data = pagesOf(collapsed, [4]);
    // After rebalance the state re-spaces to even 1024-steps.
    const fresh = [
      mkIssue("a", 1024),
      mkIssue("b", 2048),
      mkIssue("c", 3072),
      mkIssue("d", 4096, "s-doing"),
    ];
    const { deps, calls } = stubDeps(data, {
      refetchFlat: async () => fresh,
    });
    // Drag d over b: first attempt lands between collapsed a/b → null.
    await applyListDrop(collapsed, "d", "b", deps);
    expect(calls.rebalance).toEqual([{ state_id: "s-doing" }]);
    // Retry against the fresh stack: [a, d, b, c] → midpoint(1024, 2048).
    expect(calls.patch).toEqual([["d", { sort_order: 1536 }]]);
    expect(calls.invalidate).toHaveLength(1);
    expect(calls.errors).toHaveLength(0);
  });

  it("surfaces an honest error and writes nothing when rebalance fails", async () => {
    const collapsed = [
      mkIssue("a", 1),
      mkIssue("b", 1 + REBALANCE_GAP / 2),
      mkIssue("c", 5),
      mkIssue("d", 6),
    ];
    const data = pagesOf(collapsed, [4]);
    const { deps, calls } = stubDeps(data, {
      rebalance: async () => {
        throw new Error("rebalance down");
      },
    });
    await applyListDrop(collapsed, "d", "b", deps);
    expect(calls.rebalance).toHaveLength(1);
    expect(calls.patch).toHaveLength(0);
    expect(calls.setData).toHaveLength(0);
    expect(calls.errors).toEqual(["rebalance down"]);
  });

  it("gives up honestly when the gap is still collapsed after rebalance", async () => {
    const collapsed = [
      mkIssue("a", 1),
      mkIssue("b", 1 + REBALANCE_GAP / 2),
      mkIssue("c", 5),
      mkIssue("d", 6),
    ];
    const data = pagesOf(collapsed, [4]);
    const { deps, calls } = stubDeps(data, {
      refetchFlat: async () => collapsed,
    });
    await applyListDrop(collapsed, "d", "b", deps);
    expect(calls.rebalance).toHaveLength(1);
    expect(calls.patch).toHaveLength(0);
    expect(calls.errors).toEqual([
      "Couldn't place the issue even after rebalancing.",
    ]);
  });

  it("writes nothing for a no-op drop", async () => {
    const data = pagesOf(stack, [3]);
    const { deps, calls } = stubDeps(data);
    await applyListDrop(stack, "a", "a", deps);
    expect(calls.patch).toHaveLength(0);
    expect(calls.rebalance).toHaveLength(0);
    expect(calls.setData).toHaveLength(0);
    expect(calls.errors).toHaveLength(0);
  });
});
