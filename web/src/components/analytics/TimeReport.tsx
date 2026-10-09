// Time report section for the Analytics page (C6T8): project worklog
// aggregation by day/week/user/issue over a selectable window.
// Hand-rolled SVG, zero new dependencies, same visual language as the
// rest of the Analytics page.

import { useMemo, useState } from "react";
import { useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { ApiError } from "../../lib/api";
import {
  fetchTimeSummary,
  formatDuration,
  timeSummaryKey,
} from "../../lib/timeReports";
import type { TimeGroupBy, TimeSummaryBucket } from "../../lib/timeReports";

const GROUP_OPTIONS: { value: TimeGroupBy; label: string }[] = [
  { value: "day", label: "Day" },
  { value: "week", label: "Week" },
  { value: "user", label: "User" },
  { value: "issue", label: "Issue" },
];
const DAY_OPTIONS = [7, 14, 30, 90];

const CHART_W = 640;
const CHART_H = 220;
const PAD = { l: 36, r: 12, t: 12, b: 28 };

/** Vertical bars for day/week buckets (zero-filled by the backend). */
function TimeBars({ buckets }: { buckets: TimeSummaryBucket[] }) {
  const { bars, ticks } = useMemo(() => {
    const yMax = Math.max(1, ...buckets.map((b) => b.seconds));
    const innerW = CHART_W - PAD.l - PAD.r;
    const innerH = CHART_H - PAD.t - PAD.b;
    const baseY = PAD.t + innerH;
    const slot = buckets.length > 0 ? innerW / buckets.length : 0;
    const barW = Math.max(2, Math.min(24, slot * 0.6));
    return {
      bars: buckets.map((b, i) => ({
        ...b,
        x: PAD.l + i * slot + (slot - barW) / 2,
        barW,
        h: (b.seconds / yMax) * innerH,
        baseY,
      })),
      ticks: [0, 1, 2, 3].map((i) => Math.round((yMax * i) / 3)),
      baseY,
    };
  }, [buckets]);

  return (
    <svg
      width="100%"
      viewBox={`0 0 ${CHART_W} ${CHART_H}`}
      role="img"
      aria-label="Logged time per bucket"
    >
      {ticks.map((t) => {
        const y =
          PAD.t + (CHART_H - PAD.t - PAD.b) - (t / Math.max(1, ...ticks)) * (CHART_H - PAD.t - PAD.b);
        return (
          <g key={t}>
            <line
              x1={PAD.l}
              x2={CHART_W - PAD.r}
              y1={y}
              y2={y}
              className="stroke-border"
              strokeDasharray="3 3"
            />
            <text
              x={PAD.l - 6}
              y={y + 4}
              textAnchor="end"
              className="fill-muted-foreground"
              fontSize={10}
            >
              {formatDuration(t)}
            </text>
          </g>
        );
      })}
      {bars.map((b) => (
        <g key={b.key}>
          <rect
            x={b.x}
            y={b.baseY - b.h}
            width={b.barW}
            height={Math.max(0, b.h)}
            className="fill-primary"
            rx={2}
          >
            <title>{`${b.label}: ${formatDuration(b.seconds)} (${b.entries} entries)`}</title>
          </rect>
        </g>
      ))}
    </svg>
  );
}

/** Horizontal bars for user/issue buckets (ranked, longest label wins). */
function RankedBars({ buckets }: { buckets: TimeSummaryBucket[] }) {
  const rows = useMemo(() => {
    const max = Math.max(1, ...buckets.map((b) => b.seconds));
    return buckets.slice(0, 12).map((b) => ({
      ...b,
      w: (b.seconds / max) * (CHART_W - 220),
    }));
  }, [buckets]);

  return (
    <svg
      width="100%"
      viewBox={`0 0 ${CHART_W} ${Math.max(60, rows.length * 28 + 12)}`}
      role="img"
      aria-label="Logged time ranked"
    >
      {rows.map((r, i) => (
        <g key={r.key}>
          <text
            x={0}
            y={i * 28 + 18}
            className="fill-muted-foreground"
            fontSize={11}
          >
            {r.label.length > 28 ? r.label.slice(0, 27) + "…" : r.label}
            <title>{r.label}</title>
          </text>
          <rect
            x={200}
            y={i * 28 + 6}
            width={Math.max(2, r.w)}
            height={16}
            className="fill-primary"
            rx={2}
          >
            <title>{`${r.label}: ${formatDuration(r.seconds)} (${r.entries} entries)`}</title>
          </rect>
          <text
            x={200 + Math.max(2, r.w) + 6}
            y={i * 28 + 18}
            className="fill-muted-foreground"
            fontSize={11}
          >
            {formatDuration(r.seconds)}
          </text>
        </g>
      ))}
    </svg>
  );
}

export default function TimeReport() {
  const { slug = "", identifier = "" } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const [groupBy, setGroupBy] = useState<TimeGroupBy>("day");
  const [days, setDays] = useState(30);

  const query = useQuery({
    queryKey: timeSummaryKey(slug, identifier, days, groupBy),
    queryFn: () => fetchTimeSummary(slug, identifier, days, groupBy),
    enabled: slug !== "" && identifier !== "",
  });

  const err =
    query.error instanceof ApiError ? query.error.message : "Something went wrong";

  return (
    <section className="rounded-lg border bg-card p-4">
      <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-sm font-semibold">Time report</h2>
        <div className="flex items-center gap-2 text-xs">
          <label className="text-muted-foreground" htmlFor="tr-group">
            Group by
          </label>
          <select
            id="tr-group"
            value={groupBy}
            onChange={(e) => setGroupBy(e.target.value as TimeGroupBy)}
            className="rounded-md border bg-background px-2 py-1"
          >
            {GROUP_OPTIONS.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
          <label className="text-muted-foreground" htmlFor="tr-days">
            Last
          </label>
          <select
            id="tr-days"
            value={days}
            onChange={(e) => setDays(Number(e.target.value))}
            className="rounded-md border bg-background px-2 py-1"
          >
            {DAY_OPTIONS.map((d) => (
              <option key={d} value={d}>
                {d} days
              </option>
            ))}
          </select>
        </div>
      </div>

      {query.isLoading ? (
        <p className="py-8 text-center text-sm text-muted-foreground">Loading…</p>
      ) : query.error ? (
        <p className="py-8 text-center text-sm text-destructive">
          Failed to load: {err}
        </p>
      ) : (query.data?.buckets.length ?? 0) === 0 ||
        query.data!.total_seconds === 0 ? (
        <p className="py-8 text-center text-sm text-muted-foreground">
          No logged time in this window.
        </p>
      ) : (
        <div>
          <p className="mb-2 text-xs text-muted-foreground">
            Total{" "}
            <span className="font-semibold text-foreground">
              {formatDuration(query.data!.total_seconds)}
            </span>{" "}
            logged in the last {days} days
          </p>
          {groupBy === "user" || groupBy === "issue" ? (
            <RankedBars buckets={query.data!.buckets} />
          ) : (
            <TimeBars buckets={query.data!.buckets} />
          )}
        </div>
      )}
      <p className="mt-2 text-xs text-muted-foreground">
        Completed entries only — running timers are excluded.
      </p>
    </section>
  );
}
