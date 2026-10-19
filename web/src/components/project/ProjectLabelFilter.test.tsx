// ProjectLabelFilter tests (C76T1): the Projects page header label
// filter — count badge, toggle semantics, per-section + global clear,
// and the unknown-id fallback row for stale pasted selections.
// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import ProjectLabelFilter from "./ProjectLabelFilter";
import { api } from "../../lib/api";
import type { ProjectLabel } from "../../lib/types";

vi.mock("../../lib/api", () => ({
  api: { get: vi.fn() },
  ApiError: class ApiError extends Error {
    status = 0;
  },
}));

const mockGet = vi.mocked(api.get);

afterEach(() => {
  vi.clearAllMocks();
});

const SLUG = "acme";
const A = "11111111-1111-1111-1111-111111111111";
const B = "22222222-2222-2222-2222-222222222222";
const STALE = "99999999-9999-9999-9999-999999999999";

const pl = (id: string, name: string, color = "#3b82f6"): ProjectLabel => ({
  id,
  name,
  color,
});

function renderFilter(props?: {
  selected?: string[];
  onChange?: (ids: string[]) => void;
  onClear?: () => void;
}) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  mockGet.mockImplementation((path: string) => {
    if (path === `/api/v1/workspaces/${SLUG}/project-labels`)
      return Promise.resolve({ project_labels: [pl(A, "platform"), pl(B, "mobile")] });
    throw new Error(`unexpected GET ${path}`);
  });
  const onChange = props?.onChange ?? vi.fn();
  const onClear = props?.onClear ?? vi.fn();
  render(
    <QueryClientProvider client={qc}>
      <ProjectLabelFilter
        slug={SLUG}
        selected={props?.selected ?? []}
        onChange={onChange}
        onClear={onClear}
      />
    </QueryClientProvider>,
  );
  return { onChange, onClear };
}

async function openPopover() {
  fireEvent.click(screen.getByRole("button", { name: /filter projects by label/i }));
  await screen.findByText("platform");
}

describe("ProjectLabelFilter", () => {
  it("shows no badge when nothing is selected", () => {
    renderFilter();
    expect(
      screen.queryByTestId("project-label-filter-count"),
    ).not.toBeInTheDocument();
  });

  it("shows the active-count badge", () => {
    renderFilter({ selected: [A, B] });
    expect(screen.getByTestId("project-label-filter-count")).toHaveTextContent("2");
  });

  it("toggling a label calls onChange with the toggled ids", async () => {
    const { onChange } = renderFilter({ selected: [A] });
    await openPopover();
    fireEvent.click(screen.getByText("mobile"));
    expect(onChange).toHaveBeenCalledWith([A, B]);
  });

  it("untoggling a selected label removes it", async () => {
    const { onChange } = renderFilter({ selected: [A, B] });
    await openPopover();
    fireEvent.click(screen.getByText("platform"));
    expect(onChange).toHaveBeenCalledWith([B]);
  });

  it("per-section Clear resets just the labels", async () => {
    const { onChange } = renderFilter({ selected: [A] });
    await openPopover();
    fireEvent.click(screen.getByRole("button", { name: "Clear labels filter" }));
    expect(onChange).toHaveBeenCalledWith([]);
  });

  it("Clear all calls the global onClear", async () => {
    const { onClear } = renderFilter({ selected: [A] });
    await openPopover();
    fireEvent.click(screen.getByRole("button", { name: /clear all/i }));
    expect(onClear).toHaveBeenCalledTimes(1);
  });

  it("renders a fallback row for a selected id that no longer exists", async () => {
    renderFilter({ selected: [STALE] });
    await openPopover();
    const popover = screen.getByRole("dialog");
    expect(within(popover).getByText("unknown")).toBeInTheDocument();
    // The stale selection stays clearable.
    expect(
      within(popover).getByText("99999999…", { exact: false }),
    ).toBeInTheDocument();
  });
});
