// @vitest-environment jsdom
// Unscheduled-lane drag to schedule (C156T1): dragging a top-level
// unscheduled label row onto the timeline PATCHes both dates to the
// drop day (a 1-day bar). Harness follows GanttSingleDateDrag.test.tsx
// / GanttChildDrag.test.tsx (mocked api, jsdom PointerEvent polyfill,
// makeIssue with nullable dates).
//
// jsdom note: getBoundingClientRect returns zeros, so svgX(clientX)
// === clientX. The window is Nov-2026-centered (2026-10-01 ..
// 2026-12-31, DAY_W 28): clientX 1120 is day index 40, i.e.
// 2026-11-10; a negative clientX is a release over the label column.

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
vi.mock("../lib/auth", () => ({
  useAuth: () => ({ user: { id: "u-1", email: "u@x.com" }, loading: false }),
}));
import Gantt from "./Gantt";
import { api } from "../lib/api";
import type { Issue, IssueState } from "../lib/types";
import {
  makeQueryClient,
  stubMatchMedia,
} from "../components/shell/test-utils";

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
  sequenceId = 1,
): Issue {
  return {
    id,
    project_id: "p-eng",
    sequence_id: sequenceId,
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

// Nov-2026-centered window is 2026-10-01 .. 2026-12-31. The dated
// issue renders a bar (group row + issue row); the undated issue
// renders in the lane (header row + label row), so its ghost sits at
// y = 56 + 3*34 = 158, bar y = 167.
const dated = makeIssue(
  "i-d",
  "ENG-1",
  "Dated task",
  "2026-11-10T00:00:00Z",
  "2026-11-12T00:00:00Z",
  2,
);
const undated = makeIssue("i-u", "ENG-9", "Undated task", null, null, 1);

// Full-Issue mirror of the undated issue, served ONLY on the peek
// detail path (PeekDrawer fetches one issue by id).
const byId: Record<string, Issue> = { "i-d": dated, "i-u": undated };

function renderPage(opts: { role?: number } = {}) {
  const get = vi.spyOn(api, "get").mockImplementation((path: string) => {
    if (path === "/api/v1/workspaces")
      return Promise.resolve({
        workspaces: [{ slug: "acme", name: "Acme" }],
      });
    if (path === "/api/v1/workspaces/acme")
      return Promise.resolve({ role: opts.role ?? 15 });
    if (path === `${BASE}/states`)
      return Promise.resolve({ states: [stateStarted] });
    // Peek-drawer support paths (detail content fetches).
    if (path.includes("/subscribers"))
      return Promise.resolve({ subscribers: [] });
    if (path === `${BASE}/labels`) return Promise.resolve({ labels: [] });
    if (path === "/api/v1/workspaces/acme/members")
      return Promise.resolve({ members: [] });
    if (path.startsWith(`${BASE}/issues/`)) {
      const id = path.slice(`${BASE}/issues/`.length);
      const issue = byId[id];
      if (issue) return Promise.resolve(issue);
    }
    if (path.includes("/issues")) {
      const qs = new URLSearchParams(path.split("?")[1] ?? "");
      if (qs.get("undated") !== null)
        return Promise.resolve({ results: [undated], links: [], next_cursor: null });
      return Promise.resolve({ results: [dated], links: [], next_cursor: null });
    }
    return Promise.reject(new Error(`unexpected GET ${path}`));
  });
  const patch = vi.spyOn(api, "patch").mockImplementation(() =>
    Promise.resolve({ ...undated }),
  );
  const post = vi.spyOn(api, "post").mockImplementation(() =>
    Promise.resolve({ id: "l-1", kind: "blocks" }),
  );
  const qc = makeQueryClient();
  const { container } = render(
    <MemoryRouter initialEntries={["/w/acme/p/ENG/gantt"]}>
      <QueryClientProvider client={qc}>
        <Routes>
          <Route path="/w/:slug/p/:identifier/gantt" element={<Gantt />} />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
  return { get, patch, post, container };
}

async function waitForLane() {
  await waitFor(() => {
    expect(screen.queryByText("Unscheduled")).not.toBeNull();
    expect(screen.queryByText("Undated task")).not.toBeNull();
    expect(screen.queryByText("Dated task")).not.toBeNull();
  });
}

/** The top-level unscheduled label button (its title is unique: the
    undated issue renders no bar, so no bar rect shares it). */
function laneButton(): Element {
  return screen.getByTitle("ENG-9: Undated task");
}

function scheduleGhost(container: HTMLElement) {
  return container.querySelector('rect[data-schedule-ghost="true"]');
}

function peekDialog() {
  return screen.queryByRole("dialog", { name: "Issue peek view" });
}

/** The timeline svg — the lane button lives in the gutter, NOT
    inside the svg, so it cannot resolve it via closest(). The dated
    bar rect is inside the timeline svg; the first svg in the
    container would be a PageHeader icon, which is outside the page
    handlers entirely. */
function timelineSvg(container: HTMLElement): Element {
  const bar = container.querySelector('rect[fill="#6366f1"]');
  expect(bar).not.toBeNull();
  const svg = bar!.closest("svg");
  expect(svg).not.toBeNull();
  return svg!;
}

/** Drag the lane button onto the timeline at clientX and release
    over the svg. clientX 1120 = day index 40 = 2026-11-10. */
function dragTo(container: HTMLElement, button: Element, clientX: number) {
  const svg = timelineSvg(container);
  fireEvent.pointerDown(button, { button: 0, clientX: 100, clientY: 100 });
  fireEvent.pointerMove(svg, { clientX, clientY: 100 });
  fireEvent.pointerUp(svg, { clientX, clientY: 100 });
}

beforeEach(() => {
  stubMatchMedia(false);
  localStorage.clear();
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("Gantt unscheduled drag (C156T1)", () => {
  it("pointer drag from an unscheduled row onto a timeline day PATCHes both dates to that day", async () => {
    const { patch, container } = renderPage();
    await waitForLane();
    const button = laneButton();
    const svg = timelineSvg(container);
    fireEvent.pointerDown(button, { button: 0, clientX: 100, clientY: 100 });
    fireEvent.pointerMove(svg, { clientX: 1120, clientY: 100 });
    // Mid-drag the 1-day ghost sits at the pointer day on the
    // dragged row's track, reusing the C146 ghost token verbatim.
    const ghost = scheduleGhost(container);
    expect(ghost).not.toBeNull();
    expect(ghost!.getAttribute("x")).toBe("1122");
    expect(ghost!.getAttribute("y")).toBe("167");
    expect(ghost!.getAttribute("width")).toBe("24");
    expect(ghost!.getAttribute("fill-opacity")).toBe("0.35");
    expect(ghost!.getAttribute("stroke-dasharray")).toBe("4 3");
    expect(ghost!.getAttribute("aria-label")).toBe(
      "Schedule preview for ENG-9",
    );
    fireEvent.pointerUp(svg, { clientX: 1120, clientY: 100 });
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(`${BASE}/issues/i-u`, {
        start_date: "2026-11-10",
        target_date: "2026-11-10",
      });
    });
    // The ghost is gone once the drag commits, and the dated
    // issue is untouched by the schedule.
    expect(scheduleGhost(container)).toBeNull();
    expect(patch).not.toHaveBeenCalledWith(
      `${BASE}/issues/i-d`,
      expect.anything(),
    );
  });

  it("drop over the label column PATCHes nothing", async () => {
    const { patch, container } = renderPage();
    await waitForLane();
    const button = laneButton();
    const svg = timelineSvg(container);
    fireEvent.pointerDown(button, { button: 0, clientX: 100, clientY: 100 });
    fireEvent.pointerMove(svg, { clientX: 1120, clientY: 100 });
    // A negative clientX is left of the svg = over the label column.
    fireEvent.pointerUp(svg, { clientX: -20, clientY: 100 });
    await new Promise((r) => setTimeout(r, 50));
    expect(patch).not.toHaveBeenCalled();
    expect(scheduleGhost(container)).toBeNull();
    expect(screen.queryByText("Undated task")).not.toBeNull();
  });

  it("release back on the label button after dragging PATCHes nothing and does not open peek", async () => {
    const { patch, container } = renderPage();
    await waitForLane();
    const button = laneButton();
    const svg = timelineSvg(container);
    fireEvent.pointerDown(button, { button: 0, clientX: 100, clientY: 100 });
    fireEvent.pointerMove(svg, { clientX: 1120, clientY: 100 });
    fireEvent.pointerUp(button, { clientX: 100, clientY: 100 });
    // Click SYNCHRONOUSLY after pointer-up, as a real browser
    // does: suppressClick is cleared by a 0ms timer, so any await
    // between pointer-up and click would let the timer fire and
    // the click would legitimately open peek.
    fireEvent.click(button);
    expect(peekDialog()).toBeNull();
    await new Promise((r) => setTimeout(r, 50));
    expect(patch).not.toHaveBeenCalled();
  });

  it("pointer-cancel PATCHes nothing", async () => {
    const { patch, container } = renderPage();
    await waitForLane();
    const button = laneButton();
    const svg = timelineSvg(container);
    fireEvent.pointerDown(button, { button: 0, clientX: 100, clientY: 100 });
    fireEvent.pointerMove(svg, { clientX: 1120, clientY: 100 });
    fireEvent.pointerCancel(svg, { clientX: 1120, clientY: 100 });
    await new Promise((r) => setTimeout(r, 50));
    expect(patch).not.toHaveBeenCalled();
    expect(scheduleGhost(container)).toBeNull();
    expect(screen.queryByText("Undated task")).not.toBeNull();
  });

  it("Escape PATCHes nothing", async () => {
    const { patch, container } = renderPage();
    await waitForLane();
    const button = laneButton();
    const svg = timelineSvg(container);
    fireEvent.pointerDown(button, { button: 0, clientX: 100, clientY: 100 });
    fireEvent.pointerMove(svg, { clientX: 1120, clientY: 100 });
    fireEvent.keyDown(svg, { key: "Escape" });
    await new Promise((r) => setTimeout(r, 50));
    expect(patch).not.toHaveBeenCalled();
    expect(scheduleGhost(container)).toBeNull();
    expect(screen.queryByText("Undated task")).not.toBeNull();
  });

  it("plain click still opens the peek", async () => {
    const { patch } = renderPage();
    await waitForLane();
    const button = laneButton();
    // A real plain click: press and release on the button itself,
    // no movement — the release bubbles to the page handler, which
    // clears the (zero-delta) drag without suppressing the click.
    fireEvent.pointerDown(button, { button: 0, clientX: 100, clientY: 100 });
    fireEvent.pointerUp(button, { clientX: 100, clientY: 100 });
    fireEvent.click(button);
    await waitFor(() => {
      expect(peekDialog()).not.toBeNull();
    });
    expect(patch).not.toHaveBeenCalled();
  });

  it("canEdit=false renders no drag source (click still opens peek)", async () => {
    const { patch, container } = renderPage({ role: 0 });
    await waitForLane();
    const button = laneButton();
    const svg = timelineSvg(container);
    fireEvent.pointerDown(button, { button: 0, clientX: 100, clientY: 100 });
    fireEvent.pointerMove(svg, { clientX: 1120, clientY: 100 });
    // No drag started, so no schedule ghost ever appears.
    expect(scheduleGhost(container)).toBeNull();
    fireEvent.pointerUp(svg, { clientX: 1120, clientY: 100 });
    await new Promise((r) => setTimeout(r, 50));
    expect(patch).not.toHaveBeenCalled();
    fireEvent.click(button);
    await waitFor(() => {
      expect(peekDialog()).not.toBeNull();
    });
  });

  it("failed PATCH leaves the issue undated and surfaces the move-commit error", async () => {
    const { patch, container } = renderPage();
    patch.mockRejectedValueOnce(new Error("boom"));
    await waitForLane();
    dragTo(container, laneButton(), 1120);
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(`${BASE}/issues/i-u`, {
        start_date: "2026-11-10",
        target_date: "2026-11-10",
      });
    });
    // The move commit's error surface: the isError banner, not a
    // new toast — the schedule commit reuses patchMutation.
    await waitFor(() => {
      expect(
        screen.queryByText("Failed to reschedule — the timeline was refreshed."),
      ).not.toBeNull();
    });
    // The issue is still undated in the lane (no success refetch
    // moved it out).
    expect(screen.queryByText("Unscheduled")).not.toBeNull();
    expect(screen.queryByText("Undated task")).not.toBeNull();
  });

  it("drop past the right edge clamps to the last window day", async () => {
    const { patch, container } = renderPage();
    await waitForLane();
    dragTo(container, laneButton(), 10000);
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(`${BASE}/issues/i-u`, {
        start_date: "2026-12-31",
        target_date: "2026-12-31",
      });
    });
  });
});
