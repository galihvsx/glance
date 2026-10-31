// @vitest-environment jsdom
// PQL mode on the board (C96T0): the C95 detail-page pattern brought to
// Board — "All issues" / "PQL" toggle + PqlEditor reused verbatim above
// the columns, probe-first apply, display-only filtering. Default mode
// is byte-for-byte today's behavior (visual filters via toApiParams +
// order_by=sort_order, cursor loop, query key includes filters).
// Drag-drop + QuickAdd stay enabled with refetch-on-settle semantics;
// this file covers the mode/fetch contract, not the drag pipeline.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import {
  MemoryRouter,
  Route,
  Routes,
  useNavigate,
} from "react-router-dom";
import Board from "./Board";
import { api, ApiError } from "../lib/api";
import type { Issue, IssueState } from "../lib/types";
import {
  makeQueryClient,
  stubMatchMedia,
} from "../components/shell/test-utils";

// Board renders SavedViewsMenu → useAuth; the real provider needs a
// session — stub the auth context (Issues.test.tsx pattern).
vi.mock("../lib/auth", () => ({
  useAuth: () => ({ user: { id: "u-1", email: "u@x.com" }, loading: false }),
}));

const ENG = "/api/v1/workspaces/acme/projects/ENG";
const OTHER = "/api/v1/workspaces/acme/projects/OTHER";

const states: IssueState[] = [
  {
    id: "s-todo",
    project_id: "p-eng",
    name: "Todo",
    group: "unstarted",
    color: "#888",
    sequence: 1,
    created_at: "",
    updated_at: "",
  },
  {
    id: "s-done",
    project_id: "p-eng",
    name: "Done",
    group: "completed",
    color: "#4ade80",
    sequence: 2,
    created_at: "",
    updated_at: "",
  },
];

function makeIssue(
  id: string,
  displayId: string,
  name: string,
  stateId: string,
  sortOrder: number,
): Issue {
  return {
    id,
    project_id: "p-eng",
    sequence_id: 1,
    display_id: displayId,
    name,
    priority: 3,
    state_id: stateId,
    sort_order: sortOrder,
    is_draft: false,
    created_by: "u-1",
    created_at: "",
    updated_at: "",
    assignees: [],
    labels: [],
  };
}

const issueA = makeIssue("i-a", "ENG-1", "Fix login redirect", "s-todo", 1);
const issueB = makeIssue("i-b", "ENG-2", "Urgent checkout bug", "s-done", 2);
const issueC = makeIssue("i-c", "OTH-1", "Other project task", "s-todo", 1);

function renderBoard() {
  const get = vi.spyOn(api, "get").mockImplementation((path: string) => {
    if (path === "/api/v1/workspaces/acme")
      return Promise.resolve({ role: 15 });
    if (path === ENG)
      return Promise.resolve({ id: "p-eng", name: "ENG" });
    if (path === OTHER)
      return Promise.resolve({ id: "p-other", name: "OTHER" });
    if (path.endsWith("/states")) return Promise.resolve({ states });
    if (path.endsWith("/workflow"))
      return Promise.resolve({ states, transitions: [] });
    if (path.endsWith("/members")) return Promise.resolve({ members: [] });
    if (path.endsWith("/labels")) return Promise.resolve({ labels: [] });
    if (path.endsWith("/estimates")) return Promise.resolve({ estimates: [] });
    if (path.includes("/epics?")) return Promise.resolve({ epics: [] });
    if (path.includes("/projects/") && path.endsWith("/views"))
      return Promise.resolve([]);
    if (path.startsWith(`${OTHER}/issues`))
      return Promise.resolve({ results: [issueC], next_cursor: null });
    if (path.startsWith(`${ENG}/issues`)) {
      const qs = new URLSearchParams(path.split("?")[1] ?? "");
      if (qs.get("pql") !== null) {
        const pql = qs.get("pql") ?? "";
        if (pql.includes("bogus")) {
          return Promise.reject(
            new ApiError(400, "unknown field: bogus", "validation_error", {
              position: 1,
            }),
          );
        }
        // Committed query pages: first page carries the match plus a
        // cursor, second page closes the loop.
        return Promise.resolve(
          qs.get("cursor") === "c1"
            ? { results: [], next_cursor: null }
            : { results: [issueB], next_cursor: "c1" },
        );
      }
      // Visual board: cursor loop across two pages.
      return Promise.resolve(
        qs.get("cursor") === "c1"
          ? { results: [issueB], next_cursor: null }
          : { results: [issueA], next_cursor: "c1" },
      );
    }
    return Promise.reject(new Error(`unexpected GET ${path}`));
  });
  const qc = makeQueryClient();
  render(
    <MemoryRouter initialEntries={["/w/acme/p/ENG/board"]}>
      <QueryClientProvider client={qc}>
        <Routes>
          <Route
            path="/w/:slug/p/:identifier/board"
            element={
              <>
                <Board />
                <GoOther />
              </>
            }
          />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
  return { get };
}

/** Test-only navigation control for the project-change reset test. */
function GoOther() {
  const navigate = useNavigate();
  return (
    <button type="button" onClick={() => navigate("/w/acme/p/OTHER/board")}>
      Go other
    </button>
  );
}

async function openBoard() {
  await screen.findByText("Fix login redirect");
  await screen.findByText("Urgent checkout bug");
}

async function applyPql(query: string) {
  fireEvent.click(screen.getByRole("button", { name: "PQL" }));
  await screen.findByLabelText("PQL query");
  fireEvent.change(screen.getByLabelText("PQL query"), {
    target: { value: query },
  });
  fireEvent.click(screen.getByRole("button", { name: "Apply" }));
}

function issueCalls(get: ReturnType<typeof renderBoard>["get"]) {
  return get.mock.calls
    .map((c) => c[0] as string)
    .filter((p) => p.includes("/issues"))
    .map((p) => new URLSearchParams(p.split("?")[1] ?? ""));
}

/** PageHeader count: the "Board" view-name span inside the breadcrumb
 *  nav (the Views tablist also has a "Board" tab, so disambiguate). */
function headerCountText(): string {
  const label = screen
    .getAllByText("Board")
    .find((el) => el.closest("nav") !== null);
  return label?.parentElement?.textContent ?? "";
}

/** A state column's header title element: card chips carry the same
 *  state name, but only the header's parent reads "Title<count>". */
function columnHeader(title: string) {
  const header = screen
    .getAllByText(title)
    .find((el) => new RegExp(`^${title}\\d`).test(el.parentElement?.textContent ?? ""));
  return header ?? null;
}

beforeEach(() => {
  stubMatchMedia(false);
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("board PQL mode (C96T0)", () => {
  it("default mode shows the visual board with no PQL editor, fetched via the cursor loop", async () => {
    const { get } = renderBoard();
    await openBoard();
    expect(screen.queryByLabelText("PQL query")).toBeNull();
    expect(
      screen.getByRole("button", { name: "All issues" }),
    ).toHaveAttribute("aria-pressed", "true");
    expect(headerCountText()).toContain("Board2");
    // Visual fetch shape: order_by sort_order, per_page 100, no pql,
    // and the cursor loop ran (a second page was fetched with c1).
    const calls = issueCalls(get);
    expect(calls.length).toBeGreaterThan(0);
    for (const qs of calls) {
      expect(qs.get("pql")).toBeNull();
      expect(qs.get("order_by")).toBe("sort_order");
      expect(qs.get("per_page")).toBe("100");
    }
    expect(calls.some((qs) => qs.get("cursor") === "c1")).toBe(true);
  });

  it("committed query fetches pql + sort_order via the cursor loop and shows only matches", async () => {
    const { get } = renderBoard();
    await openBoard();
    await applyPql("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("Fix login redirect")).toBeNull();
    });
    expect(screen.getByText("Urgent checkout bug")).toBeTruthy();
    const pqlCalls = issueCalls(get).filter((qs) => qs.get("pql") !== null);
    // Probe (1) + committed cursor loop (2 pages).
    expect(pqlCalls.length).toBe(3);
    for (const qs of pqlCalls) {
      expect(qs.get("pql")).toBe("priority = High");
      expect(qs.get("order_by")).toBe("sort_order");
      expect(qs.get("per_page")).toBe("100");
    }
    expect(pqlCalls.some((qs) => qs.get("cursor") === "c1")).toBe(true);
  });

  it("LIMIT query skips the cursor loop: probe + one committed fetch, never a cursor page", async () => {
    const { get } = renderBoard();
    await openBoard();
    await applyPql("priority = High LIMIT 2");
    await waitFor(() => {
      expect(screen.queryByText("Fix login redirect")).toBeNull();
    });
    expect(screen.getByText("Urgent checkout bug")).toBeTruthy();
    const pqlCalls = issueCalls(get).filter((qs) => qs.get("pql") !== null);
    // Probe (1) + single committed fetch (1). The stub offers a
    // cursor on the first PQL page, but a LIMIT query never follows
    // it — that is the client-side half of the single-page contract.
    expect(pqlCalls.length).toBe(2);
    for (const qs of pqlCalls) {
      expect(qs.get("pql")).toBe("priority = High LIMIT 2");
      expect(qs.get("cursor")).toBeNull();
    }
  });

  it("PQL editor on the board hides the save hint (C96T1: board Views save visual filters only, not PQL)", async () => {
    renderBoard();
    await openBoard();
    fireEvent.click(screen.getByRole("button", { name: "PQL" }));
    await screen.findByLabelText("PQL query");
    expect(screen.queryByText(/To save this query, use Views/)).toBeNull();
  });

  it("probe-first rejection shows the editor inline error and leaves the board untouched", async () => {
    const { get } = renderBoard();
    await openBoard();
    await applyPql("bogus = 1");
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("unknown field: bogus");
    expect(alert.textContent).toContain("at column 1");
    // Board untouched: both cards still on screen, header count 2.
    expect(screen.getByText("Fix login redirect")).toBeTruthy();
    expect(screen.getByText("Urgent checkout bug")).toBeTruthy();
    expect(headerCountText()).toContain("Board2");
    // Only the probe ran — no committed query was ever fetched.
    const pqlCalls = issueCalls(get).filter((qs) => qs.get("pql") !== null);
    expect(pqlCalls.length).toBe(1);
  });

  it("grouping and header count follow the displayed list in PQL mode", async () => {
    renderBoard();
    await openBoard();
    await applyPql("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("Fix login redirect")).toBeNull();
    });
    // Header count dropped to the filtered total.
    expect(headerCountText()).toContain("Board1");
    // State grouping follows: Todo column is empty (count 0), Done
    // still holds the match.
    expect(columnHeader("Todo")?.parentElement?.textContent).toContain(
      "Todo0",
    );
    expect(columnHeader("Done")?.parentElement?.textContent).toContain(
      "Done1",
    );
  });

  it("leaving PQL mode restores the visual board and closes the editor", async () => {
    renderBoard();
    await openBoard();
    await applyPql("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("Fix login redirect")).toBeNull();
    });
    fireEvent.click(screen.getByRole("button", { name: "All issues" }));
    await screen.findByText("Fix login redirect");
    expect(screen.getByText("Urgent checkout bug")).toBeTruthy();
    expect(screen.queryByLabelText("PQL query")).toBeNull();
    expect(headerCountText()).toContain("Board2");
  });

  it("Clear in PQL mode restores the visual board without leaving the mode", async () => {
    renderBoard();
    await openBoard();
    await applyPql("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("Fix login redirect")).toBeNull();
    });
    fireEvent.click(screen.getByRole("button", { name: "Clear" }));
    await screen.findByText("Fix login redirect");
    expect(screen.getByText("Urgent checkout bug")).toBeTruthy();
    expect(screen.getByLabelText("PQL query")).toBeTruthy();
    expect(headerCountText()).toContain("Board2");
  });

  it("PQL state resets when the project changes so a query never leaks across projects", async () => {
    const { get } = renderBoard();
    await openBoard();
    await applyPql("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("Fix login redirect")).toBeNull();
    });
    fireEvent.click(screen.getByRole("button", { name: "Go other" }));
    await screen.findByText("Other project task");
    // Mode + query dropped: editor gone, All issues pressed, and no
    // PQL fetch was ever issued against the other project.
    expect(screen.queryByLabelText("PQL query")).toBeNull();
    expect(
      screen.getByRole("button", { name: "All issues" }),
    ).toHaveAttribute("aria-pressed", "true");
    const otherPqlCalls = get.mock.calls
      .map((c) => c[0] as string)
      .filter((p) => p.startsWith(`${OTHER}/issues`) && p.includes("pql="));
    expect(otherPqlCalls).toEqual([]);
  });
});
