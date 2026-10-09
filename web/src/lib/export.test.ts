import { describe, expect, it } from "vitest";
import {
  buildExportUrl,
  exportFilenameFromHeader,
  type ExportFormat,
} from "./export";
import { EMPTY_FILTERS, type IssueFilters } from "./filters";

const FILTERS: IssueFilters = {
  ...EMPTY_FILTERS,
  q: "login bug",
  state: "state-uuid-1",
  priorities: [3, 1],
  labels: ["label-uuid-1"],
  assignees: ["user-uuid-1", "none"],
  archived: true,
};

describe("buildExportUrl", () => {
  it("points at the export endpoint with the format param", () => {
    for (const format of ["csv", "json"] as ExportFormat[]) {
      const url = buildExportUrl("acme", "ENG", EMPTY_FILTERS, format);
      expect(url).toBe(
        "/api/v1/workspaces/acme/projects/ENG/issues/export?format=" + format,
      );
    }
  });

  it("carries the list filter params so the export matches the view", () => {
    const url = buildExportUrl("acme", "ENG", FILTERS, "csv");
    const query = new URLSearchParams(url.split("?")[1]);
    expect(query.get("format")).toBe("csv");
    expect(query.get("q")).toBe("login bug");
    expect(query.get("state")).toBe("state-uuid-1");
    expect(query.get("priority")).toBe("1,3");
    expect(query.get("label")).toBe("label-uuid-1");
    expect(query.get("assignee")).toBe("user-uuid-1,none");
    expect(query.get("archived")).toBe("1");
  });

  it("does not add list-only pagination params", () => {
    const url = buildExportUrl("acme", "ENG", FILTERS, "json");
    const query = new URLSearchParams(url.split("?")[1]);
    expect(query.has("per_page")).toBe(false);
    expect(query.has("cursor")).toBe(false);
    expect(query.has("order_by")).toBe(false);
  });

  it("encodes slug and identifier", () => {
    const url = buildExportUrl("my ws", "ENG", EMPTY_FILTERS, "csv");
    expect(url).toContain("/workspaces/my%20ws/projects/ENG/");
  });
});

describe("exportFilenameFromHeader", () => {
  it("extracts the quoted filename", () => {
    expect(
      exportFilenameFromHeader(
        'attachment; filename="glance-ENG-issues-2026-10-09.csv"',
      ),
    ).toBe("glance-ENG-issues-2026-10-09.csv");
  });

  it("returns null when absent", () => {
    expect(exportFilenameFromHeader(null)).toBeNull();
    expect(exportFilenameFromHeader("attachment")).toBeNull();
  });
});
