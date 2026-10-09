// Shell context (T1): app-wide UI state owned by the app shell.
//
// - sidebarCollapsed: expanded/rail preference, persisted to localStorage.
// - isMobile: live matchMedia for (max-width: 1023px).
// - mobileOpen: drawer state; auto-closes on route change.
// - notificationsOpen: T2's notifications sheet reads this.
// - lastWorkspaceSlug: last-visited workspace, persisted for deep-linking
//   (e.g. My Work when no slug is in the route).

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { useLocation } from "react-router-dom";

export const SIDEBAR_COLLAPSED_KEY = "glance:sidebar-collapsed";
export const LAST_WORKSPACE_KEY = "glance:last-workspace";
const MOBILE_QUERY = "(max-width: 1023px)";

function readBool(key: string): boolean {
  try {
    return localStorage.getItem(key) === "true";
  } catch {
    return false;
  }
}

function writeBool(key: string, value: boolean): void {
  try {
    localStorage.setItem(key, value ? "true" : "false");
  } catch {
    // Storage unavailable (private mode, SSR) — keep in-memory state only.
  }
}

export interface ShellContextValue {
  sidebarCollapsed: boolean;
  setSidebarCollapsed: (v: boolean) => void;
  isMobile: boolean;
  mobileOpen: boolean;
  setMobileOpen: (v: boolean) => void;
  notificationsOpen: boolean;
  setNotificationsOpen: (v: boolean) => void;
  lastWorkspaceSlug: string | null;
  setLastWorkspaceSlug: (slug: string) => void;
}

const ShellContext = createContext<ShellContextValue | null>(null);

export function useShell(): ShellContextValue {
  const ctx = useContext(ShellContext);
  if (!ctx) throw new Error("useShell must be used within a ShellProvider.");
  return ctx;
}

function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState<boolean>(() =>
    typeof window !== "undefined" && typeof window.matchMedia !== "undefined"
      ? window.matchMedia(query).matches
      : false,
  );
  useEffect(() => {
    if (typeof window.matchMedia === "undefined") return;
    const mql = window.matchMedia(query);
    const onChange = (e: MediaQueryListEvent) => setMatches(e.matches);
    mql.addEventListener("change", onChange);
    setMatches(mql.matches);
    return () => mql.removeEventListener("change", onChange);
  }, [query]);
  return matches;
}

export function ShellProvider({ children }: { children: ReactNode }) {
  const location = useLocation();

  const [sidebarCollapsed, setSidebarCollapsedState] = useState<boolean>(() =>
    readBool(SIDEBAR_COLLAPSED_KEY),
  );
  const [mobileOpen, setMobileOpenState] = useState(false);
  const [notificationsOpen, setNotificationsOpenState] = useState(false);
  const [lastWorkspaceSlug, setLastWorkspaceSlugState] = useState<
    string | null
  >(() => {
    try {
      return localStorage.getItem(LAST_WORKSPACE_KEY);
    } catch {
      return null;
    }
  });
  const isMobile = useMediaQuery(MOBILE_QUERY);

  const setSidebarCollapsed = useCallback((v: boolean) => {
    setSidebarCollapsedState(v);
    writeBool(SIDEBAR_COLLAPSED_KEY, v);
  }, []);

  const setMobileOpen = useCallback((v: boolean) => {
    setMobileOpenState(v);
  }, []);

  const setNotificationsOpen = useCallback((v: boolean) => {
    setNotificationsOpenState(v);
  }, []);

  const setLastWorkspaceSlug = useCallback((slug: string) => {
    setLastWorkspaceSlugState(slug);
    try {
      localStorage.setItem(LAST_WORKSPACE_KEY, slug);
    } catch {
      // ignore
    }
  }, []);

  // The mobile drawer must never survive a navigation.
  useEffect(() => {
    setMobileOpenState(false);
  }, [location.pathname]);

  const value = useMemo<ShellContextValue>(
    () => ({
      sidebarCollapsed,
      setSidebarCollapsed,
      isMobile,
      mobileOpen,
      setMobileOpen,
      notificationsOpen,
      setNotificationsOpen,
      lastWorkspaceSlug,
      setLastWorkspaceSlug,
    }),
    [
      sidebarCollapsed,
      setSidebarCollapsed,
      isMobile,
      mobileOpen,
      setMobileOpen,
      notificationsOpen,
      setNotificationsOpen,
      lastWorkspaceSlug,
      setLastWorkspaceSlug,
    ],
  );

  return (
    <ShellContext.Provider value={value}>{children}</ShellContext.Provider>
  );
}
