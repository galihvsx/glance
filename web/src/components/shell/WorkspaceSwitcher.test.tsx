// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import WorkspaceSwitcher from "./WorkspaceSwitcher";
import { ShellProvider, useShell } from "./shell-context";

// T1's real ShellProvider persists lastWorkspaceSlug to localStorage —
// clear it between tests so they don't leak state into each other.
beforeEach(() => {
  window.localStorage.clear();
});

vi.mock("../../lib/useWorkspaces", () => ({
  useWorkspaces: () => ({
    data: [
      { id: "1", slug: "acme", name: "Acme Corp", role: 20, created_at: "", updated_at: "" },
      { id: "2", slug: "beta", name: "Beta LLC", role: 20, created_at: "", updated_at: "" },
    ],
    isLoading: false,
    isError: false,
  }),
}));

function LocationProbe() {
  const { pathname } = useLocation();
  return <div data-testid="location">{pathname}</div>;
}

function SlugProbe() {
  const { lastWorkspaceSlug } = useShell();
  return <div data-testid="last-slug">{lastWorkspaceSlug ?? "none"}</div>;
}

function renderSwitcher(initialEntries: string[]) {
  return render(
    <QueryClientProvider client={new QueryClient()}>
      <MemoryRouter initialEntries={initialEntries}>
        <ShellProvider>
          <Routes>
            <Route
              path="/w/:slug"
              element={
                <>
                  <WorkspaceSwitcher />
                  <LocationProbe />
                  <SlugProbe />
                </>
              }
            />
            <Route
              path="*"
              element={
                <>
                  <WorkspaceSwitcher />
                  <LocationProbe />
                  <SlugProbe />
                </>
              }
            />
          </Routes>
        </ShellProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("WorkspaceSwitcher", () => {
  it("shows the current workspace name from the route", () => {
    renderSwitcher(["/w/acme"]);
    expect(screen.getByLabelText("Switch workspace")).toHaveTextContent(
      "Acme Corp",
    );
  });

  it("lists workspaces with the active one checked, and navigates on select", async () => {
    renderSwitcher(["/w/acme"]);

    fireEvent.click(screen.getByLabelText("Switch workspace"));
    const menu = await screen.findByRole("menu");
    const menuQueries = within(menu);
    expect(menuQueries.getByText("Beta LLC")).toBeInTheDocument();

    // Active workspace gets the check icon.
    expect(menuQueries.getByLabelText("current workspace")).toBeInTheDocument();
    const acmeItem = menuQueries.getByText("Acme Corp").closest("[role='menuitem']");
    expect(acmeItem).toContainElement(
      menuQueries.getByLabelText("current workspace"),
    );

    fireEvent.click(menuQueries.getByText("Beta LLC"));

    await waitFor(() => {
      expect(screen.getByTestId("location")).toHaveTextContent("/w/beta");
    });
    expect(screen.getByTestId("last-slug")).toHaveTextContent("beta");
  });

  it("navigates to workspace settings for the current slug", async () => {
    renderSwitcher(["/w/acme"]);

    fireEvent.click(screen.getByLabelText("Switch workspace"));
    await waitFor(() => {
      expect(screen.getByText("Workspace settings")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByText("Workspace settings"));
    await waitFor(() => {
      expect(screen.getByTestId("location")).toHaveTextContent(
        "/w/acme/settings",
      );
    });
  });

  it("shows management entries", async () => {
    renderSwitcher(["/w/acme"]);

    fireEvent.click(screen.getByLabelText("Switch workspace"));
    await waitFor(() => {
      expect(screen.getByText("New workspace")).toBeInTheDocument();
      expect(screen.getByText("All workspaces")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByText("All workspaces"));
    await waitFor(() => {
      expect(screen.getByTestId("location")).toHaveTextContent("/w");
    });
  });

  it("shows 'Select workspace' when no slug is known", () => {
    render(
      <MemoryRouter initialEntries={["/notifications"]}>
        <ShellProvider>
          <WorkspaceSwitcher />
        </ShellProvider>
      </MemoryRouter>,
    );
    expect(screen.getByLabelText("Switch workspace")).toHaveTextContent(
      "Select workspace",
    );
  });
});
