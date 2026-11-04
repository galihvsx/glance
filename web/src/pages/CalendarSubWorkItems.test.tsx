// @vitest-environment jsdom
// C145T2: "Show sub-work items" on the calendar — include_subtasks=1
// on the due, start, and undated queries AND their PQL variants when
// the calendar-local toggle is on (default), omitted when off;
// nested child chips indented under their parent chip in the SAME day
// cell (child travels with parent, never also flat); orphan children
// stay flat; children of strip parents nest in the strip; toggle off
// renders today's flat cells. Follows the CalendarPql test harness
// (spyOn api.get, lane-routed payloads, PQL apply flow).

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import Calendar from "./Calendar";
import { api } from "../lib/api";
import type { Issue, SubtaskRow } from "../lib/types";
import { dayKey } from "../lib/calendar";
import {
  makeQueryClient,
  stubMatchMedia,
} from "../components/shell/test-utils";

// Calendar now renders the detail-mode SavedViewsMenu → useSavedViews
// / useAuth; mock the hooks (CalendarPql pattern) so these tests
// render the menu's empty state untouched.
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

const BASE = "/api/v1/workspaces/acme/projects/ENG";
const SUB_KEY = "glance:calendar-sub-work-items:acme:ENG";

const todayKey = dayKey(new Date());
const plus2Key = dayKey(new Date(Date.now() + 2 * 86_400_000));

/** Roadmap's formatDay: UTC-parsed YYYY-MM-DD, "Nov 5". */
function fmtDay(day: string): string {
  const [y, m, d] = day.split("-").map(Number);
  return new Date(Date.UTC(y, m - 1, d)).toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
    timeZone: "UTC",
  });
}

function makeIssue(
  id: string,
  displayId: string,
  name: string,
  extra: Partial<Issue> = {},
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
    ...extra,
  };
}

function childRow(
  id: string,
  displayId: string,
  title: string,
  sortOrder: number,
  dates: Partial<Pick<SubtaskRow, "start_date" | "target_date">> = {},
): SubtaskRow {
  return {
    id,
    display_id: displayId,
    title,
    state_id: "s-1",
    priority: 3,
    assignee_ids: [],
    sort_order: sortOrder,
    ...dates,
  };
}

// Dated parent (due today) with two nested children: one carrying its
// own start→target range, one dateless. The dated child also arrives
// as a flat due-lane issue — hidden while nested.
const datedChild = childRow("i-c", "ENG-2", "Child task", 1, {
  start_date: `${todayKey}T00:00:00Z`,
  target_date: `${plus2Key}T00:00:00Z`,
});
const datelessChild = childRow("i-dc", "ENG-4", "Dateless child", 2);
const parent = makeIssue("i-p", "ENG-1", "Parent task", {
  target_date: `${todayKey}T00:00:00Z`,
  subtasks: [datedChild, datelessChild],
});
const childFlat = makeIssue("i-c", "ENG-2", "Child task", {
  target_date: `${todayKey}T00:00:00Z`,
});
// Orphan: no parent in the fetched set references it — stays flat.
const orphan = makeIssue("i-o", "ENG-5", "Orphan task", {
  target_date: `${todayKey}T00:00:00Z`,
});
const duePayload = [parent, childFlat, orphan];

// Start lane: a plain start-dated issue (flag coverage, not nesting).
const startPayload = [
  makeIssue("i-s", "ENG-6", "Start task", {
    start_date: `${todayKey}T00:00:00Z`,
  }),
];

// Undated strip: a parent with a target-only child (nested in the
// strip with its own date text) + the same child as a flat strip
// issue (hidden while nested) + a flat strip orphan.
const stripChild = childRow("i-sc", "ENG-8", "Strip child task", 1, {
  target_date: `${plus2Key}T00:00:00Z`,
});
const stripParent = makeIssue("i-sp", "ENG-7", "Strip parent", {
  subtasks: [stripChild],
});
const stripChildFlat = makeIssue("i-sc", "ENG-8", "Strip child task");
const stripOrphan = makeIssue("i-so", "ENG-9", "Strip orphan");
const undatedPayload = [stripParent, stripChildFlat, stripOrphan];

function laneResults(qs: URLSearchParams): Issue[] {
  if (qs.get("undated") === "1") return undatedPayload;
  if (qs.get("due_after") !== null) return duePayload;
  return startPayload;
}

let lastSearch = "";
function LocationProbe() {
  const loc = useLocation();
  lastSearch = loc.search;
  return null;
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
      return Promise.resolve({
        results: laneResults(qs),
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
          <Route
            path="/w/:slug/p/:identifier/calendar"
            element={
              <>
                <LocationProbe />
                <Calendar />
              </>
            }
          />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
  return { get };
}

function issueCalls(get: ReturnType<typeof renderPage>["get"]) {
  return get.mock.calls
    .map((c) => c[0] as string)
    .filter((p) => p.includes("/issues"))
    .map((p) => new URLSearchParams(p.split("?")[1] ?? ""));
}

async function waitForInitial() {
  await screen.findByText("Parent task");
  await screen.findByText("Strip parent");
  await screen.findByText("Orphan task");
}

async function applyPql(query: string) {
  fireEvent.click(screen.getByRole("button", { name: "PQL" }));
  await screen.findByLabelText("PQL query");
  fireEvent.change(screen.getByLabelText("PQL query"), {
    target: { value: query },
  });
  fireEvent.click(screen.getByRole("button", { name: "Apply" }));
}

beforeEach(() => {
  stubMatchMedia(false);
  localStorage.clear();
  emptyViews();
  lastSearch = "";
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("calendar show sub-work items (C145T2)", () => {
  it("passes include_subtasks=1 on all three visual queries when the toggle is on (default)", async () => {
    const { get } = renderPage();
    await waitForInitial();
    const calls = issueCalls(get);
    expect(calls.length).toBeGreaterThanOrEqual(3);
    for (const qs of calls) {
      expect(qs.get("include_subtasks")).toBe("1");
    }
    expect(calls.some((qs) => qs.get("due_after") !== null)).toBe(true);
    expect(calls.some((qs) => qs.get("start_after") !== null)).toBe(true);
    expect(calls.some((qs) => qs.get("undated") === "1")).toBe(true);
    expect(
      screen.getByRole("checkbox", { name: "Show sub-work items" }),
    ).toBeChecked();
  });

  it("nests the child chip under its parent cell with the child's own date text, never also flat", async () => {
    renderPage();
    await waitForInitial();
    // One rendering of the title only: the nested chip, not a flat chip.
    expect(screen.getAllByText("Child task")).toHaveLength(1);
    const nested = document.querySelector('[data-nested-subtask="i-c"]');
    expect(nested).not.toBeNull();
    expect(nested!.textContent).toContain(
      `${fmtDay(todayKey)} → ${fmtDay(plus2Key)}`,
    );
    // The nested chip sits in the same cell stack as the parent chip:
    // its previous sibling chain contains the parent chip button.
    const parentChip = screen.getByRole("button", {
      name: /ENG-1.*Parent task/,
    });
    expect(
      parentChip.parentElement!.querySelector(
        '[data-nested-subtask="i-c"]',
      ),
    ).not.toBeNull();
  });

  it("renders a dateless nested child with no date text, never a fabricated date", async () => {
    renderPage();
    await waitForInitial();
    const nested = document.querySelector('[data-nested-subtask="i-dc"]');
    expect(nested).not.toBeNull();
    expect(nested!.textContent).not.toContain("Due");
    expect(nested!.textContent).not.toContain("Starts");
    expect(nested!.textContent).not.toContain("→");
    expect(screen.getByText("Dateless child")).toBeInTheDocument();
  });

  it("keeps an orphan child flat in its own placement", async () => {
    renderPage();
    await waitForInitial();
    expect(screen.getByText("Orphan task")).toBeInTheDocument();
    expect(
      document.querySelector('[data-nested-subtask="i-o"]'),
    ).toBeNull();
  });

  it("nests children of a strip parent in the unscheduled strip with their own date text", async () => {
    renderPage();
    await waitForInitial();
    expect(screen.getAllByText("Strip child task")).toHaveLength(1);
    const nested = document.querySelector('[data-nested-subtask="i-sc"]');
    expect(nested).not.toBeNull();
    expect(nested!.textContent).toContain(`Due ${fmtDay(plus2Key)}`);
    expect(screen.getByText("Strip orphan")).toBeInTheDocument();
  });

  it("opens the same peek when a nested child chip is clicked", async () => {
    renderPage();
    await waitForInitial();
    const nested = document.querySelector('[data-nested-subtask="i-c"]');
    fireEvent.click(nested!.querySelector("button")!);
    await waitFor(() => {
      expect(lastSearch).toContain("peek=i-c");
    });
  });

  it("omits include_subtasks when the toggle is off (stored) and renders flat cells", async () => {
    localStorage.setItem(SUB_KEY, "false");
    const { get } = renderPage();
    await waitForInitial();
    const calls = issueCalls(get);
    expect(calls.length).toBeGreaterThanOrEqual(3);
    for (const qs of calls) {
      expect(qs.get("include_subtasks")).toBeNull();
    }
    expect(
      screen.getByRole("checkbox", { name: "Show sub-work items" }),
    ).not.toBeChecked();
    // Flat: the child renders as its own chip, nothing nests.
    expect(screen.getByText("Child task")).toBeInTheDocument();
    expect(
      document.querySelectorAll("[data-nested-subtask]").length,
    ).toBe(0);
  });

  it("unchecking the toggle refetches without the flag and flattens the cells", async () => {
    const { get } = renderPage();
    await waitForInitial();
    expect(
      document.querySelector('[data-nested-subtask="i-c"]'),
    ).not.toBeNull();
    get.mockClear();
    fireEvent.click(
      screen.getByRole("checkbox", { name: "Show sub-work items" }),
    );
    await waitFor(() => {
      expect(issueCalls(get).length).toBeGreaterThanOrEqual(3);
    });
    for (const qs of issueCalls(get)) {
      expect(qs.get("include_subtasks")).toBeNull();
    }
    expect(localStorage.getItem(SUB_KEY)).toBe("false");
    await waitFor(() => {
      expect(
        document.querySelectorAll("[data-nested-subtask]").length,
      ).toBe(0);
    });
    expect(screen.getByText("Child task")).toBeInTheDocument();
  });

  it("passes include_subtasks=1 on the PQL variants when the toggle is on", async () => {
    const { get } = renderPage();
    await waitForInitial();
    await applyPql("priority = High");
    await waitFor(() => {
      const pqlCalls = issueCalls(get).filter(
        (qs) => qs.get("pql") !== null,
      );
      expect(pqlCalls.length).toBeGreaterThanOrEqual(3);
    });
    const pqlCalls = issueCalls(get).filter((qs) => qs.get("pql") !== null);
    for (const qs of pqlCalls) {
      expect(qs.get("include_subtasks")).toBe("1");
    }
    expect(pqlCalls.some((qs) => qs.get("due_after") !== null)).toBe(true);
    expect(pqlCalls.some((qs) => qs.get("start_after") !== null)).toBe(true);
    expect(pqlCalls.some((qs) => qs.get("undated") === "1")).toBe(true);
    // Nesting still holds under the PQL sets.
    expect(
      document.querySelector('[data-nested-subtask="i-c"]'),
    ).not.toBeNull();
  });
});
