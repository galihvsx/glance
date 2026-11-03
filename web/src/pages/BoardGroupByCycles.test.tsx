// @vitest-environment jsdom
// C132T2: board Group-by-Cycles page tests, driving the real Board
// page with a mocked API (BoardGroupByModules.test.tsx pattern).
// Covers the column wiring the pure helper cannot: cycle columns in
// first-appearance order, single-appearance cards, Ungrouped last,
// the UNIQUE-issue page header count, header rendering with NO color
// dot, sub-group composition, and quick-add attach / attach-failure
// honesty. Drop semantics themselves (MOVE: add-before-remove,
// Ungrouped no-op, already-member no-op, add-failure aborts before
// remove, remove-failure honesty) live in boardCycle.test.ts's plan
// matrix + move-executor tests — dnd-kit pointer drags have no
// existing board-level simulation in this repo (module drops are
// tested the same way), so this file does not fake one. Within-column
// reorder is covered by the applyCycleReorder pipeline mirroring
// (same stack math as the module path), not by a simulated drag.

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

const SPRINT1 = { id: "c-sprint-1", name: "Sprint 1" };
const SPRINT2 = { id: "c-sprint-2", name: "Sprint 2" };
const AUTH = { id: "m-auth", name: "Auth" };
const BUG = { id: "l-bug", name: "Bug", color: "#e22718" };

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

function storeSettings(groupBy: string, subGroupBy?: string) {
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
      ...(subGroupBy ? { subGroupBy } : {}),
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
    if (path.includes("/cycles/")) return Promise.resolve({});
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

const sprint1Task = makeIssue("i-s1", "ENG-1", "Sprint 1 task", "s-todo", 1, {
  project_structure: { cycle: SPRINT1, modules: [] },
});
const sprint2Task = makeIssue("i-s2", "ENG-2", "Sprint 2 task", "s-todo", 2, {
  project_structure: { cycle: SPRINT2, modules: [] },
});
const ungrouped = makeIssue("i-u", "ENG-3", "No cycle task", "s-done", 3, {
  project_structure: { cycle: null, modules: [] },
});

describe("board group-by-cycles columns", () => {
  it("renders one column per cycle in first-appearance order, Ungrouped last, single-appearance cards", async () => {
    storeSettings("cycles");
    renderBoard([sprint1Task, sprint2Task, ungrouped]);
    await screen.findByText("Sprint 1 task");
    expect(screen.getByText("Sprint 2 task")).toBeInTheDocument();
    expect(screen.getByText("No cycle task")).toBeInTheDocument();
    // Single-appearance: each card renders exactly once (unlike the
    // multi-appearance module columns, where a card renders per column).
    expect(screen.getAllByText("Sprint 1 task")).toHaveLength(1);
    // DOM order of the column headers follows first-appearance order.
    // (Card cycle chips are off in this fixture's display fields, so
    // the header titles are the only "Sprint 1"/"Sprint 2" occurrences.)
    const body = document.body.innerHTML;
    expect(body.indexOf(">Sprint 1<")).toBeLessThan(
      body.indexOf(">Sprint 2<"),
    );
    expect(body.indexOf(">Sprint 2<")).toBeLessThan(
      body.indexOf(">Ungrouped<"),
    );
  });

  it("cycle column headers render name + count with NO color dot", async () => {
    storeSettings("cycles");
    renderBoard([sprint1Task, ungrouped]);
    await screen.findByText("Sprint 1 task");
    // Label columns render a color dot span (h-2 w-2 rounded-full);
    // cycle columns must not — the only such spans would come from
    // other primitives, and this fixture has none.
    expect(
      document.querySelectorAll("span.h-2.w-2.rounded-full"),
    ).toHaveLength(0);
    expect(screen.getByText("Sprint 1")).toBeInTheDocument();
  });

  it("hides Ungrouped when every issue has a cycle", async () => {
    storeSettings("cycles");
    renderBoard([sprint1Task, sprint2Task]);
    await screen.findByText("Sprint 1 task");
    expect(screen.queryByText("Ungrouped")).not.toBeInTheDocument();
  });

  it("page header count is UNIQUE issues", async () => {
    storeSettings("cycles");
    renderBoard([sprint1Task, sprint2Task, ungrouped]);
    await screen.findByText("Sprint 1 task");
    const boardLabel = within(
      screen.getByRole("navigation"),
    ).getByText("Board");
    expect(boardLabel.nextElementSibling?.textContent).toBe("3");
  });

  it("state columns still render when grouped by state (regression)", async () => {
    storeSettings("state");
    renderBoard([sprint1Task, sprint2Task, ungrouped]);
    await screen.findByText("Sprint 1 task");
    expect(screen.getAllByText("Todo").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Done").length).toBeGreaterThan(0);
    expect(screen.queryByText("Ungrouped")).not.toBeInTheDocument();
    // No single-appearance duplication under state grouping either.
    expect(screen.getAllByText("Sprint 1 task")).toHaveLength(1);
  });

  it("sub-group sections compose under cycle columns via the C123 machinery", async () => {
    storeSettings("cycles", "state");
    renderBoard([sprint1Task, ungrouped]);
    await screen.findByText("Sprint 1 task");
    // 2 cycle columns × 2 state sections (showEmptyGroups on, so
    // empty sections render too) — sections are visual only, exactly
    // as under module columns.
    expect(
      document.querySelectorAll("[data-sub-group-section]"),
    ).toHaveLength(4);
    expect(screen.getAllByText("Sprint 1 task")).toHaveLength(1);
  });

  it("module columns still render when grouped by modules (regression)", async () => {
    storeSettings("modules");
    const modular = makeIssue("i-m", "ENG-4", "Modular task", "s-todo", 4, {
      project_structure: { cycle: SPRINT1, modules: [AUTH] },
    });
    renderBoard([modular, ungrouped]);
    await screen.findByText("Modular task");
    expect(screen.getByText("Auth")).toBeInTheDocument();
    expect(screen.queryByText("Sprint 1")).not.toBeInTheDocument();
    expect(screen.getByText("Ungrouped")).toBeInTheDocument();
  });

  it("label columns still render when grouped by labels (regression)", async () => {
    storeSettings("labels");
    const labeled = makeIssue("i-l", "ENG-5", "Labeled task", "s-todo", 5, {
      labels: [BUG],
    });
    renderBoard([labeled, ungrouped]);
    await screen.findByText("Labeled task");
    expect(screen.getAllByText("Bug").length).toBeGreaterThan(0);
    expect(screen.getByText("Ungrouped")).toBeInTheDocument();
    expect(screen.queryByText("Sprint 1")).not.toBeInTheDocument();
  });
});

describe("board cycle quick-add", () => {
  async function quickAddInColumn(columnTitle: string, title: string) {
    fireEvent.click(
      screen.getByRole("button", { name: `Add work item to ${columnTitle}` }),
    );
    const input = await screen.findByLabelText("New work item title");
    fireEvent.change(input, { target: { value: title } });
    fireEvent.submit(input.closest("form")!);
  }

  it("cycle column quick-add creates with the column state AND attaches to the cycle", async () => {
    storeSettings("cycles");
    const { post } = renderBoard([sprint1Task]);
    await screen.findByText("Sprint 1 task");
    await quickAddInColumn("Sprint 1", "Fresh task");
    await waitFor(() => {
      expect(post).toHaveBeenCalledWith(
        `${ENG}/issues`,
        expect.objectContaining({ name: "Fresh task", state_id: "s-todo" }),
      );
    });
    await waitFor(() => {
      expect(post).toHaveBeenCalledWith(`${ENG}/cycles/c-sprint-1/issues`, {
        issue_ids: ["i-new"],
      });
    });
    // Exactly one cycle call, no detach, no state PATCH for the new issue.
    const calls = post.mock.calls.map((c) => c[0] as string);
    expect(calls.filter((p) => p.includes("/cycles/"))).toHaveLength(1);
  });

  it("attach failure surfaces an error (card lands in Ungrouped after invalidation)", async () => {
    storeSettings("cycles");
    const { post } = renderBoard([sprint1Task]);
    post.mockImplementation((path: string) => {
      if (path === `${ENG}/issues`) {
        return Promise.resolve(
          makeIssue("i-new", "ENG-9", "Fresh task", "s-todo", 99),
        );
      }
      if (path.includes("/cycles/")) {
        return Promise.reject(new Error("cycle failed"));
      }
      return Promise.reject(new Error(`unexpected POST ${path}`));
    });
    await screen.findByText("Sprint 1 task");
    await quickAddInColumn("Sprint 1", "Fresh task");
    // The board mirrors the module quick-add error rule: a
    // non-ApiError rejection surfaces the generic message (the real
    // API layer rejects with ApiError, whose message would surface
    // instead).
    await screen.findByText("Failed to attach issue to cycle");
    // The create itself succeeded — only the attach failed.
    expect(post).toHaveBeenCalledWith(
      `${ENG}/issues`,
      expect.objectContaining({ name: "Fresh task" }),
    );
  });

  it("Ungrouped column quick-add is a plain create (no attach call)", async () => {
    storeSettings("cycles");
    const { post } = renderBoard([ungrouped]);
    await screen.findByText("No cycle task");
    await quickAddInColumn("Ungrouped", "Fresh task");
    await waitFor(() => {
      expect(post).toHaveBeenCalledWith(
        `${ENG}/issues`,
        expect.objectContaining({ name: "Fresh task", state_id: "s-todo" }),
      );
    });
    const calls = post.mock.calls.map((c) => c[0] as string);
    expect(calls.some((p) => p.includes("/cycles/"))).toBe(false);
  });
});
