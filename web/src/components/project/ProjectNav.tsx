import { Link, useLocation, useParams } from "react-router-dom";
import { cn } from "../../lib/utils";

/** View switcher shown on the project pages (list / board / cycles / intake). */
export default function ProjectNav() {
  const { slug = "", identifier = "" } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const { pathname } = useLocation();
  const base = `/w/${slug}/p/${identifier}`;
  const tabs = [
    { label: "List", to: base },
    { label: "Board", to: `${base}/board` },
    { label: "Cycles", to: `${base}/cycles` },
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
    </nav>
  );
}
