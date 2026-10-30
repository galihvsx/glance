// @vitest-environment jsdom
// C89T1: the Projects page shows each project's teamspace as a small
// read-only label next to the name (the move control lives on Project
// settings, not on this card).

import { cleanup, render, screen } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import Projects from "./Projects";
import { api } from "../lib/api";
import { makeQueryClient } from "../components/shell/test-utils";
import { workspacesKey } from "../lib/useWorkspaces";
import type { Project, Teamspace, Workspace } from "../lib/types";

vi.mock("../lib/api", async (importOriginal) => {
  const mod = await importOriginal<typeof import("../lib/api")>();
  return {
    ...mod,
    api: {
      get: vi.fn(),
      post: vi.fn(),
      put: vi.fn(),
      patch: vi.fn(),
      del: vi.fn(),
    },
  };
});

const get = () => api.get as unknown as Mock;

const workspace: Workspace = {
  id: "w1",
  slug: "acme",
  name: "Acme",
  role: 20,
  slack_configured: false,
  github_webhook_configured: false,
  created_at: "",
  updated_at: "",
};

const projects: Project[] = [
  { id: "p-eng", workspace_id: "w1", identifier: "ENG", name: "Engine", description: "", labels: [], parallel_cycles: false, teamspace_id: "ts-platform", created_at: "", updated_at: "" },
  { id: "p-web", workspace_id: "w1", identifier: "WEB", name: "Website", description: "", labels: [], parallel_cycles: false, teamspace_id: null, created_at: "", updated_at: "" },
];

const teamspaces: Teamspace[] = [
  { id: "ts-platform", workspace_id: "w1", name: "Platform", project_count: 1, created_at: "", updated_at: "" },
];

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

beforeEach(() => {
  get().mockImplementation((url: string) => {
    if (url.includes("/teamspaces")) return Promise.resolve({ teamspaces });
    if (url.includes("/projects?archived=true")) return Promise.resolve({ projects: [] });
    if (url.includes("/projects")) return Promise.resolve({ projects });
    if (url === "/api/v1/workspaces/acme") return Promise.resolve(workspace);
    return Promise.resolve({});
  });
});

describe("Projects teamspace labels", () => {
  it("shows the teamspace label for assigned projects and none for ungrouped", async () => {
    const qc = makeQueryClient([[workspacesKey, [workspace]]]);
    render(
      <MemoryRouter initialEntries={["/w/acme/projects"]}>
        <QueryClientProvider client={qc}>
          <Routes>
            <Route path="/w/:slug/projects" element={<Projects />} />
          </Routes>
        </QueryClientProvider>
      </MemoryRouter>,
    );
    expect(await screen.findByText("Engine")).toBeTruthy();
    expect(screen.getByText("Platform")).toBeTruthy();
    // Website is ungrouped: no teamspace label anywhere near it, and
    // exactly one Platform label renders on the page.
    expect(screen.getAllByText("Platform")).toHaveLength(1);
  });
});
