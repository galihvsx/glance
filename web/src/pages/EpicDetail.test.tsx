// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
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
import EpicDetail from "./EpicDetail";
import { api, ApiError } from "../lib/api";
import type {
  Epic,
  Initiative,
  Issue,
  IssueState,
} from "../lib/types";
import { makeQueryClient } from "../components/shell/test-utils";

// C108T3 fix-up: EpicDetail renders SavedViewsMenu → useAuth; the real
// provider needs a session — stub the auth context (C107T1 precedent
// in Cycles.test.tsx, same shape).
vi.mock("../lib/auth", () => ({
  useAuth: () => ({ user: { id: "u-1", email: "u@x.com" }, loading: false }),
}));

const states: IssueState[] = [
  { id: "s-backlog", name: "Backlog", group: "backlog" },
  { id: "s-started", name: "In Progress", group: "started" },
  { id: "s-done", name: "Done", group: "completed" },
] as IssueState[];

const issue = (over: Partial<Issue> & { id: string }): Issue =>
  ({
    project_id: "p1",
    sequence_id: 1,
    display_id: "ENG-1",
    name: "First issue",
    priority: 2,
    state_id: "s-started",
    sort_order: 1,
    is_draft: false,
    created_by: "u1",
    created_at: "",
    updated_at: "",
    assignees: [],
    labels: [],
    ...over,
  }) as Issue;

const issue1 = issue({ id: "i1", display_id: "ENG-1", name: "First issue", state_id: "s-started" });
const issue2 = issue({ id: "i2", display_id: "ENG-2", name: "Second issue", state_id: "s-done" });
const issue3 = issue({ id: "i3", display_id: "ENG-3", name: "Third issue", state_id: "s-backlog" });

const epic: Epic = {
  id: "epic-1",
  project_id: "p1",
  name: "Auth revamp",
  description: "Passwordless OTP + OAuth",
  archived_at: null,
  issue_count: 2,
  progress: {
    total: 12,
    by_group: { backlog: 2, unstarted: 3, started: 4, completed: 3, cancelled: 0 },
  },
  created_at: "2026-10-10T00:00:00Z",
  updated_at: "2026-10-10T00:00:00Z",
};

// C108T3 fix-up: the detail-mode SavedViewsMenu needs the shared
// ["project", slug, identifier] query (its project UUID feeds the
// menu's views query); seed it plus the empty views list, following
// the C107T1 project query + /api/v1/projects/.../views handling in
// Cycles.test.tsx, adapted to this file's seeded query client.
const project = {
  id: "p1",
  workspace_id: "w1",
  identifier: "ENG",
  name: "ENG",
  description: "",
  parallel_cycles: false,
  labels: [],
  created_at: "",
  updated_at: "",
};

const initiative: Initiative = {
  id: "init-1",
  workspace_id: "w1",
  identifier: "ENG",
  name: "Q4 Platform Bets",
  description: null,
  status: "active",
  project_count: 3,
  created_at: "",
  updated_at: "",
} as Initiative;

function renderDetail(opts: {
  epicData?: Epic;
  issues?: Issue[];
  boardIssues?: Issue[];
  initiatives?: Initiative[];
  linked?: Initiative[];
} = {}) {
  const epicData = opts.epicData ?? epic;
  const issues = opts.issues ?? [issue1, issue2];
  const boardIssues = opts.boardIssues ?? [issue1, issue2, issue3];
  const initiatives = opts.initiatives ?? [initiative];
  const linked = opts.linked ?? [initiative];
  const seeds: [readonly unknown[], unknown][] = [
    [["project", "acme", "ENG"], project],
    [["saved-views", "p1"], []],
    [["epic-detail", "acme", "ENG", "epic-1"], epicData],
    [["epic-issues", "acme", "ENG", "epic-1"], issues],
    [["states", "acme", "ENG"], states],
    [["initiatives", "acme"], initiatives],
    [["epic-linked-initiatives", "acme", "epic-1"], linked],
    [["board-issues", "acme", "ENG"], boardIssues],
  ];
  const qc = makeQueryClient(seeds);
  render(
    <MemoryRouter initialEntries={["/w/acme/p/ENG/epics/epic-1"]}>
      <QueryClientProvider client={qc}>
        <Routes>
          <Route
            path="/w/:slug/p/:identifier/epics/:epicID"
            element={<EpicDetail />}
          />
          <Route
            path="/w/:slug/p/:identifier/epics"
            element={<div>epics list</div>}
          />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("EpicDetail progress", () => {
  it("renders the done caption and by-group legend", () => {
    renderDetail();
    // done = completed + cancelled = 3 + 0.
    expect(screen.getByText(/3 of 12 issues done/i)).toBeTruthy();
    expect(screen.getByText("Backlog")).toBeTruthy();
    expect(screen.getByText("Cancelled")).toBeTruthy();
    expect(
      screen.getByRole("progressbar", { name: "Auth revamp progress" }),
    ).toBeTruthy();
  });

  it("renders the empty progress state for an epic with no issues", () => {
    renderDetail({
      epicData: { ...epic, issue_count: 0, progress: { total: 0, by_group: {} } },
      issues: [],
    });
    expect(screen.getByText(/no issues yet/i)).toBeTruthy();
  });
});

describe("EpicDetail issues", () => {
  it("lists member issues with states", () => {
    renderDetail();
    expect(screen.getByText("First issue")).toBeTruthy();
    expect(screen.getByText("Second issue")).toBeTruthy();
  });

  it("marks already-attached rows in the add-issues picker and attaches the picked ones", async () => {
    const post = vi.spyOn(api, "post").mockResolvedValue({ added: 1 });
    renderDetail();

    fireEvent.click(screen.getByText("Add issues"));

    await waitFor(() =>
      expect(screen.getByText("Third issue")).toBeTruthy(),
    );
    expect(screen.getAllByText("Already attached")).toHaveLength(2);
    // The attached rows' checkboxes are disabled.
    const firstRow = screen.getByLabelText("First issue already attached");
    expect(firstRow).toHaveAttribute("data-disabled");

    fireEvent.click(screen.getByLabelText("Attach Third issue"));
    fireEvent.click(screen.getByRole("button", { name: "Add 1 issue" }));

    await waitFor(() => expect(post).toHaveBeenCalledTimes(1));
    expect(post).toHaveBeenCalledWith(
      "/api/v1/workspaces/acme/projects/ENG/epics/epic-1/issues",
      { issue_ids: ["i3"] },
    );
  });

  it("detaches an issue via the row action", async () => {
    const del = vi.spyOn(api, "del").mockResolvedValue({ removed: 1 });
    renderDetail();

    fireEvent.click(
      screen.getByRole("button", { name: "Remove ENG-1 from epic" }),
    );

    await waitFor(() => expect(del).toHaveBeenCalledTimes(1));
    expect(del).toHaveBeenCalledWith(
      "/api/v1/workspaces/acme/projects/ENG/epics/epic-1/issues",
      { issue_ids: ["i1"] },
    );
  });
});

describe("EpicDetail lifecycle", () => {
  it("archives via the confirm dialog", async () => {
    const post = vi.spyOn(api, "post").mockResolvedValue({ ...epic, archived_at: "2026-10-19T00:00:00Z" });
    renderDetail();

    fireEvent.click(screen.getByRole("button", { name: "Archive" }));
    fireEvent.click(screen.getByRole("button", { name: "Archive epic" }));

    await waitFor(() => expect(post).toHaveBeenCalledTimes(1));
    expect(post).toHaveBeenCalledWith(
      "/api/v1/workspaces/acme/projects/ENG/epics/epic-1/archive",
    );
  });

  it("restores an archived epic", async () => {
    const post = vi.spyOn(api, "post").mockResolvedValue({ ...epic, archived_at: null });
    renderDetail({
      epicData: { ...epic, archived_at: "2026-10-02T00:00:00Z" },
    });

    expect(screen.getByText("Archived")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Restore" }));
    fireEvent.click(screen.getByRole("button", { name: "Restore epic" }));

    await waitFor(() => expect(post).toHaveBeenCalledTimes(1));
    expect(post).toHaveBeenCalledWith(
      "/api/v1/workspaces/acme/projects/ENG/epics/epic-1/unarchive",
    );
  });

  it("409 on delete offers 'Delete anyway (detach N issues)' and retries with ?force=true", async () => {
    const del = vi
      .spyOn(api, "del")
      .mockRejectedValueOnce(new ApiError(409, "epic still has issues"))
      .mockResolvedValue(undefined);
    renderDetail();

    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    fireEvent.click(screen.getByRole("button", { name: "Delete epic" }));

    await waitFor(() => expect(del).toHaveBeenCalledTimes(1));
    expect(del).toHaveBeenCalledWith(
      "/api/v1/workspaces/acme/projects/ENG/epics/epic-1",
    );

    // The dialog flips to the force copy after the 409.
    await waitFor(() =>
      expect(
        screen.getByText("Delete anyway (detach 2 issues)?"),
      ).toBeTruthy(),
    );
    fireEvent.click(screen.getByRole("button", { name: "Delete anyway" }));

    await waitFor(() => expect(del).toHaveBeenCalledTimes(2));
    expect(del).toHaveBeenCalledWith(
      "/api/v1/workspaces/acme/projects/ENG/epics/epic-1?force=true",
    );
    await waitFor(() => expect(screen.getByText("epics list")).toBeTruthy());
  });
});

describe("EpicDetail edit", () => {
  it("patches the changed fields", async () => {
    const patch = vi.spyOn(api, "patch").mockResolvedValue({ ...epic, name: "Auth v2" });
    renderDetail();

    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    fireEvent.change(screen.getByLabelText("Name"), {
      target: { value: "Auth v2" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1));
    expect(patch).toHaveBeenCalledWith(
      "/api/v1/workspaces/acme/projects/ENG/epics/epic-1",
      { name: "Auth v2" },
    );
  });
});

describe("EpicDetail linked initiatives", () => {
  it("renders linked initiatives with an unlink action", async () => {
    const del = vi.spyOn(api, "del").mockResolvedValue({ removed: "epic-1" });
    renderDetail();

    const row = screen.getByTestId("epic-initiative-row");
    expect(within(row).getByText("Q4 Platform Bets")).toBeTruthy();
    expect(within(row).getByText("active")).toBeTruthy();

    fireEvent.click(
      within(row).getByRole("button", { name: "Unlink Q4 Platform Bets" }),
    );
    await waitFor(() => expect(del).toHaveBeenCalledTimes(1));
    expect(del).toHaveBeenCalledWith(
      "/api/v1/workspaces/acme/initiatives/init-1/epics/epic-1",
    );
  });

  it("links an initiative from the picker", async () => {
    const post = vi.spyOn(api, "post").mockResolvedValue({ added: "epic-1" });
    const other: Initiative = {
      ...initiative,
      id: "init-2",
      name: "2027 Vision",
    };
    renderDetail({ initiatives: [initiative, other], linked: [] });

    fireEvent.click(screen.getByText("Link initiative"));
    await waitFor(() =>
      expect(screen.getByLabelText("Link 2027 Vision")).toBeTruthy(),
    );
    fireEvent.click(screen.getByLabelText("Link 2027 Vision"));
    fireEvent.click(screen.getByRole("button", { name: "Link initiative" }));

    await waitFor(() => expect(post).toHaveBeenCalledTimes(1));
    expect(post).toHaveBeenCalledWith(
      "/api/v1/workspaces/acme/initiatives/init-2/epics",
      { epic_id: "epic-1" },
    );
  });
});
