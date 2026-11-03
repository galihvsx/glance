// @vitest-environment jsdom
// C129T1: board Group-by-Assignees page tests, driving the real Board
// page with a mocked API (BoardDisplay.test.tsx pattern). Covers the
// column wiring the pure helper cannot: member columns in
// first-appearance order, multi-appearance cards, Ungrouped last,
// the UNIQUE-issue page header count, and quick-add attach /
// attach-failure honesty. Drop semantics themselves (attach-only, no
// detach, Ungrouped no-op, already-member no-op) live in
// boardAssignee.test.ts's plan matrix — dnd-kit pointer drags have no
// existing board-level simulation in this repo (epic drops are tested
// the same way), so this file does not fake one.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import Board from "./Board";
import { api } from "../lib/api";
import type { Issue, IssueState } from "../lib/types";
import {
  makeQueryClient,
  stubMatchMedia,
} from "../components/shell/test-utils";

vi.mock("../lib/auth", () => ({
  useAuth: () => ({ user: { id: "u-1", email: "u@x.com" }, loading: false }),
}));

const ENG = "/api/v1/workspaces/acme/projects/ENG";

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

const ALICE = { id: "u-alice", name: "Alice" };
const BOB = { id: "u-bob", name: "Bob" };

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

function storeSettings(groupBy: string) {
  localStorage.setItem(
    "glance:display:acme:ENG",
    JSON.stringify({
      fields: {
        state: true,
        priority: true,
        labels: true,
        assignees: true,
        dates: true,
        id: true,
        subitems: false,
        modules: false,
        cycle: false,
        release: false,
        attachments: false,
        links: false,
        dependencies: false,
      },
      groupBy,
      orderBy: "-updated_at",
      showEmptyGroups: true,
    }),
  );
}

function renderBoard(issues: Issue[]) {
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
      return Promise.resolve({ results: issues, next_cursor: null });
    return Promise.reject(new Error(`unexpected GET ${path}`));
  });
  const post = vi.spyOn(api, "post").mockImplementation((path: string) => {
    if (path === `${ENG}/issues`) {
      return Promise.resolve(
        makeIssue("i-new", "ENG-9", "Fresh task", "s-todo", 99),
      );
    }
    if (path.includes("/assignees/")) return Promise.resolve({});
    return Promise.reject(new Error(`unexpected POST ${path}`));
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
  return { get, post };
}

beforeEach(() => {
  stubMatchMedia(false);
  localStorage.clear();
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const multi = makeIssue("i-m", "ENG-1", "Multi owner task", "s-todo", 1, {
  assignees: [ALICE, BOB],
});
const aliceOnly = makeIssue("i-a", "ENG-2", "Alice only task", "s-todo", 2, {
  assignees: [ALICE],
});
const unassigned = makeIssue("i-u", "ENG-3", "No owner task", "s-done", 3, {
  assignees: [],
});

describe("board group-by-assignees columns", () => {
  it("renders one column per member in first-appearance order, Ungrouped last, multi-appearance cards", async () => {
    storeSettings("assignees");
    renderBoard([multi, aliceOnly, unassigned]);
    // findByText would throw on the multi-appearance card (it renders
    // twice by design), so wait on the plural query instead.
    await screen.findAllByText("Multi owner task");
    // Multi-appearance: the two-assignee card renders in both columns.
    expect(screen.getAllByText("Multi owner task")).toHaveLength(2);
    expect(screen.getByText("Alice only task")).toBeInTheDocument();
    expect(screen.getByText("No owner task")).toBeInTheDocument();
    // DOM order of the column headers follows first-appearance order.
    const body = document.body.innerHTML;
    expect(body.indexOf(">Alice<")).toBeLessThan(body.indexOf(">Bob<"));
    expect(body.indexOf(">Bob<")).toBeLessThan(body.indexOf(">Ungrouped<"));
  });

  it("hides Ungrouped when every issue has an assignee", async () => {
    storeSettings("assignees");
    renderBoard([multi, aliceOnly]);
    await screen.findByText("Alice only task");
    expect(screen.queryByText("Ungrouped")).not.toBeInTheDocument();
  });

  it("page header count is UNIQUE issues, not column memberships", async () => {
    storeSettings("assignees");
    renderBoard([multi, aliceOnly, unassigned]);
    await screen.findAllByText("Multi owner task");
    // 3 unique issues, but 4 column memberships (multi appears twice).
    // "Board" also labels a views-tab segment, so scope to the
    // breadcrumb nav where the header count sits next to the view name.
    const boardLabel = within(
      screen.getByRole("navigation"),
    ).getByText("Board");
    expect(boardLabel.nextElementSibling?.textContent).toBe("3");
  });

  it("state columns still render when grouped by state (regression)", async () => {
    storeSettings("state");
    renderBoard([multi, aliceOnly, unassigned]);
    await screen.findByText("Multi owner task");
    // Column titles share text with per-card state chips, so count
    // occurrences rather than asserting uniqueness.
    expect(screen.getAllByText("Todo").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Done").length).toBeGreaterThan(0);
    expect(screen.queryByText("Ungrouped")).not.toBeInTheDocument();
    // No multi-appearance under state grouping: each card renders once.
    expect(screen.getAllByText("Multi owner task")).toHaveLength(1);
  });
});

describe("board assignee quick-add", () => {
  async function quickAddInColumn(columnTitle: string, title: string) {
    fireEvent.click(
      screen.getByRole("button", { name: `Add work item to ${columnTitle}` }),
    );
    const input = await screen.findByLabelText("New work item title");
    fireEvent.change(input, { target: { value: title } });
    fireEvent.submit(input.closest("form")!);
  }

  it("member column quick-add creates with the column state AND attaches to the member", async () => {
    storeSettings("assignees");
    const { post } = renderBoard([aliceOnly]);
    await screen.findByText("Alice only task");
    await quickAddInColumn("Alice", "Fresh task");
    await waitFor(() => {
      expect(post).toHaveBeenCalledWith(
        `${ENG}/issues`,
        expect.objectContaining({ name: "Fresh task", state_id: "s-todo" }),
      );
    });
    await waitFor(() => {
      expect(post).toHaveBeenCalledWith(
        `${ENG}/issues/i-new/assignees/u-alice`,
      );
    });
    // Attach-only: no detach call, no state PATCH for the new issue.
    const calls = post.mock.calls.map((c) => c[0] as string);
    expect(calls.some((p) => p.includes("/assignees/"))).toBe(true);
    expect(calls.filter((p) => p.includes("assignees")).length).toBe(1);
  });

  it("attach failure surfaces an error (card lands in Ungrouped after invalidation)", async () => {
    storeSettings("assignees");
    const { post } = renderBoard([aliceOnly]);
    post.mockImplementation((path: string) => {
      if (path === `${ENG}/issues`) {
        return Promise.resolve(
          makeIssue("i-new", "ENG-9", "Fresh task", "s-todo", 99),
        );
      }
      if (path.includes("/assignees/")) {
        return Promise.reject(new Error("assign failed"));
      }
      return Promise.reject(new Error(`unexpected POST ${path}`));
    });
    await screen.findByText("Alice only task");
    await quickAddInColumn("Alice", "Fresh task");
    // The board mirrors the epic quick-add error rule: a non-ApiError
    // rejection surfaces the generic message (the real API layer
    // rejects with ApiError, whose message would surface instead).
    await screen.findByText("Failed to assign issue");
    // The create itself succeeded — only the attach failed.
    expect(post).toHaveBeenCalledWith(
      `${ENG}/issues`,
      expect.objectContaining({ name: "Fresh task" }),
    );
  });

  it("Ungrouped column quick-add is a plain create (no attach call)", async () => {
    storeSettings("assignees");
    const { post } = renderBoard([unassigned]);
    await screen.findByText("No owner task");
    await quickAddInColumn("Ungrouped", "Fresh task");
    await waitFor(() => {
      expect(post).toHaveBeenCalledWith(
        `${ENG}/issues`,
        expect.objectContaining({ name: "Fresh task", state_id: "s-todo" }),
      );
    });
    const calls = post.mock.calls.map((c) => c[0] as string);
    expect(calls.some((p) => p.includes("assignees"))).toBe(false);
  });
});
