// GoalDetail (C35T1): objective detail page. Header (title, status
// badge + changer, description, edit, delete), objective progress, and
// the key-result list: per-KR progress bar, metric KR "update current
// value" inline affordance, issue KR link-issue picker + unlink, add-KR
// form, KR edit (kind is immutable — no kind control on edit), KR
// delete. Guests get a read-only view; all writes are member (15)+
// gated, matching the backend contract. Deletes use window.confirm
// (the established pattern from Attachments / IssueLinks / SubIssues).
import { useMemo, useState, type FormEvent } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link2, Pencil, Plus, Trash2, X } from "lucide-react";
import { api, ApiError } from "../lib/api";
import { formatDateTime } from "../lib/format";
import { toast } from "../components/ui/toast";
import type {
  Issue,
  IssueListResult,
  KeyResult,
  KeyResultCheckin,
  KeyResultKind,
  Objective,
  ObjectiveStatus,
  Project,
  Workspace,
  WorkspaceMember,
} from "../lib/types";
import GoalProgressBar from "../components/goals/GoalProgressBar";
import {
  formatKRValue,
  formatProgressPct,
  objectiveStatusColor,
} from "../components/goals/okrFormat";
import {
  OrgDetailHeader,
  OrgEmpty,
  OrgHeaderButton,
  OrgSectionHead,
  OrgStatusChip,
} from "../components/org/OrgViews";
import { Card, CardContent } from "../components/ui/card";
import { Button } from "../components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "../components/ui/dropdown-menu";
import { Input } from "../components/ui/input";
import { Label } from "../components/ui/label";
import { Skeleton } from "../components/ui/skeleton";
import { Textarea } from "../components/ui/textarea";
import { Alert, AlertDescription } from "../components/ui/alert";
import { Checkbox } from "../components/ui/checkbox";

const STATUSES: ObjectiveStatus[] = ["active", "completed", "archived"];

function statusLabel(status: ObjectiveStatus): string {
  return status.charAt(0).toUpperCase() + status.slice(1);
}

function friendlyError(err: unknown, fallback: string): string {
  if (err instanceof ApiError) {
    if (err.status === 403)
      return "You need member access or higher to do that.";
    if (err.status === 409)
      return "An objective with that title already exists in this workspace.";
    if (err.body) return err.body;
  }
  return fallback;
}

/** Done-group state groups (same semantics as the initiative rollup). */
const DONE_GROUPS = new Set(["completed", "cancelled"]);

/** Inline current-value editor for a metric KR (prototype language:
 *  "Current value [ input ] Update · start → target"), extended with
 *  the optional check-in note. The note is passed through to the
 *  caller as-is; the caller trims it and omits the checkin_note key
 *  when it is empty. */
function MetricCurrentUpdate({
  kr,
  onUpdate,
  pending,
}: {
  kr: KeyResult;
  onUpdate: (currentValue: number, note: string) => void;
  pending: boolean;
}) {
  const [value, setValue] = useState(formatKRValue(kr.current_value));
  const [note, setNote] = useState("");
  return (
    <form
      className="mt-2 flex flex-wrap items-center gap-2"
      onSubmit={(e) => {
        e.preventDefault();
        const parsed = Number(value);
        if (Number.isFinite(parsed)) onUpdate(parsed, note);
      }}
    >
      <Label htmlFor={`kr-current-${kr.id}`} className="text-[11px]">
        Current value
      </Label>
      <Input
        id={`kr-current-${kr.id}`}
        type="number"
        step="any"
        value={value}
        onChange={(e) => setValue(e.target.value)}
        className="h-7 w-28 text-[12px]"
      />
      <Label htmlFor={`kr-note-${kr.id}`} className="text-[11px]">
        Note (optional)
      </Label>
      <Input
        id={`kr-note-${kr.id}`}
        type="text"
        value={note}
        onChange={(e) => setNote(e.target.value)}
        placeholder="Why this changed"
        maxLength={1000}
        className="h-7 min-w-40 flex-1 text-[12px]"
      />
      <Button
        type="submit"
        size="sm"
        variant="outline"
        disabled={pending || Number(value) === kr.current_value}
      >
        {pending ? "Updating…" : "Update"}
      </Button>
      <span className="w-full text-[11px] text-ink-muted">
        start {formatKRValue(kr.start_value)} → target{" "}
        {formatKRValue(kr.target_value)}
        {kr.unit ? ` ${kr.unit}` : ""} · The note is attached to this
        check-in only. Leave it empty to record just the value.
      </span>
    </form>
  );
}

/** Short axis date for the progress chart: "14 Oct 2026". Falls back
 *  to the raw string when it doesn't parse (never blank). */
function formatChartDate(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const month = d.toLocaleString("en-GB", { month: "short" });
  return `${d.getDate()} ${month} ${d.getFullYear()}`;
}

/** Progress-over-time chart for one metric KR (C42T0): pure SVG on
 *  top of the SAME check-ins query the timeline uses — no new fetch,
 *  no new dependencies. Points are every check-in's new_value in
 *  chronological order (the query returns newest-first, so reverse);
 *  the seed row is the first point. Dashed reference lines mark
 *  start_value + target_value from the KR object. Guards: zero time
 *  range (backfilled/imported rows can share one timestamp) spreads
 *  points evenly by index, never dividing by zero; zero value range
 *  renders a flat midline; descending KRs (target < start) fall out
 *  of the domain; a single point renders a dot + reference lines;
 *  empty renders nothing (the caller only mounts this when check-ins
 *  exist). Colors are existing ink/accent tokens only. */
function KRProgressChart({
  kr,
  checkins,
}: {
  kr: KeyResult;
  checkins: KeyResultCheckin[];
}) {
  if (checkins.length === 0) return null;
  const points = [...checkins].reverse();
  const values = points.map((ci) => ci.new_value);
  const times = points.map((ci) => Date.parse(ci.created_at));
  const allTimesFinite = times.every((t) => Number.isFinite(t));

  const W = 320;
  const H = 120;
  const LEFT = 44;
  const RIGHT = 8;
  const TOP = 8;
  const BOTTOM = 22;
  const plotW = W - LEFT - RIGHT;
  const plotH = H - TOP - BOTTOM;

  const tMin = Math.min(...times);
  const tMax = Math.max(...times);
  const timeRange = allTimesFinite ? tMax - tMin : 0;

  const lo = Math.min(...values, kr.start_value, kr.target_value);
  const hi = Math.max(...values, kr.start_value, kr.target_value);
  const valueRange = hi - lo;

  const x = (i: number) => {
    if (timeRange > 0) {
      return LEFT + ((times[i] - tMin) / timeRange) * plotW;
    }
    return points.length === 1
      ? LEFT + plotW / 2
      : LEFT + (i / (points.length - 1)) * plotW;
  };
  const y = (v: number) =>
    valueRange > 0 ? TOP + ((hi - v) / valueRange) * plotH : TOP + plotH / 2;

  const path = points
    .map(
      (_, i) =>
        `${i === 0 ? "M" : "L"}${x(i).toFixed(1)},${y(values[i]).toFixed(1)}`,
    )
    .join(" ");
  const first = values[0];
  const last = values[values.length - 1];
  const label = `Progress over time: ${formatKRValue(first)} → ${formatKRValue(last)}`;

  // Reference-line label sits just above its line, or just below
  // when the line is at the top edge (so it never clips).
  const refLabelY = (v: number) => {
    const lineY = y(v);
    return lineY - 4 >= TOP + 2 ? lineY - 4 : lineY + 12;
  };

  return (
    <div className="mt-2" data-testid="kr-progress-chart">
      <svg
        viewBox={`0 0 ${W} ${H}`}
        className="h-auto w-full"
        role="img"
        aria-label={label}
      >
        <title>{label}</title>
        <text
          x={LEFT - 6}
          y={TOP + 3}
          textAnchor="end"
          fontSize={11}
          className="fill-ink-muted tabular-nums"
        >
          {formatKRValue(hi)}
        </text>
        <text
          x={LEFT - 6}
          y={TOP + plotH + 3}
          textAnchor="end"
          fontSize={11}
          className="fill-ink-muted tabular-nums"
        >
          {formatKRValue(lo)}
        </text>
        <line
          data-testid="kr-chart-ref-start"
          x1={LEFT}
          x2={W - RIGHT}
          y1={y(kr.start_value)}
          y2={y(kr.start_value)}
          className="stroke-ink-muted"
          strokeWidth={1}
          strokeDasharray="4 3"
        />
        <text
          x={W - RIGHT}
          y={refLabelY(kr.start_value)}
          textAnchor="end"
          fontSize={11}
          className="fill-ink-muted tabular-nums"
        >
          Start {formatKRValue(kr.start_value)}
        </text>
        <line
          data-testid="kr-chart-ref-target"
          x1={LEFT}
          x2={W - RIGHT}
          y1={y(kr.target_value)}
          y2={y(kr.target_value)}
          className="stroke-ink-muted"
          strokeWidth={1}
          strokeDasharray="4 3"
        />
        <text
          x={W - RIGHT}
          y={refLabelY(kr.target_value)}
          textAnchor="end"
          fontSize={11}
          className="fill-ink-muted tabular-nums"
        >
          Target {formatKRValue(kr.target_value)}
        </text>
        {points.length > 1 ? (
          <path
            data-testid="kr-chart-line"
            d={path}
            fill="none"
            className="stroke-accent-blue"
            strokeWidth={2}
          />
        ) : null}
        {points.map((ci, i) => (
          <circle
            key={ci.id}
            data-testid="kr-chart-point"
            cx={x(i)}
            cy={y(values[i])}
            r={3}
            className="fill-accent-blue"
          />
        ))}
        <text
          x={LEFT}
          y={H - 6}
          textAnchor="start"
          fontSize={11}
          className="fill-ink-muted tabular-nums"
        >
          {formatChartDate(points[0].created_at)}
        </text>
        {points.length > 1 ? (
          <text
            x={W - RIGHT}
            y={H - 6}
            textAnchor="end"
            fontSize={11}
            className="fill-ink-muted tabular-nums"
          >
            {formatChartDate(points[points.length - 1].created_at)}
          </text>
        ) : null}
      </svg>
    </div>
  );
}

/** Check-in history timeline for one metric KR (newest first, as
 *  returned by the API). Fetched only for metric KRs — the parent
 *  never mounts this for issue KRs, so no fetch fires for them.
 *  The list endpoint is member (15)+, so the parent only mounts
 *  this for members; guests see no history section. */
function KRCheckinHistory({
  slug,
  objectiveId,
  kr,
}: {
  slug: string;
  objectiveId: string;
  kr: KeyResult;
}) {
  const checkinsQuery = useQuery({
    queryKey: ["kr-checkins", slug, objectiveId, kr.id],
    queryFn: () =>
      api
        .get<{ check_ins: KeyResultCheckin[] }>(
          `/api/v1/workspaces/${encodeURIComponent(slug)}/objectives/${encodeURIComponent(objectiveId)}/key-results/${encodeURIComponent(kr.id)}/check-ins`,
        )
        .then((d) => d.check_ins),
  });

  return (
    <div className="mt-3">
      <h4 className="text-[11px] font-semibold uppercase tracking-wide text-ink-muted">
        Check-in history
      </h4>
      {checkinsQuery.status === "pending" ? (
        <p className="mt-2 text-[12px] text-ink-muted">
          Loading check-in history…
        </p>
      ) : checkinsQuery.status === "error" ? (
        <p className="mt-2 text-[12px] text-accent-red">
          Couldn&apos;t load check-in history.
        </p>
      ) : checkinsQuery.data.length === 0 ? (
        <p className="mt-2 text-[12px] text-ink-muted">No check-ins yet.</p>
      ) : (
        <>
          <KRProgressChart kr={kr} checkins={checkinsQuery.data} />
          <ul className="mt-2 space-y-2 border-l-2 border-raised pl-3">
          {checkinsQuery.data.map((ci) => {
            // The seed entry (previous_value null) renders as
            // "started at {value}", never as a delta. No-op updates
            // record nothing, so a ±0 row never appears.
            const isSeed = ci.previous_value === null;
            const delta = isSeed
              ? null
              : ci.new_value - (ci.previous_value as number);
            return (
              <li key={ci.id} data-testid="kr-checkin-row">
                <p className="text-[12px] text-ink tabular-nums">
                  {isSeed ? (
                    <span>started at {formatKRValue(ci.new_value)}</span>
                  ) : (
                    <>
                      <span>
                        {formatKRValue(ci.previous_value as number)} →{" "}
                        {formatKRValue(ci.new_value)}
                      </span>{" "}
                      <span className="font-semibold tabular-nums">
                        {delta !== null && delta > 0
                          ? `+${formatKRValue(delta)}`
                          : delta !== null
                            ? `−${formatKRValue(-delta)}`
                            : ""}
                      </span>
                    </>
                  )}
                </p>
                {ci.note ? (
                  <p className="mt-0.5 text-[12px] text-ink-secondary">
                    {ci.note}
                  </p>
                ) : null}
                <p
                  className="mt-0.5 text-[11px] text-ink-muted"
                  title={ci.created_at}
                >
                  {ci.author?.display_name ?? "Deleted user"} ·{" "}
                  {formatDateTime(ci.created_at)}
                </p>
              </li>
            );
          })}
          </ul>
        </>
      )}
    </div>
  );
}

export default function GoalDetail() {
  const { slug = "", id = "" } = useParams();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const encSlug = encodeURIComponent(slug);
  const base = `/api/v1/workspaces/${encSlug}/objectives/${encodeURIComponent(id)}`;

  const [editOpen, setEditOpen] = useState(false);
  const [addKROpen, setAddKROpen] = useState(false);
  const [editKR, setEditKR] = useState<KeyResult | null>(null);
  const [linkKR, setLinkKR] = useState<KeyResult | null>(null);
  const [linkProject, setLinkProject] = useState("");
  const [linkPicked, setLinkPicked] = useState<Set<string>>(new Set());
  const [krFormError, setKrFormError] = useState<string | null>(null);

  const workspaceQuery = useQuery({
    queryKey: ["workspace", slug],
    queryFn: () => api.get<Workspace>(`/api/v1/workspaces/${encSlug}`),
  });
  const canEdit = (workspaceQuery.data?.role ?? 0) >= 15;

  const objectiveQuery = useQuery({
    queryKey: ["objective", slug, id],
    queryFn: () => api.get<Objective>(base),
  });

  // Owner picker for the edit dialog, loaded only when it opens.
  const membersQuery = useQuery({
    queryKey: ["members", slug],
    enabled: editOpen,
    queryFn: () =>
      api
        .get<{ members: WorkspaceMember[] }>(
          `/api/v1/workspaces/${encSlug}/members`,
        )
        .then((d) => d.members),
  });

  // Projects + per-project issues for the link-issue picker, loaded
  // only when it opens (Cycles add-issues pattern).
  const projectsQuery = useQuery({
    queryKey: ["projects", slug],
    enabled: linkKR !== null,
    queryFn: () =>
      api
        .get<{ projects: Project[] }>(
          `/api/v1/workspaces/${encSlug}/projects`,
        )
        .then((d) => d.projects),
  });
  const linkIssuesQuery = useQuery({
    queryKey: ["project-issues", slug, linkProject],
    enabled: linkKR !== null && linkProject !== "",
    queryFn: async () => {
      const all: Issue[] = [];
      let cursor: string | undefined;
      do {
        const p = new URLSearchParams({
          order_by: "sort_order",
          per_page: "100",
        });
        if (cursor) p.set("cursor", cursor);
        const page = await api.get<IssueListResult>(
          `/api/v1/workspaces/${encSlug}/projects/${encodeURIComponent(linkProject)}/issues?${p.toString()}`,
        );
        all.push(...page.results);
        cursor = page.next_cursor;
      } while (cursor);
      return all;
    },
  });

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: ["objective", slug, id] });
    void queryClient.invalidateQueries({ queryKey: ["objectives", slug] });
    void queryClient.invalidateQueries({
      queryKey: ["kr-checkins", slug, id],
    });
  };

  const updateMutation = useMutation({
    mutationFn: (patch: {
      title?: string;
      description?: string;
      owner_id?: string;
      start_date?: string;
      target_date?: string;
      status?: ObjectiveStatus;
    }) => api.patch<Objective>(base, patch),
    onSuccess: () => {
      invalidate();
      setEditOpen(false);
      toast.add({ title: "Objective updated", type: "success" });
    },
    onError: (err) =>
      toast.add({
        title: "Couldn't update the objective",
        description: friendlyError(err, "Please try again."),
        type: "error",
      }),
  });

  const deleteMutation = useMutation({
    mutationFn: () => api.del(base),
    onSuccess: () => {
      toast.add({ title: "Objective deleted", type: "success" });
      void queryClient.invalidateQueries({
        queryKey: ["objectives", slug],
      });
      navigate(`/w/${slug}/goals`);
    },
    onError: (err) =>
      toast.add({
        title: "Couldn't delete the objective",
        description: friendlyError(err, "Please try again."),
        type: "error",
      }),
  });

  const createKRMutation = useMutation({
    mutationFn: (body: {
      title: string;
      kind: KeyResultKind;
      unit?: string;
      start_value?: number;
      target_value: number;
      current_value?: number;
    }) => api.post<KeyResult>(`${base}/key-results`, body),
    onSuccess: () => {
      invalidate();
      setAddKROpen(false);
      setKrFormError(null);
      toast.add({ title: "Key result added", type: "success" });
    },
    onError: (err) => {
      // The add-KR form keeps its error inline (like the Initiatives
      // create form) so the 400 (zero-span target, metric-only field
      // on an issue KR) stays visible next to the fields.
      setKrFormError(friendlyError(err, "Couldn't add the key result."));
    },
  });

  const updateKRMutation = useMutation({
    mutationFn: (v: {
      krId: string;
      patch: {
        title?: string;
        unit?: string;
        target_value?: number;
        current_value?: number;
        checkin_note?: string;
      };
    }) => api.patch<KeyResult>(`${base}/key-results/${v.krId}`, v.patch),
    onSuccess: () => {
      invalidate();
      setEditKR(null);
      toast.add({ title: "Key result updated", type: "success" });
    },
    onError: (err) =>
      toast.add({
        title: "Couldn't update the key result",
        description: friendlyError(err, "Please try again."),
        type: "error",
      }),
  });

  const deleteKRMutation = useMutation({
    mutationFn: (krId: string) =>
      api.del(`${base}/key-results/${encodeURIComponent(krId)}`),
    onSuccess: () => {
      invalidate();
      toast.add({ title: "Key result deleted", type: "success" });
    },
    onError: (err) =>
      toast.add({
        title: "Couldn't delete the key result",
        description: friendlyError(err, "Please try again."),
        type: "error",
      }),
  });

  const linkIssueMutation = useMutation({
    mutationFn: async (v: { krId: string; issueIds: string[] }) => {
      // Link calls are idempotent server-side (ON CONFLICT DO NOT
      // NOTHING), so repeats are no-ops.
      await Promise.all(
        v.issueIds.map((issueId) =>
          api.post(`${base}/key-results/${v.krId}/issues`, {
            issue_id: issueId,
          }),
        ),
      );
    },
    onSuccess: (_, v) => {
      invalidate();
      setLinkKR(null);
      setLinkProject("");
      setLinkPicked(new Set());
      toast.add({
        title:
          v.issueIds.length === 1
            ? "Issue linked to the key result"
            : `${v.issueIds.length} issues linked to the key result`,
        type: "success",
      });
    },
    onError: (err) =>
      toast.add({
        title: "Couldn't link issues",
        description: friendlyError(err, "Please try again."),
        type: "error",
      }),
  });

  const unlinkIssueMutation = useMutation({
    mutationFn: (v: { krId: string; issueId: string }) =>
      api.del(
        `${base}/key-results/${v.krId}/issues/${encodeURIComponent(v.issueId)}`,
      ),
    onSuccess: () => {
      invalidate();
      toast.add({ title: "Issue unlinked", type: "success" });
    },
    onError: (err) =>
      toast.add({
        title: "Couldn't unlink the issue",
        description: friendlyError(err, "Please try again."),
        type: "error",
      }),
  });

  const objective = objectiveQuery.data;
  const krs = objective?.key_results ?? [];

  const linkedIds = useMemo(
    () =>
      new Set(
        (linkKR?.linked_issues ?? []).map((i) => i.id),
      ),
    [linkKR],
  );
  const linkableIssues = useMemo(
    () => (linkIssuesQuery.data ?? []).filter((i) => !linkedIds.has(i.id)),
    [linkIssuesQuery.data, linkedIds],
  );

  function onDeleteObjective() {
    if (!objective) return;
    if (
      window.confirm(
        `Delete objective "${objective.title}"? Its key results are removed too. Linked issues are untouched.`,
      )
    ) {
      deleteMutation.mutate();
    }
  }

  function onDeleteKR(kr: KeyResult) {
    if (
      window.confirm(
        `Delete key result "${kr.title}"? Its issue links are removed too.`,
      )
    ) {
      deleteKRMutation.mutate(kr.id);
    }
  }

  function onUnlink(kr: KeyResult, issueId: string, displayId: string) {
    if (
      window.confirm(
        `Unlink ${displayId} from "${kr.title}"? Progress will be recomputed.`,
      )
    ) {
      unlinkIssueMutation.mutate({ krId: kr.id, issueId });
    }
  }

  if (objectiveQuery.status === "pending") {
    return (
      <div className="mx-auto w-full max-w-5xl space-y-4 p-4">
        <Skeleton className="h-8 w-64" />
        <Skeleton className="h-24 w-full" />
        <Skeleton className="h-40 w-full" />
      </div>
    );
  }
  if (objectiveQuery.status === "error" || !objective) {
    return (
      <div className="mx-auto w-full max-w-5xl p-4">
        <OrgEmpty title="Objective not found">
          It may have been deleted, or you may not have access to this
          workspace.
        </OrgEmpty>
      </div>
    );
  }

  const metaParts = [
    `Owner: ${objective.owner?.name ?? objective.owner?.email ?? "—"}`,
    objective.start_date || objective.target_date
      ? `${objective.start_date ?? "…"} → ${objective.target_date ?? "…"}`
      : null,
    `Objective progress ${formatProgressPct(objective.progress)}`,
  ].filter(Boolean);

  return (
    <div className="mx-auto w-full max-w-5xl space-y-6 p-4">
      <OrgDetailHeader
        backLabel="Goals"
        onBack={() => navigate(`/w/${slug}/goals`)}
        title={objective.title}
        chip={
          canEdit ? (
            <DropdownMenu>
              <DropdownMenuTrigger
                className="rounded-full transition-opacity hover:opacity-80"
                aria-label="Change status"
              >
                <OrgStatusChip
                  color={objectiveStatusColor(objective.status)}
                  label={statusLabel(objective.status)}
                />
              </DropdownMenuTrigger>
              <DropdownMenuContent align="start">
                {STATUSES.map((s) => (
                  <DropdownMenuItem
                    key={s}
                    disabled={
                      s === objective.status || updateMutation.isPending
                    }
                    onClick={() => updateMutation.mutate({ status: s })}
                  >
                    <span
                      className="size-2 rounded-full"
                      style={{ backgroundColor: objectiveStatusColor(s) }}
                      aria-hidden
                    />
                    {statusLabel(s)}
                  </DropdownMenuItem>
                ))}
              </DropdownMenuContent>
            </DropdownMenu>
          ) : (
            <OrgStatusChip
              color={objectiveStatusColor(objective.status)}
              label={statusLabel(objective.status)}
            />
          )
        }
        meta={metaParts.join(" · ")}
        actions={
          canEdit ? (
            <>
              <OrgHeaderButton
                onClick={() => setEditOpen(true)}
                title="Edit objective"
              >
                <Pencil className="h-3 w-3" aria-hidden />
                Edit objective
              </OrgHeaderButton>
              <OrgHeaderButton
                onClick={onDeleteObjective}
                title="Delete objective"
                destructive
              >
                <Trash2 className="h-3 w-3" aria-hidden />
                Delete objective
              </OrgHeaderButton>
            </>
          ) : undefined
        }
      />

      {objective.description ? (
        <p className="text-[13px] text-ink-secondary">
          {objective.description}
        </p>
      ) : (
        <p className="text-[13px] text-ink-muted italic">No description yet.</p>
      )}

      {!canEdit && (
        <p className="text-[11px] text-ink-muted">
          You have guest access to this workspace — this objective is
          read-only.
        </p>
      )}

      <section aria-label="Overall progress">
        <OrgSectionHead title="Progress" />
        <Card className="mt-3">
          <CardContent className="p-4">
            <GoalProgressBar
              progress={objective.progress}
              caption="Mean of key-result progress"
            />
          </CardContent>
        </Card>
      </section>

      {/* Key results */}
      <section aria-label="Key results">
        <OrgSectionHead
          title="Key results"
          count={krs.length}
          actions={
            canEdit ? (
              <OrgHeaderButton
                onClick={() => {
                  setKrFormError(null);
                  setAddKROpen(true);
                }}
                title="Add key result"
              >
                <Plus className="h-3 w-3" aria-hidden />
                Add key result
              </OrgHeaderButton>
            ) : undefined
          }
        />
        {krs.length === 0 ? (
          <div className="mt-3">
            <OrgEmpty title="No key results yet">
              Add your first key result to start tracking progress — progress
              stays at 0% until then.
            </OrgEmpty>
            {canEdit && (
              <div className="mt-4 flex justify-center">
                <Button
                  onClick={() => {
                    setKrFormError(null);
                    setAddKROpen(true);
                  }}
                >
                  Add your first key result
                </Button>
              </div>
            )}
          </div>
        ) : (
          <ul className="mt-3 space-y-2">
            {krs.map((kr) => (
              <li key={kr.id} data-testid="goal-kr-row">
                <Card>
                  <CardContent className="p-3.5">
                    <div className="flex items-center gap-2">
                      <h3 className="text-[13px] font-semibold text-ink">
                        {kr.title}
                      </h3>
                      <span className="text-[11px] uppercase text-ink-muted">
                        {kr.kind === "metric" ? "Metric" : "Issue-linked"}
                      </span>
                      {canEdit && (
                        <span className="ml-auto flex items-center gap-1">
                          <button
                            type="button"
                            aria-label={`Edit key result ${kr.title}`}
                            title="Edit key result"
                            onClick={() => setEditKR(kr)}
                            className="rounded p-1 text-ink-muted transition-colors hover:bg-raised hover:text-ink"
                          >
                            <Pencil className="h-3.5 w-3.5" aria-hidden />
                          </button>
                          <button
                            type="button"
                            aria-label={`Delete key result ${kr.title}`}
                            title="Delete key result"
                            onClick={() => onDeleteKR(kr)}
                            className="rounded p-1 text-ink-muted transition-colors hover:bg-raised hover:text-accent-red"
                          >
                            <Trash2 className="h-3.5 w-3.5" aria-hidden />
                          </button>
                        </span>
                      )}
                    </div>
                    <div className="mt-2">
                      <GoalProgressBar
                        progress={kr.progress}
                        caption={
                          kr.kind === "metric"
                            ? `${formatKRValue(kr.current_value)} of ${formatKRValue(kr.target_value)}${kr.unit ? ` ${kr.unit}` : ""} · progress clamps 0–100%`
                            : `${(kr.linked_issues ?? []).filter((i) => DONE_GROUPS.has(i.state_group)).length} of ${(kr.linked_issues ?? []).length} linked issues done`
                        }
                      />
                    </div>
                    {kr.kind === "metric" && canEdit ? (
                      <MetricCurrentUpdate
                        key={`${kr.id}:${kr.current_value}`}
                        kr={kr}
                        pending={updateKRMutation.isPending}
                        onUpdate={(currentValue, note) => {
                          // Empty / whitespace-only notes send no
                          // checkin_note key at all (never null/"").
                          const trimmedNote = note.trim();
                          updateKRMutation.mutate({
                            krId: kr.id,
                            patch: {
                              current_value: currentValue,
                              ...(trimmedNote
                                ? { checkin_note: trimmedNote }
                                : {}),
                            },
                          });
                        }}
                      />
                    ) : kr.kind === "metric" ? (
                      <p className="mt-2 text-[11px] text-ink-muted tabular-nums">
                        Current {formatKRValue(kr.current_value)} · start{" "}
                        {formatKRValue(kr.start_value)} → target{" "}
                        {formatKRValue(kr.target_value)}
                        {kr.unit ? ` ${kr.unit}` : ""}
                      </p>
                    ) : null}
                    {/* Check-in history: metric KRs only, members
                        only (the list endpoint is member 15+). Issue
                        KRs show no history section at all, and no
                        history fetch fires for them. */}
                    {kr.kind === "metric" && canEdit ? (
                      <KRCheckinHistory
                        slug={slug}
                        objectiveId={id}
                        kr={kr}
                      />
                    ) : null}
                    {kr.kind === "issue" && (
                      <div className="mt-2">
                        {(kr.linked_issues ?? []).length === 0 ? (
                          <p className="text-[11px] text-ink-muted">
                            No linked issues yet — progress is 0% until
                            issues are linked.
                          </p>
                        ) : (
                          <ul className="space-y-1">
                            {(kr.linked_issues ?? []).map((li) => (
                              <li
                                key={li.id}
                                className="flex items-center gap-2 text-[12px]"
                              >
                                <span className="font-mono text-[11px] text-ink-secondary">
                                  {li.display_id}
                                </span>
                                <span className="truncate text-ink">
                                  {li.title}
                                </span>
                                <span className="text-[11px] text-ink-muted">
                                  {li.state_group || "no state"}
                                </span>
                                {canEdit && (
                                  <button
                                    type="button"
                                    aria-label={`Unlink ${li.display_id}`}
                                    title={`Unlink ${li.display_id}`}
                                    onClick={() =>
                                      onUnlink(kr, li.id, li.display_id)
                                    }
                                    className="ml-auto rounded p-1 text-ink-muted transition-colors hover:bg-raised hover:text-accent-red"
                                  >
                                    <X className="h-3.5 w-3.5" aria-hidden />
                                  </button>
                                )}
                              </li>
                            ))}
                          </ul>
                        )}
                        {canEdit && (
                          <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            className="mt-2"
                            onClick={() => {
                              setLinkKR(kr);
                              setLinkProject("");
                              setLinkPicked(new Set());
                            }}
                          >
                            <Link2 className="h-3 w-3" aria-hidden />
                            Link issues
                          </Button>
                        )}
                      </div>
                    )}
                  </CardContent>
                </Card>
              </li>
            ))}
          </ul>
        )}
      </section>

      {/* Edit objective dialog */}
      <Dialog open={editOpen} onOpenChange={setEditOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Edit objective</DialogTitle>
            <DialogDescription>
              Update the objective&apos;s title, owner, dates, or status.
            </DialogDescription>
          </DialogHeader>
          <EditObjectiveForm
            key={objective.id}
            objective={objective}
            members={membersQuery.data ?? []}
            pending={updateMutation.isPending}
            onSubmit={(patch) => updateMutation.mutate(patch)}
            onCancel={() => setEditOpen(false)}
          />
        </DialogContent>
      </Dialog>

      {/* Add key result dialog */}
      <Dialog
        open={addKROpen}
        onOpenChange={(open) => {
          setAddKROpen(open);
          if (!open) setKrFormError(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Add key result</DialogTitle>
            <DialogDescription>
              A measurable result under this objective. Kind is immutable
              after create — pick carefully.
            </DialogDescription>
          </DialogHeader>
          <KeyResultForm
            key={`add-${objective.id}`}
            mode="create"
            pending={createKRMutation.isPending}
            formError={krFormError}
            onSubmit={(body) => {
              // The form guarantees target_value in create mode
              // (required + zero-span validated); narrow for the
              // mutation's type.
              if (body.target_value === undefined) return;
              createKRMutation.mutate({
                ...body,
                target_value: body.target_value,
              });
            }}
            onCancel={() => setAddKROpen(false)}
          />
        </DialogContent>
      </Dialog>

      {/* Edit key result dialog — no kind control: kind is immutable. */}
      <Dialog
        open={editKR !== null}
        onOpenChange={(open) => {
          if (!open) setEditKR(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Edit key result</DialogTitle>
            <DialogDescription>
              Kind cannot change after create. Metric-only fields are
              hidden for issue-linked key results.
            </DialogDescription>
          </DialogHeader>
          {editKR && (
            <KeyResultForm
              key={editKR.id}
              mode="edit"
              kr={editKR}
              pending={updateKRMutation.isPending}
              onSubmit={(body) =>
                updateKRMutation.mutate({
                  krId: editKR.id,
                  patch: {
                    title: body.title,
                    ...(body.unit !== undefined
                      ? { unit: body.unit }
                      : {}),
                    ...(body.target_value !== undefined
                      ? { target_value: body.target_value }
                      : {}),
                  },
                })
              }
              onCancel={() => setEditKR(null)}
            />
          )}
        </DialogContent>
      </Dialog>

      {/* Link issues dialog */}
      <Dialog
        open={linkKR !== null}
        onOpenChange={(open) => {
          if (!open) {
            setLinkKR(null);
            setLinkProject("");
            setLinkPicked(new Set());
          }
        }}
      >
        <DialogContent className="max-h-[80vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>Link issues</DialogTitle>
            <DialogDescription>
              Pick issues from a project in this workspace. Progress is
              the share of linked issues in a done state group. Linking
              is idempotent — already-linked issues are not listed.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <div className="space-y-1.5">
              <Label htmlFor="link-project">Project</Label>
              <select
                id="link-project"
                value={linkProject}
                onChange={(e) => {
                  setLinkProject(e.target.value);
                  setLinkPicked(new Set());
                }}
                className="w-full rounded-md border border-input bg-background px-2.5 py-2 text-[13px] text-ink"
              >
                <option value="">Select a project…</option>
                {(projectsQuery.data ?? []).map((p) => (
                  <option key={p.id} value={p.identifier}>
                    {p.name} ({p.identifier})
                  </option>
                ))}
              </select>
            </div>
            {linkProject === "" ? (
              <p className="text-[13px] text-ink-muted">
                Select a project to list its issues.
              </p>
            ) : linkIssuesQuery.isPending ? (
              <div className="space-y-2">
                <Skeleton className="h-10 w-full" />
                <Skeleton className="h-10 w-full" />
              </div>
            ) : linkableIssues.length === 0 ? (
              <p className="text-[13px] text-ink-muted">
                No linkable issues in this project — every issue is already
                linked.
              </p>
            ) : (
              <div className="space-y-1">
                {linkableIssues.map((issue) => (
                  <label
                    key={issue.id}
                    className="flex cursor-pointer items-center gap-3 rounded-md px-2 py-2 text-sm hover:bg-accent"
                  >
                    <Checkbox
                      checked={linkPicked.has(issue.id)}
                      onCheckedChange={() => {
                        setLinkPicked((prev) => {
                          const next = new Set(prev);
                          if (next.has(issue.id)) next.delete(issue.id);
                          else next.add(issue.id);
                          return next;
                        });
                      }}
                    />
                    <span className="font-mono text-[11px] text-ink-secondary">
                      {issue.display_id}
                    </span>
                    <span className="truncate">{issue.name}</span>
                  </label>
                ))}
              </div>
            )}
            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                onClick={() => {
                  setLinkKR(null);
                  setLinkProject("");
                  setLinkPicked(new Set());
                }}
              >
                Cancel
              </Button>
              <Button
                type="button"
                disabled={
                  linkIssueMutation.isPending || linkPicked.size === 0
                }
                onClick={() =>
                  linkKR &&
                  linkIssueMutation.mutate({
                    krId: linkKR.id,
                    issueIds: [...linkPicked],
                  })
                }
              >
                {linkIssueMutation.isPending
                  ? "Linking…"
                  : `Link ${linkPicked.size === 0 ? "issues" : `${linkPicked.size} issue${linkPicked.size === 1 ? "" : "s"}`}`}
              </Button>
            </DialogFooter>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function EditObjectiveForm({
  objective,
  members,
  pending,
  onSubmit,
  onCancel,
}: {
  objective: Objective;
  members: WorkspaceMember[];
  pending: boolean;
  onSubmit: (patch: {
    title?: string;
    description?: string;
    owner_id?: string;
    start_date?: string;
    target_date?: string;
    status?: ObjectiveStatus;
  }) => void;
  onCancel: () => void;
}) {
  const [title, setTitle] = useState(objective.title);
  const [description, setDescription] = useState(objective.description ?? "");
  const [ownerId, setOwnerId] = useState(objective.owner?.id ?? "");
  const [startDate, setStartDate] = useState(objective.start_date ?? "");
  const [targetDate, setTargetDate] = useState(objective.target_date ?? "");
  const [status, setStatus] = useState<ObjectiveStatus>(objective.status);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    // PATCH tri-state: only changed fields are sent; "" clears the
    // nullable ones (description / owner / dates).
    const patch: {
      title?: string;
      description?: string;
      owner_id?: string;
      start_date?: string;
      target_date?: string;
      status?: ObjectiveStatus;
    } = {};
    const trimmed = title.trim();
    if (trimmed && trimmed !== objective.title) patch.title = trimmed;
    if (description !== (objective.description ?? ""))
      patch.description = description;
    if (ownerId !== (objective.owner?.id ?? "")) patch.owner_id = ownerId;
    if (startDate !== (objective.start_date ?? ""))
      patch.start_date = startDate;
    if (targetDate !== (objective.target_date ?? ""))
      patch.target_date = targetDate;
    if (status !== objective.status) patch.status = status;
    onSubmit(patch);
  };

  return (
    <form onSubmit={submit} className="space-y-4">
      <div className="space-y-1.5">
        <Label htmlFor="objective-title">Title</Label>
        <Input
          id="objective-title"
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          maxLength={255}
        />
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="objective-description">Description</Label>
        <Textarea
          id="objective-description"
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          rows={3}
          placeholder="What this objective means"
        />
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="objective-owner">Owner</Label>
        <select
          id="objective-owner"
          value={ownerId}
          onChange={(e) => setOwnerId(e.target.value)}
          className="w-full rounded-md border border-input bg-background px-2.5 py-2 text-[13px] text-ink"
        >
          <option value="">No owner</option>
          {members.map((m) => (
            <option key={m.id} value={m.id}>
              {m.name ?? m.email}
            </option>
          ))}
        </select>
      </div>
      <div className="grid grid-cols-2 gap-4">
        <div className="space-y-1.5">
          <Label htmlFor="objective-start">Start date</Label>
          <Input
            id="objective-start"
            type="date"
            value={startDate}
            onChange={(e) => setStartDate(e.target.value)}
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="objective-target">Target date</Label>
          <Input
            id="objective-target"
            type="date"
            value={targetDate}
            onChange={(e) => setTargetDate(e.target.value)}
          />
        </div>
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="objective-status">Status</Label>
        <select
          id="objective-status"
          value={status}
          onChange={(e) => setStatus(e.target.value as ObjectiveStatus)}
          className="w-full rounded-md border border-input bg-background px-2.5 py-2 text-[13px] text-ink"
        >
          {STATUSES.map((s) => (
            <option key={s} value={s}>
              {statusLabel(s)}
            </option>
          ))}
        </select>
      </div>
      <DialogFooter>
        <Button type="button" variant="outline" onClick={onCancel}>
          Cancel
        </Button>
        <Button type="submit" disabled={pending || title.trim().length === 0}>
          {pending ? "Saving…" : "Save changes"}
        </Button>
      </DialogFooter>
    </form>
  );
}

/** Shared add / edit key-result form. Create mode: title, kind, unit
 *  (metric only), start / target / current (metric only). Edit mode:
 *  title + metric-only fields — no kind control, kind is immutable.
 *  Metric-only fields are never sent for issue KRs: the backend
 *  rejects their mere presence with 400. */
function KeyResultForm({
  mode,
  kr,
  pending,
  formError,
  onSubmit,
  onCancel,
}: {
  mode: "create" | "edit";
  kr?: KeyResult;
  pending: boolean;
  formError?: string | null;
  onSubmit: (body: {
    title: string;
    kind: KeyResultKind;
    unit?: string;
    start_value?: number;
    target_value?: number;
    current_value?: number;
  }) => void;
  onCancel: () => void;
}) {
  const [title, setTitle] = useState(kr?.title ?? "");
  const [kind, setKind] = useState<KeyResultKind>(kr?.kind ?? "metric");
  const [unit, setUnit] = useState(kr?.unit ?? "");
  const [startValue, setStartValue] = useState(
    kr ? formatKRValue(kr.start_value) : "",
  );
  const [targetValue, setTargetValue] = useState(
    kr ? formatKRValue(kr.target_value) : "",
  );
  const [currentValue, setCurrentValue] = useState("");
  const [localError, setLocalError] = useState<string | null>(null);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    setLocalError(null);
    const trimmedTitle = title.trim();
    if (!trimmedTitle) {
      setLocalError("Title is required.");
      return;
    }
    if (mode === "edit" && kr) {
      // Edit: no kind control (immutable); metric-only fields only
      // for metric KRs. Title/unit/target are sent as-is — the
      // backend PATCH treats them as set, "" clears unit.
      onSubmit({
        title: trimmedTitle,
        kind: kr.kind,
        ...(kr.kind === "metric"
          ? {
              unit,
              ...(targetValue !== ""
                ? { target_value: Number(targetValue) }
                : {}),
            }
          : {}),
      });
      return;
    }
    // Create: target is required and must differ from start (a
    // zero-span KR can never move — the backend rejects it with 400).
    if (targetValue === "") {
      setLocalError("Target value is required.");
      return;
    }
    const start = startValue === "" ? 0 : Number(startValue);
    const target = Number(targetValue);
    if (Number.isFinite(start) && Number.isFinite(target) && target === start) {
      setLocalError("Target must differ from start — a zero-span key result can never move.");
      return;
    }
    onSubmit({
      title: trimmedTitle,
      kind,
      ...(kind === "metric"
        ? {
            ...(unit.trim() ? { unit: unit.trim() } : {}),
            ...(startValue !== "" ? { start_value: Number(startValue) } : {}),
            target_value: target,
            ...(currentValue !== ""
              ? { current_value: Number(currentValue) }
              : {}),
          }
        : {
            ...(startValue !== "" ? { start_value: Number(startValue) } : {}),
            target_value: target,
          }),
    });
  };

  const error = localError ?? formError;

  return (
    <form onSubmit={submit} className="space-y-4">
      <div className="space-y-1.5">
        <Label htmlFor="kr-title">Title</Label>
        <Input
          id="kr-title"
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder="Close all Plane-parity gaps"
          required
        />
      </div>
      {mode === "create" && (
        <div className="space-y-1.5">
          <Label htmlFor="kr-kind">Kind</Label>
          <select
            id="kr-kind"
            value={kind}
            onChange={(e) => setKind(e.target.value as KeyResultKind)}
            className="w-full rounded-md border border-input bg-background px-2.5 py-2 text-[13px] text-ink"
          >
            <option value="metric">Metric — manual current value</option>
            <option value="issue">
              Issue-linked — progress from linked issues
            </option>
          </select>
        </div>
      )}
      {kind === "metric" && (
        <div className="space-y-1.5">
          <Label htmlFor="kr-unit">Unit</Label>
          <Input
            id="kr-unit"
            value={unit}
            onChange={(e) => setUnit(e.target.value)}
            placeholder="% or users (optional)"
          />
        </div>
      )}
      <div className="grid grid-cols-2 gap-4">
        {mode === "create" && (
          <div className="space-y-1.5">
            <Label htmlFor="kr-start">Start value</Label>
            <Input
              id="kr-start"
              type="number"
              step="any"
              value={startValue}
              onChange={(e) => setStartValue(e.target.value)}
              placeholder="0"
            />
          </div>
        )}
        {(mode === "create" || kind === "metric") && (
          <div className="space-y-1.5">
            <Label htmlFor="kr-target">Target value</Label>
            <Input
              id="kr-target"
              type="number"
              step="any"
              value={targetValue}
              onChange={(e) => setTargetValue(e.target.value)}
              placeholder="100"
              required={mode === "create"}
            />
          </div>
        )}
      </div>
      {mode === "create" && kind === "metric" && (
        <div className="space-y-1.5">
          <Label htmlFor="kr-current">Current value</Label>
          <Input
            id="kr-current"
            type="number"
            step="any"
            value={currentValue}
            onChange={(e) => setCurrentValue(e.target.value)}
            placeholder="Leave blank to start at the start value"
          />
        </div>
      )}
      {mode === "create" && kind === "issue" && (
        <p className="text-[12px] text-ink-muted">
          Issue-linked key results track progress from linked issues — no
          unit or current value. Link issues after creating.
        </p>
      )}
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      <DialogFooter>
        <Button type="button" variant="outline" onClick={onCancel}>
          Cancel
        </Button>
        <Button type="submit" disabled={pending || !title.trim()}>
          {pending
            ? mode === "create"
              ? "Adding…"
              : "Saving…"
            : mode === "create"
              ? "Add key result"
              : "Save changes"}
        </Button>
      </DialogFooter>
    </form>
  );
}
