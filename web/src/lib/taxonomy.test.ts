// taxonomy lib tests (C6T2): pure helpers — hex color validation, state
// group labels/vocabulary, and the estimate point-draft parser.

import { describe, expect, it } from "vitest";
import {
  STATE_GROUPS,
  groupLabel,
  isValidHexColor,
} from "./taxonomy";
import { toPoints } from "../components/settings/EstimatesSection";

describe("isValidHexColor", () => {
  it("accepts #rrggbb", () => {
    expect(isValidHexColor("#ef4444")).toBe(true);
    expect(isValidHexColor("#A78BFA")).toBe(true);
    expect(isValidHexColor("#000000")).toBe(true);
  });
  it("rejects non-hex input", () => {
    expect(isValidHexColor("red")).toBe(false);
    expect(isValidHexColor("#fff")).toBe(false);
    expect(isValidHexColor("#gggggg")).toBe(false);
    expect(isValidHexColor("")).toBe(false);
    expect(isValidHexColor("  #ef4444  ")).toBe(true); // trimmed, like the backend
  });
});

describe("state groups", () => {
  it("covers the backend CHECK vocabulary in kanban order", () => {
    expect([...STATE_GROUPS]).toEqual([
      "triage",
      "backlog",
      "unstarted",
      "started",
      "completed",
      "cancelled",
    ]);
  });
  it("labels every group", () => {
    expect(groupLabel("triage")).toBe("Triage");
    expect(groupLabel("backlog")).toBe("Backlog");
    expect(groupLabel("unstarted")).toBe("To do");
    expect(groupLabel("started")).toBe("In progress");
    expect(groupLabel("completed")).toBe("Done");
    expect(groupLabel("cancelled")).toBe("Cancelled");
  });
  it("passes unknown groups through", () => {
    expect(groupLabel("weird")).toBe("weird");
  });
});

describe("toPoints", () => {
  it("parses valid rows", () => {
    expect(
      toPoints([
        { key: "1", value: "1" },
        { key: " 3 ", value: "3" },
      ]),
    ).toEqual([
      { key: "1", value: 1 },
      { key: "3", value: 3 },
    ]);
  });
  it("drops rows with empty keys or non-numeric values", () => {
    expect(
      toPoints([
        { key: "", value: "1" },
        { key: "x", value: "abc" },
        { key: "  ", value: "" },
        { key: "M", value: "2" },
      ]),
    ).toEqual([{ key: "M", value: 2 }]);
  });
  it("returns empty for empty drafts", () => {
    expect(toPoints([])).toEqual([]);
    expect(toPoints([{ key: "", value: "" }])).toEqual([]);
  });
});
