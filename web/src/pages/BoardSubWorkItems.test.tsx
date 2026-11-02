// @vitest-environment jsdom
// C122T1: "Show sub-work items" on the board — nested rows under
// parent cards, flat child cards hidden while nested, orphans stay
// flat, toggle off = today's flat layout, include_subtasks=1 passed
// only when the toggle is on (visual AND PQL fetch paths), and
// undefined subtasks renders nothing nested and never crashes.
// BoardCardBody is module-private, so these tests drive the real
// Board page with a mocked API (BoardDisplay.test.tsx pattern).

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

function childRow(id: string, title: string, sortOrder: number): SubtaskRow {
  return {
    id,
    display_id: id.toUpperCase(),
    title,
    state_id: "s-done",
    priority: 4,
    assignee_ids: ["u-9"],
    sort_order: sortOrder,
  };
}

// Parent (Todo) with one nested child (whose own state is Done —
// the nested row still renders in the parent's Todo column).
const parent = makeIssue("i-p", "ENG-1", "Parent task", "s-todo", 1, {
  subtasks: [childRow("i-c", "Child task", 1)],
});
// The same child as a flat issue: hidden while nested, and it
// carries a grandchild row — the grandchild stays flat (one level).
const child = makeIssue("i-c", "ENG-2", "Child task", "s-done", 2, {
  subtasks: [childRow("i-g", "Grandchild task", 1)],
});
const grandchild = makeIssue("i-g", "ENG-3", "Grandchild task", "s-todo", 3);
// Orphan: parent not in the result set — stays a flat card.
const orphan = makeIssue("i-o", "ENG-4", "Orphan task", "s-done", 4);

const nestedPayload = [parent, child, grandchild, orphan];
const flatPayload = [parent, child, grandchild, orphan].map(
  ({ subtasks: _subtasks, ...rest }) => rest,
);

function renderBoard(payload: Issue[]) {
  const get = vi.spyOn(api, "get").mockImplementation((path: string) => {
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
      return Promise.resolve({ results: payload, next_cursor: null });
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
  return { get };
}

function issueCalls(get: ReturnType<typeof renderBoard>["get"]) {
  return get.mock.calls
    .map((c) => c[0] as string)
    .filter((p) => p.includes("/issues?"))
    .map((p) => new URLSearchParams(p.split("?")[1] ?? ""));
}

function storeSettings(showSubWorkItems: boolean) {
  localStorage.setItem(
    KEY,
    JSON.stringify({
      groupBy: "state",
      orderBy: "-updated_at",
      showEmptyGroups: true,
      showSubWorkItems,
    }),
  );
}

beforeEach(() => {
  stubMatchMedia(false);
  localStorage.clear();
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("board sub-work items (C122T1)", () => {
  it("passes include_subtasks=1 on the visual fetch when the toggle is on (default)", async () => {
    const { get } = renderBoard(nestedPayload);
    await screen.findByText("Parent task");
    const calls = issueCalls(get);
    expect(calls.length).toBeGreaterThan(0);
    for (const qs of calls) {
      expect(qs.get("include_subtasks")).toBe("1");
    }
  });

  it("nests the child under its parent, hides its flat card, keeps orphan + grandchild flat", async () => {
    renderBoard(nestedPayload);
    await screen.findByText("Parent task");
    // Nested row present (child's own state is Done, but the row
    // renders in the parent's column).
    expect(
      document.querySelector('[data-nested-subtask="i-c"]'),
    ).not.toBeNull();
    // Flat card for the nested child is gone…
    expect(
      document.querySelector('[data-board-card="i-c"]'),
    ).toBeNull();
    // …while the orphan and the grandchild keep their flat cards,
    // and the grandchild never gains a nested row (one level only).
    expect(
      document.querySelector('[data-board-card="i-o"]'),
    ).not.toBeNull();
    expect(
      document.querySelector('[data-board-card="i-g"]'),
    ).not.toBeNull();
    expect(
      document.querySelector('[data-nested-subtask="i-g"]'),
    ).toBeNull();
    // Nested row shows the child's priority icon + assignee avatar.
    const nested = document.querySelector(
      '[data-nested-subtask="i-c"]',
    ) as HTMLElement;
    expect(nested.textContent).toContain("Child task");
    expect(
      nested.querySelector('[aria-label="Priority: Urgent"]'),
    ).not.toBeNull();
    expect(
      nested.querySelector('[aria-label="1 assignee"]'),
    ).not.toBeNull();
  });

  it("toggle off = flat layout: child card back, no nested rows, no flag", async () => {
    storeSettings(false);
    const { get } = renderBoard(nestedPayload);
    await screen.findByText("Parent task");
    expect(
      document.querySelector('[data-board-card="i-c"]'),
    ).not.toBeNull();
    expect(
      document.querySelector("[data-nested-subtask]"),
    ).toBeNull();
    for (const qs of issueCalls(get)) {
      expect(qs.get("include_subtasks")).toBeNull();
    }
  });

  it("undefined subtasks renders no nested rows and no crash", async () => {
    renderBoard(flatPayload);
    await screen.findByText("Parent task");
    expect(
      document.querySelector('[data-board-card="i-c"]'),
    ).not.toBeNull();
    expect(
      document.querySelector("[data-nested-subtask]"),
    ).toBeNull();
  });

  it("PQL fetch path passes include_subtasks=1 and still nests", async () => {
    const { get } = renderBoard(nestedPayload);
    await screen.findByText("Parent task");
    fireEvent.click(screen.getByRole("button", { name: "PQL" }));
    await screen.findByLabelText("PQL query");
    fireEvent.change(screen.getByLabelText("PQL query"), {
      target: { value: "priority = High" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Apply" }));
    // Wait for the committed PQL query (probe first, then the query
    // itself) — the visual board already shows the nested row, so
    // waiting on text alone would not prove the PQL path fired.
    await waitFor(() => {
      const calls = issueCalls(get).filter(
        (qs) => qs.get("pql") !== null,
      );
      expect(calls.length).toBeGreaterThan(0);
    });
    const pqlCalls = issueCalls(get).filter(
      (qs) => qs.get("pql") !== null,
    );
    for (const qs of pqlCalls) {
      expect(qs.get("include_subtasks")).toBe("1");
    }
    expect(
      document.querySelector('[data-nested-subtask="i-c"]'),
    ).not.toBeNull();
  });
});
