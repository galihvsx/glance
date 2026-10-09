// Route guard for /admin (C5T1): only instance admins get through.
// Non-admins are redirected home (ProtectedRoute already handles the
// not-logged-in case, but the "login" decision is honored anyway for
// standalone use). The server (RequireAdmin) is the real gate — this is
// UX, not security.

import type { ReactNode } from "react";
import { Navigate } from "react-router-dom";
import { useAuth } from "../lib/auth";
import { adminRouteDecision } from "../lib/admin";
import { Skeleton } from "./ui/skeleton";

export default function AdminGuard({ children }: { children: ReactNode }) {
  const { user, loading } = useAuth();
  switch (adminRouteDecision(loading, user)) {
    case "loading":
      return (
        <div className="flex min-h-svh items-center justify-center">
          <div className="w-full max-w-sm space-y-3 p-6" aria-label="Loading">
            <Skeleton className="h-10 w-3/4" />
            <Skeleton className="h-4 w-full" />
            <Skeleton className="h-4 w-5/6" />
          </div>
        </div>
      );
    case "login":
      return <Navigate to="/login" replace />;
    case "home":
      return <Navigate to="/" replace />;
    case "allow":
      return <>{children}</>;
  }
}
