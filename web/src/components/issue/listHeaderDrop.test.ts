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
  sourceAssigneeId: string | null = null,
  sourceLabelId: string | null = null,
  sourceModuleId: string | null = null,
  sourceCycleId: string | null = null,
  sourceReleaseId: string | null = null,
): ListDragStackRef {
  return {
    stackKey,
    rows,
    sourceEpicId,
    sourceAssigneeId,
    sourceLabelId,
    sourceModuleId,
    sourceCycleId,
    sourceReleaseId,
  };
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
        addToAssignee: vi.fn((assigneeId: string) => {
          events.push(`addAssignee:${assigneeId}`);
          return Promise.resolve();
        }),
        removeFromAssignee: vi.fn((assigneeId: string) => {
          events.push(`removeAssignee:${assigneeId}`);
          return Promise.resolve();
        }),
        addToLabel: vi.fn((labelId: string) => {
          events.push(`addLabel:${labelId}`);
          return Promise.resolve();
        }),
        removeFromLabel: vi.fn((labelId: string) => {
          events.push(`removeLabel:${labelId}`);
          return Promise.resolve();
        }),
        addToModule: vi.fn((moduleId: string) => {
          events.push(`addModule:${moduleId}`);
          return Promise.resolve();
        }),
        removeFromModule: vi.fn((moduleId: string) => {
          events.push(`removeModule:${moduleId}`);
          return Promise.resolve();
        }),
        addToCycle: vi.fn((cycleId: string) => {
          events.push(`addCycle:${cycleId}`);
          return Promise.resolve();
        }),
        removeFromCycle: vi.fn((cycleId: string) => {
          events.push(`removeCycle:${cycleId}`);
          return Promise.resolve();
        }),
        addToRelease: vi.fn((releaseId: string) => {
          events.push(`addRelease:${releaseId}`);
          return Promise.resolve();
        }),
        removeFromRelease: vi.fn((releaseId: string) => {
          events.push(`removeRelease:${releaseId}`);
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

// C129T0: assignee header drops — the epic pipeline mirrored exactly
// (add-before-remove + sort PATCH; Ungrouped = remove-all, no PATCH).
describe("assignee header drops (C129T0)", () => {
  const targetRows = [
    mkIssue("t1", { sort_order: 1024 }),
    mkIssue("t2", { sort_order: 2048 }),
  ];

  // Local copy of the execute describe's mock factory (that one is
  // scoped to its own describe block).
  function mockAssigneeDeps() {
    const events: string[] = [];
    return {
      events,
      deps: {
        patch: vi.fn((_id: string, body: unknown) => {
          events.push(`patch:${JSON.stringify(body)}`);
          return Promise.resolve();
        }),
        addToEpic: vi.fn(() => Promise.resolve()),
        removeFromEpic: vi.fn(() => Promise.resolve()),
        addToAssignee: vi.fn((assigneeId: string) => {
          events.push(`addAssignee:${assigneeId}`);
          return Promise.resolve();
        }),
        removeFromAssignee: vi.fn((assigneeId: string) => {
          events.push(`removeAssignee:${assigneeId}`);
          return Promise.resolve();
        }),
        addToLabel: vi.fn((labelId: string) => {
          events.push(`addLabel:${labelId}`);
          return Promise.resolve();
        }),
        removeFromLabel: vi.fn((labelId: string) => {
          events.push(`removeLabel:${labelId}`);
          return Promise.resolve();
        }),
        addToModule: vi.fn(() => Promise.resolve()),
        removeFromModule: vi.fn(() => Promise.resolve()),
        invalidate: vi.fn(() => {
          events.push("invalidate");
          return Promise.resolve();
        }),
        addToCycle: vi.fn((cycleId: string) => {
          events.push(`addCycle:${cycleId}`);
          return Promise.resolve();
        }),
        removeFromCycle: vi.fn((cycleId: string) => {
          events.push(`removeCycle:${cycleId}`);
          return Promise.resolve();
        }),
        addToRelease: vi.fn((releaseId: string) => {
          events.push(`addRelease:${releaseId}`);
          return Promise.resolve();
        }),
        removeFromRelease: vi.fn((releaseId: string) => {
          events.push(`removeRelease:${releaseId}`);
          return Promise.resolve();
        }),
        onError: vi.fn((m: string) => {
          events.push(`error:${m}`);
        }),
      },
    };
  }

  it("toHeaderTarget maps assignee fields", () => {
    expect(toHeaderTarget({ targetAssigneeId: "u-1" })).toEqual({
      kind: "assignee",
      assigneeId: "u-1",
    });
    expect(toHeaderTarget({ isUngroupedAssignee: true })).toEqual({
      kind: "assignee-ungrouped",
    });
  });

  it("header ids are namespaced and disjoint from row ids", () => {
    const h = headerDropId("u-1", { kind: "assignee", assigneeId: "u-2" });
    const u = headerDropId("assignee:ungrouped", {
      kind: "assignee-ungrouped",
    });
    expect(h).toBe("header:u-1:assignee:u-2");
    expect(u).toBe("header:assignee:ungrouped:assignee:ungrouped");
    expect(h.startsWith("header:")).toBe(true);
    expect(listSortableId("u-1", "i-1")).not.toBe(h);
  });

  it("assignee header: add-before-remove + sort PATCH, from an assignee source", () => {
    const plan = resolveListHeaderDrop({
      dragged: mkIssue("d", {
        sort_order: 512,
        assignees: [{ id: "u-1", name: "Alice" }],
      }),
      sourceEpicId: null,
      sourceAssigneeId: "u-1",
      target: { kind: "assignee", assigneeId: "u-2" },
      targetRows,
    });
    expect(plan).toEqual({
      type: "assignee-move",
      addAssigneeId: "u-2",
      removeAssigneeId: "u-1",
      sortOrder: 3072,
    });
  });

  it("assignee header: no remove when the appearance came from Ungrouped", () => {
    const plan = resolveListHeaderDrop({
      dragged: mkIssue("d", { sort_order: 512 }),
      sourceEpicId: null,
      sourceAssigneeId: null,
      target: { kind: "assignee", assigneeId: "u-2" },
      targetRows,
    });
    expect(plan).toEqual({
      type: "assignee-move",
      addAssigneeId: "u-2",
      removeAssigneeId: null,
      sortOrder: 3072,
    });
  });

  it("assignee header: no remove when the source equals the target (own-group appearance)", () => {
    const plan = resolveListHeaderDrop({
      dragged: mkIssue("d", {
        sort_order: 512,
        assignees: [{ id: "u-2", name: "Bob" }],
      }),
      sourceEpicId: null,
      sourceAssigneeId: "u-2",
      target: { kind: "assignee", assigneeId: "u-2" },
      targetRows,
    });
    // Already a member of the target: silent no-op, not a move.
    expect(plan).toBeNull();
  });

  it("assignee header: already assigned to the target is a silent no-op, even from another assignee group", () => {
    expect(
      resolveListHeaderDrop({
        dragged: mkIssue("d", {
          assignees: [
            { id: "u-1", name: "Alice" },
            { id: "u-2", name: "Bob" },
          ],
        }),
        sourceEpicId: null,
        sourceAssigneeId: "u-1",
        target: { kind: "assignee", assigneeId: "u-2" },
        targetRows,
      }),
    ).toBeNull();
  });

  it("assignee Ungrouped: removes ALL current assignees, no sort PATCH", () => {
    const plan = resolveListHeaderDrop({
      dragged: mkIssue("d", {
        assignees: [
          { id: "u-1", name: "Alice" },
          { id: "u-2", name: "Bob" },
        ],
      }),
      sourceEpicId: null,
      sourceAssigneeId: "u-1",
      target: { kind: "assignee-ungrouped" },
      targetRows,
    });
    expect(plan).toEqual({
      type: "assignee-ungroup",
      removeAssigneeIds: ["u-1", "u-2"],
    });
  });

  it("assignee Ungrouped: already-unassigned is a silent no-op", () => {
    expect(
      resolveListHeaderDrop({
        dragged: mkIssue("d", {}),
        sourceEpicId: null,
        sourceAssigneeId: null,
        target: { kind: "assignee-ungrouped" },
        targetRows,
      }),
    ).toBeNull();
  });

  it("execute assignee-move: add, then remove, then PATCH {sort_order} only, then invalidate", async () => {
    const { events, deps } = mockAssigneeDeps();
    await executeListHeaderDrop(
      "d",
      {
        type: "assignee-move",
        addAssigneeId: "u-2",
        removeAssigneeId: "u-1",
        sortOrder: 3072,
      },
      deps,
    );
    expect(events).toEqual([
      "addAssignee:u-2",
      "removeAssignee:u-1",
      'patch:{"sort_order":3072}',
      "invalidate",
    ]);
    // The sort PATCH body carries exactly {sort_order} — never an assignee id.
    expect(deps.patch).toHaveBeenCalledWith("d", { sort_order: 3072 });
  });

  it("execute assignee-move: an add failure aborts remove and PATCH, surfaces honestly", async () => {
    const { events, deps } = mockAssigneeDeps();
    deps.addToAssignee.mockRejectedValueOnce(new Error("add boom"));
    await executeListHeaderDrop(
      "d",
      {
        type: "assignee-move",
        addAssigneeId: "u-2",
        removeAssigneeId: "u-1",
        sortOrder: 3072,
      },
      deps,
    );
    expect(events).toEqual(["error:add boom", "invalidate"]);
    expect(deps.removeFromAssignee).not.toHaveBeenCalled();
    expect(deps.patch).not.toHaveBeenCalled();
  });

  it("execute assignee-move: a remove failure after a successful add aborts the PATCH and surfaces honestly", async () => {
    const { events, deps } = mockAssigneeDeps();
    deps.removeFromAssignee.mockRejectedValueOnce(new Error("remove boom"));
    await executeListHeaderDrop(
      "d",
      {
        type: "assignee-move",
        addAssigneeId: "u-2",
        removeAssigneeId: "u-1",
        sortOrder: 3072,
      },
      deps,
    );
    expect(events).toEqual([
      "addAssignee:u-2",
      "error:remove boom",
      "invalidate",
    ]);
    expect(deps.patch).not.toHaveBeenCalled();
  });

  it("execute assignee-ungroup: sequential removes in order, no PATCH", async () => {
    const { events, deps } = mockAssigneeDeps();
    await executeListHeaderDrop(
      "d",
      { type: "assignee-ungroup", removeAssigneeIds: ["u-1", "u-2"] },
      deps,
    );
    expect(events).toEqual([
      "removeAssignee:u-1",
      "removeAssignee:u-2",
      "invalidate",
    ]);
    expect(deps.patch).not.toHaveBeenCalled();
  });

  it("execute assignee-ungroup: a failed remove aborts the rest and surfaces honestly", async () => {
    const { events, deps } = mockAssigneeDeps();
    deps.removeFromAssignee.mockRejectedValueOnce(new Error("remove boom"));
    await executeListHeaderDrop(
      "d",
      { type: "assignee-ungroup", removeAssigneeIds: ["u-1", "u-2", "u-3"] },
      deps,
    );
    expect(events).toEqual(["error:remove boom", "invalidate"]);
    expect(deps.removeFromAssignee).toHaveBeenCalledTimes(1);
  });

  it("dispatch routes an assignee header drop to the reassignment pipeline regardless of the reorder gate", () => {
    const stack = mkStack("u-1", [mkIssue("a")], null, "u-1");
    const sortableMap = new Map<string, ListDragRowRef>([
      ["row:u-1:a", mkRef("a", mkIssue("a"), stack)],
    ]);
    const headerMap = new Map<string, ListHeaderDropInfo>([
      [
        "header:u-2:assignee:u-2",
        {
          target: { kind: "assignee", assigneeId: "u-2" },
          targetRows: [],
        },
      ],
    ]);
    const onHeaderDrop = vi.fn();
    dispatchListDragEnd({
      activeSortableId: "row:u-1:a",
      overId: "header:u-2:assignee:u-2",
      sortableMap,
      headerMap,
      reorderEnabled: false,
      onRowDrop: vi.fn(),
      onHeaderDrop,
    });
    expect(onHeaderDrop).toHaveBeenCalledTimes(1);
    expect(onHeaderDrop.mock.calls[0][0].stack.sourceAssigneeId).toBe("u-1");
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

  it("row drop with reorder disabled is a silent no-op (non-Manual order / PQL mode)", () => {
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

  it("C128T0 epic multi-appearance: dragging one appearance reorders within its own stack only", () => {
    // Issue "m" is a member of two epics, so it appears once in
    // each epic stack under distinct stack-scoped sortable ids.
    // Dispatch reads the appearance → issue map, never parses an
    // id: the epic-1 appearance routes to the epic-1 stack's rows
    // with plain row ids, and the same appearance over an epic-2
    // row is a cross-stack no-op.
    const epic1 = mkStack("epic:e1", [mkIssue("a"), mkIssue("m")], "e1");
    const epic2 = mkStack("epic:e2", [mkIssue("m"), mkIssue("z")], "e2");
    const maps = new Map<string, ListDragRowRef>([
      ["row:epic:e1:a", mkRef("a", mkIssue("a"), epic1)],
      ["row:epic:e1:m", mkRef("m", mkIssue("m"), epic1)],
      ["row:epic:e2:m", mkRef("m", mkIssue("m"), epic2)],
      ["row:epic:e2:z", mkRef("z", mkIssue("z"), epic2)],
    ]);
    const onRowDrop = vi.fn();
    dispatchListDragEnd({
      sortableMap: maps,
      headerMap: new Map(),
      activeSortableId: "row:epic:e1:m",
      overId: "row:epic:e1:a",
      reorderEnabled: true,
      onRowDrop,
      onHeaderDrop: vi.fn(),
    });
    expect(onRowDrop).toHaveBeenCalledWith(epic1.rows, "m", "a");
    onRowDrop.mockClear();
    dispatchListDragEnd({
      sortableMap: maps,
      headerMap: new Map(),
      activeSortableId: "row:epic:e1:m",
      overId: "row:epic:e2:z",
      reorderEnabled: true,
      onRowDrop,
      onHeaderDrop: vi.fn(),
    });
    expect(onRowDrop).not.toHaveBeenCalled();
  });

  it("C128T0: same-stack row drop routes to reorder when the gate admits priority/epic grouping", () => {
    // The gate itself lives in listReorder.isListDragEnabled; dispatch
    // only consumes its boolean, so this pins the contract: gate true
    // under priority/epic grouping → row drops reach the pipeline.
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

// C130T0: label header drops — the assignee pipeline mirrored exactly
// (add-before-remove + sort PATCH; Ungrouped = remove-all, no PATCH).
describe("label header drops (C130T0)", () => {
  const targetRows = [
    mkIssue("t1", { sort_order: 1024 }),
    mkIssue("t2", { sort_order: 2048 }),
  ];

  // Local copy of the execute describe's mock factory (that one is
  // scoped to its own describe block).
  function mockLabelDeps() {
    const events: string[] = [];
    return {
      events,
      deps: {
        patch: vi.fn((_id: string, body: unknown) => {
          events.push(`patch:${JSON.stringify(body)}`);
          return Promise.resolve();
        }),
        addToEpic: vi.fn(() => Promise.resolve()),
        removeFromEpic: vi.fn(() => Promise.resolve()),
        addToAssignee: vi.fn(() => Promise.resolve()),
        removeFromAssignee: vi.fn(() => Promise.resolve()),
        addToLabel: vi.fn((labelId: string) => {
          events.push(`addLabel:${labelId}`);
          return Promise.resolve();
        }),
        removeFromLabel: vi.fn((labelId: string) => {
          events.push(`removeLabel:${labelId}`);
          return Promise.resolve();
        }),
        addToModule: vi.fn(() => Promise.resolve()),
        removeFromModule: vi.fn(() => Promise.resolve()),
        invalidate: vi.fn(() => {
          events.push("invalidate");
          return Promise.resolve();
        }),
        addToCycle: vi.fn((cycleId: string) => {
          events.push(`addCycle:${cycleId}`);
          return Promise.resolve();
        }),
        removeFromCycle: vi.fn((cycleId: string) => {
          events.push(`removeCycle:${cycleId}`);
          return Promise.resolve();
        }),
        addToRelease: vi.fn((releaseId: string) => {
          events.push(`addRelease:${releaseId}`);
          return Promise.resolve();
        }),
        removeFromRelease: vi.fn((releaseId: string) => {
          events.push(`removeRelease:${releaseId}`);
          return Promise.resolve();
        }),
        onError: vi.fn((m: string) => {
          events.push(`error:${m}`);
        }),
      },
    };
  }

  it("toHeaderTarget maps label fields", () => {
    expect(toHeaderTarget({ targetLabelId: "l-1" })).toEqual({
      kind: "label",
      labelId: "l-1",
    });
    expect(toHeaderTarget({ isUngroupedLabel: true })).toEqual({
      kind: "label-ungrouped",
    });
  });

  it("header ids are namespaced and disjoint from row ids", () => {
    const h = headerDropId("l-1", { kind: "label", labelId: "l-2" });
    const u = headerDropId("label:ungrouped", { kind: "label-ungrouped" });
    expect(h).toBe("header:l-1:label:l-2");
    expect(u).toBe("header:label:ungrouped:label:ungrouped");
    expect(h.startsWith("header:")).toBe(true);
    expect(listSortableId("l-1", "i-1")).not.toBe(h);
  });

  it("label header: add-before-remove + sort PATCH, from a label source", () => {
    const plan = resolveListHeaderDrop({
      dragged: mkIssue("d", {
        sort_order: 512,
        labels: [{ id: "l-1", name: "Bug", color: "#e22718" }],
      }),
      sourceEpicId: null,
      sourceAssigneeId: null,
      sourceLabelId: "l-1",
      target: { kind: "label", labelId: "l-2" },
      targetRows,
    });
    expect(plan).toEqual({
      type: "label-move",
      addLabelId: "l-2",
      removeLabelId: "l-1",
      sortOrder: 3072,
    });
  });

  it("label header: no remove when the appearance came from Ungrouped", () => {
    const plan = resolveListHeaderDrop({
      dragged: mkIssue("d", { sort_order: 512 }),
      sourceEpicId: null,
      sourceAssigneeId: null,
      sourceLabelId: null,
      target: { kind: "label", labelId: "l-2" },
      targetRows,
    });
    expect(plan).toEqual({
      type: "label-move",
      addLabelId: "l-2",
      removeLabelId: null,
      sortOrder: 3072,
    });
  });

  it("label header: no remove when the source equals the target (own-group appearance)", () => {
    const plan = resolveListHeaderDrop({
      dragged: mkIssue("d", {
        sort_order: 512,
        labels: [{ id: "l-2", name: "Feature", color: "#0066b1" }],
      }),
      sourceEpicId: null,
      sourceAssigneeId: null,
      sourceLabelId: "l-2",
      target: { kind: "label", labelId: "l-2" },
      targetRows,
    });
    // Already a member of the target: silent no-op, not a move.
    expect(plan).toBeNull();
  });

  it("label header: already labeled with the target is a silent no-op, even from another label group", () => {
    expect(
      resolveListHeaderDrop({
        dragged: mkIssue("d", {
          labels: [
            { id: "l-1", name: "Bug", color: "#e22718" },
            { id: "l-2", name: "Feature", color: "#0066b1" },
          ],
        }),
        sourceEpicId: null,
        sourceAssigneeId: null,
        sourceLabelId: "l-1",
        target: { kind: "label", labelId: "l-2" },
        targetRows,
      }),
    ).toBeNull();
  });

  it("label Ungrouped: removes ALL current labels, no sort PATCH", () => {
    const plan = resolveListHeaderDrop({
      dragged: mkIssue("d", {
        labels: [
          { id: "l-1", name: "Bug", color: "#e22718" },
          { id: "l-2", name: "Feature", color: "#0066b1" },
        ],
      }),
      sourceEpicId: null,
      sourceAssigneeId: null,
      sourceLabelId: "l-1",
      target: { kind: "label-ungrouped" },
      targetRows,
    });
    expect(plan).toEqual({
      type: "label-ungroup",
      removeLabelIds: ["l-1", "l-2"],
    });
  });

  it("label Ungrouped: already-unlabeled is a silent no-op", () => {
    expect(
      resolveListHeaderDrop({
        dragged: mkIssue("d", {}),
        sourceEpicId: null,
        sourceAssigneeId: null,
        sourceLabelId: null,
        target: { kind: "label-ungrouped" },
        targetRows,
      }),
    ).toBeNull();
  });

  it("execute label-move: add, then remove, then PATCH {sort_order} only, then invalidate", async () => {
    const { events, deps } = mockLabelDeps();
    await executeListHeaderDrop(
      "d",
      {
        type: "label-move",
        addLabelId: "l-2",
        removeLabelId: "l-1",
        sortOrder: 3072,
      },
      deps,
    );
    expect(events).toEqual([
      "addLabel:l-2",
      "removeLabel:l-1",
      'patch:{"sort_order":3072}',
      "invalidate",
    ]);
    // The sort PATCH body carries exactly {sort_order} — never a label id.
    expect(deps.patch).toHaveBeenCalledWith("d", { sort_order: 3072 });
  });

  it("execute label-move: an add failure aborts remove and PATCH, surfaces honestly", async () => {
    const { events, deps } = mockLabelDeps();
    deps.addToLabel.mockRejectedValueOnce(new Error("add boom"));
    await executeListHeaderDrop(
      "d",
      {
        type: "label-move",
        addLabelId: "l-2",
        removeLabelId: "l-1",
        sortOrder: 3072,
      },
      deps,
    );
    expect(events).toEqual(["error:add boom", "invalidate"]);
    expect(deps.removeFromLabel).not.toHaveBeenCalled();
    expect(deps.patch).not.toHaveBeenCalled();
  });

  it("execute label-move: a remove failure after a successful add aborts the PATCH and surfaces honestly", async () => {
    const { events, deps } = mockLabelDeps();
    deps.removeFromLabel.mockRejectedValueOnce(new Error("remove boom"));
    await executeListHeaderDrop(
      "d",
      {
        type: "label-move",
        addLabelId: "l-2",
        removeLabelId: "l-1",
        sortOrder: 3072,
      },
      deps,
    );
    expect(events).toEqual([
      "addLabel:l-2",
      "error:remove boom",
      "invalidate",
    ]);
    expect(deps.patch).not.toHaveBeenCalled();
  });

  it("execute label-ungroup: sequential removes in order, no PATCH", async () => {
    const { events, deps } = mockLabelDeps();
    await executeListHeaderDrop(
      "d",
      { type: "label-ungroup", removeLabelIds: ["l-1", "l-2"] },
      deps,
    );
    expect(events).toEqual([
      "removeLabel:l-1",
      "removeLabel:l-2",
      "invalidate",
    ]);
    expect(deps.patch).not.toHaveBeenCalled();
  });

  it("execute label-ungroup: a failed remove aborts the rest and surfaces honestly", async () => {
    const { events, deps } = mockLabelDeps();
    deps.removeFromLabel.mockRejectedValueOnce(new Error("remove boom"));
    await executeListHeaderDrop(
      "d",
      { type: "label-ungroup", removeLabelIds: ["l-1", "l-2", "l-3"] },
      deps,
    );
    expect(events).toEqual(["error:remove boom", "invalidate"]);
    expect(deps.removeFromLabel).toHaveBeenCalledTimes(1);
  });

  it("dispatch routes a label header drop to the reassignment pipeline regardless of the reorder gate", () => {
    const stack = mkStack("l-1", [mkIssue("a")], null, null, "l-1");
    const sortableMap = new Map<string, ListDragRowRef>([
      ["row:l-1:a", mkRef("a", mkIssue("a"), stack)],
    ]);
    const headerMap = new Map<string, ListHeaderDropInfo>([
      [
        "header:l-2:label:l-2",
        {
          target: { kind: "label", labelId: "l-2" },
          targetRows: [],
        },
      ],
    ]);
    const onHeaderDrop = vi.fn();
    dispatchListDragEnd({
      activeSortableId: "row:l-1:a",
      overId: "header:l-2:label:l-2",
      sortableMap,
      headerMap,
      reorderEnabled: false,
      onRowDrop: vi.fn(),
      onHeaderDrop,
    });
    expect(onHeaderDrop).toHaveBeenCalledTimes(1);
    expect(onHeaderDrop.mock.calls[0][0].stack.sourceLabelId).toBe("l-1");
  });

  it("dispatch: label multi-appearance — one appearance reorders within its own stack only", () => {
    // Issue "m" carries two labels, so it appears once in each label
    // stack under distinct stack-scoped sortable ids. The label-1
    // appearance routes to the label-1 stack's rows, and the same
    // appearance over a label-2 row is a cross-stack no-op.
    const label1 = mkStack("label:l-1", [mkIssue("a"), mkIssue("m")], null, null, "l-1");
    const label2 = mkStack("label:l-2", [mkIssue("m"), mkIssue("z")], null, null, "l-2");
    const maps = new Map<string, ListDragRowRef>([
      ["row:label:l-1:a", mkRef("a", mkIssue("a"), label1)],
      ["row:label:l-1:m", mkRef("m", mkIssue("m"), label1)],
      ["row:label:l-2:m", mkRef("m", mkIssue("m"), label2)],
      ["row:label:l-2:z", mkRef("z", mkIssue("z"), label2)],
    ]);
    const onRowDrop = vi.fn();
    dispatchListDragEnd({
      sortableMap: maps,
      headerMap: new Map(),
      activeSortableId: "row:label:l-1:m",
      overId: "row:label:l-1:a",
      reorderEnabled: true,
      onRowDrop,
      onHeaderDrop: vi.fn(),
    });
    expect(onRowDrop).toHaveBeenCalledWith(label1.rows, "m", "a");
    onRowDrop.mockClear();
    dispatchListDragEnd({
      sortableMap: maps,
      headerMap: new Map(),
      activeSortableId: "row:label:l-1:m",
      overId: "row:label:l-2:z",
      reorderEnabled: true,
      onRowDrop,
      onHeaderDrop: vi.fn(),
    });
    expect(onRowDrop).not.toHaveBeenCalled();
  });
});

// C131T0: module header drops — the label pipeline mirrored exactly
// (add-before-remove + sort PATCH; Ungrouped = remove-all, no PATCH).
// Module membership rides project_structure.modules (NamedRef: id +
// name only, no color) on the row.
describe("module header drops (C131T0)", () => {
  const targetRows = [
    mkIssue("t1", { sort_order: 1024 }),
    mkIssue("t2", { sort_order: 2048 }),
  ];

  function mockModuleDeps() {
    const events: string[] = [];
    return {
      events,
      deps: {
        patch: vi.fn((_id: string, body: unknown) => {
          events.push(`patch:${JSON.stringify(body)}`);
          return Promise.resolve();
        }),
        addToEpic: vi.fn(() => Promise.resolve()),
        removeFromEpic: vi.fn(() => Promise.resolve()),
        addToAssignee: vi.fn(() => Promise.resolve()),
        removeFromAssignee: vi.fn(() => Promise.resolve()),
        addToLabel: vi.fn(() => Promise.resolve()),
        removeFromLabel: vi.fn(() => Promise.resolve()),
        addToModule: vi.fn((moduleId: string) => {
          events.push(`addModule:${moduleId}`);
          return Promise.resolve();
        }),
        removeFromModule: vi.fn((moduleId: string) => {
          events.push(`removeModule:${moduleId}`);
          return Promise.resolve();
        }),
        addToCycle: vi.fn((cycleId: string) => {
          events.push(`addCycle:${cycleId}`);
          return Promise.resolve();
        }),
        removeFromCycle: vi.fn((cycleId: string) => {
          events.push(`removeCycle:${cycleId}`);
          return Promise.resolve();
        }),
        addToRelease: vi.fn((releaseId: string) => {
          events.push(`addRelease:${releaseId}`);
          return Promise.resolve();
        }),
        removeFromRelease: vi.fn((releaseId: string) => {
          events.push(`removeRelease:${releaseId}`);
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

  const withModules = (id: string, modules: { id: string; name: string }[]) =>
    mkIssue(id, {
      sort_order: 512,
      project_structure: { modules },
    } as Partial<Issue>);

  it("toHeaderTarget maps module fields", () => {
    expect(toHeaderTarget({ targetModuleId: "m-1" })).toEqual({
      kind: "module",
      moduleId: "m-1",
    });
    expect(toHeaderTarget({ isUngroupedModule: true })).toEqual({
      kind: "module-ungrouped",
    });
  });

  it("header ids are namespaced and disjoint from row ids", () => {
    const h = headerDropId("m-1", { kind: "module", moduleId: "m-2" });
    const u = headerDropId("module:ungrouped", { kind: "module-ungrouped" });
    expect(h).toBe("header:m-1:module:m-2");
    expect(u).toBe("header:module:ungrouped:module:ungrouped");
    expect(h.startsWith("header:")).toBe(true);
    expect(listSortableId("m-1", "i-1")).not.toBe(h);
  });

  it("module header: add-before-remove + sort PATCH, from a module source", () => {
    const plan = resolveListHeaderDrop({
      dragged: withModules("d", [{ id: "m-1", name: "Core" }]),
      sourceEpicId: null,
      sourceAssigneeId: null,
      sourceLabelId: null,
      sourceModuleId: "m-1",
      target: { kind: "module", moduleId: "m-2" },
      targetRows,
    });
    expect(plan).toEqual({
      type: "module-move",
      addModuleId: "m-2",
      removeModuleId: "m-1",
      sortOrder: 3072,
    });
  });

  it("module header: no remove when the appearance came from Ungrouped", () => {
    const plan = resolveListHeaderDrop({
      dragged: mkIssue("d", { sort_order: 512 }),
      sourceEpicId: null,
      sourceAssigneeId: null,
      sourceLabelId: null,
      sourceModuleId: null,
      target: { kind: "module", moduleId: "m-2" },
      targetRows,
    });
    expect(plan).toEqual({
      type: "module-move",
      addModuleId: "m-2",
      removeModuleId: null,
      sortOrder: 3072,
    });
  });

  it("module header: no remove when the source equals the target (own-group appearance)", () => {
    const plan = resolveListHeaderDrop({
      dragged: withModules("d", [{ id: "m-2", name: "Web" }]),
      sourceEpicId: null,
      sourceAssigneeId: null,
      sourceLabelId: null,
      sourceModuleId: "m-2",
      target: { kind: "module", moduleId: "m-2" },
      targetRows,
    });
    // Already a member of the target: silent no-op, not a move.
    expect(plan).toBeNull();
  });

  it("module header: already a member of the target is a silent no-op, even from another module group", () => {
    expect(
      resolveListHeaderDrop({
        dragged: withModules("d", [
          { id: "m-1", name: "Core" },
          { id: "m-2", name: "Web" },
        ]),
        sourceEpicId: null,
        sourceAssigneeId: null,
        sourceLabelId: null,
        sourceModuleId: "m-1",
        target: { kind: "module", moduleId: "m-2" },
        targetRows,
      }),
    ).toBeNull();
  });

  it("module Ungrouped: removes ALL current modules, no sort PATCH", () => {
    const plan = resolveListHeaderDrop({
      dragged: withModules("d", [
        { id: "m-1", name: "Core" },
        { id: "m-2", name: "Web" },
      ]),
      sourceEpicId: null,
      sourceAssigneeId: null,
      sourceLabelId: null,
      sourceModuleId: "m-1",
      target: { kind: "module-ungrouped" },
      targetRows,
    });
    expect(plan).toEqual({
      type: "module-ungroup",
      removeModuleIds: ["m-1", "m-2"],
    });
  });

  it("module Ungrouped: already-moduleless is a silent no-op", () => {
    expect(
      resolveListHeaderDrop({
        dragged: mkIssue("d", {}),
        sourceEpicId: null,
        sourceAssigneeId: null,
        sourceLabelId: null,
        sourceModuleId: null,
        target: { kind: "module-ungrouped" },
        targetRows,
      }),
    ).toBeNull();
  });

  it("execute module-move: add, then remove, then PATCH {sort_order} only, then invalidate", async () => {
    const { events, deps } = mockModuleDeps();
    await executeListHeaderDrop(
      "d",
      {
        type: "module-move",
        addModuleId: "m-2",
        removeModuleId: "m-1",
        sortOrder: 3072,
      },
      deps,
    );
    expect(events).toEqual([
      "addModule:m-2",
      "removeModule:m-1",
      'patch:{"sort_order":3072}',
      "invalidate",
    ]);
    // The sort PATCH body carries exactly {sort_order} — never a module id.
    expect(deps.patch).toHaveBeenCalledWith("d", { sort_order: 3072 });
  });

  it("execute module-move: an add failure aborts remove and PATCH, surfaces honestly", async () => {
    const { events, deps } = mockModuleDeps();
    deps.addToModule.mockRejectedValueOnce(new Error("add boom"));
    await executeListHeaderDrop(
      "d",
      {
        type: "module-move",
        addModuleId: "m-2",
        removeModuleId: "m-1",
        sortOrder: 3072,
      },
      deps,
    );
    expect(events).toEqual(["error:add boom", "invalidate"]);
    expect(deps.removeFromModule).not.toHaveBeenCalled();
    expect(deps.patch).not.toHaveBeenCalled();
  });

  it("execute module-move: a remove failure after a successful add aborts the PATCH and surfaces honestly", async () => {
    const { events, deps } = mockModuleDeps();
    deps.removeFromModule.mockRejectedValueOnce(new Error("remove boom"));
    await executeListHeaderDrop(
      "d",
      {
        type: "module-move",
        addModuleId: "m-2",
        removeModuleId: "m-1",
        sortOrder: 3072,
      },
      deps,
    );
    expect(events).toEqual([
      "addModule:m-2",
      "error:remove boom",
      "invalidate",
    ]);
    expect(deps.patch).not.toHaveBeenCalled();
  });

  it("execute module-ungroup: sequential removes in order, no PATCH", async () => {
    const { events, deps } = mockModuleDeps();
    await executeListHeaderDrop(
      "d",
      { type: "module-ungroup", removeModuleIds: ["m-1", "m-2"] },
      deps,
    );
    expect(events).toEqual([
      "removeModule:m-1",
      "removeModule:m-2",
      "invalidate",
    ]);
    expect(deps.patch).not.toHaveBeenCalled();
  });

  it("execute module-ungroup: a failed remove aborts the rest and surfaces honestly", async () => {
    const { events, deps } = mockModuleDeps();
    deps.removeFromModule.mockRejectedValueOnce(new Error("remove boom"));
    await executeListHeaderDrop(
      "d",
      { type: "module-ungroup", removeModuleIds: ["m-1", "m-2", "m-3"] },
      deps,
    );
    expect(events).toEqual(["error:remove boom", "invalidate"]);
    expect(deps.removeFromModule).toHaveBeenCalledTimes(1);
  });

  it("dispatch routes a module header drop to the reassignment pipeline regardless of the reorder gate", () => {
    const stack = mkStack("m-1", [mkIssue("a")], null, null, null, "m-1");
    const sortableMap = new Map<string, ListDragRowRef>([
      ["row:m-1:a", mkRef("a", mkIssue("a"), stack)],
    ]);
    const headerMap = new Map<string, ListHeaderDropInfo>([
      [
        "header:m-2:module:m-2",
        {
          target: { kind: "module", moduleId: "m-2" },
          targetRows: [],
        },
      ],
    ]);
    const onHeaderDrop = vi.fn();
    dispatchListDragEnd({
      activeSortableId: "row:m-1:a",
      overId: "header:m-2:module:m-2",
      sortableMap,
      headerMap,
      reorderEnabled: false,
      onRowDrop: vi.fn(),
      onHeaderDrop,
    });
    expect(onHeaderDrop).toHaveBeenCalledTimes(1);
    expect(onHeaderDrop.mock.calls[0][0].stack.sourceModuleId).toBe("m-1");
  });

  it("dispatch: module multi-appearance — one appearance reorders within its own stack only", () => {
    const module1 = mkStack("module:m-1", [mkIssue("a"), mkIssue("m")], null, null, null, "m-1");
    const module2 = mkStack("module:m-2", [mkIssue("m"), mkIssue("z")], null, null, null, "m-2");
    const maps = new Map<string, ListDragRowRef>([
      ["row:module:m-1:a", mkRef("a", mkIssue("a"), module1)],
      ["row:module:m-1:m", mkRef("m", mkIssue("m"), module1)],
      ["row:module:m-2:m", mkRef("m", mkIssue("m"), module2)],
      ["row:module:m-2:z", mkRef("z", mkIssue("z"), module2)],
    ]);
    const onRowDrop = vi.fn();
    dispatchListDragEnd({
      sortableMap: maps,
      headerMap: new Map(),
      activeSortableId: "row:module:m-1:m",
      overId: "row:module:m-1:a",
      reorderEnabled: true,
      onRowDrop,
      onHeaderDrop: vi.fn(),
    });
    expect(onRowDrop).toHaveBeenCalledWith(module1.rows, "m", "a");
    onRowDrop.mockClear();
    dispatchListDragEnd({
      sortableMap: maps,
      headerMap: new Map(),
      activeSortableId: "row:module:m-1:m",
      overId: "row:module:m-2:z",
      reorderEnabled: true,
      onRowDrop,
      onHeaderDrop: vi.fn(),
    });
    expect(onRowDrop).not.toHaveBeenCalled();
  });
});

// C132T1: cycle header drops — the module pipeline mirrored exactly
// (add-before-remove + sort PATCH; Ungrouped = remove the displayed
// cycle only, no PATCH), adapted from multi-appearance to
// single-appearance: Issue.project_structure.cycle is NamedRef |
// null, so membership IS appearance and the own-group and
// already-member no-ops coincide.
describe("cycle header drops (C132T1)", () => {
  const targetRows = [
    mkIssue("t1", { sort_order: 1024 }),
    mkIssue("t2", { sort_order: 2048 }),
  ];

  function mockCycleDeps() {
    const events: string[] = [];
    return {
      events,
      deps: {
        patch: vi.fn((_id: string, body: unknown) => {
          events.push(`patch:${JSON.stringify(body)}`);
          return Promise.resolve();
        }),
        addToEpic: vi.fn(() => Promise.resolve()),
        removeFromEpic: vi.fn(() => Promise.resolve()),
        addToAssignee: vi.fn(() => Promise.resolve()),
        removeFromAssignee: vi.fn(() => Promise.resolve()),
        addToLabel: vi.fn(() => Promise.resolve()),
        removeFromLabel: vi.fn(() => Promise.resolve()),
        addToModule: vi.fn(() => Promise.resolve()),
        removeFromModule: vi.fn(() => Promise.resolve()),
        addToCycle: vi.fn((cycleId: string) => {
          events.push(`addCycle:${cycleId}`);
          return Promise.resolve();
        }),
        removeFromCycle: vi.fn((cycleId: string) => {
          events.push(`removeCycle:${cycleId}`);
          return Promise.resolve();
        }),
        addToRelease: vi.fn((releaseId: string) => {
          events.push(`addRelease:${releaseId}`);
          return Promise.resolve();
        }),
        removeFromRelease: vi.fn((releaseId: string) => {
          events.push(`removeRelease:${releaseId}`);
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

  const withCycle = (id: string, cycle: { id: string; name: string }) =>
    mkIssue(id, {
      sort_order: 512,
      project_structure: { cycle },
    } as Partial<Issue>);

  it("toHeaderTarget maps cycle fields", () => {
    expect(toHeaderTarget({ targetCycleId: "c-1" })).toEqual({
      kind: "cycle",
      cycleId: "c-1",
    });
    expect(toHeaderTarget({ isUngroupedCycle: true })).toEqual({
      kind: "cycle-ungrouped",
    });
  });

  it("header ids are namespaced and disjoint from row ids", () => {
    const h = headerDropId("c-1", { kind: "cycle", cycleId: "c-2" });
    const u = headerDropId("cycle:ungrouped", { kind: "cycle-ungrouped" });
    expect(h).toBe("header:c-1:cycle:c-2");
    expect(u).toBe("header:cycle:ungrouped:cycle:ungrouped");
    expect(h.startsWith("header:")).toBe(true);
    expect(listSortableId("c-1", "i-1")).not.toBe(h);
  });

  it("cycle header: add-before-remove + sort PATCH, from a cycle source", () => {
    const plan = resolveListHeaderDrop({
      dragged: withCycle("d", { id: "c-1", name: "Sprint 1" }),
      sourceEpicId: null,
      sourceAssigneeId: null,
      sourceLabelId: null,
      sourceModuleId: null,
      sourceCycleId: "c-1",
      target: { kind: "cycle", cycleId: "c-2" },
      targetRows,
    });
    expect(plan).toEqual({
      type: "cycle-move",
      addCycleId: "c-2",
      removeCycleId: "c-1",
      sortOrder: 3072,
    });
  });

  it("cycle header: no remove when the appearance came from Ungrouped", () => {
    const plan = resolveListHeaderDrop({
      dragged: mkIssue("d", { sort_order: 512 }),
      sourceEpicId: null,
      sourceAssigneeId: null,
      sourceLabelId: null,
      sourceModuleId: null,
      sourceCycleId: null,
      target: { kind: "cycle", cycleId: "c-2" },
      targetRows,
    });
    expect(plan).toEqual({
      type: "cycle-move",
      addCycleId: "c-2",
      removeCycleId: null,
      sortOrder: 3072,
    });
  });

  it("cycle header: own-group appearance is a silent no-op", () => {
    const plan = resolveListHeaderDrop({
      dragged: withCycle("d", { id: "c-2", name: "Sprint 2" }),
      sourceEpicId: null,
      sourceAssigneeId: null,
      sourceLabelId: null,
      sourceModuleId: null,
      sourceCycleId: "c-2",
      target: { kind: "cycle", cycleId: "c-2" },
      targetRows,
    });
    // Already carries the target cycle: silent no-op, not a move.
    expect(plan).toBeNull();
  });

  it("cycle header: already carrying the target cycle is a silent no-op, even from another cycle group", () => {
    expect(
      resolveListHeaderDrop({
        dragged: withCycle("d", { id: "c-2", name: "Sprint 2" }),
        sourceEpicId: null,
        sourceAssigneeId: null,
        sourceLabelId: null,
        sourceModuleId: null,
        sourceCycleId: "c-1",
        target: { kind: "cycle", cycleId: "c-2" },
        targetRows,
      }),
    ).toBeNull();
  });

  it("cycle Ungrouped: removes the displayed cycle only, no sort PATCH", () => {
    const plan = resolveListHeaderDrop({
      dragged: withCycle("d", { id: "c-1", name: "Sprint 1" }),
      sourceEpicId: null,
      sourceAssigneeId: null,
      sourceLabelId: null,
      sourceModuleId: null,
      sourceCycleId: "c-1",
      target: { kind: "cycle-ungrouped" },
      targetRows,
    });
    expect(plan).toEqual({
      type: "cycle-ungroup",
      removeCycleIds: ["c-1"],
    });
  });

  it("cycle Ungrouped: already-cycleless is a silent no-op", () => {
    expect(
      resolveListHeaderDrop({
        dragged: mkIssue("d", {}),
        sourceEpicId: null,
        sourceAssigneeId: null,
        sourceLabelId: null,
        sourceModuleId: null,
        sourceCycleId: null,
        target: { kind: "cycle-ungrouped" },
        targetRows,
      }),
    ).toBeNull();
  });

  it("execute cycle-move: add, then remove, then PATCH {sort_order} only, then invalidate", async () => {
    const { events, deps } = mockCycleDeps();
    await executeListHeaderDrop(
      "d",
      {
        type: "cycle-move",
        addCycleId: "c-2",
        removeCycleId: "c-1",
        sortOrder: 3072,
      },
      deps,
    );
    expect(events).toEqual([
      "addCycle:c-2",
      "removeCycle:c-1",
      'patch:{"sort_order":3072}',
      "invalidate",
    ]);
    // The sort PATCH body carries exactly {sort_order} — never a cycle id.
    expect(deps.patch).toHaveBeenCalledWith("d", { sort_order: 3072 });
  });

  it("execute cycle-move: an add failure aborts remove and PATCH, surfaces honestly", async () => {
    const { events, deps } = mockCycleDeps();
    deps.addToCycle.mockRejectedValueOnce(new Error("add boom"));
    await executeListHeaderDrop(
      "d",
      {
        type: "cycle-move",
        addCycleId: "c-2",
        removeCycleId: "c-1",
        sortOrder: 3072,
      },
      deps,
    );
    expect(events).toEqual(["error:add boom", "invalidate"]);
    expect(deps.removeFromCycle).not.toHaveBeenCalled();
    expect(deps.patch).not.toHaveBeenCalled();
  });

  it("execute cycle-move: a remove failure after a successful add aborts the PATCH and surfaces honestly", async () => {
    const { events, deps } = mockCycleDeps();
    deps.removeFromCycle.mockRejectedValueOnce(new Error("remove boom"));
    await executeListHeaderDrop(
      "d",
      {
        type: "cycle-move",
        addCycleId: "c-2",
        removeCycleId: "c-1",
        sortOrder: 3072,
      },
      deps,
    );
    // The row is now in both cycles — reported, never hidden,
    // never silently retried.
    expect(events).toEqual([
      "addCycle:c-2",
      "error:remove boom",
      "invalidate",
    ]);
    expect(deps.patch).not.toHaveBeenCalled();
  });

  it("execute cycle-ungroup: one remove for the displayed cycle, no PATCH", async () => {
    const { events, deps } = mockCycleDeps();
    await executeListHeaderDrop(
      "d",
      { type: "cycle-ungroup", removeCycleIds: ["c-1"] },
      deps,
    );
    expect(events).toEqual(["removeCycle:c-1", "invalidate"]);
    expect(deps.patch).not.toHaveBeenCalled();
  });

  it("execute cycle-ungroup: a failed remove surfaces honestly", async () => {
    const { events, deps } = mockCycleDeps();
    deps.removeFromCycle.mockRejectedValueOnce(new Error("remove boom"));
    await executeListHeaderDrop(
      "d",
      { type: "cycle-ungroup", removeCycleIds: ["c-1"] },
      deps,
    );
    expect(events).toEqual(["error:remove boom", "invalidate"]);
    expect(deps.removeFromCycle).toHaveBeenCalledTimes(1);
  });

  it("dispatch routes a cycle header drop to the reassignment pipeline regardless of the reorder gate", () => {
    const stack = mkStack("c-1", [mkIssue("a")], null, null, null, null, "c-1");
    const sortableMap = new Map<string, ListDragRowRef>([
      ["row:c-1:a", mkRef("a", mkIssue("a"), stack)],
    ]);
    const headerMap = new Map<string, ListHeaderDropInfo>([
      [
        "header:c-2:cycle:c-2",
        {
          target: { kind: "cycle", cycleId: "c-2" },
          targetRows: [],
        },
      ],
    ]);
    const onHeaderDrop = vi.fn();
    dispatchListDragEnd({
      activeSortableId: "row:c-1:a",
      overId: "header:c-2:cycle:c-2",
      sortableMap,
      headerMap,
      reorderEnabled: false,
      onRowDrop: vi.fn(),
      onHeaderDrop,
    });
    expect(onHeaderDrop).toHaveBeenCalledTimes(1);
  });

  it("dispatch keeps a cross-stack row drop a no-op under cycle grouping", () => {
    const stackA = mkStack("c-1", [mkIssue("a")], null, null, null, null, "c-1");
    const stackB = mkStack("c-2", [mkIssue("b")], null, null, null, null, "c-2");
    const sortableMap = new Map<string, ListDragRowRef>([
      ["row:c-1:a", mkRef("a", mkIssue("a"), stackA)],
      ["row:c-2:b", mkRef("b", mkIssue("b"), stackB)],
    ]);
    const onRowDrop = vi.fn();
    dispatchListDragEnd({
      activeSortableId: "row:c-1:a",
      overId: "row:c-2:b",
      sortableMap,
      headerMap: new Map(),
      reorderEnabled: true,
      onRowDrop,
      onHeaderDrop: vi.fn(),
    });
    expect(onRowDrop).not.toHaveBeenCalled();
  });
});

// C133T1: release header drops — the cycle pipeline mirrored with the
// single-assign divergence: a target-group drop is exactly ONE POST
// (assign overwrites release_id), NO DELETE, then the sort PATCH;
// Ungrouped = remove the displayed release only, no PATCH.
describe("release header drops (C133T1)", () => {
  const targetRows = [
    mkIssue("t1", { sort_order: 1024 }),
    mkIssue("t2", { sort_order: 2048 }),
  ];

  function mockReleaseDeps() {
    const events: string[] = [];
    return {
      events,
      deps: {
        patch: vi.fn((_id: string, body: unknown) => {
          events.push(`patch:${JSON.stringify(body)}`);
          return Promise.resolve();
        }),
        addToEpic: vi.fn(() => Promise.resolve()),
        removeFromEpic: vi.fn(() => Promise.resolve()),
        addToAssignee: vi.fn(() => Promise.resolve()),
        removeFromAssignee: vi.fn(() => Promise.resolve()),
        addToLabel: vi.fn(() => Promise.resolve()),
        removeFromLabel: vi.fn(() => Promise.resolve()),
        addToModule: vi.fn(() => Promise.resolve()),
        removeFromModule: vi.fn(() => Promise.resolve()),
        addToCycle: vi.fn(() => Promise.resolve()),
        removeFromCycle: vi.fn(() => Promise.resolve()),
        addToRelease: vi.fn((releaseId: string) => {
          events.push(`addRelease:${releaseId}`);
          return Promise.resolve();
        }),
        removeFromRelease: vi.fn((releaseId: string) => {
          events.push(`removeRelease:${releaseId}`);
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

  const withRelease = (id: string, release: { id: string; name: string }) =>
    mkIssue(id, {
      sort_order: 512,
      project_structure: { release },
    } as Partial<Issue>);

  it("toHeaderTarget maps release fields", () => {
    expect(toHeaderTarget({ targetReleaseId: "r-1" })).toEqual({
      kind: "release",
      releaseId: "r-1",
    });
    expect(toHeaderTarget({ isUngroupedRelease: true })).toEqual({
      kind: "release-ungrouped",
    });
  });

  it("header ids are namespaced and disjoint from row ids", () => {
    const h = headerDropId("r-1", { kind: "release", releaseId: "r-2" });
    const u = headerDropId("release:ungrouped", {
      kind: "release-ungrouped",
    });
    expect(h).toBe("header:r-1:release:r-2");
    expect(u).toBe("header:release:ungrouped:release:ungrouped");
    expect(h.startsWith("header:")).toBe(true);
    expect(listSortableId("r-1", "i-1")).not.toBe(h);
  });

  it("release header: single assign + sort PATCH, NO remove — from a release source", () => {
    const plan = resolveListHeaderDrop({
      dragged: withRelease("d", { id: "r-1", name: "v1.0" }),
      sourceEpicId: null,
      sourceAssigneeId: null,
      sourceLabelId: null,
      sourceModuleId: null,
      sourceCycleId: null,
      sourceReleaseId: "r-1",
      target: { kind: "release", releaseId: "r-2" },
      targetRows,
    });
    // The plan carries NO remove field at all — the single-assign
    // shape, unlike cycle-move's addCycleId/removeCycleId pair.
    expect(plan).toEqual({
      type: "release-move",
      addReleaseId: "r-2",
      sortOrder: 3072,
    });
    expect(plan).not.toHaveProperty("removeReleaseId");
  });

  it("release header: same plan from an Ungrouped appearance", () => {
    const plan = resolveListHeaderDrop({
      dragged: mkIssue("d", { sort_order: 512 }),
      sourceEpicId: null,
      sourceReleaseId: null,
      target: { kind: "release", releaseId: "r-2" },
      targetRows,
    });
    expect(plan).toEqual({
      type: "release-move",
      addReleaseId: "r-2",
      sortOrder: 3072,
    });
  });

  it("release header: own-group appearance is a silent no-op with no calls", () => {
    const plan = resolveListHeaderDrop({
      dragged: withRelease("d", { id: "r-2", name: "v2.0" }),
      sourceEpicId: null,
      sourceReleaseId: "r-2",
      target: { kind: "release", releaseId: "r-2" },
      targetRows,
    });
    expect(plan).toBeNull();
  });

  it("release header: already carrying the target release is a silent no-op, even from another release group", () => {
    expect(
      resolveListHeaderDrop({
        dragged: withRelease("d", { id: "r-2", name: "v2.0" }),
        sourceEpicId: null,
        sourceReleaseId: "r-1",
        target: { kind: "release", releaseId: "r-2" },
        targetRows,
      }),
    ).toBeNull();
  });

  it("release Ungrouped: removes the displayed release only, no sort PATCH", () => {
    const plan = resolveListHeaderDrop({
      dragged: withRelease("d", { id: "r-1", name: "v1.0" }),
      sourceEpicId: null,
      sourceReleaseId: "r-1",
      target: { kind: "release-ungrouped" },
      targetRows,
    });
    expect(plan).toEqual({
      type: "release-ungroup",
      removeReleaseIds: ["r-1"],
    });
  });

  it("release Ungrouped: already-releaseless is a silent no-op", () => {
    expect(
      resolveListHeaderDrop({
        dragged: mkIssue("d", {}),
        sourceEpicId: null,
        sourceReleaseId: null,
        target: { kind: "release-ungrouped" },
        targetRows,
      }),
    ).toBeNull();
  });

  it("execute release-move: exactly ONE POST, NO DELETE, then PATCH {sort_order} only, then invalidate", async () => {
    const { events, deps } = mockReleaseDeps();
    await executeListHeaderDrop(
      "d",
      {
        type: "release-move",
        addReleaseId: "r-2",
        sortOrder: 3072,
      },
      deps,
    );
    expect(events).toEqual([
      "addRelease:r-2",
      'patch:{"sort_order":3072}',
      "invalidate",
    ]);
    expect(deps.removeFromRelease).not.toHaveBeenCalled();
    expect(deps.patch).toHaveBeenCalledWith("d", { sort_order: 3072 });
  });

  it("execute release-move: an assign failure aborts the PATCH and surfaces honestly (nothing changed)", async () => {
    const { events, deps } = mockReleaseDeps();
    deps.addToRelease.mockRejectedValueOnce(new Error("assign boom"));
    await executeListHeaderDrop(
      "d",
      {
        type: "release-move",
        addReleaseId: "r-2",
        sortOrder: 3072,
      },
      deps,
    );
    expect(events).toEqual(["error:assign boom", "invalidate"]);
    expect(deps.removeFromRelease).not.toHaveBeenCalled();
    expect(deps.patch).not.toHaveBeenCalled();
  });

  it("execute release-ungroup: one remove for the displayed release, no PATCH", async () => {
    const { events, deps } = mockReleaseDeps();
    await executeListHeaderDrop(
      "d",
      { type: "release-ungroup", removeReleaseIds: ["r-1"] },
      deps,
    );
    expect(events).toEqual(["removeRelease:r-1", "invalidate"]);
    expect(deps.patch).not.toHaveBeenCalled();
  });

  it("execute release-ungroup: a failed remove surfaces honestly", async () => {
    const { events, deps } = mockReleaseDeps();
    deps.removeFromRelease.mockRejectedValueOnce(new Error("remove boom"));
    await executeListHeaderDrop(
      "d",
      { type: "release-ungroup", removeReleaseIds: ["r-1"] },
      deps,
    );
    expect(events).toEqual(["error:remove boom", "invalidate"]);
    expect(deps.removeFromRelease).toHaveBeenCalledTimes(1);
  });

  it("dispatch routes a release header drop to the reassignment pipeline regardless of the reorder gate", () => {
    const stack = mkStack("r-1", [mkIssue("a")], null, null, null, null, null, "r-1");
    const sortableMap = new Map<string, ListDragRowRef>([
      ["row:r-1:a", mkRef("a", mkIssue("a"), stack)],
    ]);
    const headerMap = new Map<string, ListHeaderDropInfo>([
      [
        "header:r-2:release:r-2",
        {
          target: { kind: "release", releaseId: "r-2" },
          targetRows: [],
        },
      ],
    ]);
    const onHeaderDrop = vi.fn();
    dispatchListDragEnd({
      activeSortableId: "row:r-1:a",
      overId: "header:r-2:release:r-2",
      sortableMap,
      headerMap,
      reorderEnabled: false,
      onRowDrop: vi.fn(),
      onHeaderDrop,
    });
    expect(onHeaderDrop).toHaveBeenCalledTimes(1);
  });

  it("dispatch keeps a cross-stack row drop a no-op under release grouping", () => {
    const stackA = mkStack("r-1", [mkIssue("a")], null, null, null, null, null, "r-1");
    const stackB = mkStack("r-2", [mkIssue("b")], null, null, null, null, null, "r-2");
    const sortableMap = new Map<string, ListDragRowRef>([
      ["row:r-1:a", mkRef("a", mkIssue("a"), stackA)],
      ["row:r-2:b", mkRef("b", mkIssue("b"), stackB)],
    ]);
    const onRowDrop = vi.fn();
    dispatchListDragEnd({
      activeSortableId: "row:r-1:a",
      overId: "row:r-2:b",
      sortableMap,
      headerMap: new Map(),
      reorderEnabled: true,
      onRowDrop,
      onHeaderDrop: vi.fn(),
    });
    expect(onRowDrop).not.toHaveBeenCalled();
  });
});
