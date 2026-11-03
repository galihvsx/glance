// @vitest-environment jsdom
// C131T1: board Group-by-Modules page tests, driving the real Board
// page with a mocked API (BoardGroupByLabels.test.tsx pattern).
// Covers the column wiring the pure helper cannot: module columns in
// first-appearance order, multi-appearance cards, Ungrouped last,
// the UNIQUE-issue page header count, header rendering with NO color
// dot, and quick-add attach / attach-failure honesty. Drop semantics
// themselves (attach-only, no detach, Ungrouped no-op, already-member
// no-op) live in boardModule.test.ts's plan matrix — dnd-kit pointer
// drags have no existing board-level simulation in this repo (label
// drops are tested the same way), so this file does not fake one.

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

const AUTH = { id: "m-auth", name: "Auth" };
const BILLING = { id: "m-billing", name: "Billing" };
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
    if (path.includes("/modules/")) return Promise.resolve({});
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

const multi = makeIssue("i-m", "ENG-1", "Multi module task", "s-todo", 1, {
  project_structure: { modules: [AUTH, BILLING] },
});
const authOnly = makeIssue("i-a", "ENG-2", "Auth only task", "s-todo", 2, {
  project_structure: { modules: [AUTH] },
});
const ungrouped = makeIssue("i-u", "ENG-3", "No module task", "s-done", 3, {
  project_structure: { modules: [] },
});

describe("board group-by-modules columns", () => {
  it("renders one column per module in first-appearance order, Ungrouped last, multi-appearance cards", async () => {
    storeSettings("modules");
    renderBoard([multi, authOnly, ungrouped]);
    // findByText would throw on the multi-appearance card (it renders
    // twice by design), so wait on the plural query instead.
    await screen.findAllByText("Multi module task");
    // Multi-appearance: the two-module card renders in both columns.
    expect(screen.getAllByText("Multi module task")).toHaveLength(2);
    expect(screen.getByText("Auth only task")).toBeInTheDocument();
    expect(screen.getByText("No module task")).toBeInTheDocument();
    // DOM order of the column headers follows first-appearance order.
    // (Card module chips are off in this fixture's display fields, so
    // the header titles are the only "Auth"/"Billing" occurrences.)
    const body = document.body.innerHTML;
    expect(body.indexOf(">Auth<")).toBeLessThan(body.indexOf(">Billing<"));
    expect(body.indexOf(">Billing<")).toBeLessThan(
      body.indexOf(">Ungrouped<"),
    );
  });

  it("module column headers render name + count with NO color dot", async () => {
    storeSettings("modules");
    renderBoard([authOnly, ungrouped]);
    await screen.findByText("Auth only task");
    // Label columns render a color dot span (h-2 w-2 rounded-full);
    // module columns must not — the only such spans would come from
    // other primitives, and this fixture has none.
    expect(
      document.querySelectorAll("span.h-2.w-2.rounded-full"),
    ).toHaveLength(0);
    expect(screen.getByText("Auth")).toBeInTheDocument();
  });

  it("hides Ungrouped when every issue has a module", async () => {
    storeSettings("modules");
    renderBoard([multi, authOnly]);
    await screen.findByText("Auth only task");
    expect(screen.queryByText("Ungrouped")).not.toBeInTheDocument();
  });

  it("page header count is UNIQUE issues, not column memberships", async () => {
    storeSettings("modules");
    renderBoard([multi, authOnly, ungrouped]);
    await screen.findAllByText("Multi module task");
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
    renderBoard([multi, authOnly, ungrouped]);
    await screen.findByText("Multi module task");
    // Column titles share text with per-card state chips, so count
    // occurrences rather than asserting uniqueness.
    expect(screen.getAllByText("Todo").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Done").length).toBeGreaterThan(0);
    expect(screen.queryByText("Ungrouped")).not.toBeInTheDocument();
    // No multi-appearance under state grouping: each card renders once.
    expect(screen.getAllByText("Multi module task")).toHaveLength(1);
  });

  it("sub-group sections compose under module columns via the C123 machinery", async () => {
    storeSettings("modules", "state");
    renderBoard([multi, ungrouped]);
    await screen.findAllByText("Multi module task");
    // 3 module columns × 2 state sections (showEmptyGroups on, so
    // empty sections render too) — sections are visual only, exactly
    // as under label columns.
    expect(
      document.querySelectorAll("[data-sub-group-section]"),
    ).toHaveLength(6);
    // Multi-appearance survives sub-grouping: the card still renders
    // once per module column.
    expect(screen.getAllByText("Multi module task")).toHaveLength(2);
  });

  it("label columns still render when grouped by labels (regression)", async () => {
    storeSettings("labels");
    const labeled = makeIssue("i-l", "ENG-4", "Labeled task", "s-todo", 4, {
      labels: [BUG],
    });
    renderBoard([labeled, ungrouped]);
    await screen.findByText("Labeled task");
    // "Bug" appears both as the column header and as the card's label
    // chip, so assert presence plurally rather than uniquely.
    expect(screen.getAllByText("Bug").length).toBeGreaterThan(0);
    expect(screen.getByText("Ungrouped")).toBeInTheDocument();
    expect(screen.queryByText("Auth")).not.toBeInTheDocument();
  });
});

describe("board module quick-add", () => {
  async function quickAddInColumn(columnTitle: string, title: string) {
    fireEvent.click(
      screen.getByRole("button", { name: `Add work item to ${columnTitle}` }),
    );
    const input = await screen.findByLabelText("New work item title");
    fireEvent.change(input, { target: { value: title } });
    fireEvent.submit(input.closest("form")!);
  }

  it("module column quick-add creates with the column state AND attaches to the module", async () => {
    storeSettings("modules");
    const { post } = renderBoard([authOnly]);
    await screen.findByText("Auth only task");
    await quickAddInColumn("Auth", "Fresh task");
    await waitFor(() => {
      expect(post).toHaveBeenCalledWith(
        `${ENG}/issues`,
        expect.objectContaining({ name: "Fresh task", state_id: "s-todo" }),
      );
    });
    await waitFor(() => {
      expect(post).toHaveBeenCalledWith(`${ENG}/modules/m-auth/issues`, {
        issue_ids: ["i-new"],
      });
    });
    // Attach-only: exactly one module call, no detach, no state PATCH
    // for the new issue.
    const calls = post.mock.calls.map((c) => c[0] as string);
    expect(calls.filter((p) => p.includes("/modules/"))).toHaveLength(1);
  });

  it("attach failure surfaces an error (card lands in Ungrouped after invalidation)", async () => {
    storeSettings("modules");
    const { post } = renderBoard([authOnly]);
    post.mockImplementation((path: string) => {
      if (path === `${ENG}/issues`) {
        return Promise.resolve(
          makeIssue("i-new", "ENG-9", "Fresh task", "s-todo", 99),
        );
      }
      if (path.includes("/modules/")) {
        return Promise.reject(new Error("module failed"));
      }
      return Promise.reject(new Error(`unexpected POST ${path}`));
    });
    await screen.findByText("Auth only task");
    await quickAddInColumn("Auth", "Fresh task");
    // The board mirrors the label quick-add error rule: a
    // non-ApiError rejection surfaces the generic message (the real
    // API layer rejects with ApiError, whose message would surface
    // instead).
    await screen.findByText("Failed to attach issue to module");
    // The create itself succeeded — only the attach failed.
    expect(post).toHaveBeenCalledWith(
      `${ENG}/issues`,
      expect.objectContaining({ name: "Fresh task" }),
    );
  });

  it("Ungrouped column quick-add is a plain create (no attach call)", async () => {
    storeSettings("modules");
    const { post } = renderBoard([ungrouped]);
    await screen.findByText("No module task");
    await quickAddInColumn("Ungrouped", "Fresh task");
    await waitFor(() => {
      expect(post).toHaveBeenCalledWith(
        `${ENG}/issues`,
        expect.objectContaining({ name: "Fresh task", state_id: "s-todo" }),
      );
    });
    const calls = post.mock.calls.map((c) => c[0] as string);
    expect(calls.some((p) => p.includes("/modules/"))).toBe(false);
  });
});
