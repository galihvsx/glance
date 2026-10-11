// Home dashboard (C19T9 polish): greeting + overview stat band (prototype
// SHELL CONTENT §3 language), "Your work" as dense 47px rows (C19T5 list
// row language: display-id mono 10px muted, title 12px ink, priority
// bars + 10px label), Recents + Quick links as dense hairline rows.
// Section headers use the prototype "Recently updated" bar language
// (35px, bottom hairline, 12px/600). Empty states in the token style.
import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { ChevronRight, Clock, Inbox, Layers, Plus } from "lucide-react";
import { api, ApiError } from "../lib/api";
import { useAuth } from "../lib/auth";
import type { Issue, IssueListResult, Project, Workspace } from "../lib/types";
import { priorityLabel } from "../lib/types";
import { relativeTime } from "../lib/relativeTime";
import {
  readRecentIssues,
  type RecentIssue,
} from "../components/issue/recents";
import PriorityBars from "../components/issue/PriorityBars";
import FavoritesSection from "../components/favorites/FavoritesSection";
import RecentActivitySection from "../components/home/RecentActivitySection";
import EmptyState from "../components/personal/EmptyState";
import { SectionBar, StatBand } from "../components/personal/SectionBar";
import { Button } from "../components/ui/button";
import { Skeleton } from "../components/ui/skeleton";
import { Alert, AlertDescription } from "../components/ui/alert";

/** Cap on projects fanned out for the "Your work" aggregation. */
const MAX_PROJECTS = 10;
/** Rows shown in "Your work". */
const MAX_YOUR_WORK = 10;

interface WorkRow {
  issue: Issue;
  slug: string;
  identifier: string;
  projectName: string;
}

const enc = encodeURIComponent;

function greeting(now: Date): string {
  const h = now.getHours();
  if (h < 12) return "Good morning";
  if (h < 18) return "Good afternoon";
  return "Good evening";
}

function longDate(now: Date): string {
  return now.toLocaleDateString("en-US", {
    weekday: "long",
    year: "numeric",
    month: "long",
    day: "numeric",
  });
}

/** Read-only aggregation over existing list APIs — no new backend needed. */
async function fetchYourWork(userId: string): Promise<WorkRow[]> {
  const { workspaces } = await api.get<{ workspaces: Workspace[] }>(
    "/api/v1/workspaces",
  );
  const projects: (Project & { slug: string })[] = [];
  for (const ws of workspaces) {
    const { projects: ps } = await api.get<{ projects: Project[] }>(
      `/api/v1/workspaces/${enc(ws.slug)}/projects`,
    );
    for (const p of ps) projects.push({ ...p, slug: ws.slug });
  }
  const targets = projects.slice(0, MAX_PROJECTS);
  const perProject = await Promise.all(
    targets.map(async (p) => {
      const params = new URLSearchParams({
        per_page: String(MAX_YOUR_WORK),
        order_by: "-updated_at",
        assignee: userId,
      });
      const { results } = await api.get<IssueListResult>(
        `/api/v1/workspaces/${enc(p.slug)}/projects/${enc(p.identifier)}/issues?${params}`,
      );
      return results.map((issue) => ({
        issue,
        slug: p.slug,
        identifier: p.identifier,
        projectName: p.name,
      }));
    }),
  );
  return perProject
    .flat()
    .sort((a, b) => b.issue.updated_at.localeCompare(a.issue.updated_at))
    .slice(0, MAX_YOUR_WORK);
}

function HomeWorkRow({ row, onOpen }: { row: WorkRow; onOpen: () => void }) {
  return (
    <li>
      <button
        type="button"
        onClick={onOpen}
        className="grid min-h-[47px] w-full grid-cols-[76px_minmax(0,1fr)_90px_70px] items-center gap-[10px] px-3 py-2 text-left transition-colors hover:bg-wash-hover"
      >
        <span className="truncate font-mono text-[10px] text-ink-muted">
          {row.issue.display_id}
        </span>
        <span className="flex min-w-0 items-center gap-2">
          <span className="truncate text-[12px] text-ink">
            {row.issue.name}
          </span>
          <span className="shrink-0 rounded border border-border px-1.5 py-0.5 text-[10px] leading-none text-ink-secondary">
            {row.projectName}
          </span>
        </span>
        <span className="flex min-w-0 items-center gap-[5px]">
          {row.issue.priority > 0 ? (
            <>
              <PriorityBars priority={row.issue.priority} />
              <span className="truncate text-[10px] text-ink-secondary">
                {priorityLabel(row.issue.priority)}
              </span>
            </>
          ) : null}
        </span>
        <span className="truncate text-[10px] text-ink-muted">
          {relativeTime(row.issue.updated_at)}
        </span>
      </button>
    </li>
  );
}

function QuickLinkRow({
  to,
  icon,
  label,
}: {
  to: string;
  icon?: React.ReactNode;
  label: string;
}) {
  return (
    <li>
      <Link
        to={to}
        className="flex h-[34px] items-center gap-2 border-b border-border-hairline px-3 text-[12px] text-ink-secondary transition-colors last:border-b-0 hover:bg-wash-hover hover:text-ink"
      >
        {icon}
        <span className="truncate">{label}</span>
        <ChevronRight
          className="ml-auto h-3.5 w-3.5 shrink-0 text-ink-muted"
          aria-hidden="true"
        />
      </Link>
    </li>
  );
}

export default function Home() {
  const { user } = useAuth();
  const navigate = useNavigate();
  const [now] = useState(() => new Date());
  // Recents are localStorage-only; re-read when the component mounts and
  // whenever the window regains focus (a visit on another tab/page).
  const [recents, setRecents] = useState<RecentIssue[]>(() =>
    readRecentIssues(),
  );

  // Re-read when the tab regains focus: a visit on another page (or tab)
  // updates localStorage while this component stays mounted.
  useEffect(() => {
    const onFocus = () => setRecents(readRecentIssues());
    window.addEventListener("focus", onFocus);
    return () => window.removeEventListener("focus", onFocus);
  }, []);

  const yourWorkQuery = useQuery({
    queryKey: ["dashboard-your-work", user?.id],
    enabled: !!user?.id,
    queryFn: () => fetchYourWork(user!.id),
  });

  const workspacesQuery = useQuery({
    queryKey: ["dashboard-workspaces"],
    queryFn: () =>
      api
        .get<{ workspaces: Workspace[] }>("/api/v1/workspaces")
        .then((d) => d.workspaces),
  });

  const yourWork: WorkRow[] | undefined = yourWorkQuery.data;
  const workspaces = workspacesQuery.data;

  // Quick links anchor on the most relevant project: the most recent
  // visit, else the freshest assigned issue, else nothing.
  const activeProject = useMemo(() => {
    if (recents[0])
      return {
        slug: recents[0].slug,
        identifier: recents[0].identifier,
        label: recents[0].identifier,
      };
    if (yourWork?.[0])
      return {
        slug: yourWork[0].slug,
        identifier: yourWork[0].identifier,
        label: yourWork[0].projectName,
      };
    return null;
  }, [recents, yourWork]);

  const displayName = user?.name ?? user?.email ?? "there";
  const yourWorkProjects = useMemo(
    () => new Set((yourWork ?? []).map((r) => r.identifier)).size,
    [yourWork],
  );

  return (
    <div className="mx-auto w-full max-w-6xl p-6">
      {/* Greeting */}
      <div className="mb-6">
        <h1 className="text-[15px] font-semibold tracking-tight text-ink">
          {greeting(now)}, {displayName}
        </h1>
        <p className="mt-0.5 text-xs text-ink-muted">{longDate(now)}</p>
      </div>

      {yourWorkQuery.isError && (
        <Alert variant="destructive" className="mb-6">
          <AlertDescription>
            {yourWorkQuery.error instanceof ApiError
              ? yourWorkQuery.error.message
              : "Failed to load your work"}
          </AlertDescription>
        </Alert>
      )}

      {yourWork !== undefined && (
        <div className="mb-6">
          <StatBand
            stats={[
              {
                label: "Assigned to you",
                value: yourWork.length,
                note: `${yourWorkProjects} project${yourWorkProjects === 1 ? "" : "s"}`,
              },
              {
                label: "Recent issues",
                value: recents.length,
                note: "on this device",
              },
              {
                label: "Workspaces",
                value: workspaces?.length ?? 0,
                note: "total",
              },
            ]}
          />
        </div>
      )}

      <div className="grid gap-6 lg:grid-cols-3">
        {/* Your work */}
        <div className="lg:col-span-2">
          <SectionBar
            label="Your work"
            count={yourWork?.length}
            action={
              activeProject && (
                <button
                  type="button"
                  onClick={() =>
                    navigate(
                      `/w/${activeProject.slug}/p/${activeProject.identifier}`,
                    )
                  }
                  className="flex h-7 items-center gap-1 rounded-md px-2 text-[11px] text-ink-secondary transition-colors hover:bg-wash-hover hover:text-ink"
                >
                  View all
                  <ChevronRight className="h-3.5 w-3.5" aria-hidden="true" />
                </button>
              )
            }
          />

          {yourWorkQuery.isPending ? (
            <div className="space-y-2 pt-2">
              <Skeleton className="h-[47px] w-full" />
              <Skeleton className="h-[47px] w-full" />
              <Skeleton className="h-[47px] w-full" />
            </div>
          ) : !yourWork || yourWork.length === 0 ? (
            <div className="pt-4">
              <EmptyState icon={Inbox} title="Nothing assigned to you">
                <p>
                  Issues assigned to you across all your projects will show
                  up here, freshest first.
                </p>
                <Button
                  variant="outline"
                  size="sm"
                  className="mt-3"
                  onClick={() => navigate("/w")}
                >
                  <Layers className="mr-2 h-4 w-4" /> Browse projects
                </Button>
              </EmptyState>
            </div>
          ) : (
            <ul className="mt-2 divide-y divide-border-hairline overflow-hidden rounded-md border border-border">
              {yourWork.map((row) => (
                <HomeWorkRow
                  key={row.issue.id}
                  row={row}
                  onOpen={() =>
                    navigate(
                      `/w/${row.slug}/p/${row.identifier}/i/${row.issue.id}`,
                    )
                  }
                />
              ))}
            </ul>
          )}

          {/* Recent activity (C17T4): workspace-wide feed under Your work. */}
          <RecentActivitySection workspaces={workspaces} />
        </div>

        {/* Right rail */}
        <div className="space-y-6">
          {/* Favorites (C7T4): starred issues/projects, click to navigate. */}
          <FavoritesSection />

          {/* Recents */}
          <div>
            <SectionBar label="Recents" count={recents.length || undefined} />
            {recents.length === 0 ? (
              <div className="pt-4">
                <EmptyState icon={Clock} title="No recent issues">
                  <p>Issues you open will appear here for quick access.</p>
                </EmptyState>
              </div>
            ) : (
              <ul className="mt-2 divide-y divide-border-hairline overflow-hidden rounded-md border border-border">
                {recents.map((r) => (
                  <li key={r.id}>
                    <Link
                      to={`/w/${r.slug}/p/${r.identifier}/i/${r.id}`}
                      className="flex min-h-[47px] flex-col justify-center gap-0.5 px-3 py-2 transition-colors hover:bg-wash-hover"
                    >
                      <span className="flex items-center gap-2">
                        <span className="font-mono text-[10px] text-ink-muted">
                          {r.display_id}
                        </span>
                        <span className="text-[10px] text-ink-muted">
                          {r.identifier}
                        </span>
                      </span>
                      <span className="truncate text-[12px] text-ink">
                        {r.name}
                      </span>
                    </Link>
                  </li>
                ))}
              </ul>
            )}
          </div>

          {/* Quick links */}
          <div>
            <SectionBar label="Quick links" />
            {workspacesQuery.isPending ? (
              <div className="pt-2">
                <Skeleton className="h-9 w-full" />
              </div>
            ) : !workspaces || workspaces.length === 0 ? (
              <div className="pt-4">
                <EmptyState icon={Layers} title="No workspaces yet">
                  <p>Create one to get started.</p>
                  <Button
                    variant="outline"
                    size="sm"
                    className="mt-3"
                    onClick={() => navigate("/w")}
                  >
                    <Plus className="mr-2 h-4 w-4" /> New workspace
                  </Button>
                </EmptyState>
              </div>
            ) : (
              <ul className="mt-2 overflow-hidden rounded-md border border-border">
                <QuickLinkRow
                  to="/w"
                  icon={<Layers className="h-3.5 w-3.5" aria-hidden="true" />}
                  label="Workspaces"
                />
                {activeProject && (
                  <>
                    <li className="flex h-[28px] items-center border-b border-border-hairline bg-surface px-3 text-[10px] font-semibold uppercase tracking-wide text-ink-muted">
                      {activeProject.label}
                    </li>
                    <QuickLinkRow
                      to={`/w/${activeProject.slug}/p/${activeProject.identifier}`}
                      label="Issues"
                    />
                    <QuickLinkRow
                      to={`/w/${activeProject.slug}/p/${activeProject.identifier}/board`}
                      label="Board"
                    />
                    <QuickLinkRow
                      to={`/w/${activeProject.slug}/p/${activeProject.identifier}/cycles`}
                      label="Cycles"
                    />
                    <QuickLinkRow
                      to={`/w/${activeProject.slug}/p/${activeProject.identifier}/intake`}
                      icon={<Inbox className="h-3.5 w-3.5" aria-hidden="true" />}
                      label="Inbox"
                    />
                  </>
                )}
              </ul>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
