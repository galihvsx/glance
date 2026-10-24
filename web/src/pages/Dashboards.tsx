// Dashboards (C81T1): workspace-scoped dashboard list. Cards with
// name, description, visibility badge (Public/Private), creator name,
// and updated-at; "New dashboard" opens the create dialog (Plane
// defaults new dashboards to public). Guests (role < 15) can't create
// — the server 403 stays the backstop. The list endpoint already
// filters out other members' private dashboards, so a private
// dashboard is never linked/rendered for non-creators.
import { useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Globe, Lock, Share2 } from "lucide-react";
import { api, ApiError } from "../lib/api";
import { relativeTime } from "../lib/relativeTime";
import type { Workspace } from "../lib/types";
import {
  ACCESS_LABELS,
  createDashboard,
  createDashboardFromTemplate,
  dashboardsKey,
  type Dashboard,
  type DashboardVisibility,
} from "../lib/dashboards";
import PageHeader from "../components/shell/PageHeader";
import { Alert, AlertDescription } from "../components/ui/alert";
import { Skeleton } from "../components/ui/skeleton";
import DashboardDialog, {
  type DashboardFormValue,
} from "../components/dashboards/DashboardDialog";

export function VisibilityBadge({ visibility }: { visibility: DashboardVisibility }) {
  const Icon = visibility === "public" ? Globe : Lock;
  return (
    <span
      className={
        "inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[11px] font-medium " +
        (visibility === "public"
          ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400"
          : "border-amber-500/30 bg-amber-500/10 text-amber-600 dark:text-amber-400")
      }
    >
      <Icon className="size-3" aria-hidden="true" />
      {visibility === "public" ? "Public" : "Private"}
    </span>
  );
}

/** Badge for the Shared tab: "Shared with you · Edit" for inbound
 *  shares, "Shared" for dashboards the caller owns and shared out. */
export function ShareBadge({ dashboard }: { dashboard: Dashboard }) {
  if (dashboard.shared_with_me) {
    return (
      <span className="inline-flex items-center gap-1 rounded-full border border-sky-500/30 bg-sky-500/10 px-2 py-0.5 text-[11px] font-medium text-sky-600 dark:text-sky-400">
        <Share2 className="size-3" aria-hidden="true" />
        Shared with you
        {dashboard.my_access
          ? ` · ${ACCESS_LABELS[dashboard.my_access]}`
          : ""}
      </span>
    );
  }
  if (dashboard.shared) {
    return (
      <span className="inline-flex items-center gap-1 rounded-full border border-sky-500/30 bg-sky-500/10 px-2 py-0.5 text-[11px] font-medium text-sky-600 dark:text-sky-400">
        <Share2 className="size-3" aria-hidden="true" />
        Shared
      </span>
    );
  }
  return null;
}

function DashboardRow({
  dashboard,
  onOpen,
}: {
  dashboard: Dashboard;
  onOpen: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onOpen}
      className="flex w-full items-center gap-3 rounded-lg border border-border bg-raised px-4 py-3 text-left transition-colors hover:border-primary/40"
    >
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="truncate text-sm font-semibold text-ink">
            {dashboard.name}
          </span>
          <VisibilityBadge visibility={dashboard.visibility} />
          <ShareBadge dashboard={dashboard} />
        </div>
        {dashboard.description && (
          <p className="mt-0.5 truncate text-[13px] text-ink-muted">
            {dashboard.description}
          </p>
        )}
        <p className="mt-1 text-[11px] text-muted-foreground">
          {dashboard.created_by_name} · updated {relativeTime(dashboard.updated_at)}
        </p>
      </div>
    </button>
  );
}

export default function Dashboards() {
  const { slug = "" } = useParams<{ slug: string }>();
  const navigate = useNavigate();
  const queryClient = useQueryClient();

  const [createOpen, setCreateOpen] = useState(false);
  const [serverError, setServerError] = useState<string | null>(null);
  const [tab, setTab] = useState<"all" | "shared">("all");

  const dashboardsQuery = useQuery({
    queryKey: dashboardsKey(slug),
    queryFn: () =>
      api
        .get<{ dashboards: Dashboard[] }>(
          `/api/v1/workspaces/${encodeURIComponent(slug)}/dashboards`,
        )
        .then((d) => d.dashboards),
  });
  const workspaceQuery = useQuery({
    queryKey: ["workspace", slug],
    queryFn: () =>
      api.get<Workspace>(`/api/v1/workspaces/${encodeURIComponent(slug)}`),
  });

  const role = workspaceQuery.data?.role ?? 0;
  const canCreate = role >= 15;

  const createMutation = useMutation({
    mutationFn: (v: DashboardFormValue) =>
      createDashboard(slug, {
        name: v.name,
        description: v.description,
        visibility: v.visibility,
      }),
    onSuccess: (d) => {
      setServerError(null);
      setCreateOpen(false);
      void queryClient.invalidateQueries({ queryKey: dashboardsKey(slug) });
      navigate(`/w/${encodeURIComponent(slug)}/dashboards/${encodeURIComponent(d.id)}`);
    },
    onError: (e) => {
      setServerError(
        e instanceof ApiError ? e.message : "Failed to create dashboard",
      );
    },
  });

  const templateMutation = useMutation({
    mutationFn: (v: { template: string; name?: string }) =>
      createDashboardFromTemplate(slug, v),
    onSuccess: (d) => {
      setServerError(null);
      setCreateOpen(false);
      void queryClient.invalidateQueries({ queryKey: dashboardsKey(slug) });
      navigate(`/w/${encodeURIComponent(slug)}/dashboards/${encodeURIComponent(d.id)}`);
    },
    onError: (e) => {
      setServerError(
        e instanceof ApiError ? e.message : "Failed to create dashboard",
      );
    },
  });

  const dashboards = dashboardsQuery.data ?? [];
  // Shared tab (Plane parity): dashboards shared WITH me, plus my own
  // dashboards that I have shared out.
  const sharedDashboards = dashboards.filter(
    (d) => d.shared_with_me || d.shared,
  );
  const visibleDashboards = tab === "shared" ? sharedDashboards : dashboards;

  return (
    <>
      <PageHeader
        viewName="Dashboards"
        count={dashboards.length}
        addButton={
          canCreate
            ? { label: "New dashboard", onClick: () => setCreateOpen(true) }
            : null
        }
      />
      <div className="mx-auto w-full max-w-[1060px] px-7 py-6">
        {dashboardsQuery.isPending ? (
          <div className="flex flex-col gap-2">
            <Skeleton className="h-[72px] w-full" />
            <Skeleton className="h-[72px] w-full" />
            <Skeleton className="h-[72px] w-full" />
          </div>
        ) : dashboardsQuery.isError ? (
          <Alert variant="destructive">
            <AlertDescription>
              Couldn't load dashboards.
              <button
                type="button"
                className="ml-2 underline"
                onClick={() => dashboardsQuery.refetch()}
              >
                Retry
              </button>
            </AlertDescription>
          </Alert>
        ) : dashboards.length === 0 ? (
          <div className="flex flex-col items-center justify-center gap-3 rounded-xl border border-dashed border-border bg-surface/50 px-6 py-16 text-center">
            <p className="text-sm font-semibold text-ink">No dashboards yet</p>
            <p className="max-w-sm text-sm text-muted-foreground">
              Dashboards are workspace-scoped canvases of widgets that
              visualize issue data live.
            </p>
            {canCreate && (
              <button
                type="button"
                onClick={() => setCreateOpen(true)}
                className="text-sm font-medium text-primary hover:underline"
              >
                Create your first dashboard
              </button>
            )}
          </div>
        ) : (
          <>
            <div
              role="tablist"
              aria-label="Dashboard lists"
              className="mb-4 flex items-center gap-1"
            >
              {(
                [
                  { key: "all", label: "All dashboards" },
                  { key: "shared", label: "Shared" },
                ] as const
              ).map((t) => (
                <button
                  key={t.key}
                  type="button"
                  role="tab"
                  aria-selected={tab === t.key}
                  onClick={() => setTab(t.key)}
                  className={
                    "rounded-md px-3 py-1.5 text-sm font-medium transition-colors " +
                    (tab === t.key
                      ? "bg-accent text-foreground"
                      : "text-muted-foreground hover:text-foreground")
                  }
                >
                  {t.label}
                </button>
              ))}
            </div>
            {visibleDashboards.length === 0 ? (
              <div className="flex flex-col items-center justify-center gap-2 rounded-xl border border-dashed border-border bg-surface/50 px-6 py-12 text-center">
                <p className="text-sm font-semibold text-ink">
                  Nothing shared yet
                </p>
                <p className="max-w-sm text-sm text-muted-foreground">
                  Dashboards shared with you — and dashboards you share
                  with teammates — show up here.
                </p>
              </div>
            ) : (
              <div className="flex flex-col gap-2">
                {visibleDashboards.map((d) => (
                  <DashboardRow
                    key={d.id}
                    dashboard={d}
                    onOpen={() =>
                      navigate(
                        `/w/${encodeURIComponent(slug)}/dashboards/${encodeURIComponent(d.id)}`,
                      )
                    }
                  />
                ))}
              </div>
            )}
          </>
        )}
      </div>

      <DashboardDialog
        open={createOpen}
        onOpenChange={(o) => {
          setCreateOpen(o);
          if (!o) setServerError(null);
        }}
        mode="create"
        pending={createMutation.isPending}
        serverError={serverError}
        onSubmit={(v) => createMutation.mutate(v)}
        slug={slug}
        templatePending={templateMutation.isPending}
        onCreateTemplate={(template, name) =>
          templateMutation.mutate({ template, name })
        }
      />
    </>
  );
}
