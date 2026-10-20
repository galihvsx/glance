// C81T1: grid geometry + drag/resize resolution for the dashboard
// canvas.
import { describe, expect, it } from "vitest";
import {
  GRID_COLS,
  canvasHeight,
  clampLayout,
  gridGeometry,
  nextWidgetPosition,
  resolveDropLayout,
  resolveResizeLayout,
  widgetStyle,
} from "./dashboardGrid";

const geo = gridGeometry(1200); // colW = (1200 - 11*12)/12 = 89

describe("gridGeometry", () => {
  it("splits the container into 12 gap-aware columns", () => {
    expect(geo.colW).toBeCloseTo(89, 5);
    expect(geo.rowH).toBe(56);
    expect(geo.gap).toBe(12);
  });
});

describe("widgetStyle", () => {
  it("positions a 6x4 widget at the origin", () => {
    const s = widgetStyle({ grid_x: 0, grid_y: 0, grid_w: 6, grid_h: 4 }, geo);
    expect(s.left).toBe(0);
    expect(s.top).toBe(0);
    expect(s.width).toBeCloseTo(6 * 89 + 5 * 12, 5);
    expect(s.height).toBe(4 * 56 + 3 * 12);
  });

  it("offsets by gap-aware steps", () => {
    const s = widgetStyle({ grid_x: 6, grid_y: 2, grid_w: 6, grid_h: 2 }, geo);
    expect(s.left).toBeCloseTo(6 * (89 + 12), 5);
    expect(s.top).toBe(2 * (56 + 12));
  });
});

describe("canvasHeight", () => {
  it("is zero with no widgets", () => {
    expect(canvasHeight([], geo)).toBe(0);
  });

  it("fits the lowest widget bottom", () => {
    const h = canvasHeight(
      [
        { grid_x: 0, grid_y: 0, grid_w: 6, grid_h: 2 },
        { grid_x: 6, grid_y: 2, grid_w: 6, grid_h: 3 },
      ],
      geo,
    );
    expect(h).toBe(5 * 56 + 4 * 12);
  });
});

describe("clampLayout", () => {
  it("mirrors the backend: x/y >= 0, w/h in 1..12", () => {
    expect(
      clampLayout({ grid_x: -3, grid_y: -1, grid_w: 0, grid_h: 40 }),
    ).toEqual({ grid_x: 0, grid_y: 0, grid_w: 1, grid_h: 12 });
  });

  it("leaves in-range layouts untouched", () => {
    const l = { grid_x: 4, grid_y: 3, grid_w: 6, grid_h: 4 };
    expect(clampLayout(l)).toEqual(l);
  });
});

describe("resolveDropLayout", () => {
  const base = { grid_x: 0, grid_y: 0, grid_w: 6, grid_h: 4 };

  it("moves by whole grid units from the drag delta", () => {
    // stepX = 89+12 = 101; stepY = 56+12 = 68
    const r = resolveDropLayout(base, 2 * 101 + 20, 68, geo);
    expect(r).toEqual({ grid_x: 2, grid_y: 1 });
  });

  it("returns null when the drop changes nothing (no PATCH)", () => {
    expect(resolveDropLayout(base, 10, 5, geo)).toBeNull();
  });

  it("clamps x so the widget never overflows the 12-column grid", () => {
    const r = resolveDropLayout(base, 50 * 101, 0, geo);
    // 12 - 6 = 6 is the rightmost legal x for a 6-wide widget
    expect(r).toEqual({ grid_x: 6, grid_y: 0 });
  });

  it("clamps negative drags to zero", () => {
    const r = resolveDropLayout(base, -999, -999, geo);
    expect(r).toBeNull(); // already at 0,0 → no change
    const moved = resolveDropLayout(
      { ...base, grid_x: 3, grid_y: 4 },
      -999,
      -999,
      geo,
    );
    expect(moved).toEqual({ grid_x: 0, grid_y: 0 });
  });
});

describe("resolveResizeLayout", () => {
  const base = { grid_x: 0, grid_y: 0, grid_w: 6, grid_h: 4 };

  it("grows by whole units from the drag delta", () => {
    const r = resolveResizeLayout(base, 2 * 101, 68, geo);
    expect(r).toEqual({ grid_w: 8, grid_h: 5 });
  });

  it("shrinks but never below 1", () => {
    const r = resolveResizeLayout(base, -50 * 101, -50 * 68, geo);
    expect(r).toEqual({ grid_w: 1, grid_h: 1 });
  });

  it("caps at 12 columns", () => {
    const r = resolveResizeLayout(base, 50 * 101, 50 * 68, geo);
    expect(r).toEqual({ grid_w: 12, grid_h: 12 });
  });

  it("returns null when the resize changes nothing (no PATCH)", () => {
    expect(resolveResizeLayout(base, 10, 5, geo)).toBeNull();
  });
});

describe("nextWidgetPosition", () => {
  it("starts at the origin on an empty canvas", () => {
    expect(nextWidgetPosition([])).toEqual({
      grid_x: 0,
      grid_y: 0,
      grid_w: 6,
      grid_h: 4,
    });
  });

  it("places below the lowest existing widget", () => {
    const p = nextWidgetPosition([
      { grid_x: 0, grid_y: 0, grid_w: 12, grid_h: 3 },
      { grid_x: 0, grid_y: 5, grid_w: 6, grid_h: 2 },
    ]);
    expect(p.grid_y).toBe(7);
    expect(p.grid_x).toBe(0);
  });
});

describe("GRID_COLS", () => {
  it("is 12, matching the backend contract", () => {
    expect(GRID_COLS).toBe(12);
  });
});
