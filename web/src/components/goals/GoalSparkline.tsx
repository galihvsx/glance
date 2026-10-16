// GoalSparkline (C43T2): progress-over-time sparkline for one
// objective card on the Goals list. Pure SVG, same visual language
// + guards as the C42 GoalDetail KR chart (accent-blue line, zero
// time range → index-spread, zero value range → midline, single
// point → dot, empty → null) at sparkline scale. Fed ONLY by
// `objective.progress_history` from the list response — no fetch.

import type { ObjectiveProgressPoint } from "../../lib/types";

export interface GoalSparklineProps {
  /** Progress-over-time points from `objective.progress_history`. */
  points: ObjectiveProgressPoint[];
}

function clampProgress(p: number): number {
  if (!Number.isFinite(p)) return 0;
  return Math.min(1, Math.max(0, p));
}

export default function GoalSparkline({ points }: GoalSparklineProps) {
  if (points.length === 0) return null;
  const values = points.map((p) => clampProgress(p.progress));
  const times = points.map((p) => Date.parse(p.t));
  const allTimesFinite = times.every((t) => Number.isFinite(t));

  const W = 260;
  const H = 28;
  const TOP = 2;
  const BOTTOM = 2;
  const plotW = W;
  const plotH = H - TOP - BOTTOM;

  const tMin = Math.min(...times);
  const tMax = Math.max(...times);
  const timeRange = allTimesFinite ? tMax - tMin : 0;

  const lo = Math.min(...values);
  const hi = Math.max(...values);
  const valueRange = hi - lo;

  const x = (i: number) => {
    if (timeRange > 0) {
      return ((times[i] - tMin) / timeRange) * plotW;
    }
    return points.length === 1
      ? plotW / 2
      : (i / (points.length - 1)) * plotW;
  };
  const y = (v: number) =>
    valueRange > 0 ? TOP + ((hi - v) / valueRange) * plotH : TOP + plotH / 2;

  const path = points
    .map(
      (_, i) =>
        `${i === 0 ? "M" : "L"}${x(i).toFixed(1)},${y(values[i]).toFixed(1)}`,
    )
    .join(" ");
  const firstPct = Math.round(values[0] * 100);
  const lastPct = Math.round(values[values.length - 1] * 100);
  const label = `Progress history: ${firstPct}% → ${lastPct}%`;
  const last = points.length - 1;

  return (
    <div data-testid="goal-sparkline">
      <svg
        viewBox={`0 0 ${W} ${H}`}
        className="h-auto w-full"
        role="img"
        aria-label={label}
      >
        <title>{label}</title>
        {points.length > 1 ? (
          <path
            data-testid="goal-sparkline-line"
            d={path}
            fill="none"
            className="stroke-accent-blue"
            strokeWidth={1.5}
          />
        ) : null}
        <circle
          data-testid="goal-sparkline-dot"
          cx={x(last)}
          cy={y(values[last])}
          r={2.5}
          className="fill-accent-blue"
        />
      </svg>
    </div>
  );
}
