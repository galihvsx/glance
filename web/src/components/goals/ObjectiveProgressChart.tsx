// ObjectiveProgressChart (C47T2): objective-level progress-over-time
// chart for GoalDetail. Pure SVG, same visual language + guards as
// the C42 KR chart and the C43 GoalSparkline (accent-blue line, zero
// time range → index-spread, zero value range → midline, single
// point → dot, empty → null). Fed ONLY by
// `objective.progress_history` from the detail response — no fetch.
// Each point is the objective's mean KR progress (0–1) at that
// timestamp, so this is the only chart that shows anything for
// issue-only objectives (issue KRs have no check-ins and no
// per-KR chart is duplicated for them).
import type { ObjectiveProgressPoint } from "../../lib/types";
import { formatProgressPct } from "./okrFormat";

export interface ObjectiveProgressChartProps {
  /** Progress-over-time points from `objective.progress_history`. */
  points: ObjectiveProgressPoint[];
}

function clampProgress(p: number): number {
  if (!Number.isFinite(p)) return 0;
  return Math.min(1, Math.max(0, p));
}

/** Short axis date for the progress chart: "14 Oct 2026". Falls back
 *  to the raw string when it doesn't parse (never blank). */
function formatChartDate(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const month = d.toLocaleString("en-GB", { month: "short" });
  return `${d.getDate()} ${month} ${d.getFullYear()}`;
}

export default function ObjectiveProgressChart({
  points,
}: ObjectiveProgressChartProps) {
  if (points.length === 0) return null;
  const values = points.map((p) => clampProgress(p.progress));
  const times = points.map((p) => Date.parse(p.t));
  const allTimesFinite = times.every((t) => Number.isFinite(t));

  const W = 320;
  const H = 120;
  const LEFT = 44;
  const RIGHT = 8;
  const TOP = 8;
  const BOTTOM = 22;
  const plotW = W - LEFT - RIGHT;
  const plotH = H - TOP - BOTTOM;

  const tMin = Math.min(...times);
  const tMax = Math.max(...times);
  const timeRange = allTimesFinite ? tMax - tMin : 0;

  const lo = Math.min(...values);
  const hi = Math.max(...values);
  const valueRange = hi - lo;

  const x = (i: number) => {
    if (timeRange > 0) {
      return LEFT + ((times[i] - tMin) / timeRange) * plotW;
    }
    return points.length === 1
      ? LEFT + plotW / 2
      : LEFT + (i / (points.length - 1)) * plotW;
  };
  const y = (v: number) =>
    valueRange > 0 ? TOP + ((hi - v) / valueRange) * plotH : TOP + plotH / 2;

  const path = points
    .map(
      (_, i) =>
        `${i === 0 ? "M" : "L"}${x(i).toFixed(1)},${y(values[i]).toFixed(1)}`,
    )
    .join(" ");
  const firstPct = formatProgressPct(values[0]);
  const lastPct = formatProgressPct(values[values.length - 1]);
  const label = `Objective progress over time: ${firstPct} → ${lastPct}`;

  return (
    <div className="mt-2" data-testid="objective-progress-chart">
      <svg
        viewBox={`0 0 ${W} ${H}`}
        className="h-auto w-full"
        role="img"
        aria-label={label}
      >
        <title>{label}</title>
        <text
          x={LEFT - 6}
          y={TOP + 3}
          textAnchor="end"
          fontSize={11}
          className="fill-ink-muted tabular-nums"
        >
          {formatProgressPct(hi)}
        </text>
        <text
          x={LEFT - 6}
          y={TOP + plotH + 3}
          textAnchor="end"
          fontSize={11}
          className="fill-ink-muted tabular-nums"
        >
          {formatProgressPct(lo)}
        </text>
        {points.length > 1 ? (
          <path
            data-testid="objective-chart-line"
            d={path}
            fill="none"
            className="stroke-accent-blue"
            strokeWidth={2}
          />
        ) : null}
        {points.map((p, i) => (
          <circle
            key={`${p.t}-${i}`}
            data-testid="objective-chart-point"
            cx={x(i)}
            cy={y(values[i])}
            r={3}
            className="fill-accent-blue"
          />
        ))}
        <text
          x={LEFT}
          y={H - 6}
          textAnchor="start"
          fontSize={11}
          className="fill-ink-muted tabular-nums"
        >
          {formatChartDate(points[0].t)}
        </text>
        {points.length > 1 ? (
          <text
            x={W - RIGHT}
            y={H - 6}
            textAnchor="end"
            fontSize={11}
            className="fill-ink-muted tabular-nums"
          >
            {formatChartDate(points[points.length - 1].t)}
          </text>
        ) : null}
      </svg>
    </div>
  );
}
