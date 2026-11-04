// @vitest-environment jsdom
// C142T1: nested subtask drag at the page level — the grip appears
// on nested rows ONLY when the nested gate holds (showSubWorkItems
// on + >= 2 rows + canEdit), sits inside the nested block outside
// the card face div, and never appears for a single-row stack, with
// the toggle off, or for a guest (canEdit false). The live drag
// itself is NOT proven here (mocked tests cannot prove a dnd-kit
// drop landing); the drop pipeline is covered by
// boardNestedReorder.test.ts, and the live browser drag remains the
// final verifier.
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

function childRow(id: string, title: string, sortOrder: number): SubtaskRow {
  return {
    id,
    display_id: id.toUpperCase(),
    title,
    state_id: "s-done",
    priority: 4,
    assignee_ids: [],
    sort_order: sortOrder,
  };
}

// Parent with three nested children (draggable stack) and a second
// parent with a single child (not draggable: a one-row stack).
const parent3 = makeIssue("i-p3", "ENG-1", "Parent three", "s-todo", 1, {
  subtasks: [
    childRow("i-c1", "Child one", 1024),
    childRow("i-c2", "Child two", 2048),
    childRow("i-c3", "Child three", 3072),
  ],
});
const parent1 = makeIssue("i-p1", "ENG-2", "Parent one", "s-todo", 2, {
  subtasks: [childRow("i-solo", "Solo child", 1024)],
});
const payload = [parent3, parent1];

function renderBoard(role = 15) {
  const get = vi.spyOn(api, "get").mockImplementation((path: string) => {
    if (path === "/api/v1/workspaces/acme")
      return Promise.resolve({ role });
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

describe("board nested drag grips (C142T1)", () => {
  it("renders a grip on each row of a >= 2 stack when toggle on + canEdit", async () => {
    renderBoard();
    await screen.findByText("Parent three");
    for (const displayId of ["I-C1", "I-C2", "I-C3"]) {
      expect(
        screen.getByRole("button", {
          name: `Drag ${displayId} to reorder subtask`,
        }),
      ).not.toBeNull();
    }
    // The grip sits inside the nested block, outside the card face.
    const grip = screen.getByRole("button", {
      name: "Drag I-C1 to reorder subtask",
    });
    expect(grip.closest("[data-nested-subtask]")).not.toBeNull();
    expect(grip.closest("[data-board-card]")).toBeNull();
  });

  it("renders no grip for a single-row stack", async () => {
    renderBoard();
    await screen.findByText("Parent one");
    expect(document.querySelector('[data-nested-subtask="i-solo"]')).not.toBeNull();
    expect(
      screen.queryByRole("button", {
        name: "Drag I-SOLO to reorder subtask",
      }),
    ).toBeNull();
  });

  it("renders no grip with the toggle off", async () => {
    storeSettings(false);
    renderBoard();
    await screen.findByText("Parent three");
    expect(document.querySelector("[data-nested-subtask]")).toBeNull();
    expect(
      screen.queryByRole("button", {
        name: "Drag I-C1 to reorder subtask",
      }),
    ).toBeNull();
  });

  it("renders no grip for a guest (canEdit false)", async () => {
    renderBoard(10);
    await screen.findByText("Parent three");
    expect(document.querySelector('[data-nested-subtask="i-c1"]')).not.toBeNull();
    expect(
      screen.queryByRole("button", {
        name: "Drag I-C1 to reorder subtask",
      }),
    ).toBeNull();
  });
});
