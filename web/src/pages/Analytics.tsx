import { useMemo, useState } from "react";
import { useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import ProjectNav from "../components/project/ProjectNav";
import { api } from "../lib/api";
import { cn } from "../lib/utils";
import type { Cycle, IssueState, Label } from "../lib/types";
import { PRIORITY_LABELS } from "../lib/types";
import {
  barWidths,
  burndownLine,
  donutSlices,
  priorityColor,
  resolveCounts,
  shortDate,
  trendBars,
} from "../lib/analytics";
import type {
  AnalyticsBurndown,
  AnalyticsBurndownDay,
  AnalyticsSummary,
  AnalyticsTrendDay,
} from "../lib/analytics";

/** Analytics dashboard (C4T4): project summary, cycle burndown, and
 *  created/closed trends. Hand-rolled SVG, zero new dependencies. */

const CHART_W = 640;
const CHART_H = 260;

function Section({
  title,
  right,
  children,
}: {
  title: string;
  right?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <section className="rounded-lg border bg-card p-4">
      <div className="mb-3 flex items-center justify-between">
        <h2 className="text-sm font-semibold">{title}</h2>
        {right}
      </div>
      {children}
    </section>
  );
}

function Loading() {
  return <div className="py-8 text-center text-sm text-muted-foreground">Loading…</div>;
}

function ErrorBox({ message }: { message: string }) {
  return (
    <div className="py-8 text-center text-sm text-destructive">
      Failed to load: {message}
    </div>
  );
}

function EmptyChart({ label }: { label: string }) {
  return (
    <div className="flex h-40 items-center justify-center text-sm text-muted-foreground">
      {label}
    </div>
  );
}

/** Donut chart for the by-state distribution. */
function StateDonut({
  rows,
}: {
  rows: { label: string; value: number; color: string }[];
}) {
  const cx = 110;
  const cy = 110;
  const r = 90;
  const innerR = 58;
  const slices = useMemo(
    () => donutSlices(rows, cx, cy, r, innerR),
    [rows],
  );
  const total = rows.reduce((s, x) => s + x.value, 0);
  if (slices.length === 0) return <EmptyChart label="No issues yet" />;
  const single = slices.length === 1 && slices[0].fraction >= 1;
  return (
    <div className="flex items-center gap-6">
      <svg width={cx * 2} height={cy * 2} role="img" aria-label="Issues by state">
        {single ? (
          <circle
            cx={cx}
            cy={cy}
            r={(r + innerR) / 2}
            fill="none"
            stroke={slices[0].color}
            strokeWidth={r - innerR}
          />
        ) : (
          slices.map((s) => <path key={s.label} d={s.path} fill={s.color} />)
        )}
        <text
          x={cx}
          y={cy - 6}
          textAnchor="middle"
          className="fill-foreground text-2xl font-bold"
        >
          {total}
        </text>
        <text
          x={cx}
          y={cy + 16}
          textAnchor="middle"
          className="fill-muted-foreground text-xs"
        >
          issues
        </text>
      </svg>
      <ul className="flex flex-col gap-1.5 text-sm">
        {slices.map((s) => (
          <li key={s.label} className="flex items-center gap-2">
            <span
              className="inline-block h-3 w-3 rounded-sm"
              style={{ backgroundColor: s.color }}
            />
            <span className="text-muted-foreground">{s.label}</span>
            <span className="font-medium">{s.value}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}

/** Horizontal bars for the by-priority distribution. */
function PriorityBars({
  rows,
}: {
  rows: { label: string; value: number; color: string }[];
}) {
  const widths = barWidths(
    rows.map((r) => r.value),
    220,
  );
  if (rows.length === 0) return <EmptyChart label="No issues yet" />;
  return (
    <ul className="flex flex-col gap-2">
      {rows.map((r, i) => (
        <li key={r.label} className="flex items-center gap-3 text-sm">
          <span className="w-16 shrink-0 text-muted-foreground">{r.label}</span>
          <div className="h-5 w-[220px] rounded bg-muted/60">
            <div
              className="h-5 rounded transition-all"
              style={{ width: widths[i], backgroundColor: r.color }}
            />
          </div>
          <span className="font-medium">{r.value}</span>
        </li>
      ))}
    </ul>
  );
}

function StatCard({ label, value }: { label: string; value: number | string }) {
  return (
    <div className="rounded-lg border bg-card p-4">
      <div className="text-2xl font-bold">{value}</div>
      <div className="text-xs text-muted-foreground">{label}</div>
    </div>
  );
}

/** Burndown line chart for one cycle. */
function BurndownChart({ data }: { data: AnalyticsBurndown }) {
  const [metric, setMetric] = useState<"count" | "estimate">("count");
  const layout = useMemo(
    () =>
      burndownLine(
        data.days,
        (d: AnalyticsBurndownDay) =>
          metric === "count" ? d.remaining : d.remaining_estimate,
        CHART_W,
        CHART_H,
      ),
    [data, metric],
  );
  if (layout.points.length === 0)
    return <EmptyChart label="No burndown data for this cycle" />;
  const pad = { l: 36, r: 12, t: 12, b: 28 };
  return (
    <div>
      <div className="mb-2 flex items-center gap-2 text-xs">
        <div className="flex rounded-md border">
          {(["count", "estimate"] as const).map((m) => (
            <button
              key={m}
              type="button"
              onClick={() => setMetric(m)}
              className={cn(
                "px-2 py-1 capitalize",
                metric === m
                  ? "bg-accent font-medium"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              {m}
            </button>
          ))}
        </div>
        <span className="text-muted-foreground">
          {shortDate(data.start_date)} → {shortDate(data.end_date)} · scope{" "}
          {data.total_scope}
        </span>
      </div>
      <svg
        width="100%"
        viewBox={`0 0 ${CHART_W} ${CHART_H}`}
        role="img"
        aria-label="Cycle burndown"
      >
        {layout.ticks.map((t) => {
          const y =
            pad.t +
            (CHART_H - pad.t - pad.b) -
            (t / layout.yMax) * (CHART_H - pad.t - pad.b);
          return (
            <g key={t}>
              <line
                x1={pad.l}
                x2={CHART_W - pad.r}
                y1={y}
                y2={y}
                className="stroke-border"
                strokeDasharray="3 3"
              />
              <text
                x={pad.l - 6}
                y={y + 4}
                textAnchor="end"
                className="fill-muted-foreground text-xs"
                fontSize={11}
              >
                {t}
              </text>
            </g>
          );
        })}
        <line
          x1={pad.l}
          x2={CHART_W - pad.r}
          y1={CHART_H - pad.b}
          y2={CHART_H - pad.b}
          className="stroke-border"
        />
        <path
          d={layout.path}
          fill="none"
          stroke="hsl(var(--primary))"
          strokeWidth={2}
        />
        {layout.points.map((p) => (
          <g key={p.date}>
            <circle cx={p.x} cy={p.y} r={3} fill="hsl(var(--primary))">
              <title>
                {shortDate(p.date)}: {p.value} remaining
              </title>
            </circle>
            {layout.points.length <= 14 && (
              <text
                x={p.x}
                y={CHART_H - 8}
                textAnchor="middle"
                className="fill-muted-foreground"
                fontSize={10}
              >
                {shortDate(p.date)}
              </text>
            )}
          </g>
        ))}
      </svg>
    </div>
  );
}

/** Grouped created/closed bars. */
function TrendsChart({ days }: { days: AnalyticsTrendDay[] }) {
  const { bars, ticks, baseY } = useMemo(
    () => trendBars(days, CHART_W, CHART_H),
    [days],
  );
  if (days.length === 0) return <EmptyChart label="No activity in range" />;
  const pad = { l: 36, r: 12, t: 12, b: 28 };
  return (
    <div>
      <div className="mb-2 flex items-center gap-4 text-xs text-muted-foreground">
        <span className="flex items-center gap-1">
          <span className="inline-block h-2.5 w-2.5 rounded-sm bg-[#60a5fa]" />
          Created
        </span>
        <span className="flex items-center gap-1">
          <span className="inline-block h-2.5 w-2.5 rounded-sm bg-[#34d399]" />
          Closed
        </span>
      </div>
      <svg
        width="100%"
        viewBox={`0 0 ${CHART_W} ${CHART_H}`}
        role="img"
        aria-label="Created vs closed per day"
      >
        {ticks.map((t) => {
          const y =
            pad.t +
            (CHART_H - pad.t - pad.b) -
            (t / Math.max(1, ...ticks)) * (CHART_H - pad.t - pad.b);
          return (
            <g key={t}>
              <line
                x1={pad.l}
                x2={CHART_W - pad.r}
                y1={y}
                y2={y}
                className="stroke-border"
                strokeDasharray="3 3"
              />
              <text
                x={pad.l - 6}
                y={y + 4}
                textAnchor="end"
                className="fill-muted-foreground"
                fontSize={11}
              >
                {t}
              </text>
            </g>
          );
        })}
        <line
          x1={pad.l}
          x2={CHART_W - pad.r}
          y1={baseY}
          y2={baseY}
          className="stroke-border"
        />
        {bars.map((b) => (
          <g key={b.date}>
            <rect
              x={b.x}
              y={baseY - b.createdH}
              width={b.barW}
              height={b.createdH}
              fill="#60a5fa"
              rx={2}
            >
              <title>
                {shortDate(b.date)}: {b.created} created
              </title>
            </rect>
            <rect
              x={b.x + b.barW}
              y={baseY - b.closedH}
              width={b.barW}
              height={b.closedH}
              fill="#34d399"
              rx={2}
            >
              <title>
                {shortDate(b.date)}: {b.closed} closed
              </title>
            </rect>
            {bars.length <= 31 && (
              <text
                x={b.x + b.barW}
                y={CHART_H - 8}
                textAnchor="middle"
                className="fill-muted-foreground"
                fontSize={9}
                transform={
                  bars.length > 14
                    ? `rotate(-45 ${b.x + b.barW} ${CHART_H - 8})`
                    : undefined
                }
              >
                {shortDate(b.date)}
              </text>
            )}
          </g>
        ))}
      </svg>
    </div>
  );
}

const TREND_RANGES = [7, 14, 30, 60, 90];

export default function Analytics() {
  const { slug = "", identifier = "" } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const base = `/api/v1/workspaces/${slug}/projects/${identifier}`;
  const [cycleId, setCycleId] = useState<string>("");
  const [trendDays, setTrendDays] = useState(30);

  const summaryQuery = useQuery({
    queryKey: ["analytics-summary", slug, identifier],
    queryFn: () => api.get<AnalyticsSummary>(`${base}/analytics/summary`),
  });
  const statesQuery = useQuery({
    queryKey: ["states", slug, identifier],
    queryFn: () =>
      api.get<{ states: IssueState[] }>(`${base}/states`).then((d) => d.states),
  });
  const labelsQuery = useQuery({
    queryKey: ["labels", slug, identifier],
    queryFn: () =>
      api.get<{ labels: Label[] }>(`${base}/labels`).then((d) => d.labels),
  });
  const cyclesQuery = useQuery({
    queryKey: ["cycles", slug, identifier],
    queryFn: () =>
      api.get<{ cycles: Cycle[] }>(`${base}/cycles`).then((d) => d.cycles),
  });
  // Default the burndown cycle picker to the first current/upcoming cycle.
  const cycles = cyclesQuery.data ?? [];
  const effectiveCycleId =
    cycleId ||
    cycles.find((c) => c.status === "current")?.id ||
    cycles[0]?.id ||
    "";

  const burndownQuery = useQuery({
    queryKey: ["analytics-burndown", slug, identifier, effectiveCycleId],
    queryFn: () =>
      api.get<AnalyticsBurndown>(
        `${base}/analytics/cycle/${effectiveCycleId}/burndown`,
      ),
    enabled: effectiveCycleId !== "",
  });
  const trendsQuery = useQuery({
    queryKey: ["analytics-trends", slug, identifier, trendDays],
    queryFn: () =>
      api
        .get<{ days: AnalyticsTrendDay[] }>(
          `${base}/analytics/trends?days=${trendDays}`,
        )
        .then((d) => d.days),
  });

  const stateLookup = useMemo(() => {
    const m = new Map<string, { name: string; color: string }>();
    for (const s of statesQuery.data ?? []) m.set(s.id, s);
    return m;
  }, [statesQuery.data]);
  const labelLookup = useMemo(() => {
    const m = new Map<string, { name: string; color: string }>();
    for (const l of labelsQuery.data ?? []) m.set(l.id, l);
    return m;
  }, [labelsQuery.data]);

  const summary = summaryQuery.data;
  const stateRows = useMemo(
    () => (summary ? resolveCounts(summary.by_state, stateLookup) : []),
    [summary, stateLookup],
  );
  const labelRows = useMemo(
    () => (summary ? resolveCounts(summary.by_label, labelLookup) : []),
    [summary, labelLookup],
  );
  const priorityRows = useMemo(() => {
    if (!summary) return [];
    const rows = Object.entries(summary.by_priority).map(([k, value]) => {
      const n = Number(k);
      return {
        id: k,
        label: PRIORITY_LABELS[n] ?? `P${k}`,
        value,
        color: priorityColor(n),
      };
    });
    rows.sort((a, b) => b.value - a.value);
    return rows;
  }, [summary]);

  const estimatePct =
    summary && summary.estimate_total > 0
      ? Math.round((summary.estimate_done / summary.estimate_total) * 100)
      : 0;

  return (
    <div className="flex h-full flex-col gap-4 overflow-y-auto p-4">
      <ProjectNav />
      <h1 className="text-lg font-semibold">Analytics</h1>

      {/* Summary */}
      <Section title="Issue summary">
        {summaryQuery.isPending || statesQuery.isPending ? (
          <Loading />
        ) : summaryQuery.isError ? (
          <ErrorBox message={(summaryQuery.error as Error).message} />
        ) : (
          <div className="flex flex-col gap-6">
            <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
              <StatCard
                label="Total issues"
                value={stateRows.reduce((s, r) => s + r.value, 0)}
              />
              <StatCard label="Overdue" value={summary?.overdue_count ?? 0} />
              <StatCard
                label="Estimate points"
                value={summary?.estimate_total ?? 0}
              />
              <StatCard
                label="Estimate done"
                value={`${summary?.estimate_done ?? 0} (${estimatePct}%)`}
              />
            </div>
            <div className="grid gap-6 md:grid-cols-2">
              <div>
                <h3 className="mb-2 text-xs font-medium text-muted-foreground">
                  BY STATE
                </h3>
                <StateDonut rows={stateRows} />
              </div>
              <div>
                <h3 className="mb-2 text-xs font-medium text-muted-foreground">
                  BY PRIORITY
                </h3>
                <PriorityBars rows={priorityRows} />
              </div>
            </div>
            {labelRows.length > 0 && (
              <div>
                <h3 className="mb-2 text-xs font-medium text-muted-foreground">
                  BY LABEL
                </h3>
                <div className="flex flex-wrap gap-2">
                  {labelRows.map((r) => (
                    <span
                      key={r.id}
                      className="inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs"
                    >
                      <span
                        className="inline-block h-2.5 w-2.5 rounded-full"
                        style={{ backgroundColor: r.color }}
                      />
                      {r.label}
                      <span className="font-medium">{r.value}</span>
                    </span>
                  ))}
                </div>
              </div>
            )}
          </div>
        )}
      </Section>

      {/* Burndown */}
      <Section
        title="Cycle burndown"
        right={
          <select
            className="rounded-md border bg-background px-2 py-1 text-xs"
            value={effectiveCycleId}
            onChange={(e) => setCycleId(e.target.value)}
            aria-label="Select cycle"
          >
            {cycles.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name} ({c.status})
              </option>
            ))}
          </select>
        }
      >
        {cyclesQuery.isPending ? (
          <Loading />
        ) : cycles.length === 0 ? (
          <EmptyChart label="No cycles yet — create one to see burndown" />
        ) : effectiveCycleId === "" ? (
          <Loading />
        ) : burndownQuery.isPending ? (
          <Loading />
        ) : burndownQuery.isError ? (
          <ErrorBox message={(burndownQuery.error as Error).message} />
        ) : (
          <BurndownChart
            key={effectiveCycleId}
            data={burndownQuery.data as AnalyticsBurndown}
          />
        )}
      </Section>

      {/* Trends */}
      <Section
        title="Activity trends"
        right={
          <div className="flex rounded-md border text-xs">
            {TREND_RANGES.map((d) => (
              <button
                key={d}
                type="button"
                onClick={() => setTrendDays(d)}
                className={cn(
                  "px-2 py-1",
                  trendDays === d
                    ? "bg-accent font-medium"
                    : "text-muted-foreground hover:text-foreground",
                )}
              >
                {d}d
              </button>
            ))}
          </div>
        }
      >
        {trendsQuery.isPending ? (
          <Loading />
        ) : trendsQuery.isError ? (
          <ErrorBox message={(trendsQuery.error as Error).message} />
        ) : (
          <TrendsChart days={trendsQuery.data ?? []} />
        )}
      </Section>
    </div>
  );
}
