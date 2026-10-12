import { describe, expect, it } from "vitest";
import { mergeIssuePath } from "./useMergeIssue";

describe("mergeIssuePath", () => {
  it("builds the C25T3 merge endpoint path", () => {
    expect(mergeIssuePath("acme", "ENG", "123e4567-e89b-12d3-a456-426614174000")).toBe(
      "/api/v1/workspaces/acme/projects/ENG/issues/123e4567-e89b-12d3-a456-426614174000/merge",
    );
  });

  it("URL-encodes path segments", () => {
    expect(mergeIssuePath("my ws", "ENG", "a/b")).toBe(
      "/api/v1/workspaces/my%20ws/projects/ENG/issues/a%2Fb/merge",
    );
  });
});
