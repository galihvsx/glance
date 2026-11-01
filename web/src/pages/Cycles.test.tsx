// @vitest-environment jsdom
// Page tests for the create-cycle conflict feedback (C78T1): when the
// backend rejects a create with 409 (cycle dates overlap), the dialog
// surfaces the backend message inline — naming the conflicting cycle —
// and stays open for retry. A 400 behaves the same; a successful create
// closes the dialog.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import Cycles from "./Cycles";
import { api, ApiError } from "../lib/api";
import type { Cycle } from "../lib/types";

// C107T1: CycleDetail renders SavedViewsMenu → useAuth; the real
// provider needs a session — stub the auth context (BoardPql pattern).
vi.mock("../lib/auth", () => ({
  useAuth: () => ({ user: { id: "u-1", email: "u@x.com" }, loading: false }),
}));
import {
  makeQueryClient,
  stubMatchMedia,
} from "../components/shell/test-utils";

const BASE = "/api/v1/workspaces/acme/projects/ENG";

const created: Cycle = {
  id: "c-1",
  project_id: "p-eng",
  name: "Sprint 13",
  start_date: "2026-11-02T00:00:00Z",
  end_date: "2026-11-15T00:00:00Z",
  status: "upcoming",
  progress_snapshot: {},
  created_at: "",
  updated_at: "",
};

function renderPage(opts: { role?: number; cycles?: Cycle[] } = {}) {
  const get = vi.spyOn(api, "get").mockImplementation((path: string) => {
    if (path === "/api/v1/workspaces") return Promise.resolve({ workspaces: [] });
    if (path === "/api/v1/workspaces/acme")
      return Promise.resolve({ role: opts.role ?? 5 });
    if (path === `${BASE}/states`) return Promise.resolve({ states: [] });
    if (path === `${BASE}/cycles`)
      return Promise.resolve({ cycles: opts.cycles ?? [] });
    // C107T1: CycleDetail's project query + the menu's views query.
    if (path === BASE)
      return Promise.resolve({
        id: "p-eng",
        workspace_id: "w-acme",
        identifier: "ENG",
        name: "ENG",
        description: "",
        parallel_cycles: false,
        labels: [],
        created_at: "",
        updated_at: "",
      });
    if (path === "/api/v1/projects/p-eng/views")
      return Promise.resolve({ views: [] });
    if (path.includes("/issues"))
      return Promise.resolve({ results: [], next_cursor: null });
    if (path.includes("/burndown"))
      return Promise.resolve({ total_scope: 0, days: [] });
    return Promise.reject(new Error(`unexpected GET ${path}`));
  });
  const post = vi.spyOn(api, "post").mockImplementation((path: string) => {
    return Promise.reject(new Error(`unexpected POST ${path}`));
  });
  const qc = makeQueryClient();
  render(
    <MemoryRouter initialEntries={["/w/acme/p/ENG/cycles"]}>
      <QueryClientProvider client={qc}>
        <Routes>
          <Route path="/w/:slug/p/:identifier/cycles" element={<Cycles />} />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
  return { get, post };
}

// C79T1 fixtures: manual start/end of cycles.
const upcomingCycle: Cycle = {
  id: "c-up",
  project_id: "p-eng",
  name: "Sprint 14",
  start_date: "2026-12-01T00:00:00Z",
  end_date: "2026-12-14T00:00:00Z",
  status: "upcoming",
  progress_snapshot: {},
  created_at: "",
  updated_at: "",
};
const currentCycle: Cycle = {
  ...upcomingCycle,
  id: "c-cur",
  name: "Sprint 13",
  status: "current",
};
const completedCycle: Cycle = {
  ...upcomingCycle,
  id: "c-done",
  name: "Sprint 12",
  status: "completed",
};

describe("manual start/end actions (C79T1)", () => {
  it("hides Start now for guests (role 5)", async () => {
    renderPage({ role: 5, cycles: [upcomingCycle] });
    await screen.findByRole("button", { name: "Open cycle Sprint 14" });
    expect(
      screen.queryByRole("button", { name: "Start now" }),
    ).toBeNull();
  });

  it("shows Start now for members (role 15) on upcoming cycles", async () => {
    renderPage({ role: 15, cycles: [upcomingCycle] });
    await screen.findByRole("button", { name: "Start now" });
  });

  it("Start now calls the start endpoint and invalidates the cycles list", async () => {
    const { get, post } = renderPage({ role: 15, cycles: [upcomingCycle] });
    await screen.findByRole("button", { name: "Start now" });
    const cyclesCalls = () =>
      get.mock.calls.filter((c) => c[0] === `${BASE}/cycles`).length;
    const before = cyclesCalls();
    post.mockResolvedValueOnce({ ...upcomingCycle, status: "current" });
    fireEvent.click(screen.getByRole("button", { name: "Start now" }));
    await waitFor(() => {
      expect(post).toHaveBeenCalledWith(`${BASE}/cycles/c-up/start`);
    });
    await waitFor(() => {
      expect(cyclesCalls()).toBeGreaterThan(before);
    });
  });

  it("a 409 on start surfaces the named conflicting cycle inline and stays actionable", async () => {
    const { post } = renderPage({ role: 15, cycles: [upcomingCycle] });
    post.mockRejectedValueOnce(
      new ApiError(409, 'cycle dates overlap with "Sprint 12"', "conflict"),
    );
    fireEvent.click(
      await screen.findByRole("button", { name: "Start now" }),
    );
    await screen.findByText('cycle dates overlap with "Sprint 12"');
    // The card stays actionable: the button is still there for a retry.
    expect(screen.getByRole("button", { name: "Start now" })).toBeTruthy();
    expect(post).toHaveBeenCalledWith(`${BASE}/cycles/c-up/start`);
  });

  it("shows Start now pending state while the request is in flight", async () => {
    const { post } = renderPage({ role: 15, cycles: [upcomingCycle] });
    await screen.findByRole("button", { name: "Start now" });
    let resolvePost!: (c: Cycle) => void;
    post.mockImplementationOnce(
      () =>
        new Promise<Cycle>((resolve) => {
          resolvePost = resolve;
        }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Start now" }));
    const pending = await screen.findByRole("button", { name: "Starting…" });
    expect(pending).toBeDisabled();
    resolvePost({ ...upcomingCycle, status: "current" });
    await waitFor(() => {
      expect(
        screen.queryByRole("button", { name: "Starting…" }),
      ).toBeNull();
    });
  });

  it("a 400 on start stays actionable with the message inline", async () => {
    const { post } = renderPage({ role: 15, cycles: [upcomingCycle] });
    post
      .mockRejectedValueOnce(
        new ApiError(
          400,
          "only upcoming cycles can be started manually",
          "validation_error",
        ),
      )
      .mockResolvedValueOnce({ ...upcomingCycle, status: "current" });
    fireEvent.click(
      await screen.findByRole("button", { name: "Start now" }),
    );
    await screen.findByText("only upcoming cycles can be started manually");
    // Still actionable: retry clears the error.
    fireEvent.click(screen.getByRole("button", { name: "Start now" }));
    await waitFor(() => {
      expect(
        screen.queryByText("only upcoming cycles can be started manually"),
      ).toBeNull();
    });
  });

  it("shows End now for members on current cycles, behind a confirm dialog with the freeze/rollover copy", async () => {
    renderPage({ role: 15, cycles: [currentCycle] });
    await screen.findByRole("button", { name: "Open cycle Sprint 13" });
    fireEvent.click(screen.getByRole("button", { name: "End now" }));
    await screen.findByText(/freezes the cycle's report/);
    expect(
      screen.getByText(/rolls over incomplete issues/),
    ).toBeTruthy();
    expect(screen.getByRole("button", { name: "End cycle" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Cancel" })).toBeTruthy();
  });

  it("hides End now for guests", async () => {
    renderPage({ role: 5, cycles: [currentCycle] });
    await screen.findByRole("button", { name: "Open cycle Sprint 13" });
    expect(screen.queryByRole("button", { name: "End now" })).toBeNull();
  });

  it("completed cycles show neither Start now nor End now", async () => {
    renderPage({ role: 15, cycles: [completedCycle] });
    await screen.findByRole("button", { name: "Open cycle Sprint 12" });
    expect(
      screen.queryByRole("button", { name: "Start now" }),
    ).toBeNull();
    expect(screen.queryByRole("button", { name: "End now" })).toBeNull();
  });

  it("upcoming cycles don't show End now and current cycles don't show Start now", async () => {
    renderPage({ role: 15, cycles: [upcomingCycle, currentCycle] });
    await screen.findByRole("button", { name: "Open cycle Sprint 14" });
    expect(screen.getByRole("button", { name: "Start now" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "End now" })).toBeTruthy();
    expect(
      screen.queryAllByRole("button", { name: "Start now" }),
    ).toHaveLength(1);
    expect(
      screen.queryAllByRole("button", { name: "End now" }),
    ).toHaveLength(1);
  });

  it("confirming the dialog calls the end endpoint, closes, and invalidates", async () => {
    const { get, post } = renderPage({ role: 15, cycles: [currentCycle] });
    fireEvent.click(
      await screen.findByRole("button", { name: "End now" }),
    );
    await screen.findByText(/freezes the cycle's report/);
    const cyclesCalls = () =>
      get.mock.calls.filter((c) => c[0] === `${BASE}/cycles`).length;
    const before = cyclesCalls();
    post.mockResolvedValueOnce({ ...currentCycle, status: "completed" });
    fireEvent.click(screen.getByRole("button", { name: "End cycle" }));
    await waitFor(() => {
      expect(post).toHaveBeenCalledWith(`${BASE}/cycles/c-cur/end`);
    });
    await waitFor(() => {
      expect(
        screen.queryByText(/freezes the cycle's report/),
      ).toBeNull();
    });
    await waitFor(() => {
      expect(cyclesCalls()).toBeGreaterThan(before);
    });
  });

  it("a 400 on end keeps the dialog open with the message inline", async () => {
    const { post } = renderPage({ role: 15, cycles: [currentCycle] });
    fireEvent.click(
      await screen.findByRole("button", { name: "End now" }),
    );
    await screen.findByText(/freezes the cycle's report/);
    post.mockRejectedValueOnce(
      new ApiError(400, "cycle is already completed", "validation_error"),
    );
    fireEvent.click(screen.getByRole("button", { name: "End cycle" }));
    await screen.findByText("cycle is already completed");
    // The dialog stays open and actionable — the user can retry or cancel.
    expect(
      screen.getByText(/freezes the cycle's report/),
    ).toBeTruthy();
    expect(screen.getByRole("button", { name: "End cycle" })).toBeTruthy();
  });

  it("shows End cycle pending state while the end request is in flight", async () => {
    const { post } = renderPage({ role: 15, cycles: [currentCycle] });
    fireEvent.click(
      await screen.findByRole("button", { name: "End now" }),
    );
    await screen.findByText(/freezes the cycle's report/);
    let resolvePost!: (c: Cycle) => void;
    post.mockImplementationOnce(
      () =>
        new Promise<Cycle>((resolve) => {
          resolvePost = resolve;
        }),
    );
    fireEvent.click(screen.getByRole("button", { name: "End cycle" }));
    const pending = await screen.findByRole("button", { name: "Ending…" });
    expect(pending).toBeDisabled();
    resolvePost({ ...currentCycle, status: "completed" });
    await waitFor(() => {
      expect(screen.queryByRole("button", { name: "Ending…" })).toBeNull();
    });
  });

  it("End now in the cycle detail header calls the end endpoint", async () => {
    const { post } = renderPage({ role: 15, cycles: [currentCycle] });
    fireEvent.click(
      await screen.findByRole("button", { name: "Open cycle Sprint 13" }),
    );
    await screen.findByText("Issues in this cycle");
    fireEvent.click(screen.getByRole("button", { name: "End now" }));
    await screen.findByText(/freezes the cycle's report/);
    post.mockResolvedValueOnce({ ...currentCycle, status: "completed" });
    fireEvent.click(screen.getByRole("button", { name: "End cycle" }));
    await waitFor(() => {
      expect(post).toHaveBeenCalledWith(`${BASE}/cycles/c-cur/end`);
    });
  });
});

async function openCreateDialog() {
  fireEvent.click(screen.getByRole("button", { name: "New cycle" }));
  await screen.findByRole("dialog");
}

async function fillAndSubmit() {
  fireEvent.change(screen.getByLabelText("Name"), {
    target: { value: "Sprint 13" },
  });
  fireEvent.change(screen.getByLabelText("Start date"), {
    target: { value: "2026-11-02" },
  });
  fireEvent.change(screen.getByLabelText("End date"), {
    target: { value: "2026-11-15" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Create cycle" }));
}

beforeEach(() => {
  stubMatchMedia(false);
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("Cycles create dialog conflict feedback", () => {
  it("shows the 409 overlap message inline, naming the conflicting cycle, and keeps the dialog open", async () => {
    const { post } = renderPage();
    await openCreateDialog();
    post.mockRejectedValueOnce(
      new ApiError(
        409,
        'cycle dates overlap with "Sprint 12"',
        "conflict",
      ),
    );
    await fillAndSubmit();
    await waitFor(() => {
      expect(
        screen.getByText('cycle dates overlap with "Sprint 12"'),
      ).toBeTruthy();
    });
    // The dialog stays open so the user can adjust and retry.
    expect(screen.getByRole("dialog")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Create cycle" })).toBeTruthy();
  });

  it("keeps the dialog open with the inline message on a 400 validation error", async () => {
    const { post } = renderPage();
    await openCreateDialog();
    post.mockRejectedValueOnce(
      new ApiError(
        400,
        "end date must be after start date",
        "validation_error",
      ),
    );
    await fillAndSubmit();
    await waitFor(() => {
      expect(screen.getByText("end date must be after start date")).toBeTruthy();
    });
    expect(screen.getByRole("dialog")).toBeTruthy();
  });

  it("clears the 409 error on retry and closes the dialog on success", async () => {
    const { post } = renderPage();
    await openCreateDialog();
    post
      .mockRejectedValueOnce(
        new ApiError(
          409,
          'cycle dates overlap with "Sprint 12"',
          "conflict",
        ),
      )
      .mockResolvedValueOnce(created);
    await fillAndSubmit();
    await screen.findByText('cycle dates overlap with "Sprint 12"');
    // Retry with the (unchanged) form values — the error clears first.
    fireEvent.click(screen.getByRole("button", { name: "Create cycle" }));
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
  });
});
