import { describe, expect, it } from "vitest";
import { isTypingTarget } from "./shortcuts";

function fakeEl(tagName: string, isContentEditable = false): EventTarget {
  return { tagName, isContentEditable } as unknown as EventTarget;
}

describe("isTypingTarget", () => {
  it("detects text-entry elements", () => {
    expect(isTypingTarget(fakeEl("INPUT"))).toBe(true);
    expect(isTypingTarget(fakeEl("TEXTAREA"))).toBe(true);
    expect(isTypingTarget(fakeEl("SELECT"))).toBe(true);
    // tiptap and other rich editors
    expect(isTypingTarget(fakeEl("DIV", true))).toBe(true);
  });
  it("ignores everything else", () => {
    expect(isTypingTarget(fakeEl("DIV"))).toBe(false);
    expect(isTypingTarget(fakeEl("BODY"))).toBe(false);
    expect(isTypingTarget(fakeEl("BUTTON"))).toBe(false);
    expect(isTypingTarget(null)).toBe(false);
    expect(isTypingTarget({} as EventTarget)).toBe(false);
  });
});
