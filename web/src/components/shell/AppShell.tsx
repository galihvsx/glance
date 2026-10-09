// AppShell (T1): layout route for every protected page.
//
// <SidebarProvider><AppSidebar/><SidebarInset><TopBar/>[#project-tabs]<Outlet/>
// The shadcn Sidebar renders as a left overlay Sheet on mobile and as an
// icon-collapsible rail/expanded sidebar on desktop. The shell context drives
// the controlled open state (persisted) and the mobile drawer state
// (auto-closes on navigation).

import { useEffect } from "react";
import { Outlet, useMatch } from "react-router-dom";
import { ShellProvider, useShell } from "./shell-context";
import { SidebarInset, SidebarProvider, useSidebar } from "../ui/sidebar";
import {
  SHELL_CLOSE_OVERLAY,
  SHELL_OPEN_NOTIFICATIONS,
  SHELL_TOGGLE_SIDEBAR,
} from "../../lib/shell-events";
import AppSidebar from "./AppSidebar";
import NotificationsSheet from "./NotificationsSheet";
import TopBar from "./TopBar";

/** Two-way sync between the shell context's mobileOpen and the shadcn
 *  sidebar's internal openMobile, so both the context API and the built-in
 *  Sheet/trigger behaviors stay in step. */
function MobileSync() {
  const { mobileOpen, setMobileOpen } = useShell();
  const { openMobile, setOpenMobile } = useSidebar();

  useEffect(() => {
    setOpenMobile(mobileOpen);
  }, [mobileOpen, setOpenMobile]);

  useEffect(() => {
    setMobileOpen(openMobile);
  }, [openMobile, setMobileOpen]);

  return null;
}

/** Listens for the window-event contract from ShortcutsHost (see
 *  lib/shell-events.ts): the shell owns the state, the host only forwards
 *  shortcut actions. Must be mounted inside ShellProvider. */
function ShellEventBridge() {
  const {
    sidebarCollapsed,
    setSidebarCollapsed,
    notificationsOpen,
    setNotificationsOpen,
    setMobileOpen,
  } = useShell();

  useEffect(() => {
    const onToggle = () => setSidebarCollapsed(!sidebarCollapsed);
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
    sidebarCollapsed,
    setSidebarCollapsed,
    notificationsOpen,
    setNotificationsOpen,
    setMobileOpen,
  ]);

  return null;
}

function ShellChrome() {
  const { sidebarCollapsed, setSidebarCollapsed } = useShell();
  const projectMatch = useMatch("/w/:slug/p/:identifier/*");

  return (
    <SidebarProvider
      open={!sidebarCollapsed}
      onOpenChange={(open) => setSidebarCollapsed(!open)}
    >
      <MobileSync />
      <ShellEventBridge />
      <AppSidebar />
      <NotificationsSheet />
      <SidebarInset className="max-h-svh overflow-y-auto">
        <TopBar />
        {/* T3 seam: ProjectNav portals its tab bar into this node via
            createPortal when the node exists; the node only exists on
            project routes, so ProjectNav keeps its inline fallback for
            isolation/tests. */}
        {projectMatch && (
          <div
            id="shell-project-tabs"
            data-slot="project-tabs"
            className="sticky top-12 z-20 bg-background"
          />
        )}
        <Outlet />
      </SidebarInset>
    </SidebarProvider>
  );
}

export default function AppShell() {
  return (
    <ShellProvider>
      <ShellChrome />
    </ShellProvider>
  );
}
