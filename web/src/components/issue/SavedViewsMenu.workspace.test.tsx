// @vitest-environment jsdom
// SavedViewsMenu in workspace scope (C113T1): the menu takes
// {workspaceSlug} instead of {projectId} and shares every line of
// list/save/rename/delete/share/default logic with project scope —
// only the share copy changes ("shared with the workspace"). The
// hook is mocked, as in SavedViewsMenu.test.tsx: these tests cover
// the menu's scope plumbing and row rendering, not the hook itself.
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import SavedViewsMenu from "./SavedViewsMenu";
import { useSavedViews } from "./useSavedViews";
import { toast } from "../ui/toast";
import { EMPTY_FILTERS } from "../../lib/filters";
import { ApiError } from "../../lib/api";
import type { SavedView } from "./useSavedViews";

vi.mock("./useSavedViews", () => ({ useSavedViews: vi.fn() }));
vi.mock("../ui/toast", () => ({ toast: { add: vi.fn() } }));

const mockUseSavedViews = vi.mocked(useSavedViews);
const mockToastAdd = vi.mocked(toast.add);

const ownPqlView: SavedView = {
  id: "v1",
  name: "High priority",
  filters: { ...EMPTY_FILTERS, pql: "priority = High" },
  display: null,
  isDefault: false,
  shared: false,
  ownerId: "me",
  ownerName: "Me",
};

const sharedPqlView: SavedView = {
  id: "v2",
  name: "Team highs",
  filters: { ...EMPTY_FILTERS, pql: "priority = High" },
  display: null,
  isDefault: false,
  shared: true,
  ownerId: "user-2",
  ownerName: "Bob",
};

// Detail mode lists PQL views only: this visual view must render
// nowhere, even though the hook returns it.
const visualView: SavedView = {
  id: "v3",
  name: "Visual only",
  filters: EMPTY_FILTERS,
  display: null,
  isDefault: false,
  shared: false,
  ownerId: "me",
  ownerName: "Me",
};

function setup(opts: {
  onViewApplied?: (q: string | null) => void;
  pqlQuery?: string;
} = {}) {
  const createView = vi.fn().mockResolvedValue({
    id: "v-new",
    name: "saved",
    filters: { ...EMPTY_FILTERS, pql: "priority = High" },
    display: null,
  });
  const renameView = vi.fn().mockResolvedValue(true);
  const deleteView = vi.fn().mockResolvedValue(undefined);
  const setDefaultView = vi.fn().mockResolvedValue(undefined);
  const setViewShared = vi.fn().mockResolvedValue(undefined);
  mockUseSavedViews.mockReturnValue({
    views: [ownPqlView, sharedPqlView, visualView],
    ownViews: [ownPqlView, visualView],
    sharedViews: [sharedPqlView],
    viewsLoading: false,
    viewsReady: true,
    viewsError: false,
    createView,
    renameView,
    deleteView,
    setDefaultView,
    setViewShared,
  } as ReturnType<typeof useSavedViews>);
  const onViewApplied = opts.onViewApplied ?? vi.fn();
  render(
    <MemoryRouter initialEntries={["/w/acme/my-work"]}>
      <SavedViewsMenu
        workspaceSlug="acme"
        filters={EMPTY_FILTERS}
        display={null}
        onApplyDisplay={null}
        pqlMode
        pqlQuery={opts.pqlQuery ?? "priority = High"}
        onViewApplied={onViewApplied}
        detailMode
      />
    </MemoryRouter>,
  );
  return {
    createView,
    renameView,
    deleteView,
    setDefaultView,
    setViewShared,
    onViewApplied,
  };
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

describe("SavedViewsMenu workspace scope (C113T1)", () => {
  it("passes the workspace scope to the hook and lists own + shared PQL views", async () => {
    setup();
    await openMenu();
    expect(mockUseSavedViews).toHaveBeenCalledWith({
      workspaceSlug: "acme",
    });
    expect(screen.getByText("High priority")).toBeTruthy();
    expect(screen.getByText("Team highs")).toBeTruthy();
    expect(screen.getByText("Bob")).toBeTruthy();
    expect(screen.getByText("Shared")).toBeTruthy();
  });

  it("lists PQL views only in detail mode — a visual view renders nowhere", async () => {
    setup();
    await openMenu();
    expect(screen.queryByText("Visual only")).toBeNull();
  });

  it("saving the current PQL query persists only the query", async () => {
    const { createView } = setup();
    await openMenu();
    fireEvent.click(screen.getByRole("button", { name: "Save current view" }));
    fireEvent.change(screen.getByPlaceholderText("View name…"), {
      target: { value: "Saved highs" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save view" }));
    await waitFor(() => expect(createView).toHaveBeenCalledTimes(1));
    const [name, savedFilters] = createView.mock.calls[0] as [
      string,
      typeof EMPTY_FILTERS,
    ];
    expect(name).toBe("Saved highs");
    expect(savedFilters.pql).toBe("priority = High");
  });

  it("applying a view re-runs the query in PQL mode without writing to the URL", async () => {
    const { onViewApplied } = setup();
    await openMenu();
    fireEvent.click(screen.getByTitle("Apply view “High priority”"));
    expect(onViewApplied).toHaveBeenCalledWith("priority = High");
  });

  it("rename / delete / share / set-default all go through the shared hook", async () => {
    const { renameView, deleteView, setDefaultView, setViewShared } = setup();
    await openMenu();

    // Share toggle copy is workspace-scoped, not project-scoped.
    const share = screen.getByTitle("Share “High priority” with the workspace");
    fireEvent.click(share);
    await waitFor(() =>
      expect(setViewShared).toHaveBeenCalledWith("v1", true),
    );

    fireEvent.click(screen.getByTitle("Set as default view"));
    await waitFor(() =>
      expect(setDefaultView).toHaveBeenCalledWith("v1"),
    );

    fireEvent.click(screen.getByTitle("Rename “High priority”"));
    fireEvent.change(screen.getByDisplayValue("High priority"), {
      target: { value: "Renamed" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Rename" }));
    await waitFor(() =>
      expect(renameView).toHaveBeenCalledWith("v1", "Renamed"),
    );

    fireEvent.click(screen.getByTitle("Delete “High priority”"));
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(deleteView).toHaveBeenCalledWith("v1"));
  });

  it("non-owner rows are read-only: no rename/share/delete/default controls", async () => {
    const { onViewApplied } = setup();
    await openMenu();
    expect(screen.queryByTitle(/Rename “Team highs”/)).toBeNull();
    expect(screen.queryByTitle(/Share “Team highs”/)).toBeNull();
    expect(screen.queryByTitle(/Delete “Team highs”/)).toBeNull();
    // The single default button belongs to the own row; the shared
    // row carries none.
    expect(
      screen.queryAllByTitle("Set as default view").length,
    ).toBe(1);
    // Apply still works on a shared row.
    fireEvent.click(screen.getByTitle("Apply view “Team highs”"));
    expect(onViewApplied).toHaveBeenCalledWith("priority = High");
  });

  it("shows honest errors: share failure toasts, save 409 renders inline", async () => {
    const { createView, setViewShared } = setup();
    setViewShared.mockRejectedValue(new Error("boom"));
    createView.mockRejectedValue(new ApiError(409, "duplicate", "conflict"));
    await openMenu();

    fireEvent.click(
      screen.getByTitle("Share “High priority” with the workspace"),
    );
    await waitFor(() =>
      expect(mockToastAdd).toHaveBeenCalledWith(
        expect.objectContaining({
          title: "Could not update sharing",
          type: "error",
        }),
      ),
    );

    fireEvent.click(screen.getByRole("button", { name: "Save current view" }));
    fireEvent.change(screen.getByPlaceholderText("View name…"), {
      target: { value: "High priority" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save view" }));
    await screen.findByText("A view with that name already exists.");
  });
});
