// Automations management (C11T1, C16T4): per-project workflow automation
// rules for project settings. When an issue event fires or a scheduled
// trigger evaluates, matching rules run their actions automatically.
// Reads and mutations are member (15)+; the server is the gate — guests
// get a read-only view and 403s. Failures surface as honest toasts,
// never silent.

import { useState } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Pencil, Plus, Trash2, X } from "lucide-react";
import { ApiError, api } from "../../lib/api";
import {
  automationKeys,
  createAutomationRule,
  deleteAutomationRule,
  describeAction,
  describeTrigger,
  fetchAutomationRules,
  fetchAutomationRuns,
  updateAutomationRule,
  type AutomationActionResult,
  type AutomationRule,
  type AutomationRuleInput,
  type AutomationRun,
} from "../../lib/automations";
import {
  draftFromRule,
  draftToInput,
  emptyDraftAction,
  emptyRuleDraft,
  type DraftAction,
  type RuleDraft,
} from "../../lib/automationDraft";
import {
  fetchEstimates,
  fetchLabels,
  fetchStates,
  taxonomyKeys,
  type Estimate,
  type TaxLabel,
  type TaxState,
} from "../../lib/taxonomy";
import { relativeTime } from "../../lib/relativeTime";
import { type Cycle, type Module, type WorkspaceMember } from "../../lib/types";
import { toast } from "../ui/toast";
import { Badge } from "../ui/badge";
import { Button } from "../ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "../ui/card";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../ui/dialog";
import { Input } from "../ui/input";
import { Label } from "../ui/label";
import { Skeleton } from "../ui/skeleton";
import { Switch } from "../ui/switch";
import {
  ActionFields,
  ActionTypeSelect,
  TriggerFields,
} from "./AutomationPickers";

function failToast(title: string, err: unknown, fallback: string) {
  toast.add({
    title,
    description: err instanceof ApiError ? err.message : fallback,
    type: "error",
  });
}

function projectBase(slug: string, identifier: string): string {
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;
}

// describeRunTrigger renders the stored trigger_type honestly; anything
// unknown falls back to the raw value.
function describeRunTrigger(triggerType: string): string {
  switch (triggerType) {
    case "issue.state_changed":
      return "on state change";
    case "issue.created":
      return "on issue creation";
    case "issue.assigned":
      return "on assignment";
    case "issue.unassigned":
      return "on unassignment";
    case "issue.labels_changed":
      return "on label change";
    case "issue.priority_changed":
      return "on priority change";
    case "issue.due_date_changed":
      return "on due date change";
    case "issue.estimate_changed":
      return "on estimate change";
    case "issue.comment_added":
      return "on new comment";
    case "issue.due_soon":
      return "scheduled: due soon";
    case "issue.overdue":
      return "scheduled: overdue";
    case "issue.stale":
      return "scheduled: stale";
    case "cycle.ending_soon":
      return "scheduled: cycle ending soon";
    default:
      return triggerType;
  }
}

function RunActionChips({ actions }: { actions: AutomationActionResult[] }) {
  return (
    <div className="flex flex-wrap gap-1.5">
      {actions.map((a, i) => (
        <Badge key={i} variant={a.ok ? "outline" : "destructive"}>
          {a.type} · {a.ok ? "ok" : "failed"}
        </Badge>
      ))}
    </div>
  );
}

export default function AutomationsSection({
  slug,
  identifier,
  canEdit,
}: {
  slug: string;
  identifier: string;
  canEdit: boolean;
}) {
  const queryClient = useQueryClient();
  const keys = automationKeys(slug, identifier);
  const taxKeys = taxonomyKeys(slug, identifier);
  const base = projectBase(slug, identifier);

  const [builderOpen, setBuilderOpen] = useState(false);
  const [editing, setEditing] = useState<AutomationRule | null>(null);
  const [draft, setDraft] = useState<RuleDraft>(emptyRuleDraft);

  const rulesQuery = useQuery({
    queryKey: keys.rules,
    queryFn: () => fetchAutomationRules(slug, identifier),
  });
  const runsQuery = useQuery({
    queryKey: keys.runs,
    queryFn: () => fetchAutomationRuns(slug, identifier),
  });
  const statesQuery = useQuery({
    queryKey: taxKeys.states,
    queryFn: () => fetchStates(slug, identifier),
  });
  const labelsQuery = useQuery({
    queryKey: taxKeys.labels,
    queryFn: () => fetchLabels(slug, identifier),
  });
  const membersQuery = useQuery({
    queryKey: ["workspace", slug, "members"],
    queryFn: () =>
      api
        .get<{ members: WorkspaceMember[] }>(
          `/api/v1/workspaces/${encodeURIComponent(slug)}/members`,
        )
        .then((r) => r.members ?? []),
  });
  // C16T4 lookups for the new action pickers: estimate scales (taxonomy
  // keys, shared cache), cycles and modules (shared with their pages).
  const estimatesQuery = useQuery({
    queryKey: taxKeys.estimates,
    queryFn: () => fetchEstimates(slug, identifier),
  });
  const cyclesQuery = useQuery({
    queryKey: ["cycles", slug, identifier],
    queryFn: () =>
      api.get<{ cycles: Cycle[] }>(`${base}/cycles`).then((r) => r.cycles ?? []),
  });
  const modulesQuery = useQuery({
    queryKey: ["modules", slug, identifier],
    queryFn: () =>
      api
        .get<{ modules: Module[] }>(`${base}/modules`)
        .then((r) => r.modules ?? []),
  });

  const states: TaxState[] = statesQuery.data ?? [];
  const labels: TaxLabel[] = labelsQuery.data ?? [];
  const members: WorkspaceMember[] = membersQuery.data ?? [];
  const rules: AutomationRule[] = rulesQuery.data ?? [];
  const runs: AutomationRun[] = runsQuery.data ?? [];
  const estimateScales: Estimate[] = estimatesQuery.data ?? [];
  const cycles: Cycle[] = cyclesQuery.data ?? [];
  const modules: Module[] = modulesQuery.data ?? [];

  const actionLookups = {
    cycles,
    modules,
    estimatePoints: estimateScales.flatMap((s) =>
      s.points.map((p) => ({ id: p.id, key: p.key, scaleName: s.name })),
    ),
  };

  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: keys.rules });
    queryClient.invalidateQueries({ queryKey: keys.runs });
  };

  function openCreate() {
    setEditing(null);
    setDraft(emptyRuleDraft());
    setBuilderOpen(true);
  }

  function openEdit(rule: AutomationRule) {
    setEditing(rule);
    setDraft(draftFromRule(rule));
    setBuilderOpen(true);
  }

  const saveMutation = useMutation({
    mutationFn: () => {
      const result = draftToInput(draft);
      if (!result.ok) throw new Error(result.error);
      const input: AutomationRuleInput = result.input;
      if (editing) {
        return updateAutomationRule(slug, identifier, editing.id, input);
      }
      return createAutomationRule(slug, identifier, input);
    },
    onSuccess: () => {
      setBuilderOpen(false);
      setEditing(null);
      void invalidate();
      toast.add({
        title: editing ? "Rule updated" : "Rule created",
        type: "success",
      });
    },
    onError: (e) =>
      failToast(
        editing ? "Could not update the rule" : "Could not create the rule",
        e,
        e instanceof Error ? e.message : "The rule was not saved.",
      ),
  });

  const toggleMutation = useMutation({
    mutationFn: (rule: AutomationRule) =>
      updateAutomationRule(slug, identifier, rule.id, {
        enabled: !rule.enabled,
      }),
    onSuccess: () => void invalidate(),
    onError: (e) => failToast("Could not toggle the rule", e, "Try again."),
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => deleteAutomationRule(slug, identifier, id),
    onSuccess: () => {
      void invalidate();
      toast.add({ title: "Rule deleted", type: "success" });
    },
    onError: (e) => failToast("Could not delete the rule", e, "Try again."),
  });

  function onDelete(rule: AutomationRule) {
    if (
      !window.confirm(
        `Delete automation rule "${rule.name}"? It will stop firing immediately.`,
      )
    )
      return;
    deleteMutation.mutate(rule.id);
  }

  function updateTrigger(patch: Partial<RuleDraft["trigger"]>) {
    setDraft((d) => ({ ...d, trigger: { ...d.trigger, ...patch } }));
  }

  function updateDraftAction(i: number, patch: Partial<DraftAction>) {
    setDraft((d) => ({
      ...d,
      actions: d.actions.map((a, j) => (j === i ? { ...a, ...patch } : a)),
    }));
  }

  return (
    <>
    <Card>
      <CardHeader className="flex flex-row items-center justify-between">
        <div>
          <CardTitle>Automations</CardTitle>
          <p className="mt-1 text-sm text-muted-foreground">
            When an issue event happens — assignment, labels, priority,
            due date, estimate, comments, state changes, creation — or
            on a schedule (due soon, overdue, stale, cycle ending),
            run actions automatically: assign or unassign users, add or
            remove labels, post a comment, set priority, estimate or due
            date, move to a cycle or module, or add a watcher.
            Automation-driven changes never trigger other rules. Free in
            glance.
          </p>
        </div>
        {canEdit && (
          <Button size="sm" onClick={openCreate}>
            <Plus className="mr-1 h-4 w-4" /> New rule
          </Button>
        )}
      </CardHeader>
      <CardContent>
        {rulesQuery.isLoading ? (
          <Skeleton className="h-24 w-full" />
        ) : rulesQuery.isError ? (
          <p className="text-sm text-destructive">
            Could not load automation rules.
          </p>
        ) : rules.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No automation rules yet.
            {canEdit
              ? " Create one to react to issue events automatically."
              : ""}
          </p>
        ) : (
          <ul className="space-y-3">
            {rules.map((rule) => (
              <li
                key={rule.id}
                className="flex items-start justify-between gap-3 rounded-md border p-3"
              >
                <div className="min-w-0">
                  <div className="flex items-center gap-2">
                    <span className="font-medium">{rule.name}</span>
                    {!rule.enabled && (
                      <Badge variant="secondary">disabled</Badge>
                    )}
                  </div>
                  <p className="mt-1 text-sm text-muted-foreground">
                    {describeTrigger(rule.trigger, states, labels)}
                  </p>
                  <ul className="mt-1 space-y-0.5 text-sm text-muted-foreground">
                    {rule.actions.map((a, i) => (
                      <li key={i}>
                        → {describeAction(a, members, labels, states, actionLookups)}
                      </li>
                    ))}
                  </ul>
                </div>
                {canEdit && (
                  <div className="flex shrink-0 items-center gap-2">
                    <Switch
                      checked={rule.enabled}
                      disabled={toggleMutation.isPending}
                      onCheckedChange={() => toggleMutation.mutate(rule)}
                      aria-label={`Enable rule ${rule.name}`}
                      size="sm"
                    />
                    <Button
                      variant="ghost"
                      size="icon"
                      onClick={() => openEdit(rule)}
                      aria-label={`Edit rule ${rule.name}`}
                    >
                      <Pencil className="h-4 w-4" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon"
                      onClick={() => onDelete(rule)}
                      aria-label={`Delete rule ${rule.name}`}
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </div>
                )}
              </li>
            ))}
          </ul>
        )}

        <Dialog
          open={builderOpen}
          onOpenChange={(open) => {
            setBuilderOpen(open);
            if (!open) setEditing(null);
          }}
        >
          <DialogContent className="max-w-lg">
            <DialogHeader>
              <DialogTitle>
                {editing ? "Edit automation rule" : "New automation rule"}
              </DialogTitle>
            </DialogHeader>
            <div className="space-y-4">
              <div>
                <Label htmlFor="auto-name">Name</Label>
                <Input
                  id="auto-name"
                  value={draft.name}
                  onChange={(e) =>
                    setDraft((d) => ({ ...d, name: e.target.value }))
                  }
                  placeholder="e.g. Assign QA when work starts"
                  maxLength={120}
                />
              </div>
              <TriggerFields
                trigger={draft.trigger}
                onChange={updateTrigger}
                lookups={{ states, labels }}
              />
              <div>
                <Label>Actions (run in order)</Label>
                <div className="mt-2 space-y-3">
                  {draft.actions.map((a, i) => (
                    <div
                      key={i}
                      className="flex items-start gap-2 rounded-md border p-2"
                    >
                      <ActionTypeSelect
                        index={i}
                        value={a.type}
                        onChange={(t) => updateDraftAction(i, { type: t })}
                      />
                      <ActionFields
                        index={i}
                        action={a}
                        onChange={(patch) => updateDraftAction(i, patch)}
                        lookups={{ members, labels, states, cycles, modules, estimateScales }}
                      />
                      {draft.actions.length > 1 && (
                        <Button
                          variant="ghost"
                          size="icon"
                          onClick={() =>
                            setDraft((d) => ({
                              ...d,
                              actions: d.actions.filter((_, j) => j !== i),
                            }))
                          }
                          aria-label={`Remove action ${i + 1}`}
                        >
                          <X className="h-4 w-4" />
                        </Button>
                      )}
                    </div>
                  ))}
                </div>
                {draft.actions.length < 10 && (
                  <Button
                    variant="outline"
                    size="sm"
                    className="mt-2"
                    onClick={() =>
                      setDraft((d) => ({
                        ...d,
                        actions: [...d.actions, emptyDraftAction()],
                      }))
                    }
                  >
                    <Plus className="mr-1 h-4 w-4" /> Add action
                  </Button>
                )}
              </div>
            </div>
            <DialogFooter>
              <Button
                variant="outline"
                onClick={() => setBuilderOpen(false)}
              >
                Cancel
              </Button>
              <Button
                onClick={() => saveMutation.mutate()}
                disabled={
                  saveMutation.isPending || !draft.name.trim()
                }
              >
                {editing ? "Save changes" : "Create rule"}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      </CardContent>
    </Card>

    <Card className="mt-6">
      <CardHeader>
        <div>
          <CardTitle>Recent runs</CardTitle>
          <p className="mt-1 text-sm text-muted-foreground">
            Every rule firing, newest first — what changed and whether
            each action succeeded.
          </p>
        </div>
      </CardHeader>
      <CardContent>
        {runsQuery.isLoading ? (
          <Skeleton className="h-24 w-full" />
        ) : runsQuery.isError ? (
          <p className="text-sm text-destructive">
            Could not load automation runs.
          </p>
        ) : runs.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No automation runs yet. A run appears here each time a rule
            fires.
          </p>
        ) : (
          <ul className="space-y-3">
            {runs.map((run) => (
              <li
                key={run.id}
                className="rounded-md border p-3"
              >
                <div className="flex items-center gap-2 text-sm">
                  <span className="font-medium">{run.rule_name}</span>
                  <span className="text-muted-foreground">on</span>
                  {run.issue_display_id ? (
                    <Link
                      to={`/w/${encodeURIComponent(slug)}/p/${encodeURIComponent(identifier)}/i/${encodeURIComponent(run.issue_id)}`}
                      className="font-mono text-primary underline-offset-4 hover:underline"
                    >
                      {run.issue_display_id}
                    </Link>
                  ) : (
                    <span className="text-muted-foreground">
                      removed issue
                    </span>
                  )}
                  <span className="ml-auto shrink-0 text-muted-foreground">
                    {relativeTime(run.fired_at)}
                  </span>
                </div>
                <p className="mt-1 text-xs text-muted-foreground">
                  {describeRunTrigger(run.trigger_type)}
                </p>
                <div className="mt-2">
                  <RunActionChips actions={run.actions} />
                </div>
                {run.actions
                  .filter((a) => !a.ok)
                  .map((a, i) => (
                    <p
                      key={i}
                      className="mt-1 text-xs text-destructive"
                    >
                      {a.type} failed
                      {a.error ? `: ${a.error}` : ""}
                    </p>
                  ))}
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
    </>
  );
}
