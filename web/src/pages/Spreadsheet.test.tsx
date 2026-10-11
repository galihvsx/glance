// @vitest-environment jsdom
// C20T2: spreadsheet Cycle/Module/Release columns — chip rendering,
// batched membership wiring (the list query carries
// ?include_project_structure=1, resolved server-side in one query — C21T2
// killed the old per-row N+1), and sort behavior (empty cells sort last,
// matching the existing spreadsheet convention).
import { act, fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import Spreadsheet from "./Spreadsheet";
import type { Issue, IssueProjectStructure } from "../lib/types";

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

const issues = [
  mkIssue({ id: "i1", sequence_id: 1, display_id: "ENG-1", name: "First" }),
  mkIssue({ id: "i2", sequence_id: 2, display_id: "ENG-2", name: "Second" }),
  mkIssue({ id: "i3", sequence_id: 3, display_id: "ENG-3", name: "Third" }),
];

const structures: Record<string, IssueProjectStructure> = {
  i1: {
    cycle: { id: "c1", name: "Sprint 1" },
    modules: [
      { id: "m1", name: "Auth" },
      { id: "m2", name: "API" },
    ],
    release: { id: "r1", name: "v0.2" },
  },
  i2: {
    cycle: { id: "c2", name: "Sprint 2" },
    modules: [],
    release: null,
  },
  // i3: no membership at all — cycle/release omitted, modules empty.
  i3: { modules: [] },
};

// C20T3: states + members fixtures for the inline-editing tests. The
// pre-existing C20T2 tests never open the new-issue dialog or the assignee
// popover, so the richer fixtures don't affect them.
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
      if (path.includes("/issues")) {
        // C21T2: the list carries project_structure batched on every row —
        // one request, no per-row ?include_project_structure=1 fetches.
        return Promise.resolve({
          results: issues.map((i) => ({
            ...i,
            project_structure: structures[i.id],
          })),
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
  // Mirror the real ApiError constructor (status, body, ...) so tests can
  // construct errors the same way production code does.
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

function renderSpreadsheet() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <MemoryRouter initialEntries={["/w/acme/p/ENG/spreadsheet"]}>
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

function rowLabels(): string[] {
  const table = screen.getByRole("table");
  return within(table)
    .getAllByRole("link")
    .map((row) => row.getAttribute("aria-label") ?? "");
}

beforeEach(() => {
  localStorage.clear();
  vi.clearAllMocks();
});

describe("Spreadsheet Cycle/Module/Release columns", () => {
  it("renders Cycle, Module, and Release column headers", async () => {
    renderSpreadsheet();
    expect(
      await screen.findByRole("button", { name: "Sort by Cycle" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Sort by Module" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Sort by Release" }),
    ).toBeInTheDocument();
  });

  it("fetches membership batched with the list query — one request, no per-row N+1", async () => {
    renderSpreadsheet();
    // Wait for the membership chips to render before asserting on calls.
    await screen.findByTitle("Sprint 1");
    const { api } = await import("../lib/api");
    const getMock = api.get as ReturnType<typeof vi.fn>;
    const calls = getMock.mock.calls.map(
      (args: unknown[]) => args[0] as string,
    );
    // Exactly one list request, carrying the flag.
    const listCalls = calls.filter((p) => p.includes("/issues?"));
    expect(listCalls).toHaveLength(1);
    expect(listCalls[0]).toContain("include_project_structure=1");
    // No per-row issue fetches anymore (the C20T2 useQueries N+1 is gone).
    const perRow = calls.filter((p) => /\/issues\/[^?]+\?/.test(p));
    expect(perRow).toHaveLength(0);
  });

  it("renders cycle/module/release chips; empty cells show an em dash", async () => {
    renderSpreadsheet();
    await screen.findByTitle("Sprint 1");

    // Cycle chip + release chip on the ENG-1 row.
    const row1 = screen.getByRole("link", { name: /Open issue ENG-1/ });
    expect(within(row1).getByTitle("Sprint 1")).toHaveTextContent("Sprint 1");
    expect(within(row1).getByTitle("v0.2")).toHaveTextContent("v0.2");

    // Modules: one chip per module, comma-separated.
    expect(within(row1).getByTitle("Auth")).toHaveTextContent("Auth");
    expect(within(row1).getByTitle("API")).toHaveTextContent("API");
    const moduleCell = within(row1).getByTitle("Auth").parentElement!
      .parentElement!;
    expect(moduleCell.textContent).toContain(",");

    // ENG-3 has no membership — all three cells are em dashes.
    const row3 = screen.getByRole("link", { name: /Open issue ENG-3/ });
    const dashes = within(row3).getAllByText("—");
    expect(dashes.length).toBeGreaterThanOrEqual(3);
  });

  it("sorts by cycle with empty cells last in both directions", async () => {
    renderSpreadsheet();
    await screen.findByTitle("Sprint 1");
    const user = userEvent.setup();

    await user.click(screen.getByRole("button", { name: "Sort by Cycle" }));
    let labels = rowLabels();
    expect(labels[0]).toMatch(/ENG-1/);
    expect(labels[1]).toMatch(/ENG-2/);
    expect(labels[2]).toMatch(/ENG-3/); // no cycle → last

    await user.click(screen.getByRole("button", { name: "Sort by Cycle" }));
    labels = rowLabels();
    expect(labels[0]).toMatch(/ENG-2/);
    expect(labels[1]).toMatch(/ENG-1/);
    expect(labels[2]).toMatch(/ENG-3/); // no cycle → still last
  });

  it("sorts by module and release with empty cells last", async () => {
    renderSpreadsheet();
    await screen.findByTitle("Sprint 1");
    const user = userEvent.setup();

    // Only ENG-1 has modules: "API,Auth" joined.
    await user.click(screen.getByRole("button", { name: "Sort by Module" }));
    let labels = rowLabels();
    expect(labels[0]).toMatch(/ENG-1/);
    // ENG-2 and ENG-3 are both empty → last, stable relative order.
    expect(labels.slice(1).join(" ")).toMatch(/ENG-2.*ENG-3/);

    // Only ENG-1 has a release.
    await user.click(screen.getByRole("button", { name: "Sort by Release" }));
    labels = rowLabels();
    expect(labels[0]).toMatch(/ENG-1/);
    expect(labels.slice(1).join(" ")).toMatch(/ENG-2.*ENG-3/);
  });
});

describe("Spreadsheet inline cell editing (C20T3)", () => {
  async function renderReady() {
    renderSpreadsheet();
    await screen.findByRole("button", { name: "Sort by Cycle" });
    return userEvent.setup();
  }

  function eng1() {
    return screen.getByRole("link", { name: /Open issue ENG-1/ });
  }

  it("commits a state change through PATCH .../issues/{id}", async () => {
    const user = await renderReady();
    const row = eng1();
    await user.click(within(row).getByTitle("Click to edit state"));
    const select = screen.getByLabelText("Edit state for ENG-1");
    expect(select).toBeInTheDocument();

    const { api } = await import("../lib/api");
    (api.patch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      ...issues[0],
      state_id: "s2",
    });
    await user.selectOptions(select, "s2");

    expect(api.patch).toHaveBeenCalledWith(
      expect.stringContaining("/issues/i1"),
      { state_id: "s2" },
    );
    // Editor closes after commit.
    expect(
      screen.queryByLabelText("Edit state for ENG-1"),
    ).not.toBeInTheDocument();
  });

  it("optimistically updates priority and rolls back on PATCH failure", async () => {
    const user = await renderReady();
    const row = eng1();
    const { api, ApiError } = await import("../lib/api");
    const patchMock = api.patch as ReturnType<typeof vi.fn>;
    let rejectPatch!: (e: unknown) => void;
    patchMock.mockImplementationOnce(
      () =>
        new Promise((_res, rej) => {
          rejectPatch = rej;
        }),
    );

    await user.click(within(row).getByTitle("Click to edit priority"));
    const select = screen.getByLabelText("Edit priority for ENG-1");
    await user.selectOptions(select, "3");

    // PATCH still pending → optimistic value is visible.
    expect(within(row).getByText("High")).toBeInTheDocument();

    // Fail the request → rollback to the previous value + error banner.
    await act(async () => {
      rejectPatch(new ApiError(500, "nope"));
    });
    expect(within(row).getByText("None")).toBeInTheDocument();
    expect(await screen.findByText("nope")).toBeInTheDocument();
  });

  it("commits a start-date edit on Enter", async () => {
    const user = await renderReady();
    const row = eng1();
    await user.click(within(row).getByTitle("Click to edit start date"));
    const input = screen.getByLabelText(
      "Edit start date for ENG-1",
    ) as HTMLInputElement;

    const { api } = await import("../lib/api");
    (api.patch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({});
    fireEvent.change(input, { target: { value: "2026-03-04" } });
    await user.keyboard("{Enter}");

    expect(api.patch).toHaveBeenCalledWith(
      expect.stringContaining("/issues/i1"),
      { start_date: "2026-03-04" },
    );
  });

  it("commits a target-date edit on blur", async () => {
    const user = await renderReady();
    const row = eng1();
    await user.click(within(row).getByTitle("Click to edit target date"));
    const input = screen.getByLabelText(
      "Edit target date for ENG-1",
    ) as HTMLInputElement;

    const { api } = await import("../lib/api");
    (api.patch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({});
    fireEvent.change(input, { target: { value: "2026-04-05" } });
    // Tab away → blur commits.
    await user.tab();

    expect(api.patch).toHaveBeenCalledWith(
      expect.stringContaining("/issues/i1"),
      { target_date: "2026-04-05" },
    );
  });

  it("cancels a date edit on Escape without PATCHing", async () => {
    const user = await renderReady();
    const row = eng1();
    await user.click(within(row).getByTitle("Click to edit target date"));
    const input = screen.getByLabelText(
      "Edit target date for ENG-1",
    ) as HTMLInputElement;

    fireEvent.change(input, { target: { value: "2026-05-06" } });
    await user.keyboard("{Escape}");

    const { api } = await import("../lib/api");
    expect(api.patch).not.toHaveBeenCalled();
    expect(
      screen.queryByLabelText("Edit target date for ENG-1"),
    ).not.toBeInTheDocument();
  });

  it("toggles an assignee through the relation endpoint with optimistic update", async () => {
    const user = await renderReady();
    const row = eng1();
    const { api } = await import("../lib/api");
    // Never resolve: no refetch, so the optimistic badge stays put.
    (api.post as ReturnType<typeof vi.fn>).mockImplementationOnce(
      () => new Promise(() => {}),
    );

    // Unassigned → the picker trigger shows an em dash.
    const trigger = within(row).getByRole("button", { name: "—" });
    await user.click(trigger);
    const checkbox = await screen.findByRole("checkbox", { name: "Ada" });
    await user.click(checkbox);

    expect(api.post).toHaveBeenCalledWith(
      expect.stringContaining("/issues/i1/assignees/u2"),
    );
    expect(await within(row).findByText("Ada")).toBeInTheDocument();
  });

  it("does not navigate to the issue when opening a cell editor", async () => {
    const user = await renderReady();
    const row = eng1();
    await user.click(within(row).getByTitle("Click to edit state"));
    // Editor opened instead of navigation: the select is in the document
    // and the row is still on the spreadsheet (no navigation happened —
    // navigation would unmount the table).
    expect(
      screen.getByLabelText("Edit state for ENG-1"),
    ).toBeInTheDocument();
    expect(screen.getByRole("table")).toBeInTheDocument();
  });
});
