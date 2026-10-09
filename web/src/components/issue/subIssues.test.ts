import { describe, expect, it } from "vitest";
import { childRows, removeChildMessage, subIssuesTitle } from "./subIssues";
import type { IssueChild } from "../../lib/types";

const child = (over: Partial<IssueChild> = {}): IssueChild => ({
  uuid: "uuid-1",
  identifier: "GLA-7",
  title: "Fix the flaky test",
  state: "In Progress",
  ...over,
});

describe("childRows", () => {
  it("returns an empty array when children is undefined (no ?include_children=1)", () => {
    expect(childRows(undefined)).toEqual([]);
  });

  it("passes the children through untouched", () => {
    const children = [child(), child({ uuid: "uuid-2" })];
    expect(childRows(children)).toEqual(children);
  });
});

describe("subIssuesTitle", () => {
  it("is plain when there are no children", () => {
    expect(subIssuesTitle([])).toBe("Sub-issues");
  });

  it("carries the count when children exist", () => {
    expect(subIssuesTitle([child(), child({ uuid: "uuid-2" })])).toBe(
      "Sub-issues (2)",
    );
  });
});

describe("removeChildMessage", () => {
  it("names the child and states the outcome", () => {
    const msg = removeChildMessage("Fix the flaky test");
    expect(msg).toContain("Fix the flaky test");
    expect(msg).toContain("top-level issue");
  });
});
