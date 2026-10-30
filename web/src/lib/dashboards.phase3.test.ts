// C84T1: phase-3 lib helpers — row-list types, drillable types,
// row-list config defaults, and the row-list page count.
import { describe, expect, it } from "vitest";
import {
  isDrillableType,
  isRowListType,
  normalizeWidgetConfig,
  ROW_COLUMNS,
  widgetDrilldownKey,
  widgetRowPageCount,
  type WidgetData,
} from "./dashboards";

describe("phase-3 row-list helpers", () => {
  it("classifies the two row-list types", () => {
    expect(isRowListType("work_items_table")).toBe(true);
    expect(isRowListType("assigned_to_me")).toBe(true);
    expect(isRowListType("bar")).toBe(false);
    expect(isRowListType("table_2d")).toBe(false);
  });

  it("marks every group-based widget drillable and the rest not", () => {
    for (const t of [
      "bar",
      "line",
      "area",
      "donut",
      "pie",
      "smart_counter",
      "smart_gauge",
      "table_2d",
    ] as const) {
      expect(isDrillableType(t)).toBe(true);
    }
    for (const t of [
      "number",
      "intake_breakdown",
      "work_items_table",
      "assigned_to_me",
    ] as const) {
      expect(isDrillableType(t)).toBe(false);
    }
  });

  it("defaults row-list columns to all seven in display order", () => {
    expect(normalizeWidgetConfig({}).columns).toEqual(ROW_COLUMNS);
    expect(ROW_COLUMNS).toEqual([
      "id",
      "title",
      "state",
      "priority",
      "assignee",
      "labels",
      "project",
    ]);
  });

  it("defaults row-list sort to state desc", () => {
    const cfg = normalizeWidgetConfig({});
    expect(cfg.sort_by).toBe("state");
    expect(cfg.sort_dir).toBe("desc");
  });

  it("keeps a stored column subset and sort", () => {
    const cfg = normalizeWidgetConfig({
      columns: ["title", "priority"],
      sort_by: "priority",
      sort_dir: "asc",
    });
    expect(cfg.columns).toEqual(["title", "priority"]);
    expect(cfg.sort_by).toBe("priority");
    expect(cfg.sort_dir).toBe("asc");
  });

  it("falls back when sort_by is not one of the kept columns", () => {
    const cfg = normalizeWidgetConfig({
      columns: ["title", "priority"],
      sort_by: "state",
    });
    expect(cfg.sort_by).toBe("title");
  });

  it("counts row-list pages from total / max_rows", () => {
    const data: WidgetData = { rows: [], page: 1, max_rows: 10, total: 25 };
    expect(widgetRowPageCount(data)).toBe(3);
    expect(widgetRowPageCount({ rows: [], page: 1, max_rows: 10, total: 10 })).toBe(1);
    expect(widgetRowPageCount(undefined)).toBe(1);
  });

  it("keys drill-down queries per widget, group, and page", () => {
    expect(widgetDrilldownKey("w1", "Triage", 2)).toEqual([
      "widget-drilldown",
      "w1",
      "Triage",
      2,
    ]);
  });
});
