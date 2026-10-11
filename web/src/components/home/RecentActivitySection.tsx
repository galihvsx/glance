// Recent activity section for the Home page (C17T4): the newest
// workspace-wide activity rows across the caller's workspaces, under
// "Your work". Each row carries a project badge and links to the issue.
//
// The workspace feed endpoint is per-workspace, so this fans out like
// Home's "Your work" aggregation (capped workspaces, small limits) and
// merges newest-first client-side. State names for state_id rows are
// resolved per distinct project in the feed (a handful of tiny
// requests).

import { useMemo } from "react";
import { Link } from "react-router-dom";
import { useQueries, useQuery } from "@tanstack/react-query";
import { Activity as ActivityIcon } from "lucide-react";
import { api } from "../../lib/api";
import { describeChange, type ActivityEntry } from "../../lib/activity";
import { relativeTime } from "../../lib/relativeTime";
import type { IssueState, Workspace } from "../../lib/types";
import { Avatar, AvatarFallback } from "../ui/avatar";
import { Badge } from "../ui/badge";
import { Card, CardContent } from "../ui/card";
import { Skeleton } from "../ui/skeleton";

const enc = encodeURIComponent;

/** Workspaces fanned out for the feed; rows fetched per workspace. */
const MAX_WORKSPACES = 5;
const PER_WORKSPACE = 8;
/** Rows shown in the section. */
const MAX_ROWS = 8;

/** A workspace-feed row tagged with its workspace slug for linking. */
export interface WorkspaceActivityRow extends ActivityEntry {
  slug: string;
}

/** Fetch + merge the newest workspace activity across workspaces. */
export async function fetchRecentActivity(
  workspaces: Workspace[],
): Promise<WorkspaceActivityRow[]> {
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

function actorInitial(name: string): string {
  return (name.trim()[0] ?? "?").toUpperCase();
}

function ChangeText({
  entry,
  stateById,
}: {
  entry: WorkspaceActivityRow;
  stateById: Map<string, string>;
}) {
  const d = describeChange(entry, stateById);
  switch (d.kind) {
    case "created":
      return <>created the issue</>;
    case "deleted":
      return <>deleted the issue</>;
    case "edited":
      return <>edited the description</>;
    case "set":
      return (
        <>
          set <span className="font-medium">{d.label}</span> to{" "}
          <code className="rounded bg-muted px-1 py-0.5 font-mono text-xs">
            {d.newText}
          </code>
        </>
      );
    case "cleared":
      return (
        <>
          cleared <span className="font-medium">{d.label}</span>
        </>
      );
    default:
      return (
        <>
          changed <span className="font-medium">{d.label}</span> from{" "}
          <code className="rounded bg-muted px-1 py-0.5 font-mono text-xs">
            {d.oldText}
          </code>{" "}
          to{" "}
          <code className="rounded bg-muted px-1 py-0.5 font-mono text-xs">
            {d.newText}
          </code>
        </>
      );
  }
}

function ActivityRow({
  entry,
  stateById,
}: {
  entry: WorkspaceActivityRow;
  stateById: Map<string, string>;
}) {
  return (
    <div className="flex items-start gap-3 border-b px-4 py-3 last:border-b-0">
      <Avatar className="h-8 w-8 shrink-0">
        <AvatarFallback className="text-xs font-semibold">
          {actorInitial(entry.actor)}
        </AvatarFallback>
      </Avatar>
      <div className="min-w-0 flex-1">
        <p className="text-sm leading-snug">
          <span className="font-medium">{entry.actor}</span>{" "}
          <ChangeText entry={entry} stateById={stateById} />
        </p>
        <div className="mt-1 flex flex-wrap items-center gap-2">
          <Link
            to={`/w/${entry.slug}/p/${entry.project_identifier}/i/${entry.issue_uuid}`}
            className="font-mono text-xs text-muted-foreground hover:text-foreground hover:underline"
          >
            {entry.issue_identifier}
          </Link>
          {entry.project_name && (
            <Badge variant="secondary" className="text-[11px]">
              {entry.project_name}
            </Badge>
          )}
          <span className="text-[11px] text-muted-foreground">
            {relativeTime(entry.at)}
          </span>
        </div>
      </div>
    </div>
  );
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
      <div className="mb-3 flex items-center justify-between">
        <h2 className="text-sm font-semibold uppercase tracking-wide text-muted-foreground">
          Recent activity
        </h2>
      </div>
      {activityQuery.isPending ? (
        <div className="space-y-2">
          <Skeleton className="h-14 w-full" />
          <Skeleton className="h-14 w-full" />
          <Skeleton className="h-14 w-full" />
        </div>
      ) : activityQuery.isError ? (
        <p className="text-sm text-muted-foreground">
          Couldn&apos;t load recent activity.
        </p>
      ) : rows.length === 0 ? (
        <Card>
          <CardContent className="pt-6">
            <div className="flex items-start gap-3">
              <ActivityIcon className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" />
              <div>
                <p className="text-sm font-medium">No recent activity</p>
                <p className="mt-1 text-xs text-muted-foreground">
                  Issue changes across your projects will show up here,
                  newest first.
                </p>
              </div>
            </div>
          </CardContent>
        </Card>
      ) : (
        <div className="overflow-hidden rounded-lg border bg-card">
          {rows.map((e, i) => (
            <ActivityRow
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
