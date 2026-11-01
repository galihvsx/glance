// @vitest-environment jsdom
// Saved views on the roadmap (C110T3): the detail-mode SavedViewsMenu
// beside the All-issues / PQL toggle, copying the Cycles detail shape
// verbatim — probe-first handleSavedViewApplied through the Roadmap's
// existing handlePqlApply (plain list, cursor loop), a savedViewError
// alert at the detail level for a failed probe, and month buckets +
// the Unscheduled lane recomputing from the filtered set. Roadmap has
// no window, so there is no window assertion here — the wiring is the
// same detail-mode shape for consistency, not because a window is
// involved. Follows the RoadmapPql.test.tsx / EpicDetailPql.test.tsx
// saved-view idioms (stub auth, mock useSavedViews, seed the shared
// project query as the C108T3 fix-up did for EpicDetail).

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
import Roadmap from "./Roadmap";
import { api, ApiError } from "../lib/api";
import { EMPTY_FILTERS } from "../lib/filters";
import type { Issue, IssueState } from "../lib/types";
import {
  makeQueryClient,
  stubMatchMedia,
} from "../components/shell/test-utils";

// C110T3: Roadmap renders SavedViewsMenu → useAuth; the real provider
// needs a session — stub the auth context (C108T3 fix-up precedent).
vi.mock("../lib/auth", () => ({
  useAuth: () => ({ user: { id: "u-1", email: "u@x.com" }, loading: false }),
}));

// C110T3: the detail-mode menu reads views through this hook; mock it
// (EpicDetailPql.test.tsx pattern) so page tests control the view list
// without a views endpoint.
vi.mock("../components/issue/useSavedViews", () => ({
  useSavedViews: vi.fn(),
}));
import { useSavedViews } from "../components/issue/useSavedViews";
import type { SavedView } from "../components/issue/useSavedViews";

const mockUseSavedViews = vi.mocked(useSavedViews);

function mockViews(ownViews: SavedView[]) {
  mockUseSavedViews.mockReturnValue({
    views: ownViews,
    ownViews,
    sharedViews: [],
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

const states: IssueState[] = [
  {
    id: "s-1",
    project_id: "p-eng",
    name: "Todo",
    group: "unstarted",
    color: "#888",
    sequence: 1,
    created_at: "",
    updated_at: "",
  },
];

function makeIssue(
  id: string,
  displayId: string,
  name: string,
  targetDate: string | null,
): Issue {
  return {
    id,
    project_id: "p-eng",
    sequence_id: 1,
    display_id: displayId,
    name,
    priority: 3,
    state_id: "s-1",
    sort_order: 0,
    is_draft: false,
    created_by: "u-1",
    created_at: "",
    updated_at: "",
    assignees: [],
    labels: [],
    target_date: targetDate,
  };
}

// Anchor is the current month at render time, so target dates are
// built relative to it: A in the anchor month, B in the next month,
// C undated (Unscheduled lane). Same UTC-month math as lib/roadmap,
// so the test never depends on today's date.
function monthKey(offset: number): string {
  const d = new Date();
  const m = new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth() + offset, 1));
  return `${m.getUTCFullYear()}-${String(m.getUTCMonth() + 1).padStart(2, "0")}-15T00:00:00Z`;
}

const issueA = makeIssue("i-a", "ENG-1", "November launch task", monthKey(0));
const issueB = makeIssue("i-b", "ENG-2", "December launch task", monthKey(1));
const issueC = makeIssue("i-c", "ENG-3", "Unscheduled backlog task", null);

function renderPage(ownViews: SavedView[] = []) {
  mockViews(ownViews);
  const get = vi.spyOn(api, "get").mockImplementation((path: string) => {
    if (path === "/api/v1/workspaces")
      return Promise.resolve({ workspaces: [] });
    if (path === "/api/v1/workspaces/acme")
      return Promise.resolve({ role: 15 });
    // Shared ["project", slug, identifier] query for the detail-mode
    // SavedViewsMenu (its project UUID).
    if (path === BASE)
      return Promise.resolve({
        id: "p-eng",
        workspace_id: "w-acme",
        identifier: "ENG",
        name: "ENG",
        description: "",
        parallel_cycles: false,
        labels: [],
        created_at: "",
        updated_at: "",
      });
    if (path === `${BASE}/states`) return Promise.resolve({ states });
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
        // Distinct result for a second query, so a re-apply while
        // already in PQL mode is observable in the lanes as well
        // as in the editor draft.
        if (pql.includes("Low")) {
          return Promise.resolve({ results: [issueC], next_cursor: null });
        }
        return Promise.resolve({ results: [issueB], next_cursor: null });
      }
      return Promise.resolve({
        results: [issueA, issueB, issueC],
        next_cursor: null,
      });
    }
    return Promise.reject(new Error(`unexpected GET ${path}`));
  });
  const qc = makeQueryClient();
  render(
    <MemoryRouter initialEntries={["/w/acme/p/ENG/roadmap"]}>
      <QueryClientProvider client={qc}>
        <Routes>
          <Route path="/w/:slug/p/:identifier/roadmap" element={<Roadmap />} />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
  return { get };
}

async function waitForRoadmap() {
  await screen.findByText("November launch task");
  await screen.findByText("December launch task");
  await screen.findByText("Unscheduled backlog task");
}

async function openSavedViewsMenu() {
  fireEvent.click(screen.getByRole("button", { name: "Saved views" }));
  await screen.findByRole("button", { name: "Save current view" });
}

function issueCalls(get: ReturnType<typeof renderPage>["get"]) {
  return get.mock.calls
    .map((c) => c[0] as string)
    .filter((p) => p.includes("/issues"))
    .map((p) => new URLSearchParams(p.split("?")[1] ?? ""));
}

beforeEach(() => {
  mockViews([]);
  stubMatchMedia(false);
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("roadmap saved views (C110T3)", () => {
  const savedPql: SavedView = {
    id: "v-pql",
    name: "High priority",
    filters: { ...EMPTY_FILTERS, pql: "priority = High" },
    display: null,
    isDefault: false,
    shared: false,
    ownerId: "u-1",
    ownerName: "Me",
  };
  const savedLow: SavedView = {
    id: "v-low",
    name: "Low priority",
    filters: { ...EMPTY_FILTERS, pql: "priority = Low" },
    display: null,
    isDefault: false,
    shared: false,
    ownerId: "u-1",
    ownerName: "Me",
  };
  const savedBogus: SavedView = {
    id: "v-bogus",
    name: "Bogus query",
    filters: { ...EMPTY_FILTERS, pql: "bogus = 1" },
    display: null,
    isDefault: false,
    shared: false,
    ownerId: "u-1",
    ownerName: "Me",
  };

  it("applying a PQL view enters PQL mode with the view's query applied and buckets + Unscheduled lane narrowing", async () => {
    const { get } = renderPage([savedPql]);
    await waitForRoadmap();
    await openSavedViewsMenu();
    fireEvent.click(screen.getByTitle("Apply view “High priority”"));
    await waitFor(() => {
      expect(screen.queryByText("November launch task")).toBeNull();
    });
    expect(screen.getByText("December launch task")).toBeTruthy();
    // The Unscheduled lane narrowed too: its only issue is gone and
    // the lane reports empty.
    expect(screen.queryByText("Unscheduled backlog task")).toBeNull();
    expect(screen.getByText("Every issue has a target date.")).toBeTruthy();
    // PQL mode entered with the applied query: the editor shows it
    // (keyed remount — PqlEditor's draft is initial-value-only).
    expect(
      screen.getByRole("button", { name: "PQL" }),
    ).toHaveAttribute("aria-pressed", "true");
    expect(
      (screen.getByLabelText("PQL query") as HTMLTextAreaElement).value,
    ).toBe("priority = High");
    // Probe-first apply + React Query fetch, both through the plain
    // list with pql, no window/scope params.
    const pqlCalls = issueCalls(get).filter((qs) => qs.get("pql") !== null);
    expect(pqlCalls).toHaveLength(2);
    for (const qs of pqlCalls) {
      expect(qs.get("pql")).toBe("priority = High");
    }
  });

  it("applying a view while already in PQL mode re-seeds the editor", async () => {
    renderPage([savedPql, savedLow]);
    await waitForRoadmap();
    await openSavedViewsMenu();
    fireEvent.click(screen.getByTitle("Apply view “High priority”"));
    await waitFor(() => {
      expect(screen.queryByText("November launch task")).toBeNull();
    });
    expect(
      (screen.getByLabelText("PQL query") as HTMLTextAreaElement).value,
    ).toBe("priority = High");
    // Already in PQL mode: the editor is mounted, so without the
    // applied-query key its draft would stay on the first query.
    await openSavedViewsMenu();
    fireEvent.click(screen.getByTitle("Apply view “Low priority”"));
    await waitFor(() => {
      expect(screen.queryByText("December launch task")).toBeNull();
    });
    expect(screen.getByText("Unscheduled backlog task")).toBeTruthy();
    expect(
      (screen.getByLabelText("PQL query") as HTMLTextAreaElement).value,
    ).toBe("priority = Low");
  });

  it("a view whose query fails its probe surfaces the error with the list untouched", async () => {
    const { get } = renderPage([savedBogus]);
    await waitForRoadmap();
    await openSavedViewsMenu();
    fireEvent.click(screen.getByTitle("Apply view “Bogus query”"));
    // The probe 400 surfaces at the detail level (the menu path has
    // no editor inline error, so the page's alert carries it) and
    // the list never changes.
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("unknown field: bogus");
    await waitForRoadmap();
    // PQL mode was entered (editor visible) but nothing committed:
    // no applied query, and no query fetch beyond the single failed
    // probe — so the editor must NOT mount with the failed query as
    // its draft.
    expect(screen.getByLabelText("PQL query")).toBeTruthy();
    expect(
      (screen.getByLabelText("PQL query") as HTMLTextAreaElement).value,
    ).toBe("");
    expect(
      issueCalls(get).filter((qs) => qs.get("pql") !== null),
    ).toHaveLength(1);
    // No unhandled rejection: the handler catches the probe failure
    // internally (a rejection here would fail the run).
  });

  it("leaving PQL mode after a view apply restores the unfiltered list", async () => {
    renderPage([savedPql]);
    await waitForRoadmap();
    await openSavedViewsMenu();
    fireEvent.click(screen.getByTitle("Apply view “High priority”"));
    await waitFor(() => {
      expect(screen.queryByText("November launch task")).toBeNull();
    });
    fireEvent.click(screen.getByRole("button", { name: "All issues" }));
    await waitForRoadmap();
    expect(screen.queryByLabelText("PQL query")).toBeNull();
    expect(
      screen.getByRole("button", { name: "All issues" }),
    ).toHaveAttribute("aria-pressed", "true");
  });

  it("the roadmap editor shows the corrected save hint", async () => {
    renderPage();
    await waitForRoadmap();
    fireEvent.click(screen.getByRole("button", { name: "PQL" }));
    await screen.findByLabelText("PQL query");
    expect(
      screen.getByText(
        "To save this query, use Views → Save current view next to the All issues / PQL toggle; it re-applies when the view is opened.",
      ),
    ).toBeTruthy();
  });
});
