// @vitest-environment jsdom
// C81T1: Dashboards list — rows with visibility badges/creator/updated,
// guest gating of the create affordance, navigation to detail, and the
// visibility contract (the list renders exactly what the API returns —
// the server already filters out other creators' private dashboards).
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import Dashboards from "./Dashboards";
import type { Dashboard } from "../lib/dashboards";
import type { Workspace } from "../lib/types";

const SLUG = "acme";

// Workspace role for the current test: 15 = member, 5 = guest.
const testRole = vi.hoisted(() => ({ value: 15 }));

const workspace: Workspace = {
  id: "w1",
  slug: SLUG,
  name: "Acme",
  role: 15,
  slack_configured: false,
  github_webhook_configured: false,
  created_at: "",
  updated_at: "",
};

const publicDash: Dashboard = {
  id: "d1",
  workspace_id: "w1",
  name: "Sprint health",
  description: "Team board",
  visibility: "public",
  created_by: "u-other",
  created_by_name: "ada",
  created_at: "",
  updated_at: "2026-10-20T06:00:00Z",
};

const ownPrivate: Dashboard = {
  id: "d2",
  workspace_id: "w1",
  name: "Secret stuff",
  description: null,
  visibility: "private",
  created_by: "u-me",
  created_by_name: "galih",
  created_at: "",
  updated_at: "2026-10-20T07:00:00Z",
};

const mockApi = vi.hoisted(() => ({
  get: vi.fn((path: string): Promise<unknown> => {
    if (path === `/api/v1/workspaces/${SLUG}`)
      return Promise.resolve({ ...workspace, role: testRole.value });
    if (path === `/api/v1/workspaces/${SLUG}/dashboards`)
      // Mirrors the server contract: another creator's private
      // dashboard is already filtered out of this list.
      return Promise.resolve({ dashboards: [publicDash, ownPrivate] });
    throw new Error(`unexpected GET ${path}`);
  }),
  ApiError: class ApiError extends Error {
    status: number;
    body: string;
    constructor(status: number, message: string) {
      super(message);
      this.status = status;
      this.body = message;
    }
  },
}));

vi.mock("../lib/api", () => ({
  api: { get: mockApi.get },
  ApiError: mockApi.ApiError,
}));

beforeEach(() => {
  testRole.value = 15;
});

afterEach(cleanup);

function renderPage() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <MemoryRouter initialEntries={[`/w/${SLUG}/dashboards`]}>
      <QueryClientProvider client={qc}>
        <Routes>
          <Route path="/w/:slug/dashboards" element={<Dashboards />} />
          <Route
            path="/w/:slug/dashboards/:id"
            element={<div>dashboard detail</div>}
          />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

describe("Dashboards list", () => {
  it("renders rows with name, visibility badge, creator, and updated-at", async () => {
    renderPage();
    expect(await screen.findByText("Sprint health")).toBeInTheDocument();
    expect(screen.getByText("Public")).toBeInTheDocument();
    expect(screen.getByText("Private")).toBeInTheDocument();
    expect(screen.getByText("Secret stuff")).toBeInTheDocument();
    expect(document.body.textContent).toContain("ada");
    expect(document.body.textContent).toContain("galih");
  });

  it("never links another creator's private dashboard (server-filtered list)", async () => {
    // The API contract: list hides others' private dashboards, so the
    // UI only ever sees public + own-private rows.
    renderPage();
    await screen.findByText("Sprint health");
    const names = screen
      .getAllByRole("button")
      .map((b) => b.textContent ?? "");
    expect(names.some((t) => t.includes("Secret stuff"))).toBe(true);
    expect(names.some((t) => t.includes("Someone else's private"))).toBe(
      false,
    );
  });

  it("hides the New dashboard affordance for guests", async () => {
    testRole.value = 5;
    renderPage();
    await screen.findByText("Sprint health");
    expect(
      screen.queryByRole("button", { name: /New dashboard/ }),
    ).not.toBeInTheDocument();
  });

  it("shows the New dashboard affordance for members", async () => {
    renderPage();
    await screen.findByText("Sprint health");
    expect(
      screen.getByRole("button", { name: /New dashboard/ }),
    ).toBeInTheDocument();
  });

  it("navigates to the detail page on row click", async () => {
    renderPage();
    fireEvent.click(await screen.findByText("Sprint health"));
    expect(await screen.findByText("dashboard detail")).toBeInTheDocument();
  });
});
