// @vitest-environment jsdom
// C81T1/C83T1/C84T1: WidgetDialog — twelve type picker, group-by hidden for
// number/intake_breakdown, filter summary for number, per-type config
// panels and config key sets (C83T1), submit config shape, edit prefill.
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

describe("WidgetDialog", () => {
  it("renders all twelve widget types in the picker", () => {
    renderDialog();
    // Twelve type cards + the visualization/view segmented radios render
    // only for their own types; filter to the type-picker radiogroup.
    const group = screen.getByRole("radiogroup", { name: "Widget type" });
    const typeRadios = within(group).getAllByRole("radio");
    expect(typeRadios).toHaveLength(12);
    for (const label of [
      "Bar chart",
      "Line chart",
      "Area chart",
      "Donut chart",
      "Pie chart",
      "Number",
      "Smart counter",
      "Smart gauge",
      "Table \\(2D\\)",
      "Intake breakdown",
      "Work items table",
      "Assigned to you",
    ]) {
      expect(
        within(group).getByRole("radio", { name: new RegExp(label) }),
      ).toBeInTheDocument();
    }
  });

  it("defaults to bar grouped by state", () => {
    renderDialog();
    expect(
      screen.getByRole("radio", { name: /Bar chart/ }),
    ).toHaveAttribute("aria-checked", "true");
    expect(screen.getByLabelText(/Group by/)).toHaveValue("state");
  });

  it("hides group-by for number and shows the filter summary instead", () => {
    renderDialog();
    fireEvent.click(screen.getByRole("radio", { name: /Number/ }));
    expect(screen.queryByLabelText(/Group by/)).not.toBeInTheDocument();
    expect(screen.getByText("Scope summary")).toBeInTheDocument();
    expect(screen.getByText("All issues, all projects")).toBeInTheDocument();
  });

  it("requires a title before adding", () => {
    const { onSubmit } = renderDialog();
    const add = screen.getByRole("button", { name: /Add widget/ });
    expect(add).toBeDisabled();
    fireEvent.click(add);
    expect(onSubmit).not.toHaveBeenCalled();

    fireEvent.change(screen.getByLabelText(/Title/), {
      target: { value: "Issues by state" },
    });
    expect(add).toBeEnabled();
  });

  it("submits the title, type, and full config shape", async () => {
    const { onSubmit } = renderDialog();
    fireEvent.change(screen.getByLabelText(/Title/), {
      target: { value: "Issues by state" },
    });
    fireEvent.click(screen.getByRole("button", { name: /Add widget/ }));
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    const v: WidgetFormValue = onSubmit.mock.calls[0][0];
    expect(v.title).toBe("Issues by state");
    expect(v.widget_type).toBe("bar");
    expect(v.config).toEqual({
      group_by: "state",
      filters: { assignees: [], priorities: [], states: [], labels: [] },
      project_ids: [],
    });
  });

  it("submits number widgets without group_by", async () => {
    const { onSubmit } = renderDialog();
    fireEvent.change(screen.getByLabelText(/Title/), {
      target: { value: "Total open" },
    });
    fireEvent.click(screen.getByRole("radio", { name: /Number/ }));
    fireEvent.click(screen.getByRole("button", { name: /Add widget/ }));
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    const v: WidgetFormValue = onSubmit.mock.calls[0][0];
    expect(v.widget_type).toBe("number");
    expect(v.config).not.toHaveProperty("group_by");
  });

  it("prefills title and type in edit mode", () => {
    const initial = {
      id: "w1",
      dashboard_id: "d1",
      title: "Old widget",
      widget_type: "donut",
      config: { group_by: "priority" },
      grid_x: 0,
      grid_y: 0,
      grid_w: 6,
      grid_h: 4,
      created_at: "",
      updated_at: "",
    } as DashboardWidget;
    renderDialog({ mode: "edit", initial });
    expect(screen.getByLabelText(/Title/)).toHaveValue("Old widget");
    expect(
      screen.getByRole("radio", { name: /Donut chart/ }),
    ).toHaveAttribute("aria-checked", "true");
    expect(screen.getByLabelText(/Group by/)).toHaveValue("priority");
  });

  it("shows the server error when present", () => {
    renderDialog({ serverError: "group_by is required" });
    expect(screen.getByText("group_by is required")).toBeInTheDocument();
  });
});
