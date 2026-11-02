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
import type { Cycle, Epic, Issue, Member, Module, Release } from "../../lib/types";
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
  cycles?: Cycle[];
  modules?: Module[];
  releases?: Release[];
  members?: Member[];
  missingParents?: Issue[];
}) {
  const filters = opts.filters ?? EMPTY_FILTERS;
  const onChange = vi.fn();
  const onClear = vi.fn();
  const searchResults = opts.searchResults ?? [];
  const seeds: [readonly unknown[], unknown][] = [
    [["states", "acme", "ENG"], []],
    [["members", "acme"], opts.members ?? []],
    [["labels", "acme", "ENG"], []],
    [["estimates", "acme", "ENG"], []],
    [
      ["epics", "acme", "ENG", "status=active&status=archived"],
      opts.epics ?? [],
    ],
    [["cycles", "acme", "ENG"], opts.cycles ?? []],
    [["modules", "acme", "ENG"], opts.modules ?? []],
    [["releases", "acme", "ENG"], opts.releases ?? []],
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

describe("Cycle filter section (C116T1)", () => {
  const C_CURRENT = "123e4567-e89b-12d3-a456-426614174010";
  const C_UPCOMING = "123e4567-e89b-12d3-a456-426614174011";
  const C_COMPLETED = "123e4567-e89b-12d3-a456-426614174012";
  const UNKNOWN_CYCLE = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa";
  const cycle = (
    id: string,
    name: string,
    status: Cycle["status"],
    start: string,
  ): Cycle => ({
    id,
    project_id: "p1",
    name,
    start_date: start,
    end_date: "2026-12-31",
    status,
    progress_snapshot: {},
    created_at: "",
    updated_at: "",
  });
  const cycleCurrent = cycle(C_CURRENT, "Sprint 3", "current", "2026-11-01");
  const cycleUpcoming = cycle(C_UPCOMING, "Sprint 4", "upcoming", "2026-12-01");
  const cycleCompleted = cycle(C_COMPLETED, "Sprint 2", "completed", "2026-10-01");

  it("renders the sentinel row and the project cycles with status hints", () => {
    renderPanel({ cycles: [cycleCurrent, cycleUpcoming] });
    expect(screen.getByText("No cycle")).toBeInTheDocument();
    expect(screen.getByText("Sprint 3")).toBeInTheDocument();
    expect(screen.getByText("Sprint 4")).toBeInTheDocument();
    expect(screen.getByText("current")).toBeInTheDocument();
    expect(screen.getByText("upcoming")).toBeInTheDocument();
  });

  it("sorts live cycles first (current, upcoming, completed), then by name", () => {
    renderPanel({
      cycles: [cycleCompleted, cycleUpcoming, cycleCurrent],
    });
    const current = screen.getByText("Sprint 3");
    const upcoming = screen.getByText("Sprint 4");
    const completed = screen.getByText("Sprint 2");
    expect(
      current.compareDocumentPosition(upcoming) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(
      upcoming.compareDocumentPosition(completed) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });

  it("toggling 'No cycle' and a cycle calls onChange with the right patch", () => {
    const { onChange } = renderPanel({ cycles: [cycleCurrent] });
    fireEvent.click(screen.getByText("No cycle"));
    expect(onChange).toHaveBeenCalledWith({ cycles: ["none"] });
    fireEvent.click(screen.getByText("Sprint 3"));
    expect(onChange).toHaveBeenCalledWith({ cycles: [C_CURRENT] });
  });

  it("selected cycles render checked and per-section Clear resets them", () => {
    const { onChange } = renderPanel({
      filters: { ...EMPTY_FILTERS, cycles: [C_CURRENT] },
      cycles: [cycleCurrent],
    });
    expect(isChecked("Sprint 3")).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "Clear Cycle filter" }));
    expect(onChange).toHaveBeenCalledWith({ cycles: [] });
  });

  it("renders a clearable fallback row for an unknown cycle id", () => {
    const { onChange } = renderPanel({
      filters: { ...EMPTY_FILTERS, cycles: [UNKNOWN_CYCLE] },
      cycles: [],
    });
    expect(screen.getByText("aaaaaaaa…")).toBeInTheDocument();
    fireEvent.click(screen.getByText("aaaaaaaa…"));
    expect(onChange).toHaveBeenCalledWith({ cycles: [] });
  });

  it("shows an empty state when the project has no cycles", () => {
    renderPanel({ cycles: [] });
    expect(screen.getByText("No cycles in this project.")).toBeInTheDocument();
  });

  it("bumps the filter button badge count", () => {
    renderPanel({ filters: { ...EMPTY_FILTERS, cycles: [C_CURRENT] } });
    expect(screen.getByRole("button", { name: /^filter/i })).toHaveTextContent(
      "1",
    );
  });
});

describe("Module filter section (C116T1)", () => {
  const M_ACTIVE = "123e4567-e89b-12d3-a456-426614174020";
  const M_ARCHIVED = "123e4567-e89b-12d3-a456-426614174021";
  const UNKNOWN_MODULE = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb";
  const mod = (
    id: string,
    name: string,
    status: Module["status"],
    count: number,
  ): Module => ({
    id,
    project_id: "p1",
    name,
    status,
    issue_count: count,
    created_at: "",
    updated_at: "",
  });
  const moduleActive = mod(M_ACTIVE, "Auth", "active", 5);
  const moduleArchived = mod(M_ARCHIVED, "Legacy", "archived", 0);

  it("renders the sentinel row and the project modules with hints", () => {
    renderPanel({ modules: [moduleActive, moduleArchived] });
    expect(screen.getByText("No module")).toBeInTheDocument();
    expect(screen.getByText("Auth")).toBeInTheDocument();
    expect(screen.getByText("5 issues")).toBeInTheDocument();
    expect(screen.getByText("Legacy")).toBeInTheDocument();
    expect(screen.getByText("archived")).toBeInTheDocument();
  });

  it("sorts archived modules last", () => {
    renderPanel({ modules: [moduleArchived, moduleActive] });
    const active = screen.getByText("Auth");
    const archived = screen.getByText("Legacy");
    expect(
      active.compareDocumentPosition(archived) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });

  it("toggling 'No module' and a module calls onChange with the right patch", () => {
    const { onChange } = renderPanel({ modules: [moduleActive] });
    fireEvent.click(screen.getByText("No module"));
    expect(onChange).toHaveBeenCalledWith({ modules: ["none"] });
    fireEvent.click(screen.getByText("Auth"));
    expect(onChange).toHaveBeenCalledWith({ modules: [M_ACTIVE] });
  });

  it("selected modules render checked and per-section Clear resets them", () => {
    const { onChange } = renderPanel({
      filters: { ...EMPTY_FILTERS, modules: [M_ACTIVE] },
      modules: [moduleActive],
    });
    expect(isChecked("Auth")).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "Clear Module filter" }));
    expect(onChange).toHaveBeenCalledWith({ modules: [] });
  });

  it("renders a clearable fallback row for an unknown module id", () => {
    const { onChange } = renderPanel({
      filters: { ...EMPTY_FILTERS, modules: [UNKNOWN_MODULE] },
      modules: [],
    });
    expect(screen.getByText("bbbbbbbb…")).toBeInTheDocument();
    fireEvent.click(screen.getByText("bbbbbbbb…"));
    expect(onChange).toHaveBeenCalledWith({ modules: [] });
  });

  it("shows an empty state when the project has no modules", () => {
    renderPanel({ modules: [] });
    expect(
      screen.getByText("No modules in this project."),
    ).toBeInTheDocument();
  });

  it("counts cycle + module as two badge dimensions", () => {
    renderPanel({
      filters: {
        ...EMPTY_FILTERS,
        cycles: ["123e4567-e89b-12d3-a456-426614174010"],
        modules: [M_ACTIVE],
      },
    });
    expect(screen.getByRole("button", { name: /^filter/i })).toHaveTextContent(
      "2",
    );
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

describe("Release filter section (C117T1)", () => {
  const R_PLANNED = "123e4567-e89b-12d3-a456-426614174030";
  const R_RELEASED = "123e4567-e89b-12d3-a456-426614174031";
  const UNKNOWN_RELEASE = "cccccccc-cccc-cccc-cccc-cccccccccccc";
  const rel = (
    id: string,
    name: string,
    status: Release["status"],
    count: number,
  ): Release => ({
    id,
    project_id: "p1",
    name,
    description: "",
    status,
    release_date: null,
    issue_count: count,
    progress_snapshot: {},
    created_at: "",
    updated_at: "",
  });
  const releasePlanned = rel(R_PLANNED, "v2.0", "planned", 7);
  const releaseReleased = rel(R_RELEASED, "v1.0", "released", 12);

  it("renders the sentinel row and the project releases with hints", () => {
    renderPanel({ releases: [releasePlanned, releaseReleased] });
    expect(screen.getByText("No release")).toBeInTheDocument();
    expect(screen.getByText("v2.0")).toBeInTheDocument();
    expect(screen.getByText("7 issues")).toBeInTheDocument();
    expect(screen.getByText("v1.0")).toBeInTheDocument();
    expect(screen.getByText("released")).toBeInTheDocument();
  });

  it("follows server order (no invented client sort)", () => {
    renderPanel({ releases: [releaseReleased, releasePlanned] });
    const released = screen.getByText("v1.0");
    const planned = screen.getByText("v2.0");
    expect(
      released.compareDocumentPosition(planned) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });

  it("toggling 'No release' and a release calls onChange with the right patch", () => {
    const { onChange } = renderPanel({ releases: [releasePlanned] });
    fireEvent.click(screen.getByText("No release"));
    expect(onChange).toHaveBeenCalledWith({ releases: ["none"] });
    fireEvent.click(screen.getByText("v2.0"));
    expect(onChange).toHaveBeenCalledWith({ releases: [R_PLANNED] });
  });

  it("selected releases render checked and per-section Clear resets them", () => {
    const { onChange } = renderPanel({
      filters: { ...EMPTY_FILTERS, releases: [R_PLANNED] },
      releases: [releasePlanned],
    });
    expect(isChecked("v2.0")).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "Clear Release filter" }));
    expect(onChange).toHaveBeenCalledWith({ releases: [] });
  });

  it("renders a clearable fallback row for an unknown release id", () => {
    const { onChange } = renderPanel({
      filters: { ...EMPTY_FILTERS, releases: [UNKNOWN_RELEASE] },
      releases: [],
    });
    expect(screen.getByText("cccccccc…")).toBeInTheDocument();
    fireEvent.click(screen.getByText("cccccccc…"));
    expect(onChange).toHaveBeenCalledWith({ releases: [] });
  });

  it("shows an empty state when the project has no releases", () => {
    renderPanel({ releases: [] });
    expect(
      screen.getByText("No releases in this project."),
    ).toBeInTheDocument();
  });

  it("bumps the filter button badge count", () => {
    renderPanel({ filters: { ...EMPTY_FILTERS, releases: [R_PLANNED] } });
    expect(screen.getByRole("button", { name: /^filter/i })).toHaveTextContent(
      "1",
    );
  });

  it("sits after the Module section", () => {
    renderPanel({ modules: [], releases: [releasePlanned] });
    const moduleHeader = screen.getByText("Module");
    const releaseHeader = screen.getByText("Release");
    expect(
      moduleHeader.compareDocumentPosition(releaseHeader) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });
});

describe("Created by filter section (C118T1)", () => {
  const A_ALICE = "123e4567-e89b-12d3-a456-426614174050";
  const A_BOB = "123e4567-e89b-12d3-a456-426614174051";
  const UNKNOWN_AUTHOR = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa";

  function member(id: string, name: string | null, email: string): Member {
    return { id, name, email, role: 1 };
  }

  const alice = member(A_ALICE, "Alice", "alice@example.com");
  const bob = member(A_BOB, null, "bob@example.com");

  /** The Created-by list div: the sibling after the "Created by" header. */
  function createdByList(): HTMLElement {
    const header = screen.getByText("Created by");
    const list = header.closest("div")?.nextElementSibling;
    expect(list).not.toBeNull();
    return list as HTMLElement;
  }

  function isCheckedIn(list: HTMLElement, labelText: string): boolean {
    const row = Array.from(list.querySelectorAll("label")).find(
      (l) => l.textContent?.includes(labelText),
    );
    expect(row).not.toBeUndefined();
    return row!.querySelector('[aria-checked="true"]') !== null;
  }

  it("renders member rows with name, falling back to email", () => {
    renderPanel({ members: [alice, bob] });
    const list = createdByList();
    expect(list.textContent).toContain("Alice");
    expect(list.textContent).toContain("bob@example.com");
  });

  it("has no none row: no 'No author' row, and 'Unassigned' belongs only to Assignees", () => {
    renderPanel({ members: [alice] });
    expect(screen.queryByText("No author")).toBeNull();
    expect(screen.getAllByText("Unassigned")).toHaveLength(1);
    expect(createdByList().textContent).not.toContain("Unassigned");
  });

  it("toggling a member in this section calls onChange with createdBy", () => {
    const { onChange } = renderPanel({ members: [alice, bob] });
    const list = createdByList();
    fireEvent.click(
      Array.from(list.querySelectorAll("label")).find(
        (l) => l.textContent?.includes("Alice"),
      )!,
    );
    expect(onChange).toHaveBeenCalledWith({ createdBy: [A_ALICE] });
  });

  it("selected authors render checked and per-section Clear resets them", () => {
    const { onChange } = renderPanel({
      filters: { ...EMPTY_FILTERS, createdBy: [A_ALICE] },
      members: [alice, bob],
    });
    expect(isCheckedIn(createdByList(), "Alice")).toBe(true);
    fireEvent.click(
      screen.getByRole("button", { name: "Clear Created by filter" }),
    );
    expect(onChange).toHaveBeenCalledWith({ createdBy: [] });
  });

  it("hides the per-section Clear when the section is inactive", () => {
    renderPanel({ members: [alice] });
    expect(
      screen.queryByRole("button", { name: "Clear Created by filter" }),
    ).toBeNull();
  });

  it("renders a clearable fallback row for an unknown author id", () => {
    const { onChange } = renderPanel({
      filters: { ...EMPTY_FILTERS, createdBy: [UNKNOWN_AUTHOR] },
      members: [],
    });
    const list = createdByList();
    expect(list.textContent).toContain("aaaaaaaa…");
    fireEvent.click(
      Array.from(list.querySelectorAll("label")).find(
        (l) => l.textContent?.includes("aaaaaaaa"),
      )!,
    );
    expect(onChange).toHaveBeenCalledWith({ createdBy: [] });
  });

  it("bumps the filter button badge count", () => {
    renderPanel({
      filters: { ...EMPTY_FILTERS, createdBy: [A_ALICE] },
      members: [alice],
    });
    expect(screen.getByRole("button", { name: /^filter/i })).toHaveTextContent(
      "1",
    );
  });

  it("sits after the Assignees section", () => {
    renderPanel({ members: [alice] });
    const assigneesHeader = screen.getByText("Assignees");
    const createdByHeader = screen.getByText("Created by");
    expect(
      assigneesHeader.compareDocumentPosition(createdByHeader) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });
});
