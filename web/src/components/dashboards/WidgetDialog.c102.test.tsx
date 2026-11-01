// @vitest-environment jsdom
// C102T1: WidgetDialog PQL panel — empty-means-all project scope:
// with no project picked the trigger reads "All projects", save is
// enabled with a non-blank query (still blocked on a blank query),
// and submit sends project_ids: [] with exactly the C102T0
// allow-list (query, columns, sort_by, sort_dir, max_rows,
// project_ids — never filters). Pinned multi-selection behavior
// is unchanged: the label counts picked projects and submit sends
// exactly those ids.
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

function saveButton() {
  return screen.getByRole("button", { name: /Add widget|Save changes/ });
}

async function submit(onSubmit: ReturnType<typeof vi.fn>) {
  fireEvent.click(saveButton());
  await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
  return onSubmit.mock.calls[0][0] as WidgetFormValue;
}

function pqlWidget(
  overrides: Partial<DashboardWidget> = {},
): DashboardWidget {
  return {
    id: "w1",
    dashboard_id: "d1",
    title: "Open bugs — all projects",
    widget_type: "pql_table",
    config: {
      query: 'state = "In Progress"',
      project_ids: [],
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

describe("WidgetDialog PQL panel all-projects scope (C102T1)", () => {
  it("labels the empty selection 'All projects' and shows no select-at-least-one hint", async () => {
    renderDialog();
    selectType(/PQL table/);
    expect(projectPickerTrigger()).toHaveTextContent("All projects");
    expect(
      screen.queryByText(/Select at least one project/),
    ).not.toBeInTheDocument();
  });

  it("enables save at 0 projects with a non-blank query", () => {
    renderDialog();
    selectType(/PQL table/);
    fillTitle("Open bugs — all projects");
    fillQuery('state = "In Progress"');
    expect(saveButton()).not.toBeDisabled();
  });

  it("still blocks save on a blank query at 0 projects", () => {
    const { onSubmit } = renderDialog();
    selectType(/PQL table/);
    fillTitle("Open bugs — all projects");
    expect(saveButton()).toBeDisabled();
    fireEvent.click(saveButton());
    expect(onSubmit).not.toHaveBeenCalled();
    // Whitespace-only is still blank.
    fillQuery("   ");
    expect(saveButton()).toBeDisabled();
    fireEvent.click(saveButton());
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("submits project_ids: [] with the exact key set, never filters, at 0 projects", async () => {
    const { onSubmit } = renderDialog();
    selectType(/PQL table/);
    fillTitle("Open bugs — all projects");
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
      project_ids: [],
    });
    expect(form.config.filters).toBeUndefined();
  });

  it("keeps the 'All projects' label after deselecting back to zero", async () => {
    renderDialog();
    selectType(/PQL table/);
    await openProjectPicker();
    toggleProject("ENG");
    expect(projectPickerTrigger()).toHaveTextContent("1 project selected");
    toggleProject("ENG");
    expect(projectPickerTrigger()).toHaveTextContent("All projects");
    expect(
      screen.queryByText(/Select at least one project/),
    ).not.toBeInTheDocument();
  });

  it("prefills an empty project_ids config as 'All projects' in edit mode with save enabled", () => {
    renderDialog({ mode: "edit", initial: pqlWidget() });
    expect(projectPickerTrigger()).toHaveTextContent("All projects");
    expect(saveButton()).not.toBeDisabled();
  });

  it("pinned multi-selection behavior is unchanged: label and submit key set", async () => {
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
});
