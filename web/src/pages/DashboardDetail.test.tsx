// @vitest-environment jsdom
// C83T1: DashboardDetail access gating via my_access — owner sees
// everything (Share, settings, delete, visibility), edit access gets
// rename + widget editing only, view access is fully read-only.
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import DashboardDetail from "./DashboardDetail";
import type { Dashboard } from "../lib/dashboards";
import type { Workspace } from "../lib/types";

const SLUG = "acme";

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

function dashboard(over: Partial<Dashboard>): Dashboard {
  return {
    id: "d1",
    workspace_id: "w1",
    name: "Sprint health",
    description: null,
    visibility: "private",
    created_by: "u-other",
    created_by_name: "ada",
    created_at: "",
    updated_at: "",
    widgets: [],
    ...over,
  };
}

const mockFetchDashboard = vi.hoisted(() =>
  vi.fn(() => Promise.resolve(dashboard({}))),
);

vi.mock("../lib/api", () => ({
  api: {
    get: vi.fn((path: string): Promise<unknown> => {
      if (path === `/api/v1/workspaces/${SLUG}`)
        return Promise.resolve(workspace);
      throw new Error(`unexpected GET ${path}`);
    }),
  },
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

vi.mock("../lib/auth", () => ({
  useAuth: () => ({
    user: { id: "u-me", name: "Me", email: "me@acme.io" },
  }),
}));

vi.mock("../lib/dashboards", async (importOriginal) => {
  const mod =
    await importOriginal<typeof import("../lib/dashboards")>();
  return { ...mod, fetchDashboard: mockFetchDashboard };
});

afterEach(() => {
  cleanup();
  mockFetchDashboard.mockReset();
});

function renderPage() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <MemoryRouter initialEntries={[`/w/${SLUG}/dashboards/d1`]}>
      <QueryClientProvider client={qc}>
        <Routes>
          <Route
            path="/w/:slug/dashboards/:id"
            element={<DashboardDetail />}
          />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

describe("DashboardDetail access gating", () => {
  it("view access is fully read-only: no Share, Add widget, rename, delete, or visibility control", async () => {
    mockFetchDashboard.mockResolvedValue(
      dashboard({ my_access: "view", shared_with_me: true }),
    );
    renderPage();
    expect(await screen.findByText("Sprint health")).toBeInTheDocument();
    expect(screen.getByText("View access")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /Share/ }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /Add widget/ }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByLabelText("Rename dashboard"),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByLabelText("Delete dashboard"),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByLabelText("Dashboard visibility"),
    ).not.toBeInTheDocument();
  });

  it("edit access can rename and add widgets but cannot Share, delete, or change visibility", async () => {
    mockFetchDashboard.mockResolvedValue(
      dashboard({ my_access: "edit", shared_with_me: true }),
    );
    renderPage();
    expect(await screen.findByText("Sprint health")).toBeInTheDocument();
    expect(screen.getByText("Edit access")).toBeInTheDocument();
    expect(
      screen.getByLabelText("Rename dashboard"),
    ).toBeInTheDocument();
    // The page surfaces "Add widget" in both the PageHeader and the
    // header actions — presence is what the gate controls.
    expect(
      screen.getAllByRole("button", { name: /Add widget/ }).length,
    ).toBeGreaterThan(0);
    expect(
      screen.queryByRole("button", { name: /Share/ }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByLabelText("Delete dashboard"),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByLabelText("Dashboard settings"),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByLabelText("Dashboard visibility"),
    ).not.toBeInTheDocument();
  });

  it("owner sees Share, settings, delete, and the visibility control", async () => {
    mockFetchDashboard.mockResolvedValue(
      dashboard({ my_access: "owner", created_by: "u-me" }),
    );
    renderPage();
    expect(await screen.findByText("Sprint health")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /Share/ }),
    ).toBeInTheDocument();
    expect(
      screen.getAllByRole("button", { name: /Add widget/ }).length,
    ).toBeGreaterThan(0);
    expect(screen.getByLabelText("Dashboard settings")).toBeInTheDocument();
    expect(screen.getByLabelText("Delete dashboard")).toBeInTheDocument();
    expect(screen.getByLabelText("Dashboard visibility")).toBeInTheDocument();
  });
});
