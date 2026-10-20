// Recent activity section for the Home page (C17T4, C19T9 polish): the
// newest workspace-wide activity rows across the caller's workspaces,
// under "Your work". Dense hairline rows in the token ramp: 24px actor
// avatar, 12px copy, 10px muted meta. SectionBar header, token empty
// state.
//
// The workspace feed endpoint is per-workspace, so this fans out like
// Home's "Your work" aggregation (capped workspaces, small limits) and
// merges newest-first client-side. State names for state_id rows are
// resolved per distinct project in the feed (a handful of tiny
// requests).

import { useMemo } from "react";
import { useQueries, useQuery } from "@tanstack/react-query";
import { Activity as ActivityIcon } from "lucide-react";
import { api } from "../../lib/api";
import type { ActivityEntry } from "../../lib/activity";
import type { IssueState, Workspace } from "../../lib/types";
import {
  WorkspaceActivityRow,
  type WorkspaceActivityRowEntry,
} from "../activity/WorkspaceActivityRow";
import EmptyState from "../personal/EmptyState";
import { SectionBar } from "../personal/SectionBar";
import { Skeleton } from "../ui/skeleton";

const enc = encodeURIComponent;

/** Workspaces fanned out for the feed; rows fetched per workspace. */
const MAX_WORKSPACES = 5;
const PER_WORKSPACE = 8;
/** Rows shown in the section. */
const MAX_ROWS = 8;

/** Fetch + merge the newest workspace activity across workspaces. */
export async function fetchRecentActivity(
  workspaces: Workspace[],
): Promise<WorkspaceActivityRowEntry[]> {
  const targets = workspaces.slice(0, MAX_WORKSPACES);
  const perWs = await Promise.all(
    targets.map(async (ws) => {
      const { activity } = await api.get<{ activity: ActivityEntry[] }>(
        `/api/v1/workspaces/${enc(ws.slug)}/activity?limit=${PER_WORKSPACE}`,
      );
      return activity.map((e) => ({ ...e, slug: ws.slug }));
    }),
  );
  return perWs
    .flat()
    .sort((a, b) => b.at.localeCompare(a.at))
    .slice(0, MAX_ROWS);
}

export default function RecentActivitySection({
  workspaces,
}: {
  workspaces: Workspace[] | undefined;
}) {
  const activityQuery = useQuery({
    queryKey: ["home-recent-activity", workspaces?.map((w) => w.slug)],
    enabled: !!workspaces && workspaces.length > 0,
    queryFn: () => fetchRecentActivity(workspaces!),
  });

  const rows = activityQuery.data ?? [];

  // Distinct projects in the merged feed, for state-name resolution.
  const projectKeys = useMemo(() => {
    const seen = new Map<string, { slug: string; identifier: string }>();
    for (const r of rows) {
      if (!r.project_identifier) continue;
      const key = `${r.slug}/${r.project_identifier}`;
      if (!seen.has(key))
        seen.set(key, { slug: r.slug, identifier: r.project_identifier });
    }
    return [...seen.entries()];
  }, [rows]);

  const statesQueries = useQueries({
    queries: projectKeys.map(([key, p]) => ({
      queryKey: ["home-activity-states", key],
      queryFn: () =>
        api
          .get<{ states: IssueState[] }>(
            `/api/v1/workspaces/${enc(p.slug)}/projects/${enc(p.identifier)}/states`,
          )
          .then((d) => d.states),
    })),
  });

  const stateByProject = useMemo(() => {
    const byProject = new Map<string, Map<string, string>>();
    projectKeys.forEach(([key], i) => {
      const m = new Map<string, string>();
      for (const s of statesQueries[i]?.data ?? []) m.set(s.id, s.name);
      byProject.set(key, m);
    });
    return byProject;
  }, [projectKeys, statesQueries]);

  if (!workspaces || workspaces.length === 0) return null;

  return (
    <div className="mt-8">
      <SectionBar label="Recent activity" count={rows.length || undefined} />
      {activityQuery.isPending ? (
        <div className="space-y-2 pt-2">
          <Skeleton className="h-[47px] w-full" />
          <Skeleton className="h-[47px] w-full" />
          <Skeleton className="h-[47px] w-full" />
        </div>
      ) : activityQuery.isError ? (
        <p className="pt-2 text-xs text-ink-muted">
          Couldn&apos;t load recent activity.
        </p>
      ) : rows.length === 0 ? (
        <div className="pt-4">
          <EmptyState icon={ActivityIcon} title="No recent activity">
            <p>
              Issue changes across your projects will show up here, newest
              first.
            </p>
          </EmptyState>
        </div>
      ) : (
        <div className="mt-2 divide-y divide-border-hairline overflow-hidden rounded-md border border-border">
          {rows.map((e, i) => (
            <WorkspaceActivityRow
              key={`${e.at}-${e.issue_uuid}-${e.field}-${i}`}
              entry={e}
              stateById={
                stateByProject.get(
                  `${e.slug}/${e.project_identifier ?? ""}`,
                ) ?? new Map()
              }
            />
          ))}
        </div>
      )}
    </div>
  );
}
