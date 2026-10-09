import type { ReactNode } from "react";
import { Link, useLocation, useParams } from "react-router-dom";
import { cn } from "../../lib/utils";

/** View switcher shown on the project pages (list / board / spreadsheet /
 *  calendar / cycles / modules / intake).
 *  `trailing` renders at the right end of the tab bar — used for the saved
 *  views menu (C2T6) on the filterable pages. */
export default function ProjectNav({ trailing }: { trailing?: ReactNode }) {
  const { slug = "", identifier = "" } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const { pathname } = useLocation();
  const base = `/w/${slug}/p/${identifier}`;
  const tabs = [
    { label: "List", to: base },
    { label: "Board", to: `${base}/board` },
    { label: "Spreadsheet", to: `${base}/spreadsheet` },
    { label: "Calendar", to: `${base}/calendar` },
    { label: "Cycles", to: `${base}/cycles` },
    { label: "Modules", to: `${base}/modules` },
    { label: "Intake", to: `${base}/intake` },
  ];
  return (
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
}
