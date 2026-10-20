// C81T1: dashboard grid geometry + drag/resize math, pure and
// unit-testable (same pattern as lib/calendar-dnd.ts). The canvas is a
// 12-column responsive grid; widget positions are grid units (grid_x,
// grid_y, grid_w, grid_h) that the backend clamps to x/y >= 0 and
// w/h in 1..12 (mirrored here so drags never commit out-of-range
// values).

/** Grid columns (matches the backend's 12-column contract). */
export const GRID_COLS = 12;
/** Pixel height of one grid row unit. */
export const ROW_H = 56;
/** Pixel gap between cells. */
export const GRID_GAP = 12;

export interface GridLayout {
  grid_x: number;
  grid_y: number;
  grid_w: number;
  grid_h: number;
}

export interface GridGeometry {
  /** Pixel width of one column (content, excluding gaps). */
  colW: number;
  rowH: number;
  gap: number;
}

/** Pixel geometry for the canvas at a measured container width. */
export function gridGeometry(containerWidth: number, gap = GRID_GAP): GridGeometry {
  const colW = Math.max(0, (containerWidth - gap * (GRID_COLS - 1)) / GRID_COLS);
  return { colW, rowH: ROW_H, gap };
}

/** Absolute CSS position for a widget (px). */
export function widgetStyle(
  layout: GridLayout,
  geo: GridGeometry,
): { left: number; top: number; width: number; height: number } {
  const { colW, rowH, gap } = geo;
  return {
    left: layout.grid_x * (colW + gap),
    top: layout.grid_y * (rowH + gap),
    width: layout.grid_w * colW + (layout.grid_w - 1) * gap,
    height: layout.grid_h * rowH + (layout.grid_h - 1) * gap,
  };
}

/** Canvas height that fits all widgets (px). */
export function canvasHeight(
  layouts: GridLayout[],
  geo: GridGeometry,
): number {
  const bottom = layouts.reduce(
    (m, l) => Math.max(m, l.grid_y + l.grid_h),
    0,
  );
  if (bottom === 0) return 0;
  return bottom * geo.rowH + (bottom - 1) * geo.gap;
}

/** Clamp a layout the way the backend does: x/y >= 0, w/h in 1..12. */
export function clampLayout(layout: GridLayout): GridLayout {
  return {
    grid_x: Math.max(0, Math.floor(layout.grid_x)),
    grid_y: Math.max(0, Math.floor(layout.grid_y)),
    grid_w: Math.min(GRID_COLS, Math.max(1, Math.floor(layout.grid_w))),
    grid_h: Math.min(GRID_COLS, Math.max(1, Math.floor(layout.grid_h))),
  };
}

/** Grid units crossed by a pixel delta on the canvas. */
function deltaUnits(deltaPx: number, cellPx: number): number {
  return Math.round(deltaPx / cellPx);
}

/**
 * Resolve dropping a dragged widget: new grid_x/grid_y from the drag
 * delta, clamped to the grid and the widget's own width (x <= 12 - w).
 * Returns null when the drop changes nothing (no PATCH).
 */
export function resolveDropLayout(
  layout: GridLayout,
  deltaX: number,
  deltaY: number,
  geo: GridGeometry,
): { grid_x: number; grid_y: number } | null {
  const stepX = geo.colW + geo.gap;
  const stepY = geo.rowH + geo.gap;
  const grid_x = Math.min(
    GRID_COLS - layout.grid_w,
    Math.max(0, layout.grid_x + deltaUnits(deltaX, stepX)),
  );
  const grid_y = Math.max(0, layout.grid_y + deltaUnits(deltaY, stepY));
  if (grid_x === layout.grid_x && grid_y === layout.grid_y) return null;
  return { grid_x, grid_y };
}

/**
 * Resolve a corner-resize drag: new grid_w/grid_h from the drag delta,
 * clamped 1..12. Returns null when the resize changes nothing (no PATCH).
 */
export function resolveResizeLayout(
  layout: GridLayout,
  deltaX: number,
  deltaY: number,
  geo: GridGeometry,
): { grid_w: number; grid_h: number } | null {
  const stepX = geo.colW + geo.gap;
  const stepY = geo.rowH + geo.gap;
  const grid_w = Math.min(
    GRID_COLS,
    Math.max(1, layout.grid_w + deltaUnits(deltaX, stepX)),
  );
  const grid_h = Math.min(
    GRID_COLS,
    Math.max(1, layout.grid_h + deltaUnits(deltaY, stepY)),
  );
  if (grid_w === layout.grid_w && grid_h === layout.grid_h) return null;
  return { grid_w, grid_h };
}

/**
 * Placement for a newly added widget: first column of the row below the
 * lowest existing widget (avoids stacking everything on (0,0), the
 * backend's blind default). Default size 6x4 (half width, 4 rows).
 */
export function nextWidgetPosition(layouts: GridLayout[]): {
  grid_x: number;
  grid_y: number;
  grid_w: number;
  grid_h: number;
} {
  const bottom = layouts.reduce((m, l) => Math.max(m, l.grid_y + l.grid_h), 0);
  return { grid_x: 0, grid_y: bottom, grid_w: 6, grid_h: 4 };
}
