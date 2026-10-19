// @vitest-environment jsdom
// C27T2: initiative detail page — rollup render, per-project breakdown,
// add/remove project flow, guest read-only gating.
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import InitiativeDetail from "./InitiativeDetail";
import type {
  Initiative,
  InitiativeProject,
  InitiativeRollup,
} from "../components/initiatives/types";
import type { Project } from "../lib/types";

const SLUG = "acme";
const INIT_ID = "init-1";

const initiative: Initiative = {
  id: INIT_ID,
  workspace_id: "ws-1",
  name: "Q4 Platform Push",
  description: "All the platform bets for Q4.",
  status: "active",
  created_by: "u-1",
  project_count: 2,
  created_at: "2026-10-01T00:00:00Z",
  updated_at: "2026-10-02T00:00:00Z",
};

const memberProjects: InitiativeProject[] = [
  {
    project_id: "p-1",
    project_name: "Web",
    labels: [{ id: "pl-1", name: "platform", color: "#3b82f6" }],
    added_at: "2026-10-01T00:00:00Z",
  },
  { project_id: "p-2", project_name: "API", labels: [], added_at: "2026-10-01T00:00:00Z" },
];

const rollup: InitiativeRollup = {
  initiative_id: INIT_ID,
  total: 10,
  done_count: 4,
  completion_pct: 40,
  by_group: { triage: 1, backlog: 2, unstarted: 2, started: 1, completed: 3, cancelled: 1 },
  projects: [
    {
      project_id: "p-1",
      project_name: "Web",
      total: 6,
      done_count: 3,
      completion_pct: 50,
      by_group: { backlog: 2, started: 1, completed: 3 },
    },
    {
      project_id: "p-2",
      project_name: "API",
      total: 4,
      done_count: 1,
      completion_pct: 25,
      by_group: { triage: 1, unstarted: 2, cancelled: 1 },
    },
    {
      project_id: "p-3",
      project_name: "Docs",
      total: 0,
      done_count: 0,
      completion_pct: 0,
      by_group: {},
    },
  ],
};

const workspaceProjects: Project[] = [
  { id: "p-1", workspace_id: "ws-1", identifier: "WEB", labels: [], name: "Web", description: "", parallel_cycles: false, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z" },
  { id: "p-2", workspace_id: "ws-1", identifier: "API", labels: [], name: "API", description: "", parallel_cycles: false, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z" },
  { id: "p-4", workspace_id: "ws-1", identifier: "MOB", labels: [], name: "Mobile", description: "", parallel_cycles: false, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z" },
];

// Workspace role for the current test: 15 = member, 5 = guest.
const testRole = vi.hoisted(() => ({ value: 15 }));

const mockApi = vi.hoisted(() => ({
  get: vi.fn((path: string): Promise<unknown> => {
    if (path.endsWith("/rollup"))
      return Promise.resolve(rollup);
    if (path.includes(`/initiatives/${INIT_ID}/projects`))
      return Promise.resolve({ projects: memberProjects });
    if (path.includes(`/initiatives/${INIT_ID}`))
      return Promise.resolve(initiative);
    if (path === `/api/v1/workspaces/${SLUG}/projects`)
      return Promise.resolve({ projects: workspaceProjects });
    if (path === `/api/v1/workspaces/${SLUG}`)
      return Promise.resolve({ id: "ws-1", slug: SLUG, name: "Acme", role: testRole.value });
    throw new Error(`unexpected GET ${path}`);
  }),
  post: vi.fn((): Promise<unknown> => Promise.resolve({})),
  patch: vi.fn((): Promise<unknown> => Promise.resolve(initiative)),
  del: vi.fn((): Promise<unknown> => Promise.resolve(undefined)),
}));

vi.mock("../lib/api", () => ({
  api: {
    get: mockApi.get,
    post: mockApi.post,
    patch: mockApi.patch,
    del: mockApi.del,
  },
  ApiError: class ApiError extends Error {
    status = 0;
  },
}));

vi.mock("../components/ui/toast", () => ({
  toast: { add: vi.fn() },
}));

function renderPage() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[`/w/${SLUG}/initiatives/${INIT_ID}`]}>
        <Routes>
          <Route path="/w/:slug/initiatives/:id" element={<InitiativeDetail />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe("InitiativeDetail", () => {
  it("renders the initiative header, aggregate rollup, and per-project breakdown", async () => {
    renderPage();

    // Header
    expect(await screen.findByRole("heading", { name: "Q4 Platform Push" })).toBeInTheDocument();
    expect(screen.getByText("active")).toBeInTheDocument();
    expect(screen.getByText("All the platform bets for Q4.")).toBeInTheDocument();

    // Aggregate progress: 4 of 10 done, 40%
    expect(await screen.findByText(/4 of 10 issues done/i)).toBeInTheDocument();
    expect(screen.getByText("40%")).toBeInTheDocument();

    // by_group chips
    expect(screen.getByText(/triage/i)).toBeInTheDocument();
    expect(screen.getByText(/completed/i)).toBeInTheDocument();

    // Per-project breakdown rows
    expect(screen.getByRole("heading", { name: "Web" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "API" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Docs" })).toBeInTheDocument();
    expect(screen.getByText("50%")).toBeInTheDocument();
    expect(screen.getByText("25%")).toBeInTheDocument();
  });

  it("renders zero-issue projects gracefully (zeros, empty-by_group)", async () => {
    renderPage();
    await screen.findByRole("heading", { name: "Docs" });
    expect(screen.getByText("0 issues")).toBeInTheDocument();
    expect(screen.getByText("0%")).toBeInTheDocument();
  });

  it("lets a member add projects via the multi-select dialog", async () => {
    const user = userEvent.setup();
    renderPage();
    await screen.findByRole("heading", { name: "Q4 Platform Push" });

    await user.click(screen.getByRole("button", { name: /add projects/i }));
    // "Mobile" is a workspace project not yet in the initiative; "Web" is a member.
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toBeInTheDocument();
    const select = screen.getByLabelText(/projects to add/i);
    // "Web" is already a member project, so it is not offered here.
    expect(
      within(select).queryByRole("option", { name: /web/i }),
    ).not.toBeInTheDocument();
    expect(
      within(select).getByRole("option", { name: /mobile/i }),
    ).toBeInTheDocument();

    await user.selectOptions(select, ["p-4"]);
    await user.click(screen.getByRole("button", { name: /^add$/i }));

    await waitFor(() =>
      expect(mockApi.post).toHaveBeenCalledWith(
        `/api/v1/workspaces/${SLUG}/initiatives/${INIT_ID}/projects`,
        { project_id: "p-4" },
      ),
    );
  });

  it("lets a member remove a project with confirmation", async () => {
    const user = userEvent.setup();
    renderPage();
    await screen.findByRole("heading", { name: "Web" });

    const rows = screen.getAllByTestId("initiative-project-row");
    const webRow = rows.find((r) => r.textContent?.includes("Web"))!;
    await user.click(
      webRow.querySelector('button[aria-label="Remove Web from initiative"]')!,
    );

    await user.click(await screen.findByRole("button", { name: /remove project/i }));
    await waitFor(() =>
      expect(mockApi.del).toHaveBeenCalledWith(
        `/api/v1/workspaces/${SLUG}/initiatives/${INIT_ID}/projects/p-1`,
      ),
    );
  });

  it("lets a member change the status", async () => {
    const user = userEvent.setup();
    renderPage();
    await screen.findByRole("heading", { name: "Q4 Platform Push" });

    await user.click(screen.getByRole("button", { name: /change status/i }));
    await user.click(await screen.findByRole("menuitem", { name: /completed/i }));

    await waitFor(() =>
      expect(mockApi.patch).toHaveBeenCalledWith(
        `/api/v1/workspaces/${SLUG}/initiatives/${INIT_ID}`,
        { status: "completed" },
      ),
    );
  });

  it("lets a member delete the initiative with confirmation", async () => {
    const user = userEvent.setup();
    renderPage();
    await screen.findByRole("heading", { name: "Q4 Platform Push" });

    await user.click(screen.getByRole("button", { name: /delete initiative/i }));
    await user.click(await screen.findByRole("button", { name: /^delete$/i }));

    await waitFor(() =>
      expect(mockApi.del).toHaveBeenCalledWith(
        `/api/v1/workspaces/${SLUG}/initiatives/${INIT_ID}`,
      ),
    );
  });

  it("shows a guest a read-only view with no management controls", async () => {
    testRole.value = 5;
    try {
      renderPage();
    await screen.findByRole("heading", { name: "Q4 Platform Push" });
    await screen.findByText(/4 of 10 issues done/i);

    expect(screen.queryByRole("button", { name: /add projects/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /delete initiative/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /change status/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /edit initiative/i })).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/remove .* from initiative/i)).not.toBeInTheDocument();
    } finally {
      testRole.value = 15;
    }
  });

  it("renders member-project label chips after the project name (C76T1)", async () => {
    renderPage();
    // The Web member project carries the "platform" label (fixture).
    const webRow = (await screen.findAllByTestId("initiative-project-row")).find(
      (r) => r.textContent?.includes("Web"),
    )!;
    expect(
      within(webRow).getByTitle("platform"),
    ).toBeInTheDocument();
    // The API member project has no labels — no chip group rendered.
    const apiRow = (await screen.findAllByTestId("initiative-project-row")).find(
      (r) => r.textContent?.includes("API"),
    )!;
    expect(
      within(apiRow).queryByRole("group", { name: /project labels/i }),
    ).not.toBeInTheDocument();
  });
});
