import { useMemo, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Pencil } from "lucide-react";
import ProjectNav from "../components/project/ProjectNav";
import { Avatar, AvatarFallback } from "../components/ui/avatar";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "../components/ui/card";
import { Progress } from "../components/ui/progress";
import { Skeleton } from "../components/ui/skeleton";
import { Textarea } from "../components/ui/textarea";
import { api } from "../lib/api";
import { describeChange, type ActivityEntry } from "../lib/activity";
import { renderMarkdown } from "../lib/markdown";
import {
  completionStats,
  cyclePercent,
  priorityBreakdown,
  recentActivity,
  topContributors,
  type ProjectOverview,
} from "../lib/overview";
import { relativeTime } from "../lib/relativeTime";
import { PRIORITY_LABELS } from "../lib/types";
import { cn } from "../lib/utils";

/** Project overview page (C8T5): editable markdown description, stat
 *  cards, cycle progress, recent activity, and top contributors — all
 *  from one GET .../projects/{identifier}/overview round-trip.
 *
 *  The description edit gate is the same as project settings
 *  (workspace role >= 15, member). Rendered markdown goes through the
 *  wiki's escape-first renderer — never raw user HTML. */

const EDIT_GATE_ROLE = 15;

function actorInitial(name: string): string {
  return (name.trim()[0] ?? "?").toUpperCase();
}

function StatCard({
  label,
  value,
  sub,
}: {
  label: string;
  value: string;
  sub?: string;
}) {
  return (
    <Card>
      <CardContent className="pt-4">
        <p className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
          {label}
        </p>
        <p className="mt-1 text-2xl font-semibold tabular-nums">{value}</p>
        {sub && <p className="mt-1 text-xs text-muted-foreground">{sub}</p>}
      </CardContent>
    </Card>
  );
}

function ActivityRow({
  entry,
  issueBase,
  stateById,
}: {
  entry: ActivityEntry;
  issueBase: string;
  stateById: Map<string, string>;
}) {
  const d = describeChange(entry, stateById);
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
          {d.kind === "created" ? (
            <>created</>
          ) : d.kind === "deleted" ? (
            <>deleted</>
          ) : d.kind === "edited" ? (
            <>
              edited <span className="font-medium">{d.label}</span>
            </>
          ) : d.kind === "set" ? (
            <>
              set <span className="font-medium">{d.label}</span> to{" "}
              <code className="rounded bg-muted px-1 py-0.5 font-mono text-xs">
                {d.newText}
              </code>
            </>
          ) : d.kind === "cleared" ? (
            <>
              cleared <span className="font-medium">{d.label}</span>
            </>
          ) : (
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
          )}{" "}
          <Link
            to={`${issueBase}/i/${entry.issue_uuid}`}
            className="font-medium text-primary hover:underline"
          >
            {entry.issue_identifier}
          </Link>
        </p>
        <p className="mt-0.5 text-xs text-muted-foreground">
          {relativeTime(entry.at)}
        </p>
      </div>
    </div>
  );
}

function DescriptionCard({
  description,
  canEdit,
  onSave,
  saving,
}: {
  description: string;
  canEdit: boolean;
  /** Persist the draft; call done() only after a successful save so a
   *  failed save keeps the editor open (wiki convention). */
  onSave: (next: string, done: () => void) => void;
  saving: boolean;
}) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(description);
  const [preview, setPreview] = useState(false);

  const startEdit = () => {
    setDraft(description);
    setPreview(false);
    setEditing(true);
  };

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
        <CardTitle className="text-sm font-semibold">About</CardTitle>
        {canEdit && !editing && (
          <Button variant="ghost" size="sm" onClick={startEdit} aria-label="Edit description">
            <Pencil className="mr-1 h-3.5 w-3.5" />
            Edit
          </Button>
        )}
      </CardHeader>
      <CardContent>
        {editing ? (
          <div className="space-y-3">
            <div className="flex gap-1">
              <Button
                variant={!preview ? "secondary" : "ghost"}
                size="sm"
                onClick={() => setPreview(false)}
              >
                Write
              </Button>
              <Button
                variant={preview ? "secondary" : "ghost"}
                size="sm"
                onClick={() => setPreview(true)}
              >
                Preview
              </Button>
            </div>
            {preview ? (
              <div
                className="wiki-content min-h-32 rounded-md border p-3 text-sm"
                dangerouslySetInnerHTML={{ __html: renderMarkdown(draft) }}
              />
            ) : (
              <Textarea
                value={draft}
                onChange={(e) => setDraft(e.target.value)}
                rows={8}
                placeholder={"# Heading\n\nWrite in **markdown**…"}
                aria-label="Project description (markdown)"
              />
            )}
            <div className="flex justify-end gap-2">
              <Button
                variant="outline"
                size="sm"
                onClick={() => setEditing(false)}
                disabled={saving}
              >
                Cancel
              </Button>
              <Button
                size="sm"
                onClick={() => onSave(draft, () => setEditing(false))}
                disabled={saving}
              >
                {saving ? "Saving…" : "Save"}
              </Button>
            </div>
          </div>
        ) : description ? (
          <div
            className="wiki-content text-sm"
            dangerouslySetInnerHTML={{ __html: renderMarkdown(description) }}
          />
        ) : (
          <p className="text-sm text-muted-foreground">
            No description yet.{" "}
            {canEdit && (
              <button
                type="button"
                className="font-medium text-primary hover:underline"
                onClick={startEdit}
              >
                Write one
              </button>
            )}
          </p>
        )}
      </CardContent>
    </Card>
  );
}

export default function Overview() {
  const { slug = "", identifier = "" } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const queryClient = useQueryClient();
  const base = `/api/v1/workspaces/${slug}/projects/${identifier}`;
  const issueBase = `/w/${slug}/p/${identifier}`;

  const overviewQuery = useQuery({
    queryKey: ["overview", slug, identifier],
    queryFn: () => api.get<ProjectOverview>(`${base}/overview`),
  });

  const saveMutation = useMutation({
    mutationFn: (description: string) => api.patch(`${base}`, { description }),
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: ["overview", slug, identifier] }),
  });

  const data = overviewQuery.data;
  const canEdit = (data?.role ?? 0) >= EDIT_GATE_ROLE;

  const completion = useMemo(
    () => (data ? completionStats(data.summary.by_state, data.states) : null),
    [data],
  );
  const priorities = useMemo(
    () => (data ? priorityBreakdown(data.open_by_priority, PRIORITY_LABELS) : []),
    [data],
  );
  const contributors = useMemo(
    () => (data ? topContributors(data.activity, 5) : []),
    [data],
  );
  const recent = useMemo(() => (data ? recentActivity(data.activity, 10) : []), [data]);
  const stateById = useMemo(() => {
    const m = new Map<string, string>();
    for (const s of data?.states ?? []) m.set(s.id, s.name);
    return m;
  }, [data]);
  const maxPriority = Math.max(1, ...priorities.map((p) => p.count));

  return (
    <div className="flex h-full flex-col gap-4 overflow-y-auto p-4">
      <ProjectNav />
      {overviewQuery.isPending ? (
        <div className="space-y-4">
          <Skeleton className="h-8 w-64" />
          <Skeleton className="h-40 w-full" />
          <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
            {[0, 1, 2, 3].map((i) => (
              <Skeleton key={i} className="h-24" />
            ))}
          </div>
        </div>
      ) : overviewQuery.isError ? (
        <div className="py-8 text-center text-sm text-destructive">
          Failed to load: {(overviewQuery.error as Error).message}
        </div>
      ) : (
        data && (
          <>
            <div className="flex items-center gap-3">
              <h1 className="text-lg font-semibold">{data.project.name}</h1>
              <Badge variant="secondary" className="font-mono">
                {data.project.identifier}
              </Badge>
            </div>

            <DescriptionCard
              description={data.project.description}
              canEdit={canEdit}
              saving={saveMutation.isPending}
              onSave={(next, done) =>
                saveMutation.mutate(next, { onSuccess: () => done() })
              }
            />
            {saveMutation.isError && (
              <p className="text-sm text-destructive">
                Failed to save: {(saveMutation.error as Error).message}
              </p>
            )}

            <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
              <StatCard
                label="Open issues"
                value={String(completion?.open ?? 0)}
                sub={
                  completion
                    ? `${completion.done} of ${completion.total} done`
                    : undefined
                }
              />
              <Card>
                <CardContent className="pt-4">
                  <p className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
                    Completion
                  </p>
                  <p className="mt-1 text-2xl font-semibold tabular-nums">
                    {completion?.percent ?? 0}%
                  </p>
                  <Progress value={completion?.percent ?? 0} className="mt-2" />
                </CardContent>
              </Card>
              <Card>
                <CardContent className="pt-4">
                  <p className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
                    {data.cycle ? `Cycle: ${data.cycle.name}` : "Cycle"}
                  </p>
                  <p className="mt-1 text-2xl font-semibold tabular-nums">
                    {data.cycle ? `${data.cycle.completed}/${data.cycle.total}` : "—"}
                  </p>
                  {data.cycle ? (
                    <Progress value={cyclePercent(data.cycle)} className="mt-2" />
                  ) : (
                    <p className="mt-2 text-xs text-muted-foreground">No cycles yet</p>
                  )}
                </CardContent>
              </Card>
              <StatCard
                label="Overdue"
                value={String(data.summary.overdue_count)}
                sub="past target date, not done"
              />
            </div>

            <div className="grid gap-4 lg:grid-cols-2">
              <Card>
                <CardHeader className="pb-2">
                  <CardTitle className="text-sm font-semibold">
                    Open issues by priority
                  </CardTitle>
                </CardHeader>
                <CardContent className="space-y-2">
                  {priorities.map((p) => (
                    <div key={p.priority} className="flex items-center gap-3">
                      <span className="w-16 text-xs text-muted-foreground">
                        {p.label}
                      </span>
                      <div className="h-2 flex-1 overflow-hidden rounded-full bg-muted">
                        <div
                          className={cn(
                            "h-full rounded-full",
                            p.priority === 4
                              ? "bg-destructive"
                              : p.priority === 3
                                ? "bg-orange-500"
                                : "bg-primary",
                          )}
                          style={{ width: `${(p.count / maxPriority) * 100}%` }}
                        />
                      </div>
                      <span className="w-8 text-right text-xs tabular-nums">
                        {p.count}
                      </span>
                    </div>
                  ))}
                </CardContent>
              </Card>

              <Card>
                <CardHeader className="pb-2">
                  <CardTitle className="text-sm font-semibold">
                    Top contributors
                  </CardTitle>
                </CardHeader>
                <CardContent>
                  {contributors.length === 0 ? (
                    <p className="py-4 text-center text-sm text-muted-foreground">
                      No activity yet.
                    </p>
                  ) : (
                    contributors.map((c, i) => (
                      <div
                        key={c.actor}
                        className="flex items-center gap-3 border-b py-2 last:border-b-0"
                      >
                        <span className="w-6 text-xs tabular-nums text-muted-foreground">
                          {i + 1}
                        </span>
                        <Avatar className="h-7 w-7">
                          <AvatarFallback className="text-xs font-semibold">
                            {actorInitial(c.actor)}
                          </AvatarFallback>
                        </Avatar>
                        <span className="flex-1 truncate text-sm font-medium">
                          {c.actor}
                        </span>
                        <span className="text-xs text-muted-foreground tabular-nums">
                          {c.changes} {c.changes === 1 ? "change" : "changes"}
                        </span>
                      </div>
                    ))
                  )}
                  <p className="mt-2 text-xs text-muted-foreground">
                    Ranked by changes in the recent activity window.
                  </p>
                </CardContent>
              </Card>
            </div>

            <Card>
              <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                <CardTitle className="text-sm font-semibold">
                  Recent activity
                </CardTitle>
                <Link
                  to={`${issueBase}/activity`}
                  className="text-xs font-medium text-primary hover:underline"
                >
                  View all
                </Link>
              </CardHeader>
              <CardContent className="p-0">
                {recent.length === 0 ? (
                  <p className="py-8 text-center text-sm text-muted-foreground">
                    No activity yet.
                  </p>
                ) : (
                  recent.map((e, i) => (
                    <ActivityRow
                      key={`${e.at}-${e.issue_uuid}-${e.field}-${i}`}
                      entry={e}
                      issueBase={issueBase}
                      stateById={stateById}
                    />
                  ))
                )}
              </CardContent>
            </Card>
          </>
        )
      )}
    </div>
  );
}
