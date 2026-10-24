// @vitest-environment jsdom
// C83T1: WidgetDialog phase-2 panels — per-type config UI (metric,
// max-rows, visualization toggle, gauge color + show-percentage, intake
// view), exact per-type config key sets on submit, and intake hiding
// projects/filters.
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import WidgetDialog, { type WidgetFormValue } from "./WidgetDialog";
import type { DashboardWidget } from "../../lib/dashboards";

const mockApi = vi.hoisted(() => ({
  get: vi.fn((path: string): Promise<unknown> => {
    if (path.endsWith("/projects"))
      return Promise.resolve({ projects: [] });
    if (path.endsWith("/members")) return Promise.resolve({ members: [] });
    throw new Error(`unexpected GET ${path}`);
  }),
}));

vi.mock("../../lib/api", () => ({ api: { get: mockApi.get } }));

afterEach(cleanup);

function renderDialog(
  props: Partial<Parameters<typeof WidgetDialog>[0]> = {},
) {
  const onSubmit = vi.fn();
  const onOpenChange = vi.fn();
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={qc}>
      <WidgetDialog
        open
        onOpenChange={onOpenChange}
        mode="create"
        slug="acme"
        onSubmit={onSubmit}
        {...props}
      />
    </QueryClientProvider>,
  );
  return { onSubmit, onOpenChange };
}

function selectType(name: RegExp) {
  const group = screen.getByRole("radiogroup", { name: "Widget type" });
  fireEvent.click(within(group).getByRole("radio", { name }));
}

function fillTitle(v: string) {
  fireEvent.change(screen.getByLabelText(/Title/), { target: { value: v } });
}

async function submit(onSubmit: ReturnType<typeof vi.fn>) {
  fireEvent.click(screen.getByRole("button", { name: /Add widget/ }));
  await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
  return onSubmit.mock.calls[0][0] as WidgetFormValue;
}

describe("WidgetDialog phase-2 config panels", () => {
  it("offers all seven group-by dimensions for grouped types", () => {
    renderDialog();
    selectType(/Smart counter/);
    const select = screen.getByLabelText(/Group by/) as HTMLSelectElement;
    const values = Array.from(select.options).map((o) => o.value);
    expect(values).toEqual([
      "state",
      "priority",
      "assignee",
      "label",
      "state_group",
      "project",
      "created_by",
    ]);
  });

  it("shows metric, max-rows and the visualization toggle for smart_counter", () => {
    renderDialog();
    selectType(/Smart counter/);
    expect(screen.getByLabelText(/Metric/)).toBeInTheDocument();
    expect(screen.getByLabelText(/Max groups per page/)).toBeInTheDocument();
    const viz = screen.getByRole("radiogroup", {
      name: "Smart counter visualization",
    });
    expect(within(viz).getAllByRole("radio")).toHaveLength(2);
    expect(screen.queryByLabelText(/Gauge color/)).not.toBeInTheDocument();
  });

  it("shows gauge color and the show-percentage switch for smart_gauge", () => {
    renderDialog();
    selectType(/Smart gauge/);
    expect(screen.getByLabelText("Gauge color")).toBeInTheDocument();
    expect(
      screen.getByRole("switch", { name: "Show percentage label" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("radiogroup", {
        name: "Smart counter visualization",
      }),
    ).not.toBeInTheDocument();
  });

  it("submits the smart_counter config key set exactly", async () => {
    const { onSubmit } = renderDialog();
    fillTitle("Workload");
    selectType(/Smart counter/);
    fireEvent.change(screen.getByLabelText(/Metric/), {
      target: { value: "estimate_points" },
    });
    fireEvent.change(screen.getByLabelText(/Max groups per page/), {
      target: { value: "25" },
    });
    const viz = screen.getByRole("radiogroup", {
      name: "Smart counter visualization",
    });
    fireEvent.click(within(viz).getByRole("radio", { name: "Table" }));
    const v = await submit(onSubmit);
    expect(v.widget_type).toBe("smart_counter");
    expect(v.config).toEqual({
      group_by: "state",
      metric: "estimate_points",
      filters: { assignees: [], priorities: [], states: [], labels: [] },
      project_ids: [],
      max_rows: 25,
      visualization: "table",
    });
  });

  it("submits the smart_gauge config key set exactly", async () => {
    const { onSubmit } = renderDialog();
    fillTitle("Completion");
    selectType(/Smart gauge/);
    fireEvent.change(screen.getByLabelText(/Gauge color hex/), {
      target: { value: "#ef4444" },
    });
    fireEvent.click(
      screen.getByRole("switch", { name: "Show percentage label" }),
    );
    const v = await submit(onSubmit);
    expect(v.widget_type).toBe("smart_gauge");
    expect(v.config).toEqual({
      group_by: "state",
      metric: "count",
      filters: { assignees: [], priorities: [], states: [], labels: [] },
      project_ids: [],
      max_rows: 10,
      gauge_color: "#ef4444",
      show_percentage: false,
    });
  });

  it("submits table_2d without metric or visualization", async () => {
    const { onSubmit } = renderDialog();
    fillTitle("By priority");
    selectType(/Table \(2D\)/);
    fireEvent.change(screen.getByLabelText(/Max groups per page/), {
      target: { value: "5" },
    });
    const v = await submit(onSubmit);
    expect(v.widget_type).toBe("table_2d");
    expect(v.config).toEqual({
      group_by: "state",
      filters: { assignees: [], priorities: [], states: [], labels: [] },
      project_ids: [],
      max_rows: 5,
    });
  });

  it("submits intake_breakdown with only the view key and hides scope UI", async () => {
    const { onSubmit } = renderDialog();
    fillTitle("Intake");
    selectType(/Intake breakdown/);
    expect(screen.queryByLabelText(/Group by/)).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/Projects/)).not.toBeInTheDocument();
    expect(screen.getByText("Intake scope")).toBeInTheDocument();
    const view = screen.getByRole("radiogroup", {
      name: "Intake breakdown view",
    });
    fireEvent.click(within(view).getByRole("radio", { name: "Table" }));
    const v = await submit(onSubmit);
    expect(v.widget_type).toBe("intake_breakdown");
    expect(v.config).toEqual({ view: "table" });
  });

  it("prefills phase-2 fields in edit mode", () => {
    const initial = {
      id: "w1",
      dashboard_id: "d1",
      title: "Completion",
      widget_type: "smart_gauge",
      config: {
        group_by: "state_group",
        metric: "estimate_points",
        max_rows: 20,
        gauge_color: "#10b981",
        show_percentage: false,
        filters: { assignees: [], priorities: [], states: [], labels: [] },
        project_ids: [],
      },
      grid_x: 0,
      grid_y: 0,
      grid_w: 6,
      grid_h: 4,
      created_at: "",
      updated_at: "",
    } as DashboardWidget;
    renderDialog({ mode: "edit", initial });
    expect(screen.getByLabelText(/Group by/)).toHaveValue("state_group");
    expect(screen.getByLabelText(/Metric/)).toHaveValue("estimate_points");
    expect(screen.getByLabelText(/Max groups per page/)).toHaveValue(20);
    expect(screen.getByLabelText(/Gauge color hex/)).toHaveValue("#10b981");
    expect(
      screen.getByRole("switch", { name: "Show percentage label" }),
    ).not.toBeChecked();
  });
});
