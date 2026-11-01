// @vitest-environment jsdom
// Saved views on MyWork (C113T1): detail-mode SavedViewsMenu beside
// the All work / PQL toggle, applying a view through the page's
// probe-first handleSavedViewApplied, the filter tab staying put
// (tab orthogonality), the adapted save-hint wording, and PQL-mode
// exit clearing the query. Follows the MyWorkPql.test.tsx page mock
// plus the ArchivedSavedViews.test.tsx saved-view idiom (mocked
// useSavedViews hook, stubbed auth).
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import MyWork from "./MyWork";
import { api } from "../lib/api";
import { EMPTY_FILTERS } from "../lib/filters";
import type { MyWorkItem } from "../lib/mywork";
import {
  makeQueryClient,
  stubMatchMedia,
} from "../components/shell/test-utils";

vi.mock("../lib/auth", () => ({
  useAuth: () => ({ user: { id: "u-1", email: "u@x.com" }, loading: false }),
}));
vi.mock("../components/issue/useSavedViews", () => ({
  useSavedViews: vi.fn(),
}));
import { useSavedViews } from "../components/issue/useSavedViews";
import type { SavedView } from "../components/issue/useSavedViews";

const mockUseSavedViews = vi.mocked(useSavedViews);

const pqlView: SavedView = {
  id: "v1",
  name: "High priority",
  filters: { ...EMPTY_FILTERS, pql: "priority = High" },
  display: null,
  isDefault: false,
  shared: false,
  ownerId: "u-1",
  ownerName: "Me",
};

function mockViews() {
  mockUseSavedViews.mockReturnValue({
    views: [pqlView],
    ownViews: [pqlView],
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

function makeItem(
  id: string,
  displayId: string,
  name: string,
  projectId: string,
  priority: number,
): MyWorkItem {
  return {
    id,
    display_id: displayId,
    name,
    priority,
    state_id: "s-1",
    state_name: "Todo",
    state_group: "unstarted",
    project_id: projectId,
    project_identifier: "ENG",
    project_name: "Engine",
    updated_at: "2026-10-05T00:00:00Z",
  };
}

const assignedHigh = makeItem("i-ah", "ENG-1", "Assigned login bug", "p-eng", 4);
const assignedLow = makeItem("i-al", "ENG-2", "Assigned docs task", "p-eng", 1);
const createdHigh = makeItem("i-ch", "WEB-1", "Created checkout bug", "p-web", 4);
const watchedHigh = makeItem("i-wh", "WEB-2", "Watched deploy bug", "p-web", 4);

const BUCKETS: Record<string, MyWorkItem[]> = {
  assigned: [assignedHigh, assignedLow],
  created: [createdHigh],
  watched: [watchedHigh],
};
const HIGH = new Set(["i-ah", "i-ch", "i-wh"]);

function renderPage() {
  mockViews();
  const get = vi.spyOn(api, "get").mockImplementation((path: string) => {
    if (path === "/api/v1/workspaces")
      return Promise.resolve({ workspaces: [{ slug: "acme", name: "Acme" }] });
    if (path.includes("/my-issues")) {
      const qs = new URLSearchParams(path.split("?")[1] ?? "");
      let rows = BUCKETS[qs.get("filter") ?? ""] ?? [];
      if (qs.get("pql") === "priority = High")
        rows = rows.filter((r) => HIGH.has(r.id));
      return Promise.resolve({ issues: rows });
    }
    return Promise.reject(new Error(`unexpected GET ${path}`));
  });
  const qc = makeQueryClient();
  render(
    <MemoryRouter initialEntries={["/w/acme/my-work"]}>
      <QueryClientProvider client={qc}>
        <Routes>
          <Route path="/w/:slug/my-work" element={<MyWork />} />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
  return { get };
}

async function waitForInitial() {
  await screen.findByText("Assigned login bug");
  await screen.findByText("Assigned docs task");
}

async function applyViewFromMenu() {
  fireEvent.click(screen.getByRole("button", { name: "Saved views" }));
  await screen.findByText("High priority");
  fireEvent.click(screen.getByTitle("Apply view “High priority”"));
}

function myWorkCalls(get: ReturnType<typeof renderPage>["get"]) {
  return get.mock.calls
    .map((c) => c[0] as string)
    .filter((p) => p.includes("/my-issues"))
    .map((p) => new URLSearchParams(p.split("?")[1] ?? ""));
}

beforeEach(() => {
  stubMatchMedia(false);
  localStorage.clear();
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("mywork saved views (C113T1)", () => {
  it("mounts the Views menu beside the All work / PQL toggle", async () => {
    renderPage();
    await waitForInitial();
    expect(screen.getByRole("button", { name: "All work" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "PQL" })).toBeTruthy();
    expect(
      screen.getByRole("button", { name: "Saved views" }),
    ).toBeTruthy();
  });

  it("applying a view keeps the active filter tab and re-runs the query under it", async () => {
    const { get } = renderPage();
    await waitForInitial();
    const user = userEvent.setup();
    await user.click(screen.getByRole("tab", { name: "Created by me" }));
    await screen.findByText("Created checkout bug");
    await applyViewFromMenu();
    // Editor mounted, seeded with the applied query (awaited: the
    // menu apply probes first, so the editor mounts a tick later).
    expect(
      (await screen.findByLabelText("PQL query") as HTMLTextAreaElement)
        .value,
    ).toBe("priority = High");
    // The tab never moved: still Created, still selected.
    expect(
      screen.getByRole("tab", { name: "Created by me" }),
    ).toHaveAttribute("aria-selected", "true");
    // The query re-ran under the created filter — never under
    // assigned: no assigned row appears, and the created High row
    // stays. Await the refetched list itself: the waitFor below only
    // proves the fetch was issued, and the new query key loads
    // through an empty window first.
    await waitFor(() => {
      const pqlCalls = myWorkCalls(get).filter(
        (qs) => qs.get("pql") === "priority = High",
      );
      expect(pqlCalls.length).toBeGreaterThan(0);
      for (const qs of pqlCalls) expect(qs.get("filter")).toBe("created");
    });
    await screen.findByText("Created checkout bug");
    expect(screen.queryByText("Assigned login bug")).toBeNull();
  });

  it("shows the adapted detail-shape save hint in PQL mode, not the staged text", async () => {
    renderPage();
    await waitForInitial();
    fireEvent.click(screen.getByRole("button", { name: "PQL" }));
    await screen.findByLabelText("PQL query");
    expect(
      screen.getByText(
        "To save this query, use Views → Save current view next to the All work / PQL toggle; it re-applies when the view is opened.",
      ),
    ).toBeTruthy();
    expect(
      screen.queryByText(
        "There is no Views save path for this query on this page yet.",
      ),
    ).toBeNull();
  });

  it("leaving PQL mode after a view apply clears the query and restores the full set", async () => {
    renderPage();
    await waitForInitial();
    await applyViewFromMenu();
    await waitFor(() => {
      expect(screen.queryByText("Assigned docs task")).toBeNull();
    });
    expect(
      (screen.getByLabelText("PQL query") as HTMLTextAreaElement).value,
    ).toBe("priority = High");
    fireEvent.click(screen.getByRole("button", { name: "All work" }));
    await screen.findByText("Assigned docs task");
    expect(screen.getByText("Assigned login bug")).toBeTruthy();
    expect(screen.queryByLabelText("PQL query")).toBeNull();
  });
});
