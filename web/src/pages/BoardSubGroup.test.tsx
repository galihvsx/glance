// @vitest-environment jsdom
// C123T0: "Sub-group by" on the board — sub-group sections inside each
// primary column (sub-header title + count, then that section's cards),
// "none" = today's flat layout, same-as-primary and groupBy-none both
// render flat, epic multi-appearance inside a column, and C122 nested
// rows follow their parent's section. BoardCardBody is module-private,
// so these tests drive the real Board page with a mocked API
// (BoardDisplay.test.tsx pattern).

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import Board from "./Board";
import { api } from "../lib/api";
import type { Issue, IssueState, SubtaskRow } from "../lib/types";
import {
  makeQueryClient,
  stubMatchMedia,
} from "../components/shell/test-utils";

// Board renders SavedViewsMenu → useAuth; the real provider needs a
// session — stub the auth context (Issues.test.tsx pattern).
vi.mock("../lib/auth", () => ({
  useAuth: () => ({ user: { id: "u-1", email: "u@x.com" }, loading: false }),
}));

const ENG = "/api/v1/workspaces/acme/projects/ENG";
const KEY = "glance:display:acme:ENG";

const states: IssueState[] = [
  {
    id: "s-todo",
    project_id: "p-eng",
    name: "Todo",
    group: "unstarted",
    color: "#888",
    sequence: 1,
    created_at: "",
    updated_at: "",
  },
  {
    id: "s-done",
    project_id: "p-eng",
    name: "Done",
    group: "completed",
    color: "#4ade80",
    sequence: 2,
    created_at: "",
    updated_at: "",
  },
];

function makeIssue(
  id: string,
  displayId: string,
  name: string,
  stateId: string,
  sortOrder: number,
  extra: Partial<Issue> = {},
): Issue {
  return {
    id,
    project_id: "p-eng",
    sequence_id: 1,
    display_id: displayId,
    name,
    priority: 3,
    state_id: stateId,
    sort_order: sortOrder,
    is_draft: false,
    created_by: "u-1",
    created_at: "",
    updated_at: "",
    assignees: [],
    labels: [],
    ...extra,
  };
}

const EPIC_A = { id: "epic-a", name: "Alpha" };
const EPIC_B = { id: "epic-b", name: "Beta" };

// Todo column: Urgent ×2, Medium ×1, Low ×1; Done column: Urgent ×1.
const issueA = makeIssue("i-a", "ENG-1", "Fix login", "s-todo", 1, {
  priority: 4,
  epics: [EPIC_A],
});
const issueB = makeIssue("i-b", "ENG-2", "Write docs", "s-todo", 2, {
  priority: 1,
  epics: [],
});
const issueC = makeIssue("i-c", "ENG-3", "Ship feature", "s-todo", 3, {
  priority: 4,
  epics: [EPIC_B],
});
const issueD = makeIssue("i-d", "ENG-4", "Release notes", "s-done", 4, {
  priority: 4,
  epics: [EPIC_A, EPIC_B],
});
const issueE = makeIssue("i-e", "ENG-5", "Multi epic todo", "s-todo", 5, {
  priority: 2,
  epics: [EPIC_A, EPIC_B],
});
const payload = [issueA, issueB, issueC, issueD, issueE];

function childRow(id: string, title: string): SubtaskRow {
  return {
    id,
    display_id: id.toUpperCase(),
    title,
    state_id: "s-done",
    priority: 4,
    assignee_ids: [],
    sort_order: 1,
  };
}
const parent = makeIssue("i-p", "ENG-1", "Parent task", "s-todo", 1, {
  priority: 3,
  subtasks: [childRow("i-c", "Child task")],
});
const child = makeIssue("i-c", "ENG-2", "Child task", "s-done", 2);
const nestedPayload = [parent, child];

function renderBoard(issues: Issue[]) {
  vi.spyOn(api, "get").mockImplementation((path: string) => {
    if (path === "/api/v1/workspaces/acme")
      return Promise.resolve({ role: 15 });
    if (path === ENG) return Promise.resolve({ id: "p-eng", name: "ENG" });
    if (path.endsWith("/states")) return Promise.resolve({ states });
    if (path.endsWith("/workflow"))
      return Promise.resolve({ states, transitions: [] });
    if (path.endsWith("/members")) return Promise.resolve({ members: [] });
    if (path.endsWith("/labels")) return Promise.resolve({ labels: [] });
    if (path.endsWith("/estimates")) return Promise.resolve({ estimates: [] });
    if (path.includes("/epics?")) return Promise.resolve({ epics: [] });
    if (path.includes("/projects/") && path.endsWith("/views"))
      return Promise.resolve({ views: [] });
    if (path.startsWith(`${ENG}/issues`))
      return Promise.resolve({ results: issues, next_cursor: null });
    return Promise.reject(new Error(`unexpected GET ${path}`));
  });
  const qc = makeQueryClient();
  render(
    <MemoryRouter initialEntries={["/w/acme/p/ENG/board"]}>
      <QueryClientProvider client={qc}>
        <Routes>
          <Route path="/w/:slug/p/:identifier/board" element={<Board />} />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

function storeSettings(settings: Record<string, unknown>) {
  localStorage.setItem(KEY, JSON.stringify(settings));
}

function allSections(): HTMLElement[] {
  return Array.from(
    document.querySelectorAll("[data-sub-group-section]"),
  ) as HTMLElement[];
}

function sectionKeyOfCard(issueId: string): string | null {
  const card = document.querySelector(`[data-board-card="${issueId}"]`);
  return (
    card
      ?.closest("[data-sub-group-section]")
      ?.getAttribute("data-sub-group-section") ?? null
  );
}

/** [title, count] of a section's sub-header (the dot span, when
 *  present, carries no text and drops out of the filtered list). */
function sectionHeader(section: HTMLElement): [string, string] {
  const spans = Array.from(
    (section.firstElementChild as HTMLElement).querySelectorAll("span"),
  )
    .map((s) => s.textContent ?? "")
    .filter((t) => t !== "");
  return [spans[0] ?? "", spans[1] ?? ""];
}

beforeEach(() => {
  stubMatchMedia(false);
  localStorage.clear();
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("board sub-group by (C123T0)", () => {
  it("renders priority sub-sections with per-section counts and card placement", async () => {
    storeSettings({
      groupBy: "state",
      subGroupBy: "priority",
      showEmptyGroups: true,
    });
    renderBoard(payload);
    await screen.findByText("Fix login");
    // 2 columns × 5 priority sections (showEmptyGroups on).
    expect(allSections()).toHaveLength(10);
    const urgent = allSections().filter(
      (s) => s.getAttribute("data-sub-group-section") === "priority:4",
    );
    expect(urgent).toHaveLength(2);
    // Todo column first (state sequence order): 2 Urgent, then Done: 1.
    expect(sectionHeader(urgent[0])).toEqual(["Urgent", "2"]);
    expect(sectionHeader(urgent[1])).toEqual(["Urgent", "1"]);
    expect(sectionKeyOfCard("i-a")).toBe("priority:4");
    expect(sectionKeyOfCard("i-b")).toBe("priority:1");
    expect(sectionKeyOfCard("i-e")).toBe("priority:2");
    expect(sectionKeyOfCard("i-d")).toBe("priority:4");
  });

  it("hides empty sub-sections when showEmptyGroups is off", async () => {
    storeSettings({
      groupBy: "state",
      subGroupBy: "priority",
      showEmptyGroups: false,
    });
    renderBoard(payload);
    await screen.findByText("Fix login");
    // Todo: Urgent, Medium, Low; Done: Urgent. No empty sections.
    expect(allSections()).toHaveLength(4);
    expect(
      allSections().map((s) =>
        s.getAttribute("data-sub-group-section"),
      ),
    ).toEqual(["priority:4", "priority:2", "priority:1", "priority:4"]);
  });

  it("same-as-primary sub-group renders flat (no sections)", async () => {
    storeSettings({
      groupBy: "state",
      subGroupBy: "state",
      showEmptyGroups: true,
    });
    renderBoard(payload);
    await screen.findByText("Fix login");
    expect(allSections()).toHaveLength(0);
    expect(
      document.querySelector('[data-board-card="i-a"]'),
    ).not.toBeNull();
  });

  it("groupBy none renders flat whatever sub-group is stored", async () => {
    storeSettings({
      groupBy: "none",
      subGroupBy: "priority",
      showEmptyGroups: true,
    });
    renderBoard(payload);
    await screen.findByText("Fix login");
    expect(allSections()).toHaveLength(0);
  });

  it("subGroupBy none is today's layout exactly", async () => {
    storeSettings({
      groupBy: "state",
      subGroupBy: "none",
      showEmptyGroups: true,
    });
    renderBoard(payload);
    await screen.findByText("Fix login");
    expect(allSections()).toHaveLength(0);
    for (const id of ["i-a", "i-b", "i-c", "i-d", "i-e"]) {
      expect(
        document.querySelector(`[data-board-card="${id}"]`),
      ).not.toBeNull();
    }
  });

  it("epic sub-sections: multi-appearance inside a column, Ungrouped last", async () => {
    storeSettings({
      groupBy: "state",
      subGroupBy: "epic",
      showEmptyGroups: true,
    });
    renderBoard(payload);
    await screen.findByText("Fix login");
    // Todo: Alpha, Beta, Ungrouped; Done: Alpha, Beta.
    expect(
      allSections().map((s) =>
        s.getAttribute("data-sub-group-section"),
      ),
    ).toEqual(["epic-a", "epic-b", "epic:ungrouped", "epic-a", "epic-b"]);
    expect(sectionKeyOfCard("i-a")).toBe("epic-a");
    expect(sectionKeyOfCard("i-b")).toBe("epic:ungrouped");
    // Multi-appearance: the two-epic Todo issue renders twice in its
    // column, once per epic section.
    expect(
      document.querySelectorAll('[data-board-card="i-e"]'),
    ).toHaveLength(2);
    // …and the two-epic Done issue renders twice in its column.
    expect(
      document.querySelectorAll('[data-board-card="i-d"]'),
    ).toHaveLength(2);
  });

  it("C122 nested rows follow the parent's sub-section", async () => {
    storeSettings({
      groupBy: "state",
      subGroupBy: "priority",
      showEmptyGroups: true,
    });
    renderBoard(nestedPayload);
    await screen.findByText("Parent task");
    // Parent is High (priority 3); the nested child row renders in
    // the same High section, and the child's flat card stays hidden.
    expect(sectionKeyOfCard("i-p")).toBe("priority:3");
    const nested = document.querySelector('[data-nested-subtask="i-c"]');
    expect(nested).not.toBeNull();
    expect(
      nested
        ?.closest("[data-sub-group-section]")
        ?.getAttribute("data-sub-group-section"),
    ).toBe("priority:3");
    expect(
      document.querySelector('[data-board-card="i-c"]'),
    ).toBeNull();
  });
});
