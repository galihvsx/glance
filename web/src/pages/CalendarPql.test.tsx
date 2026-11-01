// @vitest-environment jsdom
// PQL mode on the calendar (C109T1): toggle on/off, PQL-mode fetch
// params (window params + pql on all three lanes), invalid-PQL error
// display with the sets intact, re-apply re-seeding the editor (the
// C107 key lesson), and window navigation keeping the applied query.
// Copies the CyclesPql.test.tsx shape.

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
import Calendar from "./Calendar";
import { api, ApiError } from "../lib/api";

// C110T1 fix-up: Calendar now renders the detail-mode SavedViewsMenu
// → useSavedViews/useAuth; mock the hook (EpicDetailPql pattern) so
// these pre-menu tests render the menu's empty state untouched.
vi.mock("../lib/auth", () => ({
  useAuth: () => ({ user: { id: "u-1", email: "u@x.com" }, loading: false }),
}));
vi.mock("../components/issue/useSavedViews", () => ({
  useSavedViews: vi.fn(),
}));
import { useSavedViews } from "../components/issue/useSavedViews";
const mockUseSavedViews = vi.mocked(useSavedViews);
function emptyViews() {
  mockUseSavedViews.mockReturnValue({
    views: [],
    ownViews: [],
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
import { dayKey, firstOfMonth, shiftMonth } from "../lib/calendar";
import type { Issue } from "../lib/types";
import {
  makeQueryClient,
  stubMatchMedia,
} from "../components/shell/test-utils";

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

// Unfiltered window sets: one issue per lane, all placed today so
// they render in the month grid / unscheduled strip.
const dueIssue = makeIssue("i-due", "ENG-1", "Due task", {
  target_date: `${todayKey}T00:00:00Z`,
});
const startIssue = makeIssue("i-start", "ENG-2", "Start task", {
  start_date: `${todayKey}T00:00:00Z`,
});
const undatedIssue = makeIssue("i-und", "ENG-3", "Undated task");

// First applied query's scoped sets (same lane shape, new issues).
const dueFiltered = makeIssue("i-fdue", "ENG-11", "Filtered due", {
  target_date: `${todayKey}T00:00:00Z`,
});
const startFiltered = makeIssue("i-fstart", "ENG-12", "Filtered start", {
  start_date: `${todayKey}T00:00:00Z`,
});
const undatedFiltered = makeIssue("i-fund", "ENG-13", "Filtered undated");

// Second query's scoped sets, so a re-apply while already in PQL
// mode is observable in the list as well as in the editor draft.
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
  // start_after lane.
  return filtered ? [second ? startSecond : startFiltered] : [startIssue];
}

function renderPage() {
  const get = vi.spyOn(api, "get").mockImplementation((path: string) => {
    if (path === "/api/v1/workspaces")
      return Promise.resolve({ workspaces: [{ slug: "acme", name: "Acme" }] });
    if (path === "/api/v1/workspaces/acme")
      return Promise.resolve({ role: 15 });
    if (path === `${BASE}/states`) return Promise.resolve({ states: [] });
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

async function applyPql(query: string) {
  fireEvent.click(screen.getByRole("button", { name: "PQL" }));
  await screen.findByLabelText("PQL query");
  fireEvent.change(screen.getByLabelText("PQL query"), {
    target: { value: query },
  });
  fireEvent.click(screen.getByRole("button", { name: "Apply" }));
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

beforeEach(() => {
  stubMatchMedia(false);
  localStorage.clear();
  emptyViews();
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("calendar PQL mode (C109T1)", () => {
  it("default mode shows the unfiltered window sets with no PQL editor", async () => {
    const { get } = renderPage();
    await waitForInitial();
    expect(screen.queryByLabelText("PQL query")).toBeNull();
    expect(
      screen.getByRole("button", { name: "All issues" }),
    ).toHaveAttribute("aria-pressed", "true");
    // Default fetches carry window params but no pql.
    const calls = issueCalls(get);
    expect(calls.length).toBeGreaterThan(0);
    for (const qs of calls) {
      expect(qs.get("pql")).toBeNull();
    }
  });

  it("applying a PQL query narrows all three lanes and the editor shows the applied query", async () => {
    const { get } = renderPage();
    await waitForInitial();
    await applyPql("priority = High");
    // Await the filtered sets, not just the absence of the old ones:
    // while a new PQL query loads, the display falls back to the
    // unfiltered sets, so absence alone passes before the fetch lands.
    await screen.findByText("Filtered due");
    expect(screen.getByText("Filtered start")).toBeTruthy();
    expect(screen.getByText("Filtered undated")).toBeTruthy();
    expect(screen.queryByText("Start task")).toBeNull();
    expect(screen.queryByText("Undated task")).toBeNull();
    expect(editorValue()).toBe("priority = High");
    // Every PQL fetch carries the window params for its lane plus pql.
    const pqlCalls = issueCalls(get).filter((qs) => qs.get("pql") !== null);
    expect(pqlCalls.length).toBeGreaterThan(0);
    for (const qs of pqlCalls) {
      expect(qs.get("pql")).toBe("priority = High");
    }
    const dueCalls = pqlCalls.filter((qs) => qs.get("due_after") !== null);
    const startCalls = pqlCalls.filter(
      (qs) => qs.get("start_after") !== null,
    );
    const undatedCalls = pqlCalls.filter((qs) => qs.get("undated") === "1");
    expect(dueCalls.length).toBeGreaterThan(0);
    expect(startCalls.length).toBeGreaterThan(0);
    expect(undatedCalls.length).toBeGreaterThan(0);
    for (const qs of dueCalls) expect(qs.get("due_before")).not.toBeNull();
    for (const qs of startCalls) expect(qs.get("start_before")).not.toBeNull();
  });

  it("invalid PQL shows the editor error with column and leaves the sets intact", async () => {
    renderPage();
    await waitForInitial();
    await applyPql("bogus = 1");
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("unknown field: bogus");
    expect(alert.textContent).toContain("at column 1");
    // Sets untouched: all three unfiltered issues still on screen,
    // and no filtered issue appeared. No unhandled rejection: the
    // editor catches the probe failure internally (a rejection here
    // would fail the run).
    expect(screen.getByText("Due task")).toBeTruthy();
    expect(screen.getByText("Start task")).toBeTruthy();
    expect(screen.getByText("Undated task")).toBeTruthy();
    expect(screen.queryByText("Filtered due")).toBeNull();
  });

  it("leaving PQL mode restores the unfiltered window sets and closes the editor", async () => {
    renderPage();
    await waitForInitial();
    await applyPql("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("Due task")).toBeNull();
    });
    fireEvent.click(screen.getByRole("button", { name: "All issues" }));
    await screen.findByText("Due task");
    expect(screen.getByText("Start task")).toBeTruthy();
    expect(screen.getByText("Undated task")).toBeTruthy();
    expect(screen.queryByLabelText("PQL query")).toBeNull();
  });

  it("re-applying a second query while in PQL mode re-seeds the editor", async () => {
    renderPage();
    await waitForInitial();
    await applyPql("priority = High");
    await waitFor(() => {
      expect(screen.getByText("Filtered due")).toBeTruthy();
    });
    expect(editorValue()).toBe("priority = High");
    // Already in PQL mode: the editor is mounted, so without the
    // applied-query key its draft would stay on the first query.
    fireEvent.change(screen.getByLabelText("PQL query"), {
      target: { value: "priority = Low" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Apply" }));
    // Same fallback caveat as the first apply: await the new sets.
    await screen.findByText("Second due");
    expect(screen.getByText("Second start")).toBeTruthy();
    expect(screen.getByText("Second undated")).toBeTruthy();
    expect(editorValue()).toBe("priority = Low");
  });

  it("moving the window keeps the applied query and re-queries the new window", async () => {
    const { get } = renderPage();
    await waitForInitial();
    await applyPql("priority = High");
    await waitFor(() => {
      expect(screen.getByText("Filtered due")).toBeTruthy();
    });
    // The new window's due_after: first of next month, RFC3339.
    const now = new Date();
    const next = shiftMonth(now.getFullYear(), now.getMonth(), 1);
    const nextDueAfter = `${firstOfMonth(next.year, next.month)}T00:00:00Z`;
    fireEvent.click(screen.getByRole("button", { name: "Next month" }));
    // The applied query is re-run against the new window (same pql,
    // new window params) — the editor keeps the query throughout.
    await waitFor(() => {
      const moved = issueCalls(get).filter(
        (qs) =>
          qs.get("pql") === "priority = High" &&
          qs.get("due_after") === nextDueAfter,
      );
      expect(moved.length).toBeGreaterThan(0);
    });
    expect(editorValue()).toBe("priority = High");
    // Moving back restores the filtered window set: the query was
    // kept across the window move, not dropped.
    fireEvent.click(screen.getByRole("button", { name: "Previous month" }));
    await screen.findByText("Filtered due");
    expect(screen.getByText("Filtered start")).toBeTruthy();
    expect(screen.getByText("Filtered undated")).toBeTruthy();
    expect(editorValue()).toBe("priority = High");
  });

  it("editor shows the corrected save hint now that the menu is the save path", async () => {
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
