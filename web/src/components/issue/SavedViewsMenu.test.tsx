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
