// @vitest-environment jsdom
// C35T1: Goal detail — KR render, metric current-value update, add-KR
// (metric + issue kinds, zero-span guard, no metric-only fields on
// issue KRs), KR edit without a kind control, link/unlink issues,
// deletes via window.confirm, guest read-only, empty KR state.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import GoalDetail from "./GoalDetail";
import type {
  Issue,
  KeyResultCheckin,
  Objective,
  Project,
  WorkspaceMember,
} from "../lib/types";

const SLUG = "acme";
const OBJ_ID = "obj-1";

const objective: Objective = {
  id: OBJ_ID,
  workspace_id: "w1",
  title: "Ship glance v0.4.0",
  description: "Zero open parity gaps",
  owner: { id: "u-1", name: "galih", email: "galih@example.com" },
  start_date: "2026-10-16",
  target_date: "2026-12-31",
  status: "active",
  progress: 0.62,
  key_result_count: 2,
  key_results: [
    {
      id: "kr-1",
      objective_id: OBJ_ID,
      title: "Release notes for every cycle",
      kind: "metric",
      unit: "cycles",
      start_value: 0,
      target_value: 40,
      current_value: 34,
      progress: 0.85,
      created_at: "2026-10-16T00:00:00Z",
      updated_at: "2026-10-16T00:00:00Z",
    },
    {
      id: "kr-2",
      objective_id: OBJ_ID,
      title: "Close all Plane-parity gaps",
      kind: "issue",
      unit: null,
      start_value: 0,
      target_value: 8,
      current_value: 0,
      progress: 0.5,
      linked_issues: [
        {
          id: "iss-1",
          display_id: "ENG-201",
          title: "Fix login bug",
          state_group: "completed",
        },
        {
          id: "iss-2",
          display_id: "ENG-202",
          title: "Polish UI",
          state_group: "started",
        },
      ],
      created_at: "2026-10-16T00:00:00Z",
      updated_at: "2026-10-16T00:00:00Z",
    },
  ],
  created_at: "2026-10-16T00:00:00Z",
  updated_at: "2026-10-16T00:00:00Z",
};

const emptyObjective: Objective = {
  ...objective,
  progress: 0,
  key_result_count: 0,
  key_results: [],
};

const members: WorkspaceMember[] = [
  { id: "u-1", name: "galih", email: "galih@example.com", role: 15 },
];

const projects: Project[] = [
  {
    id: "p-1",
    workspace_id: "w1",
    identifier: "ENG",
    name: "Engineering",
    description: "",
    created_at: "",
    updated_at: "",
  },
];

const projectIssues: Issue[] = [
  {
    id: "iss-3",
    project_id: "p-1",
    sequence_id: 203,
    display_id: "ENG-203",
    name: "Add dark mode",
    priority: 2,
    state_id: "s-1",
    sort_order: 1,
    is_draft: false,
    created_by: "u-1",
    created_at: "",
    updated_at: "",
    assignees: [],
    labels: [],
  },
];

// Check-in history fixture for kr-1 (metric), newest first: a
// +4 move with a note, a −5 move without a note, and the seed entry
// (previous_value null, author null → "Deleted user").
const checkins: KeyResultCheckin[] = [
  {
    id: "ci-3",
    key_result_id: "kr-1",
    author: { id: "u-1", display_name: "galih" },
    previous_value: 30,
    new_value: 34,
    note: "Product Hunt launch",
    created_at: "2026-10-16T10:00:00Z",
  },
  {
    id: "ci-2",
    key_result_id: "kr-1",
    author: { id: "u-2", display_name: "ana" },
    previous_value: 35,
    new_value: 30,
    note: null,
    created_at: "2026-10-15T10:00:00Z",
  },
  {
    id: "ci-1",
    key_result_id: "kr-1",
    author: null,
    previous_value: null,
    new_value: 0,
    note: null,
    created_at: "2026-10-14T10:00:00Z",
  },
];

// Which check-ins fixture the history endpoint returns.
const testCheckins = vi.hoisted(() => ({
  value: "full" as
    | "full"
    | "empty"
    | "error"
    | "pending"
    | "falling"
    | "single"
    | "equal-timestamps"
    | "descending"
    | "flat",
}));
// Which KR start/target variant the objective endpoint returns
// (ascending = the base fixture; descending = target below start;
// flat = start == target == current, for the zero-value-range guard).
const testKR = vi.hoisted(() => ({
  value: "ascending" as "ascending" | "descending" | "flat",
}));
const testRole = vi.hoisted(() => ({ value: 15 }));
// Which objective fixture the detail endpoint returns.
const testObjective = vi.hoisted(() => ({
  value: "full" as "full" | "empty",
}));

const mockApi = vi.hoisted(() => ({
  get: vi.fn((path: string): Promise<unknown> => {
    throw new Error(`unexpected GET ${path}`);
  }),
  post: vi.fn((): Promise<unknown> => Promise.resolve({})),
  patch: vi.fn((): Promise<unknown> => Promise.resolve({})),
  del: vi.fn((): Promise<unknown> => Promise.resolve(undefined)),
  ApiError: class ApiError extends Error {
    status: number;
    body: string;
    constructor(status: number, message: string) {
      super(message);
      this.status = status;
      // Mirror the real ApiError: body carries the message.
      this.body = message;
    }
  },
}));

vi.mock("../lib/api", () => ({
  api: {
    get: mockApi.get,
    post: mockApi.post,
    patch: mockApi.patch,
    del: mockApi.del,
  },
  ApiError: mockApi.ApiError,
}));

function defaultGet(path: string): Promise<unknown> {
  if (path === `/api/v1/workspaces/${SLUG}`)
    return Promise.resolve({
      id: "w1",
      slug: SLUG,
      name: "Acme",
      role: testRole.value,
      slack_configured: false,
      created_at: "",
      updated_at: "",
    });
  if (path === `/api/v1/workspaces/${SLUG}/objectives/${OBJ_ID}`) {
    if (testObjective.value === "empty")
      return Promise.resolve(emptyObjective);
    if (testKR.value === "descending")
      return Promise.resolve({
        ...objective,
        key_results: (objective.key_results ?? []).map((kr) =>
          kr.id === "kr-1"
            ? { ...kr, start_value: 100, target_value: 20, current_value: 40 }
            : kr,
        ),
      });
    if (testKR.value === "flat")
      return Promise.resolve({
        ...objective,
        key_results: (objective.key_results ?? []).map((kr) =>
          kr.id === "kr-1"
            ? { ...kr, start_value: 25, target_value: 25, current_value: 25 }
            : kr,
        ),
      });
    return Promise.resolve(objective);
  }
  if (path === `/api/v1/workspaces/${SLUG}/members`)
    return Promise.resolve({ members });
  if (path === `/api/v1/workspaces/${SLUG}/projects`)
    return Promise.resolve({ projects });
  if (path.includes(`/projects/ENG/issues`))
    return Promise.resolve({ results: projectIssues });
  if (
    path ===
    `/api/v1/workspaces/${SLUG}/objectives/${OBJ_ID}/key-results/kr-1/check-ins`
  ) {
    if (testCheckins.value === "empty")
      return Promise.resolve({ check_ins: [] });
    if (testCheckins.value === "error")
      return Promise.reject(new mockApi.ApiError(500, "boom"));
    if (testCheckins.value === "pending")
      return new Promise<unknown>(() => {});
    if (testCheckins.value === "falling")
      // Newest first; chronological: 30 → 20 → 10.
      return Promise.resolve({
        check_ins: [
          {
            id: "ci-3",
            key_result_id: "kr-1",
            author: { id: "u-1", display_name: "galih" },
            previous_value: 20,
            new_value: 10,
            note: null,
            created_at: "2026-10-16T10:00:00Z",
          },
          {
            id: "ci-2",
            key_result_id: "kr-1",
            author: { id: "u-1", display_name: "galih" },
            previous_value: 30,
            new_value: 20,
            note: null,
            created_at: "2026-10-15T10:00:00Z",
          },
          {
            id: "ci-1",
            key_result_id: "kr-1",
            author: null,
            previous_value: null,
            new_value: 30,
            note: null,
            created_at: "2026-10-14T10:00:00Z",
          },
        ],
      });
    if (testCheckins.value === "single")
      return Promise.resolve({
        check_ins: [
          {
            id: "ci-1",
            key_result_id: "kr-1",
            author: null,
            previous_value: null,
            new_value: 5,
            note: null,
            created_at: "2026-10-14T10:00:00Z",
          },
        ],
      });
    if (testCheckins.value === "equal-timestamps")
      // All three share one timestamp — the chart must spread them
      // by index instead of dividing by a zero time range.
      // Chronological: 0 → 10 → 20.
      return Promise.resolve({
        check_ins: [
          {
            id: "ci-3",
            key_result_id: "kr-1",
            author: { id: "u-1", display_name: "galih" },
            previous_value: 10,
            new_value: 20,
            note: null,
            created_at: "2026-10-15T10:00:00Z",
          },
          {
            id: "ci-2",
            key_result_id: "kr-1",
            author: { id: "u-1", display_name: "galih" },
            previous_value: 0,
            new_value: 10,
            note: null,
            created_at: "2026-10-15T10:00:00Z",
          },
          {
            id: "ci-1",
            key_result_id: "kr-1",
            author: null,
            previous_value: null,
            new_value: 0,
            note: null,
            created_at: "2026-10-15T10:00:00Z",
          },
        ],
      });
    if (testCheckins.value === "descending")
      // Descending KR (start 100 → target 20); chronological:
      // 100 → 70 → 40.
      return Promise.resolve({
        check_ins: [
          {
            id: "ci-3",
            key_result_id: "kr-1",
            author: { id: "u-1", display_name: "galih" },
            previous_value: 70,
            new_value: 40,
            note: null,
            created_at: "2026-10-16T10:00:00Z",
          },
          {
            id: "ci-2",
            key_result_id: "kr-1",
            author: { id: "u-1", display_name: "galih" },
            previous_value: 100,
            new_value: 70,
            note: null,
            created_at: "2026-10-15T10:00:00Z",
          },
          {
            id: "ci-1",
            key_result_id: "kr-1",
            author: null,
            previous_value: null,
            new_value: 100,
            note: null,
            created_at: "2026-10-14T10:00:00Z",
          },
        ],
      });
    if (testCheckins.value === "flat")
      // Zero value range: every plotted value equals start == target.
      return Promise.resolve({
        check_ins: [
          {
            id: "ci-2",
            key_result_id: "kr-1",
            author: { id: "u-1", display_name: "galih" },
            previous_value: 25,
            new_value: 25,
            note: null,
            created_at: "2026-10-15T10:00:00Z",
          },
          {
            id: "ci-1",
            key_result_id: "kr-1",
            author: null,
            previous_value: null,
            new_value: 25,
            note: null,
            created_at: "2026-10-14T10:00:00Z",
          },
        ],
      });
    return Promise.resolve({ check_ins: checkins });
  }
  throw new Error(`unexpected GET ${path}`);
}

beforeEach(() => {
  testRole.value = 15;
  testObjective.value = "full";
  testCheckins.value = "full";
  testKR.value = "ascending";
  mockApi.get.mockImplementation(defaultGet);
  mockApi.post.mockImplementation(() => Promise.resolve({}));
  mockApi.patch.mockImplementation(() => Promise.resolve({}));
  mockApi.del.mockImplementation(() => Promise.resolve(undefined));
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

function renderPage() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <MemoryRouter initialEntries={[`/w/${SLUG}/goals/${OBJ_ID}`]}>
      <QueryClientProvider client={qc}>
        <Routes>
          <Route path="/w/:slug/goals" element={<div>goals list</div>} />
          <Route path="/w/:slug/goals/:id" element={<GoalDetail />} />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

describe("GoalDetail", () => {
  it("renders the objective header and its key results", async () => {
    renderPage();
    expect(await screen.findByText("Ship glance v0.4.0")).toBeTruthy();
    expect(screen.getByText("Zero open parity gaps")).toBeTruthy();
    expect(
      document.body.textContent,
    ).toContain("Owner: galih · 2026-10-16 → 2026-12-31");
    expect(screen.getByText("Release notes for every cycle")).toBeTruthy();
    expect(screen.getByText("Close all Plane-parity gaps")).toBeTruthy();
    expect(screen.getByText("Metric")).toBeTruthy();
    expect(screen.getByText("Issue-linked")).toBeTruthy();
    expect(screen.getByText("34 of 40 cycles · progress clamps 0–100%")).toBeTruthy();
    expect(screen.getByText("1 of 2 linked issues done")).toBeTruthy();
    expect(screen.getByText("ENG-201")).toBeTruthy();
    expect(screen.getByText("Fix login bug")).toBeTruthy();
  });

  it("shows 0% progress and the first-KR CTA when there are no KRs", async () => {
    testObjective.value = "empty";
    renderPage();
    expect(await screen.findByText("No key results yet")).toBeTruthy();
    expect(document.body.textContent).toContain("Objective progress 0%");
    expect(screen.getByText("Add your first key result")).toBeTruthy();
  });
});

describe("GoalDetail metric KR update", () => {
  it("PATCHes current_value from the inline update affordance", async () => {
    renderPage();
    const input = await screen.findByLabelText("Current value");
    fireEvent.change(input, { target: { value: "35" } });
    fireEvent.click(screen.getByRole("button", { name: "Update" }));
    await waitFor(() =>
      expect(mockApi.patch).toHaveBeenCalledWith(
        `/api/v1/workspaces/${SLUG}/objectives/${OBJ_ID}/key-results/kr-1`,
        { current_value: 35 },
      ),
    );
  });
});

describe("GoalDetail add key result", () => {
  it("posts a metric KR with unit, start, target, current", async () => {
    renderPage();
    fireEvent.click(
      (await screen.findAllByText("Add key result"))[0],
    );
    const dialog = await screen.findByRole("dialog");
    const form = within(dialog);
    fireEvent.change(form.getByLabelText("Title"), {
      target: { value: "Grow WAUs to 50" },
    });
    fireEvent.change(form.getByLabelText("Unit"), {
      target: { value: "users" },
    });
    fireEvent.change(form.getByLabelText("Start value"), {
      target: { value: "0" },
    });
    fireEvent.change(form.getByLabelText("Target value"), {
      target: { value: "50" },
    });
    fireEvent.change(form.getByLabelText("Current value"), {
      target: { value: "12" },
    });
    fireEvent.click(
      form.getByRole("button", { name: "Add key result" }),
    );
    await waitFor(() =>
      expect(mockApi.post).toHaveBeenCalledWith(
        `/api/v1/workspaces/${SLUG}/objectives/${OBJ_ID}/key-results`,
        {
          title: "Grow WAUs to 50",
          kind: "metric",
          unit: "users",
          start_value: 0,
          target_value: 50,
          current_value: 12,
        },
      ),
    );
  });

  it("posts an issue KR without metric-only fields", async () => {
    renderPage();
    fireEvent.click((await screen.findAllByText("Add key result"))[0]);
    const dialog = await screen.findByRole("dialog");
    const form = within(dialog);
    fireEvent.change(form.getByLabelText("Title"), {
      target: { value: "Close gaps" },
    });
    fireEvent.change(form.getByLabelText("Kind"), {
      target: { value: "issue" },
    });
    // Unit / Current value inputs are hidden for issue KRs.
    expect(form.queryByLabelText("Unit")).toBeNull();
    expect(form.queryByLabelText("Current value")).toBeNull();
    fireEvent.change(form.getByLabelText("Target value"), {
      target: { value: "8" },
    });
    fireEvent.click(
      form.getByRole("button", { name: "Add key result" }),
    );
    await waitFor(() => expect(mockApi.post).toHaveBeenCalledTimes(1));
    const call = mockApi.post.mock
      .calls[0] as unknown as [string, Record<string, unknown>];
    const body = call[1];
    expect(body).toEqual({ title: "Close gaps", kind: "issue", target_value: 8 });
    expect("unit" in body).toBe(false);
    expect("current_value" in body).toBe(false);
  });

  it("blocks a zero-span target client-side", async () => {
    renderPage();
    fireEvent.click((await screen.findAllByText("Add key result"))[0]);
    const dialog = await screen.findByRole("dialog");
    const form = within(dialog);
    fireEvent.change(form.getByLabelText("Title"), {
      target: { value: "Flat KR" },
    });
    fireEvent.change(form.getByLabelText("Start value"), {
      target: { value: "5" },
    });
    fireEvent.change(form.getByLabelText("Target value"), {
      target: { value: "5" },
    });
    fireEvent.click(
      form.getByRole("button", { name: "Add key result" }),
    );
    expect(
      await screen.findByText(/Target must differ from start/),
    ).toBeTruthy();
    expect(mockApi.post).not.toHaveBeenCalled();
  });

  it("surfaces a server 400 for the add-KR form", async () => {
    mockApi.post.mockRejectedValueOnce(
      new mockApi.ApiError(400, "invalid key result"),
    );
    renderPage();
    fireEvent.click((await screen.findAllByText("Add key result"))[0]);
    const dialog = await screen.findByRole("dialog");
    const form = within(dialog);
    fireEvent.change(form.getByLabelText("Title"), {
      target: { value: "Bad KR" },
    });
    fireEvent.change(form.getByLabelText("Target value"), {
      target: { value: "10" },
    });
    fireEvent.click(
      form.getByRole("button", { name: "Add key result" }),
    );
    expect(await screen.findByText("invalid key result")).toBeTruthy();
  });
});

describe("GoalDetail KR edit", () => {
  it("has no kind control and PATCHes without kind", async () => {
    renderPage();
    const metricRow = (await screen.findByText(
      "Release notes for every cycle",
    )).closest("li");
    fireEvent.click(
      within(metricRow as HTMLElement).getByRole("button", {
        name: "Edit key result Release notes for every cycle",
      }),
    );
    expect(screen.queryByLabelText("Kind")).toBeNull();
    fireEvent.change(screen.getByLabelText("Title"), {
      target: { value: "Release notes for every cycle (updated)" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(mockApi.patch).toHaveBeenCalledTimes(1));
    const call = mockApi.patch.mock
      .calls[0] as unknown as [string, Record<string, unknown>];
    const body = call[1];
    expect(body.title).toBe("Release notes for every cycle (updated)");
    expect("kind" in body).toBe(false);
  });
});

describe("GoalDetail link/unlink issues", () => {
  it("links picked issues from the project picker", async () => {
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Link issues" }));
    await screen.findByRole("option", { name: "Engineering (ENG)" });
    fireEvent.change(await screen.findByLabelText("Project"), {
      target: { value: "ENG" },
    });
    // The picker lists the project's issues, excluding linked ones.
    // (Scope to the dialog: "Fix login bug" also renders in the KR
    // card's linked-issues list behind the dialog.)
    const linkDialog = await screen.findByRole("dialog");
    const picker = within(linkDialog);
    expect(await picker.findByText("Add dark mode")).toBeTruthy();
    expect(picker.queryByText("Fix login bug")).toBeNull();
    fireEvent.click(picker.getByText("Add dark mode"));
    fireEvent.click(
      screen.getByRole("button", { name: "Link 1 issue" }),
    );
    await waitFor(() =>
      expect(mockApi.post).toHaveBeenCalledWith(
        `/api/v1/workspaces/${SLUG}/objectives/${OBJ_ID}/key-results/kr-2/issues`,
        { issue_id: "iss-3" },
      ),
    );
  });

  it("unlinks after window.confirm", async () => {
    vi.spyOn(window, "confirm").mockReturnValue(true);
    renderPage();
    fireEvent.click(
      await screen.findByRole("button", { name: "Unlink ENG-201" }),
    );
    await waitFor(() =>
      expect(mockApi.del).toHaveBeenCalledWith(
        `/api/v1/workspaces/${SLUG}/objectives/${OBJ_ID}/key-results/kr-2/issues/iss-1`,
      ),
    );
  });
});

describe("GoalDetail deletes", () => {
  it("deletes a KR after window.confirm", async () => {
    vi.spyOn(window, "confirm").mockReturnValue(true);
    renderPage();
    const metricRow = (await screen.findByText(
      "Release notes for every cycle",
    )).closest("li");
    fireEvent.click(
      within(metricRow as HTMLElement).getByRole("button", {
        name: "Delete key result Release notes for every cycle",
      }),
    );
    await waitFor(() =>
      expect(mockApi.del).toHaveBeenCalledWith(
        `/api/v1/workspaces/${SLUG}/objectives/${OBJ_ID}/key-results/kr-1`,
      ),
    );
  });

  it("does not delete a KR when the confirm is dismissed", async () => {
    vi.spyOn(window, "confirm").mockReturnValue(false);
    renderPage();
    const metricRow = (await screen.findByText(
      "Release notes for every cycle",
    )).closest("li");
    fireEvent.click(
      within(metricRow as HTMLElement).getByRole("button", {
        name: "Delete key result Release notes for every cycle",
      }),
    );
    expect(mockApi.del).not.toHaveBeenCalled();
  });

  it("deletes the objective after window.confirm and navigates back", async () => {
    vi.spyOn(window, "confirm").mockReturnValue(true);
    renderPage();
    fireEvent.click(
      await screen.findByRole("button", { name: "Delete objective" }),
    );
    await waitFor(() =>
      expect(mockApi.del).toHaveBeenCalledWith(
        `/api/v1/workspaces/${SLUG}/objectives/${OBJ_ID}`,
      ),
    );
    expect(screen.getByText("goals list")).toBeTruthy();
  });
});

describe("GoalDetail guest gating", () => {
  it("hides all write affordances for guests", async () => {
    testRole.value = 5;
    renderPage();
    await screen.findByText("Ship glance v0.4.0");
    expect(screen.queryByText("Add key result")).toBeNull();
    expect(screen.queryByText("Edit objective")).toBeNull();
    expect(screen.queryByText("Delete objective")).toBeNull();
    expect(screen.queryByLabelText("Current value")).toBeNull();
    expect(screen.queryByText("Link issues")).toBeNull();
    expect(
      screen.getByText(/guest access.*read-only/i),
    ).toBeTruthy();
  });
});

describe("GoalDetail KR check-in history", () => {
  it("renders history newest first: signed deltas, note, seed entry, deleted-user author", async () => {
    renderPage();
    const rows = await screen.findAllByTestId("kr-checkin-row");
    expect(rows).toHaveLength(3);

    // Newest first: +4 move with a note, by galih.
    expect(within(rows[0]).getByText("30 → 34")).toBeTruthy();
    expect(within(rows[0]).getByText("+4")).toBeTruthy();
    expect(within(rows[0]).getByText("Product Hunt launch")).toBeTruthy();
    expect(within(rows[0]).getByText(/galih ·/)).toBeTruthy();
    expect(
      rows[0].querySelector('[title="2026-10-16T10:00:00Z"]'),
    ).toBeTruthy();

    // −5 move with no note — the note is absent, not empty.
    expect(within(rows[1]).getByText("35 → 30")).toBeTruthy();
    expect(within(rows[1]).getByText("−5")).toBeTruthy();
    expect(within(rows[1]).getByText(/ana ·/)).toBeTruthy();
    expect(
      within(rows[1]).queryByText("Product Hunt launch"),
    ).toBeNull();

    // Seed entry: "started at {value}", never a delta, null author
    // renders as "Deleted user" (the codebase convention).
    expect(within(rows[2]).getByText("started at 0")).toBeTruthy();
    expect(within(rows[2]).getByText(/Deleted user ·/)).toBeTruthy();
    expect(rows[2].textContent).not.toContain("+");
    expect(rows[2].textContent).not.toContain("−");
  });

  it("fetches history for metric KRs only — no fetch fires for issue KRs", async () => {
    renderPage();
    await screen.findAllByTestId("kr-checkin-row");
    const checkinGets = mockApi.get.mock.calls.filter(
      (c) => typeof c[0] === "string" && c[0].includes("/check-ins"),
    );
    expect(checkinGets).toHaveLength(1);
    expect(checkinGets[0][0]).toContain("/key-results/kr-1/check-ins");
    // Exactly one history section: the metric KR's. The issue KR
    // shows none.
    expect(screen.getAllByText("Check-in history")).toHaveLength(1);
  });

  it("shows a loading indicator while history is fetching", async () => {
    testCheckins.value = "pending";
    renderPage();
    expect(
      await screen.findByText("Loading check-in history…"),
    ).toBeTruthy();
  });

  it("shows an error message when history fails to load", async () => {
    testCheckins.value = "error";
    renderPage();
    expect(
      await screen.findByText("Couldn't load check-in history."),
    ).toBeTruthy();
  });

  it("renders a sensible empty state for an empty history", async () => {
    testCheckins.value = "empty";
    renderPage();
    expect(await screen.findByText("No check-ins yet.")).toBeTruthy();
  });

  it("shows no history section for guests (list endpoint is member-only)", async () => {
    testRole.value = 5;
    renderPage();
    await screen.findByText("Ship glance v0.4.0");
    expect(screen.queryByText("Check-in history")).toBeNull();
    const checkinGets = mockApi.get.mock.calls.filter(
      (c) => typeof c[0] === "string" && c[0].includes("/check-ins"),
    );
    expect(checkinGets).toHaveLength(0);
  });

  it("sends checkin_note with the value PATCH when a note is given", async () => {
    renderPage();
    const input = await screen.findByLabelText("Current value");
    fireEvent.change(input, { target: { value: "35" } });
    fireEvent.change(screen.getByLabelText("Note (optional)"), {
      target: { value: "Launch day" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Update" }));
    await waitFor(() =>
      expect(mockApi.patch).toHaveBeenCalledWith(
        `/api/v1/workspaces/${SLUG}/objectives/${OBJ_ID}/key-results/kr-1`,
        { current_value: 35, checkin_note: "Launch day" },
      ),
    );
  });

  it("omits checkin_note from the PATCH when the note is empty", async () => {
    renderPage();
    const input = await screen.findByLabelText("Current value");
    fireEvent.change(input, { target: { value: "35" } });
    fireEvent.click(screen.getByRole("button", { name: "Update" }));
    await waitFor(() => expect(mockApi.patch).toHaveBeenCalledTimes(1));
    const call = mockApi.patch.mock
      .calls[0] as unknown as [string, Record<string, unknown>];
    expect(call[1]).toEqual({ current_value: 35 });
    expect("checkin_note" in call[1]).toBe(false);
  });

  it("omits checkin_note from the PATCH when the note is whitespace-only", async () => {
    renderPage();
    const input = await screen.findByLabelText("Current value");
    fireEvent.change(input, { target: { value: "35" } });
    fireEvent.change(screen.getByLabelText("Note (optional)"), {
      target: { value: "   " },
    });
    fireEvent.click(screen.getByRole("button", { name: "Update" }));
    await waitFor(() => expect(mockApi.patch).toHaveBeenCalledTimes(1));
    const call = mockApi.patch.mock
      .calls[0] as unknown as [string, Record<string, unknown>];
    expect("checkin_note" in call[1]).toBe(false);
  });
});

describe("GoalDetail KR progress chart", () => {
  it("renders a rising chart above the timeline from the same query", async () => {
    renderPage();
    const chart = await screen.findByTestId("kr-progress-chart");
    const svg = screen.getByRole("img", {
      name: "Progress over time: 0 → 34",
    });
    expect(svg.querySelector("title")?.textContent).toBe(
      "Progress over time: 0 → 34",
    );
    expect(within(chart).getByText("Start 0")).toBeTruthy();
    expect(within(chart).getByText("Target 40")).toBeTruthy();
    // First/last date axis labels.
    expect(within(chart).getByText("14 Oct 2026")).toBeTruthy();
    expect(within(chart).getByText("16 Oct 2026")).toBeTruthy();
    // Three points (seed + two check-ins), joined by a line.
    expect(
      within(chart).getAllByTestId("kr-chart-point"),
    ).toHaveLength(3);
    expect(within(chart).getByTestId("kr-chart-line")).toBeTruthy();
    // Timeline behavior unchanged: same 3 rows, newest first.
    expect(screen.getAllByTestId("kr-checkin-row")).toHaveLength(3);
    // The chart reuses the timeline's query — no second fetch.
    const checkinGets = mockApi.get.mock.calls.filter(
      (c) => typeof c[0] === "string" && c[0].includes("/check-ins"),
    );
    expect(checkinGets).toHaveLength(1);
  });

  it("renders a falling trend", async () => {
    testCheckins.value = "falling";
    renderPage();
    expect(
      await screen.findByRole("img", {
        name: "Progress over time: 30 → 10",
      }),
    ).toBeTruthy();
    const chart = screen.getByTestId("kr-progress-chart");
    expect(
      within(chart).getAllByTestId("kr-chart-point"),
    ).toHaveLength(3);
  });

  it("renders a single point as a dot with reference lines, no line", async () => {
    testCheckins.value = "single";
    renderPage();
    expect(
      await screen.findByRole("img", {
        name: "Progress over time: 5 → 5",
      }),
    ).toBeTruthy();
    const chart = screen.getByTestId("kr-progress-chart");
    expect(
      within(chart).getAllByTestId("kr-chart-point"),
    ).toHaveLength(1);
    expect(within(chart).queryByTestId("kr-chart-line")).toBeNull();
    expect(within(chart).getByText("Start 0")).toBeTruthy();
    expect(within(chart).getByText("Target 40")).toBeTruthy();
  });

  it("renders no chart when there are no check-ins", async () => {
    testCheckins.value = "empty";
    renderPage();
    expect(await screen.findByText("No check-ins yet.")).toBeTruthy();
    expect(screen.queryByTestId("kr-progress-chart")).toBeNull();
  });

  it("spreads points evenly when all timestamps are equal", async () => {
    testCheckins.value = "equal-timestamps";
    renderPage();
    expect(
      await screen.findByRole("img", {
        name: "Progress over time: 0 → 20",
      }),
    ).toBeTruthy();
    const dots = within(
      screen.getByTestId("kr-progress-chart"),
    ).getAllByTestId("kr-chart-point");
    expect(dots).toHaveLength(3);
    // Index-spread: left edge, centre, right edge — no NaN, no
    // pile-up at one x.
    const xs = dots.map((d) => d.getAttribute("cx"));
    expect(xs).toEqual(["44", "178", "312"]);
    expect(xs.every((x) => Number.isFinite(Number(x)))).toBe(true);
  });

  it("handles a descending KR (target below start)", async () => {
    testKR.value = "descending";
    testCheckins.value = "descending";
    renderPage();
    expect(
      await screen.findByRole("img", {
        name: "Progress over time: 100 → 40",
      }),
    ).toBeTruthy();
    const chart = screen.getByTestId("kr-progress-chart");
    expect(within(chart).getByText("Start 100")).toBeTruthy();
    expect(within(chart).getByText("Target 20")).toBeTruthy();
    expect(
      within(chart).getAllByTestId("kr-chart-point"),
    ).toHaveLength(3);
  });

  it("renders dashed reference lines for start and target", async () => {
    renderPage();
    const chart = await screen.findByTestId("kr-progress-chart");
    const start = within(chart).getByTestId("kr-chart-ref-start");
    const target = within(chart).getByTestId("kr-chart-ref-target");
    expect(start.getAttribute("stroke-dasharray")).toBe("4 3");
    expect(target.getAttribute("stroke-dasharray")).toBe("4 3");
    // Ascending KR: the target line sits above the start line.
    expect(Number(target.getAttribute("y1"))).toBeLessThan(
      Number(start.getAttribute("y1")),
    );
  });

  it("renders a flat midline when all values are equal", async () => {
    testKR.value = "flat";
    testCheckins.value = "flat";
    renderPage();
    expect(
      await screen.findByRole("img", {
        name: "Progress over time: 25 → 25",
      }),
    ).toBeTruthy();
    const dots = within(
      screen.getByTestId("kr-progress-chart"),
    ).getAllByTestId("kr-chart-point");
    expect(dots).toHaveLength(2);
    // Midline: top margin 8 + plot height 90 / 2 = 53.
    expect(dots.map((d) => d.getAttribute("cy"))).toEqual(["53", "53"]);
  });
});
