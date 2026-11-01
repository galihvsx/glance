// @vitest-environment jsdom
// Saved views on the calendar (C110T1): detail-mode SavedViewsMenu
// beside the All-issues / PQL toggle, applying a view through the
// page's probe-first handlePqlApply. Follows the CalendarPql.test.tsx
// shape for the page mock plus the EpicDetailPql.test.tsx saved-view
// idiom (mocked useSavedViews hook, stubbed auth).

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import Calendar from "./Calendar";
import { api, ApiError } from "../lib/api";
import { EMPTY_FILTERS } from "../lib/filters";
import { dayKey, firstOfMonth } from "../lib/calendar";
import type { Issue } from "../lib/types";
import {
  makeQueryClient,
  stubMatchMedia,
} from "../components/shell/test-utils";

// C107T1 precedent: the detail-mode menu reads auth through
// useSavedViews; mock the hook (Modules pattern) so page tests
// control the view list, and stub auth for the menu's owner gates.
vi.mock("../lib/auth", () => ({
  useAuth: () => ({ user: { id: "u-1", email: "u@x.com" }, loading: false }),
}));
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
const todayKey = dayKey(new Date());

function makeIssue(
  id: string,
  displayId: string,
  name: string,
  dates: Partial<Pick<Issue, "start_date" | "target_date">> = {},
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
    ...dates,
  };
}

const dueIssue = makeIssue("i-due", "ENG-1", "Due task", {
  target_date: `${todayKey}T00:00:00Z`,
});
const startIssue = makeIssue("i-start", "ENG-2", "Start task", {
  start_date: `${todayKey}T00:00:00Z`,
});
const undatedIssue = makeIssue("i-und", "ENG-3", "Undated task");

const dueFiltered = makeIssue("i-fdue", "ENG-11", "Filtered due", {
  target_date: `${todayKey}T00:00:00Z`,
});
const startFiltered = makeIssue("i-fstart", "ENG-12", "Filtered start", {
  start_date: `${todayKey}T00:00:00Z`,
});
const undatedFiltered = makeIssue("i-fund", "ENG-13", "Filtered undated");

const dueSecond = makeIssue("i-sdue", "ENG-21", "Second due", {
  target_date: `${todayKey}T00:00:00Z`,
});
const startSecond = makeIssue("i-sstart", "ENG-22", "Second start", {
  start_date: `${todayKey}T00:00:00Z`,
});
const undatedSecond = makeIssue("i-sund", "ENG-23", "Second undated");

function laneResults(qs: URLSearchParams, pql: string | null) {
  const filtered = pql !== null;
  const second = pql !== null && pql.includes("Low");
  if (qs.get("undated") === "1") {
    return filtered
      ? [second ? undatedSecond : undatedFiltered]
      : [undatedIssue];
  }
  if (qs.get("due_after") !== null) {
    return filtered ? [second ? dueSecond : dueFiltered] : [dueIssue];
  }
  return filtered ? [second ? startSecond : startFiltered] : [startIssue];
}

function renderPage(ownViews: SavedView[] = []) {
  mockViews(ownViews);
  const get = vi.spyOn(api, "get").mockImplementation((path: string) => {
    if (path === "/api/v1/workspaces")
      return Promise.resolve({ workspaces: [{ slug: "acme", name: "Acme" }] });
    if (path === "/api/v1/workspaces/acme")
      return Promise.resolve({ role: 15 });
    if (path === `${BASE}/states`) return Promise.resolve({ states: [] });
    // Shared ["project", slug, identifier] query feeding the menu.
    if (path === BASE) return Promise.resolve({ id: "p-eng" });
    if (path.includes("/issues")) {
      const qs = new URLSearchParams(path.split("?")[1] ?? "");
      const pql = qs.get("pql");
      if (pql !== null && pql.includes("bogus")) {
        return Promise.reject(
          new ApiError(400, "unknown field: bogus", "validation_error", {
            position: 1,
          }),
        );
      }
      return Promise.resolve({
        results: laneResults(qs, pql),
        next_cursor: null,
      });
    }
    return Promise.reject(new Error(`unexpected GET ${path}`));
  });
  const qc = makeQueryClient();
  render(
    <MemoryRouter initialEntries={["/w/acme/p/ENG/calendar"]}>
      <QueryClientProvider client={qc}>
        <Routes>
          <Route path="/w/:slug/p/:identifier/calendar" element={<Calendar />} />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
  return { get };
}

async function waitForInitial() {
  await screen.findByText("Due task");
  await screen.findByText("Start task");
  await screen.findByText("Undated task");
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

function editorValue(): string {
  return (screen.getByLabelText("PQL query") as HTMLTextAreaElement).value;
}

function monthLabel(): string {
  return new Date().toLocaleDateString(undefined, {
    month: "long",
    year: "numeric",
  });
}

const savedHigh: SavedView = {
  id: "v-high",
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

beforeEach(() => {
  mockViews([]);
  stubMatchMedia(false);
  localStorage.clear();
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("calendar saved views (C110T1)", () => {
  it("applying a PQL view enters PQL mode with the view's query applied and the window anchor unchanged", async () => {
    const { get } = renderPage([savedHigh]);
    await waitForInitial();
    const labelBefore = monthLabel();
    expect(screen.getByText(labelBefore)).toBeTruthy();
    await openSavedViewsMenu();
    fireEvent.click(screen.getByTitle("Apply view “High priority”"));
    await screen.findByText("Filtered due");
    expect(screen.getByText("Filtered start")).toBeTruthy();
    expect(screen.getByText("Filtered undated")).toBeTruthy();
    expect(screen.queryByText("Due task")).toBeNull();
    // PQL mode entered with the view's query: the editor shows it
    // (keyed remount — PqlEditor's draft is initial-value-only).
    expect(
      screen.getByRole("button", { name: "PQL" }),
    ).toHaveAttribute("aria-pressed", "true");
    expect(editorValue()).toBe("priority = High");
    // The window anchor never moved: same month label, and the PQL
    // fetches carry the original window's due_after (first of the
    // current month) — the query ANDs with the window, it never
    // moves it.
    expect(screen.getByText(labelBefore)).toBeTruthy();
    const now = new Date();
    const windowStart = `${firstOfMonth(now.getFullYear(), now.getMonth())}T00:00:00Z`;
    const pqlCalls = issueCalls(get).filter(
      (qs) => qs.get("pql") === "priority = High",
    );
    expect(pqlCalls.length).toBeGreaterThan(0);
    for (const qs of pqlCalls) {
      if (qs.get("due_after") !== null)
        expect(qs.get("due_after")).toBe(windowStart);
    }
  });

  it("applying a view while already in PQL mode re-seeds the editor", async () => {
    renderPage([savedHigh, savedLow]);
    await waitForInitial();
    await openSavedViewsMenu();
    fireEvent.click(screen.getByTitle("Apply view “High priority”"));
    await screen.findByText("Filtered due");
    expect(editorValue()).toBe("priority = High");
    // Already in PQL mode: without the applied-query key the editor
    // would keep showing the first view's draft.
    await openSavedViewsMenu();
    fireEvent.click(screen.getByTitle("Apply view “Low priority”"));
    await screen.findByText("Second due");
    expect(screen.getByText("Second start")).toBeTruthy();
    expect(screen.getByText("Second undated")).toBeTruthy();
    expect(editorValue()).toBe("priority = Low");
  });

  it("a view whose query fails its probe surfaces the error with the displayed set untouched", async () => {
    const { get } = renderPage([savedBogus]);
    await waitForInitial();
    await openSavedViewsMenu();
    fireEvent.click(screen.getByTitle("Apply view “Bogus query”"));
    // The probe 400 surfaces at the detail level (the menu path has
    // no editor inline error) and the window sets never change.
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("unknown field: bogus");
    expect(screen.getByText("Due task")).toBeTruthy();
    expect(screen.getByText("Start task")).toBeTruthy();
    expect(screen.getByText("Undated task")).toBeTruthy();
    // PQL mode was entered (editor visible) but nothing committed:
    // no applied query, and no query fetch beyond the three failed
    // window probes — so the editor must NOT mount with the failed
    // query as its draft.
    expect(screen.getByLabelText("PQL query")).toBeTruthy();
    expect(editorValue()).toBe("");
    expect(
      issueCalls(get).filter((qs) => qs.get("pql") !== null),
    ).toHaveLength(3);
    // No unhandled rejection: the handler catches the probe failure
    // internally (a rejection here would fail the run).
  });

  it("leaving PQL mode after a view apply restores the unfiltered window sets", async () => {
    renderPage([savedHigh]);
    await waitForInitial();
    await openSavedViewsMenu();
    fireEvent.click(screen.getByTitle("Apply view “High priority”"));
    await screen.findByText("Filtered due");
    fireEvent.click(screen.getByRole("button", { name: "All issues" }));
    await screen.findByText("Due task");
    expect(screen.getByText("Start task")).toBeTruthy();
    expect(screen.getByText("Undated task")).toBeTruthy();
    expect(screen.queryByLabelText("PQL query")).toBeNull();
  });

  it("the editor shows the corrected save hint now that the menu is the save path", async () => {
    renderPage();
    await waitForInitial();
    fireEvent.click(screen.getByRole("button", { name: "PQL" }));
    await screen.findByLabelText("PQL query");
    expect(
      screen.getByText(
        "To save this query, use Views → Save current view next to the All issues / PQL toggle; it re-applies when the view is opened.",
      ),
    ).toBeTruthy();
  });
});
