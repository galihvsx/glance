// @vitest-environment jsdom
// C145T3: "Show sub-work items" on the Gantt view — nested child rows
// under their parent row, each with its own bar from the child's own
// dates (C145T1 fields), display + click-to-peek only (no move drag,
// no edge-resize drag on child bars). Follows the GanttPql test
// harness (mocked api.get, same window, same auth stub).

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
import type { Issue, IssueState, SubtaskRow } from "../lib/types";
import {
  makeQueryClient,
  stubMatchMedia,
} from "../components/shell/test-utils";

// jsdom has no PointerEvent — the drag handlers need clientX/button
// (Calendar.test.tsx pattern).
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
  seq: number,
  startDate: string | null,
  targetDate: string | null,
  subtasks?: SubtaskRow[],
): Issue {
  return {
    id,
    project_id: "p-eng",
    sequence_id: seq,
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

// Dated inside the Nov-2026-centered window (Oct–Dec 2026); the bar
// dates are RFC3339 midnight, as the backend serializes them.
const childRow1: SubtaskRow = {
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
const childRow2: SubtaskRow = {
  id: "i-c2",
  display_id: "ENG-12",
  title: "Dateless child",
  state_id: "s-1",
  priority: 3,
  assignee_ids: [],
  sort_order: 2,
};
const parentBase = () =>
  makeIssue(
    "i-p",
    "ENG-1",
    "Parent feature",
    1,
    "2026-11-10T00:00:00Z",
    "2026-11-12T00:00:00Z",
  );
const childIssue1 = makeIssue(
  "i-c1",
  "ENG-11",
  "Child task",
  2,
  "2026-11-15T00:00:00Z",
  "2026-11-17T00:00:00Z",
);
const childIssue2 = makeIssue(
  "i-c2",
  "ENG-12",
  "Dateless child",
  3,
  null,
  null,
);
// Orphan: never appears in any parent's subtasks array, so it stays
// a flat row in its own place even with the toggle on.
const orphan = makeIssue(
  "i-o",
  "ENG-9",
  "Orphan task",
  4,
  "2026-11-20T00:00:00Z",
  "2026-11-22T00:00:00Z",
);

function renderPage() {
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
      const withSubtasks = qs.get("include_subtasks") === "1";
      const parent = withSubtasks
        ? { ...parentBase(), subtasks: [childRow1, childRow2] }
        : parentBase();
      if (qs.get("undated") !== null)
        return Promise.resolve({ results: [childIssue2], next_cursor: null });
      return Promise.resolve({
        results: [parent, childIssue1, childIssue2, orphan],
        next_cursor: null,
      });
    }
    return Promise.reject(new Error(`unexpected GET ${path}`));
  });
  const patch = vi.spyOn(api, "patch").mockImplementation(() =>
    Promise.resolve({ ...orphan }),
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
  return { get, patch, container };
}

async function waitForFullSet() {
  await waitFor(() => {
    expect(screen.queryByText("Parent feature")).not.toBeNull();
    expect(screen.queryByText("Child task")).not.toBeNull();
    expect(screen.queryByText("Dateless child")).not.toBeNull();
    expect(screen.queryByText("Orphan task")).not.toBeNull();
  });
}

function issueCalls(get: ReturnType<typeof renderPage>["get"]) {
  return get.mock.calls
    .map((c) => c[0] as string)
    .filter((p) => p.includes("/issues"))
    .map((p) => new URLSearchParams(p.split("?")[1] ?? ""));
}

/** Gutter buttons (title "ENG-…") in DOM order. */
function gutterTitles(container: HTMLElement): string[] {
  return Array.from(
    container.querySelectorAll('button[title^="ENG-"]'),
  ).map((b) => b.getAttribute("title")!);
}

function gutterButton(container: HTMLElement, title: string): HTMLElement {
  const btn = container.querySelector(`button[title="${title}"]`);
  expect(btn).not.toBeNull();
  return btn as HTMLElement;
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
  // The toggle persists per project in localStorage: a previous
  // test's toggle-off must not leak into the next test's default.
  localStorage.clear();
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("Gantt sub-work items (C145T3)", () => {
  it("toggle on (default) passes include_subtasks=1 on all window queries", async () => {
    const { get } = renderPage();
    await waitForFullSet();
    expect(
      (screen.getByLabelText("Show sub-work items") as HTMLInputElement)
        .checked,
    ).toBe(true);
    const calls = issueCalls(get);
    expect(calls.length).toBeGreaterThan(0);
    for (const qs of calls) {
      expect(qs.get("include_subtasks")).toBe("1");
    }
    expect(calls.some((qs) => qs.get("due_after") !== null)).toBe(true);
    expect(calls.some((qs) => qs.get("start_after") !== null)).toBe(true);
    expect(calls.some((qs) => qs.get("undated") === "1")).toBe(true);
  });

  it("PQL path carries the flag on probes and window queries", async () => {
    const { get } = renderPage();
    await waitForFullSet();
    await applyPql("priority = High");
    await waitFor(() => {
      const pqlCalls = issueCalls(get).filter(
        (qs) => qs.get("pql") !== null,
      );
      expect(pqlCalls.length).toBeGreaterThan(0);
      for (const qs of pqlCalls) {
        expect(qs.get("include_subtasks")).toBe("1");
      }
    });
    // Nested rows survive the PQL round-trip (mock returns the
    // same set for PQL queries).
    expect(screen.queryByText("Child task")).not.toBeNull();
  });

  it("child rows nest under the parent with a bar at the child's own span; dateless child has no bar", async () => {
    const { container } = renderPage();
    await waitForFullSet();
    // Gutter order: parent, then its children in sort_order ASC,
    // then the orphan — children never also appear as flat rows.
    expect(gutterTitles(container)).toEqual([
      "ENG-1: Parent feature",
      "ENG-11: Child task",
      "ENG-12: Dateless child",
      "ENG-9: Orphan task",
    ]);
    // Indent marks nesting: children indented, parent/orphan flat.
    expect(
      gutterButton(container, "ENG-11: Child task").style.paddingLeft,
    ).toBe("28px");
    expect(
      gutterButton(container, "ENG-12: Dateless child").style.paddingLeft,
    ).toBe("28px");
    expect(
      gutterButton(container, "ENG-1: Parent feature").style.paddingLeft,
    ).toBe("");
    // Bars: parent (Nov 10–12), child (Nov 15–17), orphan
    // (Nov 20–22). The dateless child adds no fourth bar.
    // Window starts 2026-10-01, DAY_W 28: child x = 45*28+2 = 1262,
    // width = 3 days * 28 - 4 = 80.
    const bars = container.querySelectorAll('rect[fill="#6366f1"]');
    expect(bars.length).toBe(3);
    const childBar = Array.from(bars).find(
      (r) => r.getAttribute("x") === "1262",
    );
    expect(childBar).not.toBeUndefined();
    expect(childBar!.getAttribute("width")).toBe("80");
  });

  it("orphan child stays a flat row with its own bar", async () => {
    const { container } = renderPage();
    await waitForFullSet();
    expect(
      gutterButton(container, "ENG-9: Orphan task").style.paddingLeft,
    ).toBe("");
    // Orphan bar: Nov 20–22 → x = 50*28+2 = 1402, width 80.
    const bars = container.querySelectorAll('rect[fill="#6366f1"]');
    expect(
      Array.from(bars).some((r) => r.getAttribute("x") === "1402"),
    ).toBe(true);
  });

  it("child bars have no resize handles and no move drag", async () => {
    const { patch, container } = renderPage();
    await waitForFullSet();
    // Only the two parent bars (parent + orphan) carry resize
    // handles — the child bar adds none.
    expect(
      container.querySelectorAll(
        'rect[aria-label="Drag to change start date"]',
      ).length,
    ).toBe(2);
    expect(
      container.querySelectorAll(
        'rect[aria-label="Drag to change due date"]',
      ).length,
    ).toBe(2);
    // Pressing + dragging the child bar issues no PATCH.
    const childBar = Array.from(
      container.querySelectorAll('rect[fill="#6366f1"]'),
    ).find((r) => r.getAttribute("x") === "1262");
    expect(childBar).not.toBeUndefined();
    const svg = childBar!.closest("svg");
    expect(svg).not.toBeNull();
    fireEvent.pointerDown(childBar!, { button: 0, clientX: 100, clientY: 100 });
    fireEvent.pointerMove(svg!, { clientX: 128, clientY: 100 });
    fireEvent.pointerUp(svg!, { clientX: 128, clientY: 100 });
    expect(patch).not.toHaveBeenCalled();
  });

  it("toggle off omits the flag and renders today's flat rows", async () => {
    const { get, container } = renderPage();
    await waitForFullSet();
    const callsBefore = get.mock.calls.length;
    fireEvent.click(screen.getByLabelText("Show sub-work items"));
    await waitFor(() => {
      // Flat: no indented gutter buttons remain.
      expect(
        gutterButton(container, "ENG-11: Child task").style.paddingLeft,
      ).toBe("");
      expect(
        gutterButton(container, "ENG-12: Dateless child").style.paddingLeft,
      ).toBe("");
    });
    // Every refetch after the toggle omits the flag.
    const after = get.mock.calls
      .slice(callsBefore)
      .map((c) => c[0] as string)
      .filter((p) => p.includes("/issues"))
      .map((p) => new URLSearchParams(p.split("?")[1] ?? ""));
    expect(after.length).toBeGreaterThan(0);
    for (const qs of after) {
      expect(qs.get("include_subtasks")).toBeNull();
    }
    // All four issues still render (nothing vanishes).
    expect(screen.queryByText("Parent feature")).not.toBeNull();
    expect(screen.queryByText("Child task")).not.toBeNull();
    expect(screen.queryByText("Dateless child")).not.toBeNull();
    expect(screen.queryByText("Orphan task")).not.toBeNull();
  });
});
