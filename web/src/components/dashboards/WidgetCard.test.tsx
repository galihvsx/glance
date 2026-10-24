// @vitest-environment jsdom
// C81T1: WidgetCard — number scalar, loading/empty/error states, and
// read-only mode (no drag handle, no edit/delete affordances).
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import WidgetCard from "./WidgetCard";
import type {
  DashboardWidget,
  WidgetData,
  WidgetType,
} from "../../lib/dashboards";

const mockFetchWidgetData = vi.hoisted(
  () => vi.fn<(id: string) => Promise<WidgetData>>(),
);

vi.mock("../../lib/dashboards", async (importOriginal) => {
  const mod =
    await importOriginal<typeof import("../../lib/dashboards")>();
  return { ...mod, fetchWidgetData: mockFetchWidgetData };
});

afterEach(() => {
  cleanup();
  mockFetchWidgetData.mockReset();
});

function widget(type: WidgetType): DashboardWidget {
  return {
    id: "w1",
    dashboard_id: "d1",
    title: "Issues by state",
    widget_type: type,
    config: { group_by: "state" },
    grid_x: 0,
    grid_y: 0,
    grid_w: 6,
    grid_h: 4,
    created_at: "",
    updated_at: "",
  };
}

function renderCard(props: Partial<Parameters<typeof WidgetCard>[0]> = {}) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const onEdit = vi.fn();
  const onDelete = vi.fn();
  render(
    <QueryClientProvider client={qc}>
      <WidgetCard
        widget={widget("bar")}
        canEdit
        onEdit={onEdit}
        onDelete={onDelete}
        {...props}
      />
    </QueryClientProvider>,
  );
  return { onEdit, onDelete };
}

describe("WidgetCard", () => {
  it("renders the number scalar", async () => {
    mockFetchWidgetData.mockResolvedValue({ value: 42 });
    renderCard({ widget: widget("number") });
    expect(await screen.findByText("42")).toBeInTheDocument();
  });

  it("shows the loading skeleton while fetching", () => {
    mockFetchWidgetData.mockReturnValue(new Promise(() => {}));
    renderCard({ widget: widget("number") });
    expect(
      screen.getByLabelText("Loading widget data"),
    ).toBeInTheDocument();
  });

  it("shows the empty state when no data matches", async () => {
    mockFetchWidgetData.mockResolvedValue({ groups: [] });
    renderCard();
    expect(
      await screen.findByText("No data matches these filters."),
    ).toBeInTheDocument();
  });

  it("shows the empty state for a number widget with no value", async () => {
    mockFetchWidgetData.mockResolvedValue({});
    renderCard({ widget: widget("number") });
    expect(
      await screen.findByText("No data matches these filters."),
    ).toBeInTheDocument();
  });

  it("shows the error state with a retry affordance", async () => {
    mockFetchWidgetData.mockRejectedValue(new Error("boom"));
    renderCard();
    expect(
      await screen.findByText("Couldn't load this widget.", undefined, {
        // The card retries once before surfacing the error.
        timeout: 4000,
      }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /Retry/ }),
    ).toBeInTheDocument();
  });

  it("hides drag handle and edit/delete affordances for non-editors", async () => {
    mockFetchWidgetData.mockResolvedValue({ value: 7 });
    renderCard({ canEdit: false, widget: widget("number") });
    await screen.findByText("7");
    expect(
      screen.queryByLabelText(/Drag .* to reposition/),
    ).not.toBeInTheDocument();
    // The options menu itself stays (it carries Export CSV, readable
    // by anyone who can read the widget — covered in the phase-2
    // tests); only the edit/delete items are editor-only.
    expect(
      screen.getByLabelText(/Widget options for/),
    ).toBeInTheDocument();
  });

  it("shows drag handle and options menu for editors", async () => {
    mockFetchWidgetData.mockResolvedValue({ value: 7 });
    renderCard({
      widget: widget("number"),
      drag: { attributes: {}, isDragging: false },
    });
    await screen.findByText("7");
    expect(
      screen.getByLabelText("Drag Issues by state to reposition"),
    ).toBeInTheDocument();
    expect(
      screen.getByLabelText("Widget options for Issues by state"),
    ).toBeInTheDocument();
  });
});
