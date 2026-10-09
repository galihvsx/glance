import { describe, expect, it } from "vitest";
import { formatBytes, formatDateTime } from "./format";

describe("formatBytes", () => {
  it("formats bytes below 1 KB", () => {
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(1023)).toBe("1023 B");
  });

  it("formats KB/MB/GB with one decimal under 100", () => {
    expect(formatBytes(1024)).toBe("1 KB");
    expect(formatBytes(1536)).toBe("1.5 KB");
    expect(formatBytes(25 * 1024 * 1024)).toBe("25 MB");
    expect(formatBytes(2.5 * 1024 * 1024 * 1024)).toBe("2.5 GB");
  });

  it("rounds to whole numbers at 100+ units", () => {
    expect(formatBytes(150 * 1024)).toBe("150 KB");
  });

  it("never returns empty/NaN for garbage input", () => {
    expect(formatBytes(-1)).toBe("0 B");
    expect(formatBytes(NaN)).toBe("0 B");
    expect(formatBytes(Infinity)).toBe("0 B");
  });
});

describe("formatDateTime", () => {
  it("formats an ISO timestamp compactly", () => {
    // Timezone-independent assertions: check the shape, not the zone.
    const out = formatDateTime("2026-10-09T06:32:00.000Z");
    expect(out).toMatch(/^\d{1,2} \w{3} 2026, \d{2}:\d{2}$/);
  });

  it("passes unparseable input through instead of blanking", () => {
    expect(formatDateTime("not-a-date")).toBe("not-a-date");
    expect(formatDateTime("")).toBe("");
  });
});
