// Workload (C29T1): workspace-level view of each member's open load
// against their weekly capacity. Per-member rows with load-vs-capacity
// bars, overdue/urgent flags, estimate-point load and 14-day velocity;
// repeatable `project` query params scope the report to projects;
// member names drill into the Issues view filtered by assignee.

import { useMemo } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import PageHeader from "../components/shell/PageHeader";
import ProjectFilterChips from "../components/workload/ProjectFilterChips";
import WorkloadMemberRow from "../components/workload/WorkloadMemberRow";
import { Alert, AlertDescription } from "../components/ui/alert";
import { Skeleton } from "../components/ui/skeleton";
import { api } from "../lib/api";
import { relativeTime } from "../lib/relativeTime";
import type { Project } from "../lib/types";
import { fetchWorkload } from "../lib/workload";

function Loading() {
  return (
    <div className="space-y-3" aria-label="Loading workload">
      {[0, 1, 2].map((i) => (
        <Skeleton
          key={i}
          data-testid="workload-skeleton"
          className="h-[104px] w-full rounded-lg"
        />
      ))}
    </div>
  );
}

export default function Workload() {
  const { slug = "" } = useParams<{ slug: string }>();
  const [searchParams, setSearchParams] = useSearchParams();

  const projectsQuery = useQuery({
    queryKey: ["projects", slug],
    queryFn: () =>
      api
        .get<{ projects: Project[] }>(
          `/api/v1/workspaces/${encodeURIComponent(slug)}/projects`,
        )
        .then((d) => d.projects),
    enabled: !!slug,
  });
  const projects = projectsQuery.data ?? [];
  const byProjectId = useMemo(
    () => new Map((projectsQuery.data ?? []).map((p) => [p.id, p])),
    [projectsQuery.data],
  );

  // Selected project ids live in the repeatable `project` query param.
  // Unknown ids (stale links) are dropped before fetching.
  const requested = useMemo(() => searchParams.getAll("project"), [searchParams]);
  const selectedIds = useMemo(() => {
    const known = new Set((projectsQuery.data ?? []).map((p) => p.id));
    return requested.filter((id) => known.has(id));
  }, [requested, projectsQuery.data]);

  const setSelectedIds = (ids: string[]) => {
    const params = new URLSearchParams(searchParams);
    params.delete("project");
    for (const id of ids) params.append("project", id);
    setSearchParams(params, { replace: true });
  };

  const workloadQuery = useQuery({
    queryKey: ["workload", slug, selectedIds.join(",")],
    queryFn: () => fetchWorkload(slug, selectedIds),
    enabled: !!slug && !projectsQuery.isPending,
  });
  const report = workloadQuery.data;
  const members = report?.members ?? [];

  const drilldownHrefFor = (userId: string, memberProjectIds: string[]) => {
    const scoped =
      selectedIds.length === 1 ? selectedIds[0] : memberProjectIds[0];
    if (!scoped) return null;
    const project = byProjectId.get(scoped);
    if (!project) return null;
    return `/w/${encodeURIComponent(slug)}/p/${encodeURIComponent(project.identifier)}?assignee=${encodeURIComponent(userId)}`;
  };

  return (
    <div className="w-full">
      <PageHeader
        viewName="Workload"
        count={members.length}
        showAnalytics={false}
        addButton={null}
      />
      <div className="mx-auto w-full max-w-[1060px] space-y-4 px-7 py-6">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <ProjectFilterChips
            projects={projects}
            selected={selectedIds}
            onChange={setSelectedIds}
          />
          {report && (
            <p className="text-[11px] text-ink-muted">
              Updated {relativeTime(report.generated_at)}
            </p>
          )}
        </div>

        {workloadQuery.isPending || projectsQuery.isPending ? (
          <Loading />
        ) : workloadQuery.isError ? (
          <Alert variant="destructive">
            <AlertDescription>
              Couldn&apos;t load workload. Try again in a moment.
            </AlertDescription>
          </Alert>
        ) : members.length === 0 ? (
          <p className="py-8 text-center text-xs text-muted-foreground">
            No members in this workspace yet.
          </p>
        ) : (
          <section aria-label="Team workload" className="space-y-3">
            {members.map((m) => (
              <WorkloadMemberRow
                key={m.user_id}
                member={m}
                drilldownHref={drilldownHrefFor(m.user_id, m.project_ids)}
              />
            ))}
          </section>
        )}

        {report && report.unassigned.length > 0 && (
          <section
            aria-label="Unassigned issues"
            className="rounded-lg border border-border-hairline bg-surface p-4"
          >
            <h2 className="mb-3 text-xs font-semibold">Unassigned</h2>
            <div className="flex flex-wrap gap-1.5">
              {report.unassigned.map((u) => {
                const href = `/w/${encodeURIComponent(slug)}/p/${encodeURIComponent(u.project_identifier)}?assignee=none`;
                return (
                  <Link
                    key={u.project_id}
                    to={href}
                    aria-label={`View unassigned issues in ${u.project_identifier}`}
                    className="rounded-md border border-border-hairline bg-wash px-2.5 py-1.5 font-mono text-[11px] text-ink-secondary hover:text-ink"
                  >
                    {u.project_identifier} ·{" "}
                    <span className="tabular-nums">{u.open_count}</span> open
                  </Link>
                );
              })}
            </div>
          </section>
        )}
      </div>
    </div>
  );
}
