import { useDroppable } from "@dnd-kit/core";
import type { ReactNode } from "react";

/**
 * C127T0: a list group/sub-group header as a drop target. The droppable
 * ref + highlight live on the header's own div — no wrapper element —
 * so when no drag is over it, the header renders exactly as before
 * (the idle border token is the same hairline the plain header uses).
 * The highlight reuses the board column's isOver token
 * (border-accent-blue/70). The page renders this only when reassign
 * drag is enabled and the header names an assignable target; it
 * renders a plain div otherwise (flat layout, PQL mode, the
 * state-Ungrouped sub-header), so those headers stay byte-identical.
 */
export default function ListHeaderDropZone({
  droppableId,
  className,
  children,
}: {
  droppableId: string;
  /** The header's classes WITHOUT the border-color token (the zone
   *  appends the idle token, or the highlight token when dragged over). */
  className: string;
  children: ReactNode;
}) {
  const { setNodeRef, isOver } = useDroppable({ id: droppableId });
  return (
    <div
      ref={setNodeRef}
      className={`${className} ${
        isOver ? "border-accent-blue/70" : "border-border-hairline"
      }`}
    >
      {children}
    </div>
  );
}
