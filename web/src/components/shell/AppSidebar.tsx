// AppSidebar (C19T1): the app shell's sidebar, rebuilt for the Plane
// layout (prototype Screen 1, literal).
//
// 280px, bg #101012, 1px right border. Sections (top → bottom):
// header ("Work" + options/collapse) → "New work item" button + C kbd →
// scroll area: nav links (Home, Drafts, Your work, Stickies) →
// "Workspace" section (Inbox, Views) → "Projects" section (per-project
// rows with 8 indented sub-links: Overview, Work items, Cycles, Modules,
// Releases, Views, Pages, Intake).
//
// Real data: workspaces, projects, unread-notifications count (Inbox),
// drafts count (current project). Fallbacks: Wiki/AI/Stickies have no
// dedicated surface in glance yet (see Rail.tsx); the section "+" buttons
// use the closest real action.

import { useEffect, useMemo, useState } from "react";
import { Link, useLocation, useNavigate, useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import {
  Briefcase,
  ChevronDown,
  FileText,
  Gauge,
  Goal,
  Home,
  Inbox,
  Layers,
  MoreHorizontal,
  PanelLeftClose,
  Plus,
  ShieldAlert,
  StickyNote,
  Target,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { api } from "../../lib/api";
import { draftsKey, fetchDrafts } from "../../lib/drafts";
import { useUnreadCount } from "../../lib/notifications";
import { emitShortcutAction } from "../../lib/shortcuts";
import type { Initiative, Objective, Project } from "../../lib/types";
import { useShell } from "./shell-context";
import { toast } from "../ui/toast";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "../ui/dropdown-menu";
import { cn } from "cn";

const PROJECTS_COLLAPSED_KEY = "glance:projects-collapsed";
const WORKSPACE_SECTION_COLLAPSED_KEY = "glance:workspace-section-collapsed";

function readCollapsed(key: string): boolean {
  try {
    return localStorage.getItem(key) === "true";
  } catch {
    return false;
  }
}

function writeCollapsed(key: string, collapsed: boolean): void {
  try {
    localStorage.setItem(key, collapsed ? "true" : "false");
  } catch {
    // ignore
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

const stickiesSoon = () =>
  toast.add({
    title: "Stickies are coming soon",
    description: "Quick notes are on the roadmap for a future cycle.",
  });

/* ------------------------------------------------------------------ */
/* Header: 44px, "Work" 14px/620 + options/collapse 28px icon-buttons.  */
/* ------------------------------------------------------------------ */

function SidebarOptionsButton() {
  const { setSidebarCollapsed } = useShell();
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        aria-label="Sidebar options"
        className="flex size-7 items-center justify-center rounded-md text-ink-muted transition-colors hover:bg-wash-hover hover:text-ink"
      >
        <MoreHorizontal className="size-[15px]" strokeWidth={1.6} />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-48">
        <DropdownMenuItem
          onClick={() => emitShortcutAction("open-cheatsheet")}
        >
          Keyboard shortcuts
        </DropdownMenuItem>
        <DropdownMenuItem onClick={() => setSidebarCollapsed(true)}>
          Collapse sidebar
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function SidebarHeader() {
  const { setSidebarCollapsed } = useShell();
  return (
    <div className="flex h-11 shrink-0 items-center border-b border-border-hairline pr-2 pl-[14px]">
      <span className="text-[14px] font-semibold text-ink">Work</span>
      <div className="ml-auto flex items-center gap-1">
        <SidebarOptionsButton />
        <button
          type="button"
          aria-label="Collapse sidebar"
          title="Collapse sidebar"
          onClick={() => setSidebarCollapsed(true)}
          className="flex size-7 items-center justify-center rounded-md text-ink-muted transition-colors hover:bg-wash-hover hover:text-ink"
        >
          <PanelLeftClose className="size-[15px]" strokeWidth={1.6} />
        </button>
      </div>
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* "New work item": 32px button + C kbd chip.                           */
/* ------------------------------------------------------------------ */

function NewWorkItemButton() {
  return (
    <div className="mx-[10px] mt-[10px] mb-[6px]">
      <button
        type="button"
        onClick={() => emitShortcutAction("new-issue")}
        className="flex h-8 w-full items-center gap-2 rounded-md border border-border bg-[#121215] px-[9px] text-ink-muted transition-colors hover:bg-wash-hover hover:text-ink-secondary"
      >
        <Plus className="size-[14px] shrink-0" aria-hidden="true" />
        <span className="text-[12px]">New work item</span>
        <kbd className="ml-auto rounded-[3px] border border-border px-1 py-px font-mono text-[9px] leading-[14px] text-ink-muted">
          C
        </kbd>
      </button>
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* Nav links: 30px tall, 15px icons, count badge right.                 */
/* ------------------------------------------------------------------ */

function NavRow({
  icon: Icon,
  label,
  count,
  active,
  to,
  onClick,
}: {
  icon: LucideIcon;
  label: string;
  count?: number;
  active?: boolean;
  to?: string;
  onClick?: () => void;
}) {
  const cls = cn(
    "flex h-[30px] w-full items-center gap-[9px] rounded-[5px] px-2 text-[12px] transition-colors",
    active
      ? "bg-wash-active font-medium text-ink"
      : "text-ink-secondary hover:bg-wash-hover hover:text-ink",
  );
  const inner = (
    <>
      <Icon className="size-[15px] shrink-0" strokeWidth={1.55} aria-hidden="true" />
      <span className="truncate">{label}</span>
      {count != null && count > 0 && (
        <span className="ml-auto font-mono text-[10px] text-ink-muted">
          {count > 99 ? "99+" : count}
        </span>
      )}
    </>
  );
  if (to) {
    return (
      <Link to={to} onClick={onClick} aria-current={active ? "page" : undefined} className={cls}>
        {inner}
      </Link>
    );
  }
  return (
    <button type="button" onClick={onClick} className={cls}>
      {inner}
    </button>
  );
}

function MainNav() {
  const { pathname } = useLocation();
  const { slug: routeSlug, identifier: routeIdentifier } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const { lastWorkspaceSlug } = useShell();
  const slug = routeSlug ?? lastWorkspaceSlug ?? null;

  const draftsQuery = useQuery({
    queryKey: slug && routeIdentifier ? draftsKey(slug, routeIdentifier) : ["drafts", "none"],
    queryFn: () => fetchDrafts(slug as string, routeIdentifier as string),
    enabled: !!slug && !!routeIdentifier,
  });
  const draftsCount = draftsQuery.data?.length;

  const myWorkHref = slug ? `/w/${encodeURIComponent(slug)}/my-work` : "/";
  const draftsHref =
    slug && routeIdentifier
      ? `/w/${encodeURIComponent(slug)}/p/${encodeURIComponent(routeIdentifier)}/drafts`
      : myWorkHref;

  return (
    <nav aria-label="Workspace" className="flex flex-col gap-px">
      <NavRow icon={Home} label="Home" to="/" active={pathname === "/"} />
      <NavRow
        icon={FileText}
        label="Drafts"
        to={draftsHref}
        count={draftsCount}
        active={routeIdentifier != null && pathname.endsWith("/drafts")}
      />
      <NavRow
        icon={Briefcase}
        label="Your work"
        to={myWorkHref}
        active={pathname.endsWith("/my-work")}
      />
      <NavRow icon={StickyNote} label="Stickies" onClick={stickiesSoon} />
    </nav>
  );
}

/* ------------------------------------------------------------------ */
/* Collapsible sections: 28px title row, 11px muted, chevron + "+".     */
/* ------------------------------------------------------------------ */

function SectionTitle({
  label,
  collapsed,
  onToggle,
  addLabel,
  onAdd,
}: {
  label: string;
  collapsed: boolean;
  onToggle: () => void;
  addLabel: string;
  onAdd: () => void;
}) {
  return (
    <div className="mt-[9px] flex h-7 items-center px-[7px]">
      <button
        type="button"
        onClick={onToggle}
        aria-expanded={!collapsed}
        aria-label={`${collapsed ? "Expand" : "Collapse"} ${label} section`}
        className="flex items-center gap-1 text-[11px] text-ink-muted transition-colors hover:text-ink-secondary"
      >
        <ChevronDown
          className={cn("size-3 transition-transform", collapsed && "-rotate-90")}
          aria-hidden="true"
        />
        {label}
      </button>
      <button
        type="button"
        onClick={onAdd}
        aria-label={addLabel}
        title={addLabel}
        className="ml-auto flex size-6 items-center justify-center rounded-[4px] text-ink-muted transition-colors hover:bg-wash-hover hover:text-ink"
      >
        <Plus className="size-[15px]" aria-hidden="true" />
      </button>
    </div>
  );
}

function WorkspaceSection() {
  const { pathname } = useLocation();
  const { slug: routeSlug, identifier: routeIdentifier } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const { lastWorkspaceSlug, setNotificationsOpen } = useShell();
  const { data: unread } = useUnreadCount();
  const [collapsed, setCollapsed] = useState<boolean>(() =>
    readCollapsed(WORKSPACE_SECTION_COLLAPSED_KEY),
  );

  const slug = routeSlug ?? lastWorkspaceSlug ?? null;
  const toggle = () => {
    setCollapsed((c) => {
      writeCollapsed(WORKSPACE_SECTION_COLLAPSED_KEY, !c);
      return !c;
    });
  };

  // Saved views live on the project work-items page; without a project in
  // the route, fall back to the workspace home.
  const viewsHref =
    slug && routeIdentifier
      ? `/w/${encodeURIComponent(slug)}/p/${encodeURIComponent(routeIdentifier)}`
      : slug
        ? `/w/${encodeURIComponent(slug)}`
        : "/";
  // C27T1: initiatives are workspace-scoped, so their entry lives here in
  // the workspace section (badge = non-archived count, same NavRow
  // icon/badge style as Inbox).
  const initiativesHref = slug
    ? `/w/${encodeURIComponent(slug)}/initiatives`
    : "/";
  const initiativesQuery = useQuery({
    queryKey: ["initiatives", slug ?? ""],
    queryFn: () =>
      api
        .get<{ initiatives: Initiative[] }>(
          `/api/v1/workspaces/${encodeURIComponent(slug ?? "")}/initiatives`,
        )
        .then((d) => d.initiatives),
    enabled: !!slug,
  });
  const activeInitiatives = (initiativesQuery.data ?? []).filter(
    (i) => i.status !== "archived",
  ).length;
  // C35T1: goals are workspace-scoped, so their entry lives here in
  // the workspace section under Initiatives (badge = non-archived
  // count, same NavRow icon/badge style as Initiatives).
  const goalsHref = slug ? `/w/${encodeURIComponent(slug)}/goals` : "/";
  const goalsQuery = useQuery({
    queryKey: ["objectives", slug ?? ""],
    queryFn: () =>
      api
        .get<{ objectives: Objective[] }>(
          `/api/v1/workspaces/${encodeURIComponent(slug ?? "")}/objectives`,
        )
        .then((d) => d.objectives),
    enabled: !!slug,
  });
  const activeGoals = (goalsQuery.data ?? []).filter(
    (o) => o.status !== "archived",
  ).length;
  // C29T1: workload is workspace-scoped, so its entry lives here in the
  // workspace section next to Initiatives.
  const workloadHref = slug ? `/w/${encodeURIComponent(slug)}/workload` : "/";
  // C30T3: risk radar is workspace-scoped, so its entry lives here next
  // to Workload.
  const riskHref = slug ? `/w/${encodeURIComponent(slug)}/risk` : "/";

  return (
    <section aria-label="Workspace">
      <SectionTitle
        label="Workspace"
        collapsed={collapsed}
        onToggle={toggle}
        addLabel="New work item"
        onAdd={() => emitShortcutAction("new-issue")}
      />
      {!collapsed && (
        <div className="flex flex-col gap-px">
          <NavRow
            icon={Inbox}
            label="Inbox"
            count={unread ?? undefined}
            onClick={() => setNotificationsOpen(true)}
          />
          <NavRow icon={Layers} label="Views" to={viewsHref} />
          <NavRow
            icon={Target}
            label="Initiatives"
            to={initiativesHref}
            count={activeInitiatives}
            active={
              pathname === initiativesHref ||
              pathname.startsWith(`${initiativesHref}/`)
            }
          />
          <NavRow
            icon={Goal}
            label="Goals"
            to={goalsHref}
            count={activeGoals}
            active={
              pathname === goalsHref || pathname.startsWith(`${goalsHref}/`)
            }
          />
          <NavRow
            icon={Gauge}
            label="Workload"
            to={workloadHref}
            active={
              pathname === workloadHref ||
              pathname.startsWith(`${workloadHref}/`)
            }
          />
          <NavRow
            icon={ShieldAlert}
            label="Risk"
            to={riskHref}
            active={
              pathname === riskHref || pathname.startsWith(`${riskHref}/`)
            }
          />
        </div>
      )}
    </section>
  );
}

/* ------------------------------------------------------------------ */
/* Projects section: project row (18px mark + name + chevron) +         */
/* 8 indented sub-links (ml 17px, pl 11px, 1px left border).            */
/* ------------------------------------------------------------------ */

// Deterministic mark colors from the project identifier (prototype shows
// bg #2d538c / text #c7dcff for "G").
const MARK_COLORS: [string, string][] = [
  ["#2d538c", "#c7dcff"],
  ["#5b3d8c", "#dccfff"],
  ["#2d6b52", "#c4f0dc"],
  ["#8c4a2d", "#ffd9c4"],
  ["#7a2d3f", "#ffc9d4"],
  ["#3f5a7a", "#cfe3ff"],
  ["#6b5a2d", "#f0e2b8"],
];

function markColors(identifier: string): [string, string] {
  let h = 0;
  for (let i = 0; i < identifier.length; i++) h = (h * 31 + identifier.charCodeAt(i)) >>> 0;
  return MARK_COLORS[h % MARK_COLORS.length];
}

function ProjectMark({ identifier }: { identifier: string }) {
  const [bg, fg] = markColors(identifier);
  return (
    <span
      aria-hidden="true"
      className="flex size-[18px] shrink-0 items-center justify-center rounded-[4px] text-[9px] font-extrabold"
      style={{ backgroundColor: bg, color: fg }}
    >
      {identifier.trim().charAt(0).toUpperCase() || "P"}
    </span>
  );
}

interface SubLink {
  label: string;
  suffix: string; // path remainder after the project base
  activeOn: (rest: string) => boolean;
  /** Optional accent dot before the label (C71: epics get the purple dot). */
  dot?: string;
}

const VIEW_SUFFIXES = ["", "/board", "/spreadsheet", "/calendar", "/gantt", "/roadmap"];

const SUB_LINKS: SubLink[] = [
  {
    label: "Overview",
    suffix: "/overview",
    activeOn: (rest) => rest === "/overview",
  },
  {
    label: "Work items",
    suffix: "",
    // All work-item views (list/board/spreadsheet/…) count as Work items.
    activeOn: (rest) => VIEW_SUFFIXES.includes(rest),
  },
  { label: "Cycles", suffix: "/cycles", activeOn: (rest) => rest === "/cycles" },
  { label: "Modules", suffix: "/modules", activeOn: (rest) => rest === "/modules" },
  // C71T1: Epics sit between Modules and Initiatives per the spec
  // (project rail); the purple dot marks the new entry.
  {
    label: "Epics",
    suffix: "/epics",
    activeOn: (rest) => rest === "/epics" || rest.startsWith("/epics/"),
    dot: "#9c83e6",
  },
  {
    label: "Releases",
    suffix: "/releases",
    activeOn: (rest) => rest === "/releases",
  },
  {
    label: "Schedules",
    suffix: "/schedules",
    activeOn: (rest) => rest === "/schedules",
  },
  {
    label: "Views",
    suffix: "",
    // Saved views live on the work-items page; never takes the active
    // state (Work items does).
    activeOn: () => false,
  },
  { label: "Pages", suffix: "/pages", activeOn: (rest) => rest === "/pages" },
  { label: "Intake", suffix: "/intake", activeOn: (rest) => rest === "/intake" },
];

function ProjectRow({
  project,
  slug,
  expanded,
  onToggle,
}: {
  project: Project;
  slug: string;
  expanded: boolean;
  onToggle: () => void;
}) {
  const { pathname } = useLocation();
  const base = `/w/${encodeURIComponent(slug)}/p/${encodeURIComponent(project.identifier)}`;
  const rest = pathname.startsWith(base) ? pathname.slice(base.length) : null;

  return (
    <div>
      <button
        type="button"
        onClick={onToggle}
        aria-expanded={expanded}
        aria-label={`${expanded ? "Collapse" : "Expand"} ${project.name}`}
        className="flex h-[30px] w-full items-center gap-2 rounded-[5px] px-2 transition-colors hover:bg-wash-hover"
      >
        <ProjectMark identifier={project.identifier} />
        <span className="truncate text-[12px] font-medium text-ink">
          {project.name}
        </span>
        <ChevronDown
          className={cn(
            "ml-auto size-3 shrink-0 text-ink-muted transition-transform",
            !expanded && "-rotate-90",
          )}
          aria-hidden="true"
        />
      </button>
      {expanded && (
        <div className="ml-[17px] border-l border-border pl-[11px]">
          {SUB_LINKS.map((l) => {
            const active = rest !== null && l.activeOn(rest);
            return (
              <Link
                key={l.label}
                to={`${base}${l.suffix}`}
                aria-current={active ? "page" : undefined}
                className={cn(
                  "flex h-7 w-full items-center rounded-[5px] px-2 text-[12px] transition-colors",
                  active
                    ? "bg-wash-active font-medium text-ink"
                    : "text-ink-secondary hover:bg-wash-hover hover:text-ink",
                )}
              >
                {l.dot && (
                  <span
                    aria-hidden="true"
                    className="mr-2 size-1.5 shrink-0 rounded-full"
                    style={{ backgroundColor: l.dot }}
                  />
                )}
                {l.label}
              </Link>
            );
          })}
        </div>
      )}
    </div>
  );
}

function ProjectsSection() {
  const { slug: routeSlug, identifier: routeIdentifier } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const { lastWorkspaceSlug } = useShell();
  const navigate = useNavigate();
  const slug = routeSlug ?? lastWorkspaceSlug ?? null;
  const { data: projects, isLoading } = useWorkspaceProjects(slug ?? undefined);
  const [collapsed, setCollapsed] = useState<boolean>(() =>
    readCollapsed(PROJECTS_COLLAPSED_KEY),
  );
  const [expandedIds, setExpandedIds] = useState<string[]>(() =>
    routeIdentifier ? [routeIdentifier.toUpperCase()] : [],
  );

  const toggle = () => {
    setCollapsed((c) => {
      writeCollapsed(PROJECTS_COLLAPSED_KEY, !c);
      return !c;
    });
  };

  // Keep the project in the route expanded as the user navigates.
  useEffect(() => {
    if (routeIdentifier) {
      const id = routeIdentifier.toUpperCase();
      setExpandedIds((ids) => (ids.includes(id) ? ids : [...ids, id]));
    }
  }, [routeIdentifier]);

  const toggleProject = (identifier: string) => {
    const id = identifier.toUpperCase();
    setExpandedIds((ids) =>
      ids.includes(id) ? ids.filter((x) => x !== id) : [...ids, id],
    );
  };

  const list = useMemo(() => projects ?? [], [projects]);

  return (
    <section aria-label="Projects">
      <SectionTitle
        label="Projects"
        collapsed={collapsed}
        onToggle={toggle}
        addLabel="New project"
        onAdd={() => slug && navigate(`/w/${encodeURIComponent(slug)}`)}
      />
      {!collapsed && (
        <div className="flex flex-col gap-px">
          {isLoading &&
            [0, 1].map((i) => (
              <div
                key={i}
                className="h-[30px] animate-pulse rounded-[5px] bg-wash-hover"
                aria-label="Loading projects"
              />
            ))}
          {!isLoading &&
            slug &&
            list.map((p) => (
              <ProjectRow
                key={p.id}
                project={p}
                slug={slug}
                expanded={expandedIds.includes(p.identifier.toUpperCase())}
                onToggle={() => toggleProject(p.identifier)}
              />
            ))}
          {!isLoading && !slug && (
            <Link
              to="/w"
              className="flex h-[30px] items-center rounded-[5px] px-2 text-[12px] text-ink-secondary transition-colors hover:bg-wash-hover hover:text-ink"
            >
              Select a workspace
            </Link>
          )}
        </div>
      )}
    </section>
  );
}

/* ------------------------------------------------------------------ */

export default function AppSidebar() {
  const { slug: routeSlug } = useParams<{ slug: string }>();
  const { setLastWorkspaceSlug } = useShell();

  // Track the last-visited workspace for deep-linking (My Work, Projects).
  useEffect(() => {
    if (routeSlug) setLastWorkspaceSlug(routeSlug);
  }, [routeSlug, setLastWorkspaceSlug]);

  return (
    <aside
      data-slot="sidebar"
      aria-label="Workspace sidebar"
      className="flex h-full w-[280px] flex-col overflow-hidden border-r border-border bg-[#101012]"
    >
      <SidebarHeader />
      <NewWorkItemButton />
      <div className="min-h-0 flex-1 overflow-y-auto px-[7px] pt-[3px] pb-3">
        <MainNav />
        <WorkspaceSection />
        <ProjectsSection />
      </div>
    </aside>
  );
}
