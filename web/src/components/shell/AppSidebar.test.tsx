// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import AppSidebar from "./AppSidebar";

import { ShellProvider, type ShellContextValue } from "./shell-context";
import { NOTIFICATION_KEYS } from "../../lib/notifications";
import { draftsKey } from "../../lib/drafts";
import type { Issue, Project, Workspace } from "../../lib/types";
import { workspacesKey } from "../../lib/useWorkspaces";
import { onShortcutAction } from "../../lib/shortcuts";
import { ShellCapture, makeQueryClient, stubMatchMedia } from "./test-utils";

const workspaces: Workspace[] = [
  {
    id: "w1",
    slug: "acme",
    name: "Acme Corp",
    role: 20,
    slack_configured: false,
    github_webhook_configured: false,
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

function renderSidebar(opts: {
  path: string;
  routePath: string;
  seeds?: [readonly unknown[], unknown][];
  capture: { current: ShellContextValue | null };
}) {
  const qc = makeQueryClient([
    [workspacesKey, workspaces],
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
                <AppSidebar />
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

describe("header", () => {
  it("shows the Work label and a collapse button that flips the preference", () => {
    const capture: { current: ShellContextValue | null } = { current: null };
    renderSidebar({
      path: "/w/acme",
      routePath: "/w/:slug",
      seeds: [[["projects", "acme"], projects]],
      capture,
    });

    expect(screen.getByText("Work")).toBeTruthy();
    expect(capture.current?.sidebarCollapsed).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "Collapse sidebar" }));
    expect(capture.current?.sidebarCollapsed).toBe(true);
    expect(localStorage.getItem("glance:sidebar-collapsed")).toBe("true");
  });
});

describe("new work item", () => {
  it('fires the "new-issue" shortcut action (same as pressing C)', () => {
    const capture: { current: ShellContextValue | null } = { current: null };
    renderSidebar({
      path: "/w/acme",
      routePath: "/w/:slug",
      seeds: [[["projects", "acme"], projects]],
      capture,
    });

    const spy = vi.fn();
    const off = onShortcutAction("new-issue", spy);
    try {
      // The section "+" buttons share the label; the main button is the
      // one with the C kbd chip.
      const btn = screen
        .getByText("New work item", { selector: "span" })
        .closest("button")!;
      fireEvent.click(btn);
      expect(spy).toHaveBeenCalledTimes(1);
    } finally {
      off();
    }
  });
});

describe("main nav", () => {
  function renderNav(path: string, routePath: string) {
    const capture: { current: ShellContextValue | null } = { current: null };
    renderSidebar({
      path,
      routePath,
      seeds: [
        [["projects", "acme"], projects],
        [draftsKey("acme", "ENG"), [{ id: "d1" }, { id: "d2" }] as Issue[]],
      ],
      capture,
    });
    return capture;
  }

  it("links Home, Your work, and Drafts", () => {
    renderNav("/w/acme/p/ENG", "/w/:slug/p/:identifier");

    expect(screen.getByText("Home").closest("a")?.getAttribute("href")).toBe(
      "/",
    );
    expect(
      screen.getByText("Your work").closest("a")?.getAttribute("href"),
    ).toBe("/w/acme/my-work");
    expect(
      screen.getByText("Drafts").closest("a")?.getAttribute("href"),
    ).toBe("/w/acme/p/ENG/drafts");
  });

  it("shows the drafts count badge for the current project", () => {
    renderNav("/w/acme/p/ENG", "/w/:slug/p/:identifier");
    const drafts = screen.getByText("Drafts").closest("a,button")!;
    expect(drafts.textContent).toContain("2");
  });

  it("marks the active nav link", () => {
    renderNav("/w/acme/my-work", "/w/:slug/my-work");
    expect(
      screen.getByText("Your work").closest("a")?.getAttribute("aria-current"),
    ).toBe("page");
  });
});

describe("workspace section", () => {
  it("inbox button opens the notifications sheet and shows the unread badge", () => {
    const capture: { current: ShellContextValue | null } = { current: null };
    renderSidebar({
      path: "/w/acme",
      routePath: "/w/:slug",
      seeds: [[["projects", "acme"], projects]],
      capture,
    });

    expect(capture.current?.notificationsOpen).toBe(false);
    const inbox = screen.getByRole("button", { name: /inbox/i });
    expect(inbox.textContent).toContain("3");
    fireEvent.click(inbox);
    expect(capture.current?.notificationsOpen).toBe(true);
  });

  it("links Views to the current project", () => {
    const capture: { current: ShellContextValue | null } = { current: null };
    const { container } = renderSidebar({
      path: "/w/acme/p/ENG",
      routePath: "/w/:slug/p/:identifier",
      seeds: [[["projects", "acme"], projects]],
      capture,
    });
    // "Views" also exists as a project sub-link — scope to the section.
    const section = container.querySelector(
      'section[aria-label="Workspace"]',
    ) as HTMLElement;
    expect(
      within(section).getByText("Views").closest("a")?.getAttribute("href"),
    ).toBe("/w/acme/p/ENG");
  });

  it("collapses and persists", () => {
    const capture: { current: ShellContextValue | null } = { current: null };
    renderSidebar({
      path: "/w/acme",
      routePath: "/w/:slug",
      seeds: [[["projects", "acme"], projects]],
      capture,
    });

    fireEvent.click(
      screen.getByRole("button", { name: /collapse workspace section/i }),
    );
    expect(screen.queryByRole("button", { name: /inbox/i })).toBeNull();
    expect(localStorage.getItem("glance:workspace-section-collapsed")).toBe(
      "true",
    );
  });
});

describe("projects section", () => {
  function renderProjects(path: string, routePath: string) {
    const capture: { current: ShellContextValue | null } = { current: null };
    const result = renderSidebar({
      path,
      routePath,
      seeds: [[["projects", "acme"], projects]],
      capture,
    });
    return { capture, container: result.container };
  }

  it("expands the current project with the 9 sub-links", () => {
    const { container } = renderProjects("/w/acme/p/ENG", "/w/:slug/p/:identifier");
    // Scope to the Projects section ("Views" also lives in Workspace).
    const section = within(
      container.querySelector('section[aria-label="Projects"]') as HTMLElement,
    );

    expect(screen.getByText("Engineering")).toBeTruthy();
    for (const label of [
      "Overview",
      "Work items",
      "Cycles",
      "Modules",
      "Releases",
      "Schedules",
      "Views",
      "Pages",
      "Intake",
    ]) {
      expect(section.getByText(label)).toBeTruthy();
    }
    expect(
      section.getByText("Work items").closest("a")?.getAttribute("href"),
    ).toBe("/w/acme/p/ENG");
    expect(
      section.getByText("Cycles").closest("a")?.getAttribute("href"),
    ).toBe("/w/acme/p/ENG/cycles");
  });

  it("marks the active sub-link", () => {
    renderProjects("/w/acme/p/ENG/board", "/w/:slug/p/:identifier/board");
    // Board is a work-items view: "Work items" takes the active state.
    expect(
      screen.getByText("Work items").closest("a")?.getAttribute("aria-current"),
    ).toBe("page");
  });

  it("toggles a project row open and closed", () => {
    renderProjects("/w/acme", "/w/:slug");
    // Not in a project route: sub-links hidden until expanded.
    expect(screen.queryByText("Work items")).toBeNull();

    fireEvent.click(
      screen.getByRole("button", { name: /expand engineering/i }),
    );
    expect(screen.getByText("Work items")).toBeTruthy();

    fireEvent.click(
      screen.getByRole("button", { name: /collapse engineering/i }),
    );
    expect(screen.queryByText("Work items")).toBeNull();
  });

  it("records the visited workspace slug", () => {
    const { capture } = renderProjects("/w/acme", "/w/:slug");
    expect(capture.current?.lastWorkspaceSlug).toBe("acme");
    expect(localStorage.getItem("glance:last-workspace")).toBe("acme");
  });

  it('shows "Select a workspace" when no slug is in the route', () => {
    const capture: { current: ShellContextValue | null } = { current: null };
    renderSidebar({ path: "/", routePath: "/", capture });

    const row = screen.getByText("Select a workspace");
    expect(row.closest("a")?.getAttribute("href")).toBe("/w");
  });
});
