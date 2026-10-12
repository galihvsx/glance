import { useState } from "react";
import { useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CalendarClock, Loader2 } from "lucide-react";
import { api, ApiError } from "../lib/api";
import type {
  Issue,
  IssueState,
  Label as ProjectLabel,
  Member,
  Workspace,
} from "../lib/types";
import PageHeader from "../components/shell/PageHeader";
import { OrgEmpty } from "../components/org/OrgViews";
import { Skeleton } from "../components/ui/skeleton";
import { Alert, AlertDescription } from "../components/ui/alert";
import { Button } from "../components/ui/button";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "../components/ui/alert-dialog";
import ScheduleCard from "../components/schedules/ScheduleCard";
import ScheduleDialog from "../components/schedules/ScheduleDialog";
import {
  buildCreateBody,
  buildPatchBody,
  type RecurringSchedule,
  type ScheduleFormValues,
} from "../lib/schedules";

interface RunNowResult {
  issue: Issue;
  schedule: RecurringSchedule;
}

export default function Schedules() {
  const { slug = "", identifier = "" } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const queryClient = useQueryClient();
  const encSlug = encodeURIComponent(slug);
  const encIdentifier = encodeURIComponent(identifier);
  const base = `/api/v1/workspaces/${encSlug}/projects/${encIdentifier}`;
  const schedBase = `${base}/schedules`;
  const schedulesKey = ["schedules", slug, identifier] as const;

  const [dialogOpen, setDialogOpen] = useState(false);
  const [editing, setEditing] = useState<RecurringSchedule | null>(null);
  const [deleting, setDeleting] = useState<RecurringSchedule | null>(null);
  const [notice, setNotice] = useState<{
    kind: "ok" | "err";
    text: string;
  } | null>(null);
  const [togglingId, setTogglingId] = useState<string | null>(null);
  const [runningId, setRunningId] = useState<string | null>(null);
  const [dialogError, setDialogError] = useState<string | null>(null);

  const workspaceQuery = useQuery({
    queryKey: ["workspace", slug],
    queryFn: () => api.get<Workspace>(`/api/v1/workspaces/${encSlug}`),
  });
  const canEdit = (workspaceQuery.data?.role ?? 0) >= 15;

  const schedulesQuery = useQuery({
    queryKey: schedulesKey,
    queryFn: () => api.get<RecurringSchedule[]>(schedBase),
  });

  // Picker data is only needed while the builder is open.
  const statesQuery = useQuery({
    queryKey: ["states", slug, identifier],
    queryFn: () =>
      api.get<{ states: IssueState[] }>(`${base}/states`).then((d) => d.states),
    enabled: dialogOpen,
  });
  const labelsQuery = useQuery({
    queryKey: ["labels", slug, identifier],
    queryFn: () =>
      api.get<{ labels: ProjectLabel[] }>(`${base}/labels`).then((d) => d.labels),
    enabled: dialogOpen,
  });
  const membersQuery = useQuery({
    queryKey: ["members", slug],
    queryFn: () =>
      api
        .get<{ members: Member[] }>(`/api/v1/workspaces/${encSlug}/members`)
        .then((d) => d.members),
    enabled: dialogOpen,
  });

  const invalidate = () =>
    void queryClient.invalidateQueries({ queryKey: schedulesKey });

  const errMsg = (e: unknown, fallback: string) =>
    e instanceof ApiError ? e.message : fallback;

  const createMutation = useMutation({
    mutationFn: (v: ScheduleFormValues) =>
      api.post<RecurringSchedule>(schedBase, buildCreateBody(v)),
    onSuccess: () => {
      setDialogOpen(false);
      setEditing(null);
      invalidate();
      setNotice({ kind: "ok", text: "Recurring rule created." });
    },
    onError: (e) =>
      setDialogError(errMsg(e, "Failed to create the rule.")),
  });

  const updateMutation = useMutation({
    mutationFn: ({ id, v }: { id: string; v: ScheduleFormValues }) => {
      const schedule = (schedulesQuery.data ?? []).find((s) => s.id === id);
      if (!schedule) throw new Error("Schedule no longer in the list.");
      return api.patch<RecurringSchedule>(
        `${schedBase}/${encodeURIComponent(id)}`,
        buildPatchBody(schedule, v),
      );
    },
    onSuccess: () => {
      setDialogOpen(false);
      setEditing(null);
      invalidate();
      setNotice({ kind: "ok", text: "Recurring rule updated." });
    },
    onError: (e) =>
      setDialogError(errMsg(e, "Failed to update the rule.")),
  });

  const toggleActive = (s: RecurringSchedule) => {
    setTogglingId(s.id);
    api
      .patch<RecurringSchedule>(`${schedBase}/${encodeURIComponent(s.id)}`, {
        active: !s.active,
      })
      .then(() => {
        invalidate();
        setNotice({
          kind: "ok",
          text: s.active ? "Rule paused." : "Rule resumed.",
        });
      })
      .catch((e: unknown) =>
        setNotice({
          kind: "err",
          text: errMsg(e, "Failed to update the rule."),
        }),
      )
      .finally(() => setTogglingId(null));
  };

  const deleteMutation = useMutation({
    mutationFn: (id: string) =>
      api.del(`${schedBase}/${encodeURIComponent(id)}`),
    onSuccess: () => {
      setDeleting(null);
      invalidate();
      setNotice({ kind: "ok", text: "Recurring rule deleted." });
    },
    onError: (e) =>
      setNotice({ kind: "err", text: errMsg(e, "Failed to delete the rule.") }),
  });

  const runNow = (s: RecurringSchedule) => {
    setRunningId(s.id);
    api
      .post<RunNowResult>(`${schedBase}/${encodeURIComponent(s.id)}/run-now`)
      .then(() => {
        invalidate();
        setNotice({ kind: "ok", text: "Created the next issue now." });
      })
      .catch((e: unknown) => {
        if (e instanceof ApiError && e.status === 409) {
          invalidate();
          setNotice({
            kind: "ok",
            text: "The scheduler claimed this occurrence — schedule reloaded.",
          });
        } else {
          setNotice({
            kind: "err",
            text: errMsg(e, "Failed to run the rule now."),
          });
        }
      })
      .finally(() => setRunningId(null));
  };

  const schedules = schedulesQuery.data ?? [];
  const openCreate = () => {
    setEditing(null);
    setNotice(null);
    setDialogError(null);
    setDialogOpen(true);
  };
  const openEdit = (s: RecurringSchedule) => {
    setEditing(s);
    setNotice(null);
    setDialogError(null);
    setDialogOpen(true);
  };
  const today = new Date().toISOString().slice(0, 10);

  return (
    <>
      <PageHeader
        viewName="Schedules"
        count={schedules.length}
        addButton={
          canEdit ? { label: "New rule", onClick: openCreate } : undefined
        }
      />
      <div className="mx-auto w-full max-w-[1060px] space-y-4 px-7 py-6">
        {!canEdit && (
          <p className="text-[11px] text-ink-muted">
            You have guest access to this workspace — these rules are
            read-only.
          </p>
        )}

        {notice && (
          <Alert variant={notice.kind === "err" ? "destructive" : undefined}>
            <AlertDescription>{notice.text}</AlertDescription>
          </Alert>
        )}

        {schedulesQuery.isLoading ? (
          <div className="space-y-3">
            {[0, 1].map((i) => (
              <Skeleton key={i} className="h-[132px] w-full rounded-[8px]" />
            ))}
          </div>
        ) : schedulesQuery.isError ? (
          <Alert variant="destructive">
            <AlertDescription>
              {errMsg(schedulesQuery.error, "Failed to load the rules.")}
            </AlertDescription>
          </Alert>
        ) : schedules.length === 0 ? (
          <OrgEmpty
            title="No recurring rules yet"
            icon={<CalendarClock className="size-4" />}
          >
            Automate standups, reports, and chores — a rule creates an issue
            on its cadence, from a template.
            {canEdit && (
              <span className="mt-3 block">
                <Button size="sm" onClick={openCreate}>
                  Create a rule
                </Button>
              </span>
            )}
          </OrgEmpty>
        ) : (
          schedules.map((s) => (
            <ScheduleCard
              key={s.id}
              schedule={s}
              canEdit={canEdit}
              toggling={togglingId === s.id}
              runningNow={runningId === s.id}
              onToggle={() => toggleActive(s)}
              onRunNow={() => runNow(s)}
              onEdit={() => openEdit(s)}
              onDelete={() => setDeleting(s)}
            />
          ))
        )}
      </div>

      <ScheduleDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        mode={editing ? "edit" : "create"}
        initial={editing}
        states={statesQuery.data ?? []}
        labels={labelsQuery.data ?? null}
        members={membersQuery.data ?? null}
        today={today}
        saving={createMutation.isPending || updateMutation.isPending}
        error={dialogError}
        onSubmit={(v) => {
          setNotice(null);
          setDialogError(null);
          if (editing) updateMutation.mutate({ id: editing.id, v });
          else createMutation.mutate(v);
        }}
      />

      <AlertDialog open={deleting !== null} onOpenChange={(o) => !o && setDeleting(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete this rule?</AlertDialogTitle>
            <AlertDialogDescription>
              {deleting
                ? `“${deleting.name}” stops creating issues. Already-created issues are untouched.`
                : ""}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => deleting && deleteMutation.mutate(deleting.id)}
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            >
              {deleteMutation.isPending ? (
                <Loader2 className="size-4 animate-spin" />
              ) : (
                "Delete rule"
              )}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}
