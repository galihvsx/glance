// Favorites sidebar section (C7T4, C19T9 polish): the caller's starred
// issues and projects as dense hairline rows — starred issues link to
// their detail page, starred projects to their issue list. SectionBar
// header, token empty state.
import { Link } from "react-router-dom";
import { FolderOpen, Star } from "lucide-react";
import {
  issueFavoriteHref,
  projectFavoriteHref,
  useFavorites,
} from "../../lib/favorites";
import EmptyState from "../personal/EmptyState";
import { SectionBar } from "../personal/SectionBar";
import { Alert, AlertDescription } from "../ui/alert";
import { Skeleton } from "../ui/skeleton";

export default function FavoritesSection() {
  const { data, isPending, isError } = useFavorites();

  const issues = data?.issues ?? [];
  const projects = data?.projects ?? [];
  const total = issues.length + projects.length;

  return (
    <div>
      <SectionBar label="Favorites" count={total || undefined} />

      {isPending ? (
        <div className="space-y-2 pt-2">
          <Skeleton className="h-[47px] w-full" />
          <Skeleton className="h-[47px] w-full" />
        </div>
      ) : isError ? (
        <Alert variant="destructive" className="mt-2">
          <AlertDescription>Failed to load favorites.</AlertDescription>
        </Alert>
      ) : total === 0 ? (
        <div className="pt-4">
          <EmptyState icon={Star} title="No favorites yet">
            <p>Star issues and projects to pin them here for quick access.</p>
          </EmptyState>
        </div>
      ) : (
        <ul className="mt-2 divide-y divide-border-hairline overflow-hidden rounded-md border border-border">
          {issues.map((f) => (
            <li key={`issue-${f.id}`}>
              <Link
                to={issueFavoriteHref(f)}
                className="flex min-h-[47px] flex-col justify-center gap-0.5 px-3 py-2 transition-colors hover:bg-wash-hover"
              >
                <span className="flex items-center gap-2">
                  <span className="font-mono text-[10px] text-ink-muted">
                    {f.display_id}
                  </span>
                  <span className="text-[10px] text-ink-muted">
                    {f.project_identifier}
                  </span>
                </span>
                <span className="truncate text-[12px] text-ink">{f.name}</span>
              </Link>
            </li>
          ))}
          {projects.map((f) => (
            <li key={`project-${f.id}`}>
              <Link
                to={projectFavoriteHref(f)}
                className="flex min-h-[47px] items-center gap-2 px-3 py-2 transition-colors hover:bg-wash-hover"
              >
                <FolderOpen
                  className="h-3.5 w-3.5 shrink-0 text-ink-muted"
                  aria-hidden="true"
                />
                <span className="rounded border border-border px-1.5 py-0.5 font-mono text-[10px] leading-none text-ink-secondary">
                  {f.identifier}
                </span>
                <span className="truncate text-[12px] text-ink">{f.name}</span>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
