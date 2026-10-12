import { describe, expect, it } from "vitest";
import { convertIssuePath } from "./useConvertIssue";

describe("convertIssuePath", () => {
  it("builds the C24T1 convert endpoint path", () => {
    expect(convertIssuePath("acme", "ENG", "123e4567-e89b-12d3-a456-426614174000")).toBe(
      "/api/v1/workspaces/acme/projects/ENG/issues/123e4567-e89b-12d3-a456-426614174000/convert",
    );
  });

  it("URL-encodes path segments", () => {
    expect(convertIssuePath("my ws", "ENG", "a/b")).toBe(
      "/api/v1/workspaces/my%20ws/projects/ENG/issues/a%2Fb/convert",
    );
  });
});
