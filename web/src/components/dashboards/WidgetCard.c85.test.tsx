// @vitest-environment jsdom
// C85T1: WidgetCard intake_breakdown drill-down — every tile number
// clickable (except the avg-time-to-decide aggregate) into the C84
// drill-down modal shape, the funnel row in table view, metric-scoped
// CSV export, and honest empty/error states (email has no ingestion).
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import WidgetCard from "./WidgetCard";
import { ApiError } from "../../lib/api";
import {
  intakeDrilldownRowToWidgetRow,
  type DashboardWidget,
  type IntakeDrilldown,
  type IntakeDrilldownItem,
  type IntakeMetric,
  type WidgetData,
  type WidgetRowItem,
  type WidgetType,
} from "../../lib/dashboards";

const mockFetchWidgetData = vi.hoisted(
  () => vi.fn<(id: string, page?: number) => Promise<WidgetData>>(),
);
const mockFetchIntakeDrilldown = vi.hoisted(
  () =>
    vi.fn<
      (id: string, metric: string, page?: number) => Promise<IntakeDrilldown>
    >(),
);
const mockDownloadCsv = vi.hoisted(() => vi.fn());

vi.mock("../../lib/dashboards", async (importOriginal) => {
  const mod =
    await importOriginal<typeof import("../../lib/dashboards")>();
  return {
    ...mod,
    fetchWidgetData: mockFetchWidgetData,
    fetchIntakeDrilldown: mockFetchIntakeDrilldown,
    downloadWidgetCsv: mockDownloadCsv,
  };
});

afterEach(() => {
  cleanup();
  mockFetchWidgetData.mockReset();
  mockFetchIntakeDrilldown.mockReset();
  mockDownloadCsv.mockReset();
});

function widget(
  type: WidgetType,
  config: DashboardWidget["config"] = {},
): DashboardWidget {
  return {
    id: "w1",
    dashboard_id: "d1",
    title: "Intake widget",
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

const intake = {
  in_app_volume: 12,
  form_volume: 3,
  email_volume: 0,
  total_accepted: 9,
  total_rejected: 2,
  total_duplicate: 4,
  pending: 7,
  snoozed: 3,
  avg_decide_seconds: 5400,
};

function intakeItem(
  overrides: Partial<IntakeDrilldownItem> = {},
): IntakeDrilldownItem {
  return {
    id: "ii1",
    issue_id: "issue-1",
    title: "Fix login redirect",
    project: { id: "p1", name: "Acme" },
    status: 3,
    status_name: "accepted",
    source: "in_app",
    snoozed_till: null,
    created_at: "2026-10-28T10:00:00Z",
    ...overrides,
  };
}

function renderCard(
  w: DashboardWidget,
  canEdit = true,
  onIssueClick?: (row: WidgetRowItem) => void,
) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={qc}>
      <WidgetCard
        widget={w}
        canEdit={canEdit}
        onEdit={() => {}}
        onDelete={() => {}}
        onIssueClick={onIssueClick}
      />
    </QueryClientProvider>,
  );
}

async function openTile(user: ReturnType<typeof userEvent.setup>, label: string) {
  await user.click(
    screen.getByRole("button", { name: `Drill into ${label}` }),
  );
}

describe("WidgetCard intake_breakdown drill-down (grid)", () => {
  const cases: { label: string; metric: IntakeMetric }[] = [
    { label: "In-app volume", metric: "in_app" },
    { label: "Form volume", metric: "form" },
    { label: "Email volume", metric: "email" },
    { label: "Total accepted", metric: "accepted" },
    { label: "Total rejected", metric: "rejected" },
    { label: "Total duplicate", metric: "duplicate" },
    { label: "Pending", metric: "pending" },
    { label: "Snoozed", metric: "snoozed" },
  ];

  for (const { label, metric } of cases) {
    it(`tile click on ${label} opens the modal with metric ${metric}`, async () => {
      mockFetchWidgetData.mockResolvedValue({ intake });
      mockFetchIntakeDrilldown.mockResolvedValue({
        items: [intakeItem()],
        page: 1,
        per_page: 10,
        total: 1,
        metric,
      });
      const user = userEvent.setup();
      renderCard(widget("intake_breakdown", { view: "grid" }));
      expect(await screen.findByText(label)).toBeInTheDocument();

      await openTile(user, label);
      expect(mockFetchIntakeDrilldown).toHaveBeenCalledWith("w1", metric, 1);
      expect(
        await screen.findByText("Fix login redirect"),
      ).toBeInTheDocument();
    });
  }

  it("renders the funnel tiles (duplicate, pending, snoozed) with counts", async () => {
    mockFetchWidgetData.mockResolvedValue({ intake });
    renderCard(widget("intake_breakdown", { view: "grid" }));
    expect(await screen.findByText("Total duplicate")).toBeInTheDocument();
    expect(screen.getByText("Pending")).toBeInTheDocument();
    expect(screen.getByText("Snoozed")).toBeInTheDocument();
    expect(screen.getByText("4")).toBeInTheDocument();
    expect(screen.getByText("7")).toBeInTheDocument();
    // "3" is ambiguous (form volume and snoozed both equal 3) —
    // scope it to the Snoozed tile button.
    expect(
      screen.getByRole("button", { name: "Drill into Snoozed" }),
    ).toHaveTextContent("3");
  });

  it("avg tile is not clickable (aggregate, no row list)", async () => {
    mockFetchWidgetData.mockResolvedValue({ intake });
    const user = userEvent.setup();
    renderCard(widget("intake_breakdown", { view: "grid" }));
    expect(await screen.findByText("Avg. time to decide")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /Drill into Avg/ }),
    ).not.toBeInTheDocument();
    await user.click(screen.getByText("Avg. time to decide"));
    expect(mockFetchIntakeDrilldown).not.toHaveBeenCalled();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("email tile opens the modal with the honest empty state", async () => {
    mockFetchWidgetData.mockResolvedValue({ intake });
    mockFetchIntakeDrilldown.mockResolvedValue({
      items: [],
      page: 1,
      per_page: 10,
      total: 0,
      metric: "email",
    });
    const user = userEvent.setup();
    renderCard(widget("intake_breakdown", { view: "grid" }));
    await screen.findByText("Email volume");
    await openTile(user, "Email volume");
    expect(
      await screen.findByText(
        "No email intake — glance has no email ingestion yet",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByText(/Couldn't load/)).not.toBeInTheDocument();
  });

  it("modal shows metric title + total, and paginates", async () => {
    mockFetchWidgetData.mockResolvedValue({ intake });
    mockFetchIntakeDrilldown.mockImplementation((_id, _m, page = 1) =>
      Promise.resolve({
        items: page === 1 ? [intakeItem()] : [intakeItem({ id: "ii2" })],
        page,
        per_page: 1,
        total: 2,
        metric: "accepted",
      }),
    );
    const user = userEvent.setup();
    renderCard(widget("intake_breakdown", { view: "grid" }));
    await screen.findByText("Total accepted");
    await openTile(user, "Total accepted");
    expect(await screen.findByText("Fix login redirect")).toBeInTheDocument();
    expect(screen.getByText(/2 intake rows/)).toBeInTheDocument();
    expect(screen.getByText("Page 1 of 2")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Next page" }));
    expect(mockFetchIntakeDrilldown).toHaveBeenCalledWith("w1", "accepted", 2);
    expect(screen.getByText("Page 2 of 2")).toBeInTheDocument();
  });

  it("modal Export CSV downloads the metric-scoped export", async () => {
    mockFetchWidgetData.mockResolvedValue({ intake });
    mockFetchIntakeDrilldown.mockResolvedValue({
      items: [intakeItem()],
      page: 1,
      per_page: 10,
      total: 1,
      metric: "accepted",
    });
    mockDownloadCsv.mockResolvedValue(undefined);
    const user = userEvent.setup();
    renderCard(widget("intake_breakdown", { view: "grid" }));
    await screen.findByText("Total accepted");
    await openTile(user, "Total accepted");
    await screen.findByText("Fix login redirect");
    await user.click(screen.getByRole("button", { name: /Export CSV/ }));
    expect(mockDownloadCsv).toHaveBeenCalledWith("w1", "accepted");
  });

  it("modal row click navigates via onIssueClick (C84 URL pattern input)", async () => {
    mockFetchWidgetData.mockResolvedValue({ intake });
    mockFetchIntakeDrilldown.mockResolvedValue({
      items: [
        intakeItem({
          snoozed_till: "2026-11-05T09:00:00Z",
          source: "form",
        }),
      ],
      page: 1,
      per_page: 10,
      total: 1,
      metric: "snoozed",
    });
    const onIssueClick = vi.fn();
    const user = userEvent.setup();
    renderCard(widget("intake_breakdown", { view: "grid" }), true, onIssueClick);
    await screen.findByText("Snoozed");
    await openTile(user, "Snoozed");
    const row = await screen.findByText("Fix login redirect");
    // Snoozed-till renders where present, plus project/status/source.
    expect(screen.getByText("Acme")).toBeInTheDocument();
    expect(screen.getByText("accepted")).toBeInTheDocument();
    expect(screen.getByText("form")).toBeInTheDocument();
    expect(screen.getByText(/Nov 5, 2026/)).toBeInTheDocument();
    await user.click(row);
    expect(onIssueClick).toHaveBeenCalledWith(
      expect.objectContaining({
        id: "issue-1",
        title: "Fix login redirect",
        project: { id: "p1", name: "Acme" },
      }),
    );
  });

  it("honest server 400s are surfaced in-modal", async () => {
    mockFetchWidgetData.mockResolvedValue({ intake });
    mockFetchIntakeDrilldown.mockRejectedValue(
      new ApiError(400, "average time to decide has no underlying row list"),
    );
    const user = userEvent.setup();
    renderCard(widget("intake_breakdown", { view: "grid" }));
    await screen.findByText("Total accepted");
    await openTile(user, "Total accepted");
    expect(
      await screen.findByText(
        "average time to decide has no underlying row list",
      ),
    ).toBeInTheDocument();
  });

  it("shared-view read-only still drills (no edit affordance needed)", async () => {
    mockFetchWidgetData.mockResolvedValue({ intake });
    mockFetchIntakeDrilldown.mockResolvedValue({
      items: [intakeItem()],
      page: 1,
      per_page: 10,
      total: 1,
      metric: "pending",
    });
    const user = userEvent.setup();
    renderCard(widget("intake_breakdown", { view: "grid" }), false);
    await screen.findByText("Pending");
    await openTile(user, "Pending");
    expect(mockFetchIntakeDrilldown).toHaveBeenCalledWith("w1", "pending", 1);
    expect(
      await screen.findByText("Fix login redirect"),
    ).toBeInTheDocument();
  });
});

describe("WidgetCard intake_breakdown drill-down (table)", () => {
  it("funnel row counts are clickable with the same modal behavior", async () => {
    mockFetchWidgetData.mockResolvedValue({ intake });
    mockFetchIntakeDrilldown.mockResolvedValue({
      items: [intakeItem()],
      page: 1,
      per_page: 10,
      total: 1,
      metric: "pending",
    });
    const user = userEvent.setup();
    renderCard(widget("intake_breakdown", { view: "table" }));
    const table = await screen.findByRole("table");
    expect(within(table).getByText("Pending")).toBeInTheDocument();
    expect(within(table).getByText("Total duplicate")).toBeInTheDocument();
    expect(within(table).getByText("Snoozed")).toBeInTheDocument();
    await openTile(user, "Pending");
    expect(mockFetchIntakeDrilldown).toHaveBeenCalledWith("w1", "pending", 1);
    expect(
      await screen.findByText("Fix login redirect"),
    ).toBeInTheDocument();
  });

  it("avg row in table view is not clickable", async () => {
    mockFetchWidgetData.mockResolvedValue({ intake });
    const user = userEvent.setup();
    renderCard(widget("intake_breakdown", { view: "table" }));
    await screen.findByRole("table");
    expect(
      screen.queryByRole("button", { name: /Drill into Avg/ }),
    ).not.toBeInTheDocument();
    await user.click(screen.getByText("Avg. time to decide"));
    expect(mockFetchIntakeDrilldown).not.toHaveBeenCalled();
  });
});

describe("intakeDrilldownRowToWidgetRow", () => {
  it("maps the issue id + project for the C84 row-click pattern", () => {
    const row = intakeDrilldownRowToWidgetRow(intakeItem());
    expect(row.id).toBe("issue-1");
    expect(row.title).toBe("Fix login redirect");
    expect(row.state).toBe("accepted");
    expect(row.project).toEqual({ id: "p1", name: "Acme" });
  });
});
