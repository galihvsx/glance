// PageHeader (C19T4): the single shared 49px page header for all project
// pages, per .superpowers/sdd/2026-10-11-glance-cycle19/prototype-spec.md
// ("Page header").
//
// Replaces the per-page duplicated h1 + 17-tab ProjectNav rows the audit
// found on every project page (audit §1.3).
//
// Layout (exact prototype values):
// - 49px tall, bg #0c0c0e, bottom border, padding 0 12px 0 16px, gap 10px.
// - LEFT breadcrumb: gap 7px, 12px secondary — workspace name, chevron "›"
//   (#52525b), view name (strong 600, ink), muted item count.
// - RIGHT (margin-left auto, gap 5px): segmented 5-view switcher (30px,
//   padding 2px, border, radius 6px, bg #101012; buttons 24px, padding 0 8px,
//   radius 4px, 10px muted; active bg-raised ink) + Filter / Display /
//   Analytics buttons (30px, padding 0 9px, border, radius 6px, bg-surface,
//   10px secondary, icon 13px + label gap 6px; hover bg-raised ink) +
//   blue "Add work item" (bg #4f8ef7, white, 600; hover #649cf5) + "…"
//   icon-button 28x28.
//
// Filter/Display/SavedViewsMenu are passed in as nodes by the page (they own
// their popover state); the page styles their triggers with
// `pageHeaderControlClass` via each panel's `triggerClassName` prop so the
// header buttons match the spec.

import type { ReactNode } from "react";
import { Link, useLocation, useNavigate, useParams } from "react-router-dom";
import {
  BarChart3,
  Link2,
  MoreHorizontal,
  Plus,
} from "lucide-react";
import { useWorkspaces } from "../../lib/useWorkspaces";
import { cn } from "../../lib/utils";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "../ui/dropdown-menu";

/** Shared trigger styling for the Filter / Display / Analytics header buttons. */
export const pageHeaderControlClass =
  "inline-flex h-[30px] items-center gap-1.5 rounded-md border-border bg-surface px-[9px] text-[10px] font-medium text-ink-secondary hover:bg-raised hover:text-ink";

export interface ViewSegment {
  id: string;
  label: string;
  /** Route suffix under /w/:slug/p/:identifier; "" = the list view. */
  path: string;
}

/**
 * The five view segments from the prototype. Tab labels follow the spec:
 * "Table" is the spreadsheet route, "Timeline" is the gantt route (audit
 * §1.7 decision — Roadmap stays a separate destination reachable from the
 * project nav, not a segment).
 */
export const VIEW_SEGMENTS: ViewSegment[] = [
  { id: "list", label: "List", path: "" },
  { id: "board", label: "Board", path: "board" },
  { id: "calendar", label: "Calendar", path: "calendar" },
  { id: "table", label: "Table", path: "spreadsheet" },
  { id: "timeline", label: "Timeline", path: "gantt" },
];

export interface PageHeaderMenuItem {
  label: string;
  onClick: () => void;
}

export interface PageHeaderProps {
  /** Current view name, e.g. "Work items", "Board", "Cycles". */
  viewName: string;
  /** Muted item count rendered after the view name. */
  count?: number | string;
  /** Render the List/Board/Calendar/Table/Timeline view switcher. */
  showViews?: boolean;
  /** Existing <FilterPanel/> node (page styles its trigger via triggerClassName). */
  filterPanel?: ReactNode;
  /** Existing <DisplayPanel/> / <SpreadsheetDisplayPanel/> node. */
  displayPanel?: ReactNode;
  /** Extra controls (e.g. <SavedViewsMenu/>) rendered after Display. */
  extraControls?: ReactNode;
  /** Render the Analytics quick-nav button (default true; auto-hidden on the analytics route). */
  showAnalytics?: boolean;
  /**
   * The blue primary button. Defaults to "Add work item" navigating to the
   * list view with ?create=1 (the list page opens its create dialog from
   * that param). Pass `null` to hide it.
   */
  addButton?: { label?: string; onClick: () => void } | null;
  /** Extra items appended to the "…" menu (which always has "Copy link"). */
  menuItems?: PageHeaderMenuItem[];
}

function segmentForPath(suffix: string): string {
  switch (suffix) {
    case "/board":
      return "board";
    case "/calendar":
      return "calendar";
    case "/spreadsheet":
      return "table";
    case "/gantt":
      return "timeline";
    default:
      return "list";
  }
}

export default function PageHeader({
  viewName,
  count,
  showViews = false,
  filterPanel,
  displayPanel,
  extraControls,
  showAnalytics = true,
  addButton,
  menuItems,
}: PageHeaderProps) {
  const { slug = "", identifier = "" } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const location = useLocation();
  const navigate = useNavigate();
  const { data: workspaces } = useWorkspaces();

  const base = `/w/${slug}/p/${identifier}`;
  const suffix = location.pathname.startsWith(base)
    ? location.pathname.slice(base.length).replace(/\/$/, "")
    : "";
  const activeSegment = segmentForPath(suffix);
  const isAnalytics = suffix === "/analytics";

  const workspaceName =
    workspaces?.find((w) => w.slug === slug)?.name ?? slug;

  const handleAdd = () => {
    if (addButton) {
      addButton.onClick();
    } else {
      // Default: open the list view's create dialog via the ?create=1
      // deep-link the list page honors.
      navigate(`${base}?create=1`);
    }
  };

  const copyLink = () => {
    void navigator.clipboard?.writeText(window.location.href).catch(() => {});
  };

  return (
    <header className="sticky top-0 z-10 flex h-[49px] shrink-0 items-center gap-2.5 border-b border-border bg-[#0c0c0e] px-3 pl-4">
      {/* LEFT — breadcrumb */}
      <nav
        aria-label="Breadcrumb"
        className="flex min-w-0 items-center gap-[7px] text-xs text-ink-secondary"
      >
        <Link
          to={`/w/${slug}`}
          className="truncate hover:text-ink hover:underline"
        >
          {workspaceName}
        </Link>
        <span aria-hidden="true" className="shrink-0 text-[#52525b]">
          ›
        </span>
        <span className="shrink-0 font-semibold text-ink">{viewName}</span>
        {count !== undefined && (
          <span className="shrink-0 text-ink-muted">{count}</span>
        )}
      </nav>

      {/* RIGHT — controls */}
      <div className="ml-auto flex shrink-0 items-center gap-[5px]">
        {showViews && (
          <div
            role="tablist"
            aria-label="Views"
            className="hidden h-[30px] items-center rounded-md border border-border bg-[#101012] p-[2px] md:flex"
          >
            {VIEW_SEGMENTS.map((seg) => {
              const active = seg.id === activeSegment;
              return (
                <button
                  key={seg.id}
                  type="button"
                  role="tab"
                  aria-selected={active}
                  onClick={() =>
                    navigate(seg.path ? `${base}/${seg.path}` : base)
                  }
                  className={cn(
                    "h-[24px] rounded px-2 text-[10px] font-medium transition-colors",
                    active
                      ? "bg-raised text-ink"
                      : "text-ink-muted hover:text-ink-secondary",
                  )}
                >
                  {seg.label}
                </button>
              );
            })}
          </div>
        )}

        {filterPanel}
        {displayPanel}
        {extraControls}

        {showAnalytics && !isAnalytics && (
          <button
            type="button"
            onClick={() => navigate(`${base}/analytics`)}
            className={pageHeaderControlClass}
          >
            <BarChart3 className="h-[13px] w-[13px]" aria-hidden="true" />
            Analytics
          </button>
        )}

        {addButton !== null && (
          <button
            type="button"
            onClick={handleAdd}
            className="inline-flex h-[30px] items-center gap-1.5 rounded-md border border-accent-blue bg-accent-blue px-3 text-[11px] font-semibold text-white transition-colors hover:border-accent-blue-hover hover:bg-accent-blue-hover"
          >
            <Plus className="h-[13px] w-[13px]" aria-hidden="true" />
            {addButton?.label ?? "Add work item"}
          </button>
        )}

        <DropdownMenu>
          <DropdownMenuTrigger
            aria-label="More actions"
            className="inline-flex h-7 w-7 items-center justify-center rounded-md text-ink-muted transition-colors hover:bg-wash-hover hover:text-ink"
          >
            <MoreHorizontal className="h-4 w-4" aria-hidden="true" />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-48">
            {menuItems?.map((item) => (
              <DropdownMenuItem key={item.label} onClick={item.onClick}>
                {item.label}
              </DropdownMenuItem>
            ))}
            <DropdownMenuItem onClick={copyLink}>
              <Link2 className="h-3.5 w-3.5" aria-hidden="true" />
              Copy link to this view
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </header>
  );
}
