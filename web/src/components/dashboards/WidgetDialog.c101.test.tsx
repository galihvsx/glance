// @vitest-environment jsdom
// C101T1: WidgetDialog PQL panel — multi-project picker:
// picking two projects submits both, and edit mode prefills two
// picked projects. The config key set is exactly the allow-list
// (query, columns, sort_by, sort_dir, max_rows, project_ids —
// never filters). C102T1 updated the deselect-to-zero case: empty
// now means all projects and keeps save enabled (see
// WidgetDialog.c102.test.tsx for the full all-projects suite).
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

const PROJECTS = [
  { id: "p1", name: "ENG", identifier: "ENG", archived_at: null },
  { id: "p2", name: "WEB", identifier: "WEB", archived_at: null },
];

const mockApi = vi.hoisted(() => ({
  get: vi.fn((path: string): Promise<unknown> => {
    if (path.endsWith("/projects"))
      return Promise.resolve({ projects: PROJECTS });
    if (path.endsWith("/members")) return Promise.resolve({ members: [] });
    if (path.includes("/states")) return Promise.resolve({ states: [] });
    if (path.includes("/labels")) return Promise.resolve({ labels: [] });
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

function fillQuery(v: string) {
  fireEvent.change(screen.getByLabelText("PQL query"), {
    target: { value: v },
  });
}

function projectPickerTrigger() {
  return screen.getByRole("button", {
    name: /All projects|Select projects|projects? selected/,
  });
}

async function openProjectPicker() {
  fireEvent.click(projectPickerTrigger());
  await waitFor(() =>
    expect(screen.getByRole("checkbox", { name: "ENG" })).toBeInTheDocument(),
  );
}

function toggleProject(name: string) {
  fireEvent.click(screen.getByRole("checkbox", { name }));
}

async function submit(onSubmit: ReturnType<typeof vi.fn>) {
  fireEvent.click(screen.getByRole("button", { name: /Add widget|Save changes/ }));
  await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
  return onSubmit.mock.calls[0][0] as WidgetFormValue;
}

function pqlWidget(
  overrides: Partial<DashboardWidget> = {},
): DashboardWidget {
  return {
    id: "w1",
    dashboard_id: "d1",
    title: "Open bugs — ENG + WEB",
    widget_type: "pql_table",
    config: {
      query: 'state = "In Progress"',
      project_ids: ["p1", "p2"],
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

describe("WidgetDialog PQL panel multi-project picker (C101T1)", () => {
  it("submits two picked projects with the exact key set, never filters", async () => {
    const { onSubmit } = renderDialog();
    selectType(/PQL table/);
    await openProjectPicker();
    toggleProject("ENG");
    toggleProject("WEB");
    expect(projectPickerTrigger()).toHaveTextContent("2 projects selected");
    fillTitle("Open bugs — ENG + WEB");
    fillQuery('state = "In Progress" AND priority >= high');
    const form = await submit(onSubmit);
    expect(form.widget_type).toBe("pql_table");
    expect(form.config).toEqual({
      query: 'state = "In Progress" AND priority >= high',
      columns: [
        "id",
        "title",
        "state",
        "priority",
        "assignee",
        "labels",
        "project",
      ],
      sort_by: "state",
      sort_dir: "desc",
      max_rows: 10,
      project_ids: ["p1", "p2"],
    });
    expect(form.config.filters).toBeUndefined();
  });

  it("keeps save enabled after deselecting back to zero projects (all-projects scope, C102T1)", async () => {
    const { onSubmit } = renderDialog();
    selectType(/PQL table/);
    await openProjectPicker();
    toggleProject("ENG");
    expect(projectPickerTrigger()).toHaveTextContent("1 project selected");
    fillTitle("Open bugs");
    fillQuery('state = "In Progress"');
    expect(
      screen.getByRole("button", { name: /Add widget/ }),
    ).not.toBeDisabled();
    toggleProject("ENG");
    expect(projectPickerTrigger()).toHaveTextContent("All projects");
    expect(
      screen.queryByText(/Select at least one project/),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /Add widget/ }),
    ).not.toBeDisabled();
    const form = await submit(onSubmit);
    expect(form.config.project_ids).toEqual([]);
  });

  it("prefills two picked projects in edit mode and keeps save enabled", async () => {
    renderDialog({ mode: "edit", initial: pqlWidget() });
    expect(projectPickerTrigger()).toHaveTextContent("2 projects selected");
    expect(screen.getByLabelText("PQL query")).toHaveValue(
      'state = "In Progress"',
    );
    expect(
      screen.getByRole("button", { name: /Save changes/ }),
    ).not.toBeDisabled();
  });
});
