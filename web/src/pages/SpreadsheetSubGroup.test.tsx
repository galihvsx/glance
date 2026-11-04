// @vitest-environment jsdom
// C147T2: spreadsheet sub-grouping — inside each primary group, the
// grid partitions that group's top-level rows via subGroup.ts
// sections (effectiveSubGroupBy: same-dimension renders as none).
// Sub-group headers are visual only, indented under the primary
// header, title + top-level count. Set via the spreadsheet's own
// localStorage key (the T3 panel does not exist yet).
import {
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import Spreadsheet from "./Spreadsheet";
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

// Same rich fixture set as SpreadsheetGroupBy.test.tsx.
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

/** All header rows (primary + sub-group) in DOM order, "Title (count)". */
function allHeaders(): string[] {
  const table = screen.queryByRole("table");
  if (!table) return [];
  return Array.from(
    table.querySelectorAll(
      "tr[data-spreadsheet-group-header], tr[data-spreadsheet-sub-group-header]",
    ),
  ).map((tr) => (tr.textContent ?? "").replace(/\s+/g, " ").trim());
}

/** Primary header rows only, in DOM order. */
function primaryHeaders(): string[] {
  const table = screen.queryByRole("table");
  if (!table) return [];
  return Array.from(
    table.querySelectorAll("tr[data-spreadsheet-group-header]"),
  ).map((tr) => (tr.textContent ?? "").replace(/\s+/g, " ").trim());
}

/** Sub-group header rows only, in DOM order. */
function subHeaders(): string[] {
  const table = screen.queryByRole("table");
  if (!table) return [];
  return Array.from(
    table.querySelectorAll("tr[data-spreadsheet-sub-group-header]"),
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

async function renderGrouped(groupBy: string, subGroupBy: string) {
  setGrouping({ groupBy, subGroupBy });
  renderSpreadsheet();
  await screen.findByRole("button", { name: "Sort by Title" });
}

beforeEach(() => {
  localStorage.clear();
  vi.clearAllMocks();
  mockData.issues = defaultIssues();
  mockData.pqlResults = null;
});

describe("Spreadsheet sub-group rendering", () => {
  it("sub-group headers nest under the primary header with correct counts", async () => {
    await renderGrouped("state", "priority");
    // Backlog: Alpha + Gamma are both Medium; In Progress: Beta is
    // Urgent; Ungrouped: Delta is None.
    expect(allHeaders()).toEqual([
      "Backlog (2)",
      "Medium (2)",
      "In Progress (1)",
      "Urgent (1)",
      "Ungrouped (1)",
      "None (1)",
    ]);
    expect(rowLabels()).toEqual([
      "Open issue ENG-1: Alpha",
      "Open issue ENG-3: Gamma",
      "Open issue ENG-2: Beta",
      "Open issue ENG-4: Delta",
    ]);
    // Sub-group headers are visual only: not links, no controls,
    // and indented under the primary header.
    const table = screen.getByRole("table");
    const subs = table.querySelectorAll(
      "tr[data-spreadsheet-sub-group-header]",
    );
    expect(subs.length).toBe(3);
    for (const h of Array.from(subs)) {
      expect(h.querySelector("a, button")).toBeNull();
      expect(h.querySelector("td")?.className).toContain("pl-8");
    }
    // Only the four issue rows carry the link role.
    expect(within(table).getAllByRole("link")).toHaveLength(4);
  });

  it("same-dimension sub-group renders as none", async () => {
    await renderGrouped("state", "state");
    expect(subHeaders()).toEqual([]);
    expect(primaryHeaders()).toEqual([
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

  it('sub-group "none" renders T1 output exactly', async () => {
    await renderGrouped("state", "none");
    expect(subHeaders()).toEqual([]);
    expect(primaryHeaders()).toEqual([
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

  it("multi-appearance sub-group semantics are inherited from subGroup.ts", async () => {
    await renderGrouped("state", "labels");
    // Backlog group: Alpha (Bug) + Gamma (Bug, UI) → Bug (2),
    // UI (1); Gamma renders once per label sub-group.
    expect(allHeaders()).toEqual([
      "Backlog (2)",
      "Bug (2)",
      "UI (1)",
      "In Progress (1)",
      "Ungrouped (1)",
      "Ungrouped (1)",
      "Ungrouped (1)",
    ]);
    const labels = rowLabels();
    expect(labels.filter((l) => l.includes("ENG-3"))).toHaveLength(2);
    expect(labels).toEqual([
      "Open issue ENG-1: Alpha",
      "Open issue ENG-3: Gamma",
      "Open issue ENG-3: Gamma",
      "Open issue ENG-2: Beta",
      "Open issue ENG-4: Delta",
    ]);
  });

  it("nested child stays under its parent in the parent's sub-group", async () => {
    const child: SubtaskRow = {
      id: "c1",
      display_id: "ENG-9",
      title: "Child Task",
      // The child's OWN priority is Low — but it must still render
      // under its None-priority parent, never in the Low sub-group.
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
      priority: 0,
      subtasks: [child],
    });
    const childIssue = mkIssue({
      id: "c1",
      sequence_id: 2,
      display_id: "ENG-9",
      name: "Child Task",
      state_id: "s2",
      priority: 1,
    });
    const other = mkIssue({
      id: "o1",
      sequence_id: 3,
      display_id: "ENG-5",
      name: "Other Started",
      state_id: "s2",
      priority: 1,
    });
    mockData.issues = [parent, childIssue, other];
    await renderGrouped("state", "priority");
    expect(allHeaders()).toEqual([
      "Backlog (1)",
      "None (1)",
      "In Progress (1)",
      "Low (1)",
    ]);
    // Parent row, then its nested child, then the Started group —
    // the child is never a top-level Low row.
    expect(rowLabels()).toEqual([
      "Open issue ENG-1: Parent",
      "Open issue ENG-9: Child Task",
      "Open issue ENG-5: Other Started",
    ]);
  });

  it("PQL rows sub-group identically to visual rows", async () => {
    const all = defaultIssues();
    mockData.pqlResults = [all[0], all[2]];
    setGrouping({ groupBy: "state", subGroupBy: "priority" });
    renderSpreadsheet("/w/acme/p/ENG/spreadsheet?pql=priority%20%3D%202");
    await screen.findByRole("button", { name: "Sort by Title" });
    await waitFor(() => {
      expect(allHeaders()).toEqual(["Backlog (2)", "Medium (2)"]);
    });
    expect(rowLabels()).toEqual([
      "Open issue ENG-1: Alpha",
      "Open issue ENG-3: Gamma",
    ]);
  });

  it("empty state is unchanged when zero issues", async () => {
    mockData.issues = [];
    setGrouping({ groupBy: "state", subGroupBy: "priority" });
    renderSpreadsheet();
    expect(await screen.findByText("No issues yet")).toBeInTheDocument();
    expect(screen.queryByRole("table")).toBeNull();
    expect(allHeaders()).toEqual([]);
  });
});
