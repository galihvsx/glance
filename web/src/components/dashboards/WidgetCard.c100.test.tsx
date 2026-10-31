// @vitest-environment jsdom
// C100T1: WidgetCard pql_table rendering — rows via the shared
// work_items_table rendering + pagination, the query subtitle, the
// empty state, and the per-widget query error state with an Edit
// affordance that leaves the rest of the dashboard intact.
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import WidgetCard from "./WidgetCard";
import type {
  DashboardWidget,
  WidgetData,
  WidgetRowItem,
} from "../../lib/dashboards";

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
    labels: [{ id: "l1", name: "auth", color: "#000" }],
    project: { id: "p1", name: "ENG" },
    ...overrides,
  };
}

function pqlWidget(
  overrides: Partial<DashboardWidget> = {},
): DashboardWidget {
  return {
    id: "w-pql",
    dashboard_id: "d1",
    title: "Open bugs — ENG",
    widget_type: "pql_table",
    config: {
      query: 'state = "In Progress" AND priority >= high',
      project_ids: ["p1"],
      columns: ["id", "title", "state", "priority", "assignee", "labels"],
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

function renderCard(
  w: DashboardWidget,
  opts: { onEdit?: ReturnType<typeof vi.fn> } = {},
) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const onEdit = opts.onEdit ?? vi.fn();
  render(
    <QueryClientProvider client={qc}>
      <WidgetCard
        widget={w}
        canEdit
        onEdit={onEdit as () => void}
        onDelete={vi.fn()}
        onIssueClick={vi.fn()}
      />
    </QueryClientProvider>,
  );
  return { onEdit };
}

describe("WidgetCard pql_table widgets (C100T1)", () => {
  it("renders rows with the configured columns and the query subtitle", async () => {
    mockFetchWidgetData.mockResolvedValue({
      rows: [row("ENG-214", "Login session expires mid-upload")],
      page: 1,
      max_rows: 10,
      total: 1,
    });
    renderCard(pqlWidget());
    await waitFor(() =>
      expect(
        screen.getByText("Login session expires mid-upload"),
      ).toBeInTheDocument(),
    );
    expect(screen.getByText("ENG-214")).toBeInTheDocument();
    expect(screen.getByText("In progress")).toBeInTheDocument();
    // The stored query is shown under the header (prototype-as-spec).
    expect(
      screen.getByText('state = "In Progress" AND priority >= high'),
    ).toBeInTheDocument();
    // The project column was deselected in config: no project cell.
    expect(screen.queryByText("ENG")).not.toBeInTheDocument();
  });

  it("paginates through the LIMIT-capped total", async () => {
    const page1 = {
      rows: [row("ENG-214", "First page row")],
      page: 1,
      max_rows: 1,
      total: 2,
    };
    const page2 = {
      rows: [row("ENG-209", "Second page row")],
      page: 2,
      max_rows: 1,
      total: 2,
    };
    mockFetchWidgetData.mockImplementation((_id, page) =>
      Promise.resolve(page === 2 ? page2 : page1),
    );
    renderCard(pqlWidget({ config: { max_rows: 1 } }));
    await waitFor(() =>
      expect(screen.getByText("First page row")).toBeInTheDocument(),
    );
    expect(screen.getByText("Page 1 of 2")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Next page" }));
    await waitFor(() =>
      expect(screen.getByText("Second page row")).toBeInTheDocument(),
    );
    expect(mockFetchWidgetData).toHaveBeenCalledWith("w-pql", 2);
  });

  it("shows the PQL empty state when the query matches nothing", async () => {
    mockFetchWidgetData.mockResolvedValue({
      rows: [],
      page: 1,
      max_rows: 10,
      total: 0,
    });
    renderCard(pqlWidget());
    await waitFor(() =>
      expect(
        screen.getByText("No issues match this query."),
      ).toBeInTheDocument(),
    );
  });

  it("shows the query error with an Edit affordance, not a generic error", async () => {
    mockFetchWidgetData.mockRejectedValue(
      new Error('unknown state "In Progress" in this project'),
    );
    const { onEdit } = renderCard(pqlWidget());
    // The card retries once before settling on the error state.
    await waitFor(
      () =>
        expect(
          screen.getByText(
            'Query error: unknown state "In Progress" in this project',
          ),
        ).toBeInTheDocument(),
      { timeout: 5000 },
    );
    expect(
      screen.getByText(/may name something that was renamed/),
    ).toBeInTheDocument();
    const edit = screen.getByRole("button", { name: "Edit widget" });
    fireEvent.click(edit);
    expect(onEdit).toHaveBeenCalledTimes(1);
    // No pagination footer on the error state.
    expect(screen.queryByText(/Page \d+ of/)).not.toBeInTheDocument();
  });

  it("a failing PQL card does not break a sibling widget", async () => {
    mockFetchWidgetData.mockImplementation((id) =>
      id === "w-pql"
        ? Promise.reject(
            new Error('unknown state "In Progress" in this project'),
          )
        : Promise.resolve({
            rows: [row("ENG-300", "Sibling row")],
            page: 1,
            max_rows: 10,
            total: 1,
          }),
    );
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    render(
      <QueryClientProvider client={qc}>
        <WidgetCard
          widget={pqlWidget()}
          canEdit
          onEdit={vi.fn()}
          onDelete={vi.fn()}
          onIssueClick={vi.fn()}
        />
        <WidgetCard
          widget={pqlWidget({
            id: "w-ok",
            title: "Sibling table",
            widget_type: "work_items_table",
            config: {},
          })}
          canEdit
          onEdit={vi.fn()}
          onDelete={vi.fn()}
          onIssueClick={vi.fn()}
        />
      </QueryClientProvider>,
    );
    // The card retries once before settling on the error state.
    await waitFor(
      () =>
        expect(
          screen.getByText(
            'Query error: unknown state "In Progress" in this project',
          ),
        ).toBeInTheDocument(),
      { timeout: 5000 },
    );
    expect(screen.getByText("Sibling row")).toBeInTheDocument();
  });
});
