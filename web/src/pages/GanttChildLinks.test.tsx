// @vitest-environment jsdom
// C151T1: link-draw (dependency edge drawing) from child (sub-issue)
// Gantt bars. A child bar in link mode renders the draw handle and
// cursor-crosshair, its press starts an edge draw through the same
// onBarPointerDown the move path uses, the preview line resolves
// the child source through the unified endpoint lookup (top-level
// issue OR child row), and the create path is unchanged: a release
// over a parent row POSTs a blocks edge child→parent, a self-drop
// on the same child bar toasts and POSTs nothing, a drop on empty
// space cancels silently. Follows the GanttChildDrag harness
// (mocked api, jsdom PointerEvent polyfill, subtask rows in the
// fetch payload).
//
// Row layout for this fixture (HEADER_H 56, ROW_H 34, all issues
// in the "started" group): group header idx 0 (y 56), parent idx 1
// (y 90), child c1 idx 2 (y 124), child c2 idx 3 (y 158),
// dateless child idx 4 (y 192), dependent idx 5 (y 226). Drop
// targets therefore hit at clientY 100 (parent), 140 (c1),
// 170 (c2), 200 (dateless child), 240 (dependent); clientY 70
// lands on the group-header row (a visible row with no drop
// target — the only in-chart gap, since issue/child drop rows
// are contiguous and exact row boundaries resolve half-open to
// the row below, as the ganttLinks pure tests prove);
// clientY 1000 is below every row (empty space).
//
// C151T2 extends this suite: drop-onto-child creates
// (parent→child, child→child, onto a dateless child row) and
// child-touching arrows through the T1 unified lookup, plus
// edge-click delete confirm for a child-touching arrow.

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
const toastAdd = vi.hoisted(() => vi.fn());
vi.mock("../components/ui/toast", () => ({
  toast: { add: toastAdd },
}));
import Gantt from "./Gantt";
import { api } from "../lib/api";
import type { Issue, IssueLink, IssueState, SubtaskRow } from "../lib/types";
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
  subtasks?: SubtaskRow[],
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
    subtasks,
  };
}

// Children live ONLY in the parent's subtasks array — never as
// top-level results — so every child assertion below proves the
// subtask-row wiring, not top-level presence.
const childRanged: SubtaskRow = {
  id: "i-c1",
  display_id: "ENG-11",
  title: "Child task",
  state_id: "s-1",
  priority: 3,
  assignee_ids: [],
  sort_order: 1,
  start_date: "2026-11-15T00:00:00Z",
  target_date: "2026-11-17T00:00:00Z",
};
const childSecond: SubtaskRow = {
  id: "i-c2",
  display_id: "ENG-12",
  title: "Second child",
  state_id: "s-1",
  priority: 3,
  assignee_ids: [],
  sort_order: 2,
  start_date: "2026-11-19T00:00:00Z",
  target_date: "2026-11-21T00:00:00Z",
};
// Dateless child: no bar, so nothing to draw FROM — but its row
// is still a drop target (mirroring the undated-parent rule), and
// an edge touching it renders no arrow (no bar, no edge).
const childDateless: SubtaskRow = {
  id: "i-c0",
  display_id: "ENG-10",
  title: "Dateless child",
  state_id: "s-1",
  priority: 3,
  assignee_ids: [],
  // sort_order 3 keeps it after c1/c2 (rows nest sort_order ASC).
  sort_order: 3,
  start_date: null,
  target_date: null,
};
const parent = makeIssue(
  "i-p",
  "ENG-1",
  "Parent feature",
  "2026-11-10T00:00:00Z",
  "2026-11-12T00:00:00Z",
  1,
  [childRanged, childSecond, childDateless],
);
const dependent = makeIssue(
  "i-d",
  "ENG-4",
  "Dependent task",
  "2026-11-18T00:00:00Z",
  "2026-11-20T00:00:00Z",
  5,
);
// Full-Issue mirrors of the subtask rows, served ONLY on the peek
// detail path (PeekDrawer fetches one issue by id).
const byId: Record<string, Issue> = {
  "i-p": parent,
  "i-d": dependent,
  "i-c0": makeIssue("i-c0", "ENG-10", "Dateless child", null, null, 0),
  "i-c1": makeIssue(
    "i-c1",
    "ENG-11",
    "Child task",
    "2026-11-15T00:00:00Z",
    "2026-11-17T00:00:00Z",
    2,
  ),
  "i-c2": makeIssue(
    "i-c2",
    "ENG-12",
    "Second child",
    "2026-11-19T00:00:00Z",
    "2026-11-21T00:00:00Z",
    3,
  ),
};

function renderPage(
  opts: { role?: number; links?: IssueLink[] } = {},
) {
  const get = vi.spyOn(api, "get").mockImplementation((path: string) => {
    if (path === "/api/v1/workspaces")
      return Promise.resolve({
        workspaces: [{ slug: "acme", name: "Acme" }],
      });
    if (path === "/api/v1/workspaces/acme")
      return Promise.resolve({ role: opts.role ?? 15 });
    if (path === BASE)
      return Promise.resolve({ id: "p-eng", slug: "acme", identifier: "ENG" });
    if (path === `${BASE}/states`)
      return Promise.resolve({ states: [stateStarted] });
    // SavedViewsMenu (detail-mode menu) fetches the project's saved
    // views; an unmocked rejection would toast "Could not load
    // saved views" and pollute the no-toast assertions below.
    if (path.includes("/views")) return Promise.resolve([]);
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
        return Promise.resolve({ results: [], links: [], next_cursor: null });
      return Promise.resolve({
        results: [parent, dependent],
        links: opts.links ?? [],
        next_cursor: null,
      });
    }
    return Promise.reject(new Error(`unexpected GET ${path}`));
  });
  const patch = vi.spyOn(api, "patch").mockImplementation(() =>
    Promise.resolve({ ...parent }),
  );
  const del = vi.spyOn(api, "del").mockImplementation(() =>
    Promise.resolve({} as never),
  );
  const post = vi.spyOn(api, "post").mockImplementation(() =>
    Promise.resolve({
      id: "l-9",
      issue_id: "i-c1",
      target_issue_id: "i-p",
      kind: "blocks",
      created_at: "",
    }),
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
  return { get, patch, post, del, container };
}

async function waitForBars() {
  await waitFor(() => {
    for (const n of [
      "Parent feature",
      "Child task",
      "Second child",
      "Dependent task",
    ]) {
      expect(screen.queryByText(n)).not.toBeNull();
    }
  });
}

/** A bar rect by its exact <title> (ghosts carry a "(would shift
    to …)" suffix, so exact match never picks a ghost). */
function barByTitle(container: HTMLElement, title: string): Element {
  const bar = Array.from(
    container.querySelectorAll('rect[fill="#6366f1"]'),
  ).find((r) => r.querySelector("title")?.textContent === title);
  expect(bar).not.toBeUndefined();
  return bar!;
}

/** The <g> wrapping a bar rect and its sibling handles. */
function barGroup(bar: Element): Element {
  return bar.closest("g")!;
}

/** The link-mode draw handle circle in a bar's group, if any. */
function drawHandle(group: Element): Element | null {
  return group.querySelector("circle");
}

async function enterLinkMode(container: HTMLElement) {
  fireEvent.click(
    screen.getByRole("button", { name: "Add dependencies" }),
  );
  await waitFor(() => {
    expect(
      screen.getByRole("button", { name: "Linking…" }),
    ).not.toBeNull();
  });
  // Resize handles disappear in link mode (both parent and child).
  await waitFor(() => {
    expect(
      container.querySelectorAll(
        'rect[aria-label="Drag to change start date"]',
      ).length,
    ).toBe(0);
  });
}

function peekDialog() {
  return screen.queryByRole("dialog", { name: "Issue peek view" });
}

/** Start a draw from a bar: pointer-down on the bar, then a move
    over the svg so drawPos is set and the preview line renders. */
function startDraw(bar: Element, moveX = 300, moveY = 100) {
  const svg = bar.closest("svg")!;
  fireEvent.pointerDown(bar, { button: 0, clientX: 100, clientY: 100 });
  fireEvent.pointerMove(svg, { clientX: moveX, clientY: moveY });
  return svg;
}

function previewPath(container: HTMLElement): Element | null {
  return container.querySelector('path[stroke-dasharray="4 3"]');
}

beforeEach(() => {
  stubMatchMedia(false);
  localStorage.clear();
  toastAdd.mockReset();
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("Gantt child link-draw (C151T1)", () => {
  it("child bar renders a draw handle in link mode with the parent geometry", async () => {
    const { container } = renderPage();
    await waitForBars();
    const childGroup = barGroup(barByTitle(container, "ENG-11: Child task"));
    expect(drawHandle(childGroup)).toBeNull();
    await enterLinkMode(container);
    // Parent handle still renders, and the child handle matches it:
    // same fill-primary stroke-background circle, at the bar's
    // right edge (cx = bar x + bar width), mid-row y.
    const parentGroup = barGroup(
      barByTitle(container, "ENG-1: Parent feature"),
    );
    expect(drawHandle(parentGroup)).not.toBeNull();
    const childBar = barByTitle(container, "ENG-11: Child task");
    const handle = drawHandle(barGroup(childBar))!;
    expect(handle.getAttribute("class")).toContain("fill-primary");
    expect(handle.getAttribute("class")).toContain("stroke-background");
    expect(Number(handle.getAttribute("cx"))).toBe(
      Number(childBar.getAttribute("x")) +
        Number(childBar.getAttribute("width")),
    );
    // Child row idx 2 → y 124, mid-row 141.
    expect(Number(handle.getAttribute("cy"))).toBe(141);
  });

  it("child bar carries cursor-crosshair in link mode, cursor-grab outside it", async () => {
    const { container } = renderPage();
    await waitForBars();
    const bar = barByTitle(container, "ENG-11: Child task");
    expect(bar.getAttribute("class")).toContain("cursor-grab");
    expect(bar.getAttribute("class")).not.toContain("cursor-crosshair");
    await enterLinkMode(container);
    const linkBar = barByTitle(container, "ENG-11: Child task");
    expect(linkBar.getAttribute("class")).toContain("cursor-crosshair");
    expect(linkBar.getAttribute("class")).not.toContain("cursor-grab");
  });

  it("child bar press in link mode starts a draw from the child bar's right edge and no move drag", async () => {
    const { patch, post, container } = renderPage();
    await waitForBars();
    await enterLinkMode(container);
    const svg = startDraw(barByTitle(container, "ENG-11: Child task"));
    await waitFor(() => {
      expect(previewPath(container)).not.toBeNull();
    });
    // Preview starts at the child bar's right edge, mid-row y of
    // the CHILD row (141) — the old top-level-only preview rendered
    // nothing for a child source, and a parent source would sit at
    // the parent row's mid (107).
    const d = previewPath(container)!.getAttribute("d")!;
    const m = d.match(/^M (\d+) (\d+) L/);
    expect(m).not.toBeNull();
    expect(Number(m![2])).toBe(141);
    expect(patch).not.toHaveBeenCalled();
    // Release over empty space cancels: no POST, no PATCH, and
    // the preview disappears; a click afterwards still does not
    // open peek in link mode (existing !linkMode click guard).
    fireEvent.pointerUp(svg, { clientX: 300, clientY: 1000 });
    await waitFor(() => {
      expect(previewPath(container)).toBeNull();
    });
    await new Promise((r) => setTimeout(r, 50));
    expect(patch).not.toHaveBeenCalled();
    expect(post).not.toHaveBeenCalled();
    fireEvent.click(barByTitle(container, "ENG-11: Child task"));
    expect(peekDialog()).toBeNull();
  });

  it("release over a parent row POSTs a blocks edge child→parent", async () => {
    const { patch, post, container } = renderPage();
    await waitForBars();
    await enterLinkMode(container);
    const svg = startDraw(barByTitle(container, "ENG-11: Child task"));
    // Parent row y 90..124 → clientY 100 lands on the parent.
    fireEvent.pointerUp(svg, { clientX: 300, clientY: 100 });
    await waitFor(() => {
      expect(post).toHaveBeenCalledWith(`${BASE}/issues/i-c1/links`, {
        target_issue_id: "i-p",
        kind: "blocks",
      });
    });
    expect(patch).not.toHaveBeenCalled();
    // The draw is finished: the preview line is gone.
    expect(previewPath(container)).toBeNull();
  });

  it("self-drop on the same child bar toasts and POSTs nothing", async () => {
    const { post, container } = renderPage();
    await waitForBars();
    await enterLinkMode(container);
    const svg = startDraw(barByTitle(container, "ENG-11: Child task"));
    // Child c1 row y 124..158 → clientY 140 lands on itself.
    fireEvent.pointerUp(svg, { clientX: 300, clientY: 140 });
    await waitFor(() => {
      expect(toastAdd).toHaveBeenCalledWith(
        expect.objectContaining({
          title: "An issue can't depend on itself",
        }),
      );
    });
    await new Promise((r) => setTimeout(r, 50));
    expect(post).not.toHaveBeenCalled();
    expect(previewPath(container)).toBeNull();
  });

  it("drop on empty space cancels silently (no POST, no toast)", async () => {
    const { post, container } = renderPage();
    await waitForBars();
    await enterLinkMode(container);
    const svg = startDraw(barByTitle(container, "ENG-11: Child task"));
    fireEvent.pointerUp(svg, { clientX: 300, clientY: 1000 });
    await new Promise((r) => setTimeout(r, 50));
    expect(post).not.toHaveBeenCalled();
    expect(toastAdd).not.toHaveBeenCalled();
    expect(previewPath(container)).toBeNull();
  });

  it("canEdit=false child bars render no handle and start no draw", async () => {
    const { patch, post, container } = renderPage({ role: 0 });
    await waitForBars();
    // No link-mode toggle for read-only users, so link mode can
    // never arm; bars carry neither grab nor crosshair cursor.
    expect(
      screen.queryByRole("button", { name: "Add dependencies" }),
    ).toBeNull();
    const bar = barByTitle(container, "ENG-11: Child task");
    expect(drawHandle(barGroup(bar))).toBeNull();
    expect(bar.getAttribute("class")).not.toContain("cursor-grab");
    expect(bar.getAttribute("class")).not.toContain("cursor-crosshair");
    const svg = bar.closest("svg")!;
    fireEvent.pointerDown(bar, { button: 0, clientX: 100, clientY: 100 });
    fireEvent.pointerMove(svg, { clientX: 200, clientY: 100 });
    fireEvent.pointerUp(svg, { clientX: 200, clientY: 100 });
    await new Promise((r) => setTimeout(r, 50));
    expect(patch).not.toHaveBeenCalled();
    expect(post).not.toHaveBeenCalled();
    expect(previewPath(container)).toBeNull();
  });

  it("Esc cancels an in-progress child draw but stays in link mode", async () => {
    const { post, container } = renderPage();
    await waitForBars();
    await enterLinkMode(container);
    const svg = startDraw(barByTitle(container, "ENG-11: Child task"));
    await waitFor(() => {
      expect(previewPath(container)).not.toBeNull();
    });
    fireEvent.keyDown(svg, { key: "Escape" });
    await waitFor(() => {
      expect(previewPath(container)).toBeNull();
    });
    // Still in link mode: the toggle still reads "Linking…", and
    // a fresh press starts a new draw.
    expect(
      screen.getByRole("button", { name: "Linking…" }),
    ).not.toBeNull();
    startDraw(barByTitle(container, "ENG-11: Child task"));
    await waitFor(() => {
      expect(previewPath(container)).not.toBeNull();
    });
    expect(post).not.toHaveBeenCalled();
  });

  it("exiting link mode cancels an in-progress child draw", async () => {
    const { post, container } = renderPage();
    await waitForBars();
    await enterLinkMode(container);
    startDraw(barByTitle(container, "ENG-11: Child task"));
    await waitFor(() => {
      expect(previewPath(container)).not.toBeNull();
    });
    fireEvent.click(screen.getByRole("button", { name: "Linking…" }));
    await waitFor(() => {
      expect(previewPath(container)).toBeNull();
      expect(
        screen.getByRole("button", { name: "Add dependencies" }),
      ).not.toBeNull();
    });
    // Handles are gone with link mode.
    const childGroup = barGroup(barByTitle(container, "ENG-11: Child task"));
    expect(drawHandle(childGroup)).toBeNull();
    expect(post).not.toHaveBeenCalled();
  });
});

/** The fat invisible hit path of an arrow, if one renders. The
    visible path carries pointerEvents="none", so the hit path is
    the clickable edge; its <title> marks it as a dependency edge. */
function arrowHitPath(container: HTMLElement): Element | null {
  return (
    Array.from(container.querySelectorAll('path[stroke="transparent"]')).find(
      (p) => p.querySelector("title")?.textContent?.includes("click to remove"),
    ) ?? null
  );
}

/** The numbers of an arrow path's `d`, in order:
    [x1, y1, midX, y1, midX, y2, x2 - 2, y2]. */
function arrowNumbers(path: Element): number[] {
  return (path.getAttribute("d")!.match(/-?\d+/g) ?? []).map(Number);
}

describe("Gantt child link-drop (C151T2)", () => {
  it("draw from a parent released over a child row POSTs parent→child", async () => {
    const { patch, post, container } = renderPage();
    await waitForBars();
    await enterLinkMode(container);
    const svg = startDraw(barByTitle(container, "ENG-1: Parent feature"));
    // Child c1 row y 124..158 → clientY 140 lands on c1.
    fireEvent.pointerUp(svg, { clientX: 300, clientY: 140 });
    await waitFor(() => {
      expect(post).toHaveBeenCalledWith(`${BASE}/issues/i-p/links`, {
        target_issue_id: "i-c1",
        kind: "blocks",
      });
    });
    expect(patch).not.toHaveBeenCalled();
    expect(previewPath(container)).toBeNull();
  });

  it("draw from a child released over another child row POSTs child→child", async () => {
    const { patch, post, container } = renderPage();
    await waitForBars();
    await enterLinkMode(container);
    const svg = startDraw(barByTitle(container, "ENG-11: Child task"));
    // Child c2 row y 158..192 → clientY 170 lands on c2.
    fireEvent.pointerUp(svg, { clientX: 300, clientY: 170 });
    await waitFor(() => {
      expect(post).toHaveBeenCalledWith(`${BASE}/issues/i-c1/links`, {
        target_issue_id: "i-c2",
        kind: "blocks",
      });
    });
    expect(patch).not.toHaveBeenCalled();
    expect(previewPath(container)).toBeNull();
  });

  it("draw released over a dateless child row POSTs to that child", async () => {
    const { post, container } = renderPage();
    await waitForBars();
    await enterLinkMode(container);
    const svg = startDraw(barByTitle(container, "ENG-1: Parent feature"));
    // Dateless child row y 192..226 → clientY 200 lands on it:
    // drop targets include child rows regardless of the child's
    // own dates, mirroring the undated-parent rule.
    fireEvent.pointerUp(svg, { clientX: 300, clientY: 200 });
    await waitFor(() => {
      expect(post).toHaveBeenCalledWith(`${BASE}/issues/i-p/links`, {
        target_issue_id: "i-c0",
        kind: "blocks",
      });
    });
    expect(previewPath(container)).toBeNull();
  });

  it("pre-existing parent→child edge renders an arrow with parent geometry", async () => {
    const { container } = renderPage({
      links: [
        {
          id: "l-pc",
          issue_id: "i-p",
          target_issue_id: "i-c1",
          kind: "blocks",
          created_at: "",
        },
      ],
    });
    await waitForBars();
    const hit = arrowHitPath(container);
    expect(hit).not.toBeNull();
    const nums = arrowNumbers(hit!);
    const parentBar = barByTitle(container, "ENG-1: Parent feature");
    const childBar = barByTitle(container, "ENG-11: Child task");
    // Bar rects are inset 2px (x = dayToX + 2, width =
    // duration * DAY_W - 4), so the blocker arrow x1
    // (dayToX(end) + DAY_W) sits 2px past the bar's right edge,
    // and the path ends 2px before the blocked bar's left edge
    // (dayToX(start)), i.e. 4px before the rect's x. Ys are the
    // parent (107) and child (141) mid-rows.
    expect(nums[0]).toBe(
      Number(parentBar.getAttribute("x")) +
        Number(parentBar.getAttribute("width")) +
        2,
    );
    expect(nums[1]).toBe(107);
    expect(nums[6]).toBe(Number(childBar.getAttribute("x")) - 4);
    expect(nums[7]).toBe(141);
  });

  it("pre-existing child→parent edge renders an arrow with parent geometry", async () => {
    const { container } = renderPage({
      links: [
        {
          id: "l-cp",
          issue_id: "i-c1",
          target_issue_id: "i-p",
          kind: "blocks",
          created_at: "",
        },
      ],
    });
    await waitForBars();
    const hit = arrowHitPath(container);
    expect(hit).not.toBeNull();
    const nums = arrowNumbers(hit!);
    const childBar = barByTitle(container, "ENG-11: Child task");
    const parentBar = barByTitle(container, "ENG-1: Parent feature");
    expect(nums[0]).toBe(
      Number(childBar.getAttribute("x")) +
        Number(childBar.getAttribute("width")) +
        2,
    );
    expect(nums[1]).toBe(141);
    expect(nums[6]).toBe(Number(parentBar.getAttribute("x")) - 4);
    expect(nums[7]).toBe(107);
  });

  it("pre-existing child→child edge renders an arrow with parent geometry", async () => {
    const { container } = renderPage({
      links: [
        {
          id: "l-cc",
          issue_id: "i-c1",
          target_issue_id: "i-c2",
          kind: "blocks",
          created_at: "",
        },
      ],
    });
    await waitForBars();
    const hit = arrowHitPath(container);
    expect(hit).not.toBeNull();
    const nums = arrowNumbers(hit!);
    const fromBar = barByTitle(container, "ENG-11: Child task");
    const toBar = barByTitle(container, "ENG-12: Second child");
    // Same parent geometry, both endpoints child rows: c1
    // mid-row 141 → c2 mid-row 175 (row idx 3, y 158).
    expect(nums[0]).toBe(
      Number(fromBar.getAttribute("x")) +
        Number(fromBar.getAttribute("width")) +
        2,
    );
    expect(nums[1]).toBe(141);
    expect(nums[6]).toBe(Number(toBar.getAttribute("x")) - 4);
    expect(nums[7]).toBe(175);
  });

  it("clicking a child-touching arrow opens the delete confirm and Remove deletes it", async () => {
    const { del, container } = renderPage({
      links: [
        {
          id: "l-pc",
          issue_id: "i-p",
          target_issue_id: "i-c1",
          kind: "blocks",
          created_at: "",
        },
      ],
    });
    await waitForBars();
    const hit = arrowHitPath(container);
    expect(hit).not.toBeNull();
    fireEvent.click(hit!);
    await waitFor(() => {
      expect(
        screen.getByText("Remove this dependency?"),
      ).not.toBeNull();
    });
    fireEvent.click(
      screen.getByRole("button", { name: "Remove dependency" }),
    );
    await waitFor(() => {
      expect(del).toHaveBeenCalledWith(`${BASE}/issues/i-p/links/l-pc`);
    });
  });

  it("draw released over the group-header gap cancels with no POST and no toast", async () => {
    const { post, container } = renderPage();
    await waitForBars();
    await enterLinkMode(container);
    const svg = startDraw(barByTitle(container, "ENG-1: Parent feature"));
    // Group-header row y 56..90 is a visible row with no drop
    // target: clientY 70 hits no issue/child row, so the draw
    // cancels exactly like a release over empty space.
    fireEvent.pointerUp(svg, { clientX: 300, clientY: 70 });
    await new Promise((r) => setTimeout(r, 50));
    expect(post).not.toHaveBeenCalled();
    expect(toastAdd).not.toHaveBeenCalled();
    expect(previewPath(container)).toBeNull();
  });

  it("an edge touching a dateless child renders no arrow", async () => {
    const { container } = renderPage({
      links: [
        {
          id: "l-p0",
          issue_id: "i-p",
          target_issue_id: "i-c0",
          kind: "blocks",
          created_at: "",
        },
      ],
    });
    await waitForBars();
    // The edge is in the payload, but the dateless endpoint has
    // no bar, so no arrow renders — exactly as for dateless
    // parents.
    expect(arrowHitPath(container)).toBeNull();
  });

  it("canEdit=false: child-touching arrow renders but its click opens no confirm", async () => {
    const { del, container } = renderPage({
      role: 0,
      links: [
        {
          id: "l-pc",
          issue_id: "i-p",
          target_issue_id: "i-c1",
          kind: "blocks",
          created_at: "",
        },
      ],
    });
    await waitForBars();
    expect(
      screen.queryByRole("button", { name: "Add dependencies" }),
    ).toBeNull();
    const hit = arrowHitPath(container);
    expect(hit).not.toBeNull();
    fireEvent.click(hit!);
    await new Promise((r) => setTimeout(r, 50));
    expect(screen.queryByText("Remove this dependency?")).toBeNull();
    expect(del).not.toHaveBeenCalled();
  });
});
