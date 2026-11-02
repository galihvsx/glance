// @vitest-environment jsdom
// MyWork saved-view round-trip for the `cycle` PQL field (C115T1):
// a workspace view whose PQL contains `cycle = <name>` re-applies
// through the page's probe-first handleSavedViewApplied, re-runs in
// PQL mode, and leaves the filter tab unchanged (C113 tab
// orthogonality — a filter never switches tabs). Follows the
// MyWorkPqlProject.test.tsx (C114T1) saved-view idiom (mocked
// useSavedViews hook, stubbed auth, spied api.get); the mock server
// answers the cycle predicate by membership — the ENG rows are the
// Sprint 3 members — standing in for T0's workspace translator
// (T0's branch was not yet pushed when this test was written, so it
// builds against the plan contract).

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

const cycleView: SavedView = {
  id: "v-cycle",
  name: "Sprint 3 cycle",
  filters: { ...EMPTY_FILTERS, pql: "cycle = Sprint 3" },
  display: null,
  isDefault: false,
  shared: false,
  ownerId: "u-1",
  ownerName: "Me",
};

function mockViews() {
  mockUseSavedViews.mockReturnValue({
    views: [cycleView],
    ownViews: [cycleView],
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
  projectIdentifier: string,
  projectName: string,
): MyWorkItem {
  return {
    id,
    display_id: displayId,
    name,
    priority: 3,
    state_id: "s-1",
    state_name: "Todo",
    state_group: "unstarted",
    project_id: projectId,
    project_identifier: projectIdentifier,
    project_name: projectName,
    updated_at: "2026-10-05T00:00:00Z",
  };
}

const assignedEng = makeItem(
  "i-ae",
  "ENG-1",
  "Assigned ENG task",
  "p-eng",
  "ENG",
  "Engine",
);
const assignedWeb = makeItem(
  "i-aw",
  "WEB-1",
  "Assigned WEB task",
  "p-web",
  "WEB",
  "Website",
);
const createdEng = makeItem(
  "i-ce",
  "ENG-2",
  "Created ENG task",
  "p-eng",
  "ENG",
  "Engine",
);
const createdWeb = makeItem(
  "i-cw",
  "WEB-2",
  "Created WEB task",
  "p-web",
  "WEB",
  "Website",
);

const BUCKETS: Record<string, MyWorkItem[]> = {
  assigned: [assignedEng, assignedWeb],
  created: [createdEng, createdWeb],
  watched: [],
};

// Stand-in for T0's workspace translator: the Sprint 3 members are
// the ENG rows (i-ae, i-ce); the WEB rows are in no matching cycle.
const SPRINT_3_IDS = new Set(["i-ae", "i-ce"]);

function renderPage() {
  mockViews();
  const get = vi.spyOn(api, "get").mockImplementation((path: string) => {
    if (path === "/api/v1/workspaces")
      return Promise.resolve({ workspaces: [{ slug: "acme", name: "Acme" }] });
    if (path.includes("/my-issues")) {
      const qs = new URLSearchParams(path.split("?")[1] ?? "");
      let rows = BUCKETS[qs.get("filter") ?? ""] ?? [];
      if (qs.get("pql") === "cycle = Sprint 3")
        rows = rows.filter((r) => SPRINT_3_IDS.has(r.id));
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
  await screen.findByText("Assigned ENG task");
  await screen.findByText("Assigned WEB task");
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

describe("mywork saved view with cycle PQL (C115T1)", () => {
  it("re-applies a view whose PQL contains cycle = Sprint 3 in PQL mode, tab unchanged", async () => {
    const { get } = renderPage();
    await waitForInitial();
    const user = userEvent.setup();
    await user.click(screen.getByRole("tab", { name: "Created by me" }));
    await screen.findByText("Created ENG task");
    await screen.findByText("Created WEB task");
    fireEvent.click(screen.getByRole("button", { name: "Saved views" }));
    await screen.findByText("Sprint 3 cycle");
    fireEvent.click(screen.getByTitle("Apply view “Sprint 3 cycle”"));
    // Editor mounted, seeded with the applied query (awaited: the
    // menu apply probes first, so the editor mounts a tick later).
    expect(
      (await screen.findByLabelText("PQL query") as HTMLTextAreaElement)
        .value,
    ).toBe("cycle = Sprint 3");
    // The tab never moved: still Created, still selected.
    expect(
      screen.getByRole("tab", { name: "Created by me" }),
    ).toHaveAttribute("aria-selected", "true");
    // The query re-ran under the created filter — never under
    // assigned — and the cycle predicate filtered the rows: the
    // created ENG row stays, the created WEB row leaves.
    await waitFor(() => {
      const pqlCalls = myWorkCalls(get).filter(
        (qs) =>
          qs.get("pql") === "cycle = Sprint 3" &&
          qs.get("filter") === "created",
      );
      expect(pqlCalls.length).toBeGreaterThan(0);
    });
    await screen.findByText("Created ENG task");
    expect(screen.queryByText("Created WEB task")).toBeNull();
    expect(screen.queryByText("Assigned ENG task")).toBeNull();
  });
});
