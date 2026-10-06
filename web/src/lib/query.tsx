// TanStack Query client for the issue views (Task 19). One shared
// QueryClient with conservative defaults: no refetch-on-window-focus (the
// list is cursor-paginated and refetching would reset scroll position),
// single retry on failure.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      refetchOnWindowFocus: false,
      retry: 1,
      staleTime: 10_000,
    },
  },
});

export function QueryProvider({ children }: { children: ReactNode }) {
  return (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  );
}
