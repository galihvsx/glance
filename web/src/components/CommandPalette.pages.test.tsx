// @vitest-environment jsdom
// Component tests for the command palette Pages section (C66T1):
// the "Pages" group renders global-search page hits under the issue
// groups, selects a page by deep-linking to the Pages view with the page
// selected (location state { pageId }, per the C36T1 contract Pages.tsx
// already honors), stays hidden when there are no page hits, and stays
// defensive about malformed payloads.

import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import CommandPalette from "./CommandPalette";
import { ShellProvider } from "./shell/shell-context";
import { emitShortcutAction } from "../lib/shortcuts";
import { api } from "../lib/api";
import { makeQueryClient, stubMatchMedia } from "./shell/test-utils";
import type { PageSearchResult, Project } from "../lib/types";

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
    labels: [],
    name: "Engineering",
    description: "",
    created_at: "",
    updated_at: "",
    parallel_cycles: false,
  },
  {
    id: "p-plt",
    workspace_id: "w1",
    identifier: "PLT",
    labels: [],
    name: "Platform",
    description: "",
    created_at: "",
    updated_at: "",
    parallel_cycles: false,
  },
];

function pageHit(overrides: Partial<PageSearchResult> & { id: string }): PageSearchResult {
  return {
    title: "Hit",
    project_identifier: "ENG",
    workspace_slug: "acme",
    ...overrides,
  };
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
      return Promise.resolve({ results: [], page_results: [] });
    return Promise.resolve({ results: [] });
  });
});

afterEach(() => {
  cleanup();
});

describe("command palette Pages section", () => {
  it("renders a Pages group with page hits under the issue groups", async () => {
    get().mockImplementation((path: string) => {
      if (!path.startsWith("/api/v1/search"))
        return Promise.resolve({ results: [] });
      return Promise.resolve({
        results: [],
        page_results: [
          pageHit({ id: "pg-1", title: "Onboarding guide" }),
          pageHit({
            id: "pg-2",
            title: "Guide to releases",
            project_identifier: "PLT",
          }),
        ],
      });
    });
    renderPalette();
    openPalette();
    fireEvent.change(paletteInput(), { target: { value: "guide" } });

    await screen.findByText("Pages");
    await screen.findByText("Onboarding guide");
    await screen.findByText("Guide to releases");
    // Rows carry the project context, like the issue rows' identifier badge.
    expect(screen.getAllByText("ENG")).toHaveLength(1);
    expect(screen.getAllByText("PLT")).toHaveLength(1);
  });

  it("shows no Pages group when page_results is empty", async () => {
    get().mockImplementation((path: string) => {
      if (!path.startsWith("/api/v1/search"))
        return Promise.resolve({ results: [] });
      return Promise.resolve({ results: [], page_results: [] });
    });
    renderPalette();
    openPalette();
    fireEvent.change(paletteInput(), { target: { value: "nothing" } });

    await screen.findByText("No results for “nothing”.");
    expect(screen.queryByText("Pages")).toBeNull();
  });

  it("selecting a page navigates to the Pages view with that page selected", async () => {
    get().mockImplementation((path: string) => {
      if (!path.startsWith("/api/v1/search"))
        return Promise.resolve({ results: [] });
      return Promise.resolve({
        results: [],
        page_results: [pageHit({ id: "pg-9", title: "Deep-linked page" })],
      });
    });
    const locationRef = renderPalette();
    openPalette();
    const input = paletteInput();
    fireEvent.change(input, { target: { value: "deep" } });

    // Click the row directly: cmdk's fuzzy filter can match command
    // values too ("deep" matches "keyboard shortcuts cheatsheet help"),
    // so Enter-selection is not deterministic here.
    fireEvent.click(await screen.findByText("Deep-linked page"));
    await waitFor(() => {
      expect(locationRef.current.pathname).toBe("/w/acme/p/ENG/pages");
    });
    // Pages.tsx honors location state { pageId } (C36T1): the page opens
    // selected in the pages tree.
    expect(locationRef.current.state).toEqual({ pageId: "pg-9" });
  });

  it("does not crash on page hits with missing or extra fields", async () => {
    get().mockImplementation((path: string) => {
      if (!path.startsWith("/api/v1/search"))
        return Promise.resolve({ results: [] });
      return Promise.resolve({
        results: [],
        // Extra fields are ignored; missing title falls back gracefully.
        page_results: [
          { id: "pg-x", workspace_slug: "acme", rank: 0.5, extra: true },
          pageHit({ id: "pg-ok", title: "Fine page" }),
        ],
      });
    });
    renderPalette();
    openPalette();
    fireEvent.change(paletteInput(), { target: { value: "fine" } });

    await screen.findByText("Pages");
    await screen.findByText("Fine page");
    // The malformed hit renders (empty title slot), not a crash.
    expect(screen.queryByText("Pages")).toBeTruthy();
  });

  it("issue groups are unaffected when page_results accompany issue hits", async () => {
    get().mockImplementation((path: string) => {
      if (!path.startsWith("/api/v1/search"))
        return Promise.resolve({ results: [] });
      return Promise.resolve({
        results: [
          {
            id: "i-eng-1",
            display_id: "ENG-1",
            name: "Eng bug",
            project_id: "p-eng",
            project_identifier: "ENG",
            workspace_slug: "acme",
          },
        ],
        page_results: [pageHit({ id: "pg-1", title: "A page" })],
      });
    });
    renderPalette();
    openPalette();
    fireEvent.change(paletteInput(), { target: { value: "eng" } });

    // Both groups render; Pages sits below the issue groups.
    await screen.findByText("Engineering");
    await screen.findByText("Eng bug");
    await screen.findByText("Pages");
    await screen.findByText("A page");
    const list = document.querySelector('[data-slot="command-list"]')!;
    expect(list.innerHTML.indexOf("Eng bug")).toBeLessThan(
      list.innerHTML.indexOf("A page"),
    );
  });

  it("excludes page hits from archived projects (defensive, like issues)", async () => {
    const withArchived = [
      projects[0],
      { ...projects[1], archived_at: "2026-01-01T00:00:00Z" } as Project,
    ];
    const qc = makeQueryClient([[["projects", "acme"], withArchived]]);
    render(
      <MemoryRouter initialEntries={["/w/acme/p/ENG"]}>
        <QueryClientProvider client={qc}>
          <ShellProvider>
            <Routes>
              <Route
                path="/w/:slug/p/:identifier/*"
                element={<CommandPalette />}
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
        results: [],
        page_results: [
          pageHit({
            id: "pg-plt",
            title: "Archived project page",
            project_identifier: "PLT",
          }),
          pageHit({ id: "pg-eng", title: "Live project page" }),
        ],
      });
    });
    openPalette();
    fireEvent.change(paletteInput(), { target: { value: "page" } });

    await screen.findByText("Live project page");
    expect(screen.queryByText("Archived project page")).toBeNull();
  });
});
