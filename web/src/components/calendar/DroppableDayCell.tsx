import { useDroppable } from "@dnd-kit/core";
import type { ReactNode } from "react";
import { cn } from "../../lib/utils";

/**
 * A month-grid day cell that accepts dragged issue chips (C26T0). Keeps the
 * cell's existing layout classes; the `isOver` highlight speaks the board
 * column's drop language (`border-accent-blue/70`) with a matching wash.
 */
export function DroppableDayCell({
  dayKey,
  isOtherMonth,
  children,
}: {
  dayKey: string;
  isOtherMonth: boolean;
  children: ReactNode;
}) {
  const { setNodeRef, isOver } = useDroppable({ id: dayKey });
  return (
    <div
      ref={setNodeRef}
      className={cn(
        "min-h-24 border-b border-border-hairline p-1 [&:not(:nth-child(7n))]:border-r [&:not(:nth-child(7n))]:border-r-border-hairline",
        isOtherMonth && "opacity-40",
        isOver && "border-accent-blue/70 bg-[rgba(79,142,247,0.08)]",
      )}
    >
      {children}
    </div>
  );
}
