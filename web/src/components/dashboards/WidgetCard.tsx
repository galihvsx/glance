// C81T1: a single dashboard widget card — title, per-widget overflow
// menu (edit/delete, canEdit only), drag handle binding (canEdit only),
// and the recharts renderer for the six widget types with per-widget
// loading/empty/error states. Widget data is fetched per widget via
// GET /widgets/{id}/data with react-query caching (the detail endpoint
// returns widgets with config only, per the T0 contract).
import type { ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { GripVertical, MoreHorizontal, Pencil, Trash2 } from "lucide-react";
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
  fetchWidgetData,
  numberValue,
  seriesColor,
  widgetDataKey,
  type DashboardWidget,
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
  const {
    data,
    isPending,
    isError,
    refetch,
  } = useQuery({
    queryKey: widgetDataKey(widget.id),
    queryFn: () => fetchWidgetData(widget.id),
    // Widget data is live but cheap; a failure shouldn't hammer.
    retry: 1,
    staleTime: 60_000,
  });

  const isNumber = widget.widget_type === "number";
  const series = chartSeries(data?.groups);
  const scalar = numberValue(data);
  const empty = isNumber ? scalar === null : series.every((s) => s.value === 0) || series.length === 0;

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
  } else {
    body = (
      <div className="h-full w-full p-2">
        <ResponsiveContainer width="100%" height="100%">
          <WidgetChart type={widget.widget_type} series={series} />
        </ResponsiveContainer>
      </div>
    );
  }

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
        {canEdit && (
          <DropdownMenu>
            <DropdownMenuTrigger
              aria-label={`Widget options for ${widget.title}`}
              className="flex size-7 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-accent hover:text-foreground"
            >
              <MoreHorizontal className="size-4" aria-hidden="true" />
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
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
            </DropdownMenuContent>
          </DropdownMenu>
        )}
      </div>
      <div className="min-h-0 flex-1">{body}</div>
    </div>
  );
}
