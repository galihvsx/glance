// @vitest-environment jsdom
// C83T1: WidgetCard phase-2 renderers — smart_counter (grid/table +
// pagination), smart_gauge (SVG arc math, % toggle), table_2d
// (desc-sorted rows), intake_breakdown (six tiles + the honest email
// tooltip), and the per-widget Export CSV menu item.
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import WidgetCard from "./WidgetCard";
import { EMAIL_INGESTION_NOTE } from "../../lib/dashboards";
import type {
  DashboardWidget,
  WidgetData,
  WidgetType,
} from "../../lib/dashboards";

const mockFetchWidgetData = vi.hoisted(
  () => vi.fn<(id: string, page?: number) => Promise<WidgetData>>(),
);
const mockDownloadCsv = vi.hoisted(() => vi.fn());

vi.mock("../../lib/dashboards", async (importOriginal) => {
  const mod =
    await importOriginal<typeof import("../../lib/dashboards")>();
  return {
    ...mod,
    fetchWidgetData: mockFetchWidgetData,
    downloadWidgetCsv: mockDownloadCsv,
  };
});

afterEach(() => {
  cleanup();
  mockFetchWidgetData.mockReset();
  mockDownloadCsv.mockReset();
});

function widget(
  type: WidgetType,
  config: DashboardWidget["config"] = { group_by: "state" },
): DashboardWidget {
  return {
    id: "w1",
    dashboard_id: "d1",
    title: "Phase 2 widget",
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

function renderCard(w: DashboardWidget, canEdit = true) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={qc}>
      <WidgetCard widget={w} canEdit={canEdit} onEdit={() => {}} onDelete={() => {}} />
    </QueryClientProvider>,
  );
}

describe("WidgetCard smart_counter", () => {
  it("renders grid tiles and paginates through the envelope", async () => {
    mockFetchWidgetData.mockImplementation((_id, page = 1) =>
      Promise.resolve(
        page === 1
          ? {
              groups: [
                { key: "a", label: "Backlog", value: 5 },
                { key: "b", label: "Done", value: 3 },
              ],
              page: 1,
              max_rows: 2,
              total_groups: 3,
            }
          : {
              groups: [{ key: "c", label: "In review", value: 2 }],
              page: 2,
              max_rows: 2,
              total_groups: 3,
            },
      ),
    );
    const user = userEvent.setup();
    renderCard(widget("smart_counter", { group_by: "state", max_rows: 2 }));

    expect(await screen.findByText("Backlog")).toBeInTheDocument();
    expect(screen.getByText("5")).toBeInTheDocument();
    expect(screen.getByText("Page 1 of 2")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Next page" }));
    expect(mockFetchWidgetData).toHaveBeenCalledWith("w1", 2);
    expect(await screen.findByText("In review")).toBeInTheDocument();
    expect(screen.getByText("Page 2 of 2")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Next page" }),
    ).toBeDisabled();
  });

  it("renders table rows in table visualization", async () => {
    mockFetchWidgetData.mockResolvedValue({
      groups: [
        { key: "a", label: "Backlog", value: 5 },
        { key: "b", label: "Done", value: 3 },
      ],
      page: 1,
      max_rows: 10,
      total_groups: 2,
    });
    renderCard(
      widget("smart_counter", {
        group_by: "state",
        visualization: "table",
      }),
    );
    const table = await screen.findByRole("table");
    expect(within(table).getByText("Backlog")).toBeInTheDocument();
    expect(within(table).getByText("Done")).toBeInTheDocument();
    // One page only — no pagination controls.
    expect(screen.queryByText(/Page 1 of/)).not.toBeInTheDocument();
  });
});

describe("WidgetCard smart_gauge", () => {
  const groups = [
    { key: "s1", label: "Backend", value: 10, completed: 5, percentage: 50 },
    { key: "s2", label: "Frontend", value: 4, completed: 4, percentage: 100 },
  ];

  it("draws the semi-circular arc from the payload percentage", async () => {
    mockFetchWidgetData.mockResolvedValue({
      groups,
      page: 1,
      max_rows: 10,
      total_groups: 2,
    });
    renderCard(
      widget("smart_gauge", {
        group_by: "state_group",
        gauge_color: "#ef4444",
        show_percentage: true,
      }),
    );
    expect(await screen.findByText("Backend")).toBeInTheDocument();
    const arc = screen.getByTestId("gauge-arc-s1");
    // 50% of a radius-40 semicircle ends at the top point (40, 0).
    expect(arc.getAttribute("d")).toBe("M 0 40 A 40 40 0 0 1 40 0");
    expect(arc.getAttribute("stroke")).toBe("#ef4444");
    expect(arc.getAttribute("data-percentage")).toBe("50");
    expect(screen.getByText("50%")).toBeInTheDocument();
    expect(screen.getByText("100%")).toBeInTheDocument();
    expect(screen.getByText("5 of 10 completed")).toBeInTheDocument();
  });

  it("hides the % text when show_percentage is false", async () => {
    mockFetchWidgetData.mockResolvedValue({
      groups,
      page: 1,
      max_rows: 10,
      total_groups: 2,
    });
    renderCard(
      widget("smart_gauge", {
        group_by: "state_group",
        show_percentage: false,
      }),
    );
    expect(await screen.findByText("Backend")).toBeInTheDocument();
    expect(screen.queryByText("50%")).not.toBeInTheDocument();
    expect(screen.getByTestId("gauge-arc-s1")).toBeInTheDocument();
  });
});

describe("WidgetCard table_2d", () => {
  it("renders rows sorted by count descending", async () => {
    mockFetchWidgetData.mockResolvedValue({
      groups: [
        { key: "a", label: "Low", value: 2 },
        { key: "b", label: "Urgent", value: 9 },
        { key: "c", label: "High", value: 5 },
      ],
      page: 1,
      max_rows: 10,
      total_groups: 3,
    });
    renderCard(widget("table_2d", { group_by: "priority" }));
    const table = await screen.findByRole("table");
    const labels = within(table)
      .getAllByRole("row")
      .slice(1)
      .map((r) => r.textContent);
    expect(labels[0]).toContain("Urgent");
    expect(labels[1]).toContain("High");
    expect(labels[2]).toContain("Low");
  });
});

describe("WidgetCard intake_breakdown", () => {
  const intake = {
    in_app_volume: 12,
    email_volume: 0,
    form_volume: 3,
    total_accepted: 9,
    total_rejected: 2,
    avg_decide_seconds: 5400,
  };

  it("renders the six tiles with the honest email tooltip", async () => {
    mockFetchWidgetData.mockResolvedValue({ intake });
    renderCard(widget("intake_breakdown", { view: "grid" }));
    expect(await screen.findByText("In-app volume")).toBeInTheDocument();
    for (const label of [
      "Email volume",
      "Form volume",
      "Total accepted",
      "Total rejected",
      "Avg. time to decide",
    ]) {
      expect(screen.getByText(label)).toBeInTheDocument();
    }
    expect(screen.getByText("12")).toBeInTheDocument();
    expect(screen.getByText("1h 30m")).toBeInTheDocument();
    // The email tile carries the honest no-ingestion tooltip — the 0
    // is never presented as a live count.
    expect(screen.getByTitle(EMAIL_INGESTION_NOTE)).toBeInTheDocument();
  });

  it("renders the two-column table view", async () => {
    mockFetchWidgetData.mockResolvedValue({ intake });
    renderCard(widget("intake_breakdown", { view: "table" }));
    const table = await screen.findByRole("table");
    expect(within(table).getByText("Form volume")).toBeInTheDocument();
    expect(within(table).getByText("3")).toBeInTheDocument();
    expect(within(table).getByText("Avg. time to decide")).toBeInTheDocument();
  });
});

describe("WidgetCard Export CSV", () => {
  it("downloads the CSV from the widget menu (reader-visible)", async () => {
    mockFetchWidgetData.mockResolvedValue({ value: 7 });
    mockDownloadCsv.mockResolvedValue(undefined);
    const user = userEvent.setup();
    renderCard(widget("number", {}), false);
    await screen.findByText("7");
    await user.click(
      screen.getByLabelText("Widget options for Phase 2 widget"),
    );
    await user.click(
      await screen.findByRole("menuitem", { name: /Export CSV/ }),
    );
    expect(mockDownloadCsv).toHaveBeenCalledWith("w1");
  });

  it("keeps Edit/Delete items editor-only in the same menu", async () => {
    mockFetchWidgetData.mockResolvedValue({ value: 7 });
    const user = userEvent.setup();
    renderCard(widget("number", {}), false);
    await screen.findByText("7");
    await user.click(
      screen.getByLabelText("Widget options for Phase 2 widget"),
    );
    expect(
      await screen.findByRole("menuitem", { name: /Export CSV/ }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("menuitem", { name: /Edit widget/ }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("menuitem", { name: /Delete widget/ }),
    ).not.toBeInTheDocument();
  });
});
