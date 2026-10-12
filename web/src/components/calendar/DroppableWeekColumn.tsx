import { useDroppable } from "@dnd-kit/core";
import type { ReactNode } from "react";
import { cn } from "../../lib/utils";

/**
 * A week-view day column that accepts dragged issue chips (C26T1). Same
 * drop semantics and highlight language as DroppableDayCell — the droppable
 * id is the day key, and `isOver` speaks the board column's drop language
 * (`border-accent-blue/70`) with a matching wash. Layout classes match the
 * week column: tall (`min-h-64`), no internal scroll, so the highlight
 * covers the full column.
 */
export function DroppableWeekColumn({
  dayKey,
  isToday,
  children,
}: {
  dayKey: string;
  isToday: boolean;
  children: ReactNode;
}) {
  const { setNodeRef, isOver } = useDroppable({ id: dayKey });
  return (
    <div
      ref={setNodeRef}
      data-testid={`week-column-${dayKey}`}
      className={cn(
        "min-h-64 p-1 [&:not(:last-child)]:border-r [&:not(:last-child)]:border-r-border-hairline",
        isToday && "bg-[rgba(79,142,247,0.05)]",
        isOver && "border-accent-blue/70 bg-[rgba(79,142,247,0.08)]",
      )}
    >
      {children}
    </div>
  );
}
