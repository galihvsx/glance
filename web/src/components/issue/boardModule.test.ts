// C131T1: unit tests for the board's Group-by-Modules pure helpers
// (boardModule.ts), mirroring boardLabel.test.ts. Covers the plan's
// rulings: column grouping, multi-appearance, Ungrouped-last/
// hidden-when-empty, compound id parse/round-trip, the drop-plan noop
// matrix, quick-add→attach, and expand-to-full-form carry. The one
// deliberate difference from labels: modules have NO color, so no
// color field is asserted anywhere (and none is carried).
import { describe, expect, it } from "vitest";
import {
  MODULE_CARD_PREFIX,
  MODULE_COLUMN_PREFIX,
  MODULE_UNGROUPED_COLUMN_KEY,
  buildModuleBoardGroups,
  moduleCardDragId,
  moduleDragColumnKey,
  moduleDragIssueId,
  moduleDropColumnKey,
  planBoardModuleQuickExpand,
  planModuleDrop,
  planModuleQuickAdd,
} from "./boardModule";
import type { Issue } from "../../lib/types";

function mkIssue(
  id: string,
  modules: { id: string; name: string }[],
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
    project_structure: { modules },
  } as Issue;
}

const AUTH = { id: "m-auth", name: "Auth" };
const BILLING = { id: "m-billing", name: "Billing" };

describe("buildModuleBoardGroups", () => {
  it("builds one column per module, first-appearance order, with the state stamp and NO color field", () => {
    const issues = [
      mkIssue("1", [AUTH]),
      mkIssue("2", [BILLING]),
      mkIssue("3", [AUTH]),
    ];
    const groups = buildModuleBoardGroups(issues, "s-backlog");
    expect(groups.map((g) => g.title)).toEqual(["Auth", "Billing"]);
    expect(groups[0].droppableId).toBe(`${MODULE_COLUMN_PREFIX}m-auth`);
    expect(groups[0].columnKey).toBe("m-auth");
    expect(groups[0].moduleId).toBe("m-auth");
    expect("color" in groups[0]).toBe(false);
    expect(groups[0].issues.map((i) => i.id)).toEqual(["1", "3"]);
    expect(groups[0].newStateId).toBe("s-backlog");
  });

  it("multi-appearance: an issue with N modules appears in EVERY module column", () => {
    const issues = [mkIssue("1", [AUTH, BILLING])];
    const groups = buildModuleBoardGroups(issues);
    expect(groups).toHaveLength(2);
    expect(groups[0].issues.map((i) => i.id)).toEqual(["1"]);
    expect(groups[1].issues.map((i) => i.id)).toEqual(["1"]);
  });

  it("Ungrouped is always last and hidden when empty", () => {
    const issues = [mkIssue("1", []), mkIssue("2", [AUTH])];
    const withUngrouped = buildModuleBoardGroups(issues);
    expect(withUngrouped.map((g) => g.title)).toEqual(["Auth", "Ungrouped"]);
    const ungrouped = withUngrouped.find(
      (g) => g.columnKey === MODULE_UNGROUPED_COLUMN_KEY,
    )!;
    expect(ungrouped.droppableId).toBe(`${MODULE_COLUMN_PREFIX}ungrouped`);
    expect(ungrouped.moduleId).toBeNull();
    expect(ungrouped.issues.map((i) => i.id)).toEqual(["1"]);

    const noUngrouped = buildModuleBoardGroups([mkIssue("2", [AUTH])]);
    expect(noUngrouped.map((g) => g.title)).toEqual(["Auth"]);
    expect(
      noUngrouped.some((g) => g.columnKey === MODULE_UNGROUPED_COLUMN_KEY),
    ).toBe(false);
  });

  it("rows without project_structure group as Ungrouped, never crash", () => {
    const bare = mkIssue("1", [AUTH]);
    delete (bare as Partial<Issue>).project_structure;
    const groups = buildModuleBoardGroups([bare]);
    expect(groups.map((g) => g.title)).toEqual(["Ungrouped"]);
  });

  it("show-empty-groups synthesizes nothing: no module column without a loaded issue", () => {
    expect(buildModuleBoardGroups([])).toEqual([]);
  });
});

describe("moduleDropColumnKey", () => {
  it("resolves column droppables and compound card ids", () => {
    expect(moduleDropColumnKey(`${MODULE_COLUMN_PREFIX}m-auth`)).toBe(
      "m-auth",
    );
    expect(moduleDropColumnKey(`${MODULE_COLUMN_PREFIX}ungrouped`)).toBe(
      "ungrouped",
    );
    expect(moduleDropColumnKey(moduleCardDragId("m-auth", "issue-1"))).toBe(
      "m-auth",
    );
    expect(
      moduleDropColumnKey(
        moduleCardDragId(MODULE_UNGROUPED_COLUMN_KEY, "issue-1"),
      ),
    ).toBe("ungrouped");
  });

  it("returns null for non-module drop targets", () => {
    expect(moduleDropColumnKey("state-uuid")).toBeNull();
    expect(moduleDropColumnKey("priority:3")).toBeNull();
    expect(moduleDropColumnKey("epic:epic-a")).toBeNull();
    expect(moduleDropColumnKey("assignee:u-alice")).toBeNull();
    expect(moduleDropColumnKey("label:l-bug")).toBeNull();
    expect(moduleDropColumnKey("issue-1")).toBeNull();
  });
});

describe("moduleDragIssueId / moduleDragColumnKey", () => {
  it("round-trips compound card ids: issue id via last colon, column key via prefix", () => {
    const id = moduleCardDragId("m-auth", "issue-1");
    expect(id.startsWith(MODULE_CARD_PREFIX)).toBe(true);
    expect(moduleDragIssueId(id)).toBe("issue-1");
    expect(moduleDragColumnKey(id)).toBe("m-auth");
  });

  it("is identity / null for plain (state/priority mode) ids", () => {
    expect(moduleDragIssueId("issue-1")).toBe("issue-1");
    expect(moduleDragColumnKey("issue-1")).toBeNull();
    expect(moduleDragColumnKey("label-card:l-bug:issue-1")).toBeNull();
  });
});

describe("planModuleDrop", () => {
  it("drop into a module column plans an attach", () => {
    const plan = planModuleDrop("m-auth", mkIssue("1", [BILLING]));
    expect(plan).toEqual({ kind: "attach", moduleId: "m-auth" });
  });

  it("drop into Ungrouped is a deliberate no-op (detach-from-all is destructive)", () => {
    const plan = planModuleDrop(
      MODULE_UNGROUPED_COLUMN_KEY,
      mkIssue("1", [AUTH]),
    );
    expect(plan).toEqual({ kind: "noop", reason: "ungrouped" });
  });

  it("cross-column drop onto an already-member module is a no-op (idempotent)", () => {
    const plan = planModuleDrop("m-auth", mkIssue("1", [AUTH, BILLING]));
    expect(plan).toEqual({ kind: "noop", reason: "already-member" });
  });

  it("multi-module aware: dropping a multi-module issue into a NEW module attaches without detaching", () => {
    const plan = planModuleDrop("m-billing", mkIssue("1", [AUTH]));
    expect(plan).toEqual({ kind: "attach", moduleId: "m-billing" });
  });

  it("attach-only: the plan never names a detach — no other membership is touched", () => {
    const plan = planModuleDrop("m-billing", mkIssue("1", [AUTH]));
    expect(plan.kind).toBe("attach");
    if (plan.kind === "attach") {
      expect(Object.keys(plan).sort()).toEqual(["kind", "moduleId"]);
    }
  });
});

describe("planModuleQuickAdd", () => {
  it("module column quick-add creates AND attaches to the module", () => {
    const plan = planModuleQuickAdd({
      newStateId: "s-backlog",
      moduleId: "m-auth",
    });
    expect(plan).toEqual({ stateId: "s-backlog", attachModuleId: "m-auth" });
  });

  it("Ungrouped quick-add is a plain create (no attach)", () => {
    const plan = planModuleQuickAdd({
      newStateId: "s-backlog",
      moduleId: null,
    });
    expect(plan).toEqual({ stateId: "s-backlog", attachModuleId: null });
  });

  it("state/priority columns are unaffected (no attach)", () => {
    const plan = planModuleQuickAdd({ newStateId: "s-1" });
    expect(plan).toEqual({ stateId: "s-1", attachModuleId: null });
  });
});

describe("planBoardModuleQuickExpand", () => {
  it("module column expand carries title + state + module (T0 bridge)", () => {
    expect(
      planBoardModuleQuickExpand(
        { newStateId: "s-backlog", moduleId: "m-auth" },
        "New thing",
      ),
    ).toEqual({
      newIssue: true,
      newIssueName: "New thing",
      newIssueStateId: "s-backlog",
      newIssueModuleId: "m-auth",
    });
  });

  it("Ungrouped column expand carries title + state only (no module)", () => {
    const state = planBoardModuleQuickExpand(
      { newStateId: "s-backlog", moduleId: null },
      "New thing",
    );
    expect(state.newIssueModuleId).toBeUndefined();
    expect(state.newIssueStateId).toBe("s-backlog");
  });

  it("priority carry still passes through alongside the module rule", () => {
    expect(
      planBoardModuleQuickExpand(
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
