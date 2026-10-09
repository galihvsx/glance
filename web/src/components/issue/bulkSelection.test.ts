import { describe, expect, it } from "vitest";
import {
  buildBulkSetPayload,
  selectAllIds,
  toggleSelected,
} from "./bulkSelection";

describe("toggleSelected", () => {
  it("adds an id that is not selected", () => {
    const next = toggleSelected(new Set(), "a");
    expect([...next]).toEqual(["a"]);
  });

  it("removes an id that is selected", () => {
    const next = toggleSelected(new Set(["a", "b"]), "a");
    expect([...next]).toEqual(["b"]);
  });

  it("does not mutate the input set", () => {
    const sel = new Set(["a"]);
    toggleSelected(sel, "b");
    expect([...sel]).toEqual(["a"]);
  });
});

describe("selectAllIds", () => {
  it("unions ids keeping insertion order", () => {
    const next = selectAllIds(new Set(["a"]), ["b", "c"]);
    expect([...next]).toEqual(["a", "b", "c"]);
  });

  it("ignores duplicates", () => {
    const next = selectAllIds(new Set(["a", "b"]), ["b", "c", "c"]);
    expect([...next]).toEqual(["a", "b", "c"]);
  });
});

describe("buildBulkSetPayload", () => {
  it("returns null when nothing was touched", () => {
    expect(
      buildBulkSetPayload({
        stateId: null,
        priority: null,
        labelIds: null,
        assigneeId: undefined,
      }),
    ).toBeNull();
  });

  it("includes only touched fields", () => {
    expect(
      buildBulkSetPayload({
        stateId: "state-1",
        priority: null,
        labelIds: null,
        assigneeId: undefined,
      }),
    ).toEqual({ state_id: "state-1" });
  });

  it("maps every draft field to the API shape", () => {
    expect(
      buildBulkSetPayload({
        stateId: "state-1",
        priority: 4,
        labelIds: ["l1", "l2"],
        assigneeId: "user-1",
      }),
    ).toEqual({
      state_id: "state-1",
      priority: 4,
      label_ids: ["l1", "l2"],
      assignee_id: "user-1",
    });
  });

  it("treats assignee null as explicit clear", () => {
    expect(
      buildBulkSetPayload({
        stateId: null,
        priority: null,
        labelIds: null,
        assigneeId: null,
      }),
    ).toEqual({ assignee_id: null });
  });

  it("treats an empty label list as clear-labels", () => {
    expect(
      buildBulkSetPayload({
        stateId: null,
        priority: null,
        labelIds: [],
        assigneeId: undefined,
      }),
    ).toEqual({ label_ids: [] });
  });

  it("copies the label array (no aliasing)", () => {
    const labelIds = ["l1"];
    const payload = buildBulkSetPayload({
      stateId: null,
      priority: null,
      labelIds,
      assigneeId: undefined,
    });
    labelIds.push("l2");
    expect(payload?.label_ids).toEqual(["l1"]);
  });
});
