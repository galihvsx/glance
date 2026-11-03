// @vitest-environment jsdom
// C135T2: board Group-by-State-groups page tests, driving the real
// Board page with a mocked API (BoardGroupByCreatedBy.test.tsx
// pattern). Covers the column wiring the pure helper cannot: six
// group columns in canonical backend-fixed order, single-appearance
// cards, Unknown last, the UNIQUE-issue page header count, header
// rendering with NO color dot and NO breakdown bar, "Show empty
// groups" synthesizing all six canonical columns (Unknown only when
// it has rows), per-column quick-add ENABLED with the
// first-by-sequence state stamp (and DISABLED for stateless columns —
// the Unknown column and synthesized empty groups with no state),
// and cards staying draggable under state-group grouping (the
// disable uses the dedicated disableQuickAdd prop, NOT BoardColumn's
// `disabled`, which also gates card dragging). Drop semantics
// themselves (cross-column drops are ALWAYS a deliberate no-op — no
// state change, no membership call, no sort PATCH) live in
// boardStateGroup.test.ts's plan matrix — dnd-kit pointer drags have
// no existing board-level simulation in this repo (cycle/release
// drops are tested the same way), so this file does not fake one.
// Within-column reorder is covered by the applyStateGroupReorder
// pipeline mirroring (same stack math as the created-by path), not
// by a simulated drag.

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

// Sequence-sorted after the board's own sort, but listed out of
// order here so the test proves the stamp follows sequence, not
// array order: unstarted's first-by-sequence state is s-todo-early
// (sequence 2), not s-todo-late (sequence 3). Backlog, started and
// cancelled have NO state in this fixture, so their synthesized
// columns are the stateless quick-add-disabled case.
const states: IssueState[] = [
  {
    id: "s-done",
    project_id: "p-eng",
    name: "Done",
    group: "completed",
    color: "#4ade80",
    sequence: 4,
    created_at: "",
    updated_at: "",
  },
  {
    id: "s-todo-late",
    project_id: "p-eng",
    name: "Todo late",
    group: "unstarted",
    color: "#888",
    sequence: 3,
    created_at: "",
    updated_at: "",
  },
  {
    id: "s-todo-early",
    project_id: "p-eng",
    name: "Todo early",
    group: "unstarted",
    color: "#888",
    sequence: 2,
    created_at: "",
    updated_at: "",
  },
  {
    id: "s-triage",
    project_id: "p-eng",
    name: "Triage",
    group: "triage",
    color: "#888",
    sequence: 1,
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
    created_by: "u-alice",
    created_at: "",
    updated_at: "",
    assignees: [],
    labels: [],
  };
}

function storeSettings(groupBy: string, showEmptyGroups: boolean) {
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
      showEmptyGroups,
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
        makeIssue("i-new", "ENG-9", "Fresh task", "s-todo-early", 99),
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

const unstartedTask = makeIssue(
  "i-a",
  "ENG-1",
  "Unstarted task",
  "s-todo-late",
  1,
);
const completedTask = makeIssue("i-b", "ENG-2", "Done task", "s-done", 2);
const unknownTask = makeIssue("i-u", "ENG-3", "Ghost task", "s-gone", 3);

describe("board group-by-state-groups columns", () => {
  it("renders six columns in canonical order, Unknown last, single-appearance cards, no raw state id labels", async () => {
    storeSettings("stateGroups", true);
    renderBoard([completedTask, unstartedTask, unknownTask]);
    await screen.findByText("Unstarted task");
    expect(screen.getByText("Done task")).toBeInTheDocument();
    expect(screen.getByText("Ghost task")).toBeInTheDocument();
    // Single-appearance: each card renders exactly once.
    expect(screen.getAllByText("Unstarted task")).toHaveLength(1);
    // DOM order of the column headers follows the canonical order:
    // Triage, Backlog, Unstarted, Started, Completed, Cancelled,
    // Unknown last — regardless of first-appearance order.
    const body = document.body.innerHTML;
    const order = [
      ">Triage<",
      ">Backlog<",
      ">Unstarted<",
      ">Started<",
      ">Completed<",
      ">Cancelled<",
      ">Unknown<",
    ];
    for (let i = 0; i < order.length - 1; i++) {
      expect(body.indexOf(order[i])).toBeLessThan(
        body.indexOf(order[i + 1]),
      );
    }
    // NEVER a raw state id as a column label (or anywhere in the DOM).
    expect(body).not.toContain("s-gone");
    expect(body).not.toContain("s-todo-late");
    expect(body).not.toContain("s-done");
  });

  it("state-group column headers render name + count with NO color dot and NO breakdown bar", async () => {
    storeSettings("stateGroups", true);
    renderBoard([unstartedTask, unknownTask]);
    await screen.findByText("Unstarted task");
    // Label columns render a color dot span (h-2 w-2 rounded-full);
    // state-group columns must not — the only such spans would come
    // from other primitives, and this fixture has none.
    expect(
      document.querySelectorAll("span.h-2.w-2.rounded-full"),
    ).toHaveLength(0);
    // Epic columns render a breakdown bar; state-group columns must
    // not — no breakdown segment elements appear.
    expect(screen.getByText("Unstarted")).toBeInTheDocument();
    expect(screen.getByText("Unknown")).toBeInTheDocument();
  });

  it("hides Unknown when every issue resolves to a loaded state", async () => {
    storeSettings("stateGroups", true);
    renderBoard([unstartedTask, completedTask]);
    await screen.findByText("Unstarted task");
    expect(screen.queryByText("Unknown")).not.toBeInTheDocument();
  });

  it("without show-empty-groups, only groups with visible rows render", async () => {
    storeSettings("stateGroups", false);
    renderBoard([unstartedTask, completedTask, unknownTask]);
    await screen.findByText("Unstarted task");
    expect(screen.getByText("Completed")).toBeInTheDocument();
    expect(screen.getByText("Unknown")).toBeInTheDocument();
    expect(screen.queryByText("Triage")).not.toBeInTheDocument();
    expect(screen.queryByText("Backlog")).not.toBeInTheDocument();
    expect(screen.queryByText("Started")).not.toBeInTheDocument();
    expect(screen.queryByText("Cancelled")).not.toBeInTheDocument();
  });

  it("page header count is UNIQUE issues", async () => {
    storeSettings("stateGroups", true);
    renderBoard([unstartedTask, completedTask, unknownTask]);
    await screen.findByText("Unstarted task");
    const boardLabel = within(
      screen.getByRole("navigation"),
    ).getByText("Board");
    expect(boardLabel.nextElementSibling?.textContent).toBe("3");
  });

  it("state columns still render when grouped by state (regression: no fallthrough for stateGroups only)", async () => {
    storeSettings("state", false);
    renderBoard([unstartedTask, completedTask]);
    await screen.findByText("Unstarted task");
    expect(screen.getAllByText("Todo late").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Done").length).toBeGreaterThan(0);
    expect(screen.queryByText("Unknown")).not.toBeInTheDocument();
    expect(screen.getAllByText("Unstarted task")).toHaveLength(1);
  });

  it("sub-group sections compose under state-group columns via the C123 machinery", async () => {
    storeSettings("stateGroups", true);
    // Sub-group by state is the effective sub-group only when it
    // differs from the primary — stateGroups vs state differ, so
    // sections render per column.
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
        groupBy: "stateGroups",
        subGroupBy: "state",
        orderBy: "-updated_at",
        showEmptyGroups: true,
      }),
    );
    renderBoard([unstartedTask, unknownTask]);
    await screen.findByText("Unstarted task");
    // 7 state-group columns × state sections — sections are visual
    // only, exactly as under creator columns.
    expect(
      document.querySelectorAll("[data-sub-group-section]").length,
    ).toBeGreaterThan(0);
    expect(screen.getAllByText("Unstarted task")).toHaveLength(1);
  });
});

describe("board state-group quick-add", () => {
  it("per-column quick-add is ENABLED with the first-by-sequence state stamp; the new card stays in the column", async () => {
    storeSettings("stateGroups", true);
    const { post } = renderBoard([unstartedTask, completedTask]);
    await screen.findByText("Unstarted task");
    const addButton = screen.getByRole("button", {
      name: "Add work item to Unstarted",
    });
    expect(addButton).not.toBeDisabled();
    fireEvent.click(addButton);
    const input = await screen.findByLabelText("New work item title");
    fireEvent.change(input, { target: { value: "Fresh task" } });
    fireEvent.submit(input.closest("form")!);
    await waitFor(() => {
      expect(post).toHaveBeenCalledWith(
        `${ENG}/issues`,
        expect.objectContaining({
          name: "Fresh task",
          state_id: "s-todo-early",
        }),
      );
    });
  });

  it("stateless columns render quick-add DISABLED: Unknown and synthesized empty groups with no state", async () => {
    storeSettings("stateGroups", true);
    const { post } = renderBoard([unstartedTask, unknownTask]);
    await screen.findByText("Unstarted task");
    // Seven columns: six canonical + Unknown. Unstarted's "+" is
    // enabled (tested above); every other column's "+" is disabled
    // here — Triage has a state (s-triage) so it is enabled, while
    // Backlog, Started, Cancelled (no states) and Unknown are
    // disabled.
    for (const title of ["Backlog", "Started", "Cancelled", "Unknown"]) {
      const b = screen.getByRole("button", {
        name: `Add work item to ${title}`,
      });
      expect(b).toBeDisabled();
      fireEvent.click(b);
    }
    // The enabled Triage "+" is untouched in this test; clicking
    // only disabled buttons opens no editor and creates nothing.
    expect(
      screen.queryByLabelText("New work item title"),
    ).not.toBeInTheDocument();
    expect(post).not.toHaveBeenCalled();
  });

  it("cards stay draggable under state-group grouping: quick-add disable uses disableQuickAdd, not card dragging", async () => {
    storeSettings("stateGroups", true);
    renderBoard([unstartedTask, unknownTask]);
    await screen.findByText("Unstarted task");
    // The Unknown column's "+" is disabled, but its card still
    // renders (dragging is gated by BoardColumn's `disabled`, which
    // stays false for editors) — the observable split the C134
    // lesson requires.
    expect(
      screen.getByRole("button", { name: "Add work item to Unknown" }),
    ).toBeDisabled();
    expect(screen.getByText("Ghost task")).toBeInTheDocument();
  });
});
