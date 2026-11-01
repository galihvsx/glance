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
  useSearchParams,
} from "react-router-dom";
import Board from "./Board";
import { api, ApiError } from "../lib/api";
import { EMPTY_FILTERS } from "../lib/filters";
import type { IssueViewRow } from "../lib/savedViews";
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

/** Echoes the router search string so tests can assert the URL a view
 *  application (or a mode exit) writes. MemoryRouter never touches
 *  window.location. */
function SearchProbe() {
  const [sp] = useSearchParams();
  return <span data-testid="search">{sp.toString()}</span>;
}

function searchParam(name: string): string | null {
  return new URLSearchParams(
    screen.getByTestId("search").textContent ?? "",
  ).get(name);
}

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
    owner_id: "u-1",
    owner_name: "",
    created_at: "",
  };
}

function renderBoard(opts: {
  initialEntry?: string;
  views?: IssueViewRow[];
  /** Per-project views, keyed by the project id in the views path
   *  (e.g. "p-eng") — for the project-switch remount test. */
  viewsForProjectId?: (projectId: string) => IssueViewRow[];
} = {}) {
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
    if (path.includes("/projects/") && path.endsWith("/views")) {
      const projectId = path.split("/projects/")[1]?.split("/")[0] ?? "";
      return Promise.resolve({
        views: opts.viewsForProjectId
          ? opts.viewsForProjectId(projectId)
          : (opts.views ?? []),
      });
    }
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
  // Saved-view creation echoes the posted body back as the created row.
  const post = vi
    .spyOn(api, "post")
    .mockImplementation((path: string, body: unknown) => {
      if (path.endsWith("/views")) {
        const b = body as { name: string; filters: IssueViewRow["filters"]; display: IssueViewRow["display"] };
        return Promise.resolve({
          id: "v-new",
          name: b.name,
          filters: b.filters,
          display: b.display,
          is_default: false,
          shared: false,
          owner_id: "u-1",
          owner_name: "",
          created_at: "",
        });
      }
      return Promise.reject(new Error(`unexpected POST ${path}`));
    });
  const qc = makeQueryClient();
  render(
    <MemoryRouter
      initialEntries={[opts.initialEntry ?? "/w/acme/p/ENG/board"]}
    >
      <QueryClientProvider client={qc}>
        <Routes>
          <Route
            path="/w/:slug/p/:identifier/board"
            element={
              <>
                <Board />
                <GoOther />
                <SearchProbe />
              </>
            }
          />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
  return { get, post };
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

  it("PQL editor on the board hides the save hint", async () => {
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

async function openViewsMenu() {
  fireEvent.click(screen.getByRole("button", { name: "Saved views" }));
}

async function saveViewAs(name: string) {
  await screen.findByRole("button", { name: "Save current view" });
  fireEvent.click(screen.getByRole("button", { name: "Save current view" }));
  fireEvent.change(screen.getByPlaceholderText("View name…"), {
    target: { value: name },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save view" }));
}

describe("board PQL saved views (C105T0)", () => {
  const pqlView = () =>
    makeViewRow("High priority", {
      ...EMPTY_FILTERS,
      pql: "priority = High",
    });
  const visualView = () => makeViewRow("My bugs", EMPTY_FILTERS);

  it("saving in PQL mode POSTs filters carrying ONLY the query (visual dimensions dropped)", async () => {
    const { post } = renderBoard({
      // A visual filter sits in the URL while in PQL mode — Board
      // never parks, so it is still there at save time and must NOT
      // leak into the saved PQL view.
      initialEntry: "/w/acme/p/ENG/board?q=login",
    });
    await openBoard();
    await applyPql("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("Fix login redirect")).toBeNull();
    });
    await openViewsMenu();
    await saveViewAs("PQL saved");
    await waitFor(() => expect(post).toHaveBeenCalledTimes(1));
    const body = post.mock.calls[0][1] as {
      filters: typeof EMPTY_FILTERS;
    };
    expect(body.filters.pql).toBe("priority = High");
    expect(body.filters.q).toBe("");
    expect(body.filters.state).toBe("");
    expect(body.filters.priorities).toEqual([]);
    expect(body.filters.labels).toEqual([]);
  });

  it("applying a PQL view enters PQL mode and the PQL fetch carries that query", async () => {
    const { get } = renderBoard({ views: [pqlView(), visualView()] });
    await openBoard();
    await openViewsMenu();
    await screen.findByText("High priority");
    fireEvent.click(screen.getByTitle("Apply view “High priority”"));
    // Editor mounts with the view's query; the board shows only the
    // PQL match, not the visual board. Wait for the match itself:
    // waiting for the other card to disappear passes vacuously while
    // the board refetches under its new filter key (both queries
    // pending → empty list), before the PQL result has settled.
    await screen.findByText("Urgent checkout bug");
    expect(screen.queryByText("Fix login redirect")).toBeNull();
    expect(
      screen.getByRole("button", { name: "PQL" }),
    ).toHaveAttribute("aria-pressed", "true");
    expect(
      (screen.getByLabelText("PQL query") as HTMLInputElement).value,
    ).toBe("priority = High");
    const pqlCalls = issueCalls(get).filter((qs) => qs.get("pql") !== null);
    expect(pqlCalls.length).toBeGreaterThan(0);
    for (const qs of pqlCalls) {
      expect(qs.get("pql")).toBe("priority = High");
    }
  });

  it("applying a visual view exits PQL mode and the visual board returns", async () => {
    renderBoard({ views: [pqlView(), visualView()] });
    await openBoard();
    await applyPql("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("Fix login redirect")).toBeNull();
    });
    await openViewsMenu();
    await screen.findByText("My bugs");
    fireEvent.click(screen.getByTitle("Apply view “My bugs”"));
    await screen.findByText("Fix login redirect");
    expect(screen.getByText("Urgent checkout bug")).toBeTruthy();
    expect(screen.queryByLabelText("PQL query")).toBeNull();
    expect(
      screen.getByRole("button", { name: "All issues" }),
    ).toHaveAttribute("aria-pressed", "true");
    expect(searchParam("pql")).toBeNull();
  });

  it("a pasted ?pql= enters PQL mode (backstop)", async () => {
    renderBoard({
      initialEntry: "/w/acme/p/ENG/board?pql=priority%20%3D%20High",
    });
    // Positive wait: the visual card never appears here (the board
    // query itself carries the pasted pql), so waiting for its
    // absence would pass before any data loads.
    await screen.findByText("Urgent checkout bug");
    expect(screen.queryByText("Fix login redirect")).toBeNull();
    expect(
      screen.getByRole("button", { name: "PQL" }),
    ).toHaveAttribute("aria-pressed", "true");
    expect(
      (screen.getByLabelText("PQL query") as HTMLInputElement).value,
    ).toBe("priority = High");
  });

  it("exiting PQL mode clears ?pql= from the URL with no re-entry", async () => {
    renderBoard({
      initialEntry: "/w/acme/p/ENG/board?pql=priority%20%3D%20High",
    });
    await screen.findByText("Urgent checkout bug");
    expect(
      screen.getByRole("button", { name: "PQL" }),
    ).toHaveAttribute("aria-pressed", "true");
    fireEvent.click(screen.getByRole("button", { name: "All issues" }));
    await screen.findByText("Fix login redirect");
    expect(screen.getByText("Urgent checkout bug")).toBeTruthy();
    expect(screen.queryByLabelText("PQL query")).toBeNull();
    // The invariant: no non-blank ?pql= survives the exit, so the
    // backstop cannot re-enter PQL mode after the board settles.
    expect(searchParam("pql")).toBeNull();
    expect(
      screen.getByRole("button", { name: "All issues" }),
    ).toHaveAttribute("aria-pressed", "true");
  });

  it("exiting after applying a PQL view clears ?pql= with no re-entry", async () => {
    renderBoard({ views: [pqlView(), visualView()] });
    await openBoard();
    await openViewsMenu();
    await screen.findByText("High priority");
    fireEvent.click(screen.getByTitle("Apply view “High priority”"));
    await screen.findByText("Urgent checkout bug");
    expect(searchParam("pql")).toBe("priority = High");
    // The exit-time hazard: the URL clear commits separately from
    // the toggle's state update, so a backstop that re-ran on the
    // mode change would restore the stale query here.
    fireEvent.click(screen.getByRole("button", { name: "All issues" }));
    await screen.findByText("Fix login redirect");
    expect(screen.queryByLabelText("PQL query")).toBeNull();
    expect(searchParam("pql")).toBeNull();
    expect(
      screen.getByRole("button", { name: "All issues" }),
    ).toHaveAttribute("aria-pressed", "true");
  });

  it("visual-mode save is unchanged: visual dimensions saved, no pql", async () => {
    const { post } = renderBoard({
      initialEntry: "/w/acme/p/ENG/board?q=login",
    });
    await openBoard();
    await openViewsMenu();
    await saveViewAs("Visual saved");
    await waitFor(() => expect(post).toHaveBeenCalledTimes(1));
    const body = post.mock.calls[0][1] as {
      filters: typeof EMPTY_FILTERS;
    };
    expect(body.filters.q).toBe("login");
    expect(body.filters.pql).toBeUndefined();
  });

  it("visual-mode apply is unchanged: stays visual, view filters land in the URL", async () => {
    const qView = makeViewRow("Checkout search", {
      ...EMPTY_FILTERS,
      q: "checkout",
    });
    renderBoard({ views: [qView] });
    await openBoard();
    await openViewsMenu();
    await screen.findByText("Checkout search");
    fireEvent.click(screen.getByTitle("Apply view “Checkout search”"));
    await waitFor(() => expect(searchParam("q")).toBe("checkout"));
    expect(searchParam("pql")).toBeNull();
    expect(screen.queryByLabelText("PQL query")).toBeNull();
    expect(
      screen.getByRole("button", { name: "All issues" }),
    ).toHaveAttribute("aria-pressed", "true");
  });

  // Default-flagged views apply exactly like any other view through
  // the menu (the isDefault flag changes nothing about apply). The
  // menu's auto-apply-on-mount path was a pre-existing SavedViewsMenu
  // race shared by every page (the effect burned its once-flag on
  // mount while projectId was still ""); C106T0 gated it on
  // viewsReady, and the C106T1 describe below proves the fix in the
  // real Board mount sequence (project query pending → resolves →
  // views load → default applies).
  it("default visual view applies visually when chosen (regression)", async () => {
    const def = makeViewRow(
      "Default bugs",
      { ...EMPTY_FILTERS, q: "login" },
      true,
    );
    renderBoard({ views: [def] });
    await openBoard();
    await openViewsMenu();
    await screen.findByText("Default bugs");
    fireEvent.click(screen.getByTitle("Apply view “Default bugs”"));
    await waitFor(() => expect(searchParam("q")).toBe("login"));
    expect(searchParam("pql")).toBeNull();
    expect(screen.queryByLabelText("PQL query")).toBeNull();
    expect(
      screen.getByRole("button", { name: "All issues" }),
    ).toHaveAttribute("aria-pressed", "true");
  });

  it("default PQL view applies into PQL mode when chosen", async () => {
    const def = makeViewRow(
      "Default high",
      { ...EMPTY_FILTERS, pql: "priority = High" },
      true,
    );
    renderBoard({ views: [def] });
    await openBoard();
    await openViewsMenu();
    await screen.findByText("Default high");
    fireEvent.click(screen.getByTitle("Apply view “Default high”"));
    await screen.findByText("Urgent checkout bug");
    expect(screen.queryByText("Fix login redirect")).toBeNull();
    expect(
      screen.getByRole("button", { name: "PQL" }),
    ).toHaveAttribute("aria-pressed", "true");
    expect(
      (screen.getByLabelText("PQL query") as HTMLInputElement).value,
    ).toBe("priority = High");
  });
});

describe("default view auto-apply on mount (C106T1)", () => {
  // The mount sequence under test is the real one, not a mocked
  // projectId flip: Board mounts with the project query pending
  // (SavedViewsMenu gets projectId "" and its views query stays
  // disabled), the project resolves, the views load, and only then
  // may the default apply. Pre-C106T0 the menu's effect burned its
  // once-flag on the empty first mount and the default never applied;
  // these tests fail without the viewsReady gate and pass with it.
  // No test below touches the Views menu — the apply must happen
  // on its own.

  it("PQL default auto-applies on mount and enters PQL mode visibly", async () => {
    const { get } = renderBoard({
      views: [
        makeViewRow(
          "Default high",
          { ...EMPTY_FILTERS, pql: "priority = High" },
          true,
        ),
      ],
    });
    // The URL is the first observable: the menu writes ?pql= before
    // the board refetches under it.
    await waitFor(() => expect(searchParam("pql")).toBe("priority = High"));
    expect(
      screen.getByRole("button", { name: "PQL" }),
    ).toHaveAttribute("aria-pressed", "true");
    expect(
      (screen.getByLabelText("PQL query") as HTMLInputElement).value,
    ).toBe("priority = High");
    // The board itself reflects the default: only the PQL match
    // remains (the stub returns just issueB for PQL fetches). The
    // waitFor retries past the transient visual board and the pending
    // PQL fetch — both cards, or neither, until the PQL result lands.
    await waitFor(() => {
      expect(screen.getByText("Urgent checkout bug")).toBeTruthy();
      expect(screen.queryByText("Fix login redirect")).toBeNull();
    });
    const pqlCalls = issueCalls(get).filter((qs) => qs.get("pql") !== null);
    expect(pqlCalls.length).toBeGreaterThan(0);
    for (const qs of pqlCalls) {
      expect(qs.get("pql")).toBe("priority = High");
    }
  });

  it("visual default auto-applies on mount and the board refetches with its filters", async () => {
    const { get } = renderBoard({
      views: [
        makeViewRow(
          "Default login",
          { ...EMPTY_FILTERS, q: "login" },
          true,
        ),
      ],
    });
    await openBoard();
    await waitFor(() => expect(searchParam("q")).toBe("login"));
    expect(searchParam("pql")).toBeNull();
    expect(
      screen.getByRole("button", { name: "All issues" }),
    ).toHaveAttribute("aria-pressed", "true");
    // The board's own fetch path carries the default's filters (the
    // stub ignores q for row selection, so the URL + fetch params are
    // the observable — the PQL test above proves visible filtering).
    const qCalls = issueCalls(get).filter((qs) => qs.get("q") === "login");
    expect(qCalls.length).toBeGreaterThan(0);
  });

  it("default does NOT apply when the URL already carries filter params", async () => {
    const { get } = renderBoard({
      views: [
        makeViewRow(
          "Default login",
          { ...EMPTY_FILTERS, q: "login" },
          true,
        ),
      ],
      initialEntry: "/w/acme/p/ENG/board?q=checkout",
    });
    await openBoard();
    // Opening the menu proves the views loaded — the effect has had
    // its chance — so the URL below is a settled non-apply, not a
    // race between the assertion and the fetch.
    await openViewsMenu();
    await screen.findByText("Default login");
    expect(searchParam("q")).toBe("checkout");
    expect(searchParam("pql")).toBeNull();
    expect(
      issueCalls(get).some((qs) => qs.get("q") === "login"),
    ).toBe(false);
  });

  it("project switch remounts the menu and applies the new project's default fresh (inv 8)", async () => {
    const { get } = renderBoard({
      viewsForProjectId: (projectId) =>
        projectId === "p-other"
          ? [
              makeViewRow(
                "Other default",
                { ...EMPTY_FILTERS, q: "other-search" },
                true,
              ),
            ]
          : [
              makeViewRow(
                "Default high",
                { ...EMPTY_FILTERS, pql: "priority = High" },
                true,
              ),
            ],
    });
    await openBoard();
    // ENG's default applied on the first mount.
    await waitFor(() => expect(searchParam("pql")).toBe("priority = High"));
    fireEvent.click(screen.getByRole("button", { name: "Go other" }));
    await screen.findByText("Other project task");
    // The menu remounts (keyed by slug:identifier), so its once-flag
    // is fresh and OTHER's own default applies — it must not inherit
    // ENG's spent flag or ENG's PQL query.
    await waitFor(() => expect(searchParam("q")).toBe("other-search"));
    expect(searchParam("pql")).toBeNull();
    const otherCalls = get.mock.calls
      .map((c) => c[0] as string)
      .filter((p) => p.startsWith(`${OTHER}/issues`))
      .map((p) => new URLSearchParams(p.split("?")[1] ?? ""));
    expect(otherCalls.some((qs) => qs.get("q") === "other-search")).toBe(
      true,
    );
  });
});
