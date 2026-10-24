// DashboardDetail (C81T1): a dashboard's canvas page. Header with
// inline-rename, visibility toggle, delete with confirm, and "Add
// widget"; below it the DashboardCanvas. C83T1: affordances are gated
// by my_access — owner sees everything (incl. the Share button),
// edit access renames + edits widgets only, view access is fully
// read-only (no drag handles, no add/edit/delete affordances). A
// private dashboard of another creator 404s server-side and renders
// NotFound here.
import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronLeft, Pencil, Plus, Share2, Trash2 } from "lucide-react";
import { api, ApiError } from "../lib/api";
import { useAuth } from "../lib/auth";
import type { Workspace } from "../lib/types";
import {
  ACCESS_LABELS,
  canEditWidgets,
  canShareDashboard,
  createWidget,
  dashboardAccess,
  dashboardKey,
  dashboardsKey,
  deleteDashboard,
  deleteWidget,
  fetchDashboard,
  updateDashboard,
  updateWidget,
  type DashboardVisibility,
  type DashboardWidget,
} from "../lib/dashboards";
import { nextWidgetPosition } from "../lib/dashboardGrid";
import PageHeader from "../components/shell/PageHeader";
import { Button } from "../components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../components/ui/dialog";
import { Input } from "../components/ui/input";
import { NativeSelect } from "../components/ui/native-select";
import { Alert, AlertDescription } from "../components/ui/alert";
import { Skeleton } from "../components/ui/skeleton";
import DashboardCanvas from "../components/dashboards/DashboardCanvas";
import WidgetDialog, {
  type WidgetFormValue,
} from "../components/dashboards/WidgetDialog";
import DashboardDialog, {
  type DashboardFormValue,
} from "../components/dashboards/DashboardDialog";
import ShareDialog from "../components/dashboards/ShareDialog";
import { VisibilityBadge } from "./Dashboards";

/** Small confirm dialog for destructive dashboard/widget actions. */
function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel,
  pending,
  onConfirm,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description: string;
  confirmLabel: string;
  pending?: boolean;
  onConfirm: () => void;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={() => onOpenChange(false)}
          >
            Cancel
          </Button>
          <Button
            type="button"
            variant="destructive"
            disabled={pending}
            onClick={onConfirm}
          >
            {pending ? "Deleting…" : confirmLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export default function DashboardDetail() {
  const { slug = "", id = "" } = useParams<{ slug: string; id: string }>();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { user } = useAuth();

  const [renaming, setRenaming] = useState(false);
  const [nameDraft, setNameDraft] = useState("");
  const [headerError, setHeaderError] = useState<string | null>(null);
  const [widgetDialog, setWidgetDialog] = useState<{
    mode: "create" | "edit";
    widget: DashboardWidget | null;
  } | null>(null);
  const [widgetServerError, setWidgetServerError] = useState<string | null>(null);
  const [deletingWidget, setDeletingWidget] = useState<DashboardWidget | null>(null);
  const [deletingDashboard, setDeletingDashboard] = useState(false);
  const [editingDashboard, setEditingDashboard] = useState(false);
  const [sharing, setSharing] = useState(false);

  const dashboardQuery = useQuery({
    queryKey: dashboardKey(slug, id),
    queryFn: () => fetchDashboard(slug, id),
    retry: false,
  });
  const workspaceQuery = useQuery({
    queryKey: ["workspace", slug],
    queryFn: () =>
      api.get<Workspace>(`/api/v1/workspaces/${encodeURIComponent(slug)}`),
  });

  const dashboard = dashboardQuery.data;
  const role = workspaceQuery.data?.role ?? 0;
  // C83T1: my_access drives every affordance (the server enforces).
  // owner = everything; edit = rename + widget editing, but no Share,
  // delete, or visibility change; view = fully read-only.
  const access = dashboard
    ? dashboardAccess(dashboard, user?.id, role)
    : "view";
  const canEdit = !!dashboard && canEditWidgets(access);
  const isOwner = !!dashboard && canShareDashboard(access);

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: dashboardKey(slug, id) });
    void queryClient.invalidateQueries({ queryKey: dashboardsKey(slug) });
  };

  const renameMutation = useMutation({
    mutationFn: (name: string) => updateDashboard(slug, id, { name }),
    onSuccess: () => {
      setRenaming(false);
      setHeaderError(null);
      invalidate();
    },
    onError: (e) =>
      setHeaderError(e instanceof ApiError ? e.message : "Rename failed"),
  });

  const visibilityMutation = useMutation({
    mutationFn: (visibility: DashboardVisibility) =>
      updateDashboard(slug, id, { visibility }),
    onSuccess: () => {
      setHeaderError(null);
      invalidate();
    },
    onError: (e) =>
      setHeaderError(
        e instanceof ApiError ? e.message : "Visibility change failed",
      ),
  });

  const deleteMutation = useMutation({
    mutationFn: () => deleteDashboard(slug, id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: dashboardsKey(slug) });
      navigate(`/w/${encodeURIComponent(slug)}/dashboards`);
    },
    onError: (e) =>
      setHeaderError(e instanceof ApiError ? e.message : "Delete failed"),
  });

  const widgetMutation = useMutation({
    mutationFn: (v: { mode: "create" | "edit"; form: WidgetFormValue; widgetId?: string }) =>
      v.mode === "create"
        ? createWidget(slug, id, {
            title: v.form.title,
            widget_type: v.form.widget_type,
            config: v.form.config,
            ...nextWidgetPosition(dashboard?.widgets ?? []),
          })
        : updateWidget(v.widgetId!, {
            title: v.form.title,
            widget_type: v.form.widget_type,
            config: v.form.config,
          }),
    onSuccess: () => {
      setWidgetServerError(null);
      setWidgetDialog(null);
      invalidate();
    },
    onError: (e) =>
      setWidgetServerError(
        e instanceof ApiError ? e.message : "Failed to save widget",
      ),
  });

  const deleteWidgetMutation = useMutation({
    mutationFn: (widgetId: string) => deleteWidget(widgetId),
    onSuccess: () => {
      setDeletingWidget(null);
      invalidate();
    },
  });

  const dashboardFormMutation = useMutation({
    mutationFn: (v: DashboardFormValue) =>
      updateDashboard(slug, id, {
        name: v.name,
        description: v.description || null,
        visibility: v.visibility,
      }),
    onSuccess: () => {
      setEditingDashboard(false);
      invalidate();
    },
    onError: (e) =>
      setHeaderError(e instanceof ApiError ? e.message : "Save failed"),
  });

  const listHref = `/w/${encodeURIComponent(slug)}/dashboards`;

  return (
    <>
      <PageHeader
        viewName="Dashboards"
        addButton={
          canEdit
            ? {
                label: "Add widget",
                onClick: () =>
                  setWidgetDialog({ mode: "create", widget: null }),
              }
            : null
        }
      />
      <div className="mx-auto w-full max-w-[1200px] px-7 py-6">
        {dashboardQuery.isPending ? (
          <div className="flex flex-col gap-3">
            <Skeleton className="h-9 w-64" />
            <Skeleton className="h-[420px] w-full" />
          </div>
        ) : dashboardQuery.isError || !dashboard ? (
          <Alert variant="destructive">
            <AlertDescription>
              {dashboardQuery.error instanceof ApiError &&
              dashboardQuery.error.status === 404
                ? "Dashboard not found."
                : "Couldn't load this dashboard."}{" "}
              <Link to={listHref} className="underline">
                Back to dashboards
              </Link>
            </AlertDescription>
          </Alert>
        ) : (
          <>
            <div className="mb-5">
              <Link
                to={listHref}
                className="mb-2 inline-flex items-center gap-1 text-[13px] text-muted-foreground hover:text-foreground"
              >
                <ChevronLeft className="size-4" aria-hidden="true" />
                Dashboards
              </Link>
              <div className="flex flex-wrap items-center gap-2">
                {renaming ? (
                  <form
                    className="flex items-center gap-2"
                    onSubmit={(e) => {
                      e.preventDefault();
                      const name = nameDraft.trim();
                      if (name) renameMutation.mutate(name);
                    }}
                  >
                    <Input
                      value={nameDraft}
                      onChange={(e) => setNameDraft(e.target.value)}
                      autoFocus
                      className="h-9 w-72 text-lg font-semibold"
                      aria-label="Dashboard name"
                    />
                    <Button
                      type="submit"
                      size="sm"
                      disabled={renameMutation.isPending || !nameDraft.trim()}
                    >
                      Save
                    </Button>
                    <Button
                      type="button"
                      size="sm"
                      variant="outline"
                      onClick={() => setRenaming(false)}
                    >
                      Cancel
                    </Button>
                  </form>
                ) : (
                  <>
                    <h1 className="text-xl font-bold text-ink">
                      {dashboard.name}
                    </h1>
                    {canEdit && (
                      <button
                        type="button"
                        aria-label="Rename dashboard"
                        onClick={() => {
                          setNameDraft(dashboard.name);
                          setRenaming(true);
                        }}
                        className="flex size-7 items-center justify-center rounded text-muted-foreground hover:bg-accent hover:text-foreground"
                      >
                        <Pencil className="size-3.5" aria-hidden="true" />
                      </button>
                    )}
                  </>
                )}
                <VisibilityBadge visibility={dashboard.visibility} />
                {!isOwner && (
                  <span className="inline-flex items-center rounded-full border border-border px-2 py-0.5 text-[11px] font-medium text-muted-foreground">
                    {ACCESS_LABELS[access]} access
                  </span>
                )}
                {isOwner && (
                  <NativeSelect
                    aria-label="Dashboard visibility"
                    value={dashboard.visibility}
                    disabled={visibilityMutation.isPending}
                    onChange={(e) =>
                      visibilityMutation.mutate(
                        e.target.value as DashboardVisibility,
                      )
                    }
                    className="h-7 w-auto text-xs"
                  >
                    <option value="public">Public</option>
                    <option value="private">Private</option>
                  </NativeSelect>
                )}
                {(canEdit || isOwner) && (
                  <div className="ml-auto flex items-center gap-1">
                    {isOwner && (
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        onClick={() => setSharing(true)}
                      >
                        <Share2 className="size-4" aria-hidden="true" />
                        Share
                      </Button>
                    )}
                    {canEdit && (
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        onClick={() =>
                          setWidgetDialog({ mode: "create", widget: null })
                        }
                      >
                        <Plus className="size-4" aria-hidden="true" />
                        Add widget
                      </Button>
                    )}
                    {isOwner && (
                      <>
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          aria-label="Dashboard settings"
                          onClick={() => setEditingDashboard(true)}
                        >
                          <Pencil className="size-4" aria-hidden="true" />
                          Edit
                        </Button>
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          aria-label="Delete dashboard"
                          className="text-destructive hover:text-destructive"
                          onClick={() => setDeletingDashboard(true)}
                        >
                          <Trash2 className="size-4" aria-hidden="true" />
                        </Button>
                      </>
                    )}
                  </div>
                )}
              </div>
              {dashboard.description && (
                <p className="mt-1 text-sm text-muted-foreground">
                  {dashboard.description}
                </p>
              )}
              {headerError && (
                <Alert variant="destructive" className="mt-3">
                  <AlertDescription>{headerError}</AlertDescription>
                </Alert>
              )}
            </div>

            <DashboardCanvas
              slug={slug}
              dashboardId={id}
              widgets={dashboard.widgets ?? []}
              canEdit={canEdit}
              onEditWidget={(w) =>
                setWidgetDialog({ mode: "edit", widget: w })
              }
              onDeleteWidget={setDeletingWidget}
              onAddWidget={() =>
                setWidgetDialog({ mode: "create", widget: null })
              }
            />
          </>
        )}
      </div>

      {widgetDialog && (
        <WidgetDialog
          open
          onOpenChange={(o) => {
            if (!o) {
              setWidgetDialog(null);
              setWidgetServerError(null);
            }
          }}
          mode={widgetDialog.mode}
          slug={slug}
          initial={widgetDialog.widget}
          pending={widgetMutation.isPending}
          serverError={widgetServerError}
          onSubmit={(form) =>
            widgetMutation.mutate({
              mode: widgetDialog.mode,
              form,
              widgetId: widgetDialog.widget?.id,
            })
          }
        />
      )}

      <ConfirmDialog
        open={deletingWidget !== null}
        onOpenChange={(o) => {
          if (!o) setDeletingWidget(null);
        }}
        title="Delete widget?"
        description={`"${deletingWidget?.title ?? ""}" will be removed from this dashboard. This can't be undone.`}
        confirmLabel="Delete widget"
        pending={deleteWidgetMutation.isPending}
        onConfirm={() => {
          if (deletingWidget) deleteWidgetMutation.mutate(deletingWidget.id);
        }}
      />

      <ConfirmDialog
        open={deletingDashboard}
        onOpenChange={setDeletingDashboard}
        title="Delete dashboard?"
        description={`"${dashboard?.name ?? ""}" and all its widgets will be permanently deleted. This can't be undone.`}
        confirmLabel="Delete dashboard"
        pending={deleteMutation.isPending}
        onConfirm={() => deleteMutation.mutate()}
      />

      {dashboard && (
        <ShareDialog
          open={sharing}
          onOpenChange={setSharing}
          slug={slug}
          dashboardId={id}
          dashboardName={dashboard.name}
          ownerId={dashboard.created_by}
        />
      )}

      <DashboardDialog
        open={editingDashboard}
        onOpenChange={setEditingDashboard}
        mode="edit"
        initial={
          dashboard
            ? {
                name: dashboard.name,
                description: dashboard.description,
                visibility: dashboard.visibility,
              }
            : undefined
        }
        pending={dashboardFormMutation.isPending}
        serverError={headerError}
        onSubmit={(v) => dashboardFormMutation.mutate(v)}
      />
    </>
  );
}
