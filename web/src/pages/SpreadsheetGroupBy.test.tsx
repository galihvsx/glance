// @vitest-environment jsdom
// C147T1: spreadsheet group-by — the grid partitions its already
// column-sorted top-level rows via the Board-proven groupBy*
// helpers, with a full-width visual-only header row (title +
// top-level count) per group. Grouping is set through the
// spreadsheet's own localStorage key (the T3 panel does not exist
// yet); the default is today's flat grid.
import {
  act,
  render,
  renderHook,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import Spreadsheet from "./Spreadsheet";
import { useSpreadsheetGrouping } from "../components/issue/useSpreadsheetGrouping";
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
    created_by: "u2",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    assignees: [],
    labels: [],
    ...over,
  };
}

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

const BASE = "/api/v1/workspaces/acme/projects/ENG";
const STORAGE_KEY = "glance:spreadsheet-grouping:acme:ENG";

// Rich default dataset: every dimension has at least two groups
// plus an Unknown/Ungrouped tail, so header order + counts are
// assertable for each dimension from one fixture set.
function defaultIssues(): Issue[] {
  return [
    mkIssue({
      id: "i1",
      sequence_id: 1,
      display_id: "ENG-1",
      name: "Alpha",
      priority: 2,
      state_id: "s",
      created_by: "u2",
      assignees: [{ id: "u2", name: "Ada" }],
      labels: [{ id: "l1", name: "Bug", color: "#ff0000" }],
      epics: [
        { id: "e1", name: "Epic One" },
        { id: "e3", name: "Archived Epic", archived_at: "2026-01-01T00:00:00Z" },
      ],
      project_structure: {
        cycle: { id: "c1", name: "Sprint 1" },
        modules: [{ id: "m1", name: "Auth" }],
        release: { id: "r1", name: "v0.2" },
      },
    }),
    mkIssue({
      id: "i2",
      sequence_id: 2,
      display_id: "ENG-2",
      name: "Beta",
      priority: 4,
      state_id: "s2",
      created_by: "u9",
      project_structure: { modules: [] },
    }),
    mkIssue({
      id: "i3",
      sequence_id: 3,
      display_id: "ENG-3",
      name: "Gamma",
      priority: 2,
      state_id: "s",
      created_by: "u2",
      assignees: [
        { id: "u2", name: "Ada" },
        { id: "u3", name: "Bo" },
      ],
      labels: [
        { id: "l1", name: "Bug", color: "#ff0000" },
        { id: "l2", name: "UI", color: "#00ff00" },
      ],
      epics: [
        { id: "e1", name: "Epic One" },
        { id: "e2", name: "Epic Two" },
      ],
      project_structure: {
        cycle: { id: "c1", name: "Sprint 1" },
        modules: [
          { id: "m1", name: "Auth" },
          { id: "m2", name: "API" },
        ],
        release: null,
      },
    }),
    mkIssue({
      id: "i4",
      sequence_id: 4,
      display_id: "ENG-4",
      name: "Delta",
      priority: 0,
      // Unknown state: no loaded state carries this id.
      state_id: "sX",
      created_by: "u2",
      project_structure: { modules: [] },
    }),
  ];
}

const mockData = vi.hoisted(() => ({
  issues: [] as Issue[],
  pqlResults: null as Issue[] | null,
}));

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
        return Promise.resolve({ custom_fields: [] });
      }
      if (path.includes("/views")) {
        return Promise.resolve({ views: [] });
      }
      if (path.includes("/epics")) {
        return Promise.resolve({ epics: [] });
      }
      if (path.includes("/issues")) {
        if (path.includes("pql=") && mockData.pqlResults !== null) {
          return Promise.resolve({
            results: mockData.pqlResults,
            next_cursor: undefined,
          });
        }
        return Promise.resolve({
          results: mockData.issues,
          next_cursor: undefined,
        });
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

/** Group header rows in DOM order, as "Title (count)". */
function groupHeaders(): string[] {
  const table = screen.queryByRole("table");
  if (!table) return [];
  return Array.from(
    table.querySelectorAll("tr[data-spreadsheet-group-header]"),
  ).map((tr) => (tr.textContent ?? "").replace(/\s+/g, " ").trim());
}

/** Top-level + nested row links in DOM order (aria-labels). */
function rowLabels(): string[] {
  const table = screen.getByRole("table");
  return within(table)
    .getAllByRole("link")
    .map((row) => row.getAttribute("aria-label") ?? "");
}

function setGrouping(value: unknown) {
  localStorage.setItem(STORAGE_KEY, JSON.stringify(value));
}

async function renderGrouped(groupBy: string) {
  setGrouping({ groupBy, subGroupBy: "none" });
  renderSpreadsheet();
  // Grid ready once the sort headers render.
  await screen.findByRole("button", { name: "Sort by Title" });
}

beforeEach(() => {
  localStorage.clear();
  vi.clearAllMocks();
  mockData.issues = defaultIssues();
  mockData.pqlResults = null;
});

describe("useSpreadsheetGrouping storage", () => {
  it("defaults to none/none when absent", () => {
    const { result } = renderHook(() =>
      useSpreadsheetGrouping("acme", "ENG"),
    );
    expect(result.current.groupBy).toBe("none");
    expect(result.current.subGroupBy).toBe("none");
  });

  it("falls back to none/none on corrupt or unknown values", () => {
    localStorage.setItem(STORAGE_KEY, "{not json");
    const { result } = renderHook(() =>
      useSpreadsheetGrouping("acme", "ENG"),
    );
    expect(result.current.groupBy).toBe("none");
    expect(result.current.subGroupBy).toBe("none");

    localStorage.setItem(
      STORAGE_KEY,
      JSON.stringify({ groupBy: "bogus", subGroupBy: "state" }),
    );
    const { result: result2 } = renderHook(() =>
      useSpreadsheetGrouping("acme", "ENG"),
    );
    expect(result2.current.groupBy).toBe("none");
    // The valid field survives a corrupt sibling field.
    expect(result2.current.subGroupBy).toBe("state");
  });

  it("persists a grouping change to its own per-project key", () => {
    const { result } = renderHook(() =>
      useSpreadsheetGrouping("acme", "ENG"),
    );
    act(() => {
      result.current.setGroupBy("state");
    });
    expect(result.current.groupBy).toBe("state");
    expect(localStorage.getItem(STORAGE_KEY)).toBe(
      JSON.stringify({ groupBy: "state", subGroupBy: "none" }),
    );
    // Another project's key is untouched.
    expect(
      localStorage.getItem("glance:spreadsheet-grouping:acme:OPS"),
    ).toBeNull();
  });
});

describe("Spreadsheet group-by rendering", () => {
  it("default renders today's flat grid with no header rows", async () => {
    renderSpreadsheet();
    await screen.findByRole("button", { name: "Sort by Title" });
    expect(groupHeaders()).toEqual([]);
    expect(rowLabels()).toEqual([
      "Open issue ENG-1: Alpha",
      "Open issue ENG-2: Beta",
      "Open issue ENG-3: Gamma",
      "Open issue ENG-4: Delta",
    ]);
  });

  it("corrupt stored grouping falls back to the flat grid", async () => {
    localStorage.setItem(STORAGE_KEY, "{not json");
    renderSpreadsheet();
    await screen.findByRole("button", { name: "Sort by Title" });
    expect(groupHeaders()).toEqual([]);
    expect(rowLabels()).toHaveLength(4);
  });

  it("groups by state in sequence order; unknown state is Ungrouped last", async () => {
    await renderGrouped("state");
    expect(groupHeaders()).toEqual([
      "Backlog (2)",
      "In Progress (1)",
      "Ungrouped (1)",
    ]);
    expect(rowLabels()).toEqual([
      "Open issue ENG-1: Alpha",
      "Open issue ENG-3: Gamma",
      "Open issue ENG-2: Beta",
      "Open issue ENG-4: Delta",
    ]);
  });

  it("groups by priority in 4,3,2,1,0 order", async () => {
    await renderGrouped("priority");
    expect(groupHeaders()).toEqual([
      "Urgent (1)",
      "Medium (2)",
      "None (1)",
    ]);
    expect(rowLabels()).toEqual([
      "Open issue ENG-2: Beta",
      "Open issue ENG-1: Alpha",
      "Open issue ENG-3: Gamma",
      "Open issue ENG-4: Delta",
    ]);
  });

  it("groups by epic: live epics only, multi-appearance, Ungrouped last", async () => {
    await renderGrouped("epic");
    expect(groupHeaders()).toEqual([
      "Epic One (2)",
      "Epic Two (1)",
      "Ungrouped (2)",
    ]);
    // Archived Epic never becomes a header; ENG-3 renders once per
    // live epic; ENG-2/ENG-4 land in Ungrouped.
    const labels = rowLabels();
    expect(labels.filter((l) => l.includes("ENG-3"))).toHaveLength(2);
    expect(labels.filter((l) => l.includes("ENG-1"))).toHaveLength(1);
    expect(labels).toEqual([
      "Open issue ENG-1: Alpha",
      "Open issue ENG-3: Gamma",
      "Open issue ENG-3: Gamma",
      "Open issue ENG-2: Beta",
      "Open issue ENG-4: Delta",
    ]);
  });

  it("groups by assignees: multi-appearance, Ungrouped last", async () => {
    await renderGrouped("assignees");
    expect(groupHeaders()).toEqual([
      "Ada (2)",
      "Bo (1)",
      "Ungrouped (2)",
    ]);
    const labels = rowLabels();
    expect(labels.filter((l) => l.includes("ENG-3"))).toHaveLength(2);
  });

  it("groups by labels: multi-appearance, Ungrouped last", async () => {
    await renderGrouped("labels");
    expect(groupHeaders()).toEqual([
      "Bug (2)",
      "UI (1)",
      "Ungrouped (2)",
    ]);
    const labels = rowLabels();
    expect(labels.filter((l) => l.includes("ENG-3"))).toHaveLength(2);
  });

  it("groups by modules: multi-appearance, Ungrouped last", async () => {
    await renderGrouped("modules");
    expect(groupHeaders()).toEqual([
      "Auth (2)",
      "API (1)",
      "Ungrouped (2)",
    ]);
    const labels = rowLabels();
    expect(labels.filter((l) => l.includes("ENG-3"))).toHaveLength(2);
  });

  it("groups by cycles: single-appearance, Ungrouped last", async () => {
    await renderGrouped("cycles");
    expect(groupHeaders()).toEqual(["Sprint 1 (2)", "Ungrouped (2)"]);
    expect(rowLabels()).toEqual([
      "Open issue ENG-1: Alpha",
      "Open issue ENG-3: Gamma",
      "Open issue ENG-2: Beta",
      "Open issue ENG-4: Delta",
    ]);
  });

  it("groups by releases: single-appearance, Ungrouped last", async () => {
    await renderGrouped("releases");
    expect(groupHeaders()).toEqual(["v0.2 (1)", "Ungrouped (3)"]);
    expect(rowLabels()[0]).toBe("Open issue ENG-1: Alpha");
  });

  it("groups by createdBy: member name, Unknown last", async () => {
    await renderGrouped("createdBy");
    expect(groupHeaders()).toEqual(["Ada (3)", "Unknown (1)"]);
    expect(rowLabels()).toEqual([
      "Open issue ENG-1: Alpha",
      "Open issue ENG-3: Gamma",
      "Open issue ENG-4: Delta",
      "Open issue ENG-2: Beta",
    ]);
  });

  it("groups by stateGroups: canonical order, empty groups omitted, Unknown last", async () => {
    await renderGrouped("stateGroups");
    // No Triage/Unstarted/Completed/Cancelled headers: zero-row
    // canonical groups are omitted, not synthesized.
    expect(groupHeaders()).toEqual([
      "Backlog (2)",
      "Started (1)",
      "Unknown (1)",
    ]);
  });

  it("Ungrouped is hidden when every issue has the dimension", async () => {
    // Every issue carries a label here, so no Ungrouped header.
    mockData.issues = defaultIssues().map((i) => ({
      ...i,
      labels: [{ id: "l1", name: "Bug", color: "#ff0000" }],
    }));
    await renderGrouped("labels");
    expect(groupHeaders()).toEqual(["Bug (4)"]);
  });

  it("nested child stays under its parent in the parent's group and is not counted", async () => {
    const child: SubtaskRow = {
      id: "c1",
      display_id: "ENG-9",
      title: "Child Task",
      // The child's OWN state is Started — but it must still render
      // under its Backlog parent, never in the Started group.
      state_id: "s2",
      priority: 1,
      assignee_ids: [],
      sort_order: 1,
    };
    const parent = mkIssue({
      id: "p1",
      sequence_id: 1,
      display_id: "ENG-1",
      name: "Parent",
      state_id: "s",
      subtasks: [child],
    });
    const childIssue = mkIssue({
      id: "c1",
      sequence_id: 2,
      display_id: "ENG-9",
      name: "Child Task",
      state_id: "s2",
    });
    const other = mkIssue({
      id: "o1",
      sequence_id: 3,
      display_id: "ENG-5",
      name: "Other Started",
      state_id: "s2",
    });
    mockData.issues = [parent, childIssue, other];
    await renderGrouped("state");
    expect(groupHeaders()).toEqual(["Backlog (1)", "In Progress (1)"]);
    // Parent row, then its nested child, then the Started group —
    // the child is never a top-level Started row.
    expect(rowLabels()).toEqual([
      "Open issue ENG-1: Parent",
      "Open issue ENG-9: Child Task",
      "Open issue ENG-5: Other Started",
    ]);
  });

  it("column sort re-sorts within groups without reordering groups", async () => {
    await renderGrouped("state");
    const user = userEvent.setup();
    expect(groupHeaders()).toEqual([
      "Backlog (2)",
      "In Progress (1)",
      "Ungrouped (1)",
    ]);

    await user.click(screen.getByRole("button", { name: "Sort by Title" }));
    // Ascending within Backlog: Alpha, Gamma. Group order unchanged.
    expect(groupHeaders()).toEqual([
      "Backlog (2)",
      "In Progress (1)",
      "Ungrouped (1)",
    ]);
    expect(rowLabels().slice(0, 2)).toEqual([
      "Open issue ENG-1: Alpha",
      "Open issue ENG-3: Gamma",
    ]);

    await user.click(screen.getByRole("button", { name: "Sort by Title" }));
    // Descending within Backlog: Gamma, Alpha. Groups still in place.
    expect(groupHeaders()).toEqual([
      "Backlog (2)",
      "In Progress (1)",
      "Ungrouped (1)",
    ]);
    expect(rowLabels().slice(0, 2)).toEqual([
      "Open issue ENG-3: Gamma",
      "Open issue ENG-1: Alpha",
    ]);
  });

  it("PQL rows group identically to visual rows", async () => {
    const all = defaultIssues();
    mockData.pqlResults = [all[0], all[2]];
    setGrouping({ groupBy: "state", subGroupBy: "none" });
    renderSpreadsheet("/w/acme/p/ENG/spreadsheet?pql=priority%20%3D%202");
    await screen.findByRole("button", { name: "Sort by Title" });
    // Only the PQL subset groups: Backlog (2), no other headers —
    // the visual rows (Beta, Delta) do not leak into the grid.
    // waitFor: the grid first renders the visual rows while the
    // committed PQL query is still loading (C104T0 pattern).
    await waitFor(() => {
      expect(groupHeaders()).toEqual(["Backlog (2)"]);
    });
    expect(rowLabels()).toEqual([
      "Open issue ENG-1: Alpha",
      "Open issue ENG-3: Gamma",
    ]);
  });

  it("empty state is unchanged when zero issues", async () => {
    mockData.issues = [];
    setGrouping({ groupBy: "state", subGroupBy: "none" });
    renderSpreadsheet();
    expect(await screen.findByText("No issues yet")).toBeInTheDocument();
    expect(screen.queryByRole("table")).toBeNull();
    expect(groupHeaders()).toEqual([]);
  });

  it("group header rows are visual only: not links, not counted as rows", async () => {
    await renderGrouped("state");
    const table = screen.getByRole("table");
    const headers = table.querySelectorAll(
      "tr[data-spreadsheet-group-header]",
    );
    expect(headers.length).toBe(3);
    for (const h of Array.from(headers)) {
      expect(h.getAttribute("role")).toBeNull();
      expect(h.querySelector("a, button")).toBeNull();
    }
    // Only the four issue rows carry the link role.
    expect(within(table).getAllByRole("link")).toHaveLength(4);
  });
});

// C147T3: the display panel's Group by select re-groups the grid
// live — a selection persists to the spreadsheet grouping key and
// the headers appear without any new issues fetch (no reload, no
// refetch).
describe("SpreadsheetDisplayPanel grouping controls (C147T3)", () => {
  async function issuesFetchCount(): Promise<number> {
    const { api } = await import("../lib/api");
    const getMock = api.get as ReturnType<typeof vi.fn>;
    return getMock.mock.calls.filter((args) =>
      (args[0] as string).includes("/issues"),
    ).length;
  }

  it("panel Group by selection re-groups the grid without a refetch", async () => {
    const user = userEvent.setup();
    renderSpreadsheet();
    await screen.findByRole("button", { name: "Sort by Title" });
    expect(groupHeaders()).toEqual([]);
    const before = await issuesFetchCount();
    expect(before).toBeGreaterThan(0);

    await user.click(
      screen.getByRole("button", { name: "Display properties" }),
    );
    await screen.findByText("Grouping");
    await user.click(
      screen.getByRole("combobox", { name: "Group issues by" }),
    );
    await user.click(await screen.findByRole("option", { name: "State" }));

    await waitFor(() => {
      expect(groupHeaders()).toEqual([
        "Backlog (2)",
        "In Progress (1)",
        "Ungrouped (1)",
      ]);
    });
    expect(JSON.parse(localStorage.getItem(STORAGE_KEY) ?? "{}")).toEqual({
      groupBy: "state",
      subGroupBy: "none",
    });
    expect(await issuesFetchCount()).toBe(before);
  });
});
