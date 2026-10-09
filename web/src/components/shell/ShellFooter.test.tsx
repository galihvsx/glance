// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import ShellFooter from "./ShellFooter";
import type { User } from "../../lib/auth";

const state = vi.hoisted(() => ({
  user: null as User | null,
  logout: vi.fn(),
}));

vi.mock("../../lib/auth", () => ({
  useAuth: () => ({
    user: state.user,
    loading: false,
    refresh: vi.fn(),
    logout: state.logout,
  }),
}));

const adminUser: User = {
  id: "u1",
  email: "admin@example.com",
  name: "Ada Admin",
  avatar_url: null,
  is_active: true,
  is_admin: true,
  last_login_at: null,
  created_at: "",
  updated_at: "",
};

const memberUser: User = { ...adminUser, name: "Moe Member", is_admin: false };

function LocationProbe() {
  const { pathname } = useLocation();
  return <div data-testid="location">{pathname}</div>;
}

function renderFooter(collapsed = false) {
  return render(
    <MemoryRouter initialEntries={["/"]}>
      <ShellFooter collapsed={collapsed} />
      <LocationProbe />
    </MemoryRouter>,
  );
}

async function openMenu() {
  fireEvent.click(screen.getByLabelText(/Account:/));
  await waitFor(() => {
    expect(screen.getByText("Profile")).toBeInTheDocument();
  });
}

describe("ShellFooter", () => {
  it("shows the admin link only for admins", async () => {
    state.user = adminUser;
    renderFooter();
    expect(screen.getByText("Ada Admin")).toBeInTheDocument();

    await openMenu();
    expect(screen.getByText("Administration")).toBeInTheDocument();

    fireEvent.click(screen.getByText("Administration"));
    await waitFor(() => {
      expect(screen.getByTestId("location")).toHaveTextContent("/admin");
    });
  });

  it("hides the admin link for non-admins", async () => {
    state.user = memberUser;
    renderFooter();

    await openMenu();
    expect(screen.queryByText("Administration")).not.toBeInTheDocument();
    expect(screen.getByText("API tokens")).toBeInTheDocument();
  });

  it("navigates to profile and API tokens", async () => {
    state.user = memberUser;
    renderFooter();

    await openMenu();
    fireEvent.click(screen.getByText("Profile"));
    await waitFor(() => {
      expect(screen.getByTestId("location")).toHaveTextContent("/profile");
    });
  });

  it("calls logout from the menu", async () => {
    state.user = memberUser;
    state.logout.mockClear();
    renderFooter();

    await openMenu();
    fireEvent.click(screen.getByText("Log out"));
    expect(state.logout).toHaveBeenCalledTimes(1);
  });

  it("renders avatar-only in collapsed mode", () => {
    state.user = memberUser;
    renderFooter(true);

    expect(screen.getByLabelText(/Account:/)).toBeInTheDocument();
    expect(screen.queryByText("Moe Member")).not.toBeInTheDocument();
    // Theme toggle is still present.
    expect(
      screen.getByLabelText(/Switch to (light|dark) theme/),
    ).toBeInTheDocument();
  });
});
