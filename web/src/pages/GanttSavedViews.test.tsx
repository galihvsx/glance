// @vitest-environment jsdom
// Saved views on the Gantt view (C110T2): the detail-mode
// SavedViewsMenu beside the All-issues / PQL toggle, applying a
// view through the Gantt's existing probe-first handlePqlApply
// (all three window queries). A view apply enters PQL mode with
// the view's query applied and NEVER moves the year/month window
// — the saved query ANDs with the current window (C109
// semantics). Follows the GanttPql.test.tsx shape plus the
// EpicDetailPql.test.tsx saved-view idiom (mocked useSavedViews
// hook, so the page tests control the view list without a views
// endpoint).

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
import Gantt from "./Gantt";
import { api, ApiError } from "../lib/api";
import { EMPTY_FILTERS } from "../lib/filters";
import type { Issue, IssueState } from "../lib/types";
import {
  makeQueryClient,
  stubMatchMedia,
} from "../components/shell/test-utils";

// C108T3: the detail-mode menu reads views through this hook; mock
// it (same pattern as EpicDetailPql.test.tsx) so page tests control
// the view list without a views endpoint.
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

const stateStarted: IssueState = {
  id: "s-1",
  project_id: "p-eng",
  name: "In Progress",
  group: "started",
  color: "#6366f1",
  sequence: 1,
  created_at: "",
  updated_at: "",
};

function makeIssue(
  id: string,
  displayId: string,
  name: string,
  startDate: string | null,
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
    start_date: startDate,
    target_date: targetDate,
  };
}

// Dated inside the Nov-2026-centered window (Oct–Dec 2026).
const issueA = makeIssue(
  "i-a",
  "ENG-1",
  "Fix login redirect",
  "2026-11-10T00:00:00Z",
  "2026-11-12T00:00:00Z",
);
const issueB = makeIssue(
  "i-b",
  "ENG-2",
  "Urgent checkout bug",
  "2026-11-11T00:00:00Z",
  "2026-11-13T00:00:00Z",
);
const issueC = makeIssue("i-c", "ENG-3", "Undated research spike", null, null);

const savedHigh: SavedView = {
  id: "v-high",
  name: "High priority",
  filters: { ...EMPTY_FILTERS, pql: "priority = High" },
  display: null,
  isDefault: false,
  shared: false,
  ownerId: "me",
  ownerName: "Me",
};
const savedLow: SavedView = {
  id: "v-low",
  name: "Low priority",
  filters: { ...EMPTY_FILTERS, pql: "priority = Low" },
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

function renderPage(ownViews: SavedView[] = []) {
  mockViews(ownViews);
  const get = vi.spyOn(api, "get").mockImplementation((path: string) => {
    if (path === "/api/v1/workspaces")
      return Promise.resolve({
        workspaces: [{ slug: "acme", name: "Acme" }],
      });
    if (path === "/api/v1/workspaces/acme")
      return Promise.resolve({ role: 15 });
    // Project query for the detail-mode SavedViewsMenu (shared
    // ["project", slug, identifier] key).
    if (path === BASE) return Promise.resolve({ id: "p-eng" });
    if (path === `${BASE}/states`)
      return Promise.resolve({ states: [stateStarted] });
    if (path.includes("/issues")) {
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
        if (pql.includes("Low")) {
          if (qs.get("undated") !== null)
            return Promise.resolve({ results: [issueC], next_cursor: null });
          return Promise.resolve({ results: [issueA], next_cursor: null });
        }
        if (qs.get("undated") !== null)
          return Promise.resolve({ results: [], next_cursor: null });
        return Promise.resolve({ results: [issueB], next_cursor: null });
      }
      if (qs.get("undated") !== null)
        return Promise.resolve({ results: [issueC], next_cursor: null });
      return Promise.resolve({
        results: [issueA, issueB],
        next_cursor: null,
      });
    }
    return Promise.reject(new Error(`unexpected GET ${path}`));
  });
  const qc = makeQueryClient();
  render(
    <MemoryRouter initialEntries={["/w/acme/p/ENG/gantt"]}>
      <QueryClientProvider client={qc}>
        <Routes>
          <Route path="/w/:slug/p/:identifier/gantt" element={<Gantt />} />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
  return { get };
}

async function waitForFullSet() {
  await waitFor(() => {
    expect(screen.queryByText("Fix login redirect")).not.toBeNull();
    expect(screen.queryByText("Urgent checkout bug")).not.toBeNull();
    expect(screen.queryByText("Undated research spike")).not.toBeNull();
  });
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

describe("Gantt saved views (C110T2)", () => {
  it("applying a PQL view enters PQL mode with the view's query applied and the window unchanged", async () => {
    const { get } = renderPage([savedHigh]);
    await waitForFullSet();
    await openSavedViewsMenu();
    fireEvent.click(screen.getByTitle("Apply view “High priority”"));
    await waitFor(() => {
      expect(screen.queryByText("Fix login redirect")).toBeNull();
      expect(screen.queryByText("Urgent checkout bug")).not.toBeNull();
      expect(screen.queryByText("Undated research spike")).toBeNull();
    });
    expect(screen.queryByText("Unscheduled")).toBeNull();
    expect(
      screen.getByRole("button", { name: "PQL" }),
    ).toHaveAttribute("aria-pressed", "true");
    // Editor mounts seeded with the applied query (keyed remount).
    expect(
      (screen.getByLabelText("PQL query") as HTMLTextAreaElement).value,
    ).toBe("priority = High");
    // The window never moved: every PQL fetch still carries the
    // Nov-centered window (Oct 2026 start), and the header label
    // still spans the original three months.
    const pqlCalls = issueCalls(get).filter((qs) => qs.get("pql") !== null);
    expect(pqlCalls.length).toBeGreaterThan(0);
    for (const qs of pqlCalls) {
      expect(qs.get("pql")).toBe("priority = High");
      if (qs.get("due_after") !== null) {
        expect(qs.get("due_after")).toBe("2026-10-01T00:00:00Z");
        expect(qs.get("due_before")).toBe("2027-01-01T00:00:00Z");
      }
    }
    expect(screen.getByText(/October 2026 – December 2026/)).toBeTruthy();
    // Probe-first apply (3 window probes) + the 3 window fetches.
    expect(pqlCalls).toHaveLength(6);
  });

  it("applying a view while already in PQL mode re-seeds the editor", async () => {
    renderPage([savedHigh, savedLow]);
    await waitForFullSet();
    await openSavedViewsMenu();
    fireEvent.click(screen.getByTitle("Apply view “High priority”"));
    await waitFor(() => {
      expect(screen.queryByText("Fix login redirect")).toBeNull();
      expect(screen.queryByText("Urgent checkout bug")).not.toBeNull();
    });
    expect(
      (screen.getByLabelText("PQL query") as HTMLTextAreaElement).value,
    ).toBe("priority = High");
    // Already in PQL mode: without the applied-query key the editor
    // draft would stay on the first query (C107 lesson).
    await openSavedViewsMenu();
    fireEvent.click(screen.getByTitle("Apply view “Low priority”"));
    await waitFor(() => {
      expect(screen.queryByText("Fix login redirect")).not.toBeNull();
      expect(screen.queryByText("Urgent checkout bug")).toBeNull();
      expect(screen.queryByText("Undated research spike")).not.toBeNull();
    });
    expect(
      (screen.getByLabelText("PQL query") as HTMLTextAreaElement).value,
    ).toBe("priority = Low");
  });

  it("a view whose query fails its probe surfaces the error with the bars untouched", async () => {
    const { get } = renderPage([savedBogus]);
    await waitForFullSet();
    await openSavedViewsMenu();
    fireEvent.click(screen.getByTitle("Apply view “Bogus query”"));
    // The probe 400 surfaces at the detail level (the menu path has
    // no editor inline error) and the bars never change.
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("unknown field: bogus");
    // Entering PQL mode with no applied query re-keys the window
    // queries (..."pql", ""), so the timeline briefly shows its
    // loading state before the unfiltered sets come back — wait
    // for the refetch, then assert the bars are untouched.
    await waitForFullSet();
    expect(screen.getByText("Unscheduled")).toBeTruthy();
    // PQL mode was entered (editor visible) but nothing committed:
    // the editor mounts with an empty draft, and no window fetch
    // beyond the three failed probes carries the bogus query.
    expect(screen.getByLabelText("PQL query")).toBeTruthy();
    expect(
      (screen.getByLabelText("PQL query") as HTMLTextAreaElement).value,
    ).toBe("");
    expect(
      issueCalls(get).filter((qs) => qs.get("pql") !== null),
    ).toHaveLength(3);
    // No unhandled rejection: the handler catches the probe failure
    // internally (a rejection here would fail the run).
  });

  it("leaving PQL mode after a view apply restores the unfiltered sets", async () => {
    renderPage([savedHigh]);
    await waitForFullSet();
    await openSavedViewsMenu();
    fireEvent.click(screen.getByTitle("Apply view “High priority”"));
    await waitFor(() => {
      expect(screen.queryByText("Fix login redirect")).toBeNull();
      expect(screen.queryByText("Urgent checkout bug")).not.toBeNull();
    });
    fireEvent.click(screen.getByRole("button", { name: "All issues" }));
    await waitForFullSet();
    expect(screen.queryByLabelText("PQL query")).toBeNull();
    expect(screen.getByText("Unscheduled")).toBeTruthy();
  });

  it("the editor shows the corrected save hint once the menu is the save path", async () => {
    renderPage();
    await waitForFullSet();
    fireEvent.click(screen.getByRole("button", { name: "PQL" }));
    await screen.findByLabelText("PQL query");
    expect(
      screen.getByText(
        "To save this query, use Views → Save current view next to the All issues / PQL toggle; it re-applies when the view is opened.",
      ),
    ).toBeTruthy();
  });
});
