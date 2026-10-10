// Automations management (C11T1): per-project workflow automation rules
// for project settings. When an issue's state changes, matching rules
// run their actions automatically (assign user / add label / post
// comment). Reads and mutations are member (15)+; the server is the
// gate — guests get a read-only view and 403s. Failures surface as
// honest toasts, never silent.

import { useState } from "react";
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
  updateAutomationRule,
  type AutomationAction,
  type AutomationRule,
  type AutomationRuleInput,
  type AutomationTrigger,
} from "../../lib/automations";
import {
  fetchLabels,
  fetchStates,
  taxonomyKeys,
  type TaxLabel,
  type TaxState,
} from "../../lib/taxonomy";
import type { WorkspaceMember } from "../../lib/types";
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
import { Textarea } from "../ui/textarea";

function failToast(title: string, err: unknown, fallback: string) {
  toast.add({
    title,
    description: err instanceof ApiError ? err.message : fallback,
    type: "error",
  });
}

const selectClass =
  "rounded-md border border-input bg-background px-2 py-1.5 text-sm";

interface DraftAction {
  type: AutomationAction["type"];
  user_id: string;
  label_id: string;
  body: string;
}

const emptyDraftAction = (): DraftAction => ({
  type: "add_comment",
  user_id: "",
  label_id: "",
  body: "",
});

interface RuleDraft {
  name: string;
  fromState: string; // "" = any
  toState: string; // "" = any
  actions: DraftAction[];
}

const emptyDraft = (): RuleDraft => ({
  name: "",
  fromState: "",
  toState: "",
  actions: [emptyDraftAction()],
});

function draftFromRule(rule: AutomationRule): RuleDraft {
  const t = rule.trigger;
  return {
    name: rule.name,
    fromState: t.from_states?.[0] ?? "",
    toState: t.to_states?.[0] ?? "",
    actions: rule.actions.map((a) => ({
      type: a.type,
      user_id: a.user_id ?? "",
      label_id: a.label_id ?? "",
      body: a.body ?? "",
    })),
  };
}

const ACTION_LABELS: Record<DraftAction["type"], string> = {
  assign: "Assign user",
  add_label: "Add label",
  add_comment: "Post comment",
};

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

  const [builderOpen, setBuilderOpen] = useState(false);
  const [editing, setEditing] = useState<AutomationRule | null>(null);
  const [draft, setDraft] = useState<RuleDraft>(emptyDraft);

  const rulesQuery = useQuery({
    queryKey: keys.rules,
    queryFn: () => fetchAutomationRules(slug, identifier),
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

  const states: TaxState[] = statesQuery.data ?? [];
  const labels: TaxLabel[] = labelsQuery.data ?? [];
  const members: WorkspaceMember[] = membersQuery.data ?? [];
  const rules: AutomationRule[] = rulesQuery.data ?? [];

  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: keys.rules });

  function openCreate() {
    setEditing(null);
    setDraft(emptyDraft());
    setBuilderOpen(true);
  }

  function openEdit(rule: AutomationRule) {
    setEditing(rule);
    setDraft(draftFromRule(rule));
    setBuilderOpen(true);
  }

  function draftToInput(d: RuleDraft): AutomationRuleInput | null {
    const trigger: AutomationTrigger = {
      type: "issue.state_changed",
      from_states: d.fromState ? [d.fromState] : null,
      to_states: d.toState ? [d.toState] : null,
    };
    const actions: AutomationAction[] = [];
    for (const a of d.actions) {
      if (a.type === "assign" && a.user_id) {
        actions.push({ type: "assign", user_id: a.user_id });
      } else if (a.type === "add_label" && a.label_id) {
        actions.push({ type: "add_label", label_id: a.label_id });
      } else if (a.type === "add_comment" && a.body.trim()) {
        actions.push({ type: "add_comment", body: a.body.trim() });
      } else {
        return null; // incomplete action — honest block, not silent drop
      }
    }
    return { name: d.name.trim(), trigger, actions };
  }

  const saveMutation = useMutation({
    mutationFn: () => {
      const input = draftToInput(draft);
      if (!input) throw new Error("incomplete action");
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
        e instanceof Error && e.message === "incomplete action"
          ? "Every action needs its target filled in (user, label, or comment text)."
          : "The rule was not saved.",
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

  function updateDraftAction(i: number, patch: Partial<DraftAction>) {
    setDraft((d) => ({
      ...d,
      actions: d.actions.map((a, j) => (j === i ? { ...a, ...patch } : a)),
    }));
  }

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between">
        <div>
          <CardTitle>Automations</CardTitle>
          <p className="mt-1 text-sm text-muted-foreground">
            When an issue changes state, do things automatically — assign a
            user, add a label, or post a comment. Free in glance.
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
              ? " Create one to react to state changes automatically."
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
                    {describeTrigger(rule.trigger, states)}
                  </p>
                  <ul className="mt-1 space-y-0.5 text-sm text-muted-foreground">
                    {rule.actions.map((a, i) => (
                      <li key={i}>
                        → {describeAction(a, members, labels)}
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
              <div className="grid grid-cols-2 gap-3">
                <div>
                  <Label htmlFor="auto-from">From state</Label>
                  <select
                    id="auto-from"
                    className={`${selectClass} w-full`}
                    value={draft.fromState}
                    onChange={(e) =>
                      setDraft((d) => ({ ...d, fromState: e.target.value }))
                    }
                  >
                    <option value="">Any state</option>
                    {states.map((s) => (
                      <option key={s.id} value={s.id}>
                        {s.name}
                      </option>
                    ))}
                  </select>
                </div>
                <div>
                  <Label htmlFor="auto-to">To state</Label>
                  <select
                    id="auto-to"
                    className={`${selectClass} w-full`}
                    value={draft.toState}
                    onChange={(e) =>
                      setDraft((d) => ({ ...d, toState: e.target.value }))
                    }
                  >
                    <option value="">Any state</option>
                    {states.map((s) => (
                      <option key={s.id} value={s.id}>
                        {s.name}
                      </option>
                    ))}
                  </select>
                </div>
              </div>
              <div>
                <Label>Actions (run in order)</Label>
                <div className="mt-2 space-y-3">
                  {draft.actions.map((a, i) => (
                    <div
                      key={i}
                      className="flex items-start gap-2 rounded-md border p-2"
                    >
                      <select
                        aria-label={`Action ${i + 1} type`}
                        className={selectClass}
                        value={a.type}
                        onChange={(e) =>
                          updateDraftAction(i, {
                            type: e.target
                              .value as DraftAction["type"],
                          })
                        }
                      >
                        {(
                          Object.keys(ACTION_LABELS) as DraftAction["type"][]
                        ).map((t) => (
                          <option key={t} value={t}>
                            {ACTION_LABELS[t]}
                          </option>
                        ))}
                      </select>
                      <div className="flex-1">
                        {a.type === "assign" && (
                          <select
                            aria-label={`Action ${i + 1} user`}
                            className={`${selectClass} w-full`}
                            value={a.user_id}
                            onChange={(e) =>
                              updateDraftAction(i, {
                                user_id: e.target.value,
                              })
                            }
                          >
                            <option value="">Select user…</option>
                            {members
                              .filter((m) => m.role >= 15)
                              .map((m) => (
                                <option key={m.id} value={m.id}>
                                  {m.name || m.email}
                                </option>
                              ))}
                          </select>
                        )}
                        {a.type === "add_label" && (
                          <select
                            aria-label={`Action ${i + 1} label`}
                            className={`${selectClass} w-full`}
                            value={a.label_id}
                            onChange={(e) =>
                              updateDraftAction(i, {
                                label_id: e.target.value,
                              })
                            }
                          >
                            <option value="">Select label…</option>
                            {labels.map((l) => (
                              <option key={l.id} value={l.id}>
                                {l.name}
                              </option>
                            ))}
                          </select>
                        )}
                        {a.type === "add_comment" && (
                          <Textarea
                            aria-label={`Action ${i + 1} comment`}
                            value={a.body}
                            onChange={(e) =>
                              updateDraftAction(i, { body: e.target.value })
                            }
                            placeholder="Comment text…"
                            rows={2}
                            maxLength={10000}
                          />
                        )}
                      </div>
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
  );
}
