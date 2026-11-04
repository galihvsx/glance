// @vitest-environment jsdom
// C145T4: "Show sub-work items" on the roadmap — include_subtasks=1
// passed only when the toggle is on (full-list AND PQL fetch paths),
// nested child entries indented under their parent entry in the
// parent's month bucket (child travels with parent: a child whose own
// target month differs from the parent's still renders in the
// parent's bucket), children of an unscheduled parent nest in the
// Unscheduled lane, orphans stay flat, toggle off = today's flat
// entries. Roadmap-local toggle (own storage key, C143 precedent),
// default on. Follows the RoadmapPql test harness.

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
import Roadmap from "./Roadmap";
import { api } from "../lib/api";
import type { Issue, IssueState, SubtaskRow } from "../lib/types";
import {
  makeQueryClient,
  stubMatchMedia,
} from "../components/shell/test-utils";

// Roadmap renders SavedViewsMenu → useAuth; the real provider needs a
// session — stub the auth context (RoadmapPql.test.tsx pattern).
vi.mock("../lib/auth", () => ({
  useAuth: () => ({ user: { id: "u-1", email: "u@x.com" }, loading: false }),
}));

const BASE = "/api/v1/workspaces/acme/projects/ENG";
const KEY = "glance:roadmap-sub-work-items:acme:ENG";

const states: IssueState[] = [
  {
    id: "s-1",
    project_id: "p-eng",
    name: "Todo",
    group: "unstarted",
    color: "#888",
    sequence: 1,
    created_at: "",
    updated_at: "",
  },
];

// Anchor is the current month at render time, so target dates are
// built relative to it, exactly as in RoadmapPql.test.tsx.
function monthDay(offset: number, day: number): string {
  const d = new Date();
  const m = new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth() + offset, 1));
  return `${m.getUTCFullYear()}-${String(m.getUTCMonth() + 1).padStart(2, "0")}-${String(day).padStart(2, "0")}T00:00:00Z`;
}

function monthLabel(offset: number): string {
  const d = new Date();
  const m = new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth() + offset, 1));
  return m.toLocaleDateString(undefined, {
    month: "long",
    year: "numeric",
    timeZone: "UTC",
  });
}

function shortDay(offset: number, day: number): string {
  const d = new Date();
  const m = new Date(
    Date.UTC(d.getUTCFullYear(), d.getUTCMonth() + offset, day),
  );
  return m.toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
    timeZone: "UTC",
  });
}

function makeIssue(
  id: string,
  displayId: string,
  name: string,
  targetDate: string | null,
  extra: Partial<Issue> = {},
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
    target_date: targetDate,
    ...extra,
  };
}

function childRow(
  id: string,
  displayId: string,
  title: string,
  sortOrder: number,
  targetDate?: string | null,
): SubtaskRow {
  return {
    id,
    display_id: displayId,
    title,
    state_id: "s-1",
    priority: 3,
    assignee_ids: [],
    sort_order: sortOrder,
    target_date: targetDate ?? null,
  };
}

// Parent dated in the anchor month, with two children: one whose own
// target is NEXT month (travels with the parent — it must NOT land in
// the next month's bucket), one dateless (no date text, never a
// fabricated bucket).
const parent = makeIssue("i-p", "ENG-1", "Parent launch task", monthDay(0, 15), {
  subtasks: [
    childRow("i-ca", "ENG-2", "Child task A", 1, monthDay(1, 10)),
    childRow("i-cb", "ENG-3", "Child task B", 2, null),
  ],
});
const childA = makeIssue("i-ca", "ENG-2", "Child task A", monthDay(1, 10));
const childB = makeIssue("i-cb", "ENG-3", "Child task B", null);
// Undated parent with a dated child: both nest in the Unscheduled lane.
const unscheduledParent = makeIssue(
  "i-up",
  "ENG-4",
  "Unscheduled parent task",
  null,
  { subtasks: [childRow("i-cu", "ENG-5", "Unscheduled child task", 1, monthDay(0, 20))] },
);
const unscheduledChild = makeIssue(
  "i-cu",
  "ENG-5",
  "Unscheduled child task",
  monthDay(0, 20),
);
// Orphan: parent not in the result set — stays a flat entry.
const orphan = makeIssue("i-o", "ENG-6", "Orphan task", monthDay(0, 5));

const nestedPayload = [
  parent,
  childA,
  childB,
  unscheduledParent,
  unscheduledChild,
  orphan,
];
const flatPayload = nestedPayload.map(
  ({ subtasks: _subtasks, ...rest }) => rest,
);

function renderPage() {
  const get = vi.spyOn(api, "get").mockImplementation((path: string) => {
    if (path === "/api/v1/workspaces")
      return Promise.resolve({ workspaces: [] });
    if (path === "/api/v1/workspaces/acme")
      return Promise.resolve({ role: 15 });
    if (path === BASE)
      return Promise.resolve({
        id: "p-eng",
        workspace_id: "w-acme",
        identifier: "ENG",
        name: "ENG",
        description: "",
        parallel_cycles: false,
        labels: [],
        created_at: "",
        updated_at: "",
      });
    if (path === `${BASE}/states`) return Promise.resolve({ states });
    if (path.startsWith(`${BASE}/issues`)) {
      const qs = new URLSearchParams(path.split("?")[1] ?? "");
      // The backend only attaches subtask rows when the flag is set.
      return Promise.resolve({
        results:
          qs.get("include_subtasks") === "1" ? nestedPayload : flatPayload,
        next_cursor: null,
      });
    }
    return Promise.reject(new Error(`unexpected GET ${path}`));
  });
  const qc = makeQueryClient();
  render(
    <MemoryRouter initialEntries={["/w/acme/p/ENG/roadmap"]}>
      <QueryClientProvider client={qc}>
        <Routes>
          <Route path="/w/:slug/p/:identifier/roadmap" element={<Roadmap />} />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
  return { get };
}

function issueCalls(get: ReturnType<typeof renderPage>["get"]) {
  return get.mock.calls
    .map((c) => c[0] as string)
    .filter((p) => p.includes("/issues"))
    .map((p) => new URLSearchParams(p.split("?")[1] ?? ""));
}

function sectionText(label: string): string {
  const section = document.querySelector(`section[aria-label="${label}"]`);
  expect(section).toBeTruthy();
  return section!.textContent ?? "";
}

async function waitForRoadmap() {
  await screen.findByText("Parent launch task");
  await screen.findByText("Unscheduled parent task");
  await screen.findByText("Orphan task");
}

beforeEach(() => {
  stubMatchMedia(false);
  localStorage.clear();
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("roadmap sub-work items (C145T4)", () => {
  it("toggle on (default) passes include_subtasks=1 on the full-list fetch", async () => {
    const { get } = renderPage();
    await waitForRoadmap();
    const calls = issueCalls(get).filter((qs) => qs.get("pql") === null);
    expect(calls.length).toBeGreaterThan(0);
    for (const qs of calls) {
      expect(qs.get("include_subtasks")).toBe("1");
    }
    // Default on: the toggle checkbox is checked.
    expect(
      screen.getByLabelText("Show sub-work items") as HTMLInputElement,
    ).toHaveProperty("checked", true);
  });

  it("toggle on passes include_subtasks=1 on the PQL fetch path", async () => {
    const { get } = renderPage();
    await waitForRoadmap();
    fireEvent.click(screen.getByRole("button", { name: "PQL" }));
    await screen.findByLabelText("PQL query");
    fireEvent.change(screen.getByLabelText("PQL query"), {
      target: { value: "priority = High" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Apply" }));
    await waitFor(() => {
      const pqlCalls = issueCalls(get).filter(
        (qs) => qs.get("pql") !== null,
      );
      expect(pqlCalls.length).toBeGreaterThan(0);
      for (const qs of pqlCalls) {
        expect(qs.get("include_subtasks")).toBe("1");
      }
    });
  });

  it("child entries nest under the parent in the parent's bucket with their own date text", async () => {
    renderPage();
    await waitForRoadmap();
    // Both children appear exactly once (nested, not also flat).
    expect(screen.getAllByText("Child task A")).toHaveLength(1);
    expect(screen.getAllByText("Child task B")).toHaveLength(1);
    expect(document.querySelectorAll('[data-nested="true"]')).toHaveLength(3);
    // Child A travels with the parent: it sits in the anchor-month
    // section even though its own target is next month.
    const anchorText = sectionText(monthLabel(0));
    expect(anchorText).toContain("Child task A");
    expect(anchorText).toContain("Child task B");
    expect(sectionText(monthLabel(1))).not.toContain("Child task A");
    // Child A's own target date text, not the parent's.
    expect(screen.getByText(`Due ${shortDay(1, 10)}`)).toBeTruthy();
    // Dateless child shows no date text: its entry has no "Due".
    const childBButton = document.querySelector(
      'button[title="ENG-3: Child task B"]',
    );
    expect(childBButton).toBeTruthy();
    expect(childBButton!.textContent).not.toContain("Due");
  });

  it("children of an unscheduled parent nest under that parent in the Unscheduled lane", async () => {
    renderPage();
    await waitForRoadmap();
    const unscheduledText = sectionText("Unscheduled");
    expect(unscheduledText).toContain("Unscheduled parent task");
    expect(unscheduledText).toContain("Unscheduled child task");
    // The dated unscheduled child does NOT get bucketed by its own
    // date into the anchor-month column.
    expect(sectionText(monthLabel(0))).not.toContain(
      "Unscheduled child task",
    );
    expect(screen.getAllByText("Unscheduled child task")).toHaveLength(1);
  });

  it("orphan child stays a flat entry", async () => {
    renderPage();
    await waitForRoadmap();
    expect(screen.getByText("Orphan task")).toBeTruthy();
    const orphanButton = document.querySelector(
      'button[title="ENG-6: Orphan task"]',
    );
    expect(orphanButton).toBeTruthy();
    expect(orphanButton!.getAttribute("data-nested")).toBeNull();
  });

  it("toggle off omits the flag and renders today's flat entries", async () => {
    localStorage.setItem(KEY, "false");
    const { get } = renderPage();
    await waitForRoadmap();
    const calls = issueCalls(get);
    expect(calls.length).toBeGreaterThan(0);
    for (const qs of calls) {
      expect(qs.get("include_subtasks")).toBeNull();
    }
    expect(document.querySelectorAll('[data-nested="true"]')).toHaveLength(0);
    // Every issue still renders, flat — nothing vanishes.
    expect(screen.getByText("Child task A")).toBeTruthy();
    expect(screen.getByText("Child task B")).toBeTruthy();
    expect(screen.getByText("Unscheduled child task")).toBeTruthy();
  });

  it("unchecking the toggle refetches flat and drops the nesting", async () => {
    const { get } = renderPage();
    await waitForRoadmap();
    expect(document.querySelectorAll('[data-nested="true"]').length).toBeGreaterThan(0);
    fireEvent.click(screen.getByLabelText("Show sub-work items"));
    // Require a flat entry to be present too: during the refetch the
    // page shows "Loading roadmap…" and the nested count is briefly
    // 0 for the wrong reason.
    await waitFor(() => {
      expect(screen.getByText("Child task A")).toBeTruthy();
      expect(document.querySelectorAll('[data-nested="true"]')).toHaveLength(0);
    });
    const flatCalls = issueCalls(get).filter(
      (qs) => qs.get("include_subtasks") === null,
    );
    expect(flatCalls.length).toBeGreaterThan(0);
  });
});
