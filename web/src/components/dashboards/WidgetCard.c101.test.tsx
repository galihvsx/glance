// @vitest-environment jsdom
// C101T1: WidgetCard pql_table — no card change was needed: the
// C100 error state renders the data route's message verbatim, so
// the C101T0 per-project annotation (`in project "Mobile App": ...`)
// reaches the card unchanged. This test pins that passthrough.
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import WidgetCard from "./WidgetCard";
import type { DashboardWidget, WidgetData } from "../../lib/dashboards";

const mockFetchWidgetData = vi.hoisted(
  () => vi.fn<(id: string, page?: number) => Promise<WidgetData>>(),
);
const mockDownloadCsv = vi.hoisted(() => vi.fn());
const mockUpdateWidget = vi.hoisted(() => vi.fn());

vi.mock("../../lib/dashboards", async (importOriginal) => {
  const mod =
    await importOriginal<typeof import("../../lib/dashboards")>();
  return {
    ...mod,
    fetchWidgetData: mockFetchWidgetData,
    downloadWidgetCsv: mockDownloadCsv,
    updateWidget: mockUpdateWidget,
  };
});

afterEach(() => {
  cleanup();
  mockFetchWidgetData.mockReset();
  mockDownloadCsv.mockReset();
  mockUpdateWidget.mockReset();
});

function pqlWidget(
  overrides: Partial<DashboardWidget> = {},
): DashboardWidget {
  return {
    id: "w-pql",
    dashboard_id: "d1",
    title: "High-priority open work",
    widget_type: "pql_table",
    config: {
      query: 'state = "In Review" AND priority >= high',
      project_ids: ["p1", "p2"],
      columns: ["id", "title", "state", "priority", "project"],
      sort_by: "state",
      sort_dir: "desc",
      max_rows: 10,
    },
    grid_x: 0,
    grid_y: 0,
    grid_w: 6,
    grid_h: 4,
    created_at: "",
    updated_at: "",
    ...overrides,
  };
}

describe("WidgetCard pql_table per-project error (C101T1)", () => {
  it("shows the per-project error message verbatim, including the project name", async () => {
    mockFetchWidgetData.mockRejectedValue(
      new Error(
        'in project "Mobile App": unknown state "In Review" at position 12',
      ),
    );
    const onEdit = vi.fn();
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    render(
      <QueryClientProvider client={qc}>
        <WidgetCard
          widget={pqlWidget()}
          canEdit
          onEdit={onEdit}
          onDelete={vi.fn()}
          onIssueClick={vi.fn()}
        />
      </QueryClientProvider>,
    );
    await waitFor(
      () =>
        expect(
          screen.getByText(
            'Query error: in project "Mobile App": unknown state "In Review" at position 12',
          ),
        ).toBeInTheDocument(),
      { timeout: 5000 },
    );
    const edit = screen.getByRole("button", { name: "Edit widget" });
    fireEvent.click(edit);
    expect(onEdit).toHaveBeenCalledTimes(1);
  });
});
