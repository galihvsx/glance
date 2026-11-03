import type { ReactNode } from "react";
import {
  SortableContext,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable";

/**
 * C127T0: one sortable context per list-view row stack (flat list,
 * per-state group, or per-sub-section list). The dnd-kit DndContext
 * lives in ListDragRoot — one for the whole list, because C126's
 * per-stack contexts could not receive cross-group header drops —
 * so this only scopes the sortable items to the stack. Items are
 * stack-scoped ids from listHeaderDrop.listSortableId, so a row that
 * multi-appears across epic sections never collides with itself.
 *
 * When `draggable` is false this renders only its children (a bare
 * fragment adds no DOM), so disabled stacks are byte-identical to
 * today's rendering.
 */
export default function ListRowStack({
  items,
  draggable,
  children,
}: {
  /** The stack's sortable ids, in display order. */
  items: string[];
  draggable: boolean;
  children: ReactNode;
}) {
  if (!draggable) return <>{children}</>;

  return (
    <SortableContext items={items} strategy={verticalListSortingStrategy}>
      {children}
    </SortableContext>
  );
}
