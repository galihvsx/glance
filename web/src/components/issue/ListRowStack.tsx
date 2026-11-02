import { useState, type ReactNode } from "react";
import {
  DndContext,
  KeyboardSensor,
  PointerSensor,
  useSensor,
  useSensors,
  DragOverlay,
} from "@dnd-kit/core";
import {
  SortableContext,
  sortableKeyboardCoordinates,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable";
import type { Issue } from "../../lib/types";

/**
 * C126T0: one dnd-kit context per list-view row stack (flat list,
 * per-state group, or per-sub-section list), so a drag can only
 * reorder within its own stack. Group/sub-group headers are NOT drop
 * targets — they render outside this component, and the SortableContext
 * items are exactly the stack's top-level rows.
 *
 * When `draggable` is false this renders only its children (a bare
 * fragment adds no DOM), so disabled stacks are byte-identical to
 * today's rendering. The overlay renders the dragged IssueRow face
 * via `renderOverlayRow` (board precedent: the card face is shared by
 * the sortable item and the overlay) — which also means the mouseup
 * click after a drag lands on the overlay, not the row, so a drag
 * never opens the peek drawer.
 */
export default function ListRowStack({
  rows,
  draggable,
  onDrop,
  onDragStart,
  renderOverlayRow,
  children,
}: {
  /** The stack's visible top-level rows, in display order. */
  rows: Issue[];
  draggable: boolean;
  /** (draggedId, hoveredId) — the page runs the drop pipeline. */
  onDrop: (activeId: string, overId: string) => void;
  /** Fired when a drag starts (the page clears a stale reorder error). */
  onDragStart?: () => void;
  /** Plain (non-sortable) IssueRow face for the drag overlay. */
  renderOverlayRow: (issue: Issue) => ReactNode;
  children: ReactNode;
}) {
  const [activeId, setActiveId] = useState<string | null>(null);
  // Board precedent: 6px pointer activation (a plain click is never a
  // drag) + keyboard dragging via sortableKeyboardCoordinates.
  const pointerSensor = useSensor(PointerSensor, {
    activationConstraint: { distance: 6 },
  });
  const keyboardSensor = useSensor(KeyboardSensor, {
    coordinateGetter: sortableKeyboardCoordinates,
  });
  const sensors = useSensors(pointerSensor, keyboardSensor);

  if (!draggable) return <>{children}</>;

  const activeIssue = activeId
    ? rows.find((i) => i.id === activeId)
    : undefined;
  return (
    <DndContext
      sensors={sensors}
      onDragStart={(e) => {
        setActiveId(String(e.active.id));
        onDragStart?.();
      }}
      onDragEnd={(e) => {
        setActiveId(null);
        if (!e.over) return;
        const active = String(e.active.id);
        const over = String(e.over.id);
        if (active === over) return;
        onDrop(active, over);
      }}
      onDragCancel={() => setActiveId(null)}
    >
      <SortableContext
        items={rows.map((i) => i.id)}
        strategy={verticalListSortingStrategy}
      >
        {children}
      </SortableContext>
      <DragOverlay>
        {activeIssue ? renderOverlayRow(activeIssue) : null}
      </DragOverlay>
    </DndContext>
  );
}
