// @vitest-environment jsdom
// C26T3: the create-issue dialog opens with a prefilled target (due) date
// when navigated to with location.state { newIssue: true,
// newIssueTargetDate }, and creating invalidates the calendar caches so the
// new chip appears on its day.
import {
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  MemoryRouter,
  Route,
  Routes,
  useSearchParams,
} from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import Issues from "./Issues";
import { EMPTY_FILTERS } from "../lib/filters";
import type { IssueViewRow } from "../lib/savedViews";
import type { Issue } from "../lib/types";

const BASE = "/api/v1/workspaces/acme/projects/ENG";

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

const mockState = vi.hoisted(() => ({
  posted: [] as { path: string; body: unknown }[],
  toastAdd: vi.fn(),
  // C106T1: per-test views + a record of GET paths, so the
  // default-view tests can serve a default and assert the list
  // refetched under its filters.
  views: [] as unknown[],
  getPaths: [] as string[],
}));

vi.mock("../lib/api", () => ({
  api: {
    get: vi.fn((path: string) => {
      mockState.getPaths.push(path);
      if (path.includes("/states")) return Promise.resolve({ states: [] });
      if (path.includes("/templates")) return Promise.resolve({ templates: [] });
      if (path.includes("/estimates")) return Promise.resolve({ estimates: [] });
      if (path.includes("/labels")) return Promise.resolve({ labels: [] });
      if (path.includes("/custom-fields"))
        return Promise.resolve({ custom_fields: [] });
      if (path.includes("/views"))
        return Promise.resolve({ views: mockState.views });
      if (path.includes("/members")) return Promise.resolve({ members: [] });
      if (path.includes("/issues/similar")) return Promise.resolve([]);
      if (path.includes("/issues"))
        return Promise.resolve({ results: [], next_cursor: undefined });
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
    post: vi.fn((path: string, body: unknown) => {
      mockState.posted.push({ path, body });
      const b = body as { name?: string };
      return Promise.resolve(
        mkIssue({ id: "new-1", display_id: "ENG-9", name: b.name ?? "x" }),
      );
    }),
  },
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, body: string) {
      super(body);
      this.status = status;
    }
  },
}));

vi.mock("../components/ui/toast", () => ({
  toast: { add: mockState.toastAdd },
}));

// Issues renders SavedViewsMenu → useAuth; the real provider needs a
// session — stub the auth context for these dialog tests.
vi.mock("../lib/auth", () => ({
  useAuth: () => ({ user: { id: "u", email: "u@x.com" }, loading: false }),
}));

/** Echoes the router search string so tests can assert the URL a
 *  default-view application writes (BoardPql.test.tsx pattern). */
function SearchProbe() {
  const [sp] = useSearchParams();
  return <span data-testid="search">{sp.toString()}</span>;
}

function searchParam(name: string): string | null {
  return new URLSearchParams(
    screen.getByTestId("search").textContent ?? "",
  ).get(name);
}

function listCalls(): URLSearchParams[] {
  return mockState.getPaths
    .filter((p) => p.includes("/issues?"))
    .map((p) => new URLSearchParams(p.split("?")[1] ?? ""));
}

function renderIssues(
  locationState?: unknown,
  initialEntry = "/w/acme/p/ENG",
) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const invalidateSpy = vi.spyOn(client, "invalidateQueries");
  render(
    <MemoryRouter
      initialEntries={[{ pathname: initialEntry.split("?")[0], search: initialEntry.includes("?") ? initialEntry.slice(initialEntry.indexOf("?")) : "", state: locationState ?? null }]}
    >
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
  return { invalidateSpy };
}

beforeEach(() => {
  vi.clearAllMocks();
  mockState.posted = [];
  mockState.views = [];
  mockState.getPaths = [];
});

describe("Issues create dialog date prefill (C26T3)", () => {
  it("opens the dialog with the due date prefilled from location state", async () => {
    renderIssues({ newIssue: true, newIssueTargetDate: "2026-10-15" });
    await screen.findByRole("dialog");
    const due = screen.getByLabelText("Due date") as HTMLInputElement;
    expect(due.value).toBe("2026-10-15");
    const start = screen.getByLabelText("Start date") as HTMLInputElement;
    expect(start.value).toBe("");
  });

  it("sends target_date in the create body and invalidates the calendar caches", async () => {
    const user = userEvent.setup();
    const { invalidateSpy } = renderIssues({
      newIssue: true,
      newIssueTargetDate: "2026-10-15",
    });
    await screen.findByRole("dialog");
    await user.type(screen.getByLabelText("Title"), "Quickadded from calendar");
    await user.click(screen.getByRole("button", { name: "Create issue" }));

    await waitFor(() => expect(mockState.posted).toHaveLength(1));
    expect(mockState.posted[0].path).toBe(`${BASE}/issues`);
    expect(mockState.posted[0].body).toMatchObject({
      name: "Quickadded from calendar",
      target_date: "2026-10-15",
    });
    // The calendar buckets by target/start date — its caches must
    // revalidate so the new chip appears on the day.
    await waitFor(() =>
      expect(invalidateSpy).toHaveBeenCalledWith({
        queryKey: ["calendar", "acme", "ENG"],
      }),
    );
  });

  it("omits target_date from the create body when no date is set", async () => {
    const user = userEvent.setup();
    renderIssues({ newIssue: true });
    await screen.findByRole("dialog");
    await user.type(screen.getByLabelText("Title"), "Undated issue");
    await user.click(screen.getByRole("button", { name: "Create issue" }));

    await waitFor(() => expect(mockState.posted).toHaveLength(1));
    expect(mockState.posted[0].body).toMatchObject({ name: "Undated issue" });
    expect(mockState.posted[0].body).not.toHaveProperty("target_date");
  });
});

describe("default view auto-apply on mount (C106T1)", () => {
  // Same mount sequence as Board: Issues mounts with the project
  // query pending (menu gets projectId ""), the project resolves,
  // the views load, and only then may the default apply. The auth
  // stub's user is "u", so the default must be owned by "u" to be
  // eligible (only own views auto-apply). No test touches the menu
  // except the negative, where opening it proves the views loaded.
  function makeViewRow(
    name: string,
    filters: IssueViewRow["filters"],
    isDefault = false,
  ): IssueViewRow {
    return {
      id: `v-${name}`,
      name,
      filters,
      display: null,
      is_default: isDefault,
      shared: false,
      owner_id: "u",
      owner_name: "",
      created_at: "",
    };
  }

  it("visual default auto-applies on mount and the list refetches with its filters", async () => {
    mockState.views = [
      makeViewRow("Default login", { ...EMPTY_FILTERS, q: "login" }, true),
    ];
    renderIssues();
    await waitFor(() => expect(searchParam("q")).toBe("login"));
    expect(searchParam("pql")).toBeNull();
    // The list's own fetch path carries the default's filters.
    expect(listCalls().some((qs) => qs.get("q") === "login")).toBe(true);
  });

  it("default does NOT apply when the URL already carries filter params", async () => {
    mockState.views = [
      makeViewRow("Default login", { ...EMPTY_FILTERS, q: "login" }, true),
    ];
    renderIssues(null, "/w/acme/p/ENG?q=checkout");
    // Opening the menu proves the views loaded — the effect has had
    // its chance — so the URL below is a settled non-apply.
    fireEvent.click(screen.getByRole("button", { name: "Saved views" }));
    await screen.findByText("Default login");
    expect(searchParam("q")).toBe("checkout");
    expect(searchParam("pql")).toBeNull();
    expect(listCalls().some((qs) => qs.get("q") === "login")).toBe(false);
  });
});
