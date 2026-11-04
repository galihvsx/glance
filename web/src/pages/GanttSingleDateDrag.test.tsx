// @vitest-environment jsdom
// Single-date Gantt drag correctness (C149T1): moving a start-only
// or target-only bar PATCHes only the date the issue originally
// had — the filled barSpan must never silently convert it into a
// ranged issue. Resize shape is unchanged and pinned here: a
// resize that moves an edge necessarily creates a range, so it
// PATCHes both dates; an inward resize on a 1-day bar is a no-op.
// C149T2 extends this suite (same harness): 1-day bars get enlarged
// outward-only transparent hit rects as their one logical handle
// per edge, multi-day bars are byte-identical to C144, and the
// move strip stays bar-body-only.
// Harness follows GanttBarResize.test.tsx (mocked api, jsdom
// PointerEvent polyfill, makeIssue with nullable dates).

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
import type { Issue, IssueLink, IssueState } from "../lib/types";
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

function makeLink(id: string, from: string, to: string): IssueLink {
  return {
    id,
    issue_id: from,
    target_issue_id: to,
    kind: "blocks",
    created_at: "",
  };
}

// Nov-2026-centered window is 2026-10-01 .. 2026-12-31. All three
// share one state group, so row order (and therefore bar/handle
// DOM order) follows sequence_id: start-only, target-only, ranged.
const startOnly = makeIssue(
  "i-s",
  "ENG-1",
  "Start-only task",
  "2026-11-10T00:00:00Z",
  null,
  1,
);
const targetOnly = makeIssue(
  "i-t",
  "ENG-2",
  "Target-only task",
  null,
  "2026-11-12T00:00:00Z",
  2,
);
const ranged = makeIssue(
  "i-r",
  "ENG-3",
  "Ranged task",
  "2026-11-10T00:00:00Z",
  "2026-11-12T00:00:00Z",
  3,
);
// Cascade fixture: the start-only blocker (11-10) blocks the ranged
// dependent (11-11..11-13). Moving the blocker +1 day fills it to
// 11-11, violating finish-to-start, so the dependent shifts +1.
const dependent = makeIssue(
  "i-d",
  "ENG-4",
  "Dependent task",
  "2026-11-11T00:00:00Z",
  "2026-11-13T00:00:00Z",
  2,
);

function renderPage(
  opts: { dated?: Issue[]; links?: IssueLink[]; role?: number } = {},
) {
  const dated = opts.dated ?? [startOnly, targetOnly, ranged];
  const links = opts.links ?? [];
  const get = vi.spyOn(api, "get").mockImplementation((path: string) => {
    if (path === "/api/v1/workspaces")
      return Promise.resolve({
        workspaces: [{ slug: "acme", name: "Acme" }],
      });
    if (path === "/api/v1/workspaces/acme")
      return Promise.resolve({ role: opts.role ?? 15 });
    if (path === `${BASE}/states`)
      return Promise.resolve({ states: [stateStarted] });
    if (path.includes("/issues")) {
      const qs = new URLSearchParams(path.split("?")[1] ?? "");
      if (qs.get("pql") !== null)
        return Promise.resolve({
          results: [startOnly],
          links,
          next_cursor: null,
        });
      if (qs.get("undated") !== null)
        return Promise.resolve({ results: [], links: [], next_cursor: null });
      return Promise.resolve({ results: dated, links, next_cursor: null });
    }
    return Promise.reject(new Error(`unexpected GET ${path}`));
  });
  const patch = vi.spyOn(api, "patch").mockImplementation(() =>
    Promise.resolve({ ...startOnly }),
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

function bars(container: HTMLElement) {
  return container.querySelectorAll('rect[fill="#6366f1"]');
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
function hitRects(container: HTMLElement) {
  return container.querySelectorAll("rect[data-gantt-hit]");
}
/** Visible 6px handle rects (white, 0.4 opacity) — decorations on
    1-day bars, the logical handles on multi-day bars. */
function visibleHandles(container: HTMLElement) {
  return Array.from(container.querySelectorAll('rect[fill="#ffffff"]')).filter(
    (r) => r.getAttribute("width") === "6",
  );
}
/** Drag an element one whole day (28px) right and release. */
function dragRight(el: Element, dxPx = 28) {
  const svg = el.closest("svg")!;
  fireEvent.pointerDown(el, { button: 0, clientX: 100, clientY: 100 });
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

describe("Gantt single-date drag (C149T1)", () => {
  it("start-only move PATCHes start_date only (body has NO target_date key)", async () => {
    const { patch, container } = renderPage();
    await waitForBars(["Start-only task", "Target-only task", "Ranged task"]);
    // bars()[0] is the start-only bar (sequence order).
    dragRight(bars(container)[0]);
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(`${BASE}/issues/i-s`, {
        start_date: "2026-11-11",
      });
    });
    const call = patch.mock.calls.find(
      (c) => c[0] === `${BASE}/issues/i-s`,
    )!;
    expect(Object.keys(call[1] as object).sort()).toEqual(["start_date"]);
  });

  it("target-only move PATCHes target_date only (body has NO start_date key)", async () => {
    const { patch, container } = renderPage();
    await waitForBars(["Start-only task", "Target-only task", "Ranged task"]);
    // bars()[1] is the target-only bar (sequence order).
    dragRight(bars(container)[1]);
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(`${BASE}/issues/i-t`, {
        target_date: "2026-11-13",
      });
    });
    const call = patch.mock.calls.find(
      (c) => c[0] === `${BASE}/issues/i-t`,
    )!;
    expect(Object.keys(call[1] as object).sort()).toEqual(["target_date"]);
  });

  it("ranged move PATCHes both dates (unchanged)", async () => {
    const { patch, container } = renderPage();
    await waitForBars(["Start-only task", "Target-only task", "Ranged task"]);
    // bars()[2] is the ranged bar (sequence order).
    dragRight(bars(container)[2]);
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(`${BASE}/issues/i-r`, {
        start_date: "2026-11-11",
        target_date: "2026-11-13",
      });
    });
  });

  it("zero-delta move sends no PATCH", async () => {
    const { patch, container } = renderPage();
    await waitForBars(["Start-only task", "Target-only task", "Ranged task"]);
    dragRight(bars(container)[0], 0);
    await new Promise((r) => setTimeout(r, 50));
    expect(patch).not.toHaveBeenCalled();
  });

  it("single-date move in PQL mode PATCHes start only and refreshes the gantt queries", async () => {
    const { get, patch, container } = renderPage();
    await waitForBars(["Start-only task", "Target-only task", "Ranged task"]);
    await applyPql("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("Start-only task")).not.toBeNull();
      expect(screen.queryByText("Target-only task")).toBeNull();
      expect(screen.queryByText("Ranged task")).toBeNull();
    });
    const issueCallsBefore = get.mock.calls.filter((c) =>
      (c[0] as string).includes("/issues"),
    ).length;
    // In PQL mode only the start-only bar renders: bars()[0] is it.
    dragRight(bars(container)[0]);
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(`${BASE}/issues/i-s`, {
        start_date: "2026-11-11",
      });
    });
    const call = patch.mock.calls.find(
      (c) => c[0] === `${BASE}/issues/i-s`,
    )!;
    expect(Object.keys(call[1] as object).sort()).toEqual(["start_date"]);
    // ["gantt"] invalidation refetches the window queries.
    await waitFor(() => {
      const now = get.mock.calls.filter((c) =>
        (c[0] as string).includes("/issues"),
      ).length;
      expect(now).toBeGreaterThan(issueCallsBefore);
    });
  });

  it("moved single-date blocker still cascades, and its own PATCH stays start-only", async () => {
    const { patch, container } = renderPage({
      dated: [startOnly, dependent],
      links: [makeLink("l-1", "i-s", "i-d")],
    });
    await waitForBars(["Start-only task", "Dependent task"]);
    dragRight(bars(container)[0]);
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(`${BASE}/issues/i-s`, {
        start_date: "2026-11-11",
      });
      expect(patch).toHaveBeenCalledWith(`${BASE}/issues/i-d`, {
        start_date: "2026-11-12",
        target_date: "2026-11-14",
      });
    });
    const call = patch.mock.calls.find(
      (c) => c[0] === `${BASE}/issues/i-s`,
    )!;
    expect(Object.keys(call[1] as object).sort()).toEqual(["start_date"]);
  });

  it("resize-end on a start-only bar PATCHes both dates (gesture creates the target)", async () => {
    const { patch, container } = renderPage();
    await waitForBars(["Start-only task", "Target-only task", "Ranged task"]);
    // dueHandles()[0] is the start-only bar's due handle: its
    // filled span is 11-10..11-10, so +1 day ends at 11-11.
    dragRight(dueHandles(container)[0]);
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(`${BASE}/issues/i-s`, {
        start_date: "2026-11-10",
        target_date: "2026-11-11",
      });
    });
  });

  it("resize-start on a target-only bar PATCHes both dates (gesture creates the start)", async () => {
    const { patch, container } = renderPage();
    await waitForBars(["Start-only task", "Target-only task", "Ranged task"]);
    // startHandles()[1] is the target-only bar's start handle: its
    // filled span is 11-12..11-12, so -1 day starts at 11-11.
    dragRight(startHandles(container)[1], -28);
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(`${BASE}/issues/i-t`, {
        start_date: "2026-11-11",
        target_date: "2026-11-12",
      });
    });
  });

  it("inward resize on a 1-day bar is a no-op", async () => {
    const { patch, container } = renderPage();
    await waitForBars(["Start-only task", "Target-only task", "Ranged task"]);
    // Dragging the start-only bar's due handle left clamps the end
    // back to the start (11-10..11-10), i.e. the original span.
    dragRight(dueHandles(container)[0], -28);
    await new Promise((r) => setTimeout(r, 50));
    expect(patch).not.toHaveBeenCalled();
  });
});

describe("Gantt single-date handles (C149T2)", () => {
  it("1-day bar renders exactly one logical handle per edge, on enlarged outward hit rects", async () => {
    const { container } = renderPage({ dated: [startOnly] });
    await waitForBars(["Start-only task"]);
    // One logical handle per edge: the hit rect carries the
    // aria-label/title, the visible 6px rect is a decoration.
    expect(startHandles(container).length).toBe(1);
    expect(dueHandles(container).length).toBe(1);
    expect(hitRects(container).length).toBe(2);
    expect(visibleHandles(container).length).toBe(2);
    const bar = bars(container)[0];
    const barX = Number(bar.getAttribute("x"));
    const barW = Number(bar.getAttribute("width"));
    expect(barW).toBe(24);
    const startHit = startHandles(container)[0];
    const dueHit = dueHandles(container)[0];
    expect(startHit.getAttribute("data-gantt-hit")).toBe("resize-start");
    expect(dueHit.getAttribute("data-gantt-hit")).toBe("resize-end");
    expect(startHit.getAttribute("fill")).toBe("transparent");
    expect(dueHit.getAttribute("fill")).toBe("transparent");
    expect(startHit.querySelector("title")?.textContent).toBe(
      "Drag to change start date",
    );
    expect(dueHit.querySelector("title")?.textContent).toBe(
      "Drag to change due date",
    );
    // Outward-only geometry, asserted relative to the bar rect so
    // the test does not hardcode the window offset: left hit covers
    // the visible handle plus 6px outside the left edge, right hit
    // the visible handle plus 6px outside the right edge.
    expect(Number(startHit.getAttribute("x"))).toBe(barX - 6);
    expect(Number(startHit.getAttribute("width"))).toBe(12);
    expect(Number(dueHit.getAttribute("x"))).toBe(barX + barW - 6);
    expect(Number(dueHit.getAttribute("width"))).toBe(12);
    // Neither hit rect reaches into the middle 12px move strip
    // (barX+6 .. barX+barW-6): left ends where the strip starts,
    // right starts where the strip ends.
    expect(
      Number(startHit.getAttribute("x")) +
        Number(startHit.getAttribute("width")),
    ).toBe(barX + 6);
    expect(Number(dueHit.getAttribute("x"))).toBe(barX + barW - 6);
    // Visible handles stay exactly 6px at the bar edges.
    const vis = visibleHandles(container);
    expect(Number(vis[0].getAttribute("x"))).toBe(barX);
    expect(Number(vis[1].getAttribute("x"))).toBe(barX + barW - 6);
  });

  it("press on the start hit rect starts resize-start and PATCHes both dates (C144 shape)", async () => {
    const { patch, container } = renderPage({ dated: [startOnly] });
    await waitForBars(["Start-only task"]);
    // The only start-labelled element is the hit rect; dragging it
    // one day left moves the start to 11-09, creating a range.
    dragRight(startHandles(container)[0], -28);
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(`${BASE}/issues/i-s`, {
        start_date: "2026-11-09",
        target_date: "2026-11-10",
      });
    });
  });

  it("press on the due hit rect starts resize-end and PATCHes both dates (C144 shape)", async () => {
    const { patch, container } = renderPage({ dated: [startOnly] });
    await waitForBars(["Start-only task"]);
    dragRight(dueHandles(container)[0]);
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(`${BASE}/issues/i-s`, {
        start_date: "2026-11-10",
        target_date: "2026-11-11",
      });
    });
  });

  it("bar-body press on a 1-day bar still starts a move, not a resize", async () => {
    const { patch, container } = renderPage({ dated: [startOnly] });
    await waitForBars(["Start-only task"]);
    dragRight(bars(container)[0]);
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(`${BASE}/issues/i-s`, {
        start_date: "2026-11-11",
      });
    });
    const call = patch.mock.calls.find(
      (c) => c[0] === `${BASE}/issues/i-s`,
    )!;
    expect(Object.keys(call[1] as object).sort()).toEqual(["start_date"]);
  });

  it("multi-day bars render no hit rects; visible handles only, C144 geometry byte-identical", async () => {
    const { container } = renderPage({ dated: [ranged] });
    await waitForBars(["Ranged task"]);
    expect(hitRects(container).length).toBe(0);
    expect(startHandles(container).length).toBe(1);
    expect(dueHandles(container).length).toBe(1);
    const bar = bars(container)[0];
    const barX = Number(bar.getAttribute("x"));
    const barW = Number(bar.getAttribute("width"));
    // 3-day bar: 3*28-4 = 80.
    expect(barW).toBe(80);
    const startHandle = startHandles(container)[0];
    const dueHandle = dueHandles(container)[0];
    expect(startHandle.getAttribute("data-gantt-hit")).toBeNull();
    expect(dueHandle.getAttribute("data-gantt-hit")).toBeNull();
    expect(Number(startHandle.getAttribute("x"))).toBe(barX);
    expect(Number(startHandle.getAttribute("width"))).toBe(6);
    expect(Number(dueHandle.getAttribute("x"))).toBe(barX + barW - 6);
    expect(Number(dueHandle.getAttribute("width"))).toBe(6);
    expect(visibleHandles(container).length).toBe(2);
  });

  it("canEdit=false renders neither handles nor hit rects on a 1-day bar", async () => {
    const { patch, container } = renderPage({ dated: [startOnly], role: 0 });
    await waitForBars(["Start-only task"]);
    expect(startHandles(container).length).toBe(0);
    expect(dueHandles(container).length).toBe(0);
    expect(hitRects(container).length).toBe(0);
    dragRight(bars(container)[0]);
    await new Promise((r) => setTimeout(r, 50));
    expect(patch).not.toHaveBeenCalled();
  });

  it("link mode renders neither handles nor hit rects on a 1-day bar", async () => {
    const { container } = renderPage({ dated: [startOnly] });
    await waitForBars(["Start-only task"]);
    expect(startHandles(container).length).toBe(1);
    expect(hitRects(container).length).toBe(2);
    fireEvent.click(
      screen.getByRole("button", { name: "Add dependencies" }),
    );
    await waitFor(() => {
      expect(startHandles(container).length).toBe(0);
      expect(dueHandles(container).length).toBe(0);
      expect(hitRects(container).length).toBe(0);
    });
  });
});
