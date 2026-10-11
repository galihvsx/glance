// AppShell (C19T1): layout route for every protected page — the Plane
// shell grid (prototype Screen 1, literal):
//
//   grid columns: 52px rail | 280px sidebar | minmax(0,1fr) main,
//   full viewport height.
//
// The rail is always visible. Collapsing the sidebar flips the grid to
// `52px | 0 | minmax(0,1fr)` (.18s ease); the sidebar fades out
// (opacity 0 + pointer-events none, .12s) and a reopen icon-button
// appears inside the topbar's workspace switcher. On mobile (≤900px)
// the sidebar column is always 0 and opens as an overlay drawer
// (auto-closes on navigation, Escape closes via the close-overlay
// event).
//
// The shell context drives the persisted collapse preference and the
// mobile drawer state. NotificationsSheet + CommandPalette stay mounted
// at shell level (workspace-global).

import { useEffect } from "react";
import { Outlet, useMatch } from "react-router-dom";
import { ShellProvider, useShell } from "./shell-context";
import {
  SHELL_CLOSE_OVERLAY,
  SHELL_OPEN_NOTIFICATIONS,
  SHELL_TOGGLE_SIDEBAR,
} from "../../lib/shell-events";
import AppSidebar from "./AppSidebar";
import CommandPalette from "../CommandPalette";
import NotificationsSheet from "./NotificationsSheet";
import Rail from "./Rail";
import TopBar from "./TopBar";
import { cn } from "cn";

/** Listens for the window-event contract from ShortcutsHost (see
 *  lib/shell-events.ts): the shell owns the state, the host only forwards
 *  shortcut actions. Must be mounted inside ShellProvider. */
function ShellEventBridge() {
  const {
    isMobile,
    mobileOpen,
    setMobileOpen,
    sidebarCollapsed,
    setSidebarCollapsed,
    notificationsOpen,
    setNotificationsOpen,
  } = useShell();

  useEffect(() => {
    // Cmd+B: on mobile the sidebar is an overlay drawer, so the toggle
    // drives the drawer; on desktop it flips the collapse preference.
    const onToggle = () => {
      if (isMobile) setMobileOpen(!mobileOpen);
      else setSidebarCollapsed(!sidebarCollapsed);
    };
    // Idempotent: firing g n while the sheet is open keeps it open rather
    // than toggling (spec §3).
    const onOpenNotifications = () => {
      if (!notificationsOpen) setNotificationsOpen(true);
    };
    const onCloseOverlay = () => {
      setNotificationsOpen(false);
      setMobileOpen(false);
    };
    window.addEventListener(SHELL_TOGGLE_SIDEBAR, onToggle);
    window.addEventListener(SHELL_OPEN_NOTIFICATIONS, onOpenNotifications);
    window.addEventListener(SHELL_CLOSE_OVERLAY, onCloseOverlay);
    return () => {
      window.removeEventListener(SHELL_TOGGLE_SIDEBAR, onToggle);
      window.removeEventListener(SHELL_OPEN_NOTIFICATIONS, onOpenNotifications);
      window.removeEventListener(SHELL_CLOSE_OVERLAY, onCloseOverlay);
    };
  }, [
    isMobile,
    mobileOpen,
    setMobileOpen,
    sidebarCollapsed,
    setSidebarCollapsed,
    notificationsOpen,
    setNotificationsOpen,
  ]);

  return null;
}

/** Mobile (≤900px) overlay drawer. The sidebar column is always 0 on
 *  mobile; this drawer is the only way to reach it. */
function MobileDrawer() {
  const { isMobile, mobileOpen, setMobileOpen } = useShell();
  if (!isMobile) return null;
  const close = () => setMobileOpen(false);
  return (
    <div
      className={cn("fixed inset-0 z-50", !mobileOpen && "pointer-events-none")}
      role="dialog"
      aria-modal="true"
      aria-label="Sidebar"
      aria-hidden={!mobileOpen}
    >
      <div
        className={cn(
          "absolute inset-0 bg-black/60 transition-opacity duration-200",
          mobileOpen ? "opacity-100" : "opacity-0",
        )}
        onClick={close}
      />
      <div
        className={cn(
          "absolute top-0 left-0 h-full w-[280px] transition-transform duration-200 ease-out",
          mobileOpen ? "translate-x-0" : "-translate-x-full",
        )}
      >
        <AppSidebar />
      </div>
    </div>
  );
}

function ShellChrome() {
  const { sidebarCollapsed, isMobile } = useShell();
  const projectMatch = useMatch("/w/:slug/p/:identifier/*");

  // Prototype: ≤900px the sidebar collapses to grid-0 (reopen via the
  // switcher, which on mobile opens the drawer).
  const collapsed = sidebarCollapsed || isMobile;

  return (
    <>
      <div
        data-slot="shell-grid"
        className="grid h-svh w-full bg-canvas transition-[grid-template-columns] duration-[180ms] ease"
        style={{
          gridTemplateColumns: collapsed
            ? "52px 0px minmax(0,1fr)"
            : "52px 280px minmax(0,1fr)",
        }}
      >
        <Rail />
        <div
          className={cn(
            "min-h-0 min-w-0 overflow-hidden",
            collapsed &&
              "pointer-events-none opacity-0 transition-opacity duration-[120ms]",
          )}
          aria-hidden={collapsed}
        >
          <AppSidebar />
        </div>
        <div className="flex min-h-0 min-w-0 flex-col">
          <TopBar />
          <div className="min-h-0 flex-1 overflow-y-auto">
            {/* T3 seam: ProjectNav portals its tab bar into this node via
                createPortal when the node exists; the node only exists on
                project routes, so ProjectNav keeps its inline fallback for
                isolation/tests. */}
            {projectMatch && (
              <div
                id="shell-project-tabs"
                data-slot="project-tabs"
                className="sticky top-0 z-20"
              />
            )}
            <Outlet />
          </div>
        </div>
      </div>
      <ShellEventBridge />
      <NotificationsSheet />
      {/* C18T0: the command palette lives at shell level so it is
          workspace-global, not per-project. */}
      <CommandPalette />
      <MobileDrawer />
    </>
  );
}

export default function AppShell() {
  return (
    <ShellProvider>
      <ShellChrome />
    </ShellProvider>
  );
}
