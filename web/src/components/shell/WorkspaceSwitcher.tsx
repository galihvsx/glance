// Workspace switcher for the app-shell sidebar header (T2).
//
// Trigger shows the current workspace name (route /w/:slug, else the last
// visited slug from shell context, else "Select workspace") and opens a
// dropdown with the workspace list + management entries.

import { useNavigate, useParams } from "react-router-dom";
import {
  Check,
  ChevronsUpDown,
  LayoutGrid,
  Plus,
  Settings,
} from "lucide-react";
import { useShell } from "./shell-context";
import { useWorkspaces } from "../../lib/useWorkspaces";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "../ui/dropdown-menu";
import { Skeleton } from "../ui/skeleton";
import { cn } from "cn";

export default function WorkspaceSwitcher() {
  const navigate = useNavigate();
  const { slug: routeSlug } = useParams();
  const { lastWorkspaceSlug, setLastWorkspaceSlug } = useShell();
  const workspacesQuery = useWorkspaces();

  const workspaces = workspacesQuery.data ?? [];
  // Route slug wins; the persisted last-visited slug covers routes without
  // a workspace (e.g. /notifications) so the trigger still names something.
  const currentSlug = routeSlug ?? lastWorkspaceSlug ?? null;
  const current = workspaces.find((w) => w.slug === currentSlug) ?? null;
  const settingsSlug = routeSlug ?? lastWorkspaceSlug;

  const openWorkspace = (wsSlug: string) => {
    setLastWorkspaceSlug(wsSlug);
    navigate(`/w/${wsSlug}`);
  };

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        className="flex h-9 w-full items-center justify-between rounded-md px-2 font-semibold transition-colors hover:bg-accent hover:text-accent-foreground"
        aria-label="Switch workspace"
      >
        <span className="min-w-0 truncate">
          {current ? current.name : (currentSlug ?? "Select workspace")}
        </span>
        <ChevronsUpDown className="ml-2 h-4 w-4 shrink-0 text-muted-foreground" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-56">
        <div className="max-h-72 overflow-y-auto">
          {workspacesQuery.isLoading && (
            <div className="space-y-1 p-1" aria-label="Loading workspaces">
              <Skeleton className="h-8 w-full" />
              <Skeleton className="h-8 w-full" />
              <Skeleton className="h-8 w-full" />
            </div>
          )}
          {workspacesQuery.isError && (
            <DropdownMenuItem disabled>
              <span className="text-muted-foreground">
                Failed to load workspaces
              </span>
            </DropdownMenuItem>
          )}
          {workspaces.map((w) => {
            const active = w.slug === currentSlug;
            return (
              <DropdownMenuItem
                key={w.id}
                onClick={() => openWorkspace(w.slug)}
                className={cn(active && "font-medium")}
              >
                <span className="min-w-0 flex-1 truncate">{w.name}</span>
                {active && <Check className="h-4 w-4 shrink-0" aria-label="current workspace" />}
              </DropdownMenuItem>
            );
          })}
          {!workspacesQuery.isLoading && workspaces.length === 0 && (
            <DropdownMenuItem disabled>
              <span className="text-muted-foreground">No workspaces yet</span>
            </DropdownMenuItem>
          )}
        </div>
        <DropdownMenuSeparator />
        <DropdownMenuItem onClick={() => navigate("/w")}>
          <Plus className="h-4 w-4" />
          New workspace
        </DropdownMenuItem>
        <DropdownMenuItem
          disabled={!settingsSlug}
          onClick={() => settingsSlug && navigate(`/w/${settingsSlug}/settings`)}
        >
          <Settings className="h-4 w-4" />
          Workspace settings
        </DropdownMenuItem>
        <DropdownMenuItem onClick={() => navigate("/w")}>
          <LayoutGrid className="h-4 w-4" />
          All workspaces
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
