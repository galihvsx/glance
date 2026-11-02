// @vitest-environment jsdom
// C121T1: DisplayPanel gains one FIELD_ROW per new field, in Plane's
// panel order after the existing rows.
//
// Note on queries: each Switch sits inside its row <Label>, so the
// computed accessible name concatenates the visible label and the
// switch's aria-label ("IDShow id"). Tests therefore select switches
// by their aria-label attribute — the value the component sets —
// rather than by computed accessible name.

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import DisplayPanel from "./DisplayPanel";
import {
  DEFAULT_DISPLAY_SETTINGS,
  type DisplayFields,
  type DisplaySettings,
} from "./useDisplaySettings";

const NEW_ROWS: { label: string; key: keyof DisplayFields }[] = [
  { label: "ID", key: "id" },
  { label: "Sub-items", key: "subitems" },
  { label: "Attachments", key: "attachments" },
  { label: "Link", key: "links" },
  { label: "Dependencies", key: "dependencies" },
  { label: "Module", key: "modules" },
  { label: "Cycle", key: "cycle" },
  { label: "Releases", key: "release" },
];

/** The switch element for a row, selected by the aria-label the
 *  component sets (Base UI Switch renders role="switch"). */
function switchByLabel(label: string): HTMLElement {
  const el = document.querySelector(`[aria-label="${label}"]`);
  if (!el) throw new Error(`no switch with aria-label ${label}`);
  return el as HTMLElement;
}

function allSwitchLabels(): string[] {
  return Array.from(
    document.querySelectorAll("[data-slot='switch']"),
  ).map((el) => el.getAttribute("aria-label") ?? "");
}

function renderPanel(
  settings: DisplaySettings = DEFAULT_DISPLAY_SETTINGS,
  view: "list" | "board" = "list",
  onUpdateFields = vi.fn(),
) {
  render(
    <DisplayPanel
      settings={settings}
      onUpdate={vi.fn()}
      onUpdateFields={onUpdateFields}
      onReset={vi.fn()}
      view={view}
    />,
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Display properties" }),
  );
  return { onUpdateFields };
}

describe("DisplayPanel (C121T1)", () => {
  it("renders the 8 new rows after the existing rows, all switched on by default", () => {
    renderPanel();
    // Existing rows first, then Plane's panel order for the new rows,
    // then the Show-empty-groups switch.
    expect(allSwitchLabels()).toEqual([
      "Show state",
      "Show priority",
      "Show labels",
      "Show assignees",
      "Show dates",
      "Show id",
      "Show sub-items",
      "Show attachments",
      "Show link",
      "Show dependencies",
      "Show module",
      "Show cycle",
      "Show releases",
      "Show empty groups",
    ]);
    for (const row of NEW_ROWS) {
      expect(screen.getByText(row.label)).toBeInTheDocument();
      // Base UI Switch marks state with data-checked / data-unchecked.
      expect(
        switchByLabel(`Show ${row.label.toLowerCase()}`),
      ).toHaveAttribute("data-checked");
    }
  });

  it("toggles a new field off via its switch", () => {
    const { onUpdateFields } = renderPanel();
    fireEvent.click(switchByLabel("Show attachments"));
    expect(onUpdateFields).toHaveBeenCalledWith({ attachments: false });
  });

  it("reflects a switched-off new field", () => {
    renderPanel({
      ...DEFAULT_DISPLAY_SETTINGS,
      fields: { ...DEFAULT_DISPLAY_SETTINGS.fields, cycle: false },
    });
    expect(switchByLabel("Show cycle")).toHaveAttribute("data-unchecked");
  });

  it("board view keeps all 8 new rows (only State is board-hidden)", () => {
    renderPanel(DEFAULT_DISPLAY_SETTINGS, "board");
    expect(allSwitchLabels()).not.toContain("Show state");
    for (const row of NEW_ROWS) {
      expect(
        switchByLabel(`Show ${row.label.toLowerCase()}`),
      ).toBeInTheDocument();
    }
  });
});
