// @vitest-environment jsdom
// C26T3: day-cell quick-add — a subtle "+" affordance on month and week
// day cells that opens the create-issue dialog with the day prefilled.
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { DndContext } from "@dnd-kit/core";
import { describe, expect, it, vi } from "vitest";
import { DroppableDayCell } from "./DroppableDayCell";
import { DroppableWeekColumn } from "./DroppableWeekColumn";
import { formatDropDay } from "../../lib/calendar-dnd";

const DAY = "2026-10-12";

function renderDayCell(onQuickAdd?: (dayKey: string) => void) {
  return render(
    <DndContext>
      <DroppableDayCell dayKey={DAY} isOtherMonth={false} onQuickAdd={onQuickAdd}>
        <div>12</div>
      </DroppableDayCell>
    </DndContext>,
  );
}

function renderWeekColumn(onQuickAdd?: (dayKey: string) => void) {
  return render(
    <DndContext>
      <DroppableWeekColumn dayKey={DAY} isToday={false} onQuickAdd={onQuickAdd}>
        <div>chip</div>
      </DroppableWeekColumn>
    </DndContext>,
  );
}

describe("DroppableDayCell quick-add", () => {
  it("renders a + affordance in the cell corner when onQuickAdd is provided", () => {
    renderDayCell(() => undefined);
    const btn = screen.getByRole("button", { name: `Add issue on ${formatDropDay(DAY)}` });
    expect(btn).toBeInTheDocument();
    // Cell chrome: absolutely positioned in the corner, not in the flow
    // over the chips.
    expect(btn.className).toContain("absolute");
  });

  it("hides the affordance entirely when onQuickAdd is omitted (guests)", () => {
    renderDayCell();
    expect(
      screen.queryByRole("button", { name: `Add issue on ${formatDropDay(DAY)}` }),
    ).not.toBeInTheDocument();
  });

  it("clicking the + calls onQuickAdd with the cell's day key", async () => {
    const onQuickAdd = vi.fn();
    const user = userEvent.setup();
    renderDayCell(onQuickAdd);
    await user.click(screen.getByRole("button", { name: `Add issue on ${formatDropDay(DAY)}` }));
    expect(onQuickAdd).toHaveBeenCalledTimes(1);
    expect(onQuickAdd).toHaveBeenCalledWith(DAY);
  });

  it("Enter on the focused + triggers onQuickAdd (keyboard)", async () => {
    const onQuickAdd = vi.fn();
    const user = userEvent.setup();
    renderDayCell(onQuickAdd);
    const btn = screen.getByRole("button", { name: `Add issue on ${formatDropDay(DAY)}` });
    btn.focus();
    await user.keyboard("{Enter}");
    expect(onQuickAdd).toHaveBeenCalledWith(DAY);
  });

  it("the affordance carries no draggable wiring (cannot hijack chip drags)", () => {
    renderDayCell(() => undefined);
    const btn = screen.getByRole("button", { name: `Add issue on ${formatDropDay(DAY)}` });
    expect(btn).not.toHaveAttribute("aria-roledescription");
    expect(btn.className).not.toContain("cursor-grab");
    // It sits in the cell chrome, not inside a draggable chip.
    expect(
      btn.closest('[aria-roledescription="draggable"]'),
    ).not.toBeInTheDocument();
  });
});

describe("DroppableWeekColumn quick-add", () => {
  it("renders a + affordance in the column corner when onQuickAdd is provided", () => {
    renderWeekColumn(() => undefined);
    const btn = screen.getByRole("button", { name: `Add issue on ${formatDropDay(DAY)}` });
    expect(btn).toBeInTheDocument();
    expect(btn.className).toContain("absolute");
  });

  it("hides the affordance entirely when onQuickAdd is omitted (guests)", () => {
    renderWeekColumn();
    expect(
      screen.queryByRole("button", { name: `Add issue on ${formatDropDay(DAY)}` }),
    ).not.toBeInTheDocument();
  });

  it("clicking the + calls onQuickAdd with the column's day key", async () => {
    const onQuickAdd = vi.fn();
    const user = userEvent.setup();
    renderWeekColumn(onQuickAdd);
    await user.click(screen.getByRole("button", { name: `Add issue on ${formatDropDay(DAY)}` }));
    expect(onQuickAdd).toHaveBeenCalledWith(DAY);
  });

  it("Enter on the focused + triggers onQuickAdd (keyboard)", async () => {
    const onQuickAdd = vi.fn();
    const user = userEvent.setup();
    renderWeekColumn(onQuickAdd);
    const btn = screen.getByRole("button", { name: `Add issue on ${formatDropDay(DAY)}` });
    btn.focus();
    await user.keyboard("{Enter}");
    expect(onQuickAdd).toHaveBeenCalledWith(DAY);
  });
});

describe("quick-add aria-label localization (C27T3 nit 2)", () => {
  it("announces a localized human date, not the raw day key", () => {
    renderDayCell(() => undefined);
    const human = formatDropDay(DAY); // e.g. "Oct 12, 2026"
    const btn = screen.getByRole("button", { name: `Add issue on ${human}` });
    expect(btn).toHaveAccessibleName(`Add issue on ${human}`);
    // The raw ISO key must not leak into the accessible name.
    expect(btn.getAttribute("aria-label")).not.toBe(`Add issue on ${DAY}`);
    // The tooltip matches the accessible name.
    expect(btn.getAttribute("title")).toBe(`Add issue on ${human}`);
  });

  it("announces the localized date in the week column too", () => {
    renderWeekColumn(() => undefined);
    const btn = screen.getByRole("button", {
      name: `Add issue on ${formatDropDay(DAY)}`,
    });
    expect(btn.getAttribute("aria-label")).toBe(
      `Add issue on ${formatDropDay(DAY)}`,
    );
  });
});
