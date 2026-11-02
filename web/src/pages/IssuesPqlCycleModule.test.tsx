// @vitest-environment jsdom
// `cycle` / `module` PQL fields on the project-scoped Issues page
// (C115T1): applying `cycle = <name>` / `module = <name>` filters
// via the probe-first editor, and an unknown name surfaces the
// server 400 inline and leaves the list untouched (the query is
// never committed to the URL). Asserts the 400 status (via the
// probeStatuses the mock records) + inline display — NOT the exact
// wording, which is T0's call within the plan's shape. Follows the
// IssuesPqlProject.test.tsx (C114T1) page mock harness; the mock
// server answers the known names by junction membership, standing
// in for T0's translator (T0's branch was not yet pushed when this
// test was written, so it builds against the plan contract).

import {
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  MemoryRouter,
  Route,
  Routes,
  useSearchParams,
} from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import Issues from "./Issues";
import type { Issue } from "../lib/types";

const BASE = "/api/v1/workspaces/acme/projects/ENG";

// The plan-shape 400: unknown cycle/module name, with the value's
// position. The test asserts status + inline display, not this text.
const UNKNOWN_CYCLE_MESSAGE = "unknown cycle: Nope";
const UNKNOWN_MODULE_MESSAGE = "unknown module: Nope";

const mockState = vi.hoisted(() => ({
  getPaths: [] as string[],
  probeStatuses: [] as number[],
  views: [] as unknown[],
}));

function mkIssue(over: Partial<Issue> & { id: string }): Issue {
  return {
    project_id: "proj-1",
    sequence_id: 9,
    display_id: "ENG-9",
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

// Row A is in Sprint 3; row B is in the Auth module. The mock
// server's membership mapping below stands in for T0's junction
// translation.
const rowA = mkIssue({
  id: "i-a",
  display_id: "ENG-1",
  name: "Fix login redirect",
});
const rowB = mkIssue({
  id: "i-b",
  display_id: "ENG-2",
  name: "Urgent checkout bug",
});

vi.mock("../lib/api", () => {
  class ApiError extends Error {
    status: number;
    details: unknown;
    constructor(
      status: number,
      body: string,
      _code?: string,
      details?: unknown,
    ) {
      super(body);
      this.status = status;
      this.details = details;
    }
  }
  return {
  api: {
    get: vi.fn((path: string) => {
      mockState.getPaths.push(path);
      if (path.includes("/states")) return Promise.resolve({ states: [] });
      if (path.includes("/templates")) return Promise.resolve({ templates: [] });
      if (path.includes("/estimates")) return Promise.resolve({ estimates: [] });
      if (path.includes("/labels")) return Promise.resolve({ labels: [] });
      if (path.includes("/custom-fields"))
        return Promise.resolve({ custom_fields: [] });
      if (path.includes("/cycles")) return Promise.resolve({ cycles: [] });
      if (path.includes("/modules")) return Promise.resolve({ modules: [] });
      if (path.includes("/views"))
        return Promise.resolve({ views: mockState.views });
      if (path.includes("/members")) return Promise.resolve({ members: [] });
      if (path.includes("/issues/similar")) return Promise.resolve([]);
      if (path.includes("/issues")) {
        const qs = new URLSearchParams(path.split("?")[1] ?? "");
        const pql = qs.get("pql") ?? "";
        if (pql.includes("cycle = Sprint 3")) {
          return Promise.resolve({
            results: [rowA],
            next_cursor: undefined,
          });
        }
        if (pql.includes("module = Auth")) {
          return Promise.resolve({
            results: [rowB],
            next_cursor: undefined,
          });
        }
        if (pql.includes("cycle = Nope") || pql.includes("module = Nope")) {
          // Stand-in for T0's unknown-name 400 at the value's
          // position, recorded so the test can assert the status
          // the probe observed.
          mockState.probeStatuses.push(400);
          return Promise.reject(
            new ApiError(
              400,
              pql.includes("cycle =")
                ? UNKNOWN_CYCLE_MESSAGE
                : UNKNOWN_MODULE_MESSAGE,
              "bad_request",
              { position: 9 },
            ),
          );
        }
        return Promise.resolve({
          results: [rowA, rowB],
          next_cursor: undefined,
        });
      }
      if (path === BASE)
        return Promise.resolve({
          id: "proj-1",
          name: "ENG",
          identifier: "ENG",
          created_at: "2026-01-01T00:00:00Z",
          updated_at: "2026-01-01T00:00:00Z",
        });
      return Promise.reject(new Error("unexpected path: " + path));
    }),
    post: vi.fn(() => Promise.reject(new Error("unexpected post"))),
  },
  ApiError,
  };
});

vi.mock("../components/ui/toast", () => ({
  toast: { add: vi.fn() },
}));

vi.mock("../lib/auth", () => ({
  useAuth: () => ({ user: { id: "u", email: "u@x.com" }, loading: false }),
}));

/** Echoes the router search string so tests can assert the URL a
 *  PQL apply writes (BoardPql.test.tsx pattern). */
function SearchProbe() {
  const [sp] = useSearchParams();
  return <span data-testid="search">{sp.toString()}</span>;
}

function searchParam(name: string): string | null {
  return new URLSearchParams(
    screen.getByTestId("search").textContent ?? "",
  ).get(name);
}

function renderIssues(initialEntry = "/w/acme/p/ENG") {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <QueryClientProvider client={client}>
        <Routes>
          <Route
            path="/w/:slug/p/:identifier"
            element={
              <>
                <Issues />
                <SearchProbe />
              </>
            }
          />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

async function openPqlEditor() {
  fireEvent.click(screen.getByRole("button", { name: /^Filter/ }));
  fireEvent.click(screen.getByRole("button", { name: "PQL" }));
  await screen.findByLabelText("PQL query");
}

async function applyQuery(query: string) {
  fireEvent.change(screen.getByLabelText("PQL query"), {
    target: { value: query },
  });
  fireEvent.click(screen.getByRole("button", { name: "Apply" }));
}

beforeEach(() => {
  vi.clearAllMocks();
  mockState.getPaths = [];
  mockState.probeStatuses = [];
  mockState.views = [];
});

describe("Issues cycle/module PQL (C115T1)", () => {
  it("applying cycle = Sprint 3 filters the list and commits the query to the URL", async () => {
    renderIssues();
    await screen.findByText("Fix login redirect");
    await screen.findByText("Urgent checkout bug");

    await openPqlEditor();
    await applyQuery("cycle = Sprint 3");

    await waitFor(() => {
      expect(searchParam("pql")).toBe("cycle = Sprint 3");
    });
    await screen.findByText("Fix login redirect");
    await waitFor(() => {
      expect(screen.queryByText("Urgent checkout bug")).toBeNull();
    });
  });

  it("applying module = Auth filters the list and commits the query to the URL", async () => {
    renderIssues();
    await screen.findByText("Fix login redirect");
    await screen.findByText("Urgent checkout bug");

    await openPqlEditor();
    await applyQuery("module = Auth");

    await waitFor(() => {
      expect(searchParam("pql")).toBe("module = Auth");
    });
    await screen.findByText("Urgent checkout bug");
    await waitFor(() => {
      expect(screen.queryByText("Fix login redirect")).toBeNull();
    });
  });

  it("applying cycle = Nope surfaces the server 400 inline and leaves the list untouched", async () => {
    renderIssues();
    await screen.findByText("Fix login redirect");
    await screen.findByText("Urgent checkout bug");

    await openPqlEditor();
    await applyQuery("cycle = Nope");

    // Inline display: the probe's 400 message + column in the
    // editor's alert (wording asserted only at the plan-shape
    // level: it names the cycle field).
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("cycle");
    expect(alert.textContent).toContain("at column 9");
    // The probe observed a 400.
    expect(mockState.probeStatuses).toContain(400);
    // List untouched: both rows still on screen, and the failed
    // query was never committed to the URL.
    expect(screen.getByText("Fix login redirect")).toBeTruthy();
    expect(screen.getByText("Urgent checkout bug")).toBeTruthy();
    await waitFor(() => {
      expect(searchParam("pql")).toBeNull();
    });
    // The failed query stays in the editor as a draft.
    expect(screen.getByLabelText("PQL query")).toHaveValue("cycle = Nope");
  });

  it("applying module = Nope surfaces the server 400 inline and leaves the list untouched", async () => {
    renderIssues();
    await screen.findByText("Fix login redirect");
    await screen.findByText("Urgent checkout bug");

    await openPqlEditor();
    await applyQuery("module = Nope");

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("module");
    expect(alert.textContent).toContain("at column 9");
    expect(mockState.probeStatuses).toContain(400);
    expect(screen.getByText("Fix login redirect")).toBeTruthy();
    expect(screen.getByText("Urgent checkout bug")).toBeTruthy();
    await waitFor(() => {
      expect(searchParam("pql")).toBeNull();
    });
    expect(screen.getByLabelText("PQL query")).toHaveValue("module = Nope");
  });
});
