// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, useSearchParams } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import SavedViewsMenu from "./SavedViewsMenu";
import { useSavedViews } from "./useSavedViews";
import { DEFAULT_DISPLAY_SETTINGS } from "./useDisplaySettings";
import { toast } from "../ui/toast";
import { EMPTY_FILTERS } from "../../lib/filters";
import type { SavedView } from "./useSavedViews";

// The hook is mocked: these tests cover the menu's shared-section
// rendering and the share/unshare toggle wiring, not the hook itself.
vi.mock("./useSavedViews", () => ({ useSavedViews: vi.fn() }));
vi.mock("../ui/toast", () => ({ toast: { add: vi.fn() } }));

const mockUseSavedViews = vi.mocked(useSavedViews);
const mockToastAdd = vi.mocked(toast.add);

const ownView: SavedView = {
  id: "v1",
  name: "My bugs",
  filters: EMPTY_FILTERS,
  display: null,
  isDefault: false,
  shared: false,
  ownerId: "me",
  ownerName: "Me",
};

const sharedView: SavedView = {
  id: "v2",
  name: "Team board",
  filters: EMPTY_FILTERS,
  display: null,
  isDefault: false,
  shared: true,
  ownerId: "user-2",
  ownerName: "Bob",
};

function setup(overrides: Partial<ReturnType<typeof useSavedViews>> = {}) {
  const setViewShared = vi.fn().mockResolvedValue(undefined);
  mockUseSavedViews.mockReturnValue({
    views: [ownView, sharedView],
    ownViews: [ownView],
    sharedViews: [sharedView],
    viewsLoading: false,
    viewsReady: true,
    viewsError: false,
    createView: vi.fn(),
    renameView: vi.fn(),
    deleteView: vi.fn(),
    setDefaultView: vi.fn(),
    setViewShared,
    ...overrides,
  } as ReturnType<typeof useSavedViews>);
  render(
    <MemoryRouter>
      <SavedViewsMenu
        projectId="p1"
        filters={EMPTY_FILTERS}
        display={null}
        onApplyDisplay={null}
      />
    </MemoryRouter>,
  );
  return { setViewShared };
}

async function openMenu() {
  fireEvent.click(screen.getByRole("button", { name: "Saved views" }));
  await screen.findByText("Shared");
}

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => {
  cleanup();
});

describe("SavedViewsMenu shared section (C10T1)", () => {
  it("renders other members' shared views under a Shared section with the owner name", async () => {
    setup();
    await openMenu();
    expect(screen.getByText("Team board")).toBeTruthy();
    expect(screen.getByText("Bob")).toBeTruthy();
    // The own view renders in the main list ("My bugs" also labels the
    // trigger because its filters are currently active).
    expect(screen.getAllByText("My bugs").length).toBeGreaterThanOrEqual(1);
  });

  it("offers the share toggle on the owner's own row only", async () => {
    const { setViewShared } = setup();
    await openMenu();
    const toggle = screen.getByTitle("Share “My bugs” with the project");
    expect(toggle).toBeTruthy();
    fireEvent.click(toggle);
    await waitFor(() =>
      expect(setViewShared).toHaveBeenCalledWith("v1", true),
    );
    // No share toggle on the shared-by-other row.
    expect(
      screen.queryByTitle(/Share “Team board”/),
    ).toBeNull();
  });

  it("shows an honest error toast when the share toggle fails — no silent fallback", async () => {
    const { setViewShared } = setup();
    setViewShared.mockRejectedValue(new Error("boom"));
    await openMenu();
    fireEvent.click(screen.getByTitle("Share “My bugs” with the project"));
    await waitFor(() =>
      expect(mockToastAdd).toHaveBeenCalledWith(
        expect.objectContaining({
          title: "Could not update sharing",
          type: "error",
        }),
      ),
    );
  });

  it("renders no Shared section when nobody shared anything", async () => {
    setup({ views: [ownView], ownViews: [ownView], sharedViews: [] });
    fireEvent.click(screen.getByRole("button", { name: "Saved views" }));
    await screen.findAllByText("My bugs");
    expect(screen.queryByText("Shared")).toBeNull();
  });
});

/** Echoes the router search string so tests can assert the URL a
 *  default-view application writes (MemoryRouter never touches
 *  window.location). */
function SearchProbe() {
  const [sp] = useSearchParams();
  return <span data-testid="search">{sp.toString()}</span>;
}

const defaultView: SavedView = {
  id: "v-def",
  name: "Default bugs",
  filters: { ...EMPTY_FILTERS, q: "bug" },
  display: DEFAULT_DISPLAY_SETTINGS,
  isDefault: true,
  shared: false,
  ownerId: "me",
  ownerName: "Me",
};

/** Another member's shared view flagged default — must never auto-apply. */
const sharedDefaultView: SavedView = {
  id: "v-shared-def",
  name: "Team default",
  filters: { ...EMPTY_FILTERS, q: "team" },
  display: null,
  isDefault: true,
  shared: true,
  ownerId: "user-2",
  ownerName: "Bob",
};

type HookState = Partial<ReturnType<typeof useSavedViews>>;

function mockHook(state: HookState) {
  mockUseSavedViews.mockReturnValue({
    views: [],
    ownViews: [],
    sharedViews: [],
    viewsLoading: false,
    viewsReady: false,
    viewsError: false,
    createView: vi.fn(),
    renameView: vi.fn(),
    deleteView: vi.fn(),
    setDefaultView: vi.fn(),
    setViewShared: vi.fn(),
    ...state,
  } as ReturnType<typeof useSavedViews>);
}

function renderDefaultMenu(opts: {
  projectId: string;
  entry?: string;
}) {
  const onViewApplied = vi.fn();
  const onApplyDisplay = vi.fn();
  const utils = render(
    <MemoryRouter initialEntries={[opts.entry ?? "/w/acme/p/ENG"]}>
      <SearchProbe />
      <SavedViewsMenu
        projectId={opts.projectId}
        filters={EMPTY_FILTERS}
        display={null}
        onApplyDisplay={onApplyDisplay}
        onViewApplied={onViewApplied}
      />
    </MemoryRouter>,
  );
  return { ...utils, onViewApplied, onApplyDisplay };
}

/** The real first-mount state on every page: projectId "" (the project
 *  query has not resolved yet) and the disabled views query reporting
 *  "not loading" with an empty list. */
const pendingProjectState: HookState = {
  views: [],
  ownViews: [],
  sharedViews: [],
  viewsLoading: false,
  viewsReady: false,
  viewsError: false,
};

/** Views arrived for the real project, with the own default present. */
const readyDefaultState: HookState = {
  views: [defaultView],
  ownViews: [defaultView],
  sharedViews: [],
  viewsLoading: false,
  viewsReady: true,
  viewsError: false,
};

function rerenderDefaultMenu(
  utils: ReturnType<typeof renderDefaultMenu>,
  opts: { projectId: string; state: HookState },
) {
  mockHook(opts.state);
  utils.rerender(
    <MemoryRouter initialEntries={["/w/acme/p/ENG"]}>
      <SearchProbe />
      <SavedViewsMenu
        projectId={opts.projectId}
        filters={EMPTY_FILTERS}
        display={null}
        onApplyDisplay={utils.onApplyDisplay}
        onViewApplied={utils.onViewApplied}
      />
    </MemoryRouter>,
  );
}

describe("SavedViewsMenu default-view auto-apply race (C106T0)", () => {
  it("applies the own default exactly once on the '' → real-id mount sequence (inv 1)", async () => {
    mockHook(pendingProjectState);
    const utils = renderDefaultMenu({ projectId: "" });
    // The empty first mount must not decide anything.
    expect(utils.onViewApplied).not.toHaveBeenCalled();
    rerenderDefaultMenu(utils, {
      projectId: "p1",
      state: readyDefaultState,
    });
    expect(utils.onViewApplied).toHaveBeenCalledTimes(1);
    expect(utils.onViewApplied).toHaveBeenCalledWith(null);
    expect(utils.onApplyDisplay).toHaveBeenCalledTimes(1);
    expect(utils.onApplyDisplay).toHaveBeenCalledWith(
      DEFAULT_DISPLAY_SETTINGS,
    );
    await waitFor(() =>
      expect(
        new URLSearchParams(
          screen.getByTestId("search").textContent ?? "",
        ).get("q"),
      ).toBe("bug"),
    );
  });

  it("does not double-apply when views re-render after the apply (inv 3)", async () => {
    mockHook(pendingProjectState);
    const utils = renderDefaultMenu({ projectId: "" });
    rerenderDefaultMenu(utils, {
      projectId: "p1",
      state: readyDefaultState,
    });
    expect(utils.onViewApplied).toHaveBeenCalledTimes(1);
    // A fresh views array identity (e.g. a cache invalidation) re-runs
    // the effect; the once-flag plus the has-filter-params guard must
    // both hold, so the apply still happens exactly once.
    rerenderDefaultMenu(utils, {
      projectId: "p1",
      state: {
        ...readyDefaultState,
        views: [{ ...defaultView }],
        ownViews: [{ ...defaultView }],
      },
    });
    expect(utils.onViewApplied).toHaveBeenCalledTimes(1);
    expect(utils.onApplyDisplay).toHaveBeenCalledTimes(1);
    expect(
      new URLSearchParams(
        screen.getByTestId("search").textContent ?? "",
      ).get("q"),
    ).toBe("bug");
  });

  it("does not apply when the URL already carries filter params (inv 4)", () => {
    mockHook(readyDefaultState);
    const utils = renderDefaultMenu({
      projectId: "p1",
      entry: "/w/acme/p/ENG?state=abc",
    });
    expect(utils.onViewApplied).not.toHaveBeenCalled();
    expect(utils.onApplyDisplay).not.toHaveBeenCalled();
    expect(screen.getByTestId("search").textContent).toBe("state=abc");
  });

  it("does not apply when there is no own default (inv 5)", () => {
    mockHook({
      views: [ownView],
      ownViews: [ownView],
      sharedViews: [],
      viewsLoading: false,
      viewsReady: true,
      viewsError: false,
    });
    const utils = renderDefaultMenu({ projectId: "p1" });
    expect(utils.onViewApplied).not.toHaveBeenCalled();
    expect(utils.onApplyDisplay).not.toHaveBeenCalled();
  });

  it("does not apply another member's shared default (inv 5)", () => {
    mockHook({
      views: [sharedDefaultView],
      ownViews: [],
      sharedViews: [sharedDefaultView],
      viewsLoading: false,
      viewsReady: true,
      viewsError: false,
    });
    const utils = renderDefaultMenu({ projectId: "p1" });
    expect(utils.onViewApplied).not.toHaveBeenCalled();
    expect(utils.onApplyDisplay).not.toHaveBeenCalled();
  });

  it("waits for auth before applying and then applies exactly once (inv 6)", () => {
    // Views fetched but the current user not yet resolved: viewsLoading
    // covers authLoading, so nothing may apply — and the flag must not
    // burn while waiting.
    mockHook({ ...readyDefaultState, viewsLoading: true });
    const utils = renderDefaultMenu({ projectId: "p1" });
    expect(utils.onViewApplied).not.toHaveBeenCalled();
    rerenderDefaultMenu(utils, {
      projectId: "p1",
      state: readyDefaultState,
    });
    expect(utils.onViewApplied).toHaveBeenCalledTimes(1);
  });

  it("a fetch error does not burn the flag: a later successful load still applies (inv 7)", () => {
    mockHook({
      views: [],
      ownViews: [],
      sharedViews: [],
      viewsLoading: false,
      viewsReady: true,
      viewsError: true,
    });
    const utils = renderDefaultMenu({ projectId: "p1" });
    expect(utils.onViewApplied).not.toHaveBeenCalled();
    rerenderDefaultMenu(utils, {
      projectId: "p1",
      state: readyDefaultState,
    });
    expect(utils.onViewApplied).toHaveBeenCalledTimes(1);
    expect(utils.onApplyDisplay).toHaveBeenCalledTimes(1);
  });
});

describe("SavedViewsMenu detail-page mode (C107T0)", () => {
  const detailOwnPql: SavedView = {
    id: "v-own-pql",
    name: "High priority",
    filters: { ...EMPTY_FILTERS, pql: "  priority = High  " },
    display: null,
    isDefault: false,
    shared: false,
    ownerId: "me",
    ownerName: "Me",
  };
  const detailOwnVisual: SavedView = {
    id: "v-own-vis",
    name: "My bugs",
    filters: EMPTY_FILTERS,
    display: null,
    isDefault: false,
    shared: false,
    ownerId: "me",
    ownerName: "Me",
  };
  const detailSharedPql: SavedView = {
    id: "v-sh-pql",
    name: "Team pql",
    filters: { ...EMPTY_FILTERS, pql: "state = Done" },
    display: null,
    isDefault: false,
    shared: true,
    ownerId: "user-2",
    ownerName: "Bob",
  };
  const detailSharedVisual: SavedView = {
    id: "v-sh-vis",
    name: "Team visual",
    filters: EMPTY_FILTERS,
    display: null,
    isDefault: false,
    shared: true,
    ownerId: "user-2",
    ownerName: "Bob",
  };
  const detailOwnDefaultPql: SavedView = {
    id: "v-def-pql",
    name: "Default pql",
    filters: { ...EMPTY_FILTERS, pql: "priority = High" },
    display: null,
    isDefault: true,
    shared: false,
    ownerId: "me",
    ownerName: "Me",
  };

  function setupDetail(opts: {
    views: SavedView[];
    ownViews: SavedView[];
    sharedViews: SavedView[];
    entry?: string;
    filters?: typeof EMPTY_FILTERS;
    pqlMode?: boolean;
    pqlQuery?: string;
  }) {
    const createView = vi.fn().mockResolvedValue({
      id: "v-new",
      name: "saved",
      filters: EMPTY_FILTERS,
      display: null,
    });
    const renameView = vi.fn().mockResolvedValue(true);
    const deleteView = vi.fn().mockResolvedValue(undefined);
    const setDefaultView = vi.fn().mockResolvedValue(undefined);
    const setViewShared = vi.fn().mockResolvedValue(undefined);
    const onViewApplied = vi.fn();
    const onApplyDisplay = vi.fn();
    mockUseSavedViews.mockReturnValue({
      views: opts.views,
      ownViews: opts.ownViews,
      sharedViews: opts.sharedViews,
      viewsLoading: false,
      viewsReady: true,
      viewsError: false,
      createView,
      renameView,
      deleteView,
      setDefaultView,
      setViewShared,
    } as ReturnType<typeof useSavedViews>);
    const utils = render(
      <MemoryRouter initialEntries={[opts.entry ?? "/w/acme/p/ENG"]}>
        <SearchProbe />
        <SavedViewsMenu
          projectId="p1"
          filters={opts.filters ?? EMPTY_FILTERS}
          display={null}
          onApplyDisplay={onApplyDisplay}
          pqlMode={opts.pqlMode}
          pqlQuery={opts.pqlQuery}
          onViewApplied={onViewApplied}
          detailMode
        />
      </MemoryRouter>,
    );
    return {
      ...utils,
      createView,
      renameView,
      deleteView,
      setDefaultView,
      setViewShared,
      onViewApplied,
      onApplyDisplay,
    };
  }

  async function openDetailMenu() {
    fireEvent.click(screen.getByRole("button", { name: "Saved views" }));
    await screen.findByRole("button", { name: "Save current view" });
  }

  it("detail-mode apply calls onViewApplied with the trimmed query and writes NOTHING to the URL (inv 1)", async () => {
    const { onViewApplied } = setupDetail({
      views: [detailOwnPql, detailSharedPql],
      ownViews: [detailOwnPql],
      sharedViews: [detailSharedPql],
      entry: "/w/acme/p/ENG?peek=abc",
    });
    await openDetailMenu();
    expect(screen.getByTestId("search").textContent).toBe("peek=abc");
    fireEvent.click(screen.getByTitle("Apply view “High priority”"));
    expect(onViewApplied).toHaveBeenCalledTimes(1);
    expect(onViewApplied).toHaveBeenCalledWith("priority = High");
    // Byte-identical: not even the non-filter param is rewritten.
    expect(screen.getByTestId("search").textContent).toBe("peek=abc");
  });

  it("detail-mode mount with an own default and filter-free URL applies NOTHING (inv 2)", async () => {
    // Same '' → real-id mount sequence as the C106 tests, but in
    // detail mode the default must never apply in either phase.
    mockHook(pendingProjectState);
    const onViewApplied = vi.fn();
    const onApplyDisplay = vi.fn();
    const utils = render(
      <MemoryRouter initialEntries={["/w/acme/p/ENG"]}>
        <SearchProbe />
        <SavedViewsMenu
          projectId=""
          filters={EMPTY_FILTERS}
          display={null}
          onApplyDisplay={onApplyDisplay}
          onViewApplied={onViewApplied}
          detailMode
        />
      </MemoryRouter>,
    );
    expect(onViewApplied).not.toHaveBeenCalled();
    mockHook({
      views: [detailOwnDefaultPql],
      ownViews: [detailOwnDefaultPql],
      sharedViews: [],
      viewsLoading: false,
      viewsReady: true,
      viewsError: false,
    });
    utils.rerender(
      <MemoryRouter initialEntries={["/w/acme/p/ENG"]}>
        <SearchProbe />
        <SavedViewsMenu
          projectId="p1"
          filters={EMPTY_FILTERS}
          display={null}
          onApplyDisplay={onApplyDisplay}
          onViewApplied={onViewApplied}
          detailMode
        />
      </MemoryRouter>,
    );
    expect(onViewApplied).not.toHaveBeenCalled();
    expect(onApplyDisplay).not.toHaveBeenCalled();
    expect(screen.getByTestId("search").textContent).toBe("");
  });

  it("detail mode lists PQL views only, own and shared; visual views render nowhere (inv 3)", async () => {
    setupDetail({
      views: [
        detailOwnPql,
        detailOwnVisual,
        detailSharedPql,
        detailSharedVisual,
      ],
      ownViews: [detailOwnPql, detailOwnVisual],
      sharedViews: [detailSharedPql, detailSharedVisual],
    });
    await openDetailMenu();
    expect(screen.getByText("High priority")).toBeTruthy();
    expect(screen.getByText("Team pql")).toBeTruthy();
    expect(screen.getByText("Shared")).toBeTruthy();
    expect(screen.queryByText("My bugs")).toBeNull();
    expect(screen.queryByText("Team visual")).toBeNull();
  });

  it("detail-mode PQL save stores ONLY the query, never parked visual filters (inv 4, with pqlMode)", async () => {
    const { createView } = setupDetail({
      views: [detailOwnPql],
      ownViews: [detailOwnPql],
      sharedViews: [],
      filters: { ...EMPTY_FILTERS, state: "stale-visual" },
      pqlMode: true,
      pqlQuery: "priority = High",
    });
    await openDetailMenu();
    fireEvent.click(screen.getByRole("button", { name: "Save current view" }));
    fireEvent.change(screen.getByPlaceholderText("View name…"), {
      target: { value: "PQL saved" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save view" }));
    expect(createView).toHaveBeenCalledTimes(1);
    const savedFilters = createView.mock.calls[0][1] as typeof EMPTY_FILTERS;
    expect(savedFilters.pql).toBe("priority = High");
    expect(savedFilters.state).toBe("");
    expect(savedFilters.priorities).toEqual([]);
  });

  it("detail mode saves the PQL-only shape even without pqlMode (inv 4, detail guarantee)", async () => {
    // A detail page has no visual filter bar, so a save from its menu
    // is always a query save — the shape must not depend on the
    // caller also passing pqlMode.
    const { createView } = setupDetail({
      views: [detailOwnPql],
      ownViews: [detailOwnPql],
      sharedViews: [],
      filters: { ...EMPTY_FILTERS, state: "stale-visual" },
      pqlQuery: "priority = High",
    });
    await openDetailMenu();
    fireEvent.click(screen.getByRole("button", { name: "Save current view" }));
    fireEvent.change(screen.getByPlaceholderText("View name…"), {
      target: { value: "PQL saved" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save view" }));
    expect(createView).toHaveBeenCalledTimes(1);
    const savedFilters = createView.mock.calls[0][1] as typeof EMPTY_FILTERS;
    expect(savedFilters.pql).toBe("priority = High");
    expect(savedFilters.state).toBe("");
    expect(savedFilters.priorities).toEqual([]);
  });

  it("set-default and share still work on a listed PQL view in detail mode", async () => {
    const { setDefaultView, setViewShared } = setupDetail({
      views: [detailOwnPql],
      ownViews: [detailOwnPql],
      sharedViews: [],
    });
    await openDetailMenu();
    fireEvent.click(screen.getByTitle("Set as default view"));
    await waitFor(() =>
      expect(setDefaultView).toHaveBeenCalledWith("v-own-pql"),
    );
    fireEvent.click(
      screen.getByTitle("Share “High priority” with the project"),
    );
    await waitFor(() =>
      expect(setViewShared).toHaveBeenCalledWith("v-own-pql", true),
    );
  });

  it("rename and delete still work on a listed PQL view in detail mode", async () => {
    const { renameView, deleteView } = setupDetail({
      views: [detailOwnPql],
      ownViews: [detailOwnPql],
      sharedViews: [],
    });
    await openDetailMenu();
    fireEvent.click(screen.getByTitle("Rename “High priority”"));
    fireEvent.change(screen.getByDisplayValue("High priority"), {
      target: { value: "Renamed" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Rename" }));
    await waitFor(() =>
      expect(renameView).toHaveBeenCalledWith("v-own-pql", "Renamed"),
    );
    fireEvent.click(screen.getByTitle("Delete “High priority”"));
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    await waitFor(() =>
      expect(deleteView).toHaveBeenCalledWith("v-own-pql"),
    );
  });
});
