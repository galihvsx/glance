// AppSidebar (T1): the app shell's sidebar, built only from
// components/ui/sidebar.tsx primitives.
//
// Sections (top → bottom): workspace header, Triage, Favorites, Projects,
// footer. T2 owns the workspace switcher dropdown, the notifications sheet,
// and the real footer (avatar dropdown + ThemeToggle) — the data-slot hooks
// and TODOs below are the seams for that work.

import { useEffect, useMemo, useState } from "react";
import { Link, useLocation, useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import {
  Bell,
  Briefcase,
  Building2,
  ChevronDown,
  ChevronsUpDown,
  CircleDot,
  Folder,
  FolderKanban,
  Home,
  Plus,
  Search,
  User as UserIcon,
} from "lucide-react";
import { api } from "../../lib/api";
import {
  issueFavoriteHref,
  projectFavoriteHref,
  useFavorites,
} from "../../lib/favorites";
import { useUnreadCount } from "../../lib/notifications";
import type { Project } from "../../lib/types";
import { useWorkspaces } from "../../lib/useWorkspaces";
import { useShell } from "./shell-context";
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "../ui/collapsible";
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarInput,
  SidebarMenu,
  SidebarMenuBadge,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarMenuSkeleton,
  SidebarRail,
  SidebarSeparator,
} from "../ui/sidebar";

const PROJECTS_COLLAPSED_KEY = "glance:projects-collapsed";

function readProjectsCollapsed(): boolean {
  try {
    return localStorage.getItem(PROJECTS_COLLAPSED_KEY) === "true";
  } catch {
    return false;
  }
}

function useWorkspaceProjects(slug: string | undefined) {
  return useQuery({
    queryKey: ["projects", slug],
    queryFn: () =>
      api
        .get<{ projects: Project[] }>(
          `/api/v1/workspaces/${encodeURIComponent(slug ?? "")}/projects`,
        )
        .then((d) => d.projects),
    enabled: !!slug,
  });
}

function WorkspaceHeader() {
  const { slug: routeSlug } = useParams<{ slug: string }>();
  const { lastWorkspaceSlug } = useShell();
  const { data: workspaces, isLoading } = useWorkspaces();
  const slug = routeSlug ?? lastWorkspaceSlug ?? undefined;
  const workspace = workspaces?.find((w) => w.slug === slug);
  const name = workspace?.name ?? slug ?? "glance";

  if (!isLoading && (workspaces?.length ?? 0) === 0) {
    // No workspaces: the header becomes a create CTA (spec §2).
    return (
      <SidebarHeader>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton
              render={<Link to="/w" />}
              tooltip="Create workspace"
            >
              <Plus />
              <span>Create workspace</span>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarHeader>
    );
  }

  return (
    <SidebarHeader>
      <SidebarMenu>
        <SidebarMenuItem>
          {/* TODO(T2): replace with WorkspaceSwitcher dropdown (workspace
              list, "New workspace", "Workspace settings", "All workspaces"). */}
          <SidebarMenuButton
            size="lg"
            data-slot="workspace-switcher"
            tooltip={name}
          >
            <div className="flex aspect-square size-8 items-center justify-center rounded-lg bg-sidebar-primary text-sidebar-primary-foreground">
              <span className="text-sm font-semibold">
                {name.charAt(0).toUpperCase() || "?"}
              </span>
            </div>
            <div className="grid flex-1 text-left text-sm leading-tight">
              <span className="truncate font-medium">{name}</span>
            </div>
            <ChevronsUpDown className="ml-auto" />
          </SidebarMenuButton>
        </SidebarMenuItem>
      </SidebarMenu>
    </SidebarHeader>
  );
}

function TriageGroup() {
  const { pathname } = useLocation();
  const { slug: routeSlug } = useParams<{ slug: string }>();
  const { lastWorkspaceSlug, setNotificationsOpen } = useShell();
  const { data: unread } = useUnreadCount();

  const slug = routeSlug ?? lastWorkspaceSlug ?? undefined;
  const myWorkHref = slug
    ? `/w/${encodeURIComponent(slug)}/my-work`
    : "/w";

  return (
    <SidebarGroup>
      <SidebarGroupLabel>Triage</SidebarGroupLabel>
      <SidebarGroupContent>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton
              render={<Link to="/" />}
              isActive={pathname === "/"}
              tooltip="Home"
            >
              <Home />
              <span>Home</span>
            </SidebarMenuButton>
          </SidebarMenuItem>
          <SidebarMenuItem>
            <SidebarMenuButton
              render={<Link to={myWorkHref} />}
              isActive={pathname === myWorkHref}
              tooltip="My Work"
            >
              <Briefcase />
              <span>My Work</span>
            </SidebarMenuButton>
          </SidebarMenuItem>
          <SidebarMenuItem>
            <SidebarMenuButton
              onClick={() => setNotificationsOpen(true)}
              isActive={pathname === "/notifications"}
              data-slot="notifications-sheet-trigger"
              tooltip="Notifications"
            >
              <Bell />
              <span>Notifications</span>
            </SidebarMenuButton>
            {(unread ?? 0) > 0 && (
              <SidebarMenuBadge>
                {(unread ?? 0) > 99 ? "99+" : String(unread)}
              </SidebarMenuBadge>
            )}
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarGroupContent>
    </SidebarGroup>
  );
}

function FavoritesGroup() {
  const { pathname } = useLocation();
  const { data: favorites, isLoading } = useFavorites();

  const issues = favorites?.issues ?? [];
  const projects = favorites?.projects ?? [];

  if (isLoading) {
    return (
      <SidebarGroup>
        <SidebarGroupLabel>Favorites</SidebarGroupLabel>
        <SidebarGroupContent>
          <SidebarMenu>
            <SidebarMenuItem>
              <SidebarMenuSkeleton showIcon />
            </SidebarMenuItem>
            <SidebarMenuItem>
              <SidebarMenuSkeleton showIcon />
            </SidebarMenuItem>
          </SidebarMenu>
        </SidebarGroupContent>
      </SidebarGroup>
    );
  }

  // Hidden when empty (not an empty-state card) — the Home page covers
  // favorites discovery (spec §1).
  if (issues.length === 0 && projects.length === 0) return null;

  return (
    <SidebarGroup>
      <SidebarGroupLabel>Favorites</SidebarGroupLabel>
      <SidebarGroupContent>
        <SidebarMenu>
          {issues.map((f) => {
            const href = issueFavoriteHref(f);
            return (
              <SidebarMenuItem key={`issue-${f.id}`}>
                <SidebarMenuButton
                  render={<Link to={href} />}
                  isActive={pathname === href}
                  tooltip={f.name}
                >
                  <CircleDot />
                  <span className="truncate">
                    <span className="text-muted-foreground">
                      {f.display_id}
                    </span>{" "}
                    {f.name}
                  </span>
                </SidebarMenuButton>
              </SidebarMenuItem>
            );
          })}
          {projects.map((f) => {
            const href = projectFavoriteHref(f);
            return (
              <SidebarMenuItem key={`project-${f.id}`}>
                <SidebarMenuButton
                  render={<Link to={href} />}
                  isActive={pathname === href}
                  tooltip={f.name}
                >
                  <Folder />
                  <span>{f.name}</span>
                </SidebarMenuButton>
              </SidebarMenuItem>
            );
          })}
        </SidebarMenu>
      </SidebarGroupContent>
    </SidebarGroup>
  );
}

function ProjectsGroup() {
  const { slug: routeSlug, identifier: routeIdentifier } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const { lastWorkspaceSlug } = useShell();
  const slug = routeSlug ?? lastWorkspaceSlug ?? undefined;
  const { data: projects, isLoading } = useWorkspaceProjects(slug);
  const [filter, setFilter] = useState("");
  const [collapsed, setCollapsed] = useState<boolean>(() =>
    readProjectsCollapsed(),
  );

  const onOpenChange = (open: boolean) => {
    setCollapsed(!open);
    try {
      localStorage.setItem(PROJECTS_COLLAPSED_KEY, open ? "false" : "true");
    } catch {
      // ignore
    }
  };

  const filtered = useMemo(() => {
    const q = filter.trim().toLowerCase();
    if (!q) return projects ?? [];
    return (projects ?? []).filter(
      (p) =>
        p.name.toLowerCase().includes(q) ||
        p.identifier.toLowerCase().includes(q),
    );
  }, [projects, filter]);

  if (!slug) {
    return (
      <SidebarGroup>
        <SidebarGroupLabel>Projects</SidebarGroupLabel>
        <SidebarGroupContent>
          <SidebarMenu>
            <SidebarMenuItem>
              <SidebarMenuButton
                render={<Link to="/w" />}
                tooltip="Select a workspace"
              >
                <Building2 />
                <span>Select a workspace</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
          </SidebarMenu>
        </SidebarGroupContent>
      </SidebarGroup>
    );
  }

  return (
    <SidebarGroup>
      <Collapsible open={!collapsed} onOpenChange={onOpenChange}>
        <div className="flex items-center justify-between">
          <SidebarGroupLabel>Projects</SidebarGroupLabel>
          <CollapsibleTrigger className="flex aspect-square w-5 items-center justify-center rounded-md text-sidebar-foreground/70 transition-transform hover:bg-sidebar-accent hover:text-sidebar-accent-foreground group-data-[collapsible=icon]:hidden [&>svg]:size-4">
            <ChevronDown
              className={`transition-transform ${collapsed ? "-rotate-90" : ""}`}
            />
            <span className="sr-only">Toggle projects section</span>
          </CollapsibleTrigger>
        </div>
        <CollapsibleContent>
          <SidebarGroupContent>
            {isLoading ? (
              <SidebarMenu>
                {[0, 1, 2].map((i) => (
                  <SidebarMenuItem key={i}>
                    <SidebarMenuSkeleton showIcon />
                  </SidebarMenuItem>
                ))}
              </SidebarMenu>
            ) : (
              <>
                {(projects?.length ?? 0) > 8 && (
                  <div className="relative px-0 pb-1 group-data-[collapsible=icon]:hidden">
                    <Search className="pointer-events-none absolute top-2 left-2 size-4 text-muted-foreground" />
                    <SidebarInput
                      value={filter}
                      onChange={(e) => setFilter(e.target.value)}
                      placeholder="Search projects"
                      aria-label="Search projects"
                      className="pl-8"
                    />
                  </div>
                )}
                <SidebarMenu>
                  {filtered.map((p) => {
                    const href = `/w/${encodeURIComponent(slug)}/p/${encodeURIComponent(p.identifier)}`;
                    return (
                      <SidebarMenuItem key={p.id}>
                        <SidebarMenuButton
                          render={<Link to={href} />}
                          isActive={
                            routeIdentifier?.toUpperCase() ===
                            p.identifier.toUpperCase()
                          }
                          tooltip={p.name}
                        >
                          <FolderKanban />
                          <span>{p.name}</span>
                        </SidebarMenuButton>
                      </SidebarMenuItem>
                    );
                  })}
                  {filtered.length === 0 && (
                    <div className="px-2 py-1 text-xs text-muted-foreground">
                      No projects match.
                    </div>
                  )}
                </SidebarMenu>
              </>
            )}
          </SidebarGroupContent>
        </CollapsibleContent>
      </Collapsible>
    </SidebarGroup>
  );
}

function ShellFooter() {
  const { pathname } = useLocation();
  return (
    <SidebarFooter>
      <SidebarMenu>
        <SidebarMenuItem>
          <SidebarMenuButton
            render={<Link to="/profile" />}
            isActive={pathname === "/profile"}
            tooltip="Profile"
          >
            <UserIcon />
            <span>Profile</span>
          </SidebarMenuButton>
        </SidebarMenuItem>
      </SidebarMenu>
      {/* TODO(T2): real footer — avatar dropdown (Profile, API tokens, Admin,
          Log out) + ThemeToggle. */}
      <div data-slot="shell-footer" />
    </SidebarFooter>
  );
}

export default function AppSidebar() {
  const { slug: routeSlug } = useParams<{ slug: string }>();
  const { setLastWorkspaceSlug } = useShell();

  // Track the last-visited workspace for deep-linking (My Work, Projects).
  useEffect(() => {
    if (routeSlug) setLastWorkspaceSlug(routeSlug);
  }, [routeSlug, setLastWorkspaceSlug]);

  return (
    <Sidebar collapsible="icon">
      <WorkspaceHeader />
      <SidebarContent>
        <TriageGroup />
        <FavoritesGroup />
        <SidebarSeparator />
        <ProjectsGroup />
      </SidebarContent>
      <ShellFooter />
      <SidebarRail />
    </Sidebar>
  );
}
