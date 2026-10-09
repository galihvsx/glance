import { describe, expect, it } from "vitest";
import { cloneIssuePath } from "./useCloneIssue";

describe("cloneIssuePath", () => {
  it("builds the C8T4 clone endpoint path", () => {
    expect(cloneIssuePath("acme", "ENG", "123e4567-e89b-12d3-a456-426614174000")).toBe(
      "/api/v1/workspaces/acme/projects/ENG/issues/123e4567-e89b-12d3-a456-426614174000/clone",
    );
  });

  it("URL-encodes path segments", () => {
    expect(cloneIssuePath("my ws", "ENG", "a/b")).toBe(
      "/api/v1/workspaces/my%20ws/projects/ENG/issues/a%2Fb/clone",
    );
  });
});
