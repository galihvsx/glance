// @vitest-environment jsdom
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import Calendar from "./Calendar";
import { dayKey, startOfWeek, weekGrid } from "../lib/calendar";
import type { Issue } from "../lib/types";

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
const allIssues = [dueToday, startMonday, dueNextWeek];

vi.mock("../lib/api", () => ({
  api: {
    get: vi.fn((path: string) => {
      if (path === "/api/v1/workspaces") {
        return Promise.resolve({
          workspaces: [{ slug: "acme", name: "Acme" }],
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
  },
  ApiError: class ApiError extends Error {},
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
