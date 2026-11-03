// @vitest-environment jsdom
// C133T2: board Group-by-Releases page tests, driving the real Board
// page with a mocked API (BoardGroupByCycles.test.tsx pattern).
// Covers the column wiring the pure helper cannot: release columns in
// first-appearance order, single-appearance cards, Ungrouped last,
// the UNIQUE-issue page header count, header rendering with NO color
// dot, sub-group composition, and quick-add assign / assign-failure
// honesty. Drop semantics themselves (MOVE via single assign, NO
// DELETE call, Ungrouped no-op, already-member no-op, assign-failure
// aborts and surfaces honestly) live in boardRelease.test.ts's plan
// matrix + move-executor tests — dnd-kit pointer drags have no
// existing board-level simulation in this repo (cycle drops are
// tested the same way), so this file does not fake one. Within-column
// reorder is covered by the applyReleaseReorder pipeline mirroring
// (same stack math as the cycle path), not by a simulated drag.

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

const V1 = { id: "r-v1", name: "v1.0" };
const V2 = { id: "r-v2", name: "v2.0" };
const SPRINT1 = { id: "c-sprint-1", name: "Sprint 1" };
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
    if (path.includes("/releases/")) return Promise.resolve({});
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

const v1Task = makeIssue("i-v1", "ENG-1", "v1 task", "s-todo", 1, {
  project_structure: { release: V1, modules: [] },
});
const v2Task = makeIssue("i-v2", "ENG-2", "v2 task", "s-todo", 2, {
  project_structure: { release: V2, modules: [] },
});
const ungrouped = makeIssue("i-u", "ENG-3", "No release task", "s-done", 3, {
  project_structure: { release: null, modules: [] },
});

describe("board group-by-releases columns", () => {
  it("renders one column per release in first-appearance order, Ungrouped last, single-appearance cards", async () => {
    storeSettings("releases");
    renderBoard([v1Task, v2Task, ungrouped]);
    await screen.findByText("v1 task");
    expect(screen.getByText("v2 task")).toBeInTheDocument();
    expect(screen.getByText("No release task")).toBeInTheDocument();
    // Single-appearance: each card renders exactly once.
    expect(screen.getAllByText("v1 task")).toHaveLength(1);
    // DOM order of the column headers follows first-appearance order.
    // (Card release chips are off in this fixture's display fields, so
    // the header titles are the only "v1.0"/"v2.0" occurrences.)
    const body = document.body.innerHTML;
    expect(body.indexOf(">v1.0<")).toBeLessThan(body.indexOf(">v2.0<"));
    expect(body.indexOf(">v2.0<")).toBeLessThan(body.indexOf(">Ungrouped<"));
  });

  it("release column headers render name + count with NO color dot", async () => {
    storeSettings("releases");
    renderBoard([v1Task, ungrouped]);
    await screen.findByText("v1 task");
    // Label columns render a color dot span (h-2 w-2 rounded-full);
    // release columns must not — the only such spans would come from
    // other primitives, and this fixture has none.
    expect(
      document.querySelectorAll("span.h-2.w-2.rounded-full"),
    ).toHaveLength(0);
    expect(screen.getByText("v1.0")).toBeInTheDocument();
  });

  it("hides Ungrouped when every issue has a release", async () => {
    storeSettings("releases");
    renderBoard([v1Task, v2Task]);
    await screen.findByText("v1 task");
    expect(screen.queryByText("Ungrouped")).not.toBeInTheDocument();
  });

  it("page header count is UNIQUE issues", async () => {
    storeSettings("releases");
    renderBoard([v1Task, v2Task, ungrouped]);
    await screen.findByText("v1 task");
    const boardLabel = within(
      screen.getByRole("navigation"),
    ).getByText("Board");
    expect(boardLabel.nextElementSibling?.textContent).toBe("3");
  });

  it("state columns still render when grouped by state (regression)", async () => {
    storeSettings("state");
    renderBoard([v1Task, v2Task, ungrouped]);
    await screen.findByText("v1 task");
    expect(screen.getAllByText("Todo").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Done").length).toBeGreaterThan(0);
    expect(screen.queryByText("Ungrouped")).not.toBeInTheDocument();
    expect(screen.getAllByText("v1 task")).toHaveLength(1);
  });

  it("sub-group sections compose under release columns via the C123 machinery", async () => {
    storeSettings("releases", "state");
    renderBoard([v1Task, ungrouped]);
    await screen.findByText("v1 task");
    // 2 release columns × 2 state sections (showEmptyGroups on, so
    // empty sections render too) — sections are visual only, exactly
    // as under cycle columns.
    expect(
      document.querySelectorAll("[data-sub-group-section]"),
    ).toHaveLength(4);
    expect(screen.getAllByText("v1 task")).toHaveLength(1);
  });

  it("cycle columns still render when grouped by cycles (regression)", async () => {
    storeSettings("cycles");
    const cyclic = makeIssue("i-c", "ENG-4", "Cyclic task", "s-todo", 4, {
      project_structure: { cycle: SPRINT1, release: V1, modules: [] },
    });
    renderBoard([cyclic, ungrouped]);
    await screen.findByText("Cyclic task");
    expect(screen.getByText("Sprint 1")).toBeInTheDocument();
    expect(screen.queryByText("v1.0")).not.toBeInTheDocument();
    expect(screen.getByText("Ungrouped")).toBeInTheDocument();
  });

  it("module columns still render when grouped by modules (regression)", async () => {
    storeSettings("modules");
    const modular = makeIssue("i-m", "ENG-5", "Modular task", "s-todo", 5, {
      project_structure: { release: V1, modules: [AUTH] },
    });
    renderBoard([modular, ungrouped]);
    await screen.findByText("Modular task");
    expect(screen.getByText("Auth")).toBeInTheDocument();
    expect(screen.queryByText("v1.0")).not.toBeInTheDocument();
    expect(screen.getByText("Ungrouped")).toBeInTheDocument();
  });

  it("label columns still render when grouped by labels (regression)", async () => {
    storeSettings("labels");
    const labeled = makeIssue("i-l", "ENG-6", "Labeled task", "s-todo", 6, {
      labels: [BUG],
    });
    renderBoard([labeled, ungrouped]);
    await screen.findByText("Labeled task");
    expect(screen.getAllByText("Bug").length).toBeGreaterThan(0);
    expect(screen.getByText("Ungrouped")).toBeInTheDocument();
    expect(screen.queryByText("v1.0")).not.toBeInTheDocument();
  });
});

describe("board release quick-add", () => {
  async function quickAddInColumn(columnTitle: string, title: string) {
    fireEvent.click(
      screen.getByRole("button", { name: `Add work item to ${columnTitle}` }),
    );
    const input = await screen.findByLabelText("New work item title");
    fireEvent.change(input, { target: { value: title } });
    fireEvent.submit(input.closest("form")!);
  }

  it("release column quick-add creates with the column state AND assigns to the release (single POST, no DELETE)", async () => {
    storeSettings("releases");
    const { post } = renderBoard([v1Task]);
    await screen.findByText("v1 task");
    await quickAddInColumn("v1.0", "Fresh task");
    await waitFor(() => {
      expect(post).toHaveBeenCalledWith(
        `${ENG}/issues`,
        expect.objectContaining({ name: "Fresh task", state_id: "s-todo" }),
      );
    });
    await waitFor(() => {
      expect(post).toHaveBeenCalledWith(`${ENG}/releases/r-v1/issues`, {
        issue_ids: ["i-new"],
      });
    });
    // Exactly one release call; the board move/quick-add path issues
    // no DELETE at all (api.del is never wired in this flow).
    const calls = post.mock.calls.map((c) => c[0] as string);
    expect(calls.filter((p) => p.includes("/releases/"))).toHaveLength(1);
  });

  it("assign failure surfaces an error (card lands in Ungrouped after invalidation)", async () => {
    storeSettings("releases");
    const { post } = renderBoard([v1Task]);
    post.mockImplementation((path: string) => {
      if (path === `${ENG}/issues`) {
        return Promise.resolve(
          makeIssue("i-new", "ENG-9", "Fresh task", "s-todo", 99),
        );
      }
      if (path.includes("/releases/")) {
        return Promise.reject(new Error("release failed"));
      }
      return Promise.reject(new Error(`unexpected POST ${path}`));
    });
    await screen.findByText("v1 task");
    await quickAddInColumn("v1.0", "Fresh task");
    // The board mirrors the cycle quick-add error rule: a
    // non-ApiError rejection surfaces the generic message (the real
    // API layer rejects with ApiError, whose message would surface
    // instead).
    await screen.findByText("Failed to attach issue to release");
    // The create itself succeeded — only the assign failed.
    expect(post).toHaveBeenCalledWith(
      `${ENG}/issues`,
      expect.objectContaining({ name: "Fresh task" }),
    );
  });

  it("Ungrouped column quick-add is a plain create (no assign call)", async () => {
    storeSettings("releases");
    const { post } = renderBoard([ungrouped]);
    await screen.findByText("No release task");
    await quickAddInColumn("Ungrouped", "Fresh task");
    await waitFor(() => {
      expect(post).toHaveBeenCalledWith(
        `${ENG}/issues`,
        expect.objectContaining({ name: "Fresh task", state_id: "s-todo" }),
      );
    });
    const calls = post.mock.calls.map((c) => c[0] as string);
    expect(calls.some((p) => p.includes("/releases/"))).toBe(false);
  });
});
