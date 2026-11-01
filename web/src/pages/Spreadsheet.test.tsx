// @vitest-environment jsdom
// C20T2: spreadsheet Cycle/Module/Release columns — chip rendering,
// batched membership wiring (the list query carries
// ?include_project_structure=1, resolved server-side in one query — C21T2
// killed the old per-row N+1), and sort behavior (empty cells sort last,
// matching the existing spreadsheet convention).
import {
  act,
  fireEvent,
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

describe("Spreadsheet PQL mode (C104T0)", () => {
  // PQL tests route api.get by path: visual list requests return all
  // three issues, PQL requests return only the given subset, so the
  // grid's switch from visual to PQL rows is observable. All other
  // endpoints keep the factory mock's fixtures.
  async function renderPqlReady(
    pqlResults: Issue[],
    opts: { initialEntry?: string; probeError?: unknown } = {},
  ) {
    const { api } = await import("../lib/api");
    const { stubMatchMedia } = await import("../components/shell/test-utils");
    stubMatchMedia(false);
    (api.get as ReturnType<typeof vi.fn>).mockImplementation(
      (path: string) => {
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
          if (path.includes("pql=")) {
            if (opts.probeError) {
              return Promise.reject(opts.probeError);
            }
            return Promise.resolve({
              results: pqlResults.map((i) => ({
                ...i,
                project_structure: structures[i.id],
              })),
              next_cursor: undefined,
            });
          }
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
      },
    );
    renderSpreadsheet(opts.initialEntry);
    await screen.findByRole("button", { name: "Sort by Cycle" });
    return userEvent.setup();
  }

  async function openPqlEditor(
    user: ReturnType<typeof userEvent.setup>,
  ) {
    // Clicked while the popover is closed, so the "Filters" mode
    // toggle is not yet in the document to collide with this match.
    // (The trigger's accessible name is "Filter1" — no space — so a
    // \b-anchored regex never matches it.)
    await user.click(screen.getByRole("button", { name: /^Filter/ }));
    await user.click(screen.getByRole("button", { name: "PQL" }));
    return screen.getByLabelText("PQL query");
  }

  async function getCalls(): Promise<string[]> {
    const { api } = await import("../lib/api");
    return (api.get as ReturnType<typeof vi.fn>).mock.calls.map(
      (args: unknown[]) => args[0] as string,
    );
  }

  it("apply commits and re-runs the query — probe + fetch both carry pql", async () => {
    const user = await renderPqlReady([issues[1]]);
    const editor = await openPqlEditor(user);
    await user.type(editor, "priority = High");
    await user.click(screen.getByRole("button", { name: "Apply" }));

    // Grid switches to the PQL subset (ENG-2 only).
    await screen.findByRole("link", { name: /Open issue ENG-2/ });
    await waitFor(() => {
      expect(
        screen.queryByRole("link", { name: /Open issue ENG-1/ }),
      ).toBeNull();
      expect(
        screen.queryByRole("link", { name: /Open issue ENG-3/ }),
      ).toBeNull();
    });

    // Probe (per_page 100, pre-commit) and the PQL query fetch both
    // carried the pql param; neither carried a visual filter param.
    const calls = await getCalls();
    const pqlFetches = calls.filter(
      (p) => p.includes("pql=") && p.includes("per_page=100"),
    );
    expect(pqlFetches.length).toBeGreaterThanOrEqual(2);
    for (const p of pqlFetches) {
      expect(p).toContain("pql=priority");
      expect(p).toContain("include_project_structure=1");
      expect(p).not.toContain("state=");
    }
  });

  it("a 400 rejects without changing the grid", async () => {
    const { ApiError } = await import("../lib/api");
    const user = await renderPqlReady([], {
      probeError: new ApiError(400, "unknown field foo"),
    });
    const editor = await openPqlEditor(user);
    await user.type(editor, "foo = bar");
    await user.click(screen.getByRole("button", { name: "Apply" }));

    // Server message inline; the editor stays open and the grid is
    // untouched (all three visual rows still there).
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "unknown field foo",
    );
    expect(rowLabels()).toHaveLength(3);
    const calls = await getCalls();
    // Only the probe fired — no committed PQL query fetch followed it.
    expect(
      calls.filter((p) => p.includes("pql=")).length,
    ).toBeGreaterThanOrEqual(1);
  });

  it("mode exit restores the parked visual filters", async () => {
    const user = await renderPqlReady(issues, {
      initialEntry: "/w/acme/p/ENG/spreadsheet?q=hello",
    });
    // Single space here: Testing Library's normalizer collapses the
    // node's double space before comparing, so the query must too.
    const search = screen.getByPlaceholderText(
      "Search issues… ( / )",
    ) as HTMLInputElement;
    expect(search).toHaveValue("hello");

    // Enter PQL mode: the search text is parked, the box clears and
    // disables (search is a visual filter).
    await user.click(screen.getByRole("button", { name: /^Filter/ }));
    await user.click(screen.getByRole("button", { name: "PQL" }));
    const parkedSearch = screen.getByPlaceholderText(
      "Search is replaced by PQL mode",
    ) as HTMLInputElement;
    expect(parkedSearch).toHaveValue("");
    expect(parkedSearch).toBeDisabled();

    // Exit back to Filters: the parked search text comes back.
    await user.click(screen.getByRole("button", { name: "Filters" }));
    const restored = screen.getByPlaceholderText(
      "Search issues… ( / )",
    ) as HTMLInputElement;
    expect(restored).toHaveValue("hello");
    expect(restored).not.toBeDisabled();
  });

  it("header count follows the PQL result", async () => {
    const user = await renderPqlReady([issues[1]]);
    const breadcrumb = screen.getByLabelText("Breadcrumb");
    expect(within(breadcrumb).getByText("3")).toBeInTheDocument();

    const editor = await openPqlEditor(user);
    await user.type(editor, "priority = High");
    await user.click(screen.getByRole("button", { name: "Apply" }));

    await waitFor(() => {
      expect(within(breadcrumb).getByText("1")).toBeInTheDocument();
    });
  });

  it("a pasted ?pql= link opens in PQL mode (backstop)", async () => {
    const user = await renderPqlReady([issues[1]], {
      initialEntry:
        "/w/acme/p/ENG/spreadsheet?pql=priority+%3D+High",
    });
    // Grid shows the PQL subset, not the visual list.
    await screen.findByRole("link", { name: /Open issue ENG-2/ });
    await waitFor(() => {
      expect(
        screen.queryByRole("link", { name: /Open issue ENG-1/ }),
      ).toBeNull();
    });

    // The panel opens already in PQL mode with the query in the editor.
    await user.click(screen.getByRole("button", { name: /^Filter/ }));
    expect(screen.getByRole("button", { name: "PQL" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(screen.getByLabelText("PQL query")).toHaveValue(
      "priority = High",
    );
  });

  describe("PQL-mode staleness (C104T1)", () => {
    // The committed PQL fetch is per_page=100; the visual list fetch
    // is per_page=25 — that split tells a PQL re-run apart from a
    // visual refetch after a mutation settles.
    async function applyCommittedQuery(
      user: ReturnType<typeof userEvent.setup>,
    ) {
      const editor = await openPqlEditor(user);
      await user.type(editor, "priority = High");
      await user.click(screen.getByRole("button", { name: "Apply" }));
      await screen.findByRole("link", { name: /Open issue ENG-2/ });
      await waitFor(() => {
        expect(
          screen.queryByRole("link", { name: /Open issue ENG-1/ }),
        ).toBeNull();
      });
    }

    // PQL fetches are the per_page=100 ones (probe + committed query);
    // visual list fetches are per_page=25, even though they also carry
    // pql= once the query is committed (the visual query still runs
    // with the URL filters).
    function countPqlFetches(calls: string[]) {
      return calls.filter(
        (p) => p.includes("/issues") && p.includes("per_page=100"),
      ).length;
    }

    it("a cell edit while a query is committed re-runs the same committed query", async () => {
      const user = await renderPqlReady([issues[1]]);
      await applyCommittedQuery(user);

      const before = await getCalls();
      const pqlBefore = countPqlFetches(before);
      const visualBefore = before.filter((p) =>
        p.includes("per_page=25"),
      ).length;
      expect(pqlBefore).toBeGreaterThanOrEqual(1);

      const { api } = await import("../lib/api");
      (api.patch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ...issues[1],
        state_id: "s2",
      });
      const row = screen.getByRole("link", { name: /Open issue ENG-2/ });
      await user.click(within(row).getByTitle("Click to edit state"));
      await user.selectOptions(
        screen.getByLabelText("Edit state for ENG-2"),
        "s2",
      );

      // Settle invalidates the PQL key (not the visual key): the
      // committed query re-runs with the same pql param, and the
      // visual list does not refetch.
      await waitFor(async () => {
        const calls = await getCalls();
        expect(countPqlFetches(calls)).toBeGreaterThan(pqlBefore);
      });
      const after = await getCalls();
      expect(
        after.filter((p) => p.includes("per_page=25")).length,
      ).toBe(visualBefore);
      const pqlFetches = after.filter(
        (p) => p.includes("pql=") && p.includes("per_page=100"),
      );
      expect(pqlFetches[pqlFetches.length - 1]).toContain("pql=priority");
    });

    it("an inline create while a query is committed re-runs the committed query", async () => {
      const user = await renderPqlReady([issues[1]]);
      await applyCommittedQuery(user);

      const before = await getCalls();
      const pqlBefore = countPqlFetches(before);
      expect(pqlBefore).toBeGreaterThanOrEqual(1);

      const { api } = await import("../lib/api");
      (api.post as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ...issues[1],
        id: "i4",
        display_id: "ENG-4",
        name: "Brand new",
      });
      await user.click(
        screen.getByRole("button", { name: "Add work item" }),
      );
      await user.type(screen.getByLabelText("Title"), "Brand new");
      await user.click(screen.getByRole("button", { name: "Create issue" }));

      await waitFor(async () => {
        const calls = await getCalls();
        expect(countPqlFetches(calls)).toBeGreaterThan(pqlBefore);
      });
    });
  });
});
