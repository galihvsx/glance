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

function renderPage() {
  const get = vi.spyOn(api, "get").mockImplementation((path: string) => {
    if (path === "/api/v1/workspaces") return Promise.resolve({ workspaces: [] });
    if (path === `${BASE}/states`) return Promise.resolve({ states: [] });
    if (path === `${BASE}/cycles`) return Promise.resolve({ cycles: [] });
    if (path.includes("/issues"))
      return Promise.resolve({ results: [], next_cursor: null });
    if (path.includes("/burndown"))
      return Promise.resolve({ total_scope: 0, days: [] });
    return Promise.reject(new Error(`unexpected GET ${path}`));
  });
  const post = vi.spyOn(api, "post");
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
