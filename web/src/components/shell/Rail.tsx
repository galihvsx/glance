// Rail (C19T1): the 52px icon rail — the leftmost shell column, always
// visible even when the sidebar is collapsed.
//
// Layout (prototype Screen 1, literal): 32px workspace avatar button →
// 4 nav items 47px tall (Work active, Wiki, AI, Settings; 17px icons
// stroke 1.55 + 8px labels; idle muted, hover wash, active wash) →
// Stickies pinned at bottom (margin-top auto).
//
// Destinations: Work = current workspace (fallback: workspace list).
// Wiki = the current project's Pages (glance has no standalone wiki;
// falls back to the workspace home). AI = no dedicated surface in
// glance yet, so it opens the command palette — the global command
// surface where the AI-assisted create flow lives. Stickies is a
// deferred feature (audit §1.18, needs galih's call): the item renders
// per the prototype and shows a "coming soon" toast.

import { Link, useLocation, useParams } from "react-router-dom";
import {
  BookOpen,
  LayoutGrid,
  Settings,
  Sparkles,
  StickyNote,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { emitShortcutAction } from "../../lib/shortcuts";
import { useWorkspaces } from "../../lib/useWorkspaces";
import { useShell } from "./shell-context";
import { toast } from "../ui/toast";
import { cn } from "cn";

function workspaceInitial(name: string | null | undefined): string {
  const c = (name ?? "").trim().charAt(0);
  return c ? c.toUpperCase() : "G";
}

function RailItem({
  icon: Icon,
  label,
  active,
  title,
  onClick,
  to,
}: {
  icon: LucideIcon;
  label: string;
  active?: boolean;
  title: string;
  onClick?: () => void;
  to?: string;
}) {
  const cls = cn(
    "flex h-[47px] w-full flex-col items-center justify-center gap-1 rounded-md text-[8px] leading-none transition-colors",
    active
      ? "bg-wash-active text-ink"
      : "text-ink-muted hover:bg-wash-hover hover:text-ink-secondary",
  );
  const inner = (
    <>
      <Icon className="size-[17px]" strokeWidth={1.55} aria-hidden="true" />
      <span>{label}</span>
    </>
  );
  if (to) {
    return (
      <Link to={to} title={title} aria-label={label} className={cls}>
        {inner}
      </Link>
    );
  }
  return (
    <button type="button" title={title} aria-label={label} onClick={onClick} className={cls}>
      {inner}
    </button>
  );
}

export default function Rail() {
  const { pathname } = useLocation();
  const { slug: routeSlug, identifier: routeIdentifier } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const { lastWorkspaceSlug } = useShell();
  const { data: workspaces } = useWorkspaces();

  const slug = routeSlug ?? lastWorkspaceSlug ?? null;
  const current = workspaces?.find((w) => w.slug === slug) ?? null;

  const workHref = slug ? `/w/${encodeURIComponent(slug)}` : "/w";
  const wikiHref =
    slug && routeIdentifier
      ? `/w/${encodeURIComponent(slug)}/p/${encodeURIComponent(routeIdentifier)}/pages`
      : workHref;
  const settingsHref = slug
    ? `/w/${encodeURIComponent(slug)}/settings`
    : "/settings/tokens";

  const workActive =
    /\/w\/[^/]+(\/|$)/.test(pathname) && !/\/settings(\/|$)/.test(pathname);
  const wikiActive = /\/pages(\/|$)/.test(pathname);
  const settingsActive =
    /\/settings(\/|$)/.test(pathname) ||
    pathname === "/admin" ||
    pathname === "/profile";

  const openStickiesSoon = () =>
    toast.add({
      title: "Stickies are coming soon",
      description: "Quick notes are on the roadmap for a future cycle.",
    });

  return (
    <nav
      aria-label="Primary"
      data-slot="rail"
      className="flex h-full w-[52px] shrink-0 flex-col items-center border-r border-border bg-rail px-[5px] py-[7px]"
    >
      <Link
        to={workHref}
        title={current ? current.name : "Workspaces"}
        aria-label="Workspace home"
        className="mb-[10px] flex size-8 shrink-0 items-center justify-center rounded-lg bg-[#e4e4e7] text-[12px] font-extrabold text-[#18181b] transition-opacity hover:opacity-90"
      >
        {workspaceInitial(current?.name)}
      </Link>
      <div className="flex w-full flex-col gap-[3px]">
        <RailItem
          icon={LayoutGrid}
          label="Work"
          to={workHref}
          active={workActive}
          title="Work"
        />
        <RailItem
          icon={BookOpen}
          label="Wiki"
          to={wikiHref}
          active={wikiActive}
          title="Wiki"
        />
        <RailItem
          icon={Sparkles}
          label="AI"
          title="AI assistant"
          onClick={() => emitShortcutAction("toggle-palette")}
        />
        <RailItem
          icon={Settings}
          label="Settings"
          to={settingsHref}
          active={settingsActive}
          title="Settings"
        />
      </div>
      <div className="mt-auto w-full">
        <RailItem
          icon={StickyNote}
          label="Stickies"
          title="Stickies"
          onClick={openStickiesSoon}
        />
      </div>
    </nav>
  );
}
