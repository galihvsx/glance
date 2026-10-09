// Shell event contract.
//
// ShortcutsHost is mounted OUTSIDE the app shell in App.tsx, so it cannot
// import the shell context (T1's `shell-context.tsx`) directly. Instead,
// shell-scoped shortcut actions are delivered as plain window CustomEvents;
// the AppShell (which owns `sidebarCollapsed` / `setSidebarCollapsed` and
// `setNotificationsOpen`) listens for them:
//
//   "glance:toggle-sidebar"    → AppShell flips sidebarCollapsed
//   "glance:open-notifications" → AppShell calls setNotificationsOpen(true)
//   "glance:close-overlay"     → AppShell closes its topmost overlay
//                                (sheet / drawer / mobile sidebar)
//
// Fired on `window` via dispatchShellEvent(). Listeners attach with
// window.addEventListener(EVENT_NAME, handler). No payload is sent; the
// shell decides what "close the topmost overlay" means for its own state.

export const SHELL_TOGGLE_SIDEBAR = "glance:toggle-sidebar";
export const SHELL_OPEN_NOTIFICATIONS = "glance:open-notifications";
export const SHELL_CLOSE_OVERLAY = "glance:close-overlay";

export type ShellEventName =
  | typeof SHELL_TOGGLE_SIDEBAR
  | typeof SHELL_OPEN_NOTIFICATIONS
  | typeof SHELL_CLOSE_OVERLAY;

export function dispatchShellEvent(name: ShellEventName): void {
  window.dispatchEvent(new CustomEvent(name));
}
