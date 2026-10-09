import { useEffect, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { Link, useLocation, useParams } from "react-router-dom";
import { cn } from "../../lib/utils";

/** View switcher shown on the project pages (overview / list / board /
 *  spreadsheet / calendar / gantt / analytics / cycles / modules /
 *  pages / intake / activity / settings).
 *  `trailing` renders at the right end of the tab bar — used for the saved
 *  views menu (C2T6) on the filterable pages.
 *
 *  App-shell integration: when the shell's `#shell-project-tabs` slot is
 *  present (project routes rendered inside AppShell), the tab bar is
 *  portaled into that node; otherwise it renders inline as before. The
 *  inline fallback keeps the component usable standalone and in tests. */
export default function ProjectNav({ trailing }: { trailing?: ReactNode }) {
  const { slug = "", identifier = "" } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const { pathname } = useLocation();
  const base = `/w/${slug}/p/${identifier}`;
  // Shell slot lookup is deferred to an effect so server/test renders and
  // pages mounted outside the shell never touch the DOM.
  const [slot, setSlot] = useState<HTMLElement | null>(null);
  useEffect(() => {
    setSlot(document.getElementById("shell-project-tabs"));
  }, []);
  const tabs = [
    { label: "Overview", to: `${base}/overview` },
    { label: "List", to: base },
    { label: "Board", to: `${base}/board` },
    { label: "Spreadsheet", to: `${base}/spreadsheet` },
    { label: "Calendar", to: `${base}/calendar` },
    { label: "Gantt", to: `${base}/gantt` },
    { label: "Analytics", to: `${base}/analytics` },
    { label: "Cycles", to: `${base}/cycles` },
    { label: "Modules", to: `${base}/modules` },
    { label: "Releases", to: `${base}/releases` },
    { label: "Pages", to: `${base}/pages` },
    { label: "Activity", to: `${base}/activity` },
    { label: "Intake", to: `${base}/intake` },
    { label: "Settings", to: `${base}/settings` },
    { label: "Drafts", to: `${base}/drafts` },
    { label: "Archived", to: `${base}/archived` },
  ];
  const bar = (
    <nav className="flex items-center gap-1 border-b">
      {tabs.map((t) => {
        const active =
          t.to === base ? pathname === base : pathname.startsWith(t.to);
        return (
          <Link
            key={t.to}
            to={t.to}
            className={cn(
              "border-b-2 px-3 py-2 text-sm",
              active
                ? "border-primary font-medium text-foreground"
                : "border-transparent text-muted-foreground hover:text-foreground",
            )}
          >
            {t.label}
          </Link>
        );
      })}
      {trailing && <div className="ml-auto pb-1">{trailing}</div>}
    </nav>
  );
  // Portal into the shell's project-tabs slot when present (project routes
  // inside AppShell); inline fallback keeps tests/standalone usage working.
  return slot ? createPortal(bar, slot) : bar;
}
