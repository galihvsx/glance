// @vitest-environment jsdom
// C84T1: WidgetDialog phase-3 panels — the two new type cards,
// columns chips, sort controls, max rows, the forced-assignee note,
// and the exact per-type config key set on submit.
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

describe("WidgetDialog phase-3 config panels", () => {
  it("offers the two new type cards", () => {
    renderDialog();
    const group = screen.getByRole("radiogroup", { name: "Widget type" });
    expect(
      within(group).getByRole("radio", { name: /Work items table/ }),
    ).toBeInTheDocument();
    expect(
      within(group).getByRole("radio", { name: /Assigned to you/ }),
    ).toBeInTheDocument();
  });

  it("shows columns chips, sort controls, and max rows for row-list types", () => {
    renderDialog();
    selectType(/Work items table/);
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
      expect(within(chips).getByRole("button", { name: label })).toBeInTheDocument();
    }
    expect(screen.getByLabelText(/Sort by/)).toBeInTheDocument();
    expect(screen.getByRole("radiogroup", { name: "Sort direction" })).toBeInTheDocument();
    expect(screen.getByLabelText(/Max rows/)).toBeInTheDocument();
  });

  it("submits the exact row-list config key set", async () => {
    const { onSubmit } = renderDialog();
    selectType(/Work items table/);
    fillTitle("Sprint backlog");
    const form = await submit(onSubmit);
    expect(form.widget_type).toBe("work_items_table");
    expect(form.config.columns).toEqual([
      "id",
      "title",
      "state",
      "priority",
      "assignee",
      "labels",
      "project",
    ]);
    expect(form.config.sort_by).toBe("state");
    expect(form.config.sort_dir).toBe("desc");
    expect(form.config.max_rows).toBe(10);
    expect(form.config.group_by).toBeUndefined();
    expect(form.config.metric).toBeUndefined();
  });

  it("assigned_to_me shows the forced-assignee note and defaults without assignee", async () => {
    const { onSubmit } = renderDialog();
    selectType(/Assigned to you/);
    expect(
      screen.getByText(/Assignee is always you — filter is forced/),
    ).toBeInTheDocument();
    fillTitle("My queue");
    const form = await submit(onSubmit);
    expect(form.widget_type).toBe("assigned_to_me");
    expect(form.config.columns).toEqual([
      "id",
      "title",
      "state",
      "priority",
      "labels",
      "project",
    ]);
  });

  it("deselecting a column drops it from the submit, and sort falls back", async () => {
    const { onSubmit } = renderDialog();
    selectType(/Work items table/);
    fillTitle("Backlog");
    // Sort by Priority, then drop the Priority chip: submit must not
    // 400 — sort_by falls back to the first kept column.
    fireEvent.change(screen.getByLabelText(/Sort by/), {
      target: { value: "priority" },
    });
    const chips = screen.getByRole("group", { name: "Columns" });
    fireEvent.click(within(chips).getByRole("button", { name: "Priority" }));
    const form = await submit(onSubmit);
    expect(form.config.columns).not.toContain("priority");
    expect(form.config.sort_by).toBe("id");
  });

  it("clamps max rows to the 1–100 backend range", async () => {
    const { onSubmit } = renderDialog();
    selectType(/Work items table/);
    fillTitle("Backlog");
    fireEvent.change(screen.getByLabelText(/Max rows/), {
      target: { value: "500" },
    });
    const form = await submit(onSubmit);
    expect(form.config.max_rows).toBe(100);
  });

  it("surfaces a server error on the dialog", () => {
    renderDialog({ serverError: "columns must not be empty" });
    selectType(/Work items table/);
    expect(screen.getByText("columns must not be empty")).toBeInTheDocument();
  });
});
