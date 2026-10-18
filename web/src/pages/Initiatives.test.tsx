// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import Initiatives from "./Initiatives";
import { api } from "../lib/api";
import type {
  Initiative,
  InitiativeRollup,
  Project,
  Workspace,
} from "../lib/types";
import { workspacesKey } from "../lib/useWorkspaces";
import { makeQueryClient } from "../components/shell/test-utils";

const workspace: Workspace = {
  id: "w1",
  slug: "acme",
  name: "Acme",
  role: 15,
  slack_configured: false,
    github_webhook_configured: false,
  created_at: "",
  updated_at: "",
};

const initiative: Initiative = {
  id: "init-1",
  workspace_id: "w1",
  name: "Q4 Platform",
  description: "Platform bets for Q4",
  status: "active",
  project_count: 2,
  created_at: "2026-10-01T00:00:00Z",
  updated_at: "2026-10-01T00:00:00Z",
};

const rollup: InitiativeRollup = {
  initiative_id: "init-1",
  total: 40,
  done_count: 30,
  completion_pct: 75,
  by_group: { completed: 30, started: 10 },
  projects: [],
};

const projects: Project[] = [
  {
    id: "p1",
    workspace_id: "w1",
    identifier: "ENG",
    name: "Engineering",
    description: "",
    created_at: "",
    updated_at: "",
  },
  {
    id: "p2",
    workspace_id: "w1",
    identifier: "DES",
    name: "Design",
    description: "",
    created_at: "",
    updated_at: "",
  },
];

function renderPage(opts: {
  role?: number;
  initiatives?: Initiative[];
  rollups?: Record<string, InitiativeRollup>;
}) {
  const ws: Workspace = { ...workspace, role: opts.role ?? 15 };
  const initiatives = opts.initiatives ?? [initiative];
  const rollups = opts.rollups ?? { [initiative.id]: rollup };
  const seeds: [readonly unknown[], unknown][] = [
    [["initiatives", "acme"], initiatives],
    [["workspace", "acme"], ws],
    [["projects", "acme"], projects],
    [workspacesKey, [ws]],
  ];
  for (const [id, r] of Object.entries(rollups)) {
    seeds.push([["initiative-rollup", "acme", id], r]);
  }
  const qc = makeQueryClient(seeds);
  return render(
    <MemoryRouter initialEntries={["/w/acme/initiatives"]}>
      <QueryClientProvider client={qc}>
        <Routes>
          <Route path="/w/:slug/initiatives" element={<Initiatives />} />
          <Route
            path="/w/:slug/initiatives/:id"
            element={<div>initiative detail</div>}
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

describe("Initiatives list", () => {
  it("renders initiative names with their status", () => {
    renderPage({});
    expect(screen.getByText("Q4 Platform")).toBeTruthy();
    expect(screen.getByText("active")).toBeTruthy();
  });

  it("renders the rollup progress per card", () => {
    renderPage({});
    expect(screen.getByText("75%")).toBeTruthy();
    expect(screen.getByText(/30 of 40/i)).toBeTruthy();
  });

  it("shows the member-project count", () => {
    renderPage({});
    expect(document.body.textContent).toContain("2 projects");
  });

  it("navigates to the detail route when a card is clicked", () => {
    renderPage({});
    fireEvent.click(screen.getByRole("button", { name: /open initiative q4 platform/i }));
    expect(screen.getByText("initiative detail")).toBeTruthy();
  });

  it("shows a loading skeleton while rollups load", () => {
    // Omit the rollup seed so the card shows its skeleton state.
    renderPage({ rollups: {} });
    expect(
      document.querySelector('[data-testid="initiative-rollup-skeleton"]'),
    ).toBeTruthy();
  });
});

describe("Initiatives guest gating", () => {
  it("hides the create button for guests but still shows the list", () => {
    renderPage({ role: 5 });
    expect(screen.getByText("Q4 Platform")).toBeTruthy();
    expect(screen.queryByText("New initiative")).toBeNull();
  });

  it("shows the create button for members", () => {
    renderPage({ role: 15 });
    expect(screen.getByText("New initiative")).toBeTruthy();
  });

  it("shows an empty state with a Create initiative CTA for members", () => {
    renderPage({ initiatives: [], rollups: {} });
    expect(screen.getByText("No initiatives yet")).toBeTruthy();
    expect(screen.getByText("Create initiative")).toBeTruthy();
  });

  it("hides the empty-state CTA for guests", () => {
    renderPage({ role: 5, initiatives: [], rollups: {} });
    expect(screen.getByText("No initiatives yet")).toBeTruthy();
    expect(screen.queryByText("Create initiative")).toBeNull();
  });
});

describe("Initiatives create flow", () => {
  it("posts the initiative then its member projects", async () => {
    const created: Initiative = { ...initiative, id: "init-2", name: "Mobile" };
    const post = vi
      .spyOn(api, "post")
      .mockResolvedValueOnce(created)
      .mockResolvedValue({ ok: true });
    renderPage({});

    fireEvent.click(screen.getByText("New initiative"));
    fireEvent.change(screen.getByLabelText("Name"), {
      target: { value: "Mobile" },
    });
    fireEvent.change(screen.getByLabelText("Description"), {
      target: { value: "Mobile push" },
    });
    fireEvent.click(screen.getByText("Engineering"));
    fireEvent.click(screen.getByText("Design"));

    fireEvent.click(screen.getByRole("button", { name: "Create initiative" }));

    await waitFor(() => expect(post).toHaveBeenCalledTimes(3));
    expect(post).toHaveBeenNthCalledWith(
      1,
      "/api/v1/workspaces/acme/initiatives",
      { name: "Mobile", description: "Mobile push" },
    );
    expect(post).toHaveBeenNthCalledWith(
      2,
      "/api/v1/workspaces/acme/initiatives/init-2/projects",
      { project_id: "p1" },
    );
    expect(post).toHaveBeenNthCalledWith(
      3,
      "/api/v1/workspaces/acme/initiatives/init-2/projects",
      { project_id: "p2" },
    );
  });

  it("keeps the create button disabled until a name is entered", () => {
    renderPage({});
    fireEvent.click(screen.getByText("New initiative"));
    expect(
      screen.getByRole("button", { name: "Create initiative" }),
    ).toHaveProperty("disabled", true);
  });
});
