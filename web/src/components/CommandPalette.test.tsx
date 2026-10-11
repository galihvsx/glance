// @vitest-environment jsdom
// Component tests for the global command palette (C18T0): result grouping,
// keyboard-driven display-ID jump (incl. cross-project), empty states, and
// the defensive archived-project exclusion.

import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import CommandPalette from "./CommandPalette";
import { ShellProvider } from "./shell/shell-context";
import { emitShortcutAction } from "../lib/shortcuts";
import { api } from "../lib/api";
import { makeQueryClient, stubMatchMedia } from "./shell/test-utils";
import type { Project, SearchIssueItem } from "../lib/types";

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

// cmdk's CommandList measures itself with ResizeObserver and scrolls the
// active item into view — neither exists in jsdom.
class ResizeObserverStub {
  observe(): void {}
  unobserve(): void {}
  disconnect(): void {}
}
vi.stubGlobal("ResizeObserver", ResizeObserverStub);
if (typeof Element !== "undefined" && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {};
}

const get = () => api.get as unknown as Mock;

const projects: Project[] = [
  {
    id: "p-eng",
    workspace_id: "w1",
    identifier: "ENG",
    name: "Engineering",
    description: "",
    created_at: "",
    updated_at: "",
  },
  {
    id: "p-plt",
    workspace_id: "w1",
    identifier: "PLT",
    name: "Platform",
    description: "",
    created_at: "",
    updated_at: "",
  },
];

function hit(
  overrides: Partial<SearchIssueItem> & { id: string },
): SearchIssueItem {
  return {
    project_id: "p-eng",
    sequence_id: 1,
    display_id: "ENG-1",
    name: "Hit",
    priority: 0,
    state_id: "s1",
    sort_order: 0,
    is_draft: false,
    created_by: "u1",
    created_at: "",
    updated_at: "",
    assignees: [],
    labels: [],
    workspace_slug: "acme",
    project_identifier: "ENG",
    ...overrides,
  } as SearchIssueItem;
}

interface LocationState {
  pathname: string;
  state: unknown;
}

function renderPalette(path = "/w/acme/p/ENG") {
  const qc = makeQueryClient([[["projects", "acme"], projects]]);
  const locationRef: { current: LocationState } = {
    current: { pathname: "", state: null },
  };
  function Spy() {
    const loc = useLocation();
    locationRef.current = { pathname: loc.pathname, state: loc.state };
    return null;
  }
  render(
    <MemoryRouter initialEntries={[path]}>
      <QueryClientProvider client={qc}>
        <ShellProvider>
          <Routes>
            <Route
              path="/w/:slug/p/:identifier/*"
              element={
                <>
                  <CommandPalette />
                  <Spy />
                </>
              }
            />
            <Route path="*" element={<Spy />} />
          </Routes>
        </ShellProvider>
      </QueryClientProvider>
    </MemoryRouter>,
  );
  return locationRef;
}

function openPalette(): void {
  act(() => {
    emitShortcutAction("toggle-palette");
  });
}

function paletteInput(): HTMLElement {
  return screen.getByPlaceholderText(
    "Search issues across projects, or type ENG-123 to jump…",
  );
}

beforeEach(() => {
  stubMatchMedia(false);
  localStorage.clear();
  vi.clearAllMocks();
  // Default: search resolves empty; individual tests override.
  get().mockImplementation((path: string) => {
    if (path.startsWith("/api/v1/search"))
      return Promise.resolve({ results: [] });
    return Promise.resolve({ results: [] });
  });
});

afterEach(() => {
  cleanup();
});

describe("global command palette", () => {
  it("groups search results by project, current project first", async () => {
    get().mockImplementation((path: string) => {
      if (!path.startsWith("/api/v1/search"))
        return Promise.resolve({ results: [] });
      return Promise.resolve({
        results: [
          hit({
            id: "i-plt-2",
            display_id: "PLT-2",
            name: "Platform crash",
            project_id: "p-plt",
            project_identifier: "PLT",
          }),
          hit({ id: "i-eng-1", display_id: "ENG-1", name: "Eng crash" }),
        ],
      });
    });
    renderPalette();
    openPalette();
    fireEvent.change(paletteInput(), { target: { value: "crash" } });

    await screen.findByText("Engineering");
    await screen.findByText("Platform");
    await screen.findByText("Eng crash");
    await screen.findByText("Platform crash");

    // Current project (ENG) group renders before the PLT group.
    const list = document.querySelector('[data-slot="command-list"]')!;
    expect(list.innerHTML.indexOf("Eng crash")).toBeLessThan(
      list.innerHTML.indexOf("Platform crash"),
    );
  });

  it("jumps by display ID across projects without waiting for search", async () => {
    get().mockImplementation((path: string) => {
      if (path.startsWith("/api/v1/search"))
        return Promise.resolve({ results: [] });
      if (path.includes("/projects/PLT/issues"))
        return Promise.resolve({
          results: [
            {
              id: "i-plt-7",
              display_id: "PLT-7",
              name: "Platform bug",
            },
          ],
        });
      return Promise.resolve({ results: [] });
    });
    const locationRef = renderPalette();
    openPalette();
    const input = paletteInput();
    fireEvent.change(input, { target: { value: "PLT-7" } });

    // Fast-path row is pinned at the top.
    await screen.findByText("Jump directly to this issue");
    expect(get()).not.toHaveBeenCalledWith(
      expect.stringContaining("/api/v1/search"),
    );

    // Enter activates the auto-selected fast-path item.
    fireEvent.keyDown(input, { key: "Enter", code: "Enter" });
    await waitFor(() => {
      expect(locationRef.current.pathname).toBe("/w/acme/p/PLT/i/i-plt-7");
    });
  });

  it("shows an error when the display ID resolves to nothing", async () => {
    get().mockResolvedValue({ results: [] });
    renderPalette();
    openPalette();
    const input = paletteInput();
    fireEvent.change(input, { target: { value: "ZZZ-9" } });

    await screen.findByText("Jump directly to this issue");
    fireEvent.keyDown(input, { key: "Enter", code: "Enter" });
    await screen.findByText("No issue ZZZ-9 in this workspace.");
  });

  it("shows an empty state when search returns nothing", async () => {
    get().mockImplementation(() => Promise.resolve({ results: [] }));
    renderPalette();
    openPalette();
    fireEvent.change(paletteInput(), { target: { value: "no-such-thing" } });
    await screen.findByText("No results for “no-such-thing”.");
  });

  it("excludes hits from archived projects (defensive, pre-C18T3 payload)", async () => {
    const withArchived = [
      projects[0],
      { ...projects[1], archived_at: "2026-01-01T00:00:00Z" } as Project,
    ];
    const qc = makeQueryClient([[["projects", "acme"], withArchived]]);
    function Spy() {
      return null;
    }
    render(
      <MemoryRouter initialEntries={["/w/acme/p/ENG"]}>
        <QueryClientProvider client={qc}>
          <ShellProvider>
            <Routes>
              <Route
                path="/w/:slug/p/:identifier/*"
                element={
                  <>
                    <CommandPalette />
                    <Spy />
                  </>
                }
              />
            </Routes>
          </ShellProvider>
        </QueryClientProvider>
      </MemoryRouter>,
    );
    get().mockImplementation((path: string) => {
      if (!path.startsWith("/api/v1/search"))
        return Promise.resolve({ results: [] });
      return Promise.resolve({
        results: [
          hit({
            id: "i-plt-3",
            display_id: "PLT-3",
            name: "Archived project hit",
            project_id: "p-plt",
            project_identifier: "PLT",
          }),
          hit({ id: "i-eng-4", display_id: "ENG-4", name: "Live hit" }),
        ],
      });
    });
    openPalette();
    fireEvent.change(paletteInput(), { target: { value: "hit" } });

    await screen.findByText("Live hit");
    await waitFor(() => {
      expect(get()).toHaveBeenCalledWith(
        expect.stringContaining("/api/v1/search"),
      );
    });
    expect(screen.queryByText("Archived project hit")).toBeNull();
    expect(screen.queryByText("Platform")).toBeNull();
  });

  it("shows workspace-scoped recent issues when idle", async () => {
    localStorage.setItem(
      "glance:recents",
      JSON.stringify([
        {
          id: "r1",
          name: "Old bug",
          display_id: "ENG-5",
          slug: "acme",
          identifier: "ENG",
          visited_at: "2026-10-11T00:00:00Z",
        },
        {
          id: "r2",
          name: "Other workspace bug",
          display_id: "EXT-1",
          slug: "other",
          identifier: "EXT",
          visited_at: "2026-10-11T00:00:00Z",
        },
      ]),
    );
    renderPalette();
    openPalette();

    await screen.findByText("Recent issues");
    expect(screen.getByText("Old bug")).toBeTruthy();
    expect(screen.getByText("ENG-5")).toBeTruthy();
    // Other workspaces' recents are not shown.
    expect(screen.queryByText("Other workspace bug")).toBeNull();
  });

  it("create issue opens a project picker that navigates with newIssue state", async () => {
    const locationRef = renderPalette();
    openPalette();

    fireEvent.click(await screen.findByText("Create issue"));
    const pickerInput = await screen.findByPlaceholderText(
      "Select a project for the new issue…",
    );
    expect(pickerInput).toBeTruthy();
    // Current project pinned first.
    const list = document.querySelector('[data-slot="command-list"]')!;
    expect(list.innerHTML.indexOf("Engineering")).toBeLessThan(
      list.innerHTML.indexOf("Platform"),
    );

    fireEvent.click(screen.getByText("Platform"));
    await waitFor(() => {
      expect(locationRef.current.pathname).toBe("/w/acme/p/PLT");
    });
    expect(locationRef.current.state).toEqual({ newIssue: true });
  });

  it("Backspace on the empty picker input returns to the root page", async () => {
    renderPalette();
    openPalette();

    fireEvent.click(await screen.findByText("Create issue"));
    await screen.findByPlaceholderText("Select a project for the new issue…");

    const pickerInput = screen.getByPlaceholderText(
      "Select a project for the new issue…",
    );
    fireEvent.keyDown(pickerInput, { key: "Backspace", code: "Backspace" });
    await screen.findByPlaceholderText(
      "Search issues across projects, or type ENG-123 to jump…",
    );
  });

  it("keyboard: ArrowDown + Enter opens a search hit", async () => {
    get().mockImplementation((path: string) => {
      if (!path.startsWith("/api/v1/search"))
        return Promise.resolve({ results: [] });
      return Promise.resolve({
        results: [
          hit({ id: "i-eng-9", display_id: "ENG-9", name: "Ninth bug" }),
        ],
      });
    });
    const locationRef = renderPalette();
    openPalette();
    const input = paletteInput();
    fireEvent.change(input, { target: { value: "ninth" } });

    await screen.findByText("Ninth bug");
    fireEvent.keyDown(input, { key: "ArrowDown", code: "ArrowDown" });
    fireEvent.keyDown(input, { key: "Enter", code: "Enter" });
    await waitFor(() => {
      expect(locationRef.current.pathname).toBe("/w/acme/p/ENG/i/i-eng-9");
    });
  });
});
