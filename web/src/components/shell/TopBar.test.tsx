// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import TopBar from "./TopBar";
import { ShellProvider, type ShellContextValue } from "./shell-context";
import { SidebarProvider } from "../ui/sidebar";
import { onShortcutAction } from "../../lib/shortcuts";
import type { Workspace } from "../../lib/types";
import { workspacesKey } from "../../lib/useWorkspaces";
import { ShellCapture, makeQueryClient, stubMatchMedia } from "./test-utils";

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
  mobile: boolean;
  capture: { current: ShellContextValue | null };
}) {
  const qc = makeQueryClient([[workspacesKey, workspaces]]);
  return render(
    <MemoryRouter initialEntries={[opts.path]}>
      <QueryClientProvider client={qc}>
        <Routes>
          <Route
            path={opts.routePath}
            element={
              <ShellProvider>
                <SidebarProvider>
                  <TopBar />
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
  Object.defineProperty(window, "innerWidth", {
    value: 1024,
    writable: true,
    configurable: true,
  });
});

beforeEach(() => {
  localStorage.clear();
  stubMatchMedia(false);
});

describe("command palette button", () => {
  it('fires the "toggle-palette" shortcut action', () => {
    const capture: { current: ShellContextValue | null } = { current: null };
    renderTopBar({
      path: "/w/acme/p/ENG",
      routePath: "/w/:slug/p/:identifier",
      mobile: false,
      capture,
    });

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

describe("context label", () => {
  it("shows workspace name and project identifier", () => {
    const capture: { current: ShellContextValue | null } = { current: null };
    renderTopBar({
      path: "/w/acme/p/ENG",
      routePath: "/w/:slug/p/:identifier",
      mobile: false,
      capture,
    });
    expect(screen.getByText("Acme Corp / ENG")).toBeTruthy();
  });
});

describe("sidebar trigger", () => {
  it("opens the mobile drawer via setMobileOpen on mobile", () => {
    // The vendored shadcn sidebar keys its mobile Sheet off a 768px
    // breakpoint read from window.innerWidth.
    Object.defineProperty(window, "innerWidth", {
      value: 500,
      writable: true,
      configurable: true,
    });
    stubMatchMedia(true);
    const capture: { current: ShellContextValue | null } = { current: null };
    renderTopBar({
      path: "/w/acme/p/ENG",
      routePath: "/w/:slug/p/:identifier",
      mobile: true,
      capture,
    });

    expect(capture.current?.mobileOpen).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: /toggle sidebar/i }));
    expect(capture.current?.mobileOpen).toBe(true);
  });
});
