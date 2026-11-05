import {
  DndContext,
  DragOverlay,
  KeyboardSensor,
  PointerSensor,
  rectIntersection,
  useSensor,
  useSensors,
} from "@dnd-kit/core";
import type {
  DragCancelEvent,
  DragEndEvent,
  DragOverEvent,
  DragStartEvent,
} from "@dnd-kit/core";
import { sortableKeyboardCoordinates } from "@dnd-kit/sortable";
import type { ReactNode } from "react";

/**
 * DndContext wrapper for calendar schedule drags (C26T0). Mirrors the
 * board's sensor setup exactly: PointerSensor with a small activation
 * distance so click-to-peek keeps working, plus the board's keyboard
 * sensor. Day cells are plain rect droppables, so rectIntersection is the
 * collision detection (the board's closestCorners is for sortable cards).
 */
export function ScheduleDnd({
  children,
  overlay,
  onDragStart,
  onDragOver,
  onDragEnd,
  onDragCancel,
}: {
  children: ReactNode;
  overlay?: ReactNode;
  onDragStart: (e: DragStartEvent) => void;
  /**
   * C159T1: fires whenever the droppable under the pointer changes
   * mid-drag (pointer and keyboard drags alike) — the page tracks
   * the hovered day for the ghost preview. Optional, default no-op.
   */
  onDragOver?: (e: DragOverEvent) => void;
  onDragEnd: (e: DragEndEvent) => void;
  /**
   * C159T1: pointer-cancel / Escape — the page clears the ghost
   * preview alongside the drag state. Optional, default no-op.
   */
  onDragCancel?: (e: DragCancelEvent) => void;
}) {
  const pointerSensor = useSensor(PointerSensor, {
    activationConstraint: { distance: 6 },
  });
  // KeyboardSensor enables keyboard dragging, same as the board: focus a
  // chip, Space lifts it, arrow keys move it between days, Space drops,
  // Esc cancels.
  const keyboardSensor = useSensor(KeyboardSensor, {
    coordinateGetter: sortableKeyboardCoordinates,
  });
  const sensors = useSensors(pointerSensor, keyboardSensor);
  return (
    <DndContext
      sensors={sensors}
      collisionDetection={rectIntersection}
      onDragStart={onDragStart}
      onDragOver={onDragOver ?? (() => {})}
      onDragEnd={onDragEnd}
      onDragCancel={onDragCancel ?? (() => {})}
    >
      {children}
      <DragOverlay>{overlay}</DragOverlay>
    </DndContext>
  );
}
