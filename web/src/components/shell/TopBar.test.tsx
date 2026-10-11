// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import TopBar from "./TopBar";
import { ShellProvider, type ShellContextValue } from "./shell-context";
import { onShortcutAction } from "../../lib/shortcuts";
import type { Workspace } from "../../lib/types";
import { workspacesKey } from "../../lib/useWorkspaces";
import { ShellCapture, makeQueryClient, stubMatchMedia } from "./test-utils";

// The topbar avatar reads useAuth; stub it.
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

const workspaces: Workspace[] = [
  {
    id: "w1",
    slug: "acme",
    name: "Acme Corp",
    role: 20,
    slack_configured: false,
    created_at: "",
    updated_at: "",
  },
];

function renderTopBar(opts: {
  path: string;
  routePath: string;
  capture: { current: ShellContextValue | null };
  collapsed?: boolean;
}) {
  if (opts.collapsed) {
    localStorage.setItem("glance:sidebar-collapsed", "true");
  }
  const qc = makeQueryClient([[workspacesKey, workspaces]]);
  return render(
    <MemoryRouter initialEntries={[opts.path]}>
      <QueryClientProvider client={qc}>
        <Routes>
          <Route
            path={opts.routePath}
            element={
              <ShellProvider>
                <TopBar />
                <ShellCapture capture={opts.capture} />
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

describe("search trigger", () => {
  it("opens on every route (global palette, not project-scoped)", () => {
    const capture: { current: ShellContextValue | null } = { current: null };
    // Home route — the old topbar hid the trigger off project routes.
    renderTopBar({ path: "/", routePath: "/", capture });

    const spy = vi.fn();
    const off = onShortcutAction("toggle-palette", spy);
    try {
      fireEvent.click(
        screen.getByRole("button", { name: /open command palette/i }),
      );
      expect(spy).toHaveBeenCalledTimes(1);
    } finally {
      off();
    }
  });
});

describe("workspace switcher", () => {
  it("shows the current workspace name", () => {
    const capture: { current: ShellContextValue | null } = { current: null };
    renderTopBar({
      path: "/w/acme/p/ENG",
      routePath: "/w/:slug/p/:identifier",
      capture,
    });
    expect(screen.getByLabelText("Switch workspace")).toHaveTextContent(
      "Acme Corp",
    );
  });
});

describe("right cluster", () => {
  function renderCluster() {
    const capture: { current: ShellContextValue | null } = { current: null };
    renderTopBar({
      path: "/w/acme/p/ENG",
      routePath: "/w/:slug/p/:identifier",
      capture,
    });
    return capture;
  }

  it("links Get started to onboarding", () => {
    renderCluster();
    expect(screen.getByText("Get started").getAttribute("href")).toBe(
      "/onboarding",
    );
  });

  it("inbox opens the notifications sheet", () => {
    const capture = renderCluster();
    expect(capture.current?.notificationsOpen).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "Inbox" }));
    expect(capture.current?.notificationsOpen).toBe(true);
  });

  it("help opens the shortcut cheatsheet", () => {
    renderCluster();
    const spy = vi.fn();
    const off = onShortcutAction("open-cheatsheet", spy);
    try {
      fireEvent.click(screen.getByRole("button", { name: "Help" }));
      expect(spy).toHaveBeenCalledTimes(1);
    } finally {
      off();
    }
  });

  it("AI opens the command palette", () => {
    renderCluster();
    const spy = vi.fn();
    const off = onShortcutAction("toggle-palette", spy);
    try {
      fireEvent.click(screen.getByRole("button", { name: "AI assistant" }));
      expect(spy).toHaveBeenCalledTimes(1);
    } finally {
      off();
    }
  });

  it("shows the user avatar with the account menu", () => {
    renderCluster();
    expect(
      screen.getByRole("button", { name: /account: test user/i }),
    ).toBeTruthy();
  });
});

describe("sidebar reopen", () => {
  it("appears inside the switcher when collapsed and reopens", () => {
    const capture: { current: ShellContextValue | null } = { current: null };
    renderTopBar({
      path: "/w/acme/p/ENG",
      routePath: "/w/:slug/p/:identifier",
      capture,
      collapsed: true,
    });

    expect(capture.current?.sidebarCollapsed).toBe(true);
    const reopen = screen.getByRole("button", { name: "Open sidebar" });
    fireEvent.click(reopen);
    expect(capture.current?.sidebarCollapsed).toBe(false);
    expect(screen.queryByRole("button", { name: "Open sidebar" })).toBeNull();
  });

  it("is hidden while expanded", () => {
    const capture: { current: ShellContextValue | null } = { current: null };
    renderTopBar({
      path: "/w/acme/p/ENG",
      routePath: "/w/:slug/p/:identifier",
      capture,
    });
    expect(
      screen.queryByRole("button", { name: "Open sidebar" }),
    ).toBeNull();
  });
});
