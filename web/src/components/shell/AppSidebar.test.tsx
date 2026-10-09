// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import AppSidebar from "./AppSidebar";
import { ShellProvider, type ShellContextValue } from "./shell-context";
import { SidebarProvider } from "../ui/sidebar";
import { favoriteKeys, type FavoritesList } from "../../lib/favorites";
import { NOTIFICATION_KEYS } from "../../lib/notifications";
import type { Project, Workspace } from "../../lib/types";
import { workspacesKey } from "../../lib/useWorkspaces";
import { ShellCapture, makeQueryClient, stubMatchMedia } from "./test-utils";

const workspaces: Workspace[] = [
  {
    id: "w1",
    slug: "acme",
    name: "Acme Corp",
    role: 20,
    created_at: "",
    updated_at: "",
  },
];

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
];

const emptyFavorites: FavoritesList = { issues: [], projects: [] };

function renderSidebar(opts: {
  path: string;
  routePath: string;
  seeds?: [readonly unknown[], unknown][];
  capture: { current: ShellContextValue | null };
}) {
  const qc = makeQueryClient([
    [workspacesKey, workspaces],
    [favoriteKeys.all, emptyFavorites],
    [NOTIFICATION_KEYS.unread, 3],
    ...(opts.seeds ?? []),
  ]);
  return render(
    <MemoryRouter initialEntries={[opts.path]}>
      <QueryClientProvider client={qc}>
        <Routes>
          <Route
            path={opts.routePath}
            element={
              <ShellProvider>
                <SidebarProvider>
                  <AppSidebar />
                  <ShellCapture capture={opts.capture} />
                </SidebarProvider>
              </ShellProvider>
            }
          />
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

describe("Triage group", () => {
  it("notifications trigger calls setNotificationsOpen(true)", () => {
    const capture: { current: ShellContextValue | null } = { current: null };
    renderSidebar({
      path: "/w/acme",
      routePath: "/w/:slug",
      seeds: [[["projects", "acme"], projects]],
      capture,
    });

    expect(capture.current?.notificationsOpen).toBe(false);
    fireEvent.click(screen.getByText("Notifications"));
    expect(capture.current?.notificationsOpen).toBe(true);
  });

  it("shows the unread badge", () => {
    const capture: { current: ShellContextValue | null } = { current: null };
    renderSidebar({
      path: "/w/acme",
      routePath: "/w/:slug",
      seeds: [[["projects", "acme"], projects]],
      capture,
    });
    expect(screen.getByText("3")).toBeTruthy();
  });

  it("links My Work to the current workspace", () => {
    const capture: { current: ShellContextValue | null } = { current: null };
    renderSidebar({
      path: "/w/acme",
      routePath: "/w/:slug",
      seeds: [[["projects", "acme"], projects]],
      capture,
    });
    const link = screen.getByText("My Work").closest("a");
    expect(link?.getAttribute("href")).toBe("/w/acme/my-work");
  });
});

describe("Favorites group", () => {
  it("is hidden when empty", () => {
    const capture: { current: ShellContextValue | null } = { current: null };
    renderSidebar({
      path: "/w/acme",
      routePath: "/w/:slug",
      seeds: [[["projects", "acme"], projects]],
      capture,
    });
    expect(screen.queryByText("Favorites")).toBeNull();
  });

  it("renders favorite issues with deep links when present", () => {
    const capture: { current: ShellContextValue | null } = { current: null };
    const favorites: FavoritesList = {
      issues: [
        {
          id: "i1",
          display_id: "ENG-1",
          name: "Fix the bug",
          workspace_slug: "acme",
          project_id: "p1",
          project_identifier: "ENG",
          project_name: "Engineering",
          starred_at: "",
        },
      ],
      projects: [],
    };
    renderSidebar({
      path: "/w/acme",
      routePath: "/w/:slug",
      seeds: [
        [favoriteKeys.all, favorites],
        [["projects", "acme"], projects],
      ],
      capture,
    });

    expect(screen.getByText("Favorites")).toBeTruthy();
    const link = screen.getByText("Fix the bug").closest("a");
    expect(link?.getAttribute("href")).toBe("/w/acme/p/ENG/i/i1");
  });
});

describe("Projects group", () => {
  it('shows "Select a workspace" when no slug is in the route', () => {
    const capture: { current: ShellContextValue | null } = { current: null };
    renderSidebar({ path: "/", routePath: "/", capture });

    const row = screen.getByText("Select a workspace");
    expect(row).toBeTruthy();
    expect(row.closest("a")?.getAttribute("href")).toBe("/w");
  });

  it("lists the workspace projects with links", () => {
    const capture: { current: ShellContextValue | null } = { current: null };
    renderSidebar({
      path: "/w/acme",
      routePath: "/w/:slug",
      seeds: [[["projects", "acme"], projects]],
      capture,
    });

    const row = screen.getByText("Engineering");
    expect(row).toBeTruthy();
    expect(row.closest("a")?.getAttribute("href")).toBe("/w/acme/p/ENG");
  });

  it("records the visited workspace slug", () => {
    const capture: { current: ShellContextValue | null } = { current: null };
    renderSidebar({
      path: "/w/acme",
      routePath: "/w/:slug",
      seeds: [[["projects", "acme"], projects]],
      capture,
    });
    expect(capture.current?.lastWorkspaceSlug).toBe("acme");
    expect(localStorage.getItem("glance:last-workspace")).toBe("acme");
  });
});

describe("workspace header", () => {
  it("shows the current workspace name", () => {
    const capture: { current: ShellContextValue | null } = { current: null };
    renderSidebar({
      path: "/w/acme",
      routePath: "/w/:slug",
      seeds: [[["projects", "acme"], projects]],
      capture,
    });
    expect(screen.getByText("Acme Corp")).toBeTruthy();
  });
});
