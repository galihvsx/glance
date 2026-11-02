// C122T1: subtask priority normalization. The split cases moved
// with the split itself to splitNestedIssues.test.ts (C124T0).

import { describe, expect, it } from "vitest";
import { subtaskPriorityLevel } from "./boardSubtasks";

describe("subtaskPriorityLevel (C122T1)", () => {
  it("passes numeric priorities through", () => {
    expect(subtaskPriorityLevel(4)).toBe(4);
    expect(subtaskPriorityLevel(0)).toBe(0);
  });

  it("maps plan-contract label strings to levels", () => {
    expect(subtaskPriorityLevel("High")).toBe(3);
    expect(subtaskPriorityLevel("Urgent")).toBe(4);
    expect(subtaskPriorityLevel("None")).toBe(0);
  });

  it("returns null for null / unknown strings", () => {
    expect(subtaskPriorityLevel(null)).toBeNull();
    expect(subtaskPriorityLevel("bogus")).toBeNull();
  });
});
