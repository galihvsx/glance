// @vitest-environment jsdom
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import Calendar from "./Calendar";
import { dayKey } from "../lib/calendar";
import type { EpicRef, Issue, IssueState } from "../lib/types";

// jsdom has no PointerEvent — the dnd-kit sensors need isPrimary/clientX.
if (typeof window.PointerEvent === "undefined") {
  class PointerEventPolyfill extends MouseEvent {
    readonly isPrimary: boolean;
    readonly pointerId: number;
    readonly pointerType: string;
    constructor(type: string, init: PointerEventInit = {}) {
      super(type, init);
      this.isPrimary = init.isPrimary ?? true;
      this.pointerId = init.pointerId ?? 1;
      this.pointerType = init.pointerType ?? "mouse";
    }
  }
  window.PointerEvent = PointerEventPolyfill as unknown as typeof PointerEvent;
}

const todayKey = dayKey(new Date());

function mkState(id: string, name: string, group: string): IssueState {
  return {
    id,
    project_id: "p",
    name,
    group,
    color: "#fff",
    sequence: 0,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  };
}

const states = [
  mkState("s-backlog", "Backlog", "backlog"),
  mkState("s-unstarted", "Unstarted", "unstarted"),
  mkState("s-started", "Started", "started"),
  mkState("s-completed", "Completed", "completed"),
  mkState("s-cancelled", "Cancelled", "cancelled"),
];

const eAuth: EpicRef = { id: "e-auth", name: "Auth revamp" };
const ePay: EpicRef = { id: "e-pay", name: "Payments v2" };
const eLegacy: EpicRef = {
  id: "e-legacy",
  name: "Legacy SSO",
  archived_at: "2026-01-01T00:00:00Z",
};

function mkIssue(over: Partial<Issue> & { id: string }): Issue {
  return {
    project_id: "p",
    sequence_id: 1,
    display_id: "ENG-1",
    name: "test",
    priority: 0,
    state_id: "s-unstarted",
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

// All due today — inside both the month and week query ranges.
const dated = [
  mkIssue({
    id: "i101",
    sequence_id: 101,
    display_id: "ENG-101",
    name: "Auth login screen",
    target_date: `${todayKey}T00:00:00Z`,
    state_id: "s-unstarted",
    epics: [eAuth],
  }),
  mkIssue({
    id: "i102",
    sequence_id: 102,
    display_id: "ENG-102",
    name: "OAuth refresh",
    target_date: `${todayKey}T00:00:00Z`,
    state_id: "s-completed",
    epics: [eAuth],
  }),
  // Multi-appearance: attached to two live epics.
  mkIssue({
    id: "i103",
    sequence_id: 103,
    display_id: "ENG-103",
    name: "Mobile SDK release",
    target_date: `${todayKey}T00:00:00Z`,
    state_id: "s-started",
    epics: [eAuth, ePay],
  }),
  // Archived-epic-only → must land in Ungrouped.
  mkIssue({
    id: "i104",
    sequence_id: 104,
    display_id: "ENG-104",
    name: "Decommission SSO",
    target_date: `${todayKey}T00:00:00Z`,
    state_id: "s-cancelled",
    epics: [eLegacy],
  }),
  mkIssue({
    id: "i105",
    sequence_id: 105,
    display_id: "ENG-105",
    name: "Update API docs",
    target_date: `${todayKey}T00:00:00Z`,
    state_id: "s-backlog",
  }),
];

const undated = [
  mkIssue({
    id: "i110",
    sequence_id: 110,
    display_id: "ENG-110",
    name: "Dark mode",
    state_id: "s-unstarted",
    epics: [eAuth],
  }),
  mkIssue({
    id: "i111",
    sequence_id: 111,
    display_id: "ENG-111",
    name: "Refund webhook",
    state_id: "s-unstarted",
    epics: [ePay],
  }),
  mkIssue({
    id: "i112",
    sequence_id: 112,
    display_id: "ENG-112",
    name: "Changelog draft",
    state_id: "s-backlog",
  }),
];

const mockCal = vi.hoisted(() => ({
  role: 15,
  dated: [] as Issue[],
  undated: [] as Issue[],
  states: [] as IssueState[],
}));

vi.mock("../lib/api", () => ({
  api: {
    get: vi.fn((path: string) => {
      if (path === "/api/v1/workspaces") {
        return Promise.resolve({
          workspaces: [{ slug: "acme", name: "Acme" }],
        });
      }
      if (/^\/api\/v1\/workspaces\/[^/]+$/.test(path)) {
        return Promise.resolve({
          id: "w1",
          slug: "acme",
          name: "Acme",
          role: mockCal.role,
          slack_configured: false,
          github_webhook_configured: false,
          created_at: "2026-01-01T00:00:00Z",
          updated_at: "2026-01-01T00:00:00Z",
        });
      }
      if (path.includes("/states")) {
        return Promise.resolve({ states: mockCal.states });
      }
      if (path.includes("undated=1")) {
        return Promise.resolve({
          results: mockCal.undated,
          next_cursor: undefined,
        });
      }
      if (path.includes("/issues")) {
        return Promise.resolve({ results: mockCal.dated, next_cursor: undefined });
      }
      return Promise.reject(new Error("unexpected path: " + path));
    }),
    patch: vi.fn(),
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
  toast: { add: vi.fn() },
}));

const GROUP_KEY = "glance:calendar-groupby:acme:ENG";

function renderCalendar() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <MemoryRouter initialEntries={["/w/acme/p/ENG/calendar"]}>
      <QueryClientProvider client={client}>
        <Routes>
          <Route
            path="/w/:slug/p/:identifier/calendar"
            element={<Calendar />}
          />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

async function enableEpicGrouping() {
  const user = userEvent.setup();
  // Wait for the issue data to land — the month-label heading renders
  // while the calendar is still loading, so it can't gate on load.
  await screen.findByTitle("ENG-101: Auth login screen");
  await user.click(screen.getByRole("tab", { name: "Epic" }));
  await screen.findAllByTestId("epic-lane");
  return user;
}

function laneKeys() {
  return screen
    .getAllByTestId("epic-lane")
    .map((el) => el.getAttribute("data-lane-key"));
}

beforeEach(() => {
  localStorage.clear();
  vi.clearAllMocks();
  mockCal.role = 15;
  mockCal.dated = dated;
  mockCal.undated = undated;
  mockCal.states = states;
});

describe("Calendar group by epic — month view", () => {
  it("renders swimlane gutters with live epics first and Ungrouped last", async () => {
    renderCalendar();
    await enableEpicGrouping();
    // One gutter per week row (6); the first gutter's lanes:
    expect(laneKeys().slice(0, 3)).toEqual([
      "e-auth",
      "e-pay",
      "epic:ungrouped",
    ]);
  });

  it("shows per-lane counts and Done X of N captions from member states", async () => {
    renderCalendar();
    await enableEpicGrouping();
    const firstGutter = screen.getAllByTestId("epic-lane").slice(0, 3);
    // Auth revamp: unstarted + completed + started → Done 1 of 3
    expect(
      within(firstGutter[0]).getByText("Done 1 of 3"),
    ).toBeInTheDocument();
    // Payments v2: started only → Done 0 of 1
    expect(
      within(firstGutter[1]).getByText("Done 0 of 1"),
    ).toBeInTheDocument();
    // Ungrouped: cancelled + backlog → Done 1 of 2
    expect(
      within(firstGutter[2]).getByText("Done 1 of 2"),
    ).toBeInTheDocument();
  });

  it("reuses the exact GROUP_COLORS segment math in the compact bar", async () => {
    renderCalendar();
    await enableEpicGrouping();
    const bar = within(screen.getAllByTestId("epic-lane")[0]).getByRole(
      "progressbar",
      { name: "Auth revamp progress" },
    );
    // 5-segment order (no triage): unstarted, started, completed —
    // done = completed + cancelled.
    expect(bar).toHaveAttribute("aria-valuemax", "3");
    expect(bar).toHaveAttribute("aria-valuenow", "1");
    const segs = Array.from(bar.children) as HTMLElement[];
    expect(segs).toHaveLength(3);
    for (const s of segs) {
      expect(parseFloat(s.style.width)).toBeCloseTo(100 / 3, 4);
    }
    expect(segs.map((s) => s.style.backgroundColor)).toEqual([
      "rgb(229, 161, 60)", // unstarted
      "rgb(79, 142, 247)", // started
      "rgb(66, 200, 138)", // completed
    ]);
  });

  it("renders multi-appearance issues as plain duplicate chips (no link glyph)", async () => {
    renderCalendar();
    await enableEpicGrouping();
    // ENG-103 belongs to Auth revamp AND Payments v2 → two chips.
    const chips = screen.getAllByTitle("ENG-103: Mobile SDK release");
    expect(chips).toHaveLength(2);
    for (const chip of chips) {
      expect(chip.querySelector("svg")).toBeNull();
    }
  });

  it("excludes archived epics — their issues land in Ungrouped", async () => {
    renderCalendar();
    await enableEpicGrouping();
    expect(screen.queryByText("Legacy SSO")).toBeNull();
    // ENG-104's only epic is archived → exactly one chip, in Ungrouped.
    expect(
      screen.getAllByTitle("ENG-104: Decommission SSO"),
    ).toHaveLength(1);
  });

  it("hides the Ungrouped lane when it is empty", async () => {
    mockCal.dated = dated.slice(0, 3); // every issue has a live epic
    renderCalendar();
    await enableEpicGrouping();
    expect(laneKeys()).not.toContain("epic:ungrouped");
    expect(laneKeys()).toHaveLength(12); // 6 week rows × 2 lanes
  });
});

describe("Calendar group by epic — week view", () => {
  it("groups the week grid into swimlanes with the same semantics", async () => {
    renderCalendar();
    const user = userEvent.setup();
    await screen.findByTitle("ENG-101: Auth login screen");
    await user.click(screen.getByRole("tab", { name: "Week" }));
    // The week view renders 7 droppable columns (then refetches its range).
    await screen.findAllByTestId(/week-column-/);
    await screen.findByTitle("ENG-101: Auth login screen");
    await user.click(screen.getByRole("tab", { name: "Epic" }));
    await screen.findAllByTestId("epic-lane");
    // Single gutter in the week view.
    expect(laneKeys()).toEqual(["e-auth", "e-pay", "epic:ungrouped"]);
    // Multi-appearance chip renders in both lane bands.
    expect(
      screen.getAllByTitle("ENG-103: Mobile SDK release"),
    ).toHaveLength(2);
    // Archived epic excluded.
    expect(screen.queryByText("Legacy SSO")).toBeNull();
  });
});

describe("Calendar group by epic — unscheduled strip", () => {
  it("groups the strip into lane clusters with Ungrouped last, still one droppable", async () => {
    renderCalendar();
    await enableEpicGrouping();
    const strip = screen.getByText("Unscheduled (3)").closest("section")!;
    const keys = within(strip)
      .getAllByTestId("strip-lane")
      .map((el) => el.getAttribute("data-lane-key"));
    expect(keys).toEqual(["e-auth", "e-pay", "epic:ungrouped"]);
    const lanes = within(strip).getAllByTestId("strip-lane");
    expect(
      within(lanes[0]).getByTitle("ENG-110: Dark mode"),
    ).toBeInTheDocument();
    expect(
      within(lanes[1]).getByTitle("ENG-111: Refund webhook"),
    ).toBeInTheDocument();
    expect(
      within(lanes[2]).getByTitle("ENG-112: Changelog draft"),
    ).toBeInTheDocument();
    // Q3 contract: the strip stays ONE droppable.
    expect(
      strip.querySelectorAll('[data-droppable-id="unscheduled-strip"]'),
    ).toHaveLength(1);
  });

  it("hides the strip's Ungrouped cluster when it is empty", async () => {
    mockCal.undated = undated.slice(0, 2); // both have live epics
    renderCalendar();
    await enableEpicGrouping();
    const strip = screen.getByText("Unscheduled (2)").closest("section")!;
    const keys = within(strip)
      .getAllByTestId("strip-lane")
      .map((el) => el.getAttribute("data-lane-key"));
    expect(keys).toEqual(["e-auth", "e-pay"]);
  });
});

describe("Calendar group by epic — setting persistence", () => {
  it("defaults to None and persists the Group choice per project", async () => {
    const { unmount } = renderCalendar();
    const user = await enableEpicGrouping();
    const epicTab = screen.getByRole("tab", { name: "Epic" });
    expect(epicTab).toHaveAttribute("aria-selected", "true");
    expect(localStorage.getItem(GROUP_KEY)).toBe("epic");
    expect(screen.getAllByTestId("epic-lane").length).toBeGreaterThan(0);

    // Remount restores the grouped view from localStorage.
    unmount();
    renderCalendar();
    await screen.findByTitle("ENG-101: Auth login screen");
    expect(screen.getByRole("tab", { name: "Epic" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(screen.getAllByTestId("epic-lane").length).toBeGreaterThan(0);

    // Back to None clears the swimlanes and persists "none".
    await user.click(screen.getByRole("tab", { name: "None" }));
    expect(localStorage.getItem(GROUP_KEY)).toBe("none");
    expect(screen.queryAllByTestId("epic-lane")).toHaveLength(0);
    expect(screen.queryByText("Epic lane")).toBeNull();
  });

  it("renders the plain ungrouped calendar by default (regression)", async () => {
    renderCalendar();
    await screen.findByTitle("ENG-101: Auth login screen");
    expect(screen.getByRole("tab", { name: "None" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(screen.queryAllByTestId("epic-lane")).toHaveLength(0);
    expect(screen.queryByText("Epic lane")).toBeNull();
    // Multi-epic issues render once when grouping is off.
    expect(
      screen.getAllByTitle("ENG-103: Mobile SDK release"),
    ).toHaveLength(1);
  });
});
