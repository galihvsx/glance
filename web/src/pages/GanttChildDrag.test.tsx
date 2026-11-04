// @vitest-environment jsdom
// C150T1: move drag on nested child (sub-issue) Gantt bars. A child
// bar-body press starts a move drag exactly like a parent bar — same
// whole-day delta, same live preview, same commit path, C149
// single-date presence rule from the child's own stored dates —
// plus cascade source wiring: children exist ONLY as subtask rows
// in this fixture (never as top-level results), so a child-source
// cascade or a child dependent proves the flattened cascade input
// and the rule-7 child presence lookup, not top-level presence.
// Follows the GanttSingleDateDrag + GanttSubWorkItems harnesses
// (mocked api, jsdom PointerEvent polyfill).

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

function makeLink(id: string, from: string, to: string): IssueLink {
  return {
    id,
    issue_id: from,
    target_issue_id: to,
    kind: "blocks",
    created_at: "",
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
const childStartOnly: SubtaskRow = {
  id: "i-c2",
  display_id: "ENG-12",
  title: "Start-only child",
  state_id: "s-1",
  priority: 3,
  assignee_ids: [],
  sort_order: 2,
  start_date: "2026-11-19T00:00:00Z",
};
const childTargetOnly: SubtaskRow = {
  id: "i-c3",
  display_id: "ENG-13",
  title: "Target-only child",
  state_id: "s-1",
  priority: 3,
  assignee_ids: [],
  sort_order: 3,
  target_date: "2026-11-21T00:00:00Z",
};
const childSingleDependent: SubtaskRow = {
  id: "i-c4",
  display_id: "ENG-14",
  title: "Single-date dependent",
  state_id: "s-1",
  priority: 3,
  assignee_ids: [],
  sort_order: 4,
  start_date: "2026-11-18T00:00:00Z",
};
const parent = makeIssue(
  "i-p",
  "ENG-1",
  "Parent feature",
  "2026-11-10T00:00:00Z",
  "2026-11-12T00:00:00Z",
  1,
  [childRanged, childStartOnly, childTargetOnly, childSingleDependent],
);
// Top-level dependent for the child-source cascade test: Nov 18–20.
// After the child blocker (Nov 15–17) moves +1 day (end 18), the
// dependent's start (18) violates finish-to-start (18 < 18+1),
// so it shifts +1 to Nov 19–21.
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
    "Start-only child",
    "2026-11-19T00:00:00Z",
    null,
    3,
  ),
  "i-c3": makeIssue(
    "i-c3",
    "ENG-13",
    "Target-only child",
    null,
    "2026-11-21T00:00:00Z",
    4,
  ),
  "i-c4": makeIssue(
    "i-c4",
    "ENG-14",
    "Single-date dependent",
    "2026-11-18T00:00:00Z",
    null,
    5,
  ),
};

function renderPage(
  opts: { links?: IssueLink[]; role?: number } = {},
) {
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
        links,
        next_cursor: null,
      });
    }
    return Promise.reject(new Error(`unexpected GET ${path}`));
  });
  const patch = vi.spyOn(api, "patch").mockImplementation(() =>
    Promise.resolve({ ...parent }),
  );
  const post = vi.spyOn(api, "post").mockImplementation(() =>
    Promise.resolve({ id: "l-9", kind: "blocks" }),
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

async function waitForBars() {
  await waitFor(() => {
    for (const n of [
      "Parent feature",
      "Child task",
      "Start-only child",
      "Target-only child",
      "Single-date dependent",
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

function ghosts(container: HTMLElement) {
  return container.querySelectorAll('rect[data-cascade-ghost="true"]');
}

/** Drag an element dxPx pixels right (28px = one day) and release. */
function drag(el: Element, dxPx = 28) {
  const svg = el.closest("svg")!;
  fireEvent.pointerDown(el, { button: 0, clientX: 100, clientY: 100 });
  fireEvent.pointerMove(svg, { clientX: 100 + dxPx, clientY: 100 });
  fireEvent.pointerUp(svg, { clientX: 100 + dxPx, clientY: 100 });
}

function peekDialog() {
  return screen.queryByRole("dialog", { name: "Issue peek view" });
}

beforeEach(() => {
  stubMatchMedia(false);
  localStorage.clear();
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("Gantt child move drag (C150T1)", () => {
  it("child move PATCHes the child id with shifted dates", async () => {
    const { patch, container } = renderPage();
    await waitForBars();
    drag(barByTitle(container, "ENG-11: Child task"));
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(`${BASE}/issues/i-c1`, {
        start_date: "2026-11-16",
        target_date: "2026-11-18",
      });
    });
    // The parent is untouched by its child's drag.
    expect(patch).not.toHaveBeenCalledWith(
      `${BASE}/issues/i-p`,
      expect.anything(),
    );
  });

  it("start-only child move PATCHes start_date only", async () => {
    const { patch, container } = renderPage();
    await waitForBars();
    drag(barByTitle(container, "ENG-12: Start-only child"));
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(`${BASE}/issues/i-c2`, {
        start_date: "2026-11-20",
      });
    });
    const call = patch.mock.calls.find(
      (c) => c[0] === `${BASE}/issues/i-c2`,
    )!;
    expect(Object.keys(call[1] as object).sort()).toEqual(["start_date"]);
  });

  it("target-only child move PATCHes target_date only (dragged left)", async () => {
    const { patch, container } = renderPage();
    await waitForBars();
    drag(barByTitle(container, "ENG-13: Target-only child"), -28);
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(`${BASE}/issues/i-c3`, {
        target_date: "2026-11-20",
      });
    });
    const call = patch.mock.calls.find(
      (c) => c[0] === `${BASE}/issues/i-c3`,
    )!;
    expect(Object.keys(call[1] as object).sort()).toEqual(["target_date"]);
  });

  it("zero-delta child press sends no PATCH and still opens peek", async () => {
    const { patch, container } = renderPage();
    await waitForBars();
    const bar = barByTitle(container, "ENG-11: Child task");
    const svg = bar.closest("svg")!;
    fireEvent.pointerDown(bar, { button: 0, clientX: 100, clientY: 100 });
    fireEvent.pointerUp(svg, { clientX: 100, clientY: 100 });
    fireEvent.click(barByTitle(container, "ENG-11: Child task"));
    await waitFor(() => {
      expect(peekDialog()).not.toBeNull();
    });
    expect(patch).not.toHaveBeenCalled();
  });

  it("non-zero child drag does NOT open peek", async () => {
    const { patch, container } = renderPage();
    await waitForBars();
    drag(barByTitle(container, "ENG-11: Child task"));
    // Click SYNCHRONOUSLY after pointer-up, as a real browser
    // does: suppressClick is cleared by a 0ms timer, so any await
    // between pointer-up and click would let the timer fire and
    // the click would legitimately open peek. Re-query so the
    // click lands on the attached node.
    fireEvent.click(barByTitle(container, "ENG-11: Child task"));
    expect(peekDialog()).toBeNull();
    // The drag itself was real (this assertion may await freely).
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(
        `${BASE}/issues/i-c1`,
        expect.anything(),
      );
    });
  });

  it("canEdit=false starts no drag on a child bar", async () => {
    const { patch, container } = renderPage({ role: 0 });
    await waitForBars();
    drag(barByTitle(container, "ENG-11: Child task"));
    await new Promise((r) => setTimeout(r, 50));
    expect(patch).not.toHaveBeenCalled();
  });

  it("child drag with a blocks edge cascades a dependent and previews its ghost", async () => {
    const { patch, container } = renderPage({
      links: [makeLink("l-1", "i-c1", "i-d")],
    });
    await waitForBars();
    const bar = barByTitle(container, "ENG-11: Child task");
    const svg = bar.closest("svg")!;
    fireEvent.pointerDown(bar, { button: 0, clientX: 100, clientY: 100 });
    fireEvent.pointerMove(svg, { clientX: 128, clientY: 100 });
    // Ghost preview during the drag, before any PATCH.
    await waitFor(() => {
      expect(ghosts(container).length).toBe(1);
    });
    expect(ghosts(container)[0].getAttribute("aria-label")).toBe(
      "Cascade preview for ENG-4",
    );
    expect(patch).not.toHaveBeenCalled();
    fireEvent.pointerUp(svg, { clientX: 128, clientY: 100 });
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(`${BASE}/issues/i-c1`, {
        start_date: "2026-11-16",
        target_date: "2026-11-18",
      });
      expect(patch).toHaveBeenCalledWith(`${BASE}/issues/i-d`, {
        start_date: "2026-11-19",
        target_date: "2026-11-21",
      });
    });
  });

  it("moved child blocker with a single-date dependent keeps the dependent single-date", async () => {
    const { patch, container } = renderPage({
      links: [makeLink("l-1", "i-c1", "i-c4")],
    });
    await waitForBars();
    drag(barByTitle(container, "ENG-11: Child task"));
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(`${BASE}/issues/i-c1`, {
        start_date: "2026-11-16",
        target_date: "2026-11-18",
      });
      // The dependent exists only as a subtask row: without the
      // C150T1 rule-7 child lookup this PATCH would be dropped.
      expect(patch).toHaveBeenCalledWith(`${BASE}/issues/i-c4`, {
        start_date: "2026-11-19",
      });
    });
    const call = patch.mock.calls.find(
      (c) => c[0] === `${BASE}/issues/i-c4`,
    )!;
    expect(Object.keys(call[1] as object).sort()).toEqual(["start_date"]);
  });

  it("link mode: a child-bar press starts no edge draw", async () => {
    const { patch, post, container } = renderPage();
    await waitForBars();
    fireEvent.click(
      screen.getByRole("button", { name: "Add dependencies" }),
    );
    // Link mode entered: the parent bars' edge handles disappear.
    await waitFor(() => {
      expect(
        container.querySelectorAll(
          'rect[aria-label="Drag to change start date"]',
        ).length,
      ).toBe(0);
    });
    drag(barByTitle(container, "ENG-11: Child task"));
    await new Promise((r) => setTimeout(r, 50));
    expect(patch).not.toHaveBeenCalled();
    expect(post).not.toHaveBeenCalled();
    expect(peekDialog()).toBeNull();
  });
});
