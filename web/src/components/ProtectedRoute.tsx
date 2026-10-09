import { Navigate, Outlet, useLocation } from "react-router-dom";
import { useAuth } from "../lib/auth";
import { useWorkspaces } from "../lib/useWorkspaces";
import { Skeleton } from "./ui/skeleton";

// Gate for authenticated routes: while the session is being resolved show a
// skeleton; unauthenticated visitors are sent to /login.
//
// Brand-new users (zero workspaces) are routed through the onboarding
// wizard first — every protected page funnels them to /onboarding, which
// itself redirects back once a workspace exists. Existing users see zero
// change: one cached query, no redirect.
export default function ProtectedRoute() {
  const { user, loading } = useAuth();
  const location = useLocation();
  const { data: workspaces, isLoading: workspacesLoading } = useWorkspaces();

  if (loading) {
    return (
      <div className="flex min-h-svh items-center justify-center">
        <div className="w-full max-w-sm space-y-3 p-6" aria-label="Loading">
          <Skeleton className="h-10 w-3/4" />
          <Skeleton className="h-4 w-full" />
          <Skeleton className="h-4 w-5/6" />
        </div>
      </div>
    );
  }

  if (!user) {
    return <Navigate to="/login" replace />;
  }

  if (
    location.pathname !== "/onboarding" &&
    !workspacesLoading &&
    workspaces !== undefined &&
    workspaces.length === 0
  ) {
    return <Navigate to="/onboarding" replace />;
  }

  return <Outlet />;
}
