// @vitest-environment jsdom
// Drag-to-resize Gantt bars (C144T1): left-edge drag moves only
// start_date, right-edge drag moves only target_date, whole-day
// steps, min-1-day clamp in both directions, zero delta sends no
// PATCH, handles gated by canEdit and hidden in link mode (where a
// bar press still starts an edge draw), bar-body move unchanged,
// and resize in PQL mode PATCHes + refreshes. Harness follows
// GanttPql.test.tsx (mocked api, jsdom PointerEvent polyfill).

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

// Nov-2026-centered window is 2026-10-01 .. 2026-12-31.
const issueA = makeIssue(
  "i-a",
  "ENG-1",
  "Fix login redirect",
  "2026-11-10T00:00:00Z",
  "2026-11-12T00:00:00Z",
  1,
);
const issueB = makeIssue(
  "i-b",
  "ENG-2",
  "Urgent checkout bug",
  "2026-11-11T00:00:00Z",
  "2026-11-13T00:00:00Z",
  2,
);
const issueC = makeIssue("i-c", "ENG-3", "Undated research spike", null, null, 3);
// Window-clamp fixtures: D starts before the window, E ends after it.
const issueD = makeIssue(
  "i-d",
  "ENG-4",
  "Pre-window starter",
  "2026-09-15T00:00:00Z",
  "2026-11-12T00:00:00Z",
  1,
);
const issueE = makeIssue(
  "i-e",
  "ENG-5",
  "Post-window finisher",
  "2026-11-10T00:00:00Z",
  "2027-02-10T00:00:00Z",
  2,
);

function renderPage(opts: { role?: number; dated?: Issue[] } = {}) {
  const role = opts.role ?? 15;
  const dated = opts.dated ?? [issueA, issueB];
  const get = vi.spyOn(api, "get").mockImplementation((path: string) => {
    if (path === "/api/v1/workspaces")
      return Promise.resolve({
        workspaces: [{ slug: "acme", name: "Acme" }],
      });
    if (path === "/api/v1/workspaces/acme")
      return Promise.resolve({ role });
    if (path === `${BASE}/states`)
      return Promise.resolve({ states: [stateStarted] });
    if (path.includes("/issues")) {
      const qs = new URLSearchParams(path.split("?")[1] ?? "");
      if (qs.get("pql") !== null)
        return Promise.resolve({ results: [issueB], next_cursor: null });
      if (qs.get("undated") !== null)
        return Promise.resolve({ results: [issueC], next_cursor: null });
      return Promise.resolve({ results: dated, next_cursor: null });
    }
    return Promise.reject(new Error(`unexpected GET ${path}`));
  });
  const patch = vi.spyOn(api, "patch").mockImplementation(() =>
    Promise.resolve({ ...issueA }),
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

async function waitForBars(names: string[]) {
  await waitFor(() => {
    for (const n of names) expect(screen.queryByText(n)).not.toBeNull();
  });
}

function startHandles(container: HTMLElement) {
  return container.querySelectorAll(
    'rect[aria-label="Drag to change start date"]',
  );
}
function dueHandles(container: HTMLElement) {
  return container.querySelectorAll(
    'rect[aria-label="Drag to change due date"]',
  );
}
function bars(container: HTMLElement) {
  return container.querySelectorAll('rect[fill="#6366f1"]');
}
function dragHandle(handle: Element, dxPx: number) {
  const svg = handle.closest("svg")!;
  fireEvent.pointerDown(handle, { button: 0, clientX: 100, clientY: 100 });
  fireEvent.pointerMove(svg, { clientX: 100 + dxPx, clientY: 100 });
  fireEvent.pointerUp(svg, { clientX: 100 + dxPx, clientY: 100 });
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
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("Gantt bar resize (C144T1)", () => {
  it("renders a start and a due handle on every dated bar when editable", async () => {
    const { container } = renderPage();
    await waitForBars(["Fix login redirect", "Urgent checkout bug"]);
    expect(startHandles(container).length).toBe(2);
    expect(dueHandles(container).length).toBe(2);
    for (const h of [...startHandles(container), ...dueHandles(container)]) {
      expect(h.getAttribute("class")).toContain("cursor-ew-resize");
    }
  });

  it("left-edge drag PATCHes start only, target unchanged", async () => {
    const { patch, container } = renderPage();
    await waitForBars(["Fix login redirect", "Urgent checkout bug"]);
    // First start handle in DOM order is issueA's (rows sort by sequence).
    dragHandle(startHandles(container)[0], 28);
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(`${BASE}/issues/i-a`, {
        start_date: "2026-11-11",
        target_date: "2026-11-12",
      });
    });
  });

  it("right-edge drag PATCHes target only, start unchanged", async () => {
    const { patch, container } = renderPage();
    await waitForBars(["Fix login redirect", "Urgent checkout bug"]);
    dragHandle(dueHandles(container)[0], 28);
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(`${BASE}/issues/i-a`, {
        start_date: "2026-11-10",
        target_date: "2026-11-13",
      });
    });
  });

  it("start dragged past target clamps to target (min 1 day)", async () => {
    const { patch, container } = renderPage();
    await waitForBars(["Fix login redirect", "Urgent checkout bug"]);
    dragHandle(startHandles(container)[0], 5 * 28);
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(`${BASE}/issues/i-a`, {
        start_date: "2026-11-12",
        target_date: "2026-11-12",
      });
    });
  });

  it("target dragged before start clamps to start (min 1 day)", async () => {
    const { patch, container } = renderPage();
    await waitForBars(["Fix login redirect", "Urgent checkout bug"]);
    dragHandle(dueHandles(container)[0], -5 * 28);
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(`${BASE}/issues/i-a`, {
        start_date: "2026-11-10",
        target_date: "2026-11-10",
      });
    });
  });

  it("zero-delta resize sends no PATCH", async () => {
    const { patch, container } = renderPage();
    await waitForBars(["Fix login redirect", "Urgent checkout bug"]);
    dragHandle(startHandles(container)[0], 0);
    // Let any mutation settle, then assert silence.
    await new Promise((r) => setTimeout(r, 50));
    expect(patch).not.toHaveBeenCalled();
  });

  it("canEdit=false renders no handles and starts no drag", async () => {
    const { patch, container } = renderPage({ role: 0 });
    await waitForBars(["Fix login redirect", "Urgent checkout bug"]);
    expect(startHandles(container).length).toBe(0);
    expect(dueHandles(container).length).toBe(0);
    const bar = bars(container)[0];
    const svg = bar.closest("svg")!;
    fireEvent.pointerDown(bar, { button: 0, clientX: 100, clientY: 100 });
    fireEvent.pointerMove(svg, { clientX: 128, clientY: 100 });
    fireEvent.pointerUp(svg, { clientX: 128, clientY: 100 });
    await new Promise((r) => setTimeout(r, 50));
    expect(patch).not.toHaveBeenCalled();
  });

  it("link mode hides handles and a bar press still starts an edge draw", async () => {
    const { patch, container } = renderPage();
    await waitForBars(["Fix login redirect", "Urgent checkout bug"]);
    expect(startHandles(container).length).toBe(2);
    fireEvent.click(
      screen.getByRole("button", { name: "Add dependencies" }),
    );
    await waitFor(() => {
      expect(startHandles(container).length).toBe(0);
      expect(dueHandles(container).length).toBe(0);
    });
    const bar = bars(container)[0];
    fireEvent.pointerDown(bar, { button: 0, clientX: 100, clientY: 100 });
    // The in-progress draw preview (dashed path) proves the press
    // started an edge draw rather than a move/resize drag.
    await waitFor(() => {
      expect(
        container.querySelector('path[stroke-dasharray="4 3"]'),
      ).not.toBeNull();
    });
    expect(patch).not.toHaveBeenCalled();
  });

  it("bar-body move is unchanged (both dates shift)", async () => {
    const { patch, container } = renderPage();
    await waitForBars(["Fix login redirect", "Urgent checkout bug"]);
    const bar = bars(container)[0];
    const svg = bar.closest("svg")!;
    fireEvent.pointerDown(bar, { button: 0, clientX: 100, clientY: 100 });
    fireEvent.pointerMove(svg, { clientX: 128, clientY: 100 });
    fireEvent.pointerUp(svg, { clientX: 128, clientY: 100 });
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(`${BASE}/issues/i-a`, {
        start_date: "2026-11-11",
        target_date: "2026-11-13",
      });
    });
  });

  it("window-clamp rule: pre-window bar has no start handle, post-window bar has no due handle", async () => {
    const { patch, container } = renderPage({ dated: [issueD, issueE] });
    await waitForBars(["Pre-window starter", "Post-window finisher"]);
    // DOM order follows row order: D then E. D (starts before the
    // window) contributes only a due handle; E (ends after the
    // window) contributes only a start handle.
    expect(startHandles(container).length).toBe(1);
    expect(dueHandles(container).length).toBe(1);
    // The surviving start handle belongs to E: dragging it PATCHes
    // E's start only, proving the handle is E's, not D's.
    dragHandle(startHandles(container)[0], 28);
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(`${BASE}/issues/i-e`, {
        start_date: "2026-11-11",
        target_date: "2027-02-10",
      });
    });
  });

  it("resize in PQL mode PATCHes and refreshes the gantt queries", async () => {
    const { get, patch, container } = renderPage();
    await waitForBars([
      "Fix login redirect",
      "Urgent checkout bug",
      "Undated research spike",
    ]);
    await applyPql("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("Fix login redirect")).toBeNull();
      expect(screen.queryByText("Urgent checkout bug")).not.toBeNull();
    });
    const issueCallsBefore = get.mock.calls.filter((c) =>
      (c[0] as string).includes("/issues"),
    ).length;
    // In PQL mode only issueB's bar renders: its start handle is [0].
    dragHandle(startHandles(container)[0], 28);
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(`${BASE}/issues/i-b`, {
        start_date: "2026-11-12",
        target_date: "2026-11-13",
      });
    });
    // ["gantt"] invalidation refetches the window queries.
    await waitFor(() => {
      const now = get.mock.calls.filter((c) =>
        (c[0] as string).includes("/issues"),
      ).length;
      expect(now).toBeGreaterThan(issueCallsBefore);
    });
  });
});
