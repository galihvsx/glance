// @vitest-environment jsdom
import { cleanup, render } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import AppShell from "./AppShell";

// The shell footer reads useAuth; stub it (same pattern as ShellFooter.test).
vi.mock("../../lib/auth", () => ({
  useAuth: () => ({
    user: {
      id: "u1",
      email: "test@example.com",
      name: "Test User",
      avatar_url: null,
      is_active: true,
      is_admin: false,
      last_login_at: null,
      created_at: "",
      updated_at: "",
    },
    loading: false,
    refresh: vi.fn(),
    logout: vi.fn(),
  }),
}));
import { favoriteKeys, type FavoritesList } from "../../lib/favorites";
import { NOTIFICATION_KEYS } from "../../lib/notifications";
import type { Project, Workspace } from "../../lib/types";
import { workspacesKey } from "../../lib/useWorkspaces";
import { makeQueryClient, stubMatchMedia } from "./test-utils";

const seeds: [readonly unknown[], unknown][] = [
  [
    workspacesKey,
    [
      {
        id: "w1",
        slug: "acme",
        name: "Acme Corp",
        role: 20,
        created_at: "",
        updated_at: "",
      } satisfies Workspace,
    ],
  ],
  [favoriteKeys.all, { issues: [], projects: [] } satisfies FavoritesList],
  [NOTIFICATION_KEYS.unread, 0],
  [
    ["projects", "acme"],
    [
      {
        id: "p1",
        workspace_id: "w1",
        identifier: "ENG",
        name: "Engineering",
        description: "",
        created_at: "",
        updated_at: "",
      } satisfies Project,
    ],
  ],
];

function renderApp(path: string, routePath: string) {
  const qc = makeQueryClient(seeds);
  return render(
    <MemoryRouter initialEntries={[path]}>
      <QueryClientProvider client={qc}>
        <Routes>
          <Route element={<AppShell />}>
            <Route path={routePath} element={<div>page content</div>} />
          </Route>
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

afterEach(() => {
  cleanup();
});

beforeEach(() => {
  localStorage.clear();
  stubMatchMedia(false);
});

describe("project tabs slot", () => {
  it("renders on project routes", () => {
    const { container } = renderApp("/w/acme/p/ENG", "/w/:slug/p/:identifier");
    expect(container.querySelector("#shell-project-tabs")).toBeTruthy();
  });

  it("does not render outside project routes", () => {
    const { container } = renderApp("/", "/");
    expect(container.querySelector("#shell-project-tabs")).toBeNull();
  });
});

describe("shell chrome", () => {
  it("renders the sidebar and top bar around page content", () => {
    const { container } = renderApp("/w/acme/p/ENG", "/w/:slug/p/:identifier");
    // Sidebar primitives + top bar + page outlet.
    expect(container.querySelector('[data-slot="sidebar"]')).toBeTruthy();
    expect(
      container.querySelector('button[aria-label="Toggle sidebar"]'),
    ).toBeTruthy();
    expect(container.textContent).toContain("page content");
  });
});
