// @vitest-environment jsdom
// Saved views on Archived (C111T3): detail-mode SavedViewsMenu beside
// the All-archived / PQL toggle, applying a view through the page's
// probe-first handlePqlApply (which already carries archived=1 + the
// client-side archived-only filter). Follows the ArchivedPql.test.tsx
// shape for the page mock plus the CalendarSavedViews.test.tsx
// saved-view idiom (mocked useSavedViews hook, stubbed auth).

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
import Archived from "./Archived";
import { api, ApiError } from "../lib/api";
import { EMPTY_FILTERS } from "../lib/filters";
import type { Issue } from "../lib/types";
import {
  makeQueryClient,
  stubMatchMedia,
} from "../components/shell/test-utils";

// The detail-mode menu reads auth through useSavedViews; mock the
// hook (Calendar pattern) so page tests control the view list, and
// stub auth for the menu's owner gates.
vi.mock("../lib/auth", () => ({
  useAuth: () => ({ user: { id: "u-1", email: "u@x.com" }, loading: false }),
}));
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
  archivedAt: string | null,
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
    is_draft: false,
    created_by: "u-1",
    created_at: "",
    updated_at: "",
    archived_at: archivedAt,
    assignees: [],
    labels: [],
  };
}

// Two archived issues (the unfiltered set) plus one working-set
// issue the server also returns under archived=1 (include, not
// restrict) — the client filter must drop it everywhere, including
// on view applies.
const archivedA = makeIssue(
  "i-a",
  "ENG-1",
  "Old login bug",
  "2026-10-01T00:00:00Z",
);
const archivedB = makeIssue(
  "i-b",
  "ENG-2",
  "Retired report",
  "2026-10-05T00:00:00Z",
);
const workingW = makeIssue("i-w", "ENG-3", "Live checkout", null);

function renderPage(ownViews: SavedView[] = []) {
  mockViews(ownViews);
  const get = vi.spyOn(api, "get").mockImplementation((path: string) => {
    if (path === "/api/v1/workspaces")
      return Promise.resolve({ workspaces: [{ slug: "acme", name: "Acme" }] });
    // Shared ["project", slug, identifier] query feeding the menu.
    if (path === BASE) return Promise.resolve({ id: "p-eng" });
    if (path.includes("/issues")) {
      const qs = new URLSearchParams(path.split("?")[1] ?? "");
      const pql = qs.get("pql");
      if (pql !== null && pql.includes("bogus")) {
        return Promise.reject(
          new ApiError(400, "unknown field: bogus", "validation_error", {
            position: 1,
          }),
        );
      }
      if (pql === "priority = High") {
        // Predicate match: one archived issue AND one working-set
        // issue — the view must show only the archived one.
        return Promise.resolve({
          results: [archivedA, workingW],
          next_cursor: null,
        });
      }
      if (pql === "priority = Low") {
        return Promise.resolve({
          results: [archivedB],
          next_cursor: null,
        });
      }
      return Promise.resolve({
        results: [archivedA, archivedB, workingW],
        next_cursor: null,
      });
    }
    return Promise.reject(new Error(`unexpected GET ${path}`));
  });
  const qc = makeQueryClient();
  render(
    <MemoryRouter initialEntries={["/w/acme/p/ENG/archived"]}>
      <QueryClientProvider client={qc}>
        <Routes>
          <Route
            path="/w/:slug/p/:identifier/archived"
            element={<Archived />}
          />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
  return { get };
}

async function waitForInitial() {
  await screen.findByText("Old login bug");
  await screen.findByText("Retired report");
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

function editorValue(): string {
  return (screen.getByLabelText("PQL query") as HTMLTextAreaElement).value;
}

const savedHigh: SavedView = {
  id: "v-high",
  name: "High priority",
  filters: { ...EMPTY_FILTERS, pql: "priority = High" },
  display: null,
  isDefault: false,
  shared: false,
  ownerId: "u-1",
  ownerName: "Me",
};
const savedLow: SavedView = {
  id: "v-low",
  name: "Low priority",
  filters: { ...EMPTY_FILTERS, pql: "priority = Low" },
  display: null,
  isDefault: false,
  shared: false,
  ownerId: "u-1",
  ownerName: "Me",
};
const savedBogus: SavedView = {
  id: "v-bogus",
  name: "Bogus query",
  filters: { ...EMPTY_FILTERS, pql: "bogus = 1" },
  display: null,
  isDefault: false,
  shared: false,
  ownerId: "u-1",
  ownerName: "Me",
};

beforeEach(() => {
  mockViews([]);
  stubMatchMedia(false);
  localStorage.clear();
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("archived saved views (C111T3)", () => {
  it("applying a PQL view enters PQL mode with the view's query applied and the archived-only filter retained", async () => {
    const { get } = renderPage([savedHigh]);
    await waitForInitial();
    await openSavedViewsMenu();
    fireEvent.click(screen.getByTitle("Apply view “High priority”"));
    // Await the filtered list, not just the absence of the old one:
    // while a new PQL query loads, the display falls back to the
    // unfiltered set, so absence alone passes before the fetch lands.
    await waitFor(() => {
      expect(screen.queryByText("Retired report")).toBeNull();
    });
    expect(screen.getByText("Old login bug")).toBeTruthy();
    // Working-set match of the same predicate is never shown: the
    // archived-only filter applies to view applies exactly as to
    // typed queries.
    expect(screen.queryByText("Live checkout")).toBeNull();
    expect(
      screen.getByRole("button", { name: "PQL" }),
    ).toHaveAttribute("aria-pressed", "true");
    expect(editorValue()).toBe("priority = High");
    // Every PQL fetch carries archived=1 plus pql.
    const pqlCalls = issueCalls(get).filter((qs) => qs.get("pql") !== null);
    expect(pqlCalls.length).toBeGreaterThan(0);
    for (const qs of pqlCalls) {
      expect(qs.get("archived")).toBe("1");
      expect(qs.get("pql")).toBe("priority = High");
      expect(qs.get("per_page")).toBe("100");
    }
  });

  it("applying a view while already in PQL mode re-seeds the editor", async () => {
    renderPage([savedHigh, savedLow]);
    await waitForInitial();
    await openSavedViewsMenu();
    fireEvent.click(screen.getByTitle("Apply view “High priority”"));
    // Wait for the editor to mount (PQL mode entered) — "Old login
    // bug" is present in both sets, so it cannot gate the wait.
    await screen.findByLabelText("PQL query");
    expect(editorValue()).toBe("priority = High");
    // Already in PQL mode: without the applied-query key the editor
    // would keep showing the first view's draft.
    await openSavedViewsMenu();
    fireEvent.click(screen.getByTitle("Apply view “Low priority”"));
    await waitFor(() => {
      expect(screen.queryByText("Old login bug")).toBeNull();
    });
    expect(screen.getByText("Retired report")).toBeTruthy();
    expect(editorValue()).toBe("priority = Low");
  });

  it("a view whose query fails its probe surfaces the error with the displayed set untouched", async () => {
    const { get } = renderPage([savedBogus]);
    await waitForInitial();
    await openSavedViewsMenu();
    fireEvent.click(screen.getByTitle("Apply view “Bogus query”"));
    // The probe 400 surfaces at the detail level (the menu path has
    // no editor inline error) and the archived set never changes.
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("unknown field: bogus");
    expect(screen.getByText("Old login bug")).toBeTruthy();
    expect(screen.getByText("Retired report")).toBeTruthy();
    expect(screen.queryByText("Live checkout")).toBeNull();
    // PQL mode was entered (editor visible) but nothing committed:
    // no applied query, and no query fetch beyond the single failed
    // probe — so the editor must NOT mount with the failed query as
    // its draft.
    expect(screen.getByLabelText("PQL query")).toBeTruthy();
    expect(editorValue()).toBe("");
    expect(
      issueCalls(get).filter((qs) => qs.get("pql") !== null),
    ).toHaveLength(1);
    // No unhandled rejection: the handler catches the probe failure
    // internally (a rejection here would fail the run).
  });

  it("leaving PQL mode after a view apply restores the full archived set", async () => {
    renderPage([savedHigh]);
    await waitForInitial();
    await openSavedViewsMenu();
    fireEvent.click(screen.getByTitle("Apply view “High priority”"));
    await waitFor(() => {
      expect(screen.queryByText("Retired report")).toBeNull();
    });
    fireEvent.click(screen.getByRole("button", { name: "All archived" }));
    await screen.findByText("Retired report");
    expect(screen.getByText("Old login bug")).toBeTruthy();
    expect(screen.queryByLabelText("PQL query")).toBeNull();
  });

  it("the editor shows the corrected save hint now that the menu is the save path", async () => {
    renderPage();
    await waitForInitial();
    fireEvent.click(screen.getByRole("button", { name: "PQL" }));
    await screen.findByLabelText("PQL query");
    expect(
      screen.getByText(
        "To save this query, use Views → Save current view next to the All archived / PQL toggle; it re-applies when the view is opened.",
      ),
    ).toBeTruthy();
  });
});
