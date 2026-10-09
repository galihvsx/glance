import { describe, expect, it } from "vitest";
import { relativeTime } from "./relativeTime";

// Fixed "now" so the cases are deterministic regardless of wall clock.
const NOW = new Date("2026-10-09T12:00:00.000Z");
const iso = (msAgo: number) => new Date(NOW.getTime() - msAgo).toISOString();

describe("relativeTime", () => {
  it("returns just now for sub-minute and future timestamps", () => {
    expect(relativeTime(iso(0), NOW)).toBe("just now");
    expect(relativeTime(iso(59_000), NOW)).toBe("just now");
    // Future (clock skew) must never render "in Xm".
    expect(relativeTime(iso(-60_000), NOW)).toBe("just now");
  });

  it("uses minutes under an hour", () => {
    expect(relativeTime(iso(60_000), NOW)).toBe("1m ago");
    expect(relativeTime(iso(45 * 60_000), NOW)).toBe("45m ago");
  });

  it("uses hours under a day", () => {
    expect(relativeTime(iso(60 * 60_000), NOW)).toBe("1h ago");
    expect(relativeTime(iso(23 * 60 * 60_000), NOW)).toBe("23h ago");
  });

  it("uses days under a week, weeks under 30 days", () => {
    expect(relativeTime(iso(24 * 60 * 60_000), NOW)).toBe("1d ago");
    expect(relativeTime(iso(6 * 24 * 60 * 60_000), NOW)).toBe("6d ago");
    expect(relativeTime(iso(7 * 24 * 60 * 60_000), NOW)).toBe("1w ago");
    expect(relativeTime(iso(21 * 24 * 60 * 60_000), NOW)).toBe("3w ago");
  });

  it("falls back to a calendar date past 30 days", () => {
    // 40 days before 2026-10-09 → 2026-08-30, same year: no year shown.
    expect(relativeTime(iso(40 * 24 * 60 * 60_000), NOW)).toBe("Aug 30");
    // Over a year back: year included.
    expect(relativeTime(iso(400 * 24 * 60 * 60_000), NOW)).toMatch(
      /2025/,
    );
  });

  it("returns the raw string for unparseable input", () => {
    expect(relativeTime("not-a-date", NOW)).toBe("not-a-date");
  });
});
