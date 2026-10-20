// C81T1: the dashboard canvas — a 12-column responsive grid where
// widgets are absolutely positioned cards. Drag to reposition uses
// @dnd-kit/core's useDraggable (the same primitive the calendar's
// DraggableIssueChip uses — sortable is wrong for free grid
// positioning); the corner handle resizes via pointer events. Layout
// commits go to PATCH /widgets/{id}/layout on drop / pointer-up only
// (never during the gesture), optimistically with rollback on failure.
// Non-editors get a read-only canvas: no handles, no affordances.
import {
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";
import { DndContext, useDraggable, type DragEndEvent } from "@dnd-kit/core";
import { CSS } from "@dnd-kit/utilities";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { LayoutDashboard, Plus } from "lucide-react";
import {
  canvasHeight,
  gridGeometry,
  resolveDropLayout,
  resolveResizeLayout,
  widgetStyle,
  type GridGeometry,
  type GridLayout,
} from "../../lib/dashboardGrid";
import {
  dashboardKey,
  patchWidgetLayout,
  type DashboardWidget,
  type LayoutPatch,
} from "../../lib/dashboards";
import WidgetCard from "./WidgetCard";
import { Button } from "../ui/button";

/** Optimistic layout commit: PATCH the layout endpoint; roll the
 *  dashboard cache back on failure and invalidate on settle. */
function useLayoutCommit(slug: string, dashboardId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (v: { widgetId: string; layout: LayoutPatch }) =>
      patchWidgetLayout(v.widgetId, v.layout),
    onMutate: async (v) => {
      const key = dashboardKey(slug, dashboardId);
      await queryClient.cancelQueries({ queryKey: key });
      const prev = queryClient.getQueryData<{ widgets?: DashboardWidget[] }>(key);
      queryClient.setQueryData<{ widgets?: DashboardWidget[] }>(key, (old) =>
        old
          ? {
              ...old,
              widgets: (old.widgets ?? []).map((w) =>
                w.id === v.widgetId ? { ...w, ...v.layout } : w,
              ),
            }
          : old,
      );
      return { prev };
    },
    onError: (_e, _v, ctx) => {
      if (ctx?.prev)
        queryClient.setQueryData(dashboardKey(slug, dashboardId), ctx.prev);
    },
    onSettled: () => {
      void queryClient.invalidateQueries({
        queryKey: dashboardKey(slug, dashboardId),
      });
    },
  });
}

function DraggableWidget({
  widget,
  geo,
  canEdit,
  preview,
  onEdit,
  onDelete,
  onResizeStart,
}: {
  widget: DashboardWidget;
  geo: GridGeometry;
  canEdit: boolean;
  /** Live resize preview overrides the committed layout, if any. */
  preview: GridLayout | undefined;
  onEdit: (w: DashboardWidget) => void;
  onDelete: (w: DashboardWidget) => void;
  onResizeStart: (e: React.PointerEvent, widget: DashboardWidget) => void;
}) {
  const layout: GridLayout = preview ?? widget;
  const { attributes, listeners, setNodeRef, transform, isDragging } =
    useDraggable({
      id: widget.id,
      data: { widgetId: widget.id },
      disabled: !canEdit,
    });

  const pos = widgetStyle(layout, geo);

  return (
    <div
      ref={setNodeRef}
      className="absolute"
      style={{
        left: pos.left,
        top: pos.top,
        width: pos.width,
        height: pos.height,
        // The whole card follows the drag transform; the listeners live
        // on the header grip inside WidgetCard so chart interactions
        // stay intact.
        ...(transform ? { transform: CSS.Translate.toString(transform) } : {}),
        zIndex: isDragging ? 20 : undefined,
      }}
    >
      <WidgetCard
        widget={widget}
        canEdit={canEdit}
        onEdit={() => onEdit(widget)}
        onDelete={() => onDelete(widget)}
        drag={
          canEdit
            ? { attributes, listeners: listeners ?? undefined, isDragging }
            : undefined
        }
      />
      {canEdit && !isDragging && (
        <button
          type="button"
          onPointerDown={(e) => onResizeStart(e, widget)}
          aria-label={`Resize ${widget.title}`}
          className="absolute -bottom-1 -right-1 z-10 flex size-5 cursor-nwse-resize items-center justify-center rounded-br-lg text-muted-foreground opacity-0 transition-opacity hover:bg-accent hover:text-foreground hover:opacity-100 focus:opacity-100 group-hover:opacity-100"
          style={{ touchAction: "none" }}
        >
          <svg viewBox="0 0 10 10" className="size-3" aria-hidden="true">
            <path d="M9 1 L1 9 M9 5 L5 9" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
          </svg>
        </button>
      )}
    </div>
  );
}

export default function DashboardCanvas({
  slug,
  dashboardId,
  widgets,
  canEdit,
  onEditWidget,
  onDeleteWidget,
  onAddWidget,
}: {
  slug: string;
  dashboardId: string;
  widgets: DashboardWidget[];
  canEdit: boolean;
  onEditWidget: (w: DashboardWidget) => void;
  onDeleteWidget: (w: DashboardWidget) => void;
  onAddWidget: () => void;
}) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(0);
  const commit = useLayoutCommit(slug, dashboardId);
  // Live resize preview: widget id → in-progress layout. Committed on
  // pointer-up only (debounced by construction).
  const [resizePreview, setResizePreview] = useState<Record<string, GridLayout>>({});
  const resizeState = useRef<{
    widget: DashboardWidget;
    startX: number;
    startY: number;
  } | null>(null);

  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    const ro = new ResizeObserver((entries) => {
      const w = entries[0]?.contentRect.width ?? 0;
      setWidth(w);
    });
    ro.observe(el);
    setWidth(el.clientWidth);
    return () => ro.disconnect();
  }, []);

  const geo = gridGeometry(width);

  const handleDragEnd = useCallback(
    (event: DragEndEvent) => {
      const widgetId = event.active.data.current?.widgetId as string | undefined;
      const widget = widgets.find((w) => w.id === widgetId);
      if (!widget || !event.delta) return;
      const next = resolveDropLayout(
        widget,
        event.delta.x,
        event.delta.y,
        geo,
      );
      // null = the drop changed nothing → no PATCH (dashboardGrid).
      if (next) {
        commit.mutate({
          widgetId: widget.id,
          layout: { ...next, grid_w: widget.grid_w, grid_h: widget.grid_h },
        });
      }
    },
    [widgets, geo, commit],
  );

  const handleResizeStart = useCallback(
    (e: React.PointerEvent, widget: DashboardWidget) => {
      if (!canEdit) return;
      e.preventDefault();
      e.stopPropagation();
      resizeState.current = {
        widget,
        startX: e.clientX,
        startY: e.clientY,
      };
      const onMove = (ev: PointerEvent) => {
        const st = resizeState.current;
        if (!st) return;
        const next = resolveResizeLayout(
          st.widget,
          ev.clientX - st.startX,
          ev.clientY - st.startY,
          gridGeometry(containerRef.current?.clientWidth ?? 0),
        );
        // null = below one grid unit of movement → keep the committed
        // layout (no jitter); otherwise preview the new size.
        setResizePreview((prev) => {
          if (!next) {
            if (!prev[st.widget.id]) return prev;
            const { [st.widget.id]: _drop, ...rest } = prev;
            return rest;
          }
          return { ...prev, [st.widget.id]: { ...st.widget, ...next } };
        });
      };
      const onUp = (ev: PointerEvent) => {
        const st = resizeState.current;
        resizeState.current = null;
        window.removeEventListener("pointermove", onMove);
        window.removeEventListener("pointerup", onUp);
        if (!st) return;
        const next = resolveResizeLayout(
          st.widget,
          ev.clientX - st.startX,
          ev.clientY - st.startY,
          gridGeometry(containerRef.current?.clientWidth ?? 0),
        );
        setResizePreview((prev) => {
          const { [st.widget.id]: _drop, ...rest } = prev;
          return rest;
        });
        if (next) {
          commit.mutate({
            widgetId: st.widget.id,
            layout: {
              grid_x: st.widget.grid_x,
              grid_y: st.widget.grid_y,
              ...next,
            },
          });
        }
      };
      window.addEventListener("pointermove", onMove);
      window.addEventListener("pointerup", onUp);
    },
    [canEdit, commit],
  );

  if (widgets.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center gap-3 rounded-xl border border-dashed border-border bg-surface/50 px-6 py-16 text-center">
        <span className="flex size-12 items-center justify-center rounded-full bg-muted">
          <LayoutDashboard className="size-6 text-muted-foreground" aria-hidden="true" />
        </span>
        <div>
          <p className="text-sm font-semibold text-ink">
            This dashboard is empty
          </p>
          <p className="mt-1 max-w-sm text-sm text-muted-foreground">
            {canEdit
              ? "Add your first widget to start visualizing issue data."
              : "Widgets will appear here once someone with edit access adds them."}
          </p>
        </div>
        {canEdit && (
          <Button type="button" onClick={onAddWidget}>
            <Plus className="size-4" aria-hidden="true" />
            Add your first widget
          </Button>
        )}
      </div>
    );
  }

  return (
    <DndContext onDragEnd={handleDragEnd}>
      <div ref={containerRef} className="group relative w-full">
        <div
          className="relative w-full"
          style={{ height: canvasHeight(widgets, geo) }}
        >
          {widgets.map((w) => (
            <DraggableWidget
              key={w.id}
              widget={w}
              geo={geo}
              canEdit={canEdit}
              preview={resizePreview[w.id]}
              onEdit={onEditWidget}
              onDelete={onDeleteWidget}
              onResizeStart={handleResizeStart}
            />
          ))}
        </div>
      </div>
    </DndContext>
  );
}
