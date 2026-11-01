// @vitest-environment jsdom
// PageHeader (C19T4) tests: breadcrumb, view-segment sync with the route,
// filter/display slots, analytics/add/more actions.
import { fireEvent, render, screen } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import {
  MemoryRouter,
  Route,
  Routes,
  useLocation,
} from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import PageHeader from "./PageHeader";
import { makeQueryClient } from "./test-utils";
import { workspacesKey } from "../../lib/useWorkspaces";
import type { Workspace } from "../../lib/types";

const workspaces: Workspace[] = [
  {
    id: "ws-1",
    slug: "acme",
    name: "Acme Corp",
    // The remaining fields are irrelevant to the header; cast through.
  } as Workspace,
];

let lastPath = "";
function LocationSpy() {
  lastPath = useLocation().pathname + useLocation().search;
  return null;
}

function renderHeader(path: string, props: React.ComponentProps<typeof PageHeader>) {
  lastPath = "";
  const qc = makeQueryClient([[workspacesKey, workspaces]]);
  render(
    <MemoryRouter initialEntries={[path]}>
      <QueryClientProvider client={qc}>
        <Routes>
          <Route
            path="/w/:slug/p/:identifier/*"
            element={
              <>
                <PageHeader {...props} />
                <LocationSpy />
              </>
            }
          />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  vi.restoreAllMocks();
});

describe("PageHeader", () => {
  it("renders the workspace breadcrumb, view name, and count", () => {
    renderHeader("/w/acme/p/ENG", { viewName: "Work items", count: 7 });
    expect(screen.getByText("Acme Corp")).toBeTruthy();
    expect(screen.getByText("Work items")).toBeTruthy();
    expect(screen.getByText("7")).toBeTruthy();
    // Chevron separator present
    expect(screen.getByLabelText("Breadcrumb").textContent).toContain("›");
  });

  it("marks the segment matching the current route as active", () => {
    renderHeader("/w/acme/p/ENG/board", { viewName: "Board", showViews: true });
    const board = screen.getByRole("tab", { name: "Board" });
    const list = screen.getByRole("tab", { name: "List" });
    expect(board.getAttribute("aria-selected")).toBe("true");
    expect(list.getAttribute("aria-selected")).toBe("false");
  });

  it("navigates when a segment is clicked (spreadsheet route = Table tab)", () => {
    renderHeader("/w/acme/p/ENG", { viewName: "Work items", showViews: true });
    fireEvent.click(screen.getByRole("tab", { name: "Table" }));
    expect(lastPath).toBe("/w/acme/p/ENG/spreadsheet");
    fireEvent.click(screen.getByRole("tab", { name: "Timeline" }));
    expect(lastPath).toBe("/w/acme/p/ENG/gantt");
  });

  it("carries filter params (including pql) across view switches and drops peek/create", () => {
    renderHeader(
      "/w/acme/p/ENG?state=started&pql=priority+%3D+High&peek=abc&create=1",
      { viewName: "Work items", showViews: true },
    );
    fireEvent.click(screen.getByRole("tab", { name: "Table" }));
    expect(lastPath).toBe(
      "/w/acme/p/ENG/spreadsheet?state=started&pql=priority+%3D+High",
    );
    fireEvent.click(screen.getByRole("tab", { name: "List" }));
    expect(lastPath).toBe("/w/acme/p/ENG?state=started&pql=priority+%3D+High");
  });

  it("renders filter/display/extra slots in the header", () => {
    renderHeader("/w/acme/p/ENG", {
      viewName: "Work items",
      filterPanel: <button type="button">Filter</button>,
      displayPanel: <button type="button">Display</button>,
      extraControls: <button type="button">Views</button>,
    });
    expect(screen.getByRole("button", { name: "Filter" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Display" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Views" })).toBeTruthy();
  });

  it("navigates to analytics and hides the button on the analytics route", () => {
    renderHeader("/w/acme/p/ENG", { viewName: "Work items" });
    fireEvent.click(screen.getByRole("button", { name: "Analytics" }));
    expect(lastPath).toBe("/w/acme/p/ENG/analytics");

    renderHeader("/w/acme/p/ENG/analytics", { viewName: "Analytics" });
    expect(
      screen.queryByRole("button", { name: "Analytics" }),
    ).toBeNull();
  });

  it("defaults the Add button to the list ?create=1 deep-link", () => {
    renderHeader("/w/acme/p/ENG/board", { viewName: "Board", showViews: true });
    fireEvent.click(screen.getByRole("button", { name: "Add work item" }));
    expect(lastPath).toBe("/w/acme/p/ENG?create=1");
  });

  it("calls a custom addButton handler with a custom label", () => {
    const onClick = vi.fn();
    renderHeader("/w/acme/p/ENG/cycles", {
      viewName: "Cycles",
      addButton: { label: "New cycle", onClick },
    });
    fireEvent.click(screen.getByRole("button", { name: "New cycle" }));
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it("hides the Add button when addButton is null", () => {
    renderHeader("/w/acme/p/ENG/settings", {
      viewName: "Settings",
      addButton: null,
    });
    expect(screen.queryByRole("button", { name: "Add work item" })).toBeNull();
  });

  it("exposes custom menu items in the more menu", () => {
    const onClick = vi.fn();
    renderHeader("/w/acme/p/ENG", {
      viewName: "Work items",
      menuItems: [{ label: "Intake inbox", onClick }],
    });
    fireEvent.click(screen.getByRole("button", { name: "More actions" }));
    fireEvent.click(screen.getByText("Intake inbox"));
    expect(onClick).toHaveBeenCalledTimes(1);
  });
});
