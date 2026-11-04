// @vitest-environment jsdom
// Dependency cascade wiring (C146T2): Gantt drag commit calls the
// cascade engine and PATCHes shifts with presence-preserving
// payloads; an empty cascade keeps today's single PATCH; a partial
// failure invalidates + toasts an error; ghost bars render at
// shifted positions during the drag (top-level and nested child
// bars alike). Harness follows GanttBarResize.test.tsx (mocked api,
// jsdom PointerEvent polyfill); the engine math itself is covered
// by ganttCascade.test.ts, so these tests cover only the wiring.
//
// Window assumption (same as the resize suite): the run month is
// November 2026, so the Gantt window is 2026-10-01 .. 2026-12-31
// and dayToX("2026-11-14") = 44 * 28.

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

function makeLink(id: string, from: string, to: string): IssueLink {
  return {
    id,
    issue_id: from,
    target_issue_id: to,
    kind: "blocks",
    created_at: "",
  };
}

// A blocks B blocks C, each step a finish-to-start violation once A
// moves +1 day (A 10–12 → 11–13 pushes B 13–15 → 14–16, which pushes
// C 16–18 → 17–19). D is unlinked and far away: never shifted.
const issueA = makeIssue(
  "i-a",
  "ENG-1",
  "Blocker task",
  "2026-11-10T00:00:00Z",
  "2026-11-12T00:00:00Z",
  1,
);
const issueB = makeIssue(
  "i-b",
  "ENG-2",
  "Dependent task",
  "2026-11-13T00:00:00Z",
  "2026-11-15T00:00:00Z",
  2,
);
const issueC = makeIssue(
  "i-c",
  "ENG-3",
  "Transitive task",
  "2026-11-16T00:00:00Z",
  "2026-11-18T00:00:00Z",
  3,
);
const issueD = makeIssue(
  "i-d",
  "ENG-4",
  "Far task",
  "2026-11-20T00:00:00Z",
  "2026-11-22T00:00:00Z",
  4,
);
const chainLinks = [
  makeLink("l-1", "i-a", "i-b"),
  makeLink("l-2", "i-b", "i-c"),
];

function renderPage(opts: { dated?: Issue[]; links?: IssueLink[] } = {}) {
  const dated = opts.dated ?? [issueA, issueB, issueC, issueD];
  const links = opts.links ?? chainLinks;
  const get = vi.spyOn(api, "get").mockImplementation((path: string) => {
    if (path === "/api/v1/workspaces")
      return Promise.resolve({
        workspaces: [{ slug: "acme", name: "Acme" }],
      });
    if (path === "/api/v1/workspaces/acme")
      return Promise.resolve({ role: 15 });
    if (path === `${BASE}/states`)
      return Promise.resolve({ states: [stateStarted] });
    if (path.includes("/issues")) {
      const qs = new URLSearchParams(path.split("?")[1] ?? "");
      if (qs.get("undated") !== null)
        return Promise.resolve({ results: [], links: [], next_cursor: null });
      return Promise.resolve({ results: dated, links, next_cursor: null });
    }
    return Promise.reject(new Error(`unexpected GET ${path}`));
  });
  const patch = vi.spyOn(api, "patch").mockImplementation(() =>
    Promise.resolve({ ...issueA }),
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

async function waitForBars(names: string[]) {
  await waitFor(() => {
    for (const n of names) expect(screen.queryByText(n)).not.toBeNull();
  });
}

function bars(container: HTMLElement) {
  return container.querySelectorAll('rect[fill="#6366f1"]');
}
function ghosts(container: HTMLElement) {
  return container.querySelectorAll('rect[data-cascade-ghost="true"]');
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
/** Drag an element 28px right (one whole day) and release. */
function dragRight(el: Element, dxPx = 28) {
  const svg = el.closest("svg")!;
  fireEvent.pointerDown(el, { button: 0, clientX: 100, clientY: 100 });
  fireEvent.pointerMove(svg, { clientX: 100 + dxPx, clientY: 100 });
  fireEvent.pointerUp(svg, { clientX: 100 + dxPx, clientY: 100 });
}

beforeEach(() => {
  stubMatchMedia(false);
});

afterEach(() => {
  cleanup();
  toastAdd.mockReset();
  vi.restoreAllMocks();
});

describe("Gantt cascade wiring (C146T2)", () => {
  it("move commit PATCHes the dragged issue plus every shift, then toasts the count", async () => {
    const { patch, container } = renderPage();
    await waitForBars([
      "Blocker task",
      "Dependent task",
      "Transitive task",
      "Far task",
    ]);
    dragRight(bars(container)[0]);
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(`${BASE}/issues/i-a`, {
        start_date: "2026-11-11",
        target_date: "2026-11-13",
      });
      expect(patch).toHaveBeenCalledWith(`${BASE}/issues/i-b`, {
        start_date: "2026-11-14",
        target_date: "2026-11-16",
      });
      expect(patch).toHaveBeenCalledWith(`${BASE}/issues/i-c`, {
        start_date: "2026-11-17",
        target_date: "2026-11-19",
      });
    });
    // The far, unlinked issue is never patched.
    expect(patch).not.toHaveBeenCalledWith(
      `${BASE}/issues/i-d`,
      expect.anything(),
    );
    expect(patch).toHaveBeenCalledTimes(3);
    await waitFor(() => {
      expect(
        toastAdd.mock.calls.some(
          (c) =>
            c[0]?.title === "Shifted 2 dependent issues" &&
            c[0]?.type === "success",
        ),
      ).toBe(true);
    });
  });

  it("single-date dependent stays single-date: its PATCH carries start_date only", async () => {
    const singleB = makeIssue(
      "i-b",
      "ENG-2",
      "Dependent task",
      "2026-11-13T00:00:00Z",
      null,
      2,
    );
    const { patch, container } = renderPage({
      dated: [issueA, singleB],
      links: [makeLink("l-1", "i-a", "i-b")],
    });
    await waitForBars(["Blocker task", "Dependent task"]);
    dragRight(bars(container)[0]);
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(`${BASE}/issues/i-b`, {
        start_date: "2026-11-14",
      });
    });
    // Exactly one key: a target_date here would break single-date shape.
    const bCall = patch.mock.calls.find(
      (c) => c[0] === `${BASE}/issues/i-b`,
    )!;
    expect(Object.keys(bCall[1] as object).sort()).toEqual(["start_date"]);
  });

  it("empty cascade keeps today's single PATCH and sends no toast", async () => {
    const farB = makeIssue(
      "i-b",
      "ENG-2",
      "Dependent task",
      "2026-11-20T00:00:00Z",
      "2026-11-22T00:00:00Z",
      2,
    );
    const { patch, container } = renderPage({
      dated: [issueA, farB],
      links: [makeLink("l-1", "i-a", "i-b")],
    });
    await waitForBars(["Blocker task", "Dependent task"]);
    dragRight(bars(container)[0]);
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(`${BASE}/issues/i-a`, {
        start_date: "2026-11-11",
        target_date: "2026-11-13",
      });
    });
    await new Promise((r) => setTimeout(r, 50));
    expect(patch).toHaveBeenCalledTimes(1);
    expect(toastAdd).not.toHaveBeenCalled();
  });

  it("partial failure toasts an error and invalidates the gantt queries", async () => {
    const { get, patch, container } = renderPage();
    await waitForBars(["Blocker task", "Dependent task"]);
    patch.mockImplementation((path: string) =>
      path.endsWith("/i-b")
        ? Promise.reject(new Error("boom"))
        : Promise.resolve({ ...issueA }),
    );
    const issueCallsBefore = get.mock.calls.filter((c) =>
      (c[0] as string).includes("/issues"),
    ).length;
    dragRight(bars(container)[0]);
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(`${BASE}/issues/i-a`, {
        start_date: "2026-11-11",
        target_date: "2026-11-13",
      });
      expect(patch).toHaveBeenCalledWith(`${BASE}/issues/i-b`, {
        start_date: "2026-11-14",
        target_date: "2026-11-16",
      });
    });
    await waitFor(() => {
      expect(
        toastAdd.mock.calls.some((c) => c[0]?.type === "error"),
      ).toBe(true);
    });
    // Resync: ["gantt"] invalidation refetches the window queries.
    await waitFor(() => {
      const now = get.mock.calls.filter((c) =>
        (c[0] as string).includes("/issues"),
      ).length;
      expect(now).toBeGreaterThan(issueCallsBefore);
    });
  });

  it("resize-end push cascades to dependents", async () => {
    const { patch, container } = renderPage();
    await waitForBars(["Blocker task", "Dependent task"]);
    // Right-edge drag: A ends 11-13, so B (13–15) violates and shifts.
    dragRight(dueHandles(container)[0]);
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(`${BASE}/issues/i-a`, {
        start_date: "2026-11-10",
        target_date: "2026-11-13",
      });
      expect(patch).toHaveBeenCalledWith(`${BASE}/issues/i-b`, {
        start_date: "2026-11-14",
        target_date: "2026-11-16",
      });
    });
    expect(
      toastAdd.mock.calls.some(
        (c) =>
          c[0]?.title === "Shifted 2 dependent issues" &&
          c[0]?.type === "success",
      ),
    ).toBe(true);
  });

  it("resize-start commit sends only the dragged PATCH (no cascade, no toast)", async () => {
    const { patch, container } = renderPage();
    await waitForBars(["Blocker task", "Dependent task"]);
    dragRight(startHandles(container)[0]);
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(`${BASE}/issues/i-a`, {
        start_date: "2026-11-11",
        target_date: "2026-11-12",
      });
    });
    await new Promise((r) => setTimeout(r, 50));
    expect(patch).toHaveBeenCalledTimes(1);
    expect(toastAdd).not.toHaveBeenCalled();
  });

  it("ghost bars render at shifted positions during drag, real bars stay, no PATCH", async () => {
    const { patch, container } = renderPage();
    await waitForBars([
      "Blocker task",
      "Dependent task",
      "Transitive task",
      "Far task",
    ]);
    const barA = bars(container)[0];
    const svg = barA.closest("svg")!;
    fireEvent.pointerDown(barA, { button: 0, clientX: 100, clientY: 100 });
    fireEvent.pointerMove(svg, { clientX: 128, clientY: 100 });
    await waitFor(() => {
      expect(ghosts(container).length).toBe(2);
    });
    // B ghost: shifted span starts 2026-11-14 → 44 days after
    // 2026-10-01 → x = 44*28 + 2 = 1234. C ghost: 2026-11-17 →
    // 47 days → x = 47*28 + 2 = 1318.
    const xs = [...ghosts(container)].map((g) => g.getAttribute("x")).sort();
    expect(xs).toEqual(["1234", "1318"]);
    // Preview is pure rendering: the real bars are untouched and
    // nothing is PATCHed mid-drag.
    expect(patch).not.toHaveBeenCalled();
    fireEvent.pointerUp(svg, { clientX: 128, clientY: 100 });
  });

  it("ghost renders for a nested child dependent (never a drag source)", async () => {
    const childRow: SubtaskRow = {
      id: "i-c1",
      display_id: "ENG-11",
      title: "Child task",
      state_id: "s-1",
      priority: 3,
      assignee_ids: [],
      sort_order: 1,
      start_date: "2026-11-13T00:00:00Z",
      target_date: "2026-11-15T00:00:00Z",
    };
    const parent = makeIssue(
      "i-p",
      "ENG-9",
      "Parent feature",
      "2026-11-10T00:00:00Z",
      "2026-11-12T00:00:00Z",
      2,
      [childRow],
    );
    const childIssue = makeIssue(
      "i-c1",
      "ENG-11",
      "Child task",
      "2026-11-13T00:00:00Z",
      "2026-11-15T00:00:00Z",
      3,
    );
    const { patch, container } = renderPage({
      dated: [issueA, parent, childIssue],
      links: [makeLink("l-1", "i-a", "i-c1")],
    });
    await waitForBars(["Blocker task", "Parent feature"]);
    await waitFor(() => {
      expect(screen.queryByText("Child task")).not.toBeNull();
    });
    const barA = bars(container)[0];
    const svg = barA.closest("svg")!;
    fireEvent.pointerDown(barA, { button: 0, clientX: 100, clientY: 100 });
    fireEvent.pointerMove(svg, { clientX: 128, clientY: 100 });
    await waitFor(() => {
      expect(ghosts(container).length).toBe(1);
    });
    const ghost = ghosts(container)[0];
    expect(ghost.getAttribute("aria-label")).toBe(
      "Cascade preview for ENG-11",
    );
    // Child shifts 13–15 → 14–16: ghost x = 44*28 + 2 = 1234.
    expect(ghost.getAttribute("x")).toBe("1234");
    expect(patch).not.toHaveBeenCalled();
    fireEvent.pointerUp(svg, { clientX: 128, clientY: 100 });
  });
});
