// @vitest-environment jsdom
import { render, screen } from "@testing-library/react";
import { DndContext } from "@dnd-kit/core";
import { describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { UnscheduledStrip } from "./UnscheduledStrip";
import type { Issue } from "../../lib/types";

function mkIssue(over: Partial<Issue> & { id: string }): Issue {
  return {
    project_id: "p",
    sequence_id: 7,
    display_id: "ENG-7",
    name: "Unscheduled work",
    priority: 0,
    state_id: "s",
    sort_order: 0,
    is_draft: false,
    created_by: "u",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    assignees: [],
    labels: [],
    start_date: null,
    target_date: null,
    ...over,
  };
}

const statesById = new Map();
const onOpen = vi.fn();

function renderStrip(ui: ReactNode) {
  return render(<DndContext>{ui}</DndContext>);
}

describe("UnscheduledStrip", () => {
  it("renders the drop zone for members even when empty", () => {
    const { container } = renderStrip(
      <UnscheduledStrip
        issues={[]}
        statesById={statesById}
        canEdit
        onOpen={onOpen}
      />,
    );
    expect(screen.getByText("Unscheduled (0)")).toBeTruthy();
    expect(
      container.querySelector('[data-droppable-id="unscheduled-strip"]'),
    ).not.toBeNull();
    expect(
      screen.getByText("Drag dated chips here to unschedule"),
    ).toBeTruthy();
  });

  it("renders member chips as draggables", () => {
    const { container } = renderStrip(
      <UnscheduledStrip
        issues={[mkIssue({ id: "i1" })]}
        statesById={statesById}
        canEdit
        onOpen={onOpen}
      />,
    );
    expect(screen.getByText("Unscheduled (1)")).toBeTruthy();
    expect(
      container.querySelector('[data-droppable-id="unscheduled-strip"]'),
    ).not.toBeNull();
    // Draggable chips carry the board's grab-cursor drag language.
    expect(container.querySelectorAll("button.cursor-grab")).toHaveLength(1);
  });

  it("renders plain chips with no drop affordance for guests", () => {
    const { container } = renderStrip(
      <UnscheduledStrip
        issues={[mkIssue({ id: "i1" })]}
        statesById={statesById}
        canEdit={false}
        onOpen={onOpen}
      />,
    );
    expect(screen.getByText("Unscheduled (1)")).toBeTruthy();
    expect(
      container.querySelector('[data-droppable-id="unscheduled-strip"]'),
    ).toBeNull();
    expect(container.querySelectorAll("button.cursor-grab")).toHaveLength(0);
    // The chip itself still renders (click-to-peek).
    expect(screen.getByText("Unscheduled work")).toBeTruthy();
  });

  it("renders nothing for guests when there are no unscheduled issues", () => {
    const { container } = renderStrip(
      <UnscheduledStrip
        issues={[]}
        statesById={statesById}
        canEdit={false}
        onOpen={onOpen}
      />,
    );
    expect(screen.queryByText(/Unscheduled/)).toBeNull();
    // DndContext renders its own a11y live regions; the component itself
    // adds no section, header, or drop affordance.
    expect(container.querySelector("section")).toBeNull();
    expect(
      container.querySelector('[data-droppable-id="unscheduled-strip"]'),
    ).toBeNull();
  });
});
