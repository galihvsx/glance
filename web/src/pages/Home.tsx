import { useEffect, useMemo, useState, type ReactNode } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { ChevronRight, Clock, Inbox, Layers, Plus, Shield } from "lucide-react";
import { api, ApiError } from "../lib/api";
import { useAuth } from "../lib/auth";
import { initials } from "../lib/profile";
import type { Issue, IssueListResult, Project, Workspace } from "../lib/types";
import IssueCard from "../components/issue/IssueCard";
import { DEFAULT_DISPLAY_SETTINGS } from "../components/issue/useDisplaySettings";
import {
  readRecentIssues,
  type RecentIssue,
} from "../components/issue/recents";
import ThemeToggle from "../components/ThemeToggle";
import NotificationBell from "../components/notifications/NotificationBell";
import { Button, buttonVariants } from "../components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "../components/ui/card";
import { Badge } from "../components/ui/badge";
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

function SectionTitle({
  children,
  action,
}: {
  children: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className="mb-3 flex items-center justify-between">
      <h2 className="text-sm font-semibold uppercase tracking-wide text-muted-foreground">
        {children}
      </h2>
      {action}
    </div>
  );
}

export default function Home() {
  const { user, logout } = useAuth();
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

  return (
    <div className="mx-auto w-full max-w-6xl p-6">
      {/* Top bar */}
      <div className="mb-8 flex items-center justify-between">
        <Link to="/" className="text-lg font-bold tracking-tight">
          glance
        </Link>
        <div className="flex items-center gap-2">
          {/* Instance admin entry (C5T1): visible only to is_admin users;
              the /admin route itself is guarded by AdminGuard + server-side
              RequireAdmin. */}
          {user?.is_admin && (
            <Link
              to="/admin"
              className={buttonVariants({ variant: "ghost", size: "sm" })}
              aria-label="Administration"
              title="Instance administration"
            >
              <Shield className="mr-1.5 h-4 w-4" />
              Admin
            </Link>
          )}
          <NotificationBell />
          <ThemeToggle />
          {/* Profile entry (C6T7): initials avatar linking to /profile. */}
          <Link
            to="/profile"
            className={buttonVariants({ variant: "ghost", size: "sm" })}
            aria-label="Profile and sessions"
            title={user?.email ?? "Profile"}
          >
            <span
              className="flex h-6 w-6 items-center justify-center rounded-full bg-primary text-[11px] font-semibold text-primary-foreground"
              aria-hidden
            >
              {initials(user?.name ?? null, user?.email ?? "?")}
            </span>
          </Link>
          <Button variant="ghost" size="sm" onClick={() => void logout()}>
            Log out
          </Button>
        </div>
      </div>

      {/* Greeting */}
      <div className="mb-8">
        <h1 className="text-3xl font-semibold tracking-tight">
          {greeting(now)}, {displayName}
        </h1>
        <p className="mt-1 text-sm text-muted-foreground">{longDate(now)}</p>
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

      <div className="grid gap-6 lg:grid-cols-3">
        {/* Your work */}
        <div className="lg:col-span-2">
          <SectionTitle
            action={
              activeProject && (
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() =>
                    navigate(
                      `/w/${activeProject.slug}/p/${activeProject.identifier}`,
                    )
                  }
                >
                  View all <ChevronRight className="ml-1 h-3.5 w-3.5" />
                </Button>
              )
            }
          >
            Your work
          </SectionTitle>

          {yourWorkQuery.isPending ? (
            <div className="space-y-3">
              <Skeleton className="h-20 w-full" />
              <Skeleton className="h-20 w-full" />
              <Skeleton className="h-20 w-full" />
            </div>
          ) : !yourWork || yourWork.length === 0 ? (
            <Card>
              <CardHeader>
                <CardTitle className="text-base">
                  Nothing assigned to you
                </CardTitle>
                <CardDescription>
                  Issues assigned to you across all your projects will show
                  up here, freshest first.
                </CardDescription>
              </CardHeader>
              <CardContent>
                <Button onClick={() => navigate("/w")}>
                  <Layers className="mr-2 h-4 w-4" /> Browse projects
                </Button>
              </CardContent>
            </Card>
          ) : (
            <div className="space-y-3">
              {yourWork.map((row) => (
                <div key={row.issue.id} className="relative">
                  <IssueCard
                    issue={row.issue}
                    state={undefined}
                    fields={DEFAULT_DISPLAY_SETTINGS.fields}
                    onOpen={(it) =>
                      navigate(
                        `/w/${row.slug}/p/${row.identifier}/i/${it.id}`,
                      )
                    }
                  />
                  <Badge
                    variant="secondary"
                    className="absolute right-3 top-3 text-[11px]"
                  >
                    {row.projectName}
                  </Badge>
                </div>
              ))}
            </div>
          )}
        </div>

        {/* Right rail */}
        <div className="space-y-8">
          {/* Recents */}
          <div>
            <SectionTitle>Recents</SectionTitle>
            {recents.length === 0 ? (
              <Card>
                <CardContent className="pt-6">
                  <div className="flex items-start gap-3">
                    <Clock className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" />
                    <div>
                      <p className="text-sm font-medium">No recent issues</p>
                      <p className="mt-1 text-xs text-muted-foreground">
                        Issues you open will appear here for quick access.
                      </p>
                      <Link
                        to="/w"
                        className="mt-3 inline-flex h-8 items-center rounded-md border border-input bg-background px-3 text-xs font-medium shadow-sm transition-colors hover:bg-accent hover:text-accent-foreground"
                      >
                        Browse projects
                      </Link>
                    </div>
                  </div>
                </CardContent>
              </Card>
            ) : (
              <div className="space-y-2">
                {recents.map((r) => (
                  <Link
                    key={r.id}
                    to={`/w/${r.slug}/p/${r.identifier}/i/${r.id}`}
                    className="block rounded-lg border p-3 transition-colors hover:bg-muted/50"
                  >
                    <div className="flex items-center gap-2">
                      <Badge variant="outline" className="font-mono text-[11px]">
                        {r.display_id}
                      </Badge>
                      <span className="text-[11px] text-muted-foreground">
                        {r.identifier}
                      </span>
                    </div>
                    <p className="mt-1.5 truncate text-sm font-medium">
                      {r.name}
                    </p>
                  </Link>
                ))}
              </div>
            )}
          </div>

          {/* Quick links */}
          <div>
            <SectionTitle>Quick links</SectionTitle>
            <Card>
              <CardContent className="space-y-1 pt-6">
                <Button
                  variant="ghost"
                  className="w-full justify-start"
                  onClick={() => navigate("/w")}
                >
                  <Layers className="mr-2 h-4 w-4" /> Workspaces
                  <ChevronRight className="ml-auto h-4 w-4 text-muted-foreground" />
                </Button>
                {workspacesQuery.isPending ? (
                  <Skeleton className="h-9 w-full" />
                ) : workspaces && workspaces.length === 0 ? (
                  <p className="px-3 py-2 text-xs text-muted-foreground">
                    No workspaces yet — create one to get started.
                  </p>
                ) : (
                  activeProject && (
                    <>
                      <p className="px-3 pb-1 pt-3 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
                        {activeProject.label}
                      </p>
                      {[
                        {
                          to: `/w/${activeProject.slug}/p/${activeProject.identifier}`,
                          label: "Issues",
                        },
                        {
                          to: `/w/${activeProject.slug}/p/${activeProject.identifier}/board`,
                          label: "Board",
                        },
                        {
                          to: `/w/${activeProject.slug}/p/${activeProject.identifier}/cycles`,
                          label: "Cycles",
                        },
                        {
                          to: `/w/${activeProject.slug}/p/${activeProject.identifier}/intake`,
                          label: "Inbox",
                          icon: <Inbox className="mr-2 h-4 w-4" />,
                        },
                      ].map((l) => (
                        <Button
                          key={l.to}
                          variant="ghost"
                          className="w-full justify-start"
                          onClick={() => navigate(l.to)}
                        >
                          {l.icon}
                          {l.label}
                          <ChevronRight className="ml-auto h-4 w-4 text-muted-foreground" />
                        </Button>
                      ))}
                    </>
                  )
                )}
                {workspaces && workspaces.length === 0 && (
                  <Button
                    className="mt-2 w-full"
                    onClick={() => navigate("/w")}
                  >
                    <Plus className="mr-2 h-4 w-4" /> New workspace
                  </Button>
                )}
              </CardContent>
            </Card>
          </div>
        </div>
      </div>
    </div>
  );
}
