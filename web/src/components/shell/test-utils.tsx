// Shared test helpers for the shell tests.

import { useEffect } from "react";
import { QueryClient } from "@tanstack/react-query";
import { useShell, type ShellContextValue } from "./shell-context";

export function stubMatchMedia(initialMatches: boolean) {
  const listeners = new Set<(e: { matches: boolean }) => void>();
  const mql = {
    matches: initialMatches,
    media: "",
    addEventListener: (_type: string, fn: (e: { matches: boolean }) => void) =>
      void listeners.add(fn),
    removeEventListener: (
      _type: string,
      fn: (e: { matches: boolean }) => void,
    ) => void listeners.delete(fn),
    addListener: (fn: (e: { matches: boolean }) => void) =>
      void listeners.add(fn),
    removeListener: (fn: (e: { matches: boolean }) => void) =>
      void listeners.delete(fn),
    dispatchEvent: () => false,
  };
  Object.defineProperty(window, "matchMedia", {
    value: () => mql,
    writable: true,
    configurable: true,
  });
  return {
    /** Simulate the media query result flipping. */
    setMatches(matches: boolean) {
      mql.matches = matches;
      listeners.forEach((fn) => fn({ matches }));
    },
  };
}

export function makeQueryClient(
  seeds: [readonly unknown[], unknown][] = [],
): QueryClient {
  const qc = new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: Infinity, gcTime: Infinity },
    },
  });
  for (const [key, data] of seeds) qc.setQueryData(key, data);
  return qc;
}

/** Captures the latest shell context value into `capture.current`. */
export function ShellCapture({
  capture,
}: {
  capture: { current: ShellContextValue | null };
}) {
  const value = useShell();
  useEffect(() => {
    capture.current = value;
  });
  return null;
}
