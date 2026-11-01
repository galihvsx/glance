// @vitest-environment jsdom
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import Calendar from "./Calendar";
import { ApiError } from "../lib/api";

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
import { dayKey, startOfWeek, weekGrid } from "../lib/calendar";
import { formatDropDay } from "../lib/calendar-dnd";
import type { Issue } from "../lib/types";

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
const monday = startOfWeek(new Date());
const mondayKey = dayKey(monday);

function mkIssue(over: Partial<Issue> & { id: string }): Issue {
  return {
    project_id: "p",
    sequence_id: 1,
    display_id: "ENG-1",
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

const dueToday = mkIssue({
  id: "due-today",
  display_id: "ENG-1",
  name: "Fix login redirect",
  target_date: `${todayKey}T00:00:00Z`,
});
const startMonday = mkIssue({
  id: "start-monday",
  display_id: "ENG-2",
  name: "Draft release notes",
  start_date: `${mondayKey}T00:00:00Z`,
});
const dueNextWeek = mkIssue({
  id: "due-next-week",
  display_id: "ENG-3",
  name: "Rotate audit logs",
  target_date: `${dayKey(new Date(monday.getTime() + 7 * 86_400_000))}T00:00:00Z`,
});
// C26T1: spans Monday→Wednesday (start + target) to exercise
// duration-preserving drops.
const spanning = mkIssue({
  id: "spanning",
  display_id: "ENG-4",
  name: "Spanning sprint work",
  start_date: `${mondayKey}T00:00:00Z`,
  target_date: `${dayKey(new Date(monday.getTime() + 2 * 86_400_000))}T00:00:00Z`,
});
const allIssues = [dueToday, startMonday, dueNextWeek, spanning];

// C26T1: hoisted knobs for the week-view drag tests — workspace role
// (drag gate), the PATCH mock, and the toast mock.
const mockState = vi.hoisted(() => ({
  role: 15,
  patch: vi.fn(),
  toastAdd: vi.fn(),
}));

vi.mock("../lib/api", () => ({
  api: {
    get: vi.fn((path: string) => {
      if (path === "/api/v1/workspaces") {
        return Promise.resolve({
          workspaces: [{ slug: "acme", name: "Acme" }],
        });
      }
      // C26T0: workspace role query (same as the board's) — member role so
      // drag is enabled under test; week-drag tests lower it for guests.
      if (/^\/api\/v1\/workspaces\/[^/]+$/.test(path)) {
        return Promise.resolve({
          id: "w1",
          slug: "acme",
          name: "Acme",
          role: mockState.role,
          slack_configured: false,
    github_webhook_configured: false,
          created_at: "2026-01-01T00:00:00Z",
          updated_at: "2026-01-01T00:00:00Z",
        });
      }
      if (path.includes("/states")) {
        return Promise.resolve({ states: [] });
      }
      if (path.includes("undated=1")) {
        return Promise.resolve({ results: [], next_cursor: undefined });
      }
      if (path.includes("/issues")) {
        return Promise.resolve({ results: allIssues, next_cursor: undefined });
      }
      return Promise.reject(new Error("unexpected path: " + path));
    }),
    patch: mockState.patch,
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

const VIEW_KEY = "glance:calendar-view:acme:ENG";

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

async function switchToWeek() {
  const user = userEvent.setup();
  await user.click(screen.getByRole("tab", { name: "Week" }));
  return user;
}

beforeEach(() => {
  localStorage.clear();
  vi.clearAllMocks();
  emptyViews();
  mockState.role = 15;
  mockState.patch.mockReset();
  mockState.toastAdd.mockReset();
});

afterEach(() => {
  // Never leak a layout stub (or an unfinished drag's document listeners)
  // from a failed test into the next one.
  activeRestore?.();
  activeRestore = null;
});

describe("Calendar view switcher", () => {
  it("renders the month view by default with a Month/Week switcher", async () => {
    renderCalendar();
    const monthLabel = new Date().toLocaleDateString(undefined, {
      month: "long",
      year: "numeric",
    });
    expect(
      await screen.findByRole("heading", { name: monthLabel }),
    ).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Month" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(screen.getByRole("tab", { name: "Week" })).toHaveAttribute(
      "aria-selected",
      "false",
    );
  });

  it("switches to week view and persists the choice in localStorage", async () => {
    renderCalendar();
    await screen.findByRole("heading"); // wait for load
    await switchToWeek();
    expect(screen.getByRole("tab", { name: "Week" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(localStorage.getItem(VIEW_KEY)).toBe("week");
    // Switching back persists "month" too.
    const user = userEvent.setup();
    await user.click(screen.getByRole("tab", { name: "Month" }));
    expect(localStorage.getItem(VIEW_KEY)).toBe("month");
  });

  it("restores the week view from localStorage", async () => {
    localStorage.setItem(VIEW_KEY, "week");
    renderCalendar();
    await screen.findByRole("heading");
    expect(screen.getByRole("tab", { name: "Week" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
  });
});

describe("Calendar week view", () => {
  it("renders 7 Monday-first day columns with weekday name + date number", async () => {
    renderCalendar();
    await screen.findByRole("heading");
    await switchToWeek();
    const days = weekGrid(new Date());
    const names = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];
    for (let i = 0; i < 7; i++) {
      expect(screen.getByText(names[i])).toBeInTheDocument();
      expect(screen.getByText(String(days[i].getDate()))).toBeInTheDocument();
    }
  });

  it("stacks issue chips on the right days (due date, else start date)", async () => {
    renderCalendar();
    await screen.findByRole("heading");
    await switchToWeek();
    expect(await screen.findByText("Fix login redirect")).toBeInTheDocument();
    expect(screen.getByText("Draft release notes")).toBeInTheDocument();
    // Due next week: visible in the month view, not in this week.
    expect(screen.queryByText("Rotate audit logs")).not.toBeInTheDocument();
  });

  it("highlights the current day", async () => {
    renderCalendar();
    await screen.findByRole("heading");
    await switchToWeek();
    const num = String(new Date().getDate());
    const el = screen.getByText(num);
    expect(el.className).toContain("bg-accent-blue");
  });

  it("prev/next-week navigation moves a week and Today jumps back", async () => {
    renderCalendar();
    await screen.findByRole("heading");
    const user = await switchToWeek();
    const todayNum = String(new Date().getDate());

    await user.click(screen.getByRole("button", { name: "Previous week" }));
    // The current week (and today's number) is gone…
    expect(screen.queryByText(todayNum)).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Today" }));
    // …and Today brings it back.
    expect(screen.getByText(todayNum)).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Next week" }));
    expect(screen.queryByText(todayNum)).not.toBeInTheDocument();
  });

  it("week navigation keeps the same view (still week after moving)", async () => {
    renderCalendar();
    await screen.findByRole("heading");
    const user = await switchToWeek();
    await user.click(screen.getByRole("button", { name: "Previous week" }));
    expect(screen.getByRole("tab", { name: "Week" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
  });
});

// ---------------------------------------------------------------------------
// C26T1: week-view drag-reschedule. Real pointer-event drags against the
// dnd-kit sensors (PointerEvent polyfill + stubbed layout, since jsdom has
// neither pointer events nor layout).
// ---------------------------------------------------------------------------

const COL_W = 140;
const COL_Y = 100;
const COL_H = 256;

function makeRect(x: number, y: number, w: number, h: number): DOMRect {
  return {
    x,
    y,
    width: w,
    height: h,
    top: y,
    left: x,
    right: x + w,
    bottom: y + h,
    toJSON: () => ({}),
  } as DOMRect;
}

// Last dispatched pointer position — the DragOverlay ghost's rect is
// measured against it (dnd-kit translates the measured rect by the drag
// delta for collision detection, and jsdom measures everything as 0x0).
let pointerPos = { x: 0, y: 0 };

// Tracks the currently installed layout stub so a test that fails mid-drag
// can never leak it (or dnd-kit's document listeners from an unfinished
// drag) into the next test.
let activeRestore: (() => void) | null = null;

function installLayoutStub(): () => void {
  const columns = Array.from(
    document.querySelectorAll<HTMLElement>('[data-testid^="week-column-"]'),
  );
  const orig = Element.prototype.getBoundingClientRect;
  Element.prototype.getBoundingClientRect = function (this: Element) {
    const el = this as HTMLElement;
    const colIdx = columns.findIndex((c) => c === el || c.contains(el));
    if (colIdx >= 0) {
      if (columns[colIdx] === el) {
        return makeRect(colIdx * COL_W, COL_Y, COL_W, COL_H);
      }
      // A chip inside the column.
      return makeRect(colIdx * COL_W + 8, COL_Y + 10, COL_W - 16, 22);
    }
    // DragOverlay ghost (the w-56 wrapper) — follows the pointer.
    if (typeof el.className === "string" && el.className.includes("w-56")) {
      return makeRect(pointerPos.x - 112, pointerPos.y - 12, 224, 24);
    }
    return orig.call(this);
  };
  return () => {
    Element.prototype.getBoundingClientRect = orig;
  };
}

const PE = () =>
  window.PointerEvent as unknown as new (
    type: string,
    init: Record<string, unknown>,
  ) => PointerEvent;

function pointerInit(x: number, y: number): Record<string, unknown> {
  pointerPos = { x, y };
  return {
    bubbles: true,
    cancelable: true,
    clientX: x,
    clientY: y,
    button: 0,
    isPrimary: true,
    pointerId: 1,
  };
}

interface ActiveDrag {
  target: HTMLElement;
  drop: () => Promise<void>;
}

/** pointerdown on the chip + two moves (activate, then travel to the target). */
async function beginDrag(
  chipName: RegExp,
  targetDayKey: string,
): Promise<ActiveDrag> {
  const chip = screen.getByRole("button", { name: chipName });
  const target = screen.getByTestId(`week-column-${targetDayKey}`);
  const restore = installLayoutStub();
  activeRestore = restore;
  const chipRect = chip.getBoundingClientRect();
  const targetRect = target.getBoundingClientRect();
  const startX = chipRect.x + chipRect.width / 2;
  const startY = chipRect.y + chipRect.height / 2;
  const endX = targetRect.x + targetRect.width / 2;
  const endY = targetRect.y + targetRect.height / 2;
  const Ctor = PE();
  await act(async () => {
    chip.dispatchEvent(new Ctor("pointerdown", pointerInit(startX, startY)));
  });
  // Separate act() blocks: the first move activates the drag (the overlay
  // mounts and is measured at the activation point); the second move then
  // travels with a correct translate. Batching both moves in one act()
  // measures the overlay at the travel endpoint and double-counts the delta.
  await act(async () => {
    document.dispatchEvent(
      new Ctor("pointermove", pointerInit(startX + 30, startY)),
    );
  });
  await act(async () => {
    document.dispatchEvent(new Ctor("pointermove", pointerInit(endX, endY)));
  });
  return {
    target,
    drop: async () => {
      try {
        await act(async () => {
          document.dispatchEvent(
            new Ctor("pointerup", pointerInit(endX, endY)),
          );
        });
      } finally {
        restore();
        activeRestore = null;
      }
    },
  };
}

async function dragChipToDay(chipName: RegExp, targetDayKey: string) {
  const drag = await beginDrag(chipName, targetDayKey);
  await drag.drop();
}

function weekDayKeys(): string[] {
  return weekGrid(new Date()).map((d) => dayKey(d));
}

describe("Calendar week view drag-reschedule", () => {
  it("renders draggable chips and droppable week columns when the user can edit", async () => {
    renderCalendar();
    await screen.findByRole("heading");
    await switchToWeek();
    const keys = weekDayKeys();
    for (const key of keys) {
      expect(screen.getByTestId(`week-column-${key}`)).toBeInTheDocument();
    }
    const chip = screen.getByRole("button", {
      name: /ENG-2.*Draft release notes/,
    });
    expect(chip.className).toContain("cursor-grab");
    expect(chip).toHaveAttribute("aria-roledescription", "draggable");
  });

  it("drops a start-dated chip on another column and PATCHes the shifted start date", async () => {
    mockState.patch.mockResolvedValue({});
    renderCalendar();
    await screen.findByRole("heading");
    await switchToWeek();
    const keys = weekDayKeys();

    await dragChipToDay(/ENG-2.*Draft release notes/, keys[2]);

    await waitFor(() =>
      expect(mockState.patch).toHaveBeenCalledWith(
        "/api/v1/workspaces/acme/projects/ENG/issues/start-monday",
        { start_date: keys[2] },
      ),
    );
  });

  it("preserves duration when the chip has both a start and a target date", async () => {
    mockState.patch.mockImplementation(() => new Promise(() => {})); // hang: observe the optimistic state
    renderCalendar();
    await screen.findByRole("heading");
    await switchToWeek();
    const keys = weekDayKeys();
    // spanning: start Monday, target Wednesday — the chip sits on its
    // target day (Wednesday). Dropping on Friday shifts both by +2.
    expect(
      within(screen.getByTestId(`week-column-${keys[2]}`)).getByText(
        "Spanning sprint work",
      ),
    ).toBeInTheDocument();

    await dragChipToDay(/ENG-4.*Spanning sprint work/, keys[4]);

    await waitFor(() =>
      expect(mockState.patch).toHaveBeenCalledWith(
        "/api/v1/workspaces/acme/projects/ENG/issues/spanning",
        { start_date: keys[2], target_date: keys[4] },
      ),
    );
    // Optimistic: the chip already sits on Friday before the PATCH resolves.
    expect(
      within(screen.getByTestId(`week-column-${keys[4]}`)).getByText(
        "Spanning sprint work",
      ),
    ).toBeInTheDocument();
  });

  it("issues no PATCH when dropped on the chip's own day", async () => {
    mockState.patch.mockResolvedValue({});
    renderCalendar();
    await screen.findByRole("heading");
    await switchToWeek();
    const keys = weekDayKeys();

    await dragChipToDay(/ENG-2.*Draft release notes/, keys[0]);

    // Let any stray mutation flush.
    await waitFor(() =>
      expect(screen.getByTestId(`week-column-${keys[0]}`)).toBeInTheDocument(),
    );
    expect(mockState.patch).not.toHaveBeenCalled();
  });

  it("highlights the hovered week column for the whole column while dragging", async () => {
    mockState.patch.mockResolvedValue({});
    renderCalendar();
    await screen.findByRole("heading");
    await switchToWeek();
    const keys = weekDayKeys();

    const drag = await beginDrag(/ENG-2.*Draft release notes/, keys[3]);
    try {
      // The drop highlight covers the full tall column…
      expect(drag.target.className).toContain("min-h-64");
      expect(drag.target.className).toContain("border-accent-blue/70");
      // …and no other column is highlighted.
      for (const key of keys) {
        if (key === keys[3]) continue;
        expect(
          screen.getByTestId(`week-column-${key}`).className,
        ).not.toContain("border-accent-blue/70");
      }
    } finally {
      // Always finish the drag — an unfinished drag leaves dnd-kit's
      // document listeners (e.g. click stopPropagation) behind.
      await drag.drop();
    }
    // Highlight clears after the drop.
    expect(drag.target.className).not.toContain("border-accent-blue/70");
  });

  it("rolls back the optimistic move and toasts when the PATCH fails", async () => {
    mockState.patch.mockRejectedValue(
      new ApiError(400, "start_date cannot be after target_date"),
    );
    renderCalendar();
    await screen.findByRole("heading");
    await switchToWeek();
    const keys = weekDayKeys();
    const monCol = () => screen.getByTestId(`week-column-${keys[0]}`);
    const wedCol = () => screen.getByTestId(`week-column-${keys[2]}`);

    await dragChipToDay(/ENG-2.*Draft release notes/, keys[2]);

    // Error toast carries the server message…
    await waitFor(() =>
      expect(mockState.toastAdd).toHaveBeenCalledWith(
        expect.objectContaining({
          type: "error",
          description: "start_date cannot be after target_date",
        }),
      ),
    );
    // …and the chip is back on its original day.
    await waitFor(() => {
      expect(
        within(monCol()).getByText("Draft release notes"),
      ).toBeInTheDocument();
      expect(
        within(wedCol()).queryByText("Draft release notes"),
      ).not.toBeInTheDocument();
    });
  });

  it("announces the move in the aria-live region", async () => {
    mockState.patch.mockResolvedValue({});
    renderCalendar();
    await screen.findByRole("heading");
    await switchToWeek();
    const keys = weekDayKeys();

    await dragChipToDay(/ENG-2.*Draft release notes/, keys[2]);

    await screen.findByText(/ENG-2 moved to/);
  });

  it("disables drag for guests (role < 15) in the week view", async () => {
    mockState.role = 5;
    renderCalendar();
    await screen.findByRole("heading");
    await switchToWeek();
    const chip = screen.getByRole("button", {
      name: /ENG-2.*Draft release notes/,
    });
    expect(chip.className).not.toContain("cursor-grab");
    expect(chip).not.toHaveAttribute("aria-roledescription");
    // Click-to-peek still works for guests.
    expect(chip).toHaveAttribute("title", "ENG-2: Draft release notes");
  });
});

// ---------------------------------------------------------------------------
// C26T3: day-cell quick-add. The "+" on a day cell navigates to the issues
// page, opening the EXISTING create dialog with the day prefilled as the
// target (due) date — same location.state mechanism the board's
// expand-quick-add uses.
// ---------------------------------------------------------------------------

let lastLocation: { pathname: string; state: unknown } | null = null;

function LocationProbe() {
  const loc = useLocation();
  lastLocation = { pathname: loc.pathname, state: loc.state };
  return null;
}

function renderCalendarWithIssuesRoute() {
  lastLocation = null;
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
          <Route path="/w/:slug/p/:identifier" element={<LocationProbe />} />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

describe("Calendar day-cell quick-add", () => {
  it("shows a + affordance on every month day cell for members", async () => {
    renderCalendarWithIssuesRoute();
    // Wait for the month grid itself, not just the page header.
    const btns = await screen.findAllByRole("button", { name: /Add issue on/ });
    // Full 6-row month grid.
    expect(btns.length).toBe(42);
  });

  it("clicking a day's + navigates to the issues page with the day as target_date", async () => {
    const user = userEvent.setup();
    renderCalendarWithIssuesRoute();
    const btn = await screen.findByRole("button", {
      name: `Add issue on ${formatDropDay(todayKey)}`,
    });
    await user.click(btn);
    await waitFor(() => expect(lastLocation).not.toBeNull());
    expect(lastLocation!.pathname).toBe("/w/acme/p/ENG");
    expect(lastLocation!.state).toEqual({
      newIssue: true,
      newIssueTargetDate: todayKey,
    });
  });

  it("Enter on the focused + opens the create flow for that day (keyboard)", async () => {
    const user = userEvent.setup();
    renderCalendarWithIssuesRoute();
    const btn = await screen.findByRole("button", {
      name: `Add issue on ${formatDropDay(todayKey)}`,
    });
    btn.focus();
    await user.keyboard("{Enter}");
    await waitFor(() => expect(lastLocation).not.toBeNull());
    expect(lastLocation!.state).toEqual({
      newIssue: true,
      newIssueTargetDate: todayKey,
    });
  });

  it("shows a + affordance on every week column for members", async () => {
    renderCalendarWithIssuesRoute();
    await screen.findByRole("heading");
    await switchToWeek();
    const btns = screen.getAllByRole("button", { name: /Add issue on/ });
    expect(btns.length).toBe(7);
  });

  it("clicking a week column's + prefills that column's day", async () => {
    const user = userEvent.setup();
    renderCalendarWithIssuesRoute();
    await screen.findByRole("heading");
    await switchToWeek();
    const keys = weekDayKeys();
    await user.click(
      screen.getByRole("button", { name: `Add issue on ${formatDropDay(keys[3])}` }),
    );
    await waitFor(() => expect(lastLocation).not.toBeNull());
    expect(lastLocation!.state).toEqual({
      newIssue: true,
      newIssueTargetDate: keys[3],
    });
  });

  it("hides every + affordance for guests (role < 15)", async () => {
    mockState.role = 5;
    renderCalendarWithIssuesRoute();
    await screen.findByRole("heading");
    expect(
      screen.queryByRole("button", { name: /Add issue on/ }),
    ).not.toBeInTheDocument();
    await switchToWeek();
    expect(
      screen.queryByRole("button", { name: /Add issue on/ }),
    ).not.toBeInTheDocument();
  });
});
