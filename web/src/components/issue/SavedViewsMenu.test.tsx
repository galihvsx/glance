// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import SavedViewsMenu from "./SavedViewsMenu";
import { useSavedViews } from "./useSavedViews";
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
