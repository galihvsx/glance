// @vitest-environment jsdom
// C35T1: Goals list — cards, create flow (incl. 409 duplicate title),
// guest gating, empty state, navigation to detail.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import Goals from "./Goals";
import type {
  Objective,
  Workspace,
  WorkspaceMember,
} from "../lib/types";

const SLUG = "acme";

const workspace: Workspace = {
  id: "w1",
  slug: SLUG,
  name: "Acme",
  role: 15,
  slack_configured: false,
  created_at: "",
  updated_at: "",
};

const objective: Objective = {
  id: "obj-1",
  workspace_id: "w1",
  title: "Ship glance v0.4.0",
  description: "Zero open parity gaps",
  owner: { id: "u-1", name: "galih", email: "galih@example.com" },
  start_date: "2026-10-16",
  target_date: "2026-12-31",
  status: "active",
  progress: 0.62,
  key_result_count: 3,
  created_at: "2026-10-16T00:00:00Z",
  updated_at: "2026-10-16T00:00:00Z",
};

const members: WorkspaceMember[] = [
  { id: "u-1", name: "galih", email: "galih@example.com", role: 15 },
  { id: "u-2", name: null, email: "ada@example.com", role: 15 },
];

// Workspace role for the current test: 15 = member, 5 = guest.
const testRole = vi.hoisted(() => ({ value: 15 }));

const mockApi = vi.hoisted(() => ({
  get: vi.fn((path: string): Promise<unknown> => {
    if (path === `/api/v1/workspaces/${SLUG}`)
      return Promise.resolve({
        ...workspace,
        role: testRole.value,
      });
    if (path === `/api/v1/workspaces/${SLUG}/objectives`)
      return Promise.resolve({ objectives: [objective] });
    if (path === `/api/v1/workspaces/${SLUG}/members`)
      return Promise.resolve({ members });
    throw new Error(`unexpected GET ${path}`);
  }),
  post: vi.fn((): Promise<unknown> => Promise.resolve(objective)),
  patch: vi.fn((): Promise<unknown> => Promise.resolve(objective)),
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

// Default GET routing; individual tests override with
// mockImplementation and beforeEach restores this.
function defaultGet(path: string): Promise<unknown> {
  if (path === `/api/v1/workspaces/${SLUG}`)
    return Promise.resolve({ ...workspace, role: testRole.value });
  if (path === `/api/v1/workspaces/${SLUG}/objectives`)
    return Promise.resolve({ objectives: [objective] });
  if (path === `/api/v1/workspaces/${SLUG}/members`)
    return Promise.resolve({ members });
  throw new Error(`unexpected GET ${path}`);
}

beforeEach(() => {
  testRole.value = 15;
  mockApi.get.mockImplementation(defaultGet);
  mockApi.post.mockImplementation(() => Promise.resolve(objective));
  mockApi.patch.mockImplementation(() => Promise.resolve(objective));
  mockApi.del.mockImplementation(() => Promise.resolve(undefined));
});

function renderPage() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <MemoryRouter initialEntries={[`/w/${SLUG}/goals`]}>
      <QueryClientProvider client={qc}>
        <Routes>
          <Route path="/w/:slug/goals" element={<Goals />} />
          <Route
            path="/w/:slug/goals/:id"
            element={<div>goal detail</div>}
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

describe("Goals list", () => {
  it("renders objective cards with owner, target date, KR count, progress", async () => {
    renderPage();
    expect(await screen.findByText("Ship glance v0.4.0")).toBeTruthy();
    expect(screen.getByText("Active")).toBeTruthy();
    expect(
      document.body.textContent,
    ).toContain("Owner: galih · Target: 2026-12-31 · 3 key results");
    expect(screen.getByText("62%")).toBeTruthy();
  });

  it("navigates to the detail route when a card is clicked", async () => {
    renderPage();
    fireEvent.click(
      await screen.findByRole("button", {
        name: /open objective ship glance v0\.4\.0/i,
      }),
    );
    expect(screen.getByText("goal detail")).toBeTruthy();
  });

  it("shows a loading skeleton while objectives load", () => {
    renderPage();
    expect(
      document.querySelector(".animate-pulse, [class*='skeleton']"),
    ).toBeTruthy();
  });
});

describe("Goals guest gating", () => {
  it("hides the create button for guests but still shows the list", async () => {
    testRole.value = 5;
    renderPage();
    expect(await screen.findByText("Ship glance v0.4.0")).toBeTruthy();
    expect(screen.queryByText("New objective")).toBeNull();
  });

  it("shows the create button for members", async () => {
    renderPage();
    expect(await screen.findByText("New objective")).toBeTruthy();
  });
});

describe("Goals empty state", () => {
  it("shows No goals yet with a create CTA for members", async () => {
    mockApi.get.mockImplementation((path: string) => {
      if (path === `/api/v1/workspaces/${SLUG}`)
        return Promise.resolve({ ...workspace, role: 15 });
      if (path === `/api/v1/workspaces/${SLUG}/objectives`)
        return Promise.resolve({ objectives: [] });
      if (path === `/api/v1/workspaces/${SLUG}/members`)
        return Promise.resolve({ members });
      throw new Error(`unexpected GET ${path}`);
    });
    renderPage();
    expect(await screen.findByText("No goals yet")).toBeTruthy();
    expect(screen.getByText("Create objective")).toBeTruthy();
  });

  it("hides the empty-state CTA for guests", async () => {
    testRole.value = 5;
    mockApi.get.mockImplementation((path: string) => {
      if (path === `/api/v1/workspaces/${SLUG}`)
        return Promise.resolve({ ...workspace, role: 5 });
      if (path === `/api/v1/workspaces/${SLUG}/objectives`)
        return Promise.resolve({ objectives: [] });
      throw new Error(`unexpected GET ${path}`);
    });
    renderPage();
    expect(await screen.findByText("No goals yet")).toBeTruthy();
    expect(screen.queryByText("Create objective")).toBeNull();
  });
});

describe("Goals create flow", () => {
  it("posts the objective with owner, dates, and status", async () => {
    renderPage();
    fireEvent.click(await screen.findByText("New objective"));
    fireEvent.change(await screen.findByLabelText("Title"), {
      target: { value: "Grow WAUs to 50" },
    });
    fireEvent.change(screen.getByLabelText("Description"), {
      target: { value: "Weekly active workspaces" },
    });
    fireEvent.change(screen.getByLabelText("Owner"), {
      target: { value: "u-2" },
    });
    fireEvent.change(screen.getByLabelText("Start date"), {
      target: { value: "2026-10-16" },
    });
    fireEvent.change(screen.getByLabelText("Target date"), {
      target: { value: "2026-12-31" },
    });
    fireEvent.change(screen.getByLabelText("Status"), {
      target: { value: "active" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Create objective" }));

    await waitFor(() =>
      expect(mockApi.post).toHaveBeenCalledWith(
        `/api/v1/workspaces/${SLUG}/objectives`,
        {
          title: "Grow WAUs to 50",
          description: "Weekly active workspaces",
          owner_id: "u-2",
          start_date: "2026-10-16",
          target_date: "2026-12-31",
          status: "active",
        },
      ),
    );
  });

  it("surfaces the duplicate-title 409 error inline", async () => {
    mockApi.post.mockRejectedValueOnce(
      new mockApi.ApiError(409, "objective title already exists"),
    );
    renderPage();
    fireEvent.click(await screen.findByText("New objective"));
    fireEvent.change(await screen.findByLabelText("Title"), {
      target: { value: "Ship glance v0.4.0" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Create objective" }));
    expect(
      await screen.findByText(
        "An objective with that title already exists in this workspace.",
      ),
    ).toBeTruthy();
  });
});
