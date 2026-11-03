// C133T2: unit tests for the board's Group-by-Releases pure helpers
// (boardRelease.ts), mirroring boardCycle.test.ts. Covers the plan's
// rulings: column grouping, single-appearance, Ungrouped-last/
// hidden-when-empty, compound id parse/round-trip, the drop-plan noop
// matrix and single-assign move-plan shape, the move executor's
// exactly-one-POST / no-DELETE shape and failure honesty, quick-add→
// assign, and expand-to-full-form carry via T1's newIssueReleaseId
// bridge. The one deliberate difference from cycles: moves are a
// SINGLE assign (assign overwrites release_id), never add-then-remove.
// Releases have NO color, so no color field is asserted anywhere
// (and none is carried).

import { describe, expect, it, vi } from "vitest";
import {
  RELEASE_CARD_PREFIX,
  RELEASE_COLUMN_PREFIX,
  RELEASE_UNGROUPED_COLUMN_KEY,
  buildReleaseBoardGroups,
  executeReleaseMove,
  planBoardReleaseQuickExpand,
  planReleaseDrop,
  planReleaseQuickAdd,
  releaseCardDragId,
  releaseDragColumnKey,
  releaseDragIssueId,
  releaseDropColumnKey,
} from "./boardRelease";
import type { Issue } from "../../lib/types";

function mkIssue(
  id: string,
  release: { id: string; name: string } | null,
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
    project_structure: { release, modules: [] },
  } as Issue;
}

const V1 = { id: "r-v1", name: "v1.0" };
const V2 = { id: "r-v2", name: "v2.0" };

describe("buildReleaseBoardGroups", () => {
  it("builds one column per release, first-appearance order, with the state stamp and NO color field", () => {
    const issues = [mkIssue("1", V1), mkIssue("2", V2), mkIssue("3", V1)];
    const groups = buildReleaseBoardGroups(issues, "s-backlog");
    expect(groups.map((g) => g.title)).toEqual(["v1.0", "v2.0"]);
    expect(groups[0].droppableId).toBe(`${RELEASE_COLUMN_PREFIX}r-v1`);
    expect(groups[0].columnKey).toBe("r-v1");
    expect(groups[0].releaseId).toBe("r-v1");
    expect("color" in groups[0]).toBe(false);
    expect(groups[0].issues.map((i) => i.id)).toEqual(["1", "3"]);
    expect(groups[0].newStateId).toBe("s-backlog");
  });

  it("single-appearance: an issue appears in exactly ONE release column", () => {
    const issues = [mkIssue("1", V1), mkIssue("2", V2)];
    const groups = buildReleaseBoardGroups(issues);
    expect(groups).toHaveLength(2);
    expect(groups.flatMap((g) => g.issues.map((i) => i.id))).toEqual([
      "1",
      "2",
    ]);
  });

  it("Ungrouped is always last and hidden when empty", () => {
    const issues = [mkIssue("1", null), mkIssue("2", V1)];
    const withUngrouped = buildReleaseBoardGroups(issues);
    expect(withUngrouped.map((g) => g.title)).toEqual(["v1.0", "Ungrouped"]);
    const ungrouped = withUngrouped.find(
      (g) => g.columnKey === RELEASE_UNGROUPED_COLUMN_KEY,
    )!;
    expect(ungrouped.droppableId).toBe(`${RELEASE_COLUMN_PREFIX}ungrouped`);
    expect(ungrouped.releaseId).toBeNull();
    expect(ungrouped.issues.map((i) => i.id)).toEqual(["1"]);

    const noUngrouped = buildReleaseBoardGroups([mkIssue("2", V1)]);
    expect(noUngrouped.map((g) => g.title)).toEqual(["v1.0"]);
    expect(
      noUngrouped.some((g) => g.columnKey === RELEASE_UNGROUPED_COLUMN_KEY),
    ).toBe(false);
  });

  it("rows without project_structure group as Ungrouped, never crash", () => {
    const bare = mkIssue("1", V1);
    delete (bare as Partial<Issue>).project_structure;
    const groups = buildReleaseBoardGroups([bare]);
    expect(groups.map((g) => g.title)).toEqual(["Ungrouped"]);
  });

  it("show-empty-groups synthesizes nothing: no release column without a loaded issue", () => {
    expect(buildReleaseBoardGroups([])).toEqual([]);
  });
});

describe("releaseDropColumnKey", () => {
  it("resolves column droppables and compound card ids", () => {
    expect(releaseDropColumnKey(`${RELEASE_COLUMN_PREFIX}r-v1`)).toBe(
      "r-v1",
    );
    expect(releaseDropColumnKey(`${RELEASE_COLUMN_PREFIX}ungrouped`)).toBe(
      "ungrouped",
    );
    expect(releaseDropColumnKey(releaseCardDragId("r-v1", "issue-1"))).toBe(
      "r-v1",
    );
    expect(
      releaseDropColumnKey(
        releaseCardDragId(RELEASE_UNGROUPED_COLUMN_KEY, "issue-1"),
      ),
    ).toBe("ungrouped");
  });

  it("card-prefix check precedes column-prefix check (card ids start with release: too)", () => {
    expect(releaseDropColumnKey("release-card:r-v1:issue-1")).toBe("r-v1");
  });

  it("returns null for non-release drop targets", () => {
    expect(releaseDropColumnKey("state-uuid")).toBeNull();
    expect(releaseDropColumnKey("priority:3")).toBeNull();
    expect(releaseDropColumnKey("epic:epic-a")).toBeNull();
    expect(releaseDropColumnKey("assignee:u-alice")).toBeNull();
    expect(releaseDropColumnKey("label:l-bug")).toBeNull();
    expect(releaseDropColumnKey("module:m-auth")).toBeNull();
    expect(releaseDropColumnKey("cycle:c-sprint-1")).toBeNull();
    expect(releaseDropColumnKey("issue-1")).toBeNull();
  });
});

describe("releaseDragIssueId / releaseDragColumnKey", () => {
  it("round-trips compound card ids: issue id via last colon, column key via prefix", () => {
    const id = releaseCardDragId("r-v1", "issue-1");
    expect(id.startsWith(RELEASE_CARD_PREFIX)).toBe(true);
    expect(releaseDragIssueId(id)).toBe("issue-1");
    expect(releaseDragColumnKey(id)).toBe("r-v1");
  });

  it("is identity / null for plain (state/priority mode) ids", () => {
    expect(releaseDragIssueId("issue-1")).toBe("issue-1");
    expect(releaseDragColumnKey("issue-1")).toBeNull();
    expect(releaseDragColumnKey("cycle-card:c-sprint-1:issue-1")).toBeNull();
  });
});

describe("planReleaseDrop", () => {
  it("drop into a different release column plans a MOVE naming only the target release", () => {
    const plan = planReleaseDrop("r-v2", mkIssue("1", V1));
    expect(plan).toEqual({ kind: "move", releaseId: "r-v2" });
  });

  it("drop from Ungrouped plans a MOVE to the target release", () => {
    const plan = planReleaseDrop("r-v1", mkIssue("1", null));
    expect(plan).toEqual({ kind: "move", releaseId: "r-v1" });
  });

  it("drop into Ungrouped is a deliberate no-op (board removal is out of scope)", () => {
    const plan = planReleaseDrop(
      RELEASE_UNGROUPED_COLUMN_KEY,
      mkIssue("1", V1),
    );
    expect(plan).toEqual({ kind: "noop", reason: "ungrouped" });
  });

  it("cross-column drop onto the already-member release is a no-op (idempotent)", () => {
    const plan = planReleaseDrop("r-v1", mkIssue("1", V1));
    expect(plan).toEqual({ kind: "noop", reason: "already-member" });
  });

  it("single-assign move-plan shape: exactly the target release, no source field, no DELETE shape", () => {
    const plan = planReleaseDrop("r-v2", mkIssue("1", V1));
    expect(plan.kind).toBe("move");
    if (plan.kind === "move") {
      expect(Object.keys(plan).sort()).toEqual(["kind", "releaseId"]);
      expect("sourceReleaseId" in plan).toBe(false);
    }
  });

  it("move plans never change state: no state field anywhere on the plan", () => {
    const plan = planReleaseDrop("r-v2", mkIssue("1", V1));
    expect("stateId" in plan).toBe(false);
    expect("state_id" in plan).toBe(false);
  });
});

describe("executeReleaseMove", () => {
  it("MOVE calls exactly ONE assign with {issue_ids} semantics (single POST, no DELETE dep exists)", async () => {
    const deps = {
      assignToRelease: vi.fn(async () => {}),
    };
    // The deps type carries no remove function at all: a release move
    // structurally cannot issue a DELETE.
    expect("removeFromRelease" in deps).toBe(false);
    const result = await executeReleaseMove(
      deps,
      { kind: "move", releaseId: "r-v2" },
      "issue-1",
    );
    expect(result).toEqual({ ok: true });
    expect(deps.assignToRelease).toHaveBeenCalledTimes(1);
    expect(deps.assignToRelease).toHaveBeenCalledWith("r-v2", "issue-1");
  });

  it("assign failure surfaces honestly as stage assign (nothing changed)", async () => {
    const assignError = new Error("assign failed");
    const deps = {
      assignToRelease: vi.fn(async () => {
        throw assignError;
      }),
    };
    const result = await executeReleaseMove(
      deps,
      { kind: "move", releaseId: "r-v2" },
      "issue-1",
    );
    expect(result).toEqual({
      ok: false,
      stage: "assign",
      error: assignError,
    });
    expect(deps.assignToRelease).toHaveBeenCalledTimes(1);
  });
});

describe("planReleaseQuickAdd", () => {
  it("release column quick-add creates AND assigns to the release", () => {
    const plan = planReleaseQuickAdd({
      newStateId: "s-backlog",
      releaseId: "r-v1",
    });
    expect(plan).toEqual({
      stateId: "s-backlog",
      attachReleaseId: "r-v1",
    });
  });

  it("Ungrouped quick-add is a plain create (no assign)", () => {
    const plan = planReleaseQuickAdd({
      newStateId: "s-backlog",
      releaseId: null,
    });
    expect(plan).toEqual({ stateId: "s-backlog", attachReleaseId: null });
  });
});

describe("planBoardReleaseQuickExpand", () => {
  it("release column expand carries title + state + release via T1's newIssueReleaseId bridge", () => {
    expect(
      planBoardReleaseQuickExpand(
        { newStateId: "s-backlog", releaseId: "r-v1" },
        "New thing",
      ),
    ).toEqual({
      newIssue: true,
      newIssueName: "New thing",
      newIssueStateId: "s-backlog",
      newIssueReleaseId: "r-v1",
    });
  });

  it("Ungrouped column expand carries title + state only (no release)", () => {
    const state = planBoardReleaseQuickExpand(
      { newStateId: "s-backlog", releaseId: null },
      "New thing",
    );
    expect(state.newIssueReleaseId).toBeUndefined();
    expect(state.newIssueStateId).toBe("s-backlog");
  });

  it("priority carry still passes through alongside the release rule", () => {
    expect(
      planBoardReleaseQuickExpand(
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
