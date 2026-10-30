// @vitest-environment jsdom
// C84T1: WidgetCard phase-3 renderers — work_items_table and
// assigned_to_me (configured columns, sort-to-config, pagination, row
// navigation), the drill-down modal on every group-based widget
// (smart_counter tiles, table_2d rows, smart_gauge arcs), no drill-down
// on number widgets, CSV export for the new types, and view-only
// read affordances (drill + export, no sort PATCH).
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import WidgetCard from "./WidgetCard";
import type {
  DashboardWidget,
  WidgetData,
  WidgetDrilldown,
  WidgetRowItem,
  WidgetType,
} from "../../lib/dashboards";

const mockFetchWidgetData = vi.hoisted(
  () => vi.fn<(id: string, page?: number) => Promise<WidgetData>>(),
);
const mockFetchDrilldown = vi.hoisted(
  () => vi.fn<(id: string, group: string, page?: number) => Promise<WidgetDrilldown>>(),
);
const mockDownloadCsv = vi.hoisted(() => vi.fn());
const mockUpdateWidget = vi.hoisted(() => vi.fn());

vi.mock("../../lib/dashboards", async (importOriginal) => {
  const mod =
    await importOriginal<typeof import("../../lib/dashboards")>();
  return {
    ...mod,
    fetchWidgetData: mockFetchWidgetData,
    fetchWidgetDrilldown: mockFetchDrilldown,
    downloadWidgetCsv: mockDownloadCsv,
    updateWidget: mockUpdateWidget,
  };
});

afterEach(() => {
  cleanup();
  mockFetchWidgetData.mockReset();
  mockFetchDrilldown.mockReset();
  mockDownloadCsv.mockReset();
  mockUpdateWidget.mockReset();
});

function row(
  id: string,
  title: string,
  overrides: Partial<WidgetRowItem> = {},
): WidgetRowItem {
  return {
    id,
    title,
    state: "In progress",
    state_group: "started",
    priority: 3,
    assignee: { id: "u1", name: "Galih" },
    labels: [{ id: "l1", name: "payments", color: "#000" }],
    project: { id: "p1", name: "Acme" },
    ...overrides,
  };
}

function widget(
  type: WidgetType,
  config: DashboardWidget["config"] = {},
  title = "Phase 3 widget",
): DashboardWidget {
  return {
    id: "w1",
    dashboard_id: "d1",
    title,
    widget_type: type,
    config,
    grid_x: 0,
    grid_y: 0,
    grid_w: 6,
    grid_h: 4,
    created_at: "",
    updated_at: "",
  };
}

function renderCard(
  w: DashboardWidget,
  opts: { canEdit?: boolean; onIssueClick?: (r: WidgetRowItem) => void } = {},
) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const onIssueClick = opts.onIssueClick ?? vi.fn();
  render(
    <QueryClientProvider client={qc}>
      <WidgetCard
        widget={w}
        canEdit={opts.canEdit ?? true}
        onEdit={vi.fn()}
        onDelete={vi.fn()}
        onIssueClick={onIssueClick}
      />
    </QueryClientProvider>,
  );
  return { onIssueClick: onIssueClick as ReturnType<typeof vi.fn> };
}

const drilldown = (
  overrides: Partial<WidgetDrilldown> = {},
): WidgetDrilldown => ({
  items: [row("i1", "Checkout crashes"), row("i2", "Refund retries")],
  page: 1,
  per_page: 10,
  total: 2,
  group_label: "Triage",
  group_by: "state",
  ...overrides,
});

describe("WidgetCard row-list widgets", () => {
  it("renders only the configured columns, in config order", async () => {
    mockFetchWidgetData.mockResolvedValue({
      rows: [row("i1", "Checkout crashes")],
      page: 1,
      max_rows: 10,
      total: 1,
    });
    renderCard(
      widget("work_items_table", {
        columns: ["title", "priority", "state"],
        sort_by: "state",
        sort_dir: "desc",
        max_rows: 10,
      }),
    );
    await screen.findByText("Checkout crashes");
    const table = screen.getByText("Checkout crashes").closest("table")!;
    const headers = Array.from(table.querySelectorAll("th")).map((th) =>
      th.textContent?.replace(/[▲▼]/g, "").trim(),
    );
    expect(headers).toEqual(["Title", "Priority", "State"]);
    expect(screen.queryByText("Acme")).not.toBeInTheDocument();
  });

  it("shows the Assigned to you badge on assigned_to_me cards", async () => {
    mockFetchWidgetData.mockResolvedValue({
      rows: [row("i1", "My item")],
      page: 1,
      max_rows: 10,
      total: 1,
    });
    renderCard(widget("assigned_to_me", {}, "My queue"));
    await screen.findByText("My item");
    expect(screen.getByText("Assigned to you")).toBeInTheDocument();
  });

  it("sort header click PATCHes the widget config (sort is config)", async () => {
    mockFetchWidgetData.mockResolvedValue({
      rows: [row("i1", "A item")],
      page: 1,
      max_rows: 10,
      total: 1,
    });
    mockUpdateWidget.mockResolvedValue({});
    renderCard(
      widget("work_items_table", {
        columns: ["id", "title", "priority"],
        sort_by: "title",
        sort_dir: "desc",
        max_rows: 10,
      }),
    );
    await screen.findByText("A item");
    fireEvent.click(screen.getByRole("button", { name: "Sort by Priority" }));
    await waitFor(() =>
      expect(mockUpdateWidget).toHaveBeenCalledWith("w1", {
        config: expect.objectContaining({
          sort_by: "priority",
          sort_dir: "desc",
          columns: ["id", "title", "priority"],
        }),
      }),
    );
  });

  it("clicking the active sort column flips its direction", async () => {
    mockFetchWidgetData.mockResolvedValue({
      rows: [row("i1", "A item")],
      page: 1,
      max_rows: 10,
      total: 1,
    });
    mockUpdateWidget.mockResolvedValue({});
    renderCard(
      widget("work_items_table", {
        columns: ["id", "title"],
        sort_by: "title",
        sort_dir: "desc",
        max_rows: 10,
      }),
    );
    await screen.findByText("A item");
    fireEvent.click(screen.getByRole("button", { name: "Sort by Title" }));
    await waitFor(() =>
      expect(mockUpdateWidget).toHaveBeenCalledWith("w1", {
        config: expect.objectContaining({ sort_by: "title", sort_dir: "asc" }),
      }),
    );
  });

  it("paginates the row list and refetches the next page", async () => {
    mockFetchWidgetData.mockImplementation((_id, page = 1) =>
      Promise.resolve({
        rows: [row(`i${page}`, `Item page ${page}`)],
        page,
        max_rows: 10,
        total: 25,
      }),
    );
    renderCard(widget("work_items_table", { max_rows: 10 }));
    await screen.findByText("Item page 1");
    expect(screen.getByText("Page 1 of 3")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Next page" }));
    await screen.findByText("Item page 2");
    expect(mockFetchWidgetData).toHaveBeenCalledWith("w1", 2);
  });

  it("row click navigates via onIssueClick", async () => {
    mockFetchWidgetData.mockResolvedValue({
      rows: [row("i1", "Clickable item")],
      page: 1,
      max_rows: 10,
      total: 1,
    });
    const { onIssueClick } = renderCard(widget("work_items_table", {}));
    await screen.findByText("Clickable item");
    fireEvent.click(screen.getByText("Clickable item"));
    expect(onIssueClick).toHaveBeenCalledWith(
      expect.objectContaining({ id: "i1", title: "Clickable item" }),
    );
  });

  it("view-only readers get plain sort headers (no PATCH affordance)", async () => {
    mockFetchWidgetData.mockResolvedValue({
      rows: [row("i1", "Read-only item")],
      page: 1,
      max_rows: 10,
      total: 1,
    });
    renderCard(widget("work_items_table", {}), { canEdit: false });
    await screen.findByText("Read-only item");
    expect(
      screen.queryByRole("button", { name: /Sort by/ }),
    ).not.toBeInTheDocument();
  });

  it("exports CSV from the widget menu for the new types", async () => {
    mockFetchWidgetData.mockResolvedValue({
      rows: [row("i1", "Exportable item")],
      page: 1,
      max_rows: 10,
      total: 1,
    });
    mockDownloadCsv.mockResolvedValue(undefined);
    const user = userEvent.setup();
    renderCard(widget("work_items_table", {}), { canEdit: false });
    await screen.findByText("Exportable item");
    await user.click(
      screen.getByLabelText("Widget options for Phase 3 widget"),
    );
    await user.click(
      await screen.findByRole("menuitem", { name: /Export CSV/ }),
    );
    expect(mockDownloadCsv).toHaveBeenCalledWith("w1");
  });
});

describe("WidgetCard drill-down modal", () => {
  it("smart_counter tile click opens the modal with group label + items", async () => {
    mockFetchWidgetData.mockResolvedValue({
      groups: [{ key: "Triage", label: "Triage", value: 34 }],
      page: 1,
      max_rows: 10,
      total_groups: 1,
    });
    mockFetchDrilldown.mockResolvedValue(drilldown());
    renderCard(widget("smart_counter", { group_by: "state" }, "Open by state"));
    await screen.findByText("Triage");
    fireEvent.click(screen.getByText("Triage"));
    expect(
      await screen.findByText("Open by state — Triage"),
    ).toBeInTheDocument();
    // The title renders from the click prop before the drill-down
    // fetch lands — await the items, don't race them.
    expect(await screen.findByText("Checkout crashes")).toBeInTheDocument();
    expect(screen.getByText("2 work items · dashboard filters apply")).toBeInTheDocument();
    expect(mockFetchDrilldown).toHaveBeenCalledWith("w1", "Triage", 1);
  });

  it("table_2d row click opens the drill-down modal", async () => {
    mockFetchWidgetData.mockResolvedValue({
      groups: [{ key: "open", label: "Open", value: 5 }],
      page: 1,
      max_rows: 10,
      total_groups: 1,
    });
    mockFetchDrilldown.mockResolvedValue(
      drilldown({ group_label: "Open", group_by: "state_group" }),
    );
    renderCard(widget("table_2d", { group_by: "state_group" }, "By group"));
    await screen.findByText("Open");
    fireEvent.click(screen.getByText("Open"));
    expect(await screen.findByText("By group — Open")).toBeInTheDocument();
  });

  it("smart_gauge arc click opens the drill-down modal", async () => {
    mockFetchWidgetData.mockResolvedValue({
      groups: [
        { key: "done", label: "Done", value: 7, completed: 7, percentage: 100 },
      ],
      page: 1,
      max_rows: 10,
      total_groups: 1,
    });
    mockFetchDrilldown.mockResolvedValue(
      drilldown({ group_label: "Done", total: 7 }),
    );
    renderCard(widget("smart_gauge", { group_by: "state" }, "Progress"));
    await screen.findByText("Done");
    fireEvent.click(screen.getAllByTitle("Click to drill down")[0]);
    expect(await screen.findByText("Progress — Done")).toBeInTheDocument();
  });

  it("drill-down modal paginates and row click navigates, close returns", async () => {
    mockFetchWidgetData.mockResolvedValue({
      groups: [{ key: "Triage", label: "Triage", value: 34 }],
      page: 1,
      max_rows: 10,
      total_groups: 1,
    });
    mockFetchDrilldown.mockImplementation((_id, _g, page = 1) =>
      Promise.resolve(
        drilldown({
          items: [row(`i${page}`, `Drill item ${page}`)],
          page,
          total: 34,
        }),
      ),
    );
    const { onIssueClick } = renderCard(
      widget("smart_counter", { group_by: "state" }, "Open by state"),
    );
    await screen.findByText("Triage");
    fireEvent.click(screen.getByText("Triage"));
    await screen.findByText("Drill item 1");
    const modal = screen
      .getByText("Open by state — Triage")
      .closest("div")!.parentElement!;
    fireEvent.click(within(modal).getByRole("button", { name: "Next page" }));
    await screen.findByText("Drill item 2");
    expect(mockFetchDrilldown).toHaveBeenCalledWith("w1", "Triage", 2);
    fireEvent.click(screen.getByText("Drill item 2"));
    expect(onIssueClick).toHaveBeenCalledWith(
      expect.objectContaining({ id: "i2" }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(
      screen.queryByText("Open by state — Triage"),
    ).not.toBeInTheDocument();
  });

  it("drill-down modal Export CSV downloads the widget CSV", async () => {
    mockFetchWidgetData.mockResolvedValue({
      groups: [{ key: "Triage", label: "Triage", value: 34 }],
      page: 1,
      max_rows: 10,
      total_groups: 1,
    });
    mockFetchDrilldown.mockResolvedValue(drilldown());
    mockDownloadCsv.mockResolvedValue(undefined);
    renderCard(widget("smart_counter", { group_by: "state" }, "Open by state"));
    await screen.findByText("Triage");
    fireEvent.click(screen.getByText("Triage"));
    await screen.findByText("Checkout crashes");
    fireEvent.click(screen.getByRole("button", { name: /Export CSV/ }));
    expect(mockDownloadCsv).toHaveBeenCalledWith("w1");
  });

  it("number widgets have no drill-down affordance", async () => {
    mockFetchWidgetData.mockResolvedValue({ value: 42 });
    renderCard(widget("number", {}));
    await screen.findByText("42");
    expect(mockFetchDrilldown).not.toHaveBeenCalled();
    expect(screen.queryByText(/—/)).not.toBeInTheDocument();
  });
});
