// @vitest-environment jsdom
// C83T1: Dashboards list "Shared" tab — dashboards shared WITH me
// (badge shows my access) plus my dashboards I have shared out; the
// All tab and Public/Private sections are unchanged.
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import Dashboards from "./Dashboards";
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

const plain: Dashboard = {
  id: "d1",
  workspace_id: "w1",
  name: "Plain board",
  description: null,
  visibility: "public",
  created_by: "u-other",
  created_by_name: "ada",
  created_at: "",
  updated_at: "2026-10-20T06:00:00Z",
};

const sharedWithMe: Dashboard = {
  id: "d2",
  workspace_id: "w1",
  name: "Team health",
  description: null,
  visibility: "private",
  created_by: "u-other",
  created_by_name: "ada",
  created_at: "",
  updated_at: "2026-10-20T07:00:00Z",
  shared_with_me: true,
  my_access: "edit",
};

const sharedOut: Dashboard = {
  id: "d3",
  workspace_id: "w1",
  name: "My shared board",
  description: null,
  visibility: "private",
  created_by: "u-me",
  created_by_name: "galih",
  created_at: "",
  updated_at: "2026-10-20T08:00:00Z",
  shared: true,
  my_access: "owner",
};

vi.mock("../lib/api", () => ({
  api: {
    get: vi.fn((path: string): Promise<unknown> => {
      if (path === `/api/v1/workspaces/${SLUG}`)
        return Promise.resolve(workspace);
      if (path === `/api/v1/workspaces/${SLUG}/dashboards`)
        return Promise.resolve({
          dashboards: [plain, sharedWithMe, sharedOut],
        });
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
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

describe("Dashboards Shared tab", () => {
  it("shows every dashboard on the All tab", async () => {
    renderPage();
    expect(await screen.findByText("Plain board")).toBeInTheDocument();
    expect(screen.getByText("Team health")).toBeInTheDocument();
    expect(screen.getByText("My shared board")).toBeInTheDocument();
  });

  it("Shared tab lists inbound shares with my access, plus dashboards I shared out", async () => {
    renderPage();
    await screen.findByText("Plain board");
    fireEvent.click(screen.getByRole("tab", { name: "Shared" }));
    expect(screen.getByText("Team health")).toBeInTheDocument();
    expect(screen.getByText("My shared board")).toBeInTheDocument();
    expect(screen.queryByText("Plain board")).not.toBeInTheDocument();
    // The inbound badge names my access level.
    expect(screen.getByText(/Shared with you · Edit/)).toBeInTheDocument();
    // The outbound badge is present on my shared dashboard.
    expect(screen.getByText("Shared", { selector: "span" })).toBeInTheDocument();
  });
});
