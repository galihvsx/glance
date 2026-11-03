// @vitest-environment jsdom
// C134T2: board Group-by-Created-by page tests, driving the real Board
// page with a mocked API (BoardGroupByReleases.test.tsx pattern).
// Covers the column wiring the pure helper cannot: creator columns in
// first-appearance order, single-appearance cards, Unknown last, the
// UNIQUE-issue page header count, header rendering with NO color dot,
// sub-group composition, the members-pending gate (no Unknown flash,
// no raw id labels), members-error honesty (unresolved creators
// collapse to Unknown), and per-column quick-add DISABLED (zero
// functioning quick-adds). Drop semantics themselves (cross-column
// drops are ALWAYS a deliberate no-op — no state change, no membership
// call, no sort PATCH) live in boardCreatedBy.test.ts's plan matrix —
// dnd-kit pointer drags have no existing board-level simulation in
// this repo (cycle/release drops are tested the same way), so this
// file does not fake one. Within-column reorder is covered by the
// applyCreatedByReorder pipeline mirroring (same stack math as the
// release path, whose stackDropSortOrder/midpoint inputs are covered
// by sortOrder.test.ts and whose column lookup is covered by
// boardCreatedBy.test.ts's columnKey mapping), not by a simulated drag.
// Cards stay draggable under created-by grouping: the quick-add
// disable uses a dedicated disableQuickAdd prop, NOT BoardColumn's
// `disabled` (which also gates card dragging). That prop split is
// asserted below through its observable effect: quick-add controls
// are disabled while the columns still render their cards.

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
import type { Issue, IssueState, Member } from "../lib/types";
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

const MEMBERS: Member[] = [
  { id: "u-alice", name: "Alice", email: "alice@x.com", role: 20 },
  { id: "u-bob", name: null, email: "bob@x.com", role: 20 },
];

function makeIssue(
  id: string,
  displayId: string,
  name: string,
  stateId: string,
  sortOrder: number,
  createdBy: string,
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
    created_by: createdBy,
    created_at: "",
    updated_at: "",
    assignees: [],
    labels: [],
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

function renderBoard(
  issues: Issue[],
  opts: { membersError?: boolean; membersDeferred?: boolean } = {},
) {
  let resolveMembers: ((members: Member[]) => void) | null = null;
  const membersPromise = opts.membersDeferred
    ? new Promise<Member[]>((resolve) => {
        resolveMembers = resolve;
      })
    : null;
  const get = vi.spyOn(api, "get").mockImplementation((path: string) => {
    if (path === "/api/v1/workspaces/acme")
      return Promise.resolve({ role: 15 });
    if (path === ENG) return Promise.resolve({ id: "p-eng", name: "ENG" });
    if (path.endsWith("/states")) return Promise.resolve({ states });
    if (path.endsWith("/workflow"))
      return Promise.resolve({ states, transitions: [] });
    if (path.endsWith("/members")) {
      if (opts.membersError)
        return Promise.reject(new Error("members failed"));
      if (membersPromise) return membersPromise.then((m) => ({ members: m }));
      return Promise.resolve({ members: MEMBERS });
    }
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
        makeIssue("i-new", "ENG-9", "Fresh task", "s-todo", 99, "u-alice"),
      );
    }
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
  return {
    get,
    post,
    resolveMembers: (members: Member[]) => resolveMembers?.(members),
  };
}

beforeEach(() => {
  stubMatchMedia(false);
  localStorage.clear();
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const aliceTask = makeIssue("i-a", "ENG-1", "Alice task", "s-todo", 1, "u-alice");
const bobTask = makeIssue("i-b", "ENG-2", "Bob task", "s-todo", 2, "u-bob");
const unknownTask = makeIssue(
  "i-u",
  "ENG-3",
  "Ghost task",
  "s-done",
  3,
  "u-gone",
);

describe("board group-by-created-by columns", () => {
  it("renders one column per creator in first-appearance order, Unknown last, single-appearance cards, no raw id labels", async () => {
    storeSettings("createdBy");
    renderBoard([aliceTask, bobTask, unknownTask]);
    await screen.findByText("Alice task");
    expect(screen.getByText("Bob task")).toBeInTheDocument();
    expect(screen.getByText("Ghost task")).toBeInTheDocument();
    // Single-appearance: each card renders exactly once.
    expect(screen.getAllByText("Alice task")).toHaveLength(1);
    // DOM order of the column headers follows first-appearance order:
    // Alice (name), bob@x.com (name ?? email fallback), Unknown last.
    const body = document.body.innerHTML;
    expect(body.indexOf(">Alice<")).toBeLessThan(body.indexOf(">bob@x.com<"));
    expect(body.indexOf(">bob@x.com<")).toBeLessThan(
      body.indexOf(">Unknown<"),
    );
    // NEVER a raw user id as a column label (or anywhere in the DOM).
    expect(body).not.toContain("u-alice");
    expect(body).not.toContain("u-bob");
    expect(body).not.toContain("u-gone");
  });

  it("creator column headers render name + count with NO color dot", async () => {
    storeSettings("createdBy");
    renderBoard([aliceTask, unknownTask]);
    await screen.findByText("Alice task");
    // Label columns render a color dot span (h-2 w-2 rounded-full);
    // creator columns must not — the only such spans would come from
    // other primitives, and this fixture has none.
    expect(
      document.querySelectorAll("span.h-2.w-2.rounded-full"),
    ).toHaveLength(0);
    expect(screen.getByText("Alice")).toBeInTheDocument();
    expect(screen.getByText("Unknown")).toBeInTheDocument();
  });

  it("hides Unknown when every creator resolves to a member", async () => {
    storeSettings("createdBy");
    renderBoard([aliceTask, bobTask]);
    await screen.findByText("Alice task");
    expect(screen.queryByText("Unknown")).not.toBeInTheDocument();
  });

  it("page header count is UNIQUE issues", async () => {
    storeSettings("createdBy");
    renderBoard([aliceTask, bobTask, unknownTask]);
    await screen.findByText("Alice task");
    const boardLabel = within(
      screen.getByRole("navigation"),
    ).getByText("Board");
    expect(boardLabel.nextElementSibling?.textContent).toBe("3");
  });

  it("state columns still render when grouped by state (regression: no more state fallthrough for createdBy only)", async () => {
    storeSettings("state");
    renderBoard([aliceTask, bobTask, unknownTask]);
    await screen.findByText("Alice task");
    expect(screen.getAllByText("Todo").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Done").length).toBeGreaterThan(0);
    expect(screen.queryByText("Unknown")).not.toBeInTheDocument();
    expect(screen.getAllByText("Alice task")).toHaveLength(1);
  });

  it("sub-group sections compose under creator columns via the C123 machinery", async () => {
    storeSettings("createdBy", "state");
    renderBoard([aliceTask, unknownTask]);
    await screen.findByText("Alice task");
    // 2 creator columns × 2 state sections (showEmptyGroups on, so
    // empty sections render too) — sections are visual only, exactly
    // as under release columns.
    expect(
      document.querySelectorAll("[data-sub-group-section]"),
    ).toHaveLength(4);
    expect(screen.getAllByText("Alice task")).toHaveLength(1);
  });

  it("release columns still render when grouped by releases (regression)", async () => {
    storeSettings("releases");
    const v1Task = {
      ...aliceTask,
      id: "i-v1",
      name: "v1 task",
      project_structure: {
        release: { id: "r-v1", name: "v1.0" },
        modules: [],
      },
    } as Issue;
    renderBoard([v1Task, unknownTask]);
    await screen.findByText("v1 task");
    expect(screen.getByText("v1.0")).toBeInTheDocument();
    expect(screen.queryByText("Alice")).not.toBeInTheDocument();
  });

  it("while the members query is pending, NO column renders as Unknown and no raw id label appears", async () => {
    storeSettings("createdBy");
    const { resolveMembers } = renderBoard(
      [aliceTask, bobTask, unknownTask],
      { membersDeferred: true },
    );
    // The page header renders regardless of column data; the issues
    // query resolves immediately while members stays pending.
    expect(
      within(screen.getByRole("navigation")).getByText("Board"),
    ).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.queryByText("Unknown")).not.toBeInTheDocument();
      expect(screen.queryByText("Alice")).not.toBeInTheDocument();
      expect(document.body.innerHTML).not.toContain("u-alice");
      expect(document.body.innerHTML).not.toContain("u-gone");
    });
    // Resolving members then renders the real columns (no flash was
    // ever shown — the first creator paint is the resolved one).
    resolveMembers(MEMBERS);
    await screen.findByText("Alice task");
    expect(screen.getByText("Alice")).toBeInTheDocument();
    expect(screen.getByText("Unknown")).toBeInTheDocument();
  });

  it("on members error, unresolved creators honestly collapse to Unknown", async () => {
    storeSettings("createdBy");
    renderBoard([aliceTask, bobTask, unknownTask], { membersError: true });
    await screen.findByText("Alice task");
    // With no members data, every creator is unresolved: one Unknown
    // column holds all three cards, and no raw id is ever a label.
    expect(screen.getByText("Unknown")).toBeInTheDocument();
    expect(screen.queryByText("Alice")).not.toBeInTheDocument();
    expect(document.body.innerHTML).not.toContain("u-alice");
    expect(screen.getAllByText("Bob task")).toHaveLength(1);
  });
});

describe("board created-by quick-add", () => {
  it("zero per-column quick-adds: every column '+' tool and QuickAdd row is disabled, clicking opens no editor, nothing is created", async () => {
    storeSettings("createdBy");
    const { post } = renderBoard([aliceTask, bobTask]);
    await screen.findByText("Alice task");
    const addButtons = screen.getAllByRole("button", {
      name: /^Add work item to /,
    });
    expect(addButtons).toHaveLength(2);
    for (const b of addButtons) {
      expect(b).toBeDisabled();
      fireEvent.click(b);
    }
    // The QuickAdd idle rows are disabled too.
    const inlineButtons = screen.getAllByRole("button", {
      name: "Add new work item inline",
    });
    expect(inlineButtons).toHaveLength(2);
    for (const b of inlineButtons) {
      expect(b).toBeDisabled();
      fireEvent.click(b);
    }
    // No editor opened anywhere, and no create was attempted.
    expect(
      screen.queryByLabelText("New work item title"),
    ).not.toBeInTheDocument();
    expect(post).not.toHaveBeenCalled();
  });

  it("state-column quick-add still works when grouped by state (regression: disable is created-by-only)", async () => {
    storeSettings("state");
    const { post } = renderBoard([aliceTask]);
    await screen.findByText("Alice task");
    fireEvent.click(
      screen.getByRole("button", { name: "Add work item to Todo" }),
    );
    const input = await screen.findByLabelText("New work item title");
    fireEvent.change(input, { target: { value: "Fresh task" } });
    fireEvent.submit(input.closest("form")!);
    await waitFor(() => {
      expect(post).toHaveBeenCalledWith(
        `${ENG}/issues`,
        expect.objectContaining({ name: "Fresh task", state_id: "s-todo" }),
      );
    });
  });
});
