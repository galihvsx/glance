// C127T0: group-header drop targets — drop resolution, call sequences,
// bottom-append math, id namespacing, and drag-end dispatch, all pure.
import { describe, expect, it, vi } from "vitest";
import type { Issue } from "../../lib/types";
import {
  bottomSortOrder,
  dispatchListDragEnd,
  executeListHeaderDrop,
  headerDropId,
  isListReassignEnabled,
  listSortableId,
  resolveListHeaderDrop,
  toHeaderTarget,
  type ListDragRowRef,
  type ListDragStackRef,
  type ListHeaderDropInfo,
  type ListHeaderDropPlan,
} from "./listHeaderDrop";

function mkIssue(
  id: string,
  over: Partial<Issue> = {},
): Issue {
  return {
    id,
    project_id: "p",
    sequence_id: 1,
    display_id: `ENG-${id}`,
    name: id,
    priority: 0,
    state_id: "s-todo",
    sort_order: 1024,
    is_draft: false,
    created_by: "u-1",
    created_at: "",
    updated_at: "",
    assignees: [],
    labels: [],
    ...over,
  };
}

function mkStack(
  stackKey: string,
  rows: Issue[],
  sourceEpicId: string | null = null,
): ListDragStackRef {
  return { stackKey, rows, sourceEpicId };
}

function mkRef(
  rowId: string,
  issue: Issue,
  stack: ListDragStackRef,
): ListDragRowRef {
  return { rowId, issue, stack };
}

describe("toHeaderTarget", () => {
  it("maps each explicit field to its target", () => {
    expect(toHeaderTarget({ targetStateId: "s-1" })).toEqual({
      kind: "state",
      stateId: "s-1",
    });
    expect(toHeaderTarget({ targetPriority: 0 })).toEqual({
      kind: "priority",
      priority: 0,
    });
    expect(toHeaderTarget({ targetEpicId: "e-1" })).toEqual({
      kind: "epic",
      epicId: "e-1",
    });
    expect(toHeaderTarget({ isUngroupedEpic: true })).toEqual({
      kind: "epic-ungrouped",
    });
  });

  it("returns null when no field names an assignable value (state-Ungrouped)", () => {
    expect(toHeaderTarget({})).toBeNull();
  });
});

describe("isListReassignEnabled", () => {
  it("needs grouping active and visual mode, under any orderBy", () => {
    expect(
      isListReassignEnabled({ pqlMode: false, groupingActive: true }),
    ).toBe(true);
    expect(
      isListReassignEnabled({ pqlMode: true, groupingActive: true }),
    ).toBe(false);
    expect(
      isListReassignEnabled({ pqlMode: false, groupingActive: false }),
    ).toBe(false);
  });
});

describe("drop ids", () => {
  it("header ids never collide with row sortable ids, by prefix", () => {
    const headers = [
      headerDropId("s-todo", { kind: "state", stateId: "s-doing" }),
      headerDropId("priority:3", { kind: "priority", priority: 2 }),
      headerDropId("e-1", { kind: "epic", epicId: "e-2" }),
      headerDropId("epic:ungrouped", { kind: "epic-ungrouped" }),
    ];
    const rows = [
      listSortableId("flat", "i-1"),
      listSortableId("s-todo", "i-1"),
      listSortableId("priority:3::s-todo", "i-1"),
    ];
    for (const h of headers) {
      expect(h.startsWith("header:")).toBe(true);
      expect(rows).not.toContain(h);
    }
    for (const r of rows) {
      expect(r.startsWith("row:")).toBe(true);
      expect(headers).not.toContain(r);
    }
  });

  it("the same target under different sections gets different header ids", () => {
    const a = headerDropId("priority:4::s-todo", {
      kind: "state",
      stateId: "s-todo",
    });
    const b = headerDropId("priority:3::s-todo", {
      kind: "state",
      stateId: "s-todo",
    });
    expect(a).not.toBe(b);
  });
});

describe("bottomSortOrder", () => {
  it("appends one step past the section max", () => {
    const dragged = mkIssue("d", { sort_order: 512 });
    expect(
      bottomSortOrder(
        [mkIssue("a", { sort_order: 1024 }), mkIssue("b", { sort_order: 3072 })],
        dragged,
      ),
    ).toBe(4096);
  });

  it("keeps the dragged row's value when the target section is empty", () => {
    const dragged = mkIssue("d", { sort_order: 512 });
    expect(bottomSortOrder([], dragged)).toBe(512);
  });
});

describe("resolveListHeaderDrop", () => {
  const targetRows = [
    mkIssue("t1", { sort_order: 1024 }),
    mkIssue("t2", { sort_order: 2048 }),
  ];

  it("state header: PATCH carries exactly {state_id, sort_order}", () => {
    const plan = resolveListHeaderDrop({
      dragged: mkIssue("d", { state_id: "s-todo", sort_order: 512 }),
      sourceEpicId: null,
      target: { kind: "state", stateId: "s-doing" },
      targetRows,
    });
    expect(plan).toEqual({
      type: "patch",
      body: { state_id: "s-doing", sort_order: 3072 },
    });
    // Nothing else changes: no priority key, no epic membership call.
    expect(plan && "body" in plan ? Object.keys(plan.body).sort() : []).toEqual([
      "sort_order",
      "state_id",
    ]);
  });

  it("state header: own-group drop is a silent no-op", () => {
    expect(
      resolveListHeaderDrop({
        dragged: mkIssue("d", { state_id: "s-doing" }),
        sourceEpicId: null,
        target: { kind: "state", stateId: "s-doing" },
        targetRows,
      }),
    ).toBeNull();
  });

  it("priority header: PATCH carries exactly {priority, sort_order}", () => {
    const plan = resolveListHeaderDrop({
      dragged: mkIssue("d", { priority: 1, sort_order: 512 }),
      sourceEpicId: null,
      target: { kind: "priority", priority: 3 },
      targetRows,
    });
    expect(plan).toEqual({
      type: "patch",
      body: { priority: 3, sort_order: 3072 },
    });
  });

  it("priority header: own-priority drop is a silent no-op", () => {
    expect(
      resolveListHeaderDrop({
        dragged: mkIssue("d", { priority: 3 }),
        sourceEpicId: null,
        target: { kind: "priority", priority: 3 },
        targetRows,
      }),
    ).toBeNull();
  });

  it("priority header: priority 0 is a real target, not falsy-no-target", () => {
    const plan = resolveListHeaderDrop({
      dragged: mkIssue("d", { priority: 2, sort_order: 512 }),
      sourceEpicId: null,
      target: { kind: "priority", priority: 0 },
      targetRows,
    });
    expect(plan).toEqual({
      type: "patch",
      body: { priority: 0, sort_order: 3072 },
    });
  });

  it("epic header: add-before-remove + sort PATCH, from an epic source", () => {
    const plan = resolveListHeaderDrop({
      dragged: mkIssue("d", {
        sort_order: 512,
        epics: [{ id: "e-1", name: "E1", archived_at: null }],
      }),
      sourceEpicId: "e-1",
      target: { kind: "epic", epicId: "e-2" },
      targetRows,
    });
    expect(plan).toEqual({
      type: "epic-move",
      addEpicId: "e-2",
      removeEpicId: "e-1",
      sortOrder: 3072,
    });
  });

  it("epic header: no remove when the appearance came from Ungrouped", () => {
    const plan = resolveListHeaderDrop({
      dragged: mkIssue("d", { sort_order: 512 }),
      sourceEpicId: null,
      target: { kind: "epic", epicId: "e-2" },
      targetRows,
    });
    expect(plan).toEqual({
      type: "epic-move",
      addEpicId: "e-2",
      removeEpicId: null,
      sortOrder: 3072,
    });
  });

  it("epic header: already a member is a silent no-op, even from another epic group", () => {
    expect(
      resolveListHeaderDrop({
        dragged: mkIssue("d", {
          epics: [
            { id: "e-1", name: "E1", archived_at: null },
            { id: "e-2", name: "E2", archived_at: null },
          ],
        }),
        sourceEpicId: "e-1",
        target: { kind: "epic", epicId: "e-2" },
        targetRows,
      }),
    ).toBeNull();
  });

  it("epic header: an archived-only membership does not count as member", () => {
    const plan = resolveListHeaderDrop({
      dragged: mkIssue("d", {
        sort_order: 512,
        epics: [{ id: "e-2", name: "E2", archived_at: "2026-01-01" }],
      }),
      sourceEpicId: null,
      target: { kind: "epic", epicId: "e-2" },
      targetRows,
    });
    expect(plan).not.toBeNull();
    expect(plan).toEqual({
      type: "epic-move",
      addEpicId: "e-2",
      removeEpicId: null,
      sortOrder: 3072,
    });
  });

  it("epic Ungrouped: removes ALL live memberships, no sort PATCH", () => {
    const plan = resolveListHeaderDrop({
      dragged: mkIssue("d", {
        epics: [
          { id: "e-1", name: "E1", archived_at: null },
          { id: "e-2", name: "E2", archived_at: null },
          { id: "e-3", name: "E3", archived_at: "2026-01-01" },
        ],
      }),
      sourceEpicId: "e-1",
      target: { kind: "epic-ungrouped" },
      targetRows,
    });
    expect(plan).toEqual({
      type: "epic-ungroup",
      removeEpicIds: ["e-1", "e-2"],
    });
  });

  it("epic Ungrouped: already-ungrouped is a silent no-op", () => {
    expect(
      resolveListHeaderDrop({
        dragged: mkIssue("d", {}),
        sourceEpicId: null,
        target: { kind: "epic-ungrouped" },
        targetRows,
      }),
    ).toBeNull();
  });

  it("epic Ungrouped: archived-only memberships count as already ungrouped", () => {
    expect(
      resolveListHeaderDrop({
        dragged: mkIssue("d", {
          epics: [{ id: "e-1", name: "E1", archived_at: "2026-01-01" }],
        }),
        sourceEpicId: null,
        target: { kind: "epic-ungrouped" },
        targetRows,
      }),
    ).toBeNull();
  });
});

describe("executeListHeaderDrop", () => {
  function mockDeps() {
    const events: string[] = [];
    return {
      events,
      deps: {
        patch: vi.fn((_id: string, body: unknown) => {
          events.push(`patch:${JSON.stringify(body)}`);
          return Promise.resolve();
        }),
        addToEpic: vi.fn((epicId: string) => {
          events.push(`add:${epicId}`);
          return Promise.resolve();
        }),
        removeFromEpic: vi.fn((epicId: string) => {
          events.push(`remove:${epicId}`);
          return Promise.resolve();
        }),
        invalidate: vi.fn(() => {
          events.push("invalidate");
          return Promise.resolve();
        }),
        onError: vi.fn((m: string) => {
          events.push(`error:${m}`);
        }),
      },
    };
  }

  it("patch plan: exactly one PATCH, then invalidate", async () => {
    const { events, deps } = mockDeps();
    const plan: ListHeaderDropPlan = {
      type: "patch",
      body: { state_id: "s-doing", sort_order: 3072 },
    };
    await executeListHeaderDrop("d", plan, deps);
    expect(events).toEqual([
      'patch:{"state_id":"s-doing","sort_order":3072}',
      "invalidate",
    ]);
    expect(deps.addToEpic).not.toHaveBeenCalled();
    expect(deps.removeFromEpic).not.toHaveBeenCalled();
  });

  it("epic-move: add, then remove, then sort PATCH, then invalidate", async () => {
    const { events, deps } = mockDeps();
    await executeListHeaderDrop(
      "d",
      { type: "epic-move", addEpicId: "e-2", removeEpicId: "e-1", sortOrder: 3072 },
      deps,
    );
    expect(events).toEqual([
      "add:e-2",
      "remove:e-1",
      'patch:{"sort_order":3072}',
      "invalidate",
    ]);
  });

  it("epic-move without a remove: add then sort PATCH", async () => {
    const { events, deps } = mockDeps();
    await executeListHeaderDrop(
      "d",
      { type: "epic-move", addEpicId: "e-2", removeEpicId: null, sortOrder: 3072 },
      deps,
    );
    expect(events).toEqual(["add:e-2", 'patch:{"sort_order":3072}', "invalidate"]);
  });

  it("epic-move: an add failure aborts remove and PATCH, surfaces honestly", async () => {
    const { events, deps } = mockDeps();
    deps.addToEpic.mockRejectedValueOnce(new Error("add boom"));
    await executeListHeaderDrop(
      "d",
      { type: "epic-move", addEpicId: "e-2", removeEpicId: "e-1", sortOrder: 3072 },
      deps,
    );
    expect(events).toEqual(["error:add boom", "invalidate"]);
    expect(deps.removeFromEpic).not.toHaveBeenCalled();
    expect(deps.patch).not.toHaveBeenCalled();
  });

  it("epic-move: a remove failure after a successful add aborts the PATCH and surfaces honestly", async () => {
    const { events, deps } = mockDeps();
    deps.removeFromEpic.mockRejectedValueOnce(new Error("remove boom"));
    await executeListHeaderDrop(
      "d",
      { type: "epic-move", addEpicId: "e-2", removeEpicId: "e-1", sortOrder: 3072 },
      deps,
    );
    expect(events).toEqual(["add:e-2", "error:remove boom", "invalidate"]);
    expect(deps.patch).not.toHaveBeenCalled();
  });

  it("epic-ungroup: sequential removes in order, no PATCH", async () => {
    const { events, deps } = mockDeps();
    await executeListHeaderDrop(
      "d",
      { type: "epic-ungroup", removeEpicIds: ["e-1", "e-2"] },
      deps,
    );
    expect(events).toEqual(["remove:e-1", "remove:e-2", "invalidate"]);
    expect(deps.patch).not.toHaveBeenCalled();
  });

  it("epic-ungroup: a failed remove aborts the rest and surfaces honestly", async () => {
    const { events, deps } = mockDeps();
    deps.removeFromEpic.mockRejectedValueOnce(new Error("remove boom"));
    await executeListHeaderDrop(
      "d",
      { type: "epic-ungroup", removeEpicIds: ["e-1", "e-2", "e-3"] },
      deps,
    );
    expect(events).toEqual(["error:remove boom", "invalidate"]);
    expect(deps.removeFromEpic).toHaveBeenCalledTimes(1);
  });
});

describe("dispatchListDragEnd", () => {
  const stackA = mkStack("s-todo", [mkIssue("a"), mkIssue("b")]);
  const stackB = mkStack("s-doing", [mkIssue("c")]);
  const sortableMap = new Map<string, ListDragRowRef>([
    ["row:s-todo:a", mkRef("a", mkIssue("a"), stackA)],
    ["row:s-todo:b", mkRef("b", mkIssue("b"), stackA)],
    ["row:s-doing:c", mkRef("c", mkIssue("c"), stackB)],
  ]);
  const headerMap = new Map<string, ListHeaderDropInfo>([
    [
      "header:s-doing:state:s-doing",
      { target: { kind: "state", stateId: "s-doing" }, targetRows: stackB.rows },
    ],
  ]);
  const base = {
    sortableMap,
    headerMap,
    onRowDrop: vi.fn(),
    onHeaderDrop: vi.fn(),
  };

  it("same-stack row drop routes to the reorder pipeline with row ids", () => {
    const onRowDrop = vi.fn();
    dispatchListDragEnd({
      ...base,
      activeSortableId: "row:s-todo:a",
      overId: "row:s-todo:b",
      reorderEnabled: true,
      onRowDrop,
      onHeaderDrop: vi.fn(),
    });
    expect(onRowDrop).toHaveBeenCalledWith(stackA.rows, "a", "b");
  });

  it("cross-stack row drop is a silent no-op, even with reorder enabled", () => {
    const onRowDrop = vi.fn();
    const onHeaderDrop = vi.fn();
    dispatchListDragEnd({
      ...base,
      activeSortableId: "row:s-todo:a",
      overId: "row:s-doing:c",
      reorderEnabled: true,
      onRowDrop,
      onHeaderDrop,
    });
    expect(onRowDrop).not.toHaveBeenCalled();
    expect(onHeaderDrop).not.toHaveBeenCalled();
  });

  it("row drop with reorder disabled is a silent no-op (priority/epic grouping)", () => {
    const onRowDrop = vi.fn();
    dispatchListDragEnd({
      ...base,
      activeSortableId: "row:s-todo:a",
      overId: "row:s-todo:b",
      reorderEnabled: false,
      onRowDrop,
      onHeaderDrop: vi.fn(),
    });
    expect(onRowDrop).not.toHaveBeenCalled();
  });

  it("header drop routes to the reassignment pipeline, regardless of the reorder gate", () => {
    const onHeaderDrop = vi.fn();
    dispatchListDragEnd({
      ...base,
      activeSortableId: "row:s-todo:a",
      overId: "header:s-doing:state:s-doing",
      reorderEnabled: false,
      onRowDrop: vi.fn(),
      onHeaderDrop,
    });
    expect(onHeaderDrop).toHaveBeenCalledTimes(1);
    expect(onHeaderDrop.mock.calls[0][0].rowId).toBe("a");
    expect(onHeaderDrop.mock.calls[0][1].target).toEqual({
      kind: "state",
      stateId: "s-doing",
    });
  });

  it("null over, same id, and unknown ids are silent no-ops", () => {
    const onRowDrop = vi.fn();
    const onHeaderDrop = vi.fn();
    const args = {
      ...base,
      reorderEnabled: true,
      onRowDrop,
      onHeaderDrop,
    };
    dispatchListDragEnd({ ...args, activeSortableId: "row:s-todo:a", overId: null });
    dispatchListDragEnd({ ...args, activeSortableId: "row:s-todo:a", overId: "row:s-todo:a" });
    dispatchListDragEnd({ ...args, activeSortableId: "row:s-todo:a", overId: "row:nope:x" });
    dispatchListDragEnd({ ...args, activeSortableId: "row:nope:x", overId: "row:s-todo:b" });
    expect(onRowDrop).not.toHaveBeenCalled();
    expect(onHeaderDrop).not.toHaveBeenCalled();
  });
});
