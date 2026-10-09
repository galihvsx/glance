import type { BurndownDay } from "./types";

/** Pure SVG layout math for the cycle burndown chart (no DOM). */

export const CHART_W = 640;
export const CHART_H = 280;
const MARGIN = { top: 12, right: 12, bottom: 30, left: 36 };

const MONTHS = [
  "Jan", "Feb", "Mar", "Apr", "May", "Jun",
  "Jul", "Aug", "Sep", "Oct", "Nov", "Dec",
];

/** "2026-10-09" → "Oct 9". No Date parsing (avoids TZ shifts). */
export function formatDayLabel(isoDate: string): string {
  const [y, m, d] = isoDate.split("-").map(Number);
  if (!y || !m || !d) return isoDate;
  return `${MONTHS[m - 1]} ${d}`;
}

export interface ChartPoint {
  x: number;
  y: number;
}

export interface ChartTick extends ChartPoint {
  label: string;
}

export interface BurndownLayout {
  width: number;
  height: number;
  yMax: number;
  ideal: ChartPoint[];
  actual: ChartPoint[];
  xTicks: ChartTick[];
  yTicks: ChartTick[];
}

/**
 * Map burndown days onto a 640×280 SVG coordinate space.
 * - `actual` only includes days with non-null remaining (no line into the future).
 * - x ticks are thinned to at most 6 labels; y ticks are integers 0..yMax (max 6).
 */
export function layoutBurndown(
  days: BurndownDay[],
  totalScope: number,
): BurndownLayout {
  const { top, right, bottom, left } = MARGIN;
  const innerW = CHART_W - left - right;
  const innerH = CHART_H - top - bottom;

  const maxRemaining = days.reduce(
    (m, d) => (d.remaining != null ? Math.max(m, d.remaining) : m),
    0,
  );
  const yMax = Math.max(1, totalScope, maxRemaining);

  const n = days.length;
  const x = (i: number) =>
    n === 1 ? left + innerW / 2 : left + (i * innerW) / (n - 1);
  const y = (v: number) => top + innerH * (1 - v / yMax);

  const ideal = days.map((d, i) => ({ x: x(i), y: y(d.ideal) }));
  const actual = days
    .map((d, i) => ({ d, i }))
    .filter(({ d }) => d.remaining != null)
    .map(({ d, i }) => ({ x: x(i), y: y(d.remaining as number) }));

  // X ticks: at most 6, always including first and last.
  const xTicks: ChartTick[] = [];
  if (n > 0) {
    const step = Math.max(1, Math.ceil(n / 6));
    const idxs = new Set<number>();
    for (let i = 0; i < n; i += step) idxs.add(i);
    idxs.add(n - 1);
    for (const i of [...idxs].sort((a, b) => a - b)) {
      xTicks.push({ x: x(i), y: top + innerH, label: formatDayLabel(days[i].date) });
    }
  }

  // Y ticks: integers, at most 6.
  const yTicks: ChartTick[] = [];
  const yStep = Math.max(1, Math.ceil(yMax / 5));
  for (let v = 0; v <= yMax; v += yStep) {
    yTicks.push({ x: left, y: y(v), label: String(v) });
  }
  if (yTicks[yTicks.length - 1]?.label !== String(yMax)) {
    yTicks.push({ x: left, y: y(yMax), label: String(yMax) });
  }

  return { width: CHART_W, height: CHART_H, yMax, ideal, actual, xTicks, yTicks };
}
