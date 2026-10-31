// C81T1: a single dashboard widget card — title, per-widget overflow
// menu (edit/delete for editors, Export CSV for anyone who can read
// the widget), drag handle binding (canEdit only), and the recharts
// renderer for the six widget types with per-widget loading/empty/
// error states. Widget data is fetched per widget via
// GET /widgets/{id}/data with react-query caching (the detail endpoint
// returns widgets with config only, per the T0 contract).
// C83T1: the four phase-2 renderers — smart_counter (grid tiles or
// table), smart_gauge (SVG semi-circular arcs), table_2d (desc-sorted
// rows), intake_breakdown (nine tiles) — with page state driving the
// paginated data envelope.
// C85T1: intake_breakdown tiles drill into the intake drill-down modal
// (eight row-set metrics; avg-time-to-decide stays plain text).
// C100T1: pql_table joins the row-list renderers (WorkItemsTable +
// pagination via isRowListType) with two PQL-specific states: the
// empty state ("No issues match this query") and the per-widget query
// error state (the data route's PQL message verbatim + an Edit
// affordance) — a bad stored query breaks only its own card, never
// the dashboard. The card also shows the stored query under the
// header so the widget is self-explanatory (prototype-as-spec).
import { useEffect, useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ChevronLeft,
  ChevronRight,
  Download,
  GripVertical,
  MoreHorizontal,
  Pencil,
  Trash2,
} from "lucide-react";
import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  Line,
  LineChart,
  Pie,
  PieChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import {
  chartSeries,
  downloadWidgetCsv,
  downloadWidgetTrendCsv,
  EMAIL_INGESTION_NOTE,
  fetchIntakeDrilldown,
  fetchIntakeDrilldownBucket,
  fetchIntakeTrend,
  fetchWidgetData,
  fetchWidgetDrilldown,
  formatDuration,
  INTAKE_TILES,
  INTAKE_TREND_BUCKETS,
  INTAKE_TREND_GRANULARITIES,
  intakeBucketTitle,
  intakeDrilldownRowToWidgetRow,
  isDrillableType,
  isPaginatedType,
  isRowListType,
  normalizeWidgetConfig,
  numberValue,
  ROW_COLUMN_LABELS,
  semiGaugeArc,
  seriesColor,
  trendDecided,
  trendVolume,
  updateWidget,
  widgetDataKey,
  widgetDrilldownKey,
  widgetIntakeDrilldownBucketKey,
  widgetIntakeDrilldownKey,
  widgetIntakeTrendKey,
  widgetPageCount,
  widgetRowPageCount,
  DEFAULT_GAUGE_COLOR,
  type DashboardWidget,
  type IntakeDrilldownItem,
  type IntakeMetric,
  type IntakeTile,
  type IntakeTrendBucket,
  type IntakeTrendGranularity,
  type WidgetData,
  type WidgetDataGroup,
  type WidgetRowColumn,
  type WidgetRowItem,
  type WidgetType,
} from "../../lib/dashboards";
import { cn } from "cn";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "../ui/dropdown-menu";
import { Skeleton } from "../ui/skeleton";
import { Button } from "../ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../ui/dialog";
import { priorityLabel } from "../../lib/types";

/** dnd-kit draggable binding for the card's header grip. The canvas
 *  owns useDraggable and applies the drag transform to the positioned
 *  wrapper; the card's grip carries the handle listeners/attributes.
 *  Omitted when the viewer can't edit — read-only mode. */
export interface WidgetDragBinding {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  attributes: Record<string, any>;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  listeners?: Record<string, any>;
  isDragging: boolean;
}

function ChartTooltipContent({
  active,
  payload,
  label,
}: {
  active?: boolean;
  payload?: { name?: string; value?: number | string }[];
  label?: string | number;
}) {
  if (!active || !payload || payload.length === 0) return null;
  const p = payload[0];
  return (
    <div className="rounded-md border border-border bg-surface px-2.5 py-1.5 text-xs shadow-md">
      <p className="font-medium text-ink">{label ?? p.name}</p>
      <p className="tabular-nums text-ink-muted">{p.value}</p>
    </div>
  );
}

const axisTick: { fontSize: number; fill: string } = {
  fontSize: 11,
  fill: "var(--color-ink-muted)",
};

/** Extract the segment label from a recharts click payload (Bar/Pie
 *  hand the datum; Line/Area hand the chart event with activeLabel). */
function segmentLabelFromClick(d: unknown): string | null {
  if (d && typeof d === "object") {
    const o = d as Record<string, unknown>;
    const direct = o.name ?? o.label;
    if (typeof direct === "string" && direct) return direct;
    const payload = o.payload as Record<string, unknown> | undefined;
    if (payload && typeof payload.name === "string" && payload.name) {
      return payload.name;
    }
    const active = o.activeLabel;
    if (typeof active === "string" && active) return active;
    if (typeof active === "number") return String(active);
    const activePayload = o.activePayload as { name?: unknown }[] | undefined;
    const first = activePayload?.[0]?.name;
    if (typeof first === "string" && first) return first;
  }
  return null;
}

function WidgetChart({
  type,
  series,
  onSegmentClick,
}: {
  type: WidgetType;
  series: { name: string; value: number }[];
  /** Absent = not drillable (number/intake/row-list never render here). */
  onSegmentClick?: (label: string) => void;
}) {
  const cells = series.map((_, i) => (
    <Cell key={i} fill={seriesColor(i)} />
  ));
  const barClick = (d: unknown) => {
    if (!onSegmentClick) return;
    const label = segmentLabelFromClick(d);
    if (label) onSegmentClick(label);
  };
  const chartClick = (e: unknown) => {
    if (!onSegmentClick) return;
    const label = segmentLabelFromClick(e);
    if (label) onSegmentClick(label);
  };
  if (type === "bar") {
    return (
      <BarChart data={series} margin={{ top: 8, right: 8, bottom: 0, left: -12 }}>
        <CartesianGrid strokeDasharray="3 3" stroke="var(--color-border)" vertical={false} />
        <XAxis dataKey="name" tick={axisTick} interval="preserveStartEnd" tickLine={false} axisLine={{ stroke: "var(--color-border)" }} />
        <YAxis tick={axisTick} tickLine={false} axisLine={false} allowDecimals={false} width={40} />
        <Tooltip content={<ChartTooltipContent />} cursor={{ fill: "var(--color-wash-hover)" }} />
        <Bar
          dataKey="value"
          radius={[4, 4, 0, 0]}
          // eslint-disable-next-line @typescript-eslint/no-explicit-any
          onClick={barClick as any}
          style={onSegmentClick ? { cursor: "pointer" } : undefined}
        >
          {cells}
        </Bar>
      </BarChart>
    );
  }
  if (type === "line") {
    return (
      <LineChart
        data={series}
        margin={{ top: 8, right: 8, bottom: 0, left: -12 }}
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        onClick={chartClick as any}
        style={onSegmentClick ? { cursor: "pointer" } : undefined}
      >
        <CartesianGrid strokeDasharray="3 3" stroke="var(--color-border)" vertical={false} />
        <XAxis dataKey="name" tick={axisTick} interval="preserveStartEnd" tickLine={false} axisLine={{ stroke: "var(--color-border)" }} />
        <YAxis tick={axisTick} tickLine={false} axisLine={false} allowDecimals={false} width={40} />
        <Tooltip content={<ChartTooltipContent />} />
        <Line type="monotone" dataKey="value" stroke="var(--color-primary)" strokeWidth={2} dot={{ r: 3, fill: "var(--color-primary)" }} />
      </LineChart>
    );
  }
  if (type === "area") {
    return (
      <AreaChart
        data={series}
        margin={{ top: 8, right: 8, bottom: 0, left: -12 }}
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        onClick={chartClick as any}
        style={onSegmentClick ? { cursor: "pointer" } : undefined}
      >
        <defs>
          <linearGradient id="widgetAreaFill" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="var(--color-primary)" stopOpacity={0.35} />
            <stop offset="100%" stopColor="var(--color-primary)" stopOpacity={0.05} />
          </linearGradient>
        </defs>
        <CartesianGrid strokeDasharray="3 3" stroke="var(--color-border)" vertical={false} />
        <XAxis dataKey="name" tick={axisTick} interval="preserveStartEnd" tickLine={false} axisLine={{ stroke: "var(--color-border)" }} />
        <YAxis tick={axisTick} tickLine={false} axisLine={false} allowDecimals={false} width={40} />
        <Tooltip content={<ChartTooltipContent />} />
        <Area type="monotone" dataKey="value" stroke="var(--color-primary)" strokeWidth={2} fill="url(#widgetAreaFill)" />
      </AreaChart>
    );
  }
  // pie / donut
  return (
    <PieChart>
      <Tooltip content={<ChartTooltipContent />} />
      <Pie
        data={series}
        dataKey="value"
        nameKey="name"
        innerRadius={type === "donut" ? "55%" : 0}
        outerRadius="85%"
        paddingAngle={2}
        strokeWidth={0}
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        onClick={barClick as any}
        style={onSegmentClick ? { cursor: "pointer" } : undefined}
      >
        {cells}
      </Pie>
    </PieChart>
  );
}

/* ------------------------------------------------------------------ */
/* Phase-2 renderers (C83T1).                                          */
/* ------------------------------------------------------------------ */

/** Prev/next controls for the paginated envelope; hidden when the
 *  whole result fits on one page. */
function PaginationControls({
  page,
  pageCount,
  onPage,
}: {
  page: number;
  pageCount: number;
  onPage: (page: number) => void;
}) {
  if (pageCount <= 1) return null;
  return (
    <div className="flex shrink-0 items-center justify-between border-t border-border/60 px-3 py-1.5">
      <Button
        type="button"
        variant="ghost"
        size="sm"
        aria-label="Previous page"
        disabled={page <= 1}
        onClick={() => onPage(page - 1)}
        className="h-7 px-2"
      >
        <ChevronLeft className="size-4" aria-hidden="true" />
      </Button>
      <span className="text-xs tabular-nums text-muted-foreground">
        Page {page} of {pageCount}
      </span>
      <Button
        type="button"
        variant="ghost"
        size="sm"
        aria-label="Next page"
        disabled={page >= pageCount}
        onClick={() => onPage(page + 1)}
        className="h-7 px-2"
      >
        <ChevronRight className="size-4" aria-hidden="true" />
      </Button>
    </div>
  );
}

/** Label + count rows shared by smart_counter's table view and
 *  table_2d (which sorts desc by count first). */
function GroupRowsTable({
  groups,
  sortDesc,
  onRowClick,
}: {
  groups: WidgetDataGroup[];
  sortDesc?: boolean;
  /** Absent = row is not drillable (never for intake/number). */
  onRowClick?: (group: WidgetDataGroup) => void;
}) {
  const rows = sortDesc
    ? [...groups].sort((a, b) => b.value - a.value)
    : groups;
  return (
    <table className="w-full text-sm">
      <thead>
        <tr className="border-b border-border/60 text-left text-xs text-muted-foreground">
          <th scope="col" className="px-3 py-2 font-medium">
            Group
          </th>
          <th scope="col" className="px-3 py-2 text-right font-medium">
            Count
          </th>
        </tr>
      </thead>
      <tbody>
        {rows.map((g) => (
          <tr
            key={g.key}
            className={cn(
              "border-b border-border/40 last:border-0",
              onRowClick && "cursor-pointer hover:bg-accent/50",
            )}
            onClick={onRowClick ? () => onRowClick(g) : undefined}
          >
            <td className="truncate px-3 py-2 text-ink">{g.label}</td>
            <td className="px-3 py-2 text-right tabular-nums text-ink">
              {g.value}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function SmartCounterBody({
  widget,
  groups,
  onGroupClick,
}: {
  widget: DashboardWidget;
  groups: WidgetDataGroup[];
  onGroupClick?: (group: WidgetDataGroup) => void;
}) {
  const cfg = normalizeWidgetConfig(widget.config);
  if (cfg.visualization === "table") {
    return (
      <div className="h-full overflow-auto">
        <GroupRowsTable groups={groups} onRowClick={onGroupClick} />
      </div>
    );
  }
  return (
    <div className="grid h-full grid-cols-2 content-start gap-2 overflow-auto p-3">
      {groups.map((g) => (
        <div
          key={g.key}
          className={cn(
            "rounded-md border border-border/60 bg-surface px-3 py-2.5",
            onGroupClick && "cursor-pointer hover:bg-accent/50",
          )}
          title={onGroupClick ? "Click to drill down" : undefined}
          onClick={onGroupClick ? () => onGroupClick(g) : undefined}
        >
          <p className="truncate text-xs text-muted-foreground">{g.label}</p>
          <p className="text-2xl font-bold tabular-nums text-ink">{g.value}</p>
        </div>
      ))}
    </div>
  );
}

/** Completion share of one gauge group: the payload's percentage wins;
 *  otherwise derive it from completed/value. */
export function gaugePercentage(g: WidgetDataGroup): number {
  if (typeof g.percentage === "number") return g.percentage;
  if (typeof g.completed === "number" && g.value > 0) {
    return (100 * g.completed) / g.value;
  }
  return 0;
}

function SmartGaugeBody({
  widget,
  groups,
  onGroupClick,
}: {
  widget: DashboardWidget;
  groups: WidgetDataGroup[];
  onGroupClick?: (group: WidgetDataGroup) => void;
}) {
  const cfg = normalizeWidgetConfig(widget.config);
  const color = cfg.gauge_color || DEFAULT_GAUGE_COLOR;
  const radius = 40;
  const track = semiGaugeArc(100, radius).path;
  return (
    <div className="grid h-full grid-cols-2 content-start gap-3 overflow-auto p-3">
      {groups.map((g) => {
        const pct = gaugePercentage(g);
        const arc = semiGaugeArc(pct, radius);
        return (
          <div
            key={g.key}
            className={cn(
              "flex flex-col items-center gap-1",
              onGroupClick && "cursor-pointer",
            )}
            title={onGroupClick ? "Click to drill down" : undefined}
            onClick={onGroupClick ? () => onGroupClick(g) : undefined}
          >
            <svg
              viewBox={`0 0 ${radius * 2} ${radius + 6}`}
              className="w-full max-w-[120px]"
              role="img"
              aria-label={`${g.label}: ${Math.round(pct)}% complete`}
            >
              <path
                d={track}
                fill="none"
                stroke="var(--color-border)"
                strokeWidth={8}
                strokeLinecap="round"
              />
              <path
                d={arc.path}
                fill="none"
                stroke={color}
                strokeWidth={8}
                strokeLinecap="round"
                data-testid={`gauge-arc-${g.key}`}
                data-percentage={pct}
              />
            </svg>
            <p className="max-w-full truncate text-xs text-muted-foreground">
              {g.label}
            </p>
            {cfg.show_percentage && (
              <p className="text-sm font-semibold tabular-nums text-ink">
                {Math.round(pct)}%
              </p>
            )}
            {typeof g.completed === "number" && (
              <p className="text-[11px] tabular-nums text-muted-foreground">
                {g.completed} of {g.value} completed
              </p>
            )}
          </div>
        );
      })}
    </div>
  );
}

/** Format an intake timestamp as a short date (Nov 5, 2026). */
function intakeDate(iso: string): string {
  return new Date(iso).toLocaleDateString("en-US", {
    month: "short",
    day: "numeric",
    year: "numeric",
  });
}

/** The intake_breakdown card body (C83T1 tiles, C85T1 drill-down):
 *  every tile number is clickable into the intake drill-down modal
 *  EXCEPT avg-time-to-decide, an aggregate with no row list — it
 *  renders as plain text, never a dead click. The funnel tiles
 *  (duplicate/pending/snoozed) behave exactly like the other tiles.
 *  Clicking is a read affordance: view-only readers drill too. */
function IntakeBody({
  widget,
  data,
  onTileClick,
}: {
  widget: DashboardWidget;
  data: WidgetData;
  onTileClick?: (tile: IntakeTile) => void;
}) {
  const intake = data.intake!;
  const cfg = normalizeWidgetConfig(widget.config);
  if (cfg.view === "table") {
    return (
      <table className="w-full text-sm">
        <tbody>
          {INTAKE_TILES.map((tile) => {
            const raw = intake[tile.key];
            const value = tile.duration
              ? formatDuration(raw ?? 0)
              : (raw ?? 0);
            return (
              <tr
                key={tile.key}
                className="border-b border-border/40 last:border-0"
                {...(tile.honestEmpty ? { title: EMAIL_INGESTION_NOTE } : {})}
              >
                <td className="px-3 py-2 text-muted-foreground">
                  {tile.label}
                </td>
                <td className="px-3 py-2 text-right tabular-nums text-ink">
                  {tile.metric && onTileClick ? (
                    <button
                      type="button"
                      aria-label={`Drill into ${tile.label}`}
                      onClick={() => onTileClick(tile)}
                      className="cursor-pointer hover:text-primary hover:underline"
                    >
                      {value}
                    </button>
                  ) : (
                    value
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    );
  }
  return (
    <div className="grid grid-cols-2 content-start gap-2 p-3 sm:grid-cols-3">
      {INTAKE_TILES.map((tile) => {
        const raw = intake[tile.key];
        const value = tile.duration
          ? formatDuration(raw ?? 0)
          : (raw ?? 0);
        const inner = (
          <>
            <p className="truncate text-xs text-muted-foreground">
              {tile.label}
            </p>
            <p className="text-2xl font-bold tabular-nums text-ink">
              {value}
            </p>
          </>
        );
        if (tile.metric && onTileClick) {
          return (
            <button
              key={tile.key}
              type="button"
              aria-label={`Drill into ${tile.label}`}
              onClick={() => onTileClick(tile)}
              className="cursor-pointer rounded-md border border-border/60 bg-surface px-3 py-2.5 text-left hover:border-primary/60"
              {...(tile.honestEmpty ? { title: EMAIL_INGESTION_NOTE } : {})}
            >
              {inner}
            </button>
          );
        }
        return (
          <div
            key={tile.key}
            className="rounded-md border border-border/60 bg-surface px-3 py-2.5"
            {...(tile.honestEmpty ? { title: EMAIL_INGESTION_NOTE } : {})}
          >
            {inner}
          </div>
        );
      })}
    </div>
  );
}

/** Multi-series tooltip for the intake trend chart: created volume
 *  and decided volume per bucket (email is always 0 — it lives in
 *  the legend, not the tooltip). */
function TrendTooltipContent({
  active,
  payload,
  label,
}: {
  active?: boolean;
  payload?: { name?: string; value?: number | string }[];
  label?: string | number;
}) {
  if (!active || !payload || payload.length === 0) return null;
  return (
    <div className="rounded-md border border-border bg-surface px-2.5 py-1.5 text-xs shadow-md">
      <p className="font-medium text-ink">{label}</p>
      {payload.map((p) => (
        <p key={p.name} className="tabular-nums text-ink-muted">
          {p.name}: {p.value}
        </p>
      ))}
    </div>
  );
}

/** Extract the clicked trend bucket's bucket_start from a recharts
 *  area-chart click event (C87T1). Recharts hands the chart event
 *  with activePayload: the clicked point's datum, whose
 *  `bucket_start` the trend section stamps onto every chart point
 *  (verbatim from the trend payload — the server owns window math,
 *  the client never recomputes it). Falls back to the datum's `name`
 *  (which is also bucket_start) and to activeLabel. Returns null
 *  when there is no active payload — a click on empty chart margins
 *  — and the caller must open nothing in that case, never an empty
 *  modal. Exported for the mocked chart-click-event tests. */
export function bucketStartFromTrendClick(e: unknown): string | null {
  if (!e || typeof e !== "object") return null;
  const o = e as Record<string, unknown>;
  const activePayload = o.activePayload as
    | { payload?: Record<string, unknown>; name?: unknown }[]
    | undefined;
  const first = activePayload?.[0];
  if (first) {
    const payload = first.payload;
    if (payload && typeof payload.bucket_start === "string" && payload.bucket_start) {
      return payload.bucket_start;
    }
    if (typeof first.name === "string" && first.name) return first.name;
    if (payload && typeof payload.name === "string" && payload.name) {
      return payload.name;
    }
  }
  const activeLabel = o.activeLabel;
  if (typeof activeLabel === "string" && activeLabel) return activeLabel;
  return null;
}

/** The intake trend section (C86T1, prototype-as-spec): a recharts
 *  area chart BELOW the tiles (below the table in Table view — the
 *  card renders this section after IntakeBody in both views), with
 *  the granularity toggle above it (Day / Week / Month, default
 *  Week, fixed 12 buckets — no bucket-count control) and the honest
 *  legend: created volume (in-app + form), the decided line
 *  (accepted + rejected + duplicate), and the all-zero email series
 *  with the no-ingestion note, never hidden. Empty state: when the
 *  widget has no intake rows at all, the honest empty state replaces
 *  the chart — never a flat zero line pretending to be data. Partial
 *  data renders the continuous zero-filled line, per the backend
 *  contract. Read affordance: view-only readers fetch the trend too
 *  (the server enforces read).
 *  C87T1: the chart is clickable — a chart click with an active
 *  payload calls onBucketClick with that point's bucket_start,
 *  opening the bucket drill-down modal; clicks with no active
 *  payload (empty chart margins) do nothing. The pointer cursor
 *  and the hint line under the chart advertise the affordance
 *  (prototype-as-spec). */
function IntakeTrendSection({
  widgetId,
  granularity,
  onGranularity,
  hasIntake,
  onBucketClick,
}: {
  widgetId: string;
  granularity: IntakeTrendGranularity;
  onGranularity: (g: IntakeTrendGranularity) => void;
  /** False when the headline intake volumes sum to zero — i.e. no
   *  intake rows exist at all (a zero-filled window alone must NOT
   *  trigger the empty state: old rows outside the window still
   *  count as intake, and render as a zero line). */
  hasIntake: boolean;
  /** Absent = chart not clickable (never for intake widgets). */
  onBucketClick?: (bucket_start: string) => void;
}) {
  const { data, isPending, isError, refetch } = useQuery({
    queryKey: widgetIntakeTrendKey(widgetId, granularity),
    queryFn: () =>
      fetchIntakeTrend(widgetId, granularity, INTAKE_TREND_BUCKETS),
    retry: 1,
    staleTime: 60_000,
  });
  const period =
    INTAKE_TREND_GRANULARITIES.find((g) => g.value === granularity)
      ?.period ?? "weeks";
  const chartData = (data?.buckets ?? []).map((b: IntakeTrendBucket) => ({
    name: b.bucket_start,
    bucket_start: b.bucket_start,
    volume: trendVolume(b),
    decided: trendDecided(b),
  }));
  const gradientId = `intakeTrendFill-${widgetId}`;
  return (
    <section
      aria-label="Intake trend"
      className="shrink-0 border-t border-border/60 px-3 py-3"
    >
      <h4 className="text-[13px] font-semibold text-ink">
        Trend — last {INTAKE_TREND_BUCKETS} {period}
      </h4>
      <div className="mt-2 flex gap-1.5" role="group" aria-label="Trend granularity">
        {INTAKE_TREND_GRANULARITIES.map((g) => (
          <button
            key={g.value}
            type="button"
            aria-pressed={granularity === g.value}
            onClick={() => onGranularity(g.value)}
            className={
              granularity === g.value
                ? "rounded-full bg-primary px-3 py-1 text-xs font-medium text-primary-foreground"
                : "rounded-full border border-border px-3 py-1 text-xs text-muted-foreground hover:text-foreground"
            }
          >
            {g.label}
          </button>
        ))}
      </div>
      {isPending ? (
        <div className="mt-3 flex flex-col gap-2" aria-label="Loading intake trend">
          <Skeleton className="h-40 w-full" />
        </div>
      ) : isError ? (
        <div className="mt-3 flex flex-col items-center gap-2 py-6 text-center">
          <p className="text-sm text-destructive">
            Couldn&apos;t load the intake trend.
          </p>
          <Button type="button" variant="outline" size="sm" onClick={() => refetch()}>
            Retry
          </Button>
        </div>
      ) : !hasIntake ? (
        <p className="py-6 text-center text-sm text-muted-foreground">
          No intake yet — trends appear once intake rows exist
        </p>
      ) : (
        <>
          <div className="mt-2 h-56 w-full">
            <ResponsiveContainer width="100%" height="100%">
              <AreaChart
                data={chartData}
                margin={{ top: 8, right: 8, bottom: 0, left: -12 }}
                // eslint-disable-next-line @typescript-eslint/no-explicit-any
                onClick={(e: any) => {
                  if (!onBucketClick) return;
                  const bucket_start = bucketStartFromTrendClick(e);
                  if (bucket_start) onBucketClick(bucket_start);
                }}
                style={onBucketClick ? { cursor: "pointer" } : undefined}
              >
                <defs>
                  <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor="#3b82f6" stopOpacity={0.35} />
                    <stop offset="100%" stopColor="#3b82f6" stopOpacity={0.05} />
                  </linearGradient>
                </defs>
                <CartesianGrid strokeDasharray="3 3" stroke="var(--color-border)" vertical={false} />
                <XAxis dataKey="name" tick={axisTick} interval="preserveStartEnd" tickLine={false} axisLine={{ stroke: "var(--color-border)" }} />
                <YAxis tick={axisTick} tickLine={false} axisLine={false} allowDecimals={false} width={40} />
                <Tooltip content={<TrendTooltipContent />} />
                <Area type="monotone" dataKey="volume" name="In-app + form volume" stroke="#3b82f6" strokeWidth={2} fill={`url(#${gradientId})`} />
                <Line type="monotone" dataKey="decided" name="Decided (accepted + rejected + duplicate)" stroke="#22c55e" strokeWidth={2} strokeDasharray="6 4" dot={false} />
              </AreaChart>
            </ResponsiveContainer>
          </div>
          {onBucketClick && (
            <p className="mt-2 text-xs text-muted-foreground">
              Click a point to see that period&apos;s rows
            </p>
          )}
          <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
            <span>
              <span className="mr-1.5 inline-block size-2.5 rounded-full bg-[#3b82f6]" aria-hidden="true" />
              In-app + form volume
            </span>
            <span>
              <span className="mr-1.5 inline-block size-2.5 rounded-full bg-[#22c55e]" aria-hidden="true" />
              Decided (accepted + rejected + duplicate)
            </span>
            <span>
              <span className="mr-1.5 inline-block size-2.5 rounded-full bg-muted-foreground" aria-hidden="true" />
              Email — 0 (no email ingestion yet)
            </span>
          </div>
          <p className="mt-2 text-xs text-muted-foreground">
            Buckets count rows by creation {granularity === "day" ? "day" : granularity === "week" ? "week" : "month"} — a row
            decided this week but created last month counts in last
            month&apos;s bucket. Recent buckets read decided &lt; created
            when triage lags — that is the honest signal.
          </p>
        </>
      )}
    </section>
  );
}

/** The intake drill-down modal (C85T1): the C84 modal shape applied
 *  to intake rows — fixed columns, pagination, metric-scoped CSV
 *  export. Row click navigates to the issue detail via the C84
 *  row-click pattern (issue_id + project id). Email shows the honest
 *  empty state, never a spinner or an error; honest server 400s are
 *  surfaced verbatim in-modal. Read affordance: view-only users
 *  drill and export too (the server enforces read).
 *  C87T1 bucket mode: exactly one of `metric` / `bucket` is set. A
 *  bucket click passes `bucket` (current granularity + the trend
 *  payload's bucket_start) and NO metric — the C85 modal carries no
 *  metric filter UI, so "All" is the fetch itself, not a control
 *  default. Title is the bucket title ("Intake — week of …" etc.),
 *  the empty state is the honest "No intake rows in this period."
 *  C88T1 bucket mode: the footer gains the Export CSV button (same
 *  position + visual language as the C85 metric modal's button,
 *  prototype cycle88-prototype.html as spec). It downloads exactly
 *  the rows the modal lists, via the T0 contract
 *  GET /api/v1/widgets/:widget_id/export.csv?granularity=<current>
 *  &bucket_start=<bucket_start> with NO metric — the server
 *  filename carries the bucket suffix
 *  (<dashboard-slug>-<widget-slug>-<granularity>-<bucket_start>.csv).
 *  An empty bucket still shows the button: an empty file is a valid
 *  export of zero rows, not an error. */
function IntakeDrilldownModal({
  widget,
  metric,
  metricLabel,
  bucket,
  onClose,
  onIssueClick,
}: {
  widget: DashboardWidget;
  metric?: IntakeMetric;
  metricLabel: string;
  bucket?: {
    granularity: IntakeTrendGranularity;
    bucket_start: string;
  };
  onClose: () => void;
  onIssueClick?: (row: WidgetRowItem) => void;
}) {
  const [page, setPage] = useState(1);
  const { data, isPending, isError, error, refetch } = useQuery({
    queryKey: bucket
      ? widgetIntakeDrilldownBucketKey(
          widget.id,
          bucket.granularity,
          bucket.bucket_start,
          page,
        )
      : widgetIntakeDrilldownKey(widget.id, metric!, page),
    queryFn: () =>
      bucket
        ? fetchIntakeDrilldownBucket(
            widget.id,
            bucket.granularity,
            bucket.bucket_start,
            page,
          )
        : fetchIntakeDrilldown(widget.id, metric!, page),
    retry: false,
    staleTime: 60_000,
  });
  const pageCount =
    data && data.per_page > 0
      ? Math.max(1, Math.ceil(data.total / data.per_page))
      : 1;
  return (
    <Dialog
      open
      onOpenChange={(o) => {
        if (!o) onClose();
      }}
    >
      <DialogContent
        className="max-h-[88vh] overflow-y-auto sm:max-w-2xl"
        showCloseButton={false}
      >
        <DialogHeader>
          <DialogTitle>
            {bucket
              ? intakeBucketTitle(bucket.granularity, bucket.bucket_start)
              : `${widget.title} — ${metricLabel}`}
          </DialogTitle>
          <DialogDescription>
            {data
              ? `${data.total} intake rows`
              : bucket
                ? "Loading the intake rows for this period…"
                : "Loading the intake rows behind this tile…"}
          </DialogDescription>
        </DialogHeader>
        {isPending ? (
          <div className="flex flex-col gap-2 py-4" aria-label="Loading intake rows">
            <Skeleton className="h-4 w-2/3" />
            <Skeleton className="h-4 w-1/2" />
            <Skeleton className="h-24" />
          </div>
        ) : isError ? (
          <div className="flex flex-col items-center justify-center gap-2 py-6 text-center">
            <p className="text-sm text-destructive">
              {error instanceof Error
                ? error.message
                : "Couldn't load these intake rows."}
            </p>
            <Button type="button" variant="outline" size="sm" onClick={() => refetch()}>
              Retry
            </Button>
          </div>
        ) : data.items.length === 0 ? (
          <p className="py-6 text-center text-sm text-muted-foreground">
            {bucket
              ? "No intake rows in this period."
              : metric === "email"
                ? "No email intake — glance has no email ingestion yet"
                : "No intake rows for this metric."}
          </p>
        ) : (
          <div className="overflow-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-border/60 text-left text-xs text-muted-foreground">
                  <th scope="col" className="px-3 py-2 font-medium">Issue</th>
                  <th scope="col" className="px-3 py-2 font-medium">Project</th>
                  <th scope="col" className="px-3 py-2 font-medium">Status</th>
                  <th scope="col" className="px-3 py-2 font-medium">Source</th>
                  <th scope="col" className="px-3 py-2 font-medium">Snoozed till</th>
                  <th scope="col" className="px-3 py-2 font-medium">Created</th>
                </tr>
              </thead>
              <tbody>
                {data.items.map((item: IntakeDrilldownItem) => (
                  <tr
                    key={item.id}
                    className={cn(
                      "border-b border-border/40 last:border-0",
                      onIssueClick && "cursor-pointer hover:bg-accent/50",
                    )}
                    onClick={
                      onIssueClick
                        ? () =>
                            onIssueClick(intakeDrilldownRowToWidgetRow(item))
                        : undefined
                    }
                  >
                    <td className="max-w-[240px] truncate px-3 py-2 text-ink">
                      {item.title}
                    </td>
                    <td className="whitespace-nowrap px-3 py-2 text-ink">
                      {item.project.name}
                    </td>
                    <td className="whitespace-nowrap px-3 py-2 text-ink">
                      <span className="inline-block rounded border border-border px-1.5 py-0.5 text-[11px]">
                        {item.status_name}
                      </span>
                    </td>
                    <td className="whitespace-nowrap px-3 py-2 text-ink">
                      <span className="inline-block rounded border border-border px-1.5 py-0.5 text-[11px]">
                        {item.source}
                      </span>
                    </td>
                    <td className="whitespace-nowrap px-3 py-2 text-ink">
                      {item.snoozed_till ? intakeDate(item.snoozed_till) : "—"}
                    </td>
                    <td className="whitespace-nowrap px-3 py-2 text-ink">
                      {intakeDate(item.created_at)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            <PaginationControls
              page={data.page}
              pageCount={pageCount}
              onPage={setPage}
            />
          </div>
        )}
        <DialogFooter>
          <Button type="button" variant="outline" onClick={onClose}>
            Close
          </Button>
          {bucket ? (
            <Button
              type="button"
              onClick={() => {
                void downloadWidgetCsv(widget.id, undefined, bucket).catch(
                  () => {
                    // Read-gated server-side; a failed export leaves
                    // the modal untouched (no partial download).
                  },
                );
              }}
            >
              <Download className="size-3.5" aria-hidden="true" />
              Export CSV
            </Button>
          ) : (
            metric && (
              <Button
                type="button"
                onClick={() => {
                  void downloadWidgetCsv(widget.id, metric).catch(() => {
                    // Read-gated server-side; a failed export leaves the
                    // modal untouched (no partial download).
                  });
                }}
              >
                <Download className="size-3.5" aria-hidden="true" />
                Export CSV
              </Button>
            )
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/* ------------------------------------------------------------------ */
/* Phase-3 renderers (C84T1).                                          */
/* ------------------------------------------------------------------ */

/** One row cell per configured column, in config order. */
function RowCells({
  row,
  columns,
}: {
  row: WidgetRowItem;
  columns: WidgetRowColumn[];
}) {
  return (
    <>
      {columns.map((c) => {
        switch (c) {
          case "id":
            return (
              <td key={c} className="whitespace-nowrap px-3 py-2 font-semibold text-primary">
                {row.id}
              </td>
            );
          case "title":
            return (
              <td key={c} className="max-w-[240px] truncate px-3 py-2 text-ink">
                {row.title}
              </td>
            );
          case "state":
            return (
              <td key={c} className="whitespace-nowrap px-3 py-2 text-ink">
                {row.state}
              </td>
            );
          case "priority":
            return (
              <td key={c} className="whitespace-nowrap px-3 py-2 text-ink">
                {priorityLabel(row.priority)}
              </td>
            );
          case "assignee":
            return (
              <td key={c} className="whitespace-nowrap px-3 py-2 text-ink">
                {row.assignee ? row.assignee.name : "Unassigned"}
              </td>
            );
          case "labels":
            return (
              <td key={c} className="px-3 py-2 text-ink">
                {row.labels.length > 0 ? (
                  row.labels.map((l) => (
                    <span
                      key={l.id}
                      className="mr-1 inline-block rounded border border-border px-1.5 py-0.5 text-[11px]"
                    >
                      {l.name}
                    </span>
                  ))
                ) : (
                  <span className="text-muted-foreground">—</span>
                )}
              </td>
            );
          case "project":
            return (
              <td key={c} className="whitespace-nowrap px-3 py-2 text-ink">
                {row.project.name}
              </td>
            );
        }
      })}
    </>
  );
}

/** The row-list card body: configured columns in order, sortable
 *  headers (click toggles asc/desc and SAVES to the widget config —
 *  sort is config, not ephemeral table state), paginated by the card.
 *  View-only readers get plain headers (a sort PATCH would 403). */
function WorkItemsTable({
  rows,
  columns,
  sortBy,
  sortDir,
  canEdit,
  onSort,
  onRowClick,
}: {
  rows: WidgetRowItem[];
  columns: WidgetRowColumn[];
  sortBy: WidgetRowColumn;
  sortDir: "asc" | "desc";
  canEdit: boolean;
  onSort: (column: WidgetRowColumn) => void;
  onRowClick?: (row: WidgetRowItem) => void;
}) {
  return (
    <div className="h-full overflow-auto">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b border-border/60 text-left text-xs text-muted-foreground">
            {columns.map((c) => (
              <th key={c} scope="col" className="whitespace-nowrap px-3 py-2 font-medium">
                {canEdit ? (
                  <button
                    type="button"
                    onClick={() => onSort(c)}
                    aria-label={`Sort by ${ROW_COLUMN_LABELS[c]}`}
                    className="inline-flex items-center gap-1 uppercase tracking-wide hover:text-foreground"
                  >
                    {ROW_COLUMN_LABELS[c]}
                    {sortBy === c && (
                      <span aria-hidden="true">{sortDir === "asc" ? "▲" : "▼"}</span>
                    )}
                  </button>
                ) : (
                  <span className="uppercase tracking-wide">
                    {ROW_COLUMN_LABELS[c]}
                    {sortBy === c && (
                      <span aria-hidden="true"> {sortDir === "asc" ? "▲" : "▼"}</span>
                    )}
                  </span>
                )}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr
              key={row.id}
              className={cn(
                "border-b border-border/40 last:border-0",
                onRowClick && "cursor-pointer hover:bg-accent/50",
              )}
              onClick={onRowClick ? () => onRowClick(row) : undefined}
            >
              <RowCells row={row} columns={columns} />
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/** The drill-down modal (C84T1): the items behind one chart segment.
 *  Title is "«Widget title» — «group label»"; closing returns to the
 *  dashboard — modal state only, no route change. Read affordance:
 *  view-only users drill and export too (the server enforces read). */
function DrilldownModal({
  widget,
  groupKey,
  groupLabel,
  onClose,
  onIssueClick,
}: {
  widget: DashboardWidget;
  groupKey: string;
  groupLabel: string;
  onClose: () => void;
  onIssueClick?: (row: WidgetRowItem) => void;
}) {
  const [page, setPage] = useState(1);
  const { data, isPending, isError, refetch } = useQuery({
    queryKey: widgetDrilldownKey(widget.id, groupKey, page),
    queryFn: () => fetchWidgetDrilldown(widget.id, groupKey, page),
    retry: false,
    staleTime: 60_000,
  });
  const pageCount =
    data && data.per_page > 0
      ? Math.max(1, Math.ceil(data.total / data.per_page))
      : 1;
  return (
    <Dialog
      open
      onOpenChange={(o) => {
        if (!o) onClose();
      }}
    >
      <DialogContent
        className="max-h-[88vh] overflow-y-auto sm:max-w-2xl"
        showCloseButton={false}
      >
        <DialogHeader>
          <DialogTitle>
            {widget.title} — {data?.group_label ?? groupLabel}
          </DialogTitle>
          <DialogDescription>
            {data
              ? `${data.total} work items · dashboard filters apply`
              : "Loading the items behind this segment…"}
          </DialogDescription>
        </DialogHeader>
        {isPending ? (
          <div className="flex flex-col gap-2 py-4" aria-label="Loading drill-down items">
            <Skeleton className="h-4 w-2/3" />
            <Skeleton className="h-4 w-1/2" />
            <Skeleton className="h-24" />
          </div>
        ) : isError ? (
          <div className="flex flex-col items-center justify-center gap-2 py-6 text-center">
            <p className="text-sm text-destructive">
              Couldn&apos;t load these items.
            </p>
            <Button type="button" variant="outline" size="sm" onClick={() => refetch()}>
              Retry
            </Button>
          </div>
        ) : data.items.length === 0 ? (
          <p className="py-6 text-center text-sm text-muted-foreground">
            No items in this segment.
          </p>
        ) : (
          <div className="overflow-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-border/60 text-left text-xs text-muted-foreground">
                  <th scope="col" className="px-3 py-2 font-medium">ID</th>
                  <th scope="col" className="px-3 py-2 font-medium">Title</th>
                  <th scope="col" className="px-3 py-2 font-medium">State</th>
                  <th scope="col" className="px-3 py-2 font-medium">Priority</th>
                  <th scope="col" className="px-3 py-2 font-medium">Assignee</th>
                </tr>
              </thead>
              <tbody>
                {data.items.map((item) => (
                  <tr
                    key={item.id}
                    className={cn(
                      "border-b border-border/40 last:border-0",
                      onIssueClick && "cursor-pointer hover:bg-accent/50",
                    )}
                    onClick={onIssueClick ? () => onIssueClick(item) : undefined}
                  >
                    <td className="whitespace-nowrap px-3 py-2 font-semibold text-primary">
                      {item.id}
                    </td>
                    <td className="max-w-[240px] truncate px-3 py-2 text-ink">
                      {item.title}
                    </td>
                    <td className="whitespace-nowrap px-3 py-2 text-ink">
                      {item.state}
                    </td>
                    <td className="whitespace-nowrap px-3 py-2 text-ink">
                      {priorityLabel(item.priority)}
                    </td>
                    <td className="whitespace-nowrap px-3 py-2 text-ink">
                      {item.assignee ? item.assignee.name : "Unassigned"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            <PaginationControls
              page={data.page}
              pageCount={pageCount}
              onPage={setPage}
            />
          </div>
        )}
        <DialogFooter>
          <Button type="button" variant="outline" onClick={onClose}>
            Close
          </Button>
          <Button
            type="button"
            onClick={() => {
              void downloadWidgetCsv(widget.id).catch(() => {
                // Read-gated server-side; a failed export leaves the
                // modal untouched (no partial download).
              });
            }}
          >
            <Download className="size-3.5" aria-hidden="true" />
            Export CSV
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export default function WidgetCard({
  widget,
  canEdit,
  onEdit,
  onDelete,
  drag,
  onIssueClick,
}: {
  widget: DashboardWidget;
  canEdit: boolean;
  onEdit: () => void;
  onDelete: () => void;
  drag?: WidgetDragBinding;
  /** Row-click navigation (DashboardDetail supplies the workspace
   *  slug + project-identifier resolution — the row payload carries
   *  only the project {id, name}). Absent = rows are not clickable. */
  onIssueClick?: (row: WidgetRowItem) => void;
}) {
  const paginated = isPaginatedType(widget.widget_type);
  const isRowList = isRowListType(widget.widget_type);
  const isIntake = widget.widget_type === "intake_breakdown";
  const isPql = widget.widget_type === "pql_table";
  const [page, setPage] = useState(1);
  const {
    data,
    isPending,
    isError,
    error,
    refetch,
  } = useQuery({
    queryKey: widgetDataKey(widget.id, paginated || isRowList ? page : 1),
    queryFn: () => fetchWidgetData(widget.id, paginated || isRowList ? page : 1),
    // Widget data is live but cheap; a failure shouldn't hammer.
    retry: 1,
    staleTime: 60_000,
  });

  // Drill-down modal state (C84T1): the clicked segment's group key
  // (backend matches key OR label) + display label. Modal state only
  // — opening it never changes the route. C85T1: intake tiles set
  // `metric` instead, opening the intake drill-down modal. C87T1:
  // trend-bucket clicks set `bucket` (the CURRENT granularity at
  // click time + the trend payload's bucket_start), opening the
  // same intake modal in bucket mode — no metric, all rows in the
  // bucket. Clicking is a read affordance: view-only readers drill
  // buckets too (no canEdit gate anywhere on this state).
  const [drill, setDrill] = useState<{
    key: string;
    label: string;
    metric?: IntakeMetric;
    bucket?: {
      granularity: IntakeTrendGranularity;
      bucket_start: string;
    };
  } | null>(null);
  // C86T1: the trend granularity toggle lives in the trend section,
  // but the Trend CSV menu item (header) needs the current value —
  // so the card owns it and the section is controlled.
  const [trendGranularity, setTrendGranularity] =
    useState<IntakeTrendGranularity>("week");
  const drillable = isDrillableType(widget.widget_type);
  const openDrill = (group: WidgetDataGroup) =>
    setDrill({ key: group.key, label: group.label });
  const openDrillByLabel = (label: string) =>
    setDrill({ key: label, label });

  // Row-list sort state mirrors the widget config (sort is config, not
  // ephemeral table state). It syncs when the parent refetches the
  // dashboard, and updates optimistically on a header click.
  const rowCfg = normalizeWidgetConfig(widget.config);
  const [rowSort, setRowSort] = useState({
    sortBy: rowCfg.sort_by,
    sortDir: rowCfg.sort_dir,
  });
  useEffect(() => {
    setRowSort({ sortBy: rowCfg.sort_by, sortDir: rowCfg.sort_dir });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [widget.config]);
  const queryClient = useQueryClient();
  const sortMutation = useMutation({
    mutationFn: (v: { sort_by: WidgetRowColumn; sort_dir: "asc" | "desc" }) =>
      updateWidget(widget.id, {
        config: { ...widget.config, sort_by: v.sort_by, sort_dir: v.sort_dir },
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["widget-data", widget.id],
      });
    },
    onError: () => {
      // Roll the header arrow back — the server kept the old sort.
      setRowSort({ sortBy: rowCfg.sort_by, sortDir: rowCfg.sort_dir });
    },
  });
  const handleSort = (column: WidgetRowColumn) => {
    if (!canEdit) return;
    const next =
      rowSort.sortBy === column
        ? { sortBy: column, sortDir: (rowSort.sortDir === "asc" ? "desc" : "asc") as "asc" | "desc" }
        : { sortBy: column, sortDir: "desc" as const };
    setRowSort(next);
    sortMutation.mutate({ sort_by: next.sortBy, sort_dir: next.sortDir });
  };

  const isNumber = widget.widget_type === "number";
  const series = chartSeries(data?.groups);
  const scalar = numberValue(data);
  const groups = data?.groups ?? [];
  const rows = data?.rows ?? [];
  const empty = isIntake
    ? !data?.intake
    : isRowList
      ? rows.length === 0
      : paginated
        ? groups.length === 0
        : isNumber
          ? scalar === null
          : series.every((s) => s.value === 0) || series.length === 0;

  let body: ReactNode;
  if (isPending) {
    body = (
      <div className="flex h-full flex-col gap-2 p-4" aria-label="Loading widget data">
        <Skeleton className="h-4 w-2/3" />
        <Skeleton className="h-4 w-1/2" />
        <Skeleton className="flex-1" />
      </div>
    );
  } else if (isError) {
    // C100T1: a pql_table data-route 400 is a per-widget query error
    // (a renamed state/epic/label in the stored query) — show the
    // server's PQL message verbatim with an Edit affordance so the
    // query can be fixed in place. Every other type keeps the generic
    // error state byte-for-byte.
    body = isPql ? (
      <div className="flex h-full flex-col items-center justify-center gap-2 p-4 text-center">
        <p className="text-sm text-destructive">
          {`Query error: ${error instanceof Error ? error.message : "Couldn't load this widget."}`}
        </p>
        <p className="text-xs text-muted-foreground">
          The query may name something that was renamed. Edit the
          widget to fix the query.
        </p>
        <div className="flex gap-2">
          {canEdit && (
            <Button type="button" size="sm" onClick={onEdit}>
              Edit widget
            </Button>
          )}
          <Button type="button" variant="outline" size="sm" onClick={() => refetch()}>
            Retry
          </Button>
        </div>
      </div>
    ) : (
      <div className="flex h-full flex-col items-center justify-center gap-2 p-4 text-center">
        <p className="text-sm text-destructive">Couldn't load this widget.</p>
        <Button type="button" variant="outline" size="sm" onClick={() => refetch()}>
          Retry
        </Button>
      </div>
    );
  } else if (empty) {
    body = (
      <div className="flex h-full items-center justify-center p-4">
        <p className="text-sm text-muted-foreground">
          {isPql
            ? "No issues match this query."
            : "No data matches these filters."}
        </p>
      </div>
    );
  } else if (isNumber) {
    body = (
      <div className="flex h-full items-center justify-center p-4">
        <span className="text-5xl font-bold tabular-nums text-ink" aria-label={`Value: ${scalar}`}>
          {scalar}
        </span>
      </div>
    );
  } else if (widget.widget_type === "smart_counter") {
    body = (
      <SmartCounterBody
        widget={widget}
        groups={groups}
        onGroupClick={drillable ? openDrill : undefined}
      />
    );
  } else if (widget.widget_type === "smart_gauge") {
    body = (
      <SmartGaugeBody
        widget={widget}
        groups={groups}
        onGroupClick={drillable ? openDrill : undefined}
      />
    );
  } else if (widget.widget_type === "table_2d") {
    body = (
      <div className="h-full overflow-auto">
        <GroupRowsTable
          groups={groups}
          sortDesc
          onRowClick={drillable ? openDrill : undefined}
        />
      </div>
    );
  } else if (isIntake) {
    // C86T1: the trend section renders BELOW the tiles in grid view
    // and BELOW the table in table view — one scrollable column for
    // both, so the chart never squeezes the tiles.
    const intake = data!.intake!;
    const hasIntake =
      (intake.in_app_volume ?? 0) +
        (intake.form_volume ?? 0) +
        (intake.email_volume ?? 0) >
      0;
    body = (
      <div className="flex h-full flex-col overflow-auto">
        <IntakeBody
          widget={widget}
          data={data!}
          onTileClick={(tile) => {
            if (tile.metric) {
              setDrill({ key: tile.metric, label: tile.label, metric: tile.metric });
            }
          }}
        />
        <IntakeTrendSection
          widgetId={widget.id}
          granularity={trendGranularity}
          onGranularity={setTrendGranularity}
          hasIntake={hasIntake}
          onBucketClick={(bucket_start) =>
            setDrill({
              key: `bucket:${trendGranularity}:${bucket_start}`,
              label: intakeBucketTitle(trendGranularity, bucket_start),
              bucket: {
                granularity: trendGranularity,
                bucket_start,
              },
            })
          }
        />
      </div>
    );
  } else if (isRowList) {
    body = (
      <WorkItemsTable
        rows={rows}
        columns={rowCfg.columns}
        sortBy={rowSort.sortBy}
        sortDir={rowSort.sortDir}
        canEdit={canEdit}
        onSort={handleSort}
        onRowClick={onIssueClick}
      />
    );
  } else {
    body = (
      <div
        className={cn("h-full w-full p-2", drillable && "cursor-pointer")}
        title={drillable ? "Click a segment to drill down" : undefined}
      >
        <ResponsiveContainer width="100%" height="100%">
          <WidgetChart
            type={widget.widget_type}
            series={series}
            onSegmentClick={drillable ? openDrillByLabel : undefined}
          />
        </ResponsiveContainer>
      </div>
    );
  }

  const pageCount = isRowList
    ? widgetRowPageCount(data)
    : widgetPageCount(data);

  return (
    <>
    <div
      className={cn(
        "flex h-full flex-col overflow-hidden rounded-lg border border-border bg-raised shadow-sm",
        drag?.isDragging && "opacity-70 shadow-lg",
      )}
      data-widget-id={widget.id}
    >
      <div className="flex h-10 shrink-0 items-center gap-1 border-b border-border/60 px-2">
        {canEdit && drag && (
          <button
            type="button"
            {...drag.attributes}
            {...drag.listeners}
            aria-label={`Drag ${widget.title} to reposition`}
            className="flex size-7 shrink-0 cursor-grab items-center justify-center rounded text-muted-foreground hover:bg-accent hover:text-foreground active:cursor-grabbing"
          >
            <GripVertical className="size-4" aria-hidden="true" />
          </button>
        )}
        <h3 className="min-w-0 flex-1 truncate px-1 text-[13px] font-semibold text-ink">
          {widget.title}
          {widget.widget_type === "assigned_to_me" && (
            <span className="ml-2 inline-flex items-center rounded-full bg-primary/10 px-2 py-0.5 align-middle text-[11px] font-medium text-primary">
              Assigned to you
            </span>
          )}
        </h3>
        <DropdownMenu>
          <DropdownMenuTrigger
            aria-label={`Widget options for ${widget.title}`}
            className="flex size-7 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-accent hover:text-foreground"
          >
            <MoreHorizontal className="size-4" aria-hidden="true" />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem
              onClick={() => {
                void downloadWidgetCsv(widget.id).catch(() => {
                  // The server enforces read access; a failed export
                  // leaves the card untouched (no partial download).
                });
              }}
            >
              <Download className="size-3.5" aria-hidden="true" />
              Export CSV
            </DropdownMenuItem>
            {isIntake && (
              <DropdownMenuItem
                onClick={() => {
                  void downloadWidgetTrendCsv(widget.id, trendGranularity).catch(() => {
                    // The server enforces read access; a failed export
                    // leaves the card untouched (no partial download).
                  });
                }}
              >
                <Download className="size-3.5" aria-hidden="true" />
                Trend CSV
              </DropdownMenuItem>
            )}
            {canEdit && (
              <>
                <DropdownMenuItem onClick={onEdit}>
                  <Pencil className="size-3.5" aria-hidden="true" />
                  Edit widget
                </DropdownMenuItem>
                <DropdownMenuItem
                  onClick={onDelete}
                  className="text-destructive focus:text-destructive"
                >
                  <Trash2 className="size-3.5" aria-hidden="true" />
                  Delete widget
                </DropdownMenuItem>
              </>
            )}
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
      {/* C100T1: the stored query under the header (prototype-as-spec)
          so a PQL widget is self-explanatory. */}
      {isPql && widget.config.query && (
        <div
          className="shrink-0 truncate border-b border-border/60 px-3 py-1 text-xs text-muted-foreground"
          title={widget.config.query}
        >
          <span className="font-medium text-primary">PQL</span>
          <code className="ml-2 rounded bg-muted px-1 py-0.5">
            {widget.config.query}
          </code>
        </div>
      )}
      <div className="min-h-0 flex-1">{body}</div>
      {(paginated || isRowList) && !isPending && !isError && (
        <PaginationControls
          page={data?.page ?? page}
          pageCount={pageCount}
          onPage={setPage}
        />
      )}
    </div>
    {drill && drill.bucket ? (
      <IntakeDrilldownModal
        widget={widget}
        metricLabel={drill.label}
        bucket={drill.bucket}
        onClose={() => setDrill(null)}
        onIssueClick={onIssueClick}
      />
    ) : drill && drill.metric ? (
      <IntakeDrilldownModal
        widget={widget}
        metric={drill.metric}
        metricLabel={drill.label}
        onClose={() => setDrill(null)}
        onIssueClick={onIssueClick}
      />
    ) : drill ? (
      <DrilldownModal
        widget={widget}
        groupKey={drill.key}
        groupLabel={drill.label}
        onClose={() => setDrill(null)}
        onIssueClick={onIssueClick}
      />
    ) : null}
    </>
  );
}
