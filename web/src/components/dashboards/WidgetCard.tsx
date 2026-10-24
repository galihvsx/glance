// C81T1: a single dashboard widget card — title, per-widget overflow
// menu (edit/delete for editors, Export CSV for anyone who can read
// the widget), drag handle binding (canEdit only), and the recharts
// renderer for the six widget types with per-widget loading/empty/
// error states. Widget data is fetched per widget via
// GET /widgets/{id}/data with react-query caching (the detail endpoint
// returns widgets with config only, per the T0 contract).
// C83T1: the four phase-2 renderers — smart_counter (grid tiles or
// table), smart_gauge (SVG semi-circular arcs), table_2d (desc-sorted
// rows), intake_breakdown (six headline tiles) — with page state
// driving the paginated data envelope.
import { useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
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
  EMAIL_INGESTION_NOTE,
  fetchWidgetData,
  formatDuration,
  INTAKE_TILES,
  isPaginatedType,
  normalizeWidgetConfig,
  numberValue,
  semiGaugeArc,
  seriesColor,
  widgetDataKey,
  widgetPageCount,
  DEFAULT_GAUGE_COLOR,
  type DashboardWidget,
  type WidgetData,
  type WidgetDataGroup,
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

function WidgetChart({ type, series }: { type: WidgetType; series: { name: string; value: number }[] }) {
  const cells = series.map((_, i) => (
    <Cell key={i} fill={seriesColor(i)} />
  ));
  if (type === "bar") {
    return (
      <BarChart data={series} margin={{ top: 8, right: 8, bottom: 0, left: -12 }}>
        <CartesianGrid strokeDasharray="3 3" stroke="var(--color-border)" vertical={false} />
        <XAxis dataKey="name" tick={axisTick} interval="preserveStartEnd" tickLine={false} axisLine={{ stroke: "var(--color-border)" }} />
        <YAxis tick={axisTick} tickLine={false} axisLine={false} allowDecimals={false} width={40} />
        <Tooltip content={<ChartTooltipContent />} cursor={{ fill: "var(--color-wash-hover)" }} />
        <Bar dataKey="value" radius={[4, 4, 0, 0]}>
          {cells}
        </Bar>
      </BarChart>
    );
  }
  if (type === "line") {
    return (
      <LineChart data={series} margin={{ top: 8, right: 8, bottom: 0, left: -12 }}>
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
      <AreaChart data={series} margin={{ top: 8, right: 8, bottom: 0, left: -12 }}>
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
}: {
  groups: WidgetDataGroup[];
  sortDesc?: boolean;
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
          <tr key={g.key} className="border-b border-border/40 last:border-0">
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
}: {
  widget: DashboardWidget;
  groups: WidgetDataGroup[];
}) {
  const cfg = normalizeWidgetConfig(widget.config);
  if (cfg.visualization === "table") {
    return (
      <div className="h-full overflow-auto">
        <GroupRowsTable groups={groups} />
      </div>
    );
  }
  return (
    <div className="grid h-full grid-cols-2 content-start gap-2 overflow-auto p-3">
      {groups.map((g) => (
        <div
          key={g.key}
          className="rounded-md border border-border/60 bg-surface px-3 py-2.5"
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
}: {
  widget: DashboardWidget;
  groups: WidgetDataGroup[];
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
          <div key={g.key} className="flex flex-col items-center gap-1">
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

function IntakeBody({
  widget,
  data,
}: {
  widget: DashboardWidget;
  data: WidgetData;
}) {
  const intake = data.intake!;
  const cfg = normalizeWidgetConfig(widget.config);
  if (cfg.view === "table") {
    return (
      <table className="w-full text-sm">
        <tbody>
          {INTAKE_TILES.map((tile) => {
            const raw = intake[tile.key];
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
                  {tile.duration ? formatDuration(raw) : raw}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    );
  }
  return (
    <div className="grid h-full grid-cols-2 content-start gap-2 overflow-auto p-3 sm:grid-cols-3">
      {INTAKE_TILES.map((tile) => {
        const raw = intake[tile.key];
        return (
          <div
            key={tile.key}
            className="rounded-md border border-border/60 bg-surface px-3 py-2.5"
            {...(tile.honestEmpty ? { title: EMAIL_INGESTION_NOTE } : {})}
          >
            <p className="truncate text-xs text-muted-foreground">
              {tile.label}
            </p>
            <p className="text-2xl font-bold tabular-nums text-ink">
              {tile.duration ? formatDuration(raw) : raw}
            </p>
          </div>
        );
      })}
    </div>
  );
}

export default function WidgetCard({
  widget,
  canEdit,
  onEdit,
  onDelete,
  drag,
}: {
  widget: DashboardWidget;
  canEdit: boolean;
  onEdit: () => void;
  onDelete: () => void;
  drag?: WidgetDragBinding;
}) {
  const paginated = isPaginatedType(widget.widget_type);
  const isIntake = widget.widget_type === "intake_breakdown";
  const [page, setPage] = useState(1);
  const {
    data,
    isPending,
    isError,
    refetch,
  } = useQuery({
    queryKey: widgetDataKey(widget.id, paginated ? page : 1),
    queryFn: () => fetchWidgetData(widget.id, paginated ? page : 1),
    // Widget data is live but cheap; a failure shouldn't hammer.
    retry: 1,
    staleTime: 60_000,
  });

  const isNumber = widget.widget_type === "number";
  const series = chartSeries(data?.groups);
  const scalar = numberValue(data);
  const groups = data?.groups ?? [];
  const empty = isIntake
    ? !data?.intake
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
    body = (
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
          No data matches these filters.
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
    body = <SmartCounterBody widget={widget} groups={groups} />;
  } else if (widget.widget_type === "smart_gauge") {
    body = <SmartGaugeBody widget={widget} groups={groups} />;
  } else if (widget.widget_type === "table_2d") {
    body = (
      <div className="h-full overflow-auto">
        <GroupRowsTable groups={groups} sortDesc />
      </div>
    );
  } else if (isIntake) {
    body = <IntakeBody widget={widget} data={data!} />;
  } else {
    body = (
      <div className="h-full w-full p-2">
        <ResponsiveContainer width="100%" height="100%">
          <WidgetChart type={widget.widget_type} series={series} />
        </ResponsiveContainer>
      </div>
    );
  }

  const pageCount = widgetPageCount(data);

  return (
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
      <div className="min-h-0 flex-1">{body}</div>
      {paginated && !isPending && !isError && (
        <PaginationControls
          page={data?.page ?? page}
          pageCount={pageCount}
          onPage={setPage}
        />
      )}
    </div>
  );
}
