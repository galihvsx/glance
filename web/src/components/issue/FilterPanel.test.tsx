// @vitest-environment jsdom
// C75T1: FilterPanel behavior for the new Parent and Epic filter
// dimensions — sentinel rows, option lists, toggling, per-section clear,
// parent search, and unknown-id fallback rows.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import FilterPanel from "./FilterPanel";
import { EMPTY_FILTERS, type IssueFilters } from "../../lib/filters";
import type { Epic, Issue } from "../../lib/types";
import {
  makeQueryClient,
  stubMatchMedia,
} from "../shell/test-utils";

const P1 = "123e4567-e89b-12d3-a456-426614174000";
const E1 = "123e4567-e89b-12d3-a456-426614174001";
const UNKNOWN_PARENT = "deadbeef-dead-beef-dead-deadbeefdead";
const UNKNOWN_EPIC = "feedface-feed-face-feed-feedfacefeed";

function issue(id: string, displayId: string, name: string): Issue {
  return {
    id,
    project_id: "p1",
    sequence_id: 1,
    display_id: displayId,
    name,
    priority: 0,
    state_id: "s1",
    sort_order: 0,
    is_draft: false,
    created_by: "u1",
    created_at: "",
    updated_at: "",
    assignees: [],
    labels: [],
  };
}

const parentIssue = issue(P1, "ENG-42", "Parent task");
const otherIssue = issue(
  "123e4567-e89b-12d3-a456-426614174099",
  "ENG-43",
  "Login bug",
);

const epicActive: Epic = {
  id: E1,
  project_id: "p1",
  name: "Auth revamp",
  archived_at: null,
  issue_count: 3,
  created_at: "",
  updated_at: "",
};
const epicArchived: Epic = {
  id: "123e4567-e89b-12d3-a456-426614174002",
  project_id: "p1",
  name: "Old platform",
  archived_at: "2026-09-01T00:00:00Z",
  issue_count: 0,
  created_at: "",
  updated_at: "",
};

function renderPanel(opts: {
  filters?: IssueFilters;
  searchResults?: Issue[];
  /** Debounced-search result sets keyed by the search text. */
  searchByQuery?: Record<string, Issue[]>;
  epics?: Epic[];
  missingParents?: Issue[];
}) {
  const filters = opts.filters ?? EMPTY_FILTERS;
  const onChange = vi.fn();
  const onClear = vi.fn();
  const searchResults = opts.searchResults ?? [];
  const seeds: [readonly unknown[], unknown][] = [
    [["states", "acme", "ENG"], []],
    [["members", "acme"], []],
    [["labels", "acme", "ENG"], []],
    [["estimates", "acme", "ENG"], []],
    [
      ["epics", "acme", "ENG", "status=active&status=archived"],
      opts.epics ?? [],
    ],
    [["parent-filter-search", "acme", "ENG", ""], searchResults],
  ];
  for (const [q, results] of Object.entries(opts.searchByQuery ?? {})) {
    seeds.push([["parent-filter-search", "acme", "ENG", q], results]);
  }
  // Seed the selected-parents resolution key so the test stays
  // deterministic (no live fetch).
  const missing = (filters.parents ?? [])
    .filter((id) => id !== "none")
    .filter((id) => !searchResults.some((r) => r.id === id))
    .sort();
  if (missing.length > 0) {
    seeds.push([
      ["parent-filter-missing", "acme", "ENG", ...missing],
      opts.missingParents ?? [],
    ]);
  }
  const qc = makeQueryClient(seeds);
  render(
    <QueryClientProvider client={qc}>
      <FilterPanel
        slug="acme"
        identifier="ENG"
        filters={filters}
        onChange={onChange}
        onClear={onClear}
      />
    </QueryClientProvider>,
  );
  fireEvent.click(screen.getByRole("button", { name: /^filter/i }));
  return { onChange, onClear };
}

function isChecked(labelText: string): boolean {
  const row = screen.getByText(labelText).closest("label");
  expect(row).not.toBeNull();
  return row!.querySelector('[aria-checked="true"]') !== null;
}

beforeEach(() => {
  stubMatchMedia(false);
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.useRealTimers();
});

describe("Parent filter section", () => {
  it("renders the sentinel row and searchable issue options", () => {
    renderPanel({ searchResults: [parentIssue], epics: [] });
    expect(
      screen.getByText("No parent (top-level)"),
    ).toBeInTheDocument();
    expect(screen.getByText("ENG-42")).toBeInTheDocument();
    expect(screen.getByText("Parent task")).toBeInTheDocument();
    expect(
      screen.getByLabelText("Search parent issues"),
    ).toBeInTheDocument();
  });

  it("toggling 'No parent' adds the 'none' sentinel", () => {
    const { onChange } = renderPanel({ searchResults: [] });
    fireEvent.click(screen.getByText("No parent (top-level)"));
    expect(onChange).toHaveBeenCalledWith({ parents: ["none"] });
  });

  it("toggling an issue adds its id", () => {
    const { onChange } = renderPanel({ searchResults: [parentIssue] });
    fireEvent.click(screen.getByText("Parent task"));
    expect(onChange).toHaveBeenCalledWith({ parents: [P1] });
  });

  it("selected parents render checked and per-section Clear resets them", () => {
    const { onChange } = renderPanel({
      filters: { ...EMPTY_FILTERS, parents: ["none", P1] },
      searchResults: [parentIssue],
    });
    expect(isChecked("No parent (top-level)")).toBe(true);
    expect(isChecked("Parent task")).toBe(true);
    fireEvent.click(
      screen.getByRole("button", { name: "Clear Parent filter" }),
    );
    expect(onChange).toHaveBeenCalledWith({ parents: [] });
  });

  it("hides the per-section Clear when the section is inactive", () => {
    renderPanel({ searchResults: [] });
    expect(
      screen.queryByRole("button", { name: "Clear Parent filter" }),
    ).toBeNull();
  });

  it("search narrows the parent issue list", () => {
    vi.useFakeTimers();
    renderPanel({
      searchResults: [parentIssue],
      searchByQuery: { login: [otherIssue] },
    });
    const input = screen.getByLabelText("Search parent issues");
    fireEvent.change(input, { target: { value: "login" } });
    act(() => {
      vi.advanceTimersByTime(300);
    });
    expect(screen.getByText("Login bug")).toBeInTheDocument();
    expect(screen.queryByText("Parent task")).toBeNull();
  });

  it("renders a clearable fallback row for an unknown parent id", () => {
    const { onChange } = renderPanel({
      filters: { ...EMPTY_FILTERS, parents: [UNKNOWN_PARENT] },
      searchResults: [],
    });
    // Truncated id so the stale selection stays visible and clearable.
    expect(screen.getByText("deadbeef…")).toBeInTheDocument();
    expect(screen.getByText("unknown")).toBeInTheDocument();
    fireEvent.click(screen.getByText("deadbeef…"));
    expect(onChange).toHaveBeenCalledWith({ parents: [] });
  });

  it("shows an empty state when the project has no issues", () => {
    renderPanel({ searchResults: [] });
    expect(
      screen.getByText("No issues in this project."),
    ).toBeInTheDocument();
  });
});

describe("Epic filter section", () => {
  it("renders the sentinel row and the project epics with hints", () => {
    renderPanel({ epics: [epicActive, epicArchived] });
    expect(screen.getByText("No epic")).toBeInTheDocument();
    expect(screen.getByText("Auth revamp")).toBeInTheDocument();
    expect(screen.getByText("3 issues")).toBeInTheDocument();
    expect(screen.getByText("Old platform")).toBeInTheDocument();
    expect(screen.getByText("archived")).toBeInTheDocument();
  });

  it("toggling 'No epic' and an epic calls onChange with the right patch", () => {
    const { onChange } = renderPanel({ epics: [epicActive] });
    fireEvent.click(screen.getByText("No epic"));
    expect(onChange).toHaveBeenCalledWith({ epics: ["none"] });
    fireEvent.click(screen.getByText("Auth revamp"));
    expect(onChange).toHaveBeenCalledWith({ epics: [E1] });
  });

  it("selected epics render checked and per-section Clear resets them", () => {
    const { onChange } = renderPanel({
      filters: { ...EMPTY_FILTERS, epics: [E1] },
      epics: [epicActive],
    });
    expect(isChecked("Auth revamp")).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "Clear Epic filter" }));
    expect(onChange).toHaveBeenCalledWith({ epics: [] });
  });

  it("renders a clearable fallback row for an unknown epic id", () => {
    const { onChange } = renderPanel({
      filters: { ...EMPTY_FILTERS, epics: [UNKNOWN_EPIC] },
      epics: [],
    });
    expect(screen.getByText("feedface…")).toBeInTheDocument();
    fireEvent.click(screen.getByText("feedface…"));
    expect(onChange).toHaveBeenCalledWith({ epics: [] });
  });

  it("shows an empty state when the project has no epics", () => {
    renderPanel({ epics: [] });
    expect(screen.getByText("No epics in this project.")).toBeInTheDocument();
  });
});

describe("existing dimensions", () => {
  it("keeps per-section Clear on the older dimensions too", () => {
    const { onChange } = renderPanel({
      filters: {
        ...EMPTY_FILTERS,
        priorities: [1],
        labels: ["label-1"],
        createdAfter: "2026-10-01",
      },
    });
    fireEvent.click(
      screen.getByRole("button", { name: "Clear Priority filter" }),
    );
    expect(onChange).toHaveBeenCalledWith({ priorities: [] });
    fireEvent.click(
      screen.getByRole("button", { name: "Clear Labels filter" }),
    );
    expect(onChange).toHaveBeenCalledWith({ labels: [] });
    fireEvent.click(screen.getByRole("button", { name: "Clear Dates filter" }));
    expect(onChange).toHaveBeenCalledWith({
      createdAfter: "",
      createdBefore: "",
      updatedAfter: "",
      updatedBefore: "",
      dueAfter: "",
      dueBefore: "",
      dueDateEmpty: false,
    });
  });

  it("keeps the global Clear all button", () => {
    const { onClear } = renderPanel({
      filters: { ...EMPTY_FILTERS, parents: [P1] },
    });
    fireEvent.click(screen.getByRole("button", { name: /clear all/i }));
    expect(onClear).toHaveBeenCalled();
  });
});

describe("No due date is-empty chip (C80T1)", () => {
  it("renders the chip in the Dates section", () => {
    renderPanel({});
    expect(screen.getByText("No due date")).toBeInTheDocument();
  });

  it("toggling the chip on sets dueDateEmpty and clears the due range", () => {
    const { onChange } = renderPanel({
      filters: { ...EMPTY_FILTERS, dueAfter: "2026-10-05", dueBefore: "2026-10-09" },
    });
    fireEvent.click(screen.getByText("No due date"));
    expect(onChange).toHaveBeenCalledWith({
      dueDateEmpty: true,
      dueAfter: "",
      dueBefore: "",
    });
  });

  it("toggling the chip off only unsets dueDateEmpty", () => {
    const { onChange } = renderPanel({
      filters: { ...EMPTY_FILTERS, dueDateEmpty: true },
    });
    fireEvent.click(screen.getByText("No due date"));
    expect(onChange).toHaveBeenCalledWith({ dueDateEmpty: false });
  });

  it("shows the chip checked and the Dates section active while on", () => {
    renderPanel({ filters: { ...EMPTY_FILTERS, dueDateEmpty: true } });
    expect(isChecked("No due date")).toBe(true);
    // Active-filter display: per-section Clear appears for Dates.
    expect(
      screen.getByRole("button", { name: "Clear Dates filter" }),
    ).toBeInTheDocument();
  });

  it("bumps the filter button badge count", () => {
    renderPanel({ filters: { ...EMPTY_FILTERS, dueDateEmpty: true } });
    // The trigger badge counts active dimensions — one for due+chip.
    expect(screen.getByRole("button", { name: /^filter/i })).toHaveTextContent("1");
  });

  it("picking a due range date clears the chip (mutual exclusion)", () => {
    const { onChange } = renderPanel({
      filters: { ...EMPTY_FILTERS, dueDateEmpty: true },
    });
    fireEvent.change(screen.getByLabelText("Due after"), {
      target: { value: "2026-10-05" },
    });
    expect(onChange).toHaveBeenCalledWith({
      dueAfter: "2026-10-05",
      dueDateEmpty: false,
    });
  });

  it("picking a due-before date also clears the chip", () => {
    const { onChange } = renderPanel({
      filters: { ...EMPTY_FILTERS, dueDateEmpty: true },
    });
    fireEvent.change(screen.getByLabelText("Due before"), {
      target: { value: "2026-10-09" },
    });
    expect(onChange).toHaveBeenCalledWith({
      dueBefore: "2026-10-09",
      dueDateEmpty: false,
    });
  });

  it("per-section Clear Dates resets the chip", () => {
    const { onChange } = renderPanel({
      filters: { ...EMPTY_FILTERS, dueDateEmpty: true },
    });
    fireEvent.click(screen.getByRole("button", { name: "Clear Dates filter" }));
    expect(onChange).toHaveBeenCalledWith({
      createdAfter: "",
      createdBefore: "",
      updatedAfter: "",
      updatedBefore: "",
      dueAfter: "",
      dueBefore: "",
      dueDateEmpty: false,
    });
  });
});
