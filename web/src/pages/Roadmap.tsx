import { useMemo, useState } from "react";
import { useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import ProjectNav from "../components/project/ProjectNav";
import PeekDrawer from "../components/issue/PeekDrawer";
import { usePeekParam } from "../components/issue/usePeek";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { api } from "../lib/api";
import type { Issue, IssueListResult, IssueState } from "../lib/types";
import { priorityLabel } from "../lib/types";
import {
  bucketIssues,
  currentMonthStart,
  roadmapMonths,
} from "../lib/roadmap";
import { cn } from "../lib/utils";

/**
 * Project roadmap: issues grouped by the month of their `target_date`,
 * with issues lacking a target date in an "Unscheduled" lane.
 *
 * Rendering choice: a single lane per month with state chips (colored dot
 * + state name), NOT per-state swimlanes. A swimlane layout multiplies the
 * vertical scan cost and duplicates the Gantt view's grouping; the single
 * lane keeps the month columns scannable while the chip still carries the
 * state signal (same chip vocabulary as the board cards).
 */
const RANGES = [3, 6, 12] as const;
type Range = (typeof RANGES)[number];

async function fetchAllIssues(path: string): Promise<Issue[]> {
  const issues: Issue[] = [];
  let cursor: string | undefined;
  do {
    const sep = path.includes("?") ? "&" : "?";
    const page = new URLSearchParams({ per_page: "100" });
    if (cursor) page.set("cursor", cursor);
    const res = await api.get<IssueListResult>(`${path}${sep}${page}`);
    issues.push(...res.results);
    cursor = res.next_cursor;
  } while (cursor);
  return issues;
}

function formatDay(day: string): string {
  const [y, m, d] = day.split("-").map(Number);
  return new Date(Date.UTC(y, m - 1, d)).toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
    timeZone: "UTC",
  });
}

export default function Roadmap() {
  const { slug = "", identifier = "" } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;
  const { peekUuid, openPeek, closePeek } = usePeekParam();

  const [range, setRange] = useState<Range>(6);
  const [anchor, setAnchor] = useState<string>(() => currentMonthStart());

  // One paginated list query composed from the existing endpoint — no new
  // backend work needed (start_date/target_date are already on the list
  // response, C9T0 contract check).
  const issuesQuery = useQuery({
    queryKey: ["roadmap", slug, identifier],
    queryFn: () => fetchAllIssues(`${base}/issues`),
  });
  const statesQuery = useQuery({
    queryKey: ["states", slug, identifier],
    queryFn: () =>
      api.get<{ states: IssueState[] }>(`${base}/states`).then((d) => d.states),
  });

  const statesById = useMemo(() => {
    const m = new Map<string, IssueState>();
    for (const s of statesQuery.data ?? []) m.set(s.id, s);
    return m;
  }, [statesQuery.data]);

  const buckets = useMemo(() => roadmapMonths(anchor, range), [anchor, range]);
  const { columns, unscheduled } = useMemo(
    () => bucketIssues(issuesQuery.data ?? [], buckets),
    [issuesQuery.data, buckets],
  );

  const loading = issuesQuery.isLoading || statesQuery.isLoading;
  const error = issuesQuery.error ?? statesQuery.error;

  const windowLabel =
    buckets.length > 0
      ? `${buckets[0].label} – ${buckets[buckets.length - 1].label}`
      : "";

  const renderIssue = (issue: Issue) => {
    const state = statesById.get(issue.state_id);
    const target = issue.target_date?.slice(0, 10);
    return (
      <button
        key={issue.id}
        type="button"
        onClick={() => openPeek(issue.id)}
        className="w-full space-y-1.5 rounded-md border bg-card p-2.5 text-left text-sm shadow-sm transition-colors hover:bg-accent"
        title={`${issue.display_id}: ${issue.name}`}
      >
        <div className="flex items-center gap-2">
          <Badge variant="outline" className="font-mono text-[11px]">
            {issue.display_id}
          </Badge>
          <span className="ml-auto text-[11px] text-muted-foreground">
            {priorityLabel(issue.priority)}
          </span>
        </div>
        <p className="font-medium leading-snug">{issue.name}</p>
        <div className="flex flex-wrap items-center gap-2">
          {state && (
            <span className="inline-flex items-center gap-1 text-[11px] text-muted-foreground">
              <span
                className="h-2 w-2 rounded-full"
                style={{ backgroundColor: state.color }}
                aria-hidden
              />
              {state.name}
            </span>
          )}
          {target && (
            <span className="text-[11px] text-muted-foreground">
              Due {formatDay(target)}
            </span>
          )}
        </div>
      </button>
    );
  };

  return (
    <div className="flex h-full flex-col gap-4 p-4">
      <ProjectNav />
      <div className="flex flex-wrap items-center gap-2">
        <div
          className="inline-flex rounded-md border"
          role="group"
          aria-label="Range"
        >
          {RANGES.map((r) => (
            <Button
              key={r}
              type="button"
              variant={range === r ? "secondary" : "ghost"}
              size="sm"
              className={cn(
                "rounded-none first:rounded-l-md last:rounded-r-md",
                range === r && "font-medium",
              )}
              onClick={() => setRange(r)}
            >
              {r} mo
            </Button>
          ))}
        </div>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => setAnchor(currentMonthStart())}
        >
          Today
        </Button>
        <span className="text-sm font-medium">{windowLabel}</span>
      </div>

      {loading && (
        <div className="text-sm text-muted-foreground">Loading roadmap…</div>
      )}
      {error && (
        <div className="text-sm text-destructive">
          Failed to load issues.{" "}
          <button
            type="button"
            className="underline"
            onClick={() => {
              void issuesQuery.refetch();
              void statesQuery.refetch();
            }}
          >
            Retry
          </button>
        </div>
      )}
      {!loading && !error && (
        <div className="flex min-h-0 flex-1 gap-3 overflow-x-auto pb-2">
          {columns.map(({ bucket, issues }) => (
            <section
              key={bucket.month}
              aria-label={bucket.label}
              className="flex w-72 shrink-0 flex-col rounded-md border bg-muted/30"
            >
              <header className="flex items-center justify-between border-b px-3 py-2">
                <h3 className="text-sm font-medium">{bucket.label}</h3>
                <Badge variant="secondary" className="text-[11px]">
                  {issues.length}
                </Badge>
              </header>
              <div className="flex min-h-0 flex-1 flex-col gap-2 overflow-y-auto p-2">
                {issues.map(renderIssue)}
                {issues.length === 0 && (
                  <p className="px-1 py-2 text-xs text-muted-foreground">
                    No issues due this month.
                  </p>
                )}
              </div>
            </section>
          ))}
          {/* Unscheduled lane: issues with no target_date (target_date is
              the bucket key — start_date alone does not schedule an
              issue; see roadmapBucket). */}
          <section
            aria-label="Unscheduled"
            className="flex w-72 shrink-0 flex-col rounded-md border border-dashed bg-muted/10"
          >
            <header className="flex items-center justify-between border-b border-dashed px-3 py-2">
              <h3 className="text-sm font-medium text-muted-foreground">
                Unscheduled
              </h3>
              <Badge variant="outline" className="text-[11px]">
                {unscheduled.length}
              </Badge>
            </header>
            <div className="flex min-h-0 flex-1 flex-col gap-2 overflow-y-auto p-2">
              {unscheduled.map(renderIssue)}
              {unscheduled.length === 0 && (
                <p className="px-1 py-2 text-xs text-muted-foreground">
                  Every issue has a target date.
                </p>
              )}
            </div>
          </section>
        </div>
      )}
      {peekUuid && (
        <PeekDrawer
          slug={slug}
          identifier={identifier}
          uuid={peekUuid}
          onClose={closePeek}
        />
      )}
    </div>
  );
}
