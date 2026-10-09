// @vitest-environment jsdom
import { act, render } from "@testing-library/react";
import { useEffect } from "react";
import { MemoryRouter, useNavigate, type NavigateFunction } from "react-router-dom";
import { beforeEach, describe, expect, it } from "vitest";
import {
  LAST_WORKSPACE_KEY,
  SIDEBAR_COLLAPSED_KEY,
  ShellProvider,
  useShell,
  type ShellContextValue,
} from "./shell-context";
import { stubMatchMedia } from "./test-utils";

let latest: ShellContextValue | null = null;
let navigate: NavigateFunction | null = null;

function Probe() {
  const value = useShell();
  useEffect(() => {
    latest = value;
  });
  return null;
}

function NavProbe() {
  const n = useNavigate();
  useEffect(() => {
    navigate = n;
  });
  return null;
}

function renderShell(initialPath = "/") {
  latest = null;
  navigate = null;
  return render(
    <MemoryRouter initialEntries={[initialPath]}>
      <ShellProvider>
        <Probe />
        <NavProbe />
      </ShellProvider>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  localStorage.clear();
  stubMatchMedia(false);
});

describe("sidebarCollapsed persistence", () => {
  it("defaults to expanded and round-trips through localStorage", () => {
    renderShell();
    expect(latest?.sidebarCollapsed).toBe(false);

    act(() => latest?.setSidebarCollapsed(true));
    expect(latest?.sidebarCollapsed).toBe(true);
    expect(localStorage.getItem(SIDEBAR_COLLAPSED_KEY)).toBe("true");

    act(() => latest?.setSidebarCollapsed(false));
    expect(localStorage.getItem(SIDEBAR_COLLAPSED_KEY)).toBe("false");
  });

  it("reads the persisted value on init", () => {
    localStorage.setItem(SIDEBAR_COLLAPSED_KEY, "true");
    renderShell();
    expect(latest?.sidebarCollapsed).toBe(true);
  });
});

describe("mobile drawer", () => {
  it("opens/closes via setMobileOpen", () => {
    renderShell();
    expect(latest?.mobileOpen).toBe(false);
    act(() => latest?.setMobileOpen(true));
    expect(latest?.mobileOpen).toBe(true);
    act(() => latest?.setMobileOpen(false));
    expect(latest?.mobileOpen).toBe(false);
  });

  it("auto-closes on route change", () => {
    renderShell("/a");
    act(() => latest?.setMobileOpen(true));
    expect(latest?.mobileOpen).toBe(true);

    act(() => navigate?.("/b"));
    expect(latest?.mobileOpen).toBe(false);
  });
});

describe("isMobile", () => {
  it("reflects the media query and follows changes", () => {
    const media = stubMatchMedia(false);
    renderShell();
    expect(latest?.isMobile).toBe(false);

    act(() => media.setMatches(true));
    expect(latest?.isMobile).toBe(true);

    act(() => media.setMatches(false));
    expect(latest?.isMobile).toBe(false);
  });
});

describe("notificationsOpen", () => {
  it("toggles", () => {
    renderShell();
    expect(latest?.notificationsOpen).toBe(false);
    act(() => latest?.setNotificationsOpen(true));
    expect(latest?.notificationsOpen).toBe(true);
  });
});

describe("lastWorkspaceSlug", () => {
  it("persists and rehydrates", () => {
    renderShell();
    expect(latest?.lastWorkspaceSlug).toBeNull();

    act(() => latest?.setLastWorkspaceSlug("acme"));
    expect(localStorage.getItem(LAST_WORKSPACE_KEY)).toBe("acme");

    renderShell();
    expect(latest?.lastWorkspaceSlug).toBe("acme");
  });
});
