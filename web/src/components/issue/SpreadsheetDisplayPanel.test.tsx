// @vitest-environment jsdom
// C147T3: spreadsheet grouping controls — the display panel gains
// "Group by" / "Sub-group by" selects following the DisplayPanel
// precedent (same option order; sub-group control disabled entirely
// when groupBy is "none"; the same-dimension sub-group option
// disabled). Selections persist through useSpreadsheetGrouping's own
// localStorage key and re-render live — no reload, no refetch.
//
// Corrupt-value fallback is NOT re-tested here: T1's
// SpreadsheetGroupBy.test.tsx already covers it at hook level
// ("falls back to none/none on corrupt or unknown values") and at
// grid level ("corrupt stored grouping falls back to the flat grid").
import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import SpreadsheetDisplayPanel from "./SpreadsheetDisplayPanel";
import { useSpreadsheetGrouping } from "./useSpreadsheetGrouping";
import type { GroupBy, SubGroupBy } from "./useDisplaySettings";

const STORAGE_KEY = "glance:spreadsheet-grouping:acme:ENG";

const GROUP_LABELS = [
  "State",
  "Priority",
  "Epic",
  "Assignees",
  "Labels",
  "Modules",
  "Cycles",
  "Releases",
  "Created by",
  "State groups",
  "None",
];

const SUB_GROUP_LABELS = [
  "None",
  "State",
  "Priority",
  "Epic",
  "Assignees",
  "Labels",
  "Created by",
  "State groups",
  "Cycles",
  "Modules",
  "Releases",
];

function renderPanel(
  over: {
    groupBy?: GroupBy;
    subGroupBy?: SubGroupBy;
    onGroupByChange?: (next: GroupBy) => void;
    onSubGroupByChange?: (next: SubGroupBy) => void;
    onResetGrouping?: () => void;
    onReset?: () => void;
    onResetShowSubWorkItems?: () => void;
  } = {},
) {
  const handlers = {
    onGroupByChange: vi.fn(),
    onSubGroupByChange: vi.fn(),
    onResetGrouping: vi.fn(),
    onReset: vi.fn(),
    onResetShowSubWorkItems: vi.fn(),
    ...over,
  };
  render(
    <SpreadsheetDisplayPanel
      fields={[]}
      isLoading={false}
      isError={false}
      onRetry={vi.fn()}
      visible={{}}
      onToggle={vi.fn()}
      onReset={handlers.onReset}
      showSubWorkItems
      onToggleShowSubWorkItems={vi.fn()}
      onResetShowSubWorkItems={handlers.onResetShowSubWorkItems}
      groupBy={over.groupBy ?? "none"}
      subGroupBy={over.subGroupBy ?? "none"}
      onGroupByChange={handlers.onGroupByChange}
      onSubGroupByChange={handlers.onSubGroupByChange}
      onResetGrouping={handlers.onResetGrouping}
    />,
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Display properties" }),
  );
  return handlers;
}

function optionLabels(): string[] {
  return Array.from(document.querySelectorAll('[role="option"]')).map(
    (el) => (el.textContent ?? "").trim(),
  );
}

function optionByName(name: string): HTMLElement {
  const el = Array.from(document.querySelectorAll('[role="option"]')).find(
    (o) => (o.textContent ?? "").trim() === name,
  );
  if (!el) throw new Error(`no option named ${name}`);
  return el as HTMLElement;
}

/** Base-UI marks a disabled item with data-disabled / aria-disabled. */
function isDisabledOption(el: HTMLElement): boolean {
  return (
    el.hasAttribute("data-disabled") ||
    el.getAttribute("aria-disabled") === "true"
  );
}

beforeEach(() => {
  localStorage.clear();
  vi.clearAllMocks();
});

describe("SpreadsheetDisplayPanel grouping controls (C147T3)", () => {
  it("group select lists all dimensions + None in DisplayPanel order", async () => {
    renderPanel();
    fireEvent.click(
      screen.getByRole("combobox", { name: "Group issues by" }),
    );
    expect(
      await screen.findByRole("option", { name: "State groups" }),
    ).toBeInTheDocument();
    expect(optionLabels()).toEqual(GROUP_LABELS);
  });

  it("sub-group select lists None first, then all dimensions", async () => {
    renderPanel({ groupBy: "state" });
    fireEvent.click(
      screen.getByRole("combobox", { name: "Sub-group issues by" }),
    );
    expect(
      await screen.findByRole("option", { name: "State groups" }),
    ).toBeInTheDocument();
    expect(optionLabels()).toEqual(SUB_GROUP_LABELS);
  });

  it("sub-group select is disabled entirely when group is None", () => {
    renderPanel({ groupBy: "none" });
    expect(
      screen.getByRole("combobox", { name: "Sub-group issues by" }),
    ).toBeDisabled();
    expect(screen.getByText("Choose a Group by first")).toBeInTheDocument();
  });

  it("same-dimension sub-group option is disabled, not hidden", async () => {
    renderPanel({ groupBy: "state" });
    fireEvent.click(
      screen.getByRole("combobox", { name: "Sub-group issues by" }),
    );
    await screen.findByRole("option", { name: "State" });
    expect(isDisabledOption(optionByName("State"))).toBe(true);
    // The other dimensions stay enabled.
    expect(isDisabledOption(optionByName("Priority"))).toBe(false);
    expect(isDisabledOption(optionByName("None"))).toBe(false);
  });

  it("selecting a group reports the value", async () => {
    const user = userEvent.setup();
    const { onGroupByChange } = renderPanel();
    await user.click(
      screen.getByRole("combobox", { name: "Group issues by" }),
    );
    await user.click(await screen.findByRole("option", { name: "State" }));
    expect(onGroupByChange).toHaveBeenCalledWith("state");
  });

  it("selecting a sub-group reports the value", async () => {
    const user = userEvent.setup();
    const { onSubGroupByChange } = renderPanel({ groupBy: "state" });
    await user.click(
      screen.getByRole("combobox", { name: "Sub-group issues by" }),
    );
    await user.click(
      await screen.findByRole("option", { name: "Priority" }),
    );
    expect(onSubGroupByChange).toHaveBeenCalledWith("priority");
  });

  it("existing sections stay in order, grouping section first", () => {
    renderPanel();
    const text = document.body.textContent ?? "";
    expect(text.indexOf("Grouping")).toBeGreaterThan(-1);
    expect(text.indexOf("Grouping")).toBeLessThan(
      text.indexOf("Sub-work items"),
    );
    expect(text.indexOf("Sub-work items")).toBeLessThan(
      text.indexOf("Custom field columns"),
    );
    // Switch sits inside its row <Label>, so the computed accessible
    // name concatenates label + aria-label (DisplayPanel.test.tsx
    // precedent) — select by the aria-label attribute instead.
    expect(
      document.querySelector('[aria-label="Show sub-work items"]'),
    ).not.toBeNull();
  });

  it("Reset to defaults also resets grouping", async () => {
    const user = userEvent.setup();
    const { onResetGrouping, onReset, onResetShowSubWorkItems } =
      renderPanel({ groupBy: "state", subGroupBy: "priority" });
    await user.click(
      screen.getByRole("button", { name: "Reset to defaults" }),
    );
    expect(onResetGrouping).toHaveBeenCalled();
    expect(onReset).toHaveBeenCalled();
    expect(onResetShowSubWorkItems).toHaveBeenCalled();
  });
});

describe("SpreadsheetDisplayPanel wired to useSpreadsheetGrouping", () => {
  function WiredPanel() {
    const { groupBy, subGroupBy, setGroupBy, setSubGroupBy, reset } =
      useSpreadsheetGrouping("acme", "ENG");
    return (
      <SpreadsheetDisplayPanel
        fields={[]}
        isLoading={false}
        isError={false}
        onRetry={vi.fn()}
        visible={{}}
        onToggle={vi.fn()}
        onReset={vi.fn()}
        showSubWorkItems
        onToggleShowSubWorkItems={vi.fn()}
        onResetShowSubWorkItems={vi.fn()}
        groupBy={groupBy}
        subGroupBy={subGroupBy}
        onGroupByChange={setGroupBy}
        onSubGroupByChange={setSubGroupBy}
        onResetGrouping={reset}
      />
    );
  }

  it("a selection persists to localStorage and re-renders live, with no fetch involved", async () => {
    const user = userEvent.setup();
    render(<WiredPanel />);
    fireEvent.click(
      screen.getByRole("button", { name: "Display properties" }),
    );
    await user.click(
      screen.getByRole("combobox", { name: "Group issues by" }),
    );
    await user.click(await screen.findByRole("option", { name: "State" }));
    expect(JSON.parse(localStorage.getItem(STORAGE_KEY) ?? "{}")).toEqual({
      groupBy: "state",
      subGroupBy: "none",
    });
    // The panel shows the live value (no reload, no refetch — this
    // harness performs no fetch at all). Whether the popover stayed
    // open across the Select choice is Base-UI behaviour, not the
    // panel's: open it only if the choice closed it.
    if (
      !screen.queryByRole("combobox", { name: "Group issues by" })
    ) {
      fireEvent.click(
        screen.getByRole("button", { name: "Display properties" }),
      );
    }
    // Live value, not a stale default: in jsdom Base-UI's Value
    // resolves the label from mounted items, so after the popup
    // closes it falls back to the raw value — the shared Select
    // component's behaviour (same as DisplayPanel's Order-by), not
    // this panel's. The label rendering in a real browser comes
    // from the mounted SelectItem, as everywhere else.
    expect(
      screen.getByRole("combobox", { name: "Group issues by" }),
    ).toHaveTextContent("state");
    // Sub-group is now enabled; selecting one persists alongside.
    await user.click(
      screen.getByRole("combobox", { name: "Sub-group issues by" }),
    );
    await user.click(
      await screen.findByRole("option", { name: "Priority" }),
    );
    expect(JSON.parse(localStorage.getItem(STORAGE_KEY) ?? "{}")).toEqual({
      groupBy: "state",
      subGroupBy: "priority",
    });
  });
});
