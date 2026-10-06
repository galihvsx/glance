import { describe, expect, it } from "vitest";
import { parseDisplayId } from "./displayId";

describe("parseDisplayId", () => {
  it("parses ENG-123", () => {
    expect(parseDisplayId("ENG-123")).toEqual({ identifier: "ENG", sequence: 123 });
  });
  it("is case-insensitive and trims whitespace", () => {
    expect(parseDisplayId("  eng-7 ")).toEqual({ identifier: "ENG", sequence: 7 });
  });
  it("rejects non-display-id shapes", () => {
    expect(parseDisplayId("ENG")).toBeNull();
    expect(parseDisplayId("ENG-")).toBeNull();
    expect(parseDisplayId("-123")).toBeNull();
    expect(parseDisplayId("ENG-0")).toBeNull();
    expect(parseDisplayId("ENG-abc")).toBeNull();
    expect(parseDisplayId("E-123")).toBeNull(); // identifier too short
    expect(parseDisplayId("ENG 123")).toBeNull();
    expect(parseDisplayId("")).toBeNull();
    expect(parseDisplayId("cmd k")).toBeNull();
  });
});
