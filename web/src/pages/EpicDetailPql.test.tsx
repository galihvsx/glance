// @vitest-environment jsdom
// PQL mode on the epic detail member-issues list (C108T2): toggle
// on/off, PQL-mode fetch params (epic scope + pql both present),
// invalid-PQL error display with the list intact, re-apply
// re-seeding the editor (the C107 key lesson), remove invalidating
// both the membership and the PQL query, and progress / section
// count staying on the unfiltered membership. Follows the
// CyclesPql.test.tsx shape. C108T3 adds the detail-mode
// SavedViewsMenu (mocked hook, Modules pattern) and the menu-apply
// path below.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import EpicDetail from "./EpicDetail";
import { api, ApiError } from "../lib/api";
import { EMPTY_FILTERS } from "../lib/filters";
// C108T3: the detail-mode menu reads views through this hook; mock it
// (same pattern as Modules.pql.test.tsx) so page tests control the
// view list without a views endpoint. The default is no views, so the
// pre-C108T3 tests below render the menu's empty state untouched.
vi.mock("../components/issue/useSavedViews", () => ({
  useSavedViews: vi.fn(),
}));
import { useSavedViews } from "../components/issue/useSavedViews";
import type { SavedView } from "../components/issue/useSavedViews";
import type {
  Epic,
  Initiative,
  InitiativeEpic,
  Issue,
  IssueState,
} from "../lib/types";
import {
  makeQueryClient,
  stubMatchMedia,
} from "../components/shell/test-utils";

const mockUseSavedViews = vi.mocked(useSavedViews);

function mockViews(
  ownViews: SavedView[],
  sharedViews: SavedView[] = [],
) {
  mockUseSavedViews.mockReturnValue({
    views: [...ownViews, ...sharedViews],
    ownViews,
    sharedViews,
    viewsLoading: false,
    viewsReady: true,
    viewsError: false,
    createView: vi.fn(),
    renameView: vi.fn(),
    deleteView: vi.fn(),
    setDefaultView: vi.fn(),
    setViewShared: vi.fn(),
  } as ReturnType<typeof useSavedViews>);
}

const BASE = "/api/v1/workspaces/acme/projects/ENG";
const EPICS = `${BASE}/epics`;

const states: IssueState[] = [
  { id: "s-backlog", name: "Backlog", group: "backlog" },
  { id: "s-started", name: "In Progress", group: "started" },
  { id: "s-done", name: "Done", group: "completed" },
] as IssueState[];

function makeIssue(id: string, displayId: string, name: string): Issue {
  return {
    id,
    project_id: "p1",
    sequence_id: 1,
    display_id: displayId,
    name,
    priority: 2,
    state_id: "s-started",
    sort_order: 1,
    is_draft: false,
    created_by: "u1",
    created_at: "",
    updated_at: "",
    assignees: [],
    labels: [],
  } as Issue;
}

const issue1 = makeIssue("i1", "ENG-1", "First issue");
const issue2 = makeIssue("i2", "ENG-2", "Second issue");
const issue3 = makeIssue("i3", "ENG-3", "Third issue");

const epic: Epic = {
  id: "epic-1",
  project_id: "p1",
  name: "Auth revamp",
  description: "Passwordless OTP + OAuth",
  archived_at: null,
  issue_count: 2,
  progress: {
    total: 12,
    by_group: { backlog: 2, unstarted: 3, started: 4, completed: 3, cancelled: 0 },
  },
  created_at: "2026-10-10T00:00:00Z",
  updated_at: "2026-10-10T00:00:00Z",
};

const initiative: Initiative = {
  id: "init-1",
  workspace_id: "w1",
  identifier: "ENG",
  name: "Q4 Platform Bets",
  description: null,
  status: "active",
  project_count: 3,
  created_at: "",
  updated_at: "",
} as Initiative;

function renderDetail(opts: {
  removed?: Set<string>;
  ownViews?: SavedView[];
  sharedViews?: SavedView[];
} = {}) {
  mockViews(opts.ownViews ?? [], opts.sharedViews ?? []);
  // Stateful mock: membership and the PQL-filtered result drop a
  // removed issue only after a remove, so refetching both queries
  // after a remove is observable in the rendered lists.
  const removed = opts.removed ?? new Set<string>();
  const get = vi.spyOn(api, "get").mockImplementation((path: string) => {
    if (path === `${EPICS}/epic-1`) return Promise.resolve(epic);
    // Project query for the detail-mode SavedViewsMenu (shared
    // ["project", slug, identifier] key).
    if (path === BASE) return Promise.resolve({ id: "p1" });
    if (path === `${EPICS}/epic-1/issues`)
      return Promise.resolve({
        issues: [issue1, issue2].filter((i) => !removed.has(i.id)),
      });
    if (path === `${BASE}/states`) return Promise.resolve({ states });
    if (path === "/api/v1/workspaces/acme/initiatives")
      return Promise.resolve({ initiatives: [initiative] });
    if (path === "/api/v1/workspaces/acme/initiatives/init-1/epics")
      return Promise.resolve({
        epics: [{ epic_id: "epic-1" } as InitiativeEpic],
      });
    if (path.startsWith(`${BASE}/issues`)) {
      const qs = new URLSearchParams(path.split("?")[1] ?? "");
      const pql = qs.get("pql");
      if (pql !== null) {
        if (pql.includes("bogus")) {
          return Promise.reject(
            new ApiError(400, "unknown field: bogus", "validation_error", {
              position: 1,
            }),
          );
        }
        // Distinct scoped result for a second query, so a re-apply
        // while already in PQL mode is observable in the list as
        // well as in the editor draft.
        if (pql.includes("Low")) {
          return Promise.resolve({
            results: [issue1].filter((i) => !removed.has(i.id)),
            next_cursor: null,
          });
        }
        return Promise.resolve({
          results: [issue2].filter((i) => !removed.has(i.id)),
          next_cursor: null,
        });
      }
      // Board-issues fetch for the add dialog: all project issues.
      return Promise.resolve({
        results: [issue1, issue2, issue3],
        next_cursor: null,
      });
    }
    return Promise.reject(new Error(`unexpected GET ${path}`));
  });
  const qc = makeQueryClient();
  render(
    <MemoryRouter initialEntries={["/w/acme/p/ENG/epics/epic-1"]}>
      <QueryClientProvider client={qc}>
        <Routes>
          <Route
            path="/w/:slug/p/:identifier/epics/:epicID"
            element={<EpicDetail />}
          />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
  return { get, removed };
}

async function openPqlAndApply(query: string) {
  fireEvent.click(screen.getByRole("button", { name: "PQL" }));
  await screen.findByLabelText("PQL query");
  fireEvent.change(screen.getByLabelText("PQL query"), {
    target: { value: query },
  });
  fireEvent.click(screen.getByRole("button", { name: "Apply" }));
}

function projectIssueCalls(get: ReturnType<typeof renderDetail>["get"]) {
  return get.mock.calls
    .map((c) => c[0] as string)
    .filter((p) => p.startsWith(`${BASE}/issues`))
    .map((p) => new URLSearchParams(p.split("?")[1] ?? ""));
}

function sectionCount(): string | null {
  // The Issues section head renders title + count in one row; the
  // count is the unfiltered membership size, never the filtered
  // list size.
  const heading = screen.getByRole("heading", { name: "Issues" });
  return heading.parentElement ? heading.parentElement.textContent : null;
}

beforeEach(() => {
  mockViews([]);
  stubMatchMedia(false);
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("epic detail PQL mode (C108T2)", () => {
  it("default mode shows the full membership list with no PQL editor", async () => {
    const { get } = renderDetail();
    await screen.findByText("First issue");
    expect(screen.getByText("Second issue")).toBeTruthy();
    expect(screen.queryByLabelText("PQL query")).toBeNull();
    expect(
      screen.getByRole("button", { name: "All issues" }),
    ).toHaveAttribute("aria-pressed", "true");
    // No PQL fetch in default mode.
    expect(
      projectIssueCalls(get).filter((qs) => qs.get("pql") !== null),
    ).toHaveLength(0);
  });

  it("PQL-mode fetch carries both epic scope and pql, and shows only the filtered list", async () => {
    const { get } = renderDetail();
    await screen.findByText("First issue");
    await openPqlAndApply("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("First issue")).toBeNull();
    });
    expect(screen.getByText("Second issue")).toBeTruthy();
    // The editor shows the applied query.
    expect(
      (screen.getByLabelText("PQL query") as HTMLTextAreaElement).value,
    ).toBe("priority = High");
    const pqlCalls = projectIssueCalls(get).filter(
      (qs) => qs.get("pql") !== null,
    );
    expect(pqlCalls.length).toBeGreaterThan(0);
    for (const qs of pqlCalls) {
      expect(qs.get("epic")).toBe("epic-1");
      expect(qs.get("pql")).toBe("priority = High");
    }
  });

  it("invalid PQL shows the editor error with column and leaves the list intact", async () => {
    renderDetail();
    await screen.findByText("First issue");
    await openPqlAndApply("bogus = 1");
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("unknown field: bogus");
    expect(alert.textContent).toContain("at column 1");
    // List untouched: both membership issues still on screen, and
    // the failed query stays in the editor for correction.
    expect(screen.getByText("First issue")).toBeTruthy();
    expect(screen.getByText("Second issue")).toBeTruthy();
    expect(
      (screen.getByLabelText("PQL query") as HTMLTextAreaElement).value,
    ).toBe("bogus = 1");
    // No unhandled rejection: the editor catches the probe failure
    // internally (a rejection here would fail the run).
  });

  it("leaving PQL mode restores the full list and closes the editor", async () => {
    renderDetail();
    await screen.findByText("First issue");
    await openPqlAndApply("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("First issue")).toBeNull();
    });
    fireEvent.click(screen.getByRole("button", { name: "All issues" }));
    await screen.findByText("First issue");
    expect(screen.getByText("Second issue")).toBeTruthy();
    expect(screen.queryByLabelText("PQL query")).toBeNull();
    expect(
      screen.getByRole("button", { name: "All issues" }),
    ).toHaveAttribute("aria-pressed", "true");
  });

  it("Clear in PQL mode restores the full list without leaving the mode", async () => {
    renderDetail();
    await screen.findByText("First issue");
    await openPqlAndApply("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("First issue")).toBeNull();
    });
    fireEvent.click(screen.getByRole("button", { name: "Clear" }));
    await screen.findByText("First issue");
    expect(screen.getByText("Second issue")).toBeTruthy();
    expect(screen.getByLabelText("PQL query")).toBeTruthy();
  });

  it("re-applying a second query while in PQL mode re-seeds the editor", async () => {
    renderDetail();
    await screen.findByText("First issue");
    await openPqlAndApply("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("First issue")).toBeNull();
    });
    expect(
      (screen.getByLabelText("PQL query") as HTMLTextAreaElement).value,
    ).toBe("priority = High");
    // Already in PQL mode: the editor is mounted, so without the
    // applied-query key its draft would stay on the first query.
    fireEvent.change(screen.getByLabelText("PQL query"), {
      target: { value: "priority = Low" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Apply" }));
    await waitFor(() => {
      expect(screen.queryByText("Second issue")).toBeNull();
    });
    expect(screen.getByText("First issue")).toBeTruthy();
    expect(
      (screen.getByLabelText("PQL query") as HTMLTextAreaElement).value,
    ).toBe("priority = Low");
  });

  it("the applied-query key re-seeds the draft to the trimmed query", async () => {
    // Negative check for the key: the probe trims, so the committed
    // query differs from the typed draft by whitespace. Without the
    // key the editor would keep the untrimmed draft; with it the
    // remount seeds the draft to the trimmed applied query.
    renderDetail();
    await screen.findByText("First issue");
    await openPqlAndApply("priority = High ");
    await waitFor(() => {
      expect(screen.queryByText("First issue")).toBeNull();
    });
    expect(
      (screen.getByLabelText("PQL query") as HTMLTextAreaElement).value,
    ).toBe("priority = High");
  });

  it("progress card and section count still read the unfiltered membership", async () => {
    renderDetail();
    await screen.findByText("First issue");
    // done = completed + cancelled = 3 + 0, of 12 total.
    expect(screen.getByText(/3 of 12 issues done/i)).toBeTruthy();
    expect(sectionCount()).toContain("2");
    await openPqlAndApply("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("First issue")).toBeNull();
    });
    // Filtered list has 1 issue, but progress and count are unchanged.
    expect(screen.getByText(/3 of 12 issues done/i)).toBeTruthy();
    expect(sectionCount()).toContain("2");
  });

  it("removing a row in PQL mode invalidates both the membership and the PQL query", async () => {
    const { removed } = renderDetail();
    const del = vi
      .spyOn(api, "del")
      .mockImplementation((_path: string, body?: unknown) => {
        const ids = (body as { issue_ids?: string[] } | undefined)
          ?.issue_ids;
        if (ids) ids.forEach((id) => removed.add(id));
        return Promise.resolve({ removed: 1 });
      });
    await screen.findByText("First issue");
    await openPqlAndApply("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("First issue")).toBeNull();
    });
    expect(screen.getByText("Second issue")).toBeTruthy();

    fireEvent.click(
      screen.getByRole("button", { name: "Remove ENG-2 from epic" }),
    );
    await waitFor(() => expect(del).toHaveBeenCalledTimes(1));

    // PQL query re-ran: the removed row leaves the filtered list,
    // which is now a real empty PQL result.
    await screen.findByText("No issues match this PQL query");
    // Membership query re-ran too: leaving PQL mode shows the
    // membership list without the removed issue.
    fireEvent.click(screen.getByRole("button", { name: "All issues" }));
    await screen.findByText("First issue");
    expect(screen.queryByText("Second issue")).toBeNull();
  });
});

describe("epic detail saved views (C108T3)", () => {
  const savedPql: SavedView = {
    id: "v-pql",
    name: "High priority",
    filters: { ...EMPTY_FILTERS, pql: "priority = High" },
    display: null,
    isDefault: false,
    shared: false,
    ownerId: "me",
    ownerName: "Me",
  };
  const savedBogus: SavedView = {
    id: "v-bogus",
    name: "Bogus query",
    filters: { ...EMPTY_FILTERS, pql: "bogus = 1" },
    display: null,
    isDefault: false,
    shared: false,
    ownerId: "me",
    ownerName: "Me",
  };
  const savedVisual: SavedView = {
    id: "v-vis",
    name: "My bugs",
    filters: EMPTY_FILTERS,
    display: null,
    isDefault: false,
    shared: false,
    ownerId: "me",
    ownerName: "Me",
  };
  const sharedVisual: SavedView = {
    id: "v-sh-vis",
    name: "Team visual",
    filters: EMPTY_FILTERS,
    display: null,
    isDefault: false,
    shared: true,
    ownerId: "user-2",
    ownerName: "Bob",
  };
  const savedDefaultPql: SavedView = {
    id: "v-def",
    name: "Default pql",
    filters: { ...EMPTY_FILTERS, pql: "state = Done" },
    display: null,
    isDefault: true,
    shared: false,
    ownerId: "me",
    ownerName: "Me",
  };

  async function openSavedViewsMenu() {
    fireEvent.click(screen.getByRole("button", { name: "Saved views" }));
    await screen.findByRole("button", { name: "Save current view" });
  }

  it("applying a saved PQL view narrows the list and shows the query in the editor", async () => {
    const { get } = renderDetail({ ownViews: [savedPql] });
    await screen.findByText("First issue");
    await openSavedViewsMenu();
    fireEvent.click(screen.getByTitle("Apply view “High priority”"));
    await waitFor(() => {
      expect(screen.queryByText("First issue")).toBeNull();
    });
    expect(screen.getByText("Second issue")).toBeTruthy();
    // PQL mode entered with the applied query: the editor shows it
    // (keyed remount — PqlEditor's draft is initial-value-only).
    expect(
      (screen.getByLabelText("PQL query") as HTMLTextAreaElement).value,
    ).toBe("priority = High");
    // Probe-first apply + React Query fetch, both scoped epic + pql.
    const pqlCalls = projectIssueCalls(get).filter(
      (qs) => qs.get("pql") !== null,
    );
    expect(pqlCalls).toHaveLength(2);
    for (const qs of pqlCalls) {
      expect(qs.get("epic")).toBe("epic-1");
      expect(qs.get("pql")).toBe("priority = High");
    }
  });

  it("a default view present does NOT auto-apply on detail mount", async () => {
    renderDetail({ ownViews: [savedDefaultPql] });
    await screen.findByText("First issue");
    // Membership list intact, PQL mode never entered: no editor, and
    // the All-issues toggle still shows as pressed.
    expect(screen.getByText("Second issue")).toBeTruthy();
    expect(screen.queryByLabelText("PQL query")).toBeNull();
    expect(
      screen.getByRole("button", { name: "All issues" }),
    ).toHaveAttribute("aria-pressed", "true");
  });

  it("a saved view with an invalid query fails honestly: list untouched, error surfaced", async () => {
    const { get } = renderDetail({ ownViews: [savedBogus] });
    await screen.findByText("First issue");
    await openSavedViewsMenu();
    fireEvent.click(screen.getByTitle("Apply view “Bogus query”"));
    // The probe 400 surfaces at the detail level (the menu path has
    // no editor inline error, so the page's alert carries it) and
    // the list never changes.
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("unknown field: bogus");
    expect(screen.getByText("First issue")).toBeTruthy();
    expect(screen.getByText("Second issue")).toBeTruthy();
    // PQL mode was entered (editor visible) but nothing committed:
    // no applied query, and no query fetch beyond the single failed
    // probe — so the editor must NOT mount with the failed query as
    // its draft.
    expect(screen.getByLabelText("PQL query")).toBeTruthy();
    expect(
      (screen.getByLabelText("PQL query") as HTMLTextAreaElement).value,
    ).toBe("");
    expect(
      projectIssueCalls(get).filter((qs) => qs.get("pql") !== null),
    ).toHaveLength(1);
    // No unhandled rejection: the handler catches the probe failure
    // internally (a rejection here would fail the run).
  });

  it("the detail menu renders no visual views, own or shared", async () => {
    renderDetail({ ownViews: [savedPql, savedVisual], sharedViews: [sharedVisual] });
    await screen.findByText("First issue");
    await openSavedViewsMenu();
    expect(screen.getByText("High priority")).toBeTruthy();
    expect(screen.queryByText("My bugs")).toBeNull();
    expect(screen.queryByText("Team visual")).toBeNull();
  });

  it("the detail editor shows the corrected save hint", async () => {
    renderDetail();
    await screen.findByText("First issue");
    fireEvent.click(screen.getByRole("button", { name: "PQL" }));
    await screen.findByLabelText("PQL query");
    expect(
      screen.getByText(
        "To save this query, use Views → Save current view next to the All issues / PQL toggle; it re-applies when the view is opened.",
      ),
    ).toBeTruthy();
  });

  it("leaving PQL mode after a view apply restores the membership list", async () => {
    renderDetail({ ownViews: [savedPql] });
    await screen.findByText("First issue");
    await openSavedViewsMenu();
    fireEvent.click(screen.getByTitle("Apply view “High priority”"));
    await waitFor(() => {
      expect(screen.queryByText("First issue")).toBeNull();
    });
    fireEvent.click(screen.getByRole("button", { name: "All issues" }));
    await screen.findByText("First issue");
    expect(screen.getByText("Second issue")).toBeTruthy();
    expect(screen.queryByLabelText("PQL query")).toBeNull();
  });
});
