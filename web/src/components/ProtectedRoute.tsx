import { Navigate, Outlet } from "react-router-dom";
import { useAuth } from "../lib/auth";
import { Skeleton } from "./ui/skeleton";

// Gate for authenticated routes: while the session is being resolved show a
// skeleton; unauthenticated visitors are sent to /login.
export default function ProtectedRoute() {
  const { user, loading } = useAuth();

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

  return <Outlet />;
}
