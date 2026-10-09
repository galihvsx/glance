/** Analytics data transforms (C4T4). Pure functions for the hand-rolled
 *  SVG charts on the Analytics page. Zero dependencies; covered by
 *  analytics.test.ts. */

/** Backend shapes (mirror of internal/service/analytics.go). */
export interface AnalyticsSummary {
  by_state: Record<string, number>;
  by_priority: Record<string, number>;
  by_label: Record<string, number>;
  overdue_count: number;
  estimate_total: number;
  estimate_done: number;
}

export interface AnalyticsBurndownDay {
  date: string;
  remaining: number | null;
  remaining_estimate: number | null;
}

export interface AnalyticsBurndown {
  start_date: string;
  end_date: string;
  total_scope: number;
  days: AnalyticsBurndownDay[];
}

export interface AnalyticsTrendDay {
  date: string;
  created: number;
  closed: number;
}

/** Priority colors (0=None … 4=Urgent), readable on light and dark. */
export const PRIORITY_COLORS = [
  "#9ca3af",
  "#60a5fa",
  "#fbbf24",
  "#fb923c",
  "#f87171",
] as const;

export function priorityColor(p: number): string {
  return PRIORITY_COLORS[p] ?? "#9ca3af";
}

/** One donut slice: annular-sector path between two angles (radians). */
export interface DonutSlice {
  label: string;
  value: number;
  color: string;
  /** 0–1 fraction of the whole. */
  fraction: number;
  /** SVG path for the annular sector (empty when fraction >= 1 — the
   *  caller should render a full ring instead). */
  path: string;
}

function polar(cx: number, cy: number, r: number, angle: number): [number, number] {
  return [cx + r * Math.cos(angle), cy + r * Math.sin(angle)];
}

/** Annular-sector path. Angles in radians, measured from 12 o'clock. */
export function donutSectorPath(
  cx: number,
  cy: number,
  r: number,
  innerR: number,
  startAngle: number,
  endAngle: number,
): string {
  const a0 = startAngle - Math.PI / 2;
  const a1 = endAngle - Math.PI / 2;
  const [ox0, oy0] = polar(cx, cy, r, a0);
  const [ox1, oy1] = polar(cx, cy, r, a1);
  const [ix1, iy1] = polar(cx, cy, innerR, a1);
  const [ix0, iy0] = polar(cx, cy, innerR, a0);
  const large = endAngle - startAngle > Math.PI ? 1 : 0;
  return [
    `M ${ox0.toFixed(2)} ${oy0.toFixed(2)}`,
    `A ${r} ${r} 0 ${large} 1 ${ox1.toFixed(2)} ${oy1.toFixed(2)}`,
    `L ${ix1.toFixed(2)} ${iy1.toFixed(2)}`,
    `A ${innerR} ${innerR} 0 ${large} 0 ${ix0.toFixed(2)} ${iy0.toFixed(2)}`,
    "Z",
  ].join(" ");
}

export interface DonutEntry {
  label: string;
  value: number;
  color: string;
}

/** Lay out donut slices; zero-value entries are dropped, the rest keep
 *  input order. Returns an empty array for empty/all-zero input. */
export function donutSlices(
  entries: DonutEntry[],
  cx: number,
  cy: number,
  r: number,
  innerR: number,
): DonutSlice[] {
  const kept = entries.filter((e) => e.value > 0);
  const total = kept.reduce((s, e) => s + e.value, 0);
  if (total <= 0) return [];
  let angle = 0;
  return kept.map((e) => {
    const fraction = e.value / total;
    const start = angle;
    angle += fraction * Math.PI * 2;
    const path =
      fraction >= 1 ? "" : donutSectorPath(cx, cy, r, innerR, start, angle);
    return { label: e.label, value: e.value, color: e.color, fraction, path };
  });
}

/** Proportional bar widths for a horizontal bar chart. */
export function barWidths(values: number[], maxWidth: number): number[] {
  const max = Math.max(0, ...values);
  if (max <= 0) return values.map(() => 0);
  return values.map((v) => Math.max(0, (v / max) * maxWidth));
}

export interface LinePoint {
  x: number;
  y: number;
  date: string;
  value: number;
}

export interface LineLayout {
  width: number;
  height: number;
  yMax: number;
  points: LinePoint[];
  /** SVG path through the points (empty when < 2 points). */
  path: string;
  /** Y tick values for axis labels. */
  ticks: number[];
}

/** Lay out a burndown line chart. Days with null values (future) are
 *  skipped. `getValue` picks the metric (count or estimate). */
export function burndownLine(
  days: AnalyticsBurndownDay[],
  getValue: (d: AnalyticsBurndownDay) => number | null,
  width = 640,
  height = 260,
  pad = { l: 36, r: 12, t: 12, b: 28 },
): LineLayout {
  const vals = days.map(getValue);
  const present = vals.filter((v): v is number => v !== null);
  const yMax = Math.max(1, ...present);
  const innerW = width - pad.l - pad.r;
  const innerH = height - pad.t - pad.b;
  const n = Math.max(1, days.length - 1);
  const points: LinePoint[] = [];
  days.forEach((d, i) => {
    const v = vals[i];
    if (v === null) return;
    points.push({
      x: pad.l + (i / n) * innerW,
      y: pad.t + innerH - (v / yMax) * innerH,
      date: d.date,
      value: v,
    });
  });
  const path =
    points.length >= 2
      ? points
          .map(
            (p, i) =>
              `${i === 0 ? "M" : "L"} ${p.x.toFixed(1)} ${p.y.toFixed(1)}`,
          )
          .join(" ")
      : "";
  // 4 ticks: 0, yMax/3, 2*yMax/3, yMax (rounded).
  const ticks = [0, 1, 2, 3].map((i) => Math.round((yMax * i) / 3));
  return { width, height, yMax, points, path, ticks };
}

export interface TrendBar {
  date: string;
  created: number;
  closed: number;
  /** X of the group start; each group holds two bars of barW. */
  x: number;
  barW: number;
  createdH: number;
  closedH: number;
  /** Y of the baseline. */
  baseY: number;
}

/** Lay out grouped created/closed bars. */
export function trendBars(
  days: AnalyticsTrendDay[],
  width = 640,
  height = 260,
  pad = { l: 36, r: 12, t: 12, b: 28 },
): { bars: TrendBar[]; yMax: number; ticks: number[]; baseY: number } {
  const yMax = Math.max(1, ...days.map((d) => Math.max(d.created, d.closed)));
  const innerW = width - pad.l - pad.r;
  const innerH = height - pad.t - pad.b;
  const baseY = pad.t + innerH;
  const slot = days.length > 0 ? innerW / days.length : 0;
  const barW = Math.max(2, Math.min(18, (slot - 6) / 2));
  const gap = Math.max(2, (slot - barW * 2) / 2);
  const bars = days.map((d, i) => ({
    date: d.date,
    created: d.created,
    closed: d.closed,
    x: pad.l + i * slot + gap,
    barW,
    createdH: (d.created / yMax) * innerH,
    closedH: (d.closed / yMax) * innerH,
    baseY,
  }));
  const ticks = [0, 1, 2, 3].map((i) => Math.round((yMax * i) / 3));
  return { bars, yMax, ticks, baseY };
}

/** Resolve backend count maps (keyed by UUID / numeric string) to
 *  display-ready rows via a lookup map. Unknown ids keep the raw id as
 *  label and fall back to `fallbackColor`. Sorted by value desc. */
export function resolveCounts(
  byId: Record<string, number>,
  lookup: Map<string, { name: string; color: string }>,
  fallbackColor = "#9ca3af",
): { id: string; label: string; value: number; color: string }[] {
  const rows = Object.entries(byId).map(([id, value]) => {
    const hit = lookup.get(id);
    return {
      id,
      label: hit?.name ?? id,
      value,
      color: hit?.color ?? fallbackColor,
    };
  });
  rows.sort((a, b) => b.value - a.value);
  return rows;
}

/** "Oct 9" from "2026-10-09"; passes through garbage (same contract as
 *  the burndown lib's formatDayLabel). */
const MONTHS = [
  "Jan",
  "Feb",
  "Mar",
  "Apr",
  "May",
  "Jun",
  "Jul",
  "Aug",
  "Sep",
  "Oct",
  "Nov",
  "Dec",
];
export function shortDate(iso: string): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})/.exec(iso);
  if (!m) return iso;
  const month = MONTHS[Number(m[2]) - 1];
  if (!month) return iso;
  return `${month} ${Number(m[3])}`;
}
