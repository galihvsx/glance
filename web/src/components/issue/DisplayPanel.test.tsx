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
import userEvent from "@testing-library/user-event";
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
      // C122T1: next to "Show empty groups" in the Group by section.
      "Show sub-work items",
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

describe("DisplayPanel show sub-work items (C122T1)", () => {
  function renderPanelWithUpdate(
    settings: DisplaySettings = DEFAULT_DISPLAY_SETTINGS,
  ) {
    const onUpdate = vi.fn();
    render(
      <DisplayPanel
        settings={settings}
        onUpdate={onUpdate}
        onUpdateFields={vi.fn()}
        onReset={vi.fn()}
        view="board"
      />,
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Display properties" }),
    );
    return { onUpdate };
  }

  it("renders the toggle on by default next to Show empty groups", () => {
    renderPanelWithUpdate();
    expect(screen.getByText("Show sub-work items")).toBeInTheDocument();
    expect(switchByLabel("Show sub-work items")).toHaveAttribute(
      "data-checked",
    );
  });

  it("toggles sub-work items off via its switch", () => {
    const { onUpdate } = renderPanelWithUpdate();
    fireEvent.click(switchByLabel("Show sub-work items"));
    expect(onUpdate).toHaveBeenCalledWith({ showSubWorkItems: false });
  });

  it("reflects a switched-off toggle", () => {
    renderPanelWithUpdate({
      ...DEFAULT_DISPLAY_SETTINGS,
      showSubWorkItems: false,
    });
    expect(switchByLabel("Show sub-work items")).toHaveAttribute(
      "data-unchecked",
    );
  });
});

describe("DisplayPanel sub-group by (C123T0)", () => {
  function renderPanelWithUpdate(
    settings: DisplaySettings = DEFAULT_DISPLAY_SETTINGS,
    view: "list" | "board" = "list",
  ) {
    const onUpdate = vi.fn();
    render(
      <DisplayPanel
        settings={settings}
        onUpdate={onUpdate}
        onUpdateFields={vi.fn()}
        onReset={vi.fn()}
        view={view}
      />,
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Display properties" }),
    );
    return { onUpdate };
  }

  function subGroupRadios() {
    const group = screen.getByRole("radiogroup", { name: "Sub-group by" });
    return {
      group,
      radio: (name: string) =>
        // Radio buttons are scoped to the sub-group radiogroup — the
        // Group by radiogroup has the same option labels.
        Array.from(group.querySelectorAll('[role="radio"]')).find(
          (el) => el.textContent === name,
        ) as HTMLElement,
    };
  }

  it("renders the Sub-group by row directly under Group by, None checked by default", () => {
    renderPanelWithUpdate();
    const { group, radio } = subGroupRadios();
    expect(group).toBeInTheDocument();
    for (const label of ["None", "State", "Priority", "Epic"]) {
      expect(radio(label)).toBeInTheDocument();
    }
    expect(radio("None")).toHaveAttribute("aria-checked", "true");
  });

  it("disables the active primary dimension's option (greyed out, not hidden)", () => {
    renderPanelWithUpdate({
      ...DEFAULT_DISPLAY_SETTINGS,
      groupBy: "state",
    });
    const { radio } = subGroupRadios();
    expect(radio("State")).toBeDisabled();
    expect(radio("Priority")).not.toBeDisabled();
    expect(radio("Epic")).not.toBeDisabled();
    expect(radio("None")).not.toBeDisabled();
  });

  it("disables the whole control with a hint when groupBy is none", () => {
    renderPanelWithUpdate({
      ...DEFAULT_DISPLAY_SETTINGS,
      groupBy: "none",
    });
    const { radio } = subGroupRadios();
    for (const label of ["None", "State", "Priority", "Epic"]) {
      expect(radio(label)).toBeDisabled();
    }
    expect(screen.getByText("Choose a Group by first")).toBeInTheDocument();
  });

  it("shows no hint when a Group by is chosen", () => {
    renderPanelWithUpdate();
    expect(
      screen.queryByText("Choose a Group by first"),
    ).not.toBeInTheDocument();
  });

  it("updates subGroupBy via its radios", () => {
    const { onUpdate } = renderPanelWithUpdate();
    fireEvent.click(subGroupRadios().radio("Priority"));
    expect(onUpdate).toHaveBeenCalledWith({ subGroupBy: "priority" });
  });

  it("board view keeps the control (groupBy none still disables it)", () => {
    renderPanelWithUpdate(
      { ...DEFAULT_DISPLAY_SETTINGS, groupBy: "none" },
      "board",
    );
    expect(
      screen.getByRole("radiogroup", { name: "Sub-group by" }),
    ).toBeInTheDocument();
    expect(screen.getByText("Choose a Group by first")).toBeInTheDocument();
  });
});

describe("DisplayPanel order by Manual rank (C125T0)", () => {
  function renderPanelWithUpdate(
    settings: DisplaySettings = DEFAULT_DISPLAY_SETTINGS,
    view: "list" | "board" = "list",
  ) {
    const onUpdate = vi.fn();
    render(
      <DisplayPanel
        settings={settings}
        onUpdate={onUpdate}
        onUpdateFields={vi.fn()}
        onReset={vi.fn()}
        view={view}
      />,
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Display properties" }),
    );
    return { onUpdate };
  }

  it("offers Manual rank as the last Order-by option in the list view", async () => {
    renderPanelWithUpdate();
    fireEvent.click(
      screen.getByRole("combobox", { name: "Order issues by" }),
    );
    const option = await screen.findByRole("option", {
      name: "Manual rank",
    });
    expect(option).toBeInTheDocument();
    // Last in the list, after the sequence entries.
    const options = Array.from(
      document.querySelectorAll('[role="option"]'),
    ).map((el) => el.textContent);
    expect(options[options.length - 1]).toBe("Manual rank");
    expect(options).toContain("Sequence: high to low");
  });

  it("selecting Manual rank reports orderBy sort_order", async () => {
    const user = userEvent.setup();
    const { onUpdate } = renderPanelWithUpdate();
    await user.click(
      screen.getByRole("combobox", { name: "Order issues by" }),
    );
    await user.click(
      await screen.findByRole("option", { name: "Manual rank" }),
    );
    expect(onUpdate).toHaveBeenCalledWith({ orderBy: "sort_order" });
  });

  it("board view shows no Order-by control", () => {
    renderPanelWithUpdate(DEFAULT_DISPLAY_SETTINGS, "board");
    expect(
      screen.queryByRole("combobox", { name: "Order issues by" }),
    ).toBeNull();
  });
});

describe("DisplayPanel Group by Assignees (C129T0)", () => {
  function renderGroupBy(settings: DisplaySettings = DEFAULT_DISPLAY_SETTINGS) {
    const onUpdate = vi.fn();
    render(
      <DisplayPanel
        settings={settings}
        onUpdate={onUpdate}
        onUpdateFields={vi.fn()}
        onReset={vi.fn()}
        view="list"
      />,
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Display properties" }),
    );
    const group = screen.getByRole("radiogroup", { name: "Group by" });
    const radios = Array.from(group.querySelectorAll('[role="radio"]'));
    return {
      onUpdate,
      labels: radios.map((el) => el.textContent),
      radio: (name: string) =>
        radios.find((el) => el.textContent === name) as HTMLElement,
    };
  }

  it("offers Assignees after Epic, before None — old options unreordered", () => {
    const { labels } = renderGroupBy();
    expect(labels).toEqual(["State", "Priority", "Epic", "Assignees", "None"]);
  });

  it("selecting Assignees reports groupBy assignees", () => {
    const { onUpdate, radio } = renderGroupBy();
    fireEvent.click(radio("Assignees"));
    expect(onUpdate).toHaveBeenCalledWith({ groupBy: "assignees" });
  });

  it("marks Assignees checked when it is the active grouping", () => {
    const { radio } = renderGroupBy({
      ...DEFAULT_DISPLAY_SETTINGS,
      groupBy: "assignees",
    });
    expect(radio("Assignees")).toHaveAttribute("aria-checked", "true");
  });
});
