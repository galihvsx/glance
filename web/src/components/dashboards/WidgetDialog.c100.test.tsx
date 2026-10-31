// @vitest-environment jsdom
// C100T1: WidgetDialog PQL panel — the new type card, the
// single-project picker (required, exactly one), the PQL query editor
// with cheat-sheet, the row-list columns/sort/max-rows controls, and
// the exact per-type config key set on submit (never a filters key).
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

async function waitForProjectOptions() {
  await waitFor(() =>
    expect(screen.getByRole("option", { name: "ENG" })).toBeInTheDocument(),
  );
}

function projectSelect() {
  return screen.getByLabelText(/Project \(exactly one/);
}

function pickProject(id: string) {
  fireEvent.change(projectSelect(), { target: { value: id } });
}

function fillQuery(v: string) {
  fireEvent.change(screen.getByLabelText("PQL query"), {
    target: { value: v },
  });
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
    title: "Open bugs — ENG",
    widget_type: "pql_table",
    config: {
      query: 'state = "In Progress"',
      project_ids: ["p1"],
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

describe("WidgetDialog PQL panel (C100T1)", () => {
  it("offers the PQL table type card", () => {
    renderDialog();
    const group = screen.getByRole("radiogroup", { name: "Widget type" });
    expect(
      within(group).getByRole("radio", { name: /PQL table/ }),
    ).toBeInTheDocument();
  });

  it("shows the single-project picker, PQL editor, and row-list controls for pql_table", async () => {
    renderDialog();
    selectType(/PQL table/);
    await waitForProjectOptions();
    expect(projectSelect()).toBeInTheDocument();
    expect(screen.getByLabelText("PQL query")).toBeInTheDocument();
    expect(screen.getByText("PQL is the filter")).toBeInTheDocument();
    // No visual-filters section for this type: PQL is the filter.
    expect(screen.queryByText("Filters")).not.toBeInTheDocument();
    // Row-list controls mirror the work_items_table panel.
    const chips = screen.getByRole("group", { name: "Columns" });
    for (const label of [
      "ID",
      "Title",
      "State",
      "Priority",
      "Assignee",
      "Labels",
      "Project",
    ]) {
      expect(
        within(chips).getByRole("button", { name: label }),
      ).toBeInTheDocument();
    }
    expect(screen.getByLabelText(/Sort by/)).toBeInTheDocument();
    expect(screen.getByLabelText(/Max rows/)).toBeInTheDocument();
  });

  it("toggles the PQL cheat-sheet", async () => {
    renderDialog();
    selectType(/PQL table/);
    expect(screen.queryByText("State by name (project-scoped)")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Cheat-sheet" }));
    expect(screen.getByText("State by name (project-scoped)")).toBeInTheDocument();
    expect(screen.getByText("state = Todo")).toBeInTheDocument();
  });

  it("does not submit with 0 projects or a blank query", async () => {
    const { onSubmit } = renderDialog();
    selectType(/PQL table/);
    await waitForProjectOptions();
    fillTitle("Open bugs");
    const save = screen.getByRole("button", { name: /Add widget/ });
    // Blank query + no project: disabled.
    expect(save).toBeDisabled();
    fillQuery('state = "In Progress"');
    // Query filled but still no project: disabled.
    expect(save).toBeDisabled();
    fireEvent.click(save);
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("does not submit with 2+ projects carried over from another type", async () => {
    const { onSubmit } = renderDialog({
      mode: "edit",
      initial: pqlWidget({
        widget_type: "bar",
        config: { project_ids: ["p1", "p2"] },
      }),
    });
    selectType(/PQL table/);
    await waitForProjectOptions();
    fillTitle("Open bugs");
    fillQuery('state = "In Progress"');
    // Two projects pinned: the single-select shows the placeholder and
    // save stays disabled until exactly one is picked.
    expect(projectSelect()).toHaveValue("");
    expect(
      screen.getByRole("button", { name: /Save changes/ }),
    ).toBeDisabled();
    pickProject("p1");
    const form = await submit(onSubmit);
    expect(form.config.project_ids).toEqual(["p1"]);
  });

  it("submits the exact pql_table config key set, never filters", async () => {
    const { onSubmit } = renderDialog();
    selectType(/PQL table/);
    await waitForProjectOptions();
    fillTitle("Open bugs — ENG");
    pickProject("p1");
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
      project_ids: ["p1"],
    });
    expect(form.config.filters).toBeUndefined();
    expect(form.config.group_by).toBeUndefined();
    expect(form.config.metric).toBeUndefined();
  });

  it("honors deselected columns in the submitted key set", async () => {
    const { onSubmit } = renderDialog();
    selectType(/PQL table/);
    await waitForProjectOptions();
    fillTitle("Open bugs — ENG");
    pickProject("p1");
    fillQuery('state = "In Progress"');
    const chips = screen.getByRole("group", { name: "Columns" });
    fireEvent.click(within(chips).getByRole("button", { name: "Project" }));
    const form = await submit(onSubmit);
    expect(form.config.columns).toEqual([
      "id",
      "title",
      "state",
      "priority",
      "assignee",
      "labels",
    ]);
  });

  it("prefills the query and pinned project in edit mode", async () => {
    renderDialog({ mode: "edit", initial: pqlWidget() });
    await waitForProjectOptions();
    expect(screen.getByLabelText("PQL query")).toHaveValue(
      'state = "In Progress"',
    );
    expect(projectSelect()).toHaveValue("p1");
    expect(
      screen.getByRole("button", { name: /Save changes/ }),
    ).not.toBeDisabled();
  });
});
