// @vitest-environment jsdom
// PQL mode on the Gantt view (C109T2): toggle on/off, PQL-mode fetch
// params (each window query carries its window + pql +
// include_links), invalid-PQL error display with the bars intact,
// re-apply re-seeding the editor (the C107 key lesson), window
// navigation keeping the applied query, and drag-to-reschedule on a
// visible bar in PQL mode still issuing the PATCH (the mutation
// targets the issue, not the query).

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
// C110T2: Gantt now mounts the detail-mode SavedViewsMenu, whose
// useSavedViews hook reads useAuth; the real provider needs a
// session — stub the auth context (CyclesPql.test.tsx pattern).
// The menu itself is untested here (projectId stays "" — this
// file's mock has no project endpoint — so its views query stays
// disabled); GanttSavedViews.test.tsx covers the menu path.
vi.mock("../lib/auth", () => ({
  useAuth: () => ({ user: { id: "u-1", email: "u@x.com" }, loading: false }),
}));
import Gantt from "./Gantt";
import { api, ApiError } from "../lib/api";
import type { Issue, IssueState } from "../lib/types";
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
  startDate: string | null,
  targetDate: string | null,
): Issue {
  return {
    id,
    project_id: "p-eng",
    sequence_id: 1,
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

// Dated inside the Nov-2026-centered window (Oct–Dec 2026); the bar
// dates are RFC3339 midnight, as the backend serializes them.
const issueA = makeIssue(
  "i-a",
  "ENG-1",
  "Fix login redirect",
  "2026-11-10T00:00:00Z",
  "2026-11-12T00:00:00Z",
);
const issueB = makeIssue(
  "i-b",
  "ENG-2",
  "Urgent checkout bug",
  "2026-11-11T00:00:00Z",
  "2026-11-13T00:00:00Z",
);
const issueC = makeIssue("i-c", "ENG-3", "Undated research spike", null, null);

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
      const pql = qs.get("pql");
      if (pql !== null) {
        if (pql.includes("bogus")) {
          return Promise.reject(
            new ApiError(400, "unknown field: bogus", "validation_error", {
              position: 1,
            }),
          );
        }
        // Second query, so a re-apply while already in PQL mode is
        // observable in the bars as well as in the editor draft.
        if (pql.includes("Low")) {
          if (qs.get("undated") !== null)
            return Promise.resolve({ results: [issueC], next_cursor: null });
          return Promise.resolve({ results: [issueA], next_cursor: null });
        }
        if (qs.get("undated") !== null)
          return Promise.resolve({ results: [], next_cursor: null });
        return Promise.resolve({ results: [issueB], next_cursor: null });
      }
      if (qs.get("undated") !== null)
        return Promise.resolve({ results: [issueC], next_cursor: null });
      return Promise.resolve({
        results: [issueA, issueB],
        next_cursor: null,
      });
    }
    return Promise.reject(new Error(`unexpected GET ${path}`));
  });
  const patch = vi.spyOn(api, "patch").mockImplementation(() =>
    Promise.resolve({
      ...issueB,
      start_date: "2026-11-12T00:00:00Z",
      target_date: "2026-11-14T00:00:00Z",
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
  return { get, patch, container };
}

async function waitForFullSet() {
  // Combined condition: during a query swap the timeline hides
  // behind "Loading timeline…", so absence alone would pass early.
  await waitFor(() => {
    expect(screen.queryByText("Fix login redirect")).not.toBeNull();
    expect(screen.queryByText("Urgent checkout bug")).not.toBeNull();
    expect(screen.queryByText("Undated research spike")).not.toBeNull();
  });
}

async function applyPql(query: string) {
  fireEvent.click(screen.getByRole("button", { name: "PQL" }));
  await screen.findByLabelText("PQL query");
  fireEvent.change(screen.getByLabelText("PQL query"), {
    target: { value: query },
  });
  fireEvent.click(screen.getByRole("button", { name: "Apply" }));
}

function issueCalls(get: ReturnType<typeof renderPage>["get"]) {
  return get.mock.calls
    .map((c) => c[0] as string)
    .filter((p) => p.includes("/issues"))
    .map((p) => new URLSearchParams(p.split("?")[1] ?? ""));
}

beforeEach(() => {
  stubMatchMedia(false);
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("Gantt PQL mode (C109T2)", () => {
  it("default mode shows all bars + the unscheduled lane with no PQL editor", async () => {
    const { get } = renderPage();
    await waitForFullSet();
    expect(screen.queryByLabelText("PQL query")).toBeNull();
    expect(
      screen.getByRole("button", { name: "All issues" }),
    ).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByText("Unscheduled")).toBeTruthy();
    // Visual fetches carry their window params + include_links, no pql.
    const calls = issueCalls(get);
    expect(calls.length).toBeGreaterThan(0);
    for (const qs of calls) {
      expect(qs.get("pql")).toBeNull();
      expect(qs.get("include_links")).toBe("1");
    }
    expect(
      calls.some((qs) => qs.get("due_after") !== null),
    ).toBe(true);
    expect(
      calls.some((qs) => qs.get("start_after") !== null),
    ).toBe(true);
    expect(calls.some((qs) => qs.get("undated") === "1")).toBe(true);
  });

  it("applying a PQL query narrows bars + unscheduled lane, editor shows the applied query", async () => {
    const { get } = renderPage();
    await waitForFullSet();
    await applyPql("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("Fix login redirect")).toBeNull();
      expect(screen.queryByText("Urgent checkout bug")).not.toBeNull();
      expect(screen.queryByText("Undated research spike")).toBeNull();
    });
    expect(screen.queryByText("Unscheduled")).toBeNull();
    expect(
      (screen.getByLabelText("PQL query") as HTMLTextAreaElement).value,
    ).toBe("priority = High");
    // Every PQL fetch carries its window + pql + include_links.
    const pqlCalls = issueCalls(get).filter((qs) => qs.get("pql") !== null);
    expect(pqlCalls.length).toBeGreaterThan(0);
    for (const qs of pqlCalls) {
      expect(qs.get("pql")).toBe("priority = High");
      expect(qs.get("include_links")).toBe("1");
    }
    expect(
      pqlCalls.some(
        (qs) =>
          qs.get("due_after") !== null && qs.get("due_before") !== null,
      ),
    ).toBe(true);
    expect(
      pqlCalls.some(
        (qs) =>
          qs.get("start_after") !== null && qs.get("start_before") !== null,
      ),
    ).toBe(true);
    expect(pqlCalls.some((qs) => qs.get("undated") === "1")).toBe(true);
    // C110T2: the detail-mode Views menu beside the toggle is now
    // the save path, so the corrected save hint is shown.
    expect(
      screen.getByText(
        "To save this query, use Views → Save current view next to the All issues / PQL toggle; it re-applies when the view is opened.",
      ),
    ).toBeTruthy();
  });

  it("leaving PQL mode restores the unfiltered bars + lane and closes the editor", async () => {
    renderPage();
    await waitForFullSet();
    await applyPql("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("Fix login redirect")).toBeNull();
      expect(screen.queryByText("Urgent checkout bug")).not.toBeNull();
    });
    fireEvent.click(screen.getByRole("button", { name: "All issues" }));
    await waitForFullSet();
    expect(screen.queryByLabelText("PQL query")).toBeNull();
    expect(screen.getByText("Unscheduled")).toBeTruthy();
  });

  it("invalid PQL shows the editor error with column and leaves the bars untouched", async () => {
    renderPage();
    await waitForFullSet();
    await applyPql("bogus = 1");
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("unknown field: bogus");
    expect(alert.textContent).toContain("at column 1");
    // Sets untouched: all bars + the unscheduled lane still on screen.
    expect(screen.getByText("Fix login redirect")).toBeTruthy();
    expect(screen.getByText("Urgent checkout bug")).toBeTruthy();
    expect(screen.getByText("Undated research spike")).toBeTruthy();
    // No unhandled rejection: the probe failure is caught by the
    // editor (a rejection here would fail the run).
    expect(
      screen.getByRole("button", { name: "PQL" }),
    ).toHaveAttribute("aria-pressed", "true");
  });

  it("re-applying a second query while in PQL mode re-seeds the editor", async () => {
    renderPage();
    await waitForFullSet();
    await applyPql("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("Fix login redirect")).toBeNull();
      expect(screen.queryByText("Urgent checkout bug")).not.toBeNull();
    });
    expect(
      (screen.getByLabelText("PQL query") as HTMLTextAreaElement).value,
    ).toBe("priority = High");
    // Already in PQL mode: the editor is mounted, so without the
    // applied-query key its draft would stay on the first query.
    fireEvent.change(screen.getByLabelText("PQL query"), {
      target: { value: "priority = Low" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Apply" }));
    await waitFor(() => {
      expect(screen.queryByText("Fix login redirect")).not.toBeNull();
      expect(screen.queryByText("Urgent checkout bug")).toBeNull();
      expect(screen.queryByText("Undated research spike")).not.toBeNull();
    });
    expect(
      (screen.getByLabelText("PQL query") as HTMLTextAreaElement).value,
    ).toBe("priority = Low");
  });

  it("window navigation keeps the applied query", async () => {
    const { get } = renderPage();
    await waitForFullSet();
    await applyPql("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("Fix login redirect")).toBeNull();
      expect(screen.queryByText("Urgent checkout bug")).not.toBeNull();
    });
    // Nov-centered window starts 2026-10-01; moving to Dec shifts
    // the due window to 2026-11-01 while the query stays applied.
    fireEvent.click(screen.getByRole("button", { name: "Next month" }));
    await waitFor(() => {
      const moved = issueCalls(get).filter(
        (qs) =>
          qs.get("pql") === "priority = High" &&
          qs.get("due_after") === "2026-11-01T00:00:00Z",
      );
      expect(moved.length).toBeGreaterThan(0);
    });
    expect(
      (screen.getByLabelText("PQL query") as HTMLTextAreaElement).value,
    ).toBe("priority = High");
  });

  it("drag-reschedule on a visible bar in PQL mode still issues the PATCH", async () => {
    const { patch, container } = renderPage();
    await waitForFullSet();
    await applyPql("priority = High");
    await waitFor(() => {
      expect(screen.queryByText("Fix login redirect")).toBeNull();
      expect(screen.queryByText("Urgent checkout bug")).not.toBeNull();
    });
    // Only issueB's bar is visible in PQL mode: the one rect filled
    // with its state color (month/day grid rects use classes).
    const bar = container.querySelector('rect[fill="#6366f1"]');
    expect(bar).not.toBeNull();
    // The page has many icon SVGs; the drag handlers live on the
    // timeline SVG, which is the bar's own ancestor.
    const svg = bar!.closest("svg");
    expect(svg).not.toBeNull();
    // jsdom bounding rects are all zero, so svgX = clientX: a 28px
    // move (one DAY_W) shifts the bar exactly one day.
    fireEvent.pointerDown(bar!, { button: 0, clientX: 100, clientY: 100 });
    fireEvent.pointerMove(svg!, { clientX: 128, clientY: 100 });
    fireEvent.pointerUp(svg!, { clientX: 128, clientY: 100 });
    await waitFor(() => {
      expect(patch).toHaveBeenCalledWith(`${BASE}/issues/i-b`, {
        start_date: "2026-11-12",
        target_date: "2026-11-14",
      });
    });
  });
});
