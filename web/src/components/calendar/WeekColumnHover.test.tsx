// @vitest-environment jsdom
// C27T3 nit 1: the week-view day-column drag-over highlight must carry a
// real border-width — `border-accent-blue/70` alone renders nothing without
// one. The month cell's language is a blue bottom border on hover; the
// week column should match it.
import { render, screen } from "@testing-library/react";
import { DndContext } from "@dnd-kit/core";
import { describe, expect, it, vi } from "vitest";
import { DroppableWeekColumn } from "./DroppableWeekColumn";

let droppableOver = false;

vi.mock("@dnd-kit/core", async (importOriginal) => {
  const mod = await importOriginal<typeof import("@dnd-kit/core")>();
  return {
    ...mod,
    useDroppable: () => ({ setNodeRef: () => {}, isOver: droppableOver }),
  };
});

const DAY = "2026-10-12";

function renderColumn() {
  return render(
    <DndContext>
      <DroppableWeekColumn dayKey={DAY} isToday={false}>
        <div>chip</div>
      </DroppableWeekColumn>
    </DndContext>,
  );
}

describe("DroppableWeekColumn hover border (C27T3 nit 1)", () => {
  it("carries a border-width so the blue drag-over border renders", () => {
    droppableOver = true;
    renderColumn();
    const col = screen.getByTestId(`week-column-${DAY}`);
    // A border-width slot must exist for `border-accent-blue/70` to tint —
    // without one the highlight is a wash with no visible border.
    expect(col.className).toContain("border-b");
    expect(col.className).toContain("border-accent-blue/70");
  });

  it("shows no border color at rest (transparent placeholder)", () => {
    droppableOver = false;
    renderColumn();
    const col = screen.getByTestId(`week-column-${DAY}`);
    // The placeholder width must be invisible until the hover recolors it.
    expect(col.className).toContain("border-transparent");
  });
});
