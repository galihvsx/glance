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

// Workspace role for the current test: 15 = member, 5 = guest.
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
  if (path === `/api/v1/workspaces/${SLUG}/objectives/${OBJ_ID}`)
    return Promise.resolve(
      testObjective.value === "full" ? objective : emptyObjective,
    );
  if (path === `/api/v1/workspaces/${SLUG}/members`)
    return Promise.resolve({ members });
  if (path === `/api/v1/workspaces/${SLUG}/projects`)
    return Promise.resolve({ projects });
  if (path.includes(`/projects/ENG/issues`))
    return Promise.resolve({ results: projectIssues });
  throw new Error(`unexpected GET ${path}`);
}

beforeEach(() => {
  testRole.value = 15;
  testObjective.value = "full";
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
