// profile lib tests (C6T7): initials fallback + device label guessing.

import { describe, expect, it } from "vitest";
import { deviceLabel, initials } from "./profile";

describe("initials", () => {
  it("uses the first two name words", () => {
    expect(initials("Galih Putro Aji", "x@y.z")).toBe("GP");
    expect(initials("Ada", "a@b.c")).toBe("A");
  });
  it("falls back to the email local part", () => {
    expect(initials(null, "galih@example.com")).toBe("GA");
    expect(initials("  ", "bo@example.com")).toBe("BO");
  });
  it("never returns empty", () => {
    expect(initials(null, "@")).toBe("?");
  });
});

describe("deviceLabel", () => {
  it("guesses browser + OS", () => {
    expect(
      deviceLabel(
        "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/120.0 Safari/537.36",
      ),
    ).toBe("Chrome · Windows");
    expect(
      deviceLabel(
        "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) Version/17.0 Safari/604.1",
      ),
    ).toBe("Safari · iOS");
  });
  it("handles unknown agents", () => {
    expect(deviceLabel(null)).toBe("Unknown device");
    expect(deviceLabel("curl/8.0")).toBe("Unknown device");
  });
});
