import { useDroppable } from "@dnd-kit/core";
import type { ReactNode } from "react";
import { cn } from "../../lib/utils";

/**
 * The droppable id of the unscheduled strip (C26T2). Kept distinct from
 * the day-key ids the month/week cells use ("YYYY-MM-DD") so the drag-end
 * handler can route strip drops to the unschedule semantics.
 */
export const UNSCHEDULED_STRIP_ID = "unscheduled-strip";

/**
 * The "Unscheduled" strip as a drop zone for dated chips (C26T2). Follows
 * the DroppableDayCell pattern: `useDroppable`, and the `isOver` highlight
 * speaks the same board drop language (`border-accent-blue/70` + wash).
 * `data-droppable-id` exposes the drop target to component tests.
 */
export function DroppableUnscheduledStrip({
  children,
}: {
  children: ReactNode;
}) {
  const { setNodeRef, isOver } = useDroppable({ id: UNSCHEDULED_STRIP_ID });
  return (
    <div
      ref={setNodeRef}
      data-droppable-id={UNSCHEDULED_STRIP_ID}
      className={cn(
        "flex flex-wrap gap-1 rounded-md border border-border bg-surface p-2",
        isOver && "border-accent-blue/70 bg-[rgba(79,142,247,0.08)]",
      )}
    >
      {children}
    </div>
  );
}
