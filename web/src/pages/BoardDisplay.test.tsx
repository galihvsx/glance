// @vitest-environment jsdom
// C121T1: board cards gate every new row by its toggle, and the board
// list fetches pass include_project_structure=1 (the C21T2 batched
// path). BoardCardBody is module-private, so these tests drive the
// real Board page with a mocked API.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import Board from "./Board";
import { api } from "../lib/api";
import type { Issue, IssueState } from "../lib/types";
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

// Full C121T1 payload: structure + counts + subtasks.
const issueA = makeIssue("i-a", "ENG-1", "Fix login redirect", "s-todo", 1, {
  subtask_progress: { total: 4, done: 1 },
  project_structure: {
    modules: [{ id: "m-1", name: "Auth" }],
    cycle: { id: "c-1", name: "Sprint 3" },
    release: { id: "r-1", name: "v2.1" },
  },
  attachment_count: 2,
  link_count: 1,
  relation_count: 3,
});

// Bare issue: no structure (cross-project-style fetch), zero counts.
const issueB = makeIssue("i-b", "ENG-2", "Urgent checkout bug", "s-done", 2, {
  subtask_progress: { total: 0, done: 0 },
  attachment_count: 0,
  link_count: 0,
  relation_count: 0,
});

function renderBoard() {
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
      return Promise.resolve({ results: [issueA, issueB], next_cursor: null });
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
    .filter((p) => p.includes("/issues?"));
}

beforeEach(() => {
  stubMatchMedia(false);
  localStorage.clear();
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("board cards (C121T1)", () => {
  it("passes include_project_structure=1 on the board issues fetch", async () => {
    const { get } = renderBoard();
    await screen.findByText("Fix login redirect");
    const calls = issueCalls(get);
    expect(calls.length).toBeGreaterThan(0);
    for (const url of calls) {
      expect(new URLSearchParams(url.split("?")[1] ?? "").get(
        "include_project_structure",
      )).toBe("1");
    }
  });

  it("renders every new row on a card with structure + counts + subtasks", async () => {
    renderBoard();
    await screen.findByText("Fix login redirect");
    expect(screen.getByText("ENG-1")).toBeInTheDocument();
    expect(
      screen.getByRole("progressbar", { name: "Subtasks: 1 of 4 done" }),
    ).toBeInTheDocument();
    expect(screen.getByLabelText("Module: Auth")).toBeInTheDocument();
    expect(screen.getByLabelText("Cycle: Sprint 3")).toBeInTheDocument();
    expect(screen.getByLabelText("Release: v2.1")).toBeInTheDocument();
    expect(screen.getByLabelText("2 attachments")).toBeInTheDocument();
    expect(screen.getByLabelText("1 link")).toBeInTheDocument();
    expect(screen.getByLabelText("3 dependencies")).toBeInTheDocument();
  });

  it("renders nothing for structure/count rows on a bare issue (no crash)", async () => {
    renderBoard();
    const title = await screen.findByText("Urgent checkout bug");
    const cardB = title.closest("[data-board-card]");
    expect(cardB).not.toBeNull();
    const html = cardB?.textContent ?? "";
    expect(html).not.toContain("Auth");
    expect(html).not.toContain("Sprint 3");
    expect(html).not.toContain("v2.1");
    expect(
      within(cardB as HTMLElement).queryByRole("progressbar"),
    ).not.toBeInTheDocument();
    expect(
      within(cardB as HTMLElement).queryByLabelText(/attachments|link|dependenc/),
    ).not.toBeInTheDocument();
  });

  it("hides the new rows when their toggles are off in stored settings", async () => {
    localStorage.setItem(
      "glance:display:acme:ENG",
      JSON.stringify({
        fields: {
          state: true,
          priority: true,
          labels: true,
          assignees: true,
          dates: true,
          id: false,
          subitems: false,
          modules: false,
          cycle: false,
          release: false,
          attachments: false,
          links: false,
          dependencies: false,
        },
        groupBy: "state",
        orderBy: "-updated_at",
        showEmptyGroups: true,
      }),
    );
    renderBoard();
    await screen.findByText("Fix login redirect");
    expect(screen.queryByText("ENG-1")).not.toBeInTheDocument();
    expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Module: Auth")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Cycle: Sprint 3")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Release: v2.1")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("2 attachments")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("1 link")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("3 dependencies")).not.toBeInTheDocument();
  });
});
