// Favorites sidebar section (C7T4): the caller's starred issues and
// projects as a right-rail card — starred issues link to their detail
// page, starred projects to their issue list. Honest empty state when
// nothing is starred. Dark-theme aware via theme tokens.
import { Link } from "react-router-dom";
import { FolderOpen, Star } from "lucide-react";
import {
  issueFavoriteHref,
  projectFavoriteHref,
  useFavorites,
} from "../../lib/favorites";
import { Alert, AlertDescription } from "../ui/alert";
import { Badge } from "../ui/badge";
import { Card, CardContent } from "../ui/card";
import { Skeleton } from "../ui/skeleton";

export default function FavoritesSection() {
  const { data, isPending, isError } = useFavorites();

  const issues = data?.issues ?? [];
  const projects = data?.projects ?? [];

  return (
    <div>
      <div className="mb-3 flex items-center justify-between">
        <h2 className="text-sm font-semibold uppercase tracking-wide text-muted-foreground">
          Favorites
        </h2>
      </div>

      {isPending ? (
        <div className="space-y-2">
          <Skeleton className="h-14 w-full" />
          <Skeleton className="h-14 w-full" />
        </div>
      ) : isError ? (
        <Alert variant="destructive">
          <AlertDescription>Failed to load favorites.</AlertDescription>
        </Alert>
      ) : issues.length === 0 && projects.length === 0 ? (
        <Card>
          <CardContent className="pt-6">
            <div className="flex items-start gap-3">
              <Star className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" />
              <div>
                <p className="text-sm font-medium">No favorites yet</p>
                <p className="mt-1 text-xs text-muted-foreground">
                  Star issues and projects to pin them here for quick
                  access.
                </p>
              </div>
            </div>
          </CardContent>
        </Card>
      ) : (
        <div className="space-y-2">
          {issues.map((f) => (
            <Link
              key={`issue-${f.id}`}
              to={issueFavoriteHref(f)}
              className="block rounded-lg border p-3 transition-colors hover:bg-muted/50"
            >
              <div className="flex items-center gap-2">
                <Badge variant="outline" className="font-mono text-[11px]">
                  {f.display_id}
                </Badge>
                <span className="text-[11px] text-muted-foreground">
                  {f.project_identifier}
                </span>
              </div>
              <p className="mt-1.5 truncate text-sm font-medium">{f.name}</p>
            </Link>
          ))}
          {projects.map((f) => (
            <Link
              key={`project-${f.id}`}
              to={projectFavoriteHref(f)}
              className="block rounded-lg border p-3 transition-colors hover:bg-muted/50"
            >
              <div className="flex items-center gap-2">
                <FolderOpen className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                <Badge variant="secondary" className="font-mono text-[11px]">
                  {f.identifier}
                </Badge>
              </div>
              <p className="mt-1.5 truncate text-sm font-medium">{f.name}</p>
            </Link>
          ))}
        </div>
      )}
    </div>
  );
}
