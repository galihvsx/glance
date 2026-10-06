import { useCallback, useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { api, ApiError } from "../lib/api";
import type { IssueState, Project } from "../lib/types";
import { Badge } from "../components/ui/badge";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "../components/ui/card";
import { Skeleton } from "../components/ui/skeleton";
import { Alert, AlertDescription } from "../components/ui/alert";

// Placeholder project page (Phase 2). The issue list lands in Task 19 —
// here we prove the project resolves and its default states are visible.
export default function ProjectOverview() {
  const { slug, identifier } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const [project, setProject] = useState<Project | null>(null);
  const [states, setStates] = useState<IssueState[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    if (!slug || !identifier) return;
    setError(null);
    try {
      const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;
      // Single-project GET returns the BARE object…
      const p = await api.get<Project>(base);
      setProject(p);
      // …while the states list is WRAPPED: {"states": [...]}.
      const s = await api.get<{ states: IssueState[] }>(`${base}/states`);
      setStates(s.states);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Failed to load project");
    }
  }, [slug, identifier]);

  useEffect(() => {
    void load();
  }, [load]);

  return (
    <div className="mx-auto w-full max-w-3xl p-6">
      <Link
        to={`/w/${slug}`}
        className="text-xs text-muted-foreground hover:underline"
      >
        ← Projects
      </Link>

      {error && (
        <Alert variant="destructive" className="mt-4">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {!project && !error ? (
        <div className="mt-4 space-y-3">
          <Skeleton className="h-24 w-full" />
          <Skeleton className="h-40 w-full" />
        </div>
      ) : (
        project && (
          <>
            <div className="mb-6 mt-2 flex items-center gap-3">
              <Badge>{project.identifier}</Badge>
              <h1 className="text-2xl font-semibold tracking-tight">
                {project.name}
              </h1>
            </div>
            <Card>
              <CardHeader>
                <CardTitle className="text-base">States</CardTitle>
                <CardDescription>
                  Default states seeded at project creation.
                </CardDescription>
              </CardHeader>
              <CardContent>
                {states === null ? (
                  <div className="space-y-2">
                    <Skeleton className="h-8 w-full" />
                    <Skeleton className="h-8 w-full" />
                    <Skeleton className="h-8 w-full" />
                  </div>
                ) : (
                  <ul className="space-y-2">
                    {states.map((s) => (
                      <li
                        key={s.id}
                        className="flex items-center gap-3 rounded-md border px-3 py-2"
                      >
                        <span
                          className="h-3 w-3 rounded-full"
                          style={{ backgroundColor: s.color }}
                          aria-hidden
                        />
                        <span className="text-sm font-medium">{s.name}</span>
                        <Badge variant="secondary" className="ml-auto">
                          {s.group}
                        </Badge>
                      </li>
                    ))}
                  </ul>
                )}
              </CardContent>
            </Card>
            <p className="mt-4 text-sm text-muted-foreground">
              Issues arrive in Phase 3 — this page will become the issue list.
            </p>
          </>
        )
      )}
    </div>
  );
}
