// @vitest-environment jsdom
// C91T1: SavedViewsMenu PQL wiring — PQL views save only the query,
// apply in PQL mode, and visual views leave PQL mode; old views
// (no pql key) behave exactly as before.
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, useSearchParams } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import SavedViewsMenu from "./SavedViewsMenu";
import { useSavedViews } from "./useSavedViews";
import { EMPTY_FILTERS } from "../../lib/filters";
import type { SavedView } from "./useSavedViews";

vi.mock("./useSavedViews", () => ({ useSavedViews: vi.fn() }));
vi.mock("../ui/toast", () => ({ toast: { add: vi.fn() } }));

const mockUseSavedViews = vi.mocked(useSavedViews);

const pqlView: SavedView = {
  id: "v-pql",
  name: "High priority",
  filters: { ...EMPTY_FILTERS, pql: "priority = High" },
  display: null,
  isDefault: false,
  shared: false,
  ownerId: "me",
  ownerName: "Me",
};

const visualView: SavedView = {
  id: "v-vis",
  name: "My bugs",
  filters: EMPTY_FILTERS,
  display: null,
  isDefault: false,
  shared: false,
  ownerId: "me",
  ownerName: "Me",
};

/** Echoes the router search string so tests can assert the URL a
 *  view application writes (MemoryRouter never touches window.location). */
function SearchProbe() {
  const [sp] = useSearchParams();
  return <span data-testid="search">{sp.toString()}</span>;
}

function setup(opts: {
  views?: SavedView[];
  filters?: typeof EMPTY_FILTERS;
  pqlMode?: boolean;
  pqlQuery?: string;
  onViewApplied?: (q: string | null) => void;
}) {
  const createView = vi.fn().mockResolvedValue({
    id: "v-new",
    name: "saved",
    filters: EMPTY_FILTERS,
    display: null,
  });
  const views = opts.views ?? [pqlView, visualView];
  mockUseSavedViews.mockReturnValue({
    views: views,
    ownViews: views,
    sharedViews: [],
    viewsLoading: false,
    viewsReady: true,
    viewsError: false,
    createView,
    renameView: vi.fn(),
    deleteView: vi.fn(),
    setDefaultView: vi.fn(),
    setViewShared: vi.fn(),
    ...{},
  } as ReturnType<typeof useSavedViews>);
  const onViewApplied = opts.onViewApplied ?? vi.fn();
  render(
    <MemoryRouter initialEntries={["/w/acme/p/ENG"]}>
      <SearchProbe />
      <SavedViewsMenu
        projectId="p1"
        filters={opts.filters ?? EMPTY_FILTERS}
        display={null}
        onApplyDisplay={null}
        pqlMode={opts.pqlMode}
        pqlQuery={opts.pqlQuery}
        onViewApplied={onViewApplied}
      />
    </MemoryRouter>,
  );
  return { createView, onViewApplied };
}

async function openMenu() {
  fireEvent.click(screen.getByRole("button", { name: "Saved views" }));
  await screen.findByText("High priority");
}

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => {
  cleanup();
});

describe("SavedViewsMenu PQL views (C91T1)", () => {
  it("applying a PQL view writes ?pql= to the URL and signals PQL mode", async () => {
    const { onViewApplied } = setup({});
    await openMenu();
    fireEvent.click(screen.getByTitle("Apply view “High priority”"));
    expect(onViewApplied).toHaveBeenCalledWith("priority = High");
    expect(
      new URLSearchParams(
        screen.getByTestId("search").textContent ?? "",
      ).get("pql"),
    ).toBe("priority = High");
  });

  it("applying a visual view signals leaving PQL mode (null)", async () => {
    const { onViewApplied } = setup({});
    await openMenu();
    fireEvent.click(screen.getByTitle("Apply view “My bugs”"));
    expect(onViewApplied).toHaveBeenCalledWith(null);
  });

  it("saving in PQL mode persists only the query, never parked visual filters", async () => {
    const { createView } = setup({
      filters: { ...EMPTY_FILTERS, state: "stale-visual" },
      pqlMode: true,
      pqlQuery: "priority = High",
    });
    await openMenu();
    fireEvent.click(screen.getByRole("button", { name: "Save current view" }));
    fireEvent.change(screen.getByPlaceholderText("View name…"), {
      target: { value: "PQL saved" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save view" }));
    expect(createView).toHaveBeenCalledTimes(1);
    const savedFilters = createView.mock.calls[0][1] as typeof EMPTY_FILTERS;
    expect(savedFilters.pql).toBe("priority = High");
    // The parked visual selection must not leak into the saved view.
    expect(savedFilters.state).toBe("");
    expect(savedFilters.priorities).toEqual([]);
  });

  it("saving in visual mode is unchanged (no pql key)", async () => {
    const { createView } = setup({ pqlMode: false });
    await openMenu();
    fireEvent.click(screen.getByRole("button", { name: "Save current view" }));
    fireEvent.change(screen.getByPlaceholderText("View name…"), {
      target: { value: "Visual saved" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save view" }));
    expect(createView).toHaveBeenCalledTimes(1);
    const savedFilters = createView.mock.calls[0][1] as typeof EMPTY_FILTERS;
    expect(savedFilters.pql).toBeUndefined();
  });
});

describe("SavedViewsMenu PQL default auto-apply race (C106T0, inv 2)", () => {
  const pqlDefaultView: SavedView = {
    id: "v-pql-def",
    name: "High priority default",
    filters: { ...EMPTY_FILTERS, pql: "priority = High" },
    display: null,
    isDefault: true,
    shared: false,
    ownerId: "me",
    ownerName: "Me",
  };

  function mockPqlHook(state: Partial<ReturnType<typeof useSavedViews>>) {
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

  it("a PQL default auto-applies INTO PQL mode on the '' → real-id mount sequence", async () => {
    const onViewApplied = vi.fn();
    // Phase 1: the normal first mount — projectId "" and the disabled
    // query reporting "not loading" with an empty list.
    mockPqlHook({
      views: [],
      ownViews: [],
      sharedViews: [],
      viewsLoading: false,
      viewsReady: false,
      viewsError: false,
    });
    const utils = render(
      <MemoryRouter initialEntries={["/w/acme/p/ENG"]}>
        <SearchProbe />
        <SavedViewsMenu
          projectId=""
          filters={EMPTY_FILTERS}
          display={null}
          onApplyDisplay={null}
          onViewApplied={onViewApplied}
        />
      </MemoryRouter>,
    );
    expect(onViewApplied).not.toHaveBeenCalled();
    // Phase 2: the project resolves and the PQL default arrives.
    mockPqlHook({
      views: [pqlDefaultView],
      ownViews: [pqlDefaultView],
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
          onApplyDisplay={null}
          onViewApplied={onViewApplied}
        />
      </MemoryRouter>,
    );
    expect(onViewApplied).toHaveBeenCalledTimes(1);
    expect(onViewApplied).toHaveBeenCalledWith("priority = High");
    expect(
      new URLSearchParams(
        screen.getByTestId("search").textContent ?? "",
      ).get("pql"),
    ).toBe("priority = High");
  });
});
