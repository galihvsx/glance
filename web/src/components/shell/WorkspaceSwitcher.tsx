// Workspace switcher for the topbar (C19T1).
//
// 30px trigger: 20px workspace avatar + name + chevron-down. When the
// sidebar is collapsed (or on mobile, where the sidebar is always
// collapsed), a 28px reopen icon-button appears at the left of the
// switcher — per the prototype, the reopen control lives inside the
// switcher and does not open the dropdown (stopPropagation).
//
// Trigger shows the current workspace name (route /w/:slug, else the last
// visited slug from shell context, else "Select workspace") and opens a
// dropdown with the workspace list + management entries.

import { useNavigate, useParams } from "react-router-dom";
import { Check, ChevronDown, LayoutGrid, Plus, Settings, PanelLeftOpen } from "lucide-react";
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

export function workspaceAvatarLabel(name: string | null | undefined): string {
  const c = (name ?? "").trim().charAt(0);
  return c ? c.toUpperCase() : "–";
}

function ReopenSidebarButton() {
  const { isMobile, setMobileOpen, setSidebarCollapsed } = useShell();
  return (
    <button
      type="button"
      aria-label="Open sidebar"
      title="Open sidebar"
      onClick={(e) => {
        e.stopPropagation();
        if (isMobile) setMobileOpen(true);
        else setSidebarCollapsed(false);
      }}
      className="flex size-7 shrink-0 items-center justify-center rounded-[5px] text-ink-muted transition-colors hover:bg-wash-hover hover:text-ink"
    >
      <PanelLeftOpen className="size-[15px]" strokeWidth={1.6} aria-hidden="true" />
    </button>
  );
}

export default function WorkspaceSwitcher() {
  const navigate = useNavigate();
  const { slug: routeSlug } = useParams();
  const { lastWorkspaceSlug, setLastWorkspaceSlug, sidebarCollapsed, isMobile } =
    useShell();
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

  const showReopen = sidebarCollapsed || isMobile;

  return (
    <div className="flex min-w-0 items-center gap-[7px]">
      {showReopen && <ReopenSidebarButton />}
      <DropdownMenu>
        <DropdownMenuTrigger
          className="flex h-[30px] min-w-0 items-center gap-[7px] rounded-[5px] px-[7px] text-[12px] font-medium text-ink transition-colors hover:bg-wash-hover"
          aria-label="Switch workspace"
        >
          <span
            aria-hidden="true"
            className="flex size-5 shrink-0 items-center justify-center rounded-[5px] bg-[#e4e4e7] text-[9px] font-extrabold text-[#18181b]"
          >
            {workspaceAvatarLabel(current?.name)}
          </span>
          <span className="min-w-0 truncate">
            {current ? current.name : (currentSlug ?? "Select workspace")}
          </span>
          <ChevronDown className="size-3 shrink-0 text-ink-muted" aria-hidden="true" />
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
    </div>
  );
}
