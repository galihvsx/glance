// @vitest-environment jsdom
// Saved views on Drafts (C111T4): the detail-mode SavedViewsMenu
// beside the All-drafts / PQL toggle, applying a view through the
// page's existing probe-first handlePqlApply (which already carries
// draft=true). A view apply never shows published issues — draft
// mode is the scope, same as a typed query: the server-side
// is_draft selection applies to view applies exactly as to typed
// queries (no client-side filter is involved). Follows the
// GanttSavedViews.test.tsx shape plus the EpicDetailPql.test.tsx
// saved-view idiom (mocked useSavedViews hook, so the page tests
// control the view list without a views endpoint).

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import Drafts from "./Drafts";
import { api, ApiError } from "../lib/api";
import { EMPTY_FILTERS } from "../lib/filters";
import type { Issue } from "../lib/types";
import {
  makeQueryClient,
  stubMatchMedia,
} from "../components/shell/test-utils";

// C108T3: the detail-mode menu reads views through this hook; mock
// it (same pattern as EpicDetailPql.test.tsx) so page tests control
// the view list without a views endpoint.
vi.mock("../components/issue/useSavedViews", () => ({
  useSavedViews: vi.fn(),
}));
import { useSavedViews } from "../components/issue/useSavedViews";
import type { SavedView } from "../components/issue/useSavedViews";

const mockUseSavedViews = vi.mocked(useSavedViews);

function mockViews(ownViews: SavedView[]) {
  mockUseSavedViews.mockReturnValue({
    views: ownViews,
    ownViews,
    sharedViews: [],
    viewsLoading: false,
    viewsReady: true,
    viewsError: false,
    createView: vi.fn(),
    renameView: vi.fn(),
    deleteView: vi.fn(),
    setDefaultView: vi.fn(),
    setViewShared: vi.fn(),
  } as ReturnType<typeof useSavedViews>);
}

const BASE = "/api/v1/workspaces/acme/projects/ENG";

function makeIssue(
  id: string,
  displayId: string,
  name: string,
  isDraft: boolean,
): Issue {
  return {
    id,
    project_id: "p-eng",
    sequence_id: 1,
    display_id: displayId,
    name,
    priority: 3,
    state_id: "s-1",
    sort_order: 0,
    is_draft: isDraft,
    created_by: "u-1",
    created_at: "2026-11-01T00:00:00Z",
    updated_at: "2026-11-01T00:00:00Z",
    assignees: [],
    labels: [],
    target_date: null,
  };
}

const draftA = makeIssue("i-a", "ENG-1", "Draft alpha", true);
const draftB = makeIssue("i-b", "ENG-2", "Draft beta", true);
const published = makeIssue("i-c", "ENG-3", "Published gamma", false);

const savedAlpha: SavedView = {
  id: "v-alpha",
  name: "Alpha drafts",
  filters: { ...EMPTY_FILTERS, pql: "name ~ alpha" },
  display: null,
  isDefault: false,
  shared: false,
  ownerId: "me",
  ownerName: "Me",
};
const savedBeta: SavedView = {
  id: "v-beta",
  name: "Beta drafts",
  filters: { ...EMPTY_FILTERS, pql: "name ~ beta" },
  display: null,
  isDefault: false,
  shared: false,
  ownerId: "me",
  ownerName: "Me",
};
const savedBogus: SavedView = {
  id: "v-bogus",
  name: "Bogus query",
  filters: { ...EMPTY_FILTERS, pql: "bogus = 1" },
  display: null,
  isDefault: false,
  shared: false,
  ownerId: "me",
  ownerName: "Me",
};

function renderPage(ownViews: SavedView[] = []) {
  mockViews(ownViews);
  const get = vi.spyOn(api, "get").mockImplementation((path: string) => {
    if (path === "/api/v1/workspaces")
      return Promise.resolve({ workspaces: [] });
    // Project query for the detail-mode SavedViewsMenu (shared
    // ["project", slug, identifier] key).
    if (path === BASE) return Promise.resolve({ id: "p-eng" });
    if (path.startsWith(`${BASE}/issues`)) {
      const qs = new URLSearchParams(path.split("?")[1] ?? "");
      if (qs.get("draft") !== "true") {
        // The page never issues a non-draft call; if it ever did,
        // the published issue would be reachable — fail loudly.
        return Promise.resolve({ results: [published], next_cursor: null });
      }
      const pql = qs.get("pql");
      if (pql !== null) {
        if (pql.includes("bogus")) {
          return Promise.reject(
            new ApiError(400, "unknown field: bogus", "validation_error", {
              position: 1,
            }),
          );
        }
        if (pql.includes("beta")) {
          return Promise.resolve({ results: [draftB], next_cursor: null });
        }
        if (pql.includes("alpha")) {
          return Promise.resolve({ results: [draftA], next_cursor: null });
        }
        return Promise.resolve({
          results: [draftA, draftB],
          next_cursor: null,
        });
      }
      return Promise.resolve({
        results: [draftA, draftB],
        next_cursor: null,
      });
    }
    return Promise.reject(new Error(`unexpected GET ${path}`));
  });
  const qc = makeQueryClient();
  render(
    <MemoryRouter initialEntries={["/w/acme/p/ENG/drafts"]}>
      <QueryClientProvider client={qc}>
        <Routes>
          <Route path="/w/:slug/p/:identifier/drafts" element={<Drafts />} />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
  return { get };
}

async function waitForDrafts() {
  await screen.findByText("Draft alpha");
  await screen.findByText("Draft beta");
}

async function openSavedViewsMenu() {
  fireEvent.click(screen.getByRole("button", { name: "Saved views" }));
  await screen.findByRole("button", { name: "Save current view" });
}

function issueCalls(get: ReturnType<typeof renderPage>["get"]) {
  return get.mock.calls
    .map((c) => c[0] as string)
    .filter((p) => p.includes("/issues"))
    .map((p) => new URLSearchParams(p.split("?")[1] ?? ""));
}

beforeEach(() => {
  mockViews([]);
  stubMatchMedia(false);
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("drafts saved views (C111T4)", () => {
  it("applying a PQL view enters PQL mode with the view's query applied and published issues never shown", async () => {
    const { get } = renderPage([savedBeta]);
    await waitForDrafts();
    await openSavedViewsMenu();
    fireEvent.click(screen.getByTitle("Apply view “Beta drafts”"));
    await waitFor(() => {
      expect(screen.queryByText("Draft alpha")).toBeNull();
      expect(screen.getByText("Draft beta")).toBeTruthy();
    });
    expect(screen.queryByText("Published gamma")).toBeNull();
    expect(
      screen.getByRole("button", { name: "PQL" }),
    ).toHaveAttribute("aria-pressed", "true");
    // Editor mounts seeded with the applied query (keyed remount).
    expect(
      (screen.getByLabelText("PQL query") as HTMLTextAreaElement).value,
    ).toBe("name ~ beta");
    // Every PQL fetch carries draft=true + pql + per_page=100, so
    // the server-side draft selection is what keeps published
    // issues out — no client-side filter is involved.
    const pqlCalls = issueCalls(get).filter((qs) => qs.get("pql") !== null);
    // Probe-first apply (1 probe) + the PQL-mode fetch (1).
    expect(pqlCalls).toHaveLength(2);
    for (const qs of pqlCalls) {
      expect(qs.get("draft")).toBe("true");
      expect(qs.get("pql")).toBe("name ~ beta");
      expect(qs.get("per_page")).toBe("100");
    }
  });

  it("applying a view while already in PQL mode re-seeds the editor", async () => {
    renderPage([savedAlpha, savedBeta]);
    await waitForDrafts();
    await openSavedViewsMenu();
    fireEvent.click(screen.getByTitle("Apply view “Alpha drafts”"));
    await waitFor(() => {
      expect(screen.queryByText("Draft beta")).toBeNull();
      expect(screen.getByText("Draft alpha")).toBeTruthy();
    });
    expect(
      (screen.getByLabelText("PQL query") as HTMLTextAreaElement).value,
    ).toBe("name ~ alpha");
    // Already in PQL mode: without the applied-query key the editor
    // draft would stay on the first query (C107 lesson).
    await openSavedViewsMenu();
    fireEvent.click(screen.getByTitle("Apply view “Beta drafts”"));
    await waitFor(() => {
      expect(screen.queryByText("Draft alpha")).toBeNull();
      expect(screen.getByText("Draft beta")).toBeTruthy();
    });
    expect(
      (screen.getByLabelText("PQL query") as HTMLTextAreaElement).value,
    ).toBe("name ~ beta");
  });

  it("a view whose query fails its probe surfaces the error with the displayed set untouched", async () => {
    const { get } = renderPage([savedBogus]);
    await waitForDrafts();
    await openSavedViewsMenu();
    fireEvent.click(screen.getByTitle("Apply view “Bogus query”"));
    // The probe 400 surfaces at the detail level (the menu path has
    // no editor inline error) and the displayed set never changes.
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("unknown field: bogus");
    await waitForDrafts();
    // PQL mode was entered (editor visible) but nothing committed:
    // the editor mounts with an empty draft, and no fetch beyond
    // the single failed probe carries the bogus query.
    expect(screen.getByLabelText("PQL query")).toBeTruthy();
    expect(
      (screen.getByLabelText("PQL query") as HTMLTextAreaElement).value,
    ).toBe("");
    expect(
      issueCalls(get).filter((qs) => qs.get("pql") !== null),
    ).toHaveLength(1);
    // No unhandled rejection: the handler catches the probe failure
    // internally (a rejection here would fail the run).
  });

  it("leaving PQL mode after a view apply restores the full draft set", async () => {
    renderPage([savedBeta]);
    await waitForDrafts();
    await openSavedViewsMenu();
    fireEvent.click(screen.getByTitle("Apply view “Beta drafts”"));
    await waitFor(() => {
      expect(screen.queryByText("Draft alpha")).toBeNull();
      expect(screen.getByText("Draft beta")).toBeTruthy();
    });
    fireEvent.click(screen.getByRole("button", { name: "All drafts" }));
    await waitForDrafts();
    expect(screen.queryByLabelText("PQL query")).toBeNull();
    expect(
      screen.getByRole("button", { name: "All drafts" }),
    ).toHaveAttribute("aria-pressed", "true");
  });

  it("the editor shows the corrected save hint now that the menu is the save path", async () => {
    renderPage();
    await waitForDrafts();
    fireEvent.click(screen.getByRole("button", { name: "PQL" }));
    await screen.findByLabelText("PQL query");
    expect(
      screen.getByText(
        "To save this query, use Views → Save current view next to the All drafts / PQL toggle; it re-applies when the view is opened.",
      ),
    ).toBeTruthy();
  });
});
