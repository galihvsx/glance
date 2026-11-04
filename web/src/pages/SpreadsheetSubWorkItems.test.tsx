// @vitest-environment jsdom
// C143T1: "Show sub-work items" in the Spreadsheet (Table) view —
// toggle persisted per-project in the spreadsheet's own localStorage
// key (default ON), include_subtasks=1 on BOTH fetch paths, nested
// rows via the shared splitNestedIssues helper, top-level-only header
// count, and "—" cells for everything a SubtaskRow does not carry.
import { act, renderHook } from "@testing-library/react";
import {
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import Spreadsheet from "./Spreadsheet";
import { useSpreadsheetSubWorkItems } from "../components/issue/useSpreadsheetSubWorkItems";
import type { Issue, SubtaskRow } from "../lib/types";

function mkIssue(over: Partial<Issue> & { id: string }): Issue {
  return {
    project_id: "proj-1",
    sequence_id: 1,
    display_id: "ENG-1",
    name: "test",
    priority: 0,
    state_id: "s",
    sort_order: 0,
    is_draft: false,
    created_by: "u",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    assignees: [],
    labels: [],
    ...over,
  };
}

const childA: SubtaskRow = {
  id: "c1",
  display_id: "ENG-2",
  title: "Child A",
  state_id: null,
  priority: null,
  assignee_ids: [],
  sort_order: 1,
};
const childB: SubtaskRow = {
  id: "c2",
  display_id: "ENG-3",
  title: "Child B",
  state_id: "s2",
  priority: 1,
  assignee_ids: ["u2"],
  sort_order: 2,
};
const grandchild: SubtaskRow = {
  id: "g1",
  display_id: "ENG-4",
  title: "Grandchild",
  state_id: "s",
  priority: 0,
  assignee_ids: [],
  sort_order: 1,
};

// Fetch order: parent, both children (as flat rows), grandchild,
// orphan. The parent's attachment lists Child B BEFORE Child A —
// the grid must still render them in sort_order ASC (A, then B).
const parent = mkIssue({
  id: "p1",
  sequence_id: 1,
  display_id: "ENG-1",
  name: "Parent",
  state_id: "s",
  priority: 2,
  subtasks: [childB, childA],
});
const childAIssue = mkIssue({
  id: "c1",
  sequence_id: 2,
  display_id: "ENG-2",
  name: "Child A",
  state_id: "s",
  subtasks: [grandchild],
});
const childBIssue = mkIssue({
  id: "c2",
  sequence_id: 3,
  display_id: "ENG-3",
  name: "Child B",
  state_id: "s2",
  priority: 1,
});
const grandchildIssue = mkIssue({
  id: "g1",
  sequence_id: 4,
  display_id: "ENG-4",
  name: "Grandchild",
  state_id: "s",
});
const orphanIssue = mkIssue({
  id: "o1",
  sequence_id: 5,
  display_id: "ENG-5",
  name: "Orphan",
  state_id: "s",
});
const issues = [parent, childAIssue, childBIssue, grandchildIssue, orphanIssue];

const states = [
  {
    id: "s",
    project_id: "proj-1",
    name: "Backlog",
    group: "backlog",
    color: "#999999",
    sequence: 0,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  },
  {
    id: "s2",
    project_id: "proj-1",
    name: "In Progress",
    group: "started",
    color: "#3b82f6",
    sequence: 1,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  },
];

const members = [{ id: "u2", name: "Ada", email: "ada@example.com", role: 1 }];

const customFields = [
  {
    id: "cf1",
    project_id: "proj-1",
    name: "Severity",
    field_type: "text",
    options: [],
    required: false,
    position: 0,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  },
];

const BASE = "/api/v1/workspaces/acme/projects/ENG";
const STORAGE_KEY = "glance:spreadsheet-sub-work-items:acme:ENG";

vi.mock("../lib/api", () => ({
  api: {
    get: vi.fn((path: string) => {
      if (path.includes("/states")) {
        return Promise.resolve({ states });
      }
      if (path.includes("/members")) {
        return Promise.resolve({ members });
      }
      if (path.includes("/labels")) {
        return Promise.resolve({ labels: [] });
      }
      if (path.includes("/estimates")) {
        return Promise.resolve({ estimates: [] });
      }
      if (path.includes("/custom-fields")) {
        return Promise.resolve({ custom_fields: customFields });
      }
      if (path.includes("/views")) {
        return Promise.resolve({ views: [] });
      }
      if (path.includes("/epics")) {
        return Promise.resolve({ epics: [] });
      }
      if (path.includes("include_custom=1")) {
        return Promise.resolve({ custom_values: {} });
      }
      if (path.includes("/issues?")) {
        return Promise.resolve({ results: issues, next_cursor: undefined });
      }
      if (path === BASE) {
        return Promise.resolve({
          id: "proj-1",
          identifier: "ENG",
          name: "Eng",
        });
      }
      return Promise.reject(new Error("unexpected path: " + path));
    }),
    post: vi.fn(),
    patch: vi.fn(),
    del: vi.fn(),
  },
  ApiError: class ApiError extends Error {
    code?: string;
    details?: unknown;
    constructor(status: number, body: string, code?: string, details?: unknown) {
      super(body || `Request failed with status ${status}`);
      this.name = "ApiError";
      this.code = code;
      this.details = details;
    }
  },
}));

vi.mock("../lib/auth", () => ({
  useAuth: () => ({
    user: { id: "u1", name: "U", email: "u@example.com" },
    loading: false,
  }),
}));

function renderSpreadsheet(
  initialEntry = "/w/acme/p/ENG/spreadsheet",
) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <QueryClientProvider client={client}>
        <Routes>
          <Route
            path="/w/:slug/p/:identifier/spreadsheet"
            element={<Spreadsheet />}
          />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

async function listCalls(): Promise<URLSearchParams[]> {
  const { api } = await import("../lib/api");
  const getMock = api.get as ReturnType<typeof vi.fn>;
  return getMock.mock.calls
    .map((args: unknown[]) => args[0] as string)
    .filter((p) => p.includes("/issues?"))
    .map((p) => new URLSearchParams(p.split("?")[1] ?? ""));
}

function rowLabels(): string[] {
  const table = screen.getByRole("table");
  return within(table)
    .getAllByRole("link")
    .map((row) => row.getAttribute("aria-label") ?? "");
}

async function openDisplayPanel(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("button", { name: "Display properties" }));
  // Each Switch sits inside its row <Label>, so the computed accessible
  // name concatenates the visible label and the switch's aria-label
  // (DisplayPanel.test.tsx precedent) — select switches by the
  // aria-label attribute the component sets instead.
  await screen.findByText("Sub-work items");
}

/** The switch element for a row, selected by its aria-label attribute. */
function switchByAriaLabel(label: string): HTMLElement {
  const el = document.querySelector(`[aria-label="${label}"]`);
  if (!el) throw new Error(`no switch with aria-label ${label}`);
  return el as HTMLElement;
}

beforeEach(() => {
  localStorage.clear();
  vi.clearAllMocks();
});

describe("useSpreadsheetSubWorkItems (C143T1)", () => {
  it("defaults to true", () => {
    const { result } = renderHook(() =>
      useSpreadsheetSubWorkItems("acme", "ENG"),
    );
    expect(result.current.showSubWorkItems).toBe(true);
  });

  it("persists a toggled-off value and reads it back on remount", () => {
    const { result, unmount } = renderHook(() =>
      useSpreadsheetSubWorkItems("acme", "ENG"),
    );
    act(() => result.current.toggle());
    expect(result.current.showSubWorkItems).toBe(false);
    expect(localStorage.getItem(STORAGE_KEY)).toBe("false");
    unmount();
    const { result: remounted } = renderHook(() =>
      useSpreadsheetSubWorkItems("acme", "ENG"),
    );
    expect(remounted.current.showSubWorkItems).toBe(false);
  });

  it("reset restores true and clears the key", () => {
    localStorage.setItem(STORAGE_KEY, "false");
    const { result } = renderHook(() =>
      useSpreadsheetSubWorkItems("acme", "ENG"),
    );
    expect(result.current.showSubWorkItems).toBe(false);
    act(() => result.current.reset());
    expect(result.current.showSubWorkItems).toBe(true);
    expect(localStorage.getItem(STORAGE_KEY)).toBeNull();
  });
});

describe("Spreadsheet sub-work items (C143T1)", () => {
  it("toggle off renders today's flat grid: no flag, no nested rows", async () => {
    localStorage.setItem(STORAGE_KEY, "false");
    renderSpreadsheet();
    await screen.findByRole("button", { name: "Sort by Cycle" });

    const calls = await listCalls();
    expect(calls.length).toBeGreaterThanOrEqual(1);
    expect(calls[0].get("include_subtasks")).toBeNull();

    // Every fetched issue is a top-level row, in fetch order.
    expect(rowLabels()).toEqual([
      expect.stringContaining("ENG-1"),
      expect.stringContaining("ENG-2"),
      expect.stringContaining("ENG-3"),
      expect.stringContaining("ENG-4"),
      expect.stringContaining("ENG-5"),
    ]);
    expect(
      document.querySelectorAll("[data-nested-subtask]"),
    ).toHaveLength(0);
    expect(within(screen.getByLabelText("Breadcrumb")).getByText("5")).toBeInTheDocument();
  });

  it("toggle on passes include_subtasks=1 and nests rows in sort_order ASC", async () => {
    renderSpreadsheet();
    await screen.findByRole("button", { name: "Sort by Cycle" });

    const calls = await listCalls();
    expect(calls[0].get("include_subtasks")).toBe("1");

    // Children nest under the parent (A before B despite the
    // attachment order); they no longer appear as top-level rows.
    const nested = document.querySelectorAll("[data-nested-subtask]");
    expect([...nested].map((el) => el.getAttribute("data-nested-subtask"))).toEqual([
      "c1",
      "c2",
    ]);
    expect(rowLabels()).toEqual([
      expect.stringContaining("ENG-1"),
      expect.stringContaining("ENG-2"),
      expect.stringContaining("ENG-3"),
      expect.stringContaining("ENG-4"),
      expect.stringContaining("ENG-5"),
    ]);
    // Header count is top-level only: parent + grandchild + orphan.
    expect(within(screen.getByLabelText("Breadcrumb")).getByText("3")).toBeInTheDocument();
  });

  it("toggling on refetches with the flag and renders nested rows", async () => {
    localStorage.setItem(STORAGE_KEY, "false");
    renderSpreadsheet();
    await screen.findByRole("button", { name: "Sort by Cycle" });
    expect(
      document.querySelectorAll("[data-nested-subtask]"),
    ).toHaveLength(0);

    const user = userEvent.setup();
    await openDisplayPanel(user);
    await user.click(switchByAriaLabel("Show sub-work items"));

    await waitFor(() => {
      expect(
        document.querySelectorAll("[data-nested-subtask]"),
      ).toHaveLength(2);
    });
    const calls = await listCalls();
    expect(calls.some((p) => p.get("include_subtasks") === "1")).toBe(true);
    expect(localStorage.getItem(STORAGE_KEY)).toBe("true");
  });

  it("PQL mode passes the flag and nests rows the same way", async () => {
    renderSpreadsheet("/w/acme/p/ENG/spreadsheet?pql=priority+%3D+High");
    await screen.findByRole("button", { name: "Sort by Cycle" });

    const calls = await listCalls();
    const pqlCalls = calls.filter((p) => p.has("pql"));
    expect(pqlCalls.length).toBeGreaterThanOrEqual(1);
    expect(pqlCalls[0].get("include_subtasks")).toBe("1");
    expect(
      document.querySelectorAll("[data-nested-subtask]"),
    ).toHaveLength(2);
    expect(within(screen.getByLabelText("Breadcrumb")).getByText("3")).toBeInTheDocument();
  });

  it("under a column sort, parents reorder but children stay attached in stack order", async () => {
    renderSpreadsheet();
    await screen.findByRole("button", { name: "Sort by Cycle" });
    const user = userEvent.setup();

    // Top-level names asc: Grandchild, Orphan, Parent.
    await user.click(screen.getByRole("button", { name: "Sort by Title" }));
    const labels = rowLabels();
    expect(labels[0]).toMatch(/ENG-4/);
    expect(labels[1]).toMatch(/ENG-5/);
    expect(labels[2]).toMatch(/ENG-1/);
    // Children travel with the parent, still A then B.
    expect(labels[3]).toMatch(/ENG-2/);
    expect(labels[4]).toMatch(/ENG-3/);
  });

  it("orphan and grandchild rows stay flat", async () => {
    renderSpreadsheet();
    await screen.findByRole("button", { name: "Sort by Cycle" });

    // Grandchild is nested under Child A in the attachment, but Child
    // A is itself nested — so the grandchild stays a top-level row.
    const grandchildRow = screen.getByRole("link", {
      name: /Open issue ENG-4/,
    });
    expect(grandchildRow).not.toHaveAttribute("data-nested-subtask");
    // Orphan has no fetched parent — also a top-level row.
    const orphanRow = screen.getByRole("link", {
      name: /Open issue ENG-5/,
    });
    expect(orphanRow).not.toHaveAttribute("data-nested-subtask");
    // No row ever vanishes: 3 top-level + 2 nested = 5 links.
    expect(rowLabels()).toHaveLength(5);
  });

  it("nested cells: state dot + name, priority bars, glyphs; the rest '—'", async () => {
    renderSpreadsheet();
    await screen.findByRole("button", { name: "Sort by Cycle" });

    // Child A carries no state, priority, or assignees: every
    // non-ID/title cell is an em dash (state, priority, assignees,
    // labels, start, target, created, updated, cycle, module, release).
    const childARow = screen.getByRole("link", {
      name: "Open issue ENG-2: Child A",
    });
    expect(within(childARow).getByText("ENG-2")).toBeInTheDocument();
    expect(within(childARow).getByText("Child A")).toBeInTheDocument();
    expect(within(childARow).getAllByText("—")).toHaveLength(11);

    // Child B carries a state, a priority, and one assignee.
    const childBRow = screen.getByRole("link", {
      name: "Open issue ENG-3: Child B",
    });
    expect(within(childBRow).getByText("In Progress")).toBeInTheDocument();
    expect(
      within(childBRow).getByLabelText("Priority: Low"),
    ).toBeInTheDocument();
    expect(within(childBRow).getByLabelText("1 assignee")).toBeInTheDocument();
    // The carried columns are filled, so only the 8 non-carried
    // columns (labels, dates, created, updated, cycle, module,
    // release) are dashes — plus no state/priority/assignee dashes.
    expect(within(childBRow).getAllByText("—")).toHaveLength(8);
  });

  it("nested rows are NOT inline-editable and carry no checkbox", async () => {
    renderSpreadsheet();
    await screen.findByRole("button", { name: "Sort by Cycle" });

    const childARow = screen.getByRole("link", {
      name: "Open issue ENG-2: Child A",
    });
    expect(
      within(childARow).queryByTitle("Click to edit state"),
    ).toBeNull();
    expect(
      within(childARow).queryByTitle("Click to edit priority"),
    ).toBeNull();
    expect(
      within(childARow).queryByTitle("Click to edit start date"),
    ).toBeNull();
    expect(
      within(childARow).queryByTitle("Click to edit target date"),
    ).toBeNull();
    expect(within(childARow).queryByRole("checkbox")).toBeNull();
  });

  it("a custom-field column shows '—' on nested rows", async () => {
    renderSpreadsheet();
    await screen.findByRole("button", { name: "Sort by Cycle" });
    const user = userEvent.setup();

    await openDisplayPanel(user);
    await user.click(switchByAriaLabel("Show Severity column"));

    // The custom column header appears and the nested row gains one
    // more dash (11 → 12 for the empty child).
    const childARow = screen.getByRole("link", {
      name: "Open issue ENG-2: Child A",
    });
    await waitFor(() => {
      expect(within(childARow).getAllByText("—")).toHaveLength(12);
    });
  });
});
