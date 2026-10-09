// States management (C6T2): project state CRUD for project settings.
//
// Delete semantics (backend, C6T2 additions): deleting a state that any
// issue references is 409 — the UI then offers a reassign target and
// retries with ?reassign_to. The project's last state can never be
// deleted. Mutations are member (15)+; the server is the gate.

import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Pencil, Plus, Trash2 } from "lucide-react";
import { ApiError } from "../../lib/api";
import {
  STATE_GROUPS,
  createState,
  deleteState,
  fetchStates,
  groupLabel,
  isValidHexColor,
  taxonomyKeys,
  updateState,
  type TaxState,
} from "../../lib/taxonomy";
import { Alert, AlertDescription } from "../ui/alert";
import { Button } from "../ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "../ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../ui/dialog";
import { Input } from "../ui/input";
import { Label } from "../ui/label";
import {
  NativeSelect,
  NativeSelectOption,
} from "../ui/native-select";
import { Skeleton } from "../ui/skeleton";

function errMsg(err: unknown, fallback: string): string {
  return err instanceof ApiError ? err.message : fallback;
}

interface StateForm {
  name: string;
  group: string;
  color: string;
}

const EMPTY_FORM: StateForm = { name: "", group: "backlog", color: "#6b7280" };

export default function StatesSection({
  slug,
  identifier,
  canEdit,
}: {
  slug: string;
  identifier: string;
  canEdit: boolean;
}) {
  const queryClient = useQueryClient();
  const keys = taxonomyKeys(slug, identifier);
  const [creating, setCreating] = useState(false);
  const [form, setForm] = useState<StateForm>(EMPTY_FORM);
  const [editing, setEditing] = useState<TaxState | null>(null);
  const [editForm, setEditForm] = useState<StateForm>(EMPTY_FORM);
  // 409 → reassign flow: the state the user tried to delete.
  const [reassignFor, setReassignFor] = useState<TaxState | null>(null);
  const [reassignTo, setReassignTo] = useState("");
  const [error, setError] = useState<string | null>(null);

  const statesQuery = useQuery({
    queryKey: keys.states,
    queryFn: () => fetchStates(slug, identifier),
  });
  const states = useMemo(
    () => [...(statesQuery.data ?? [])].sort((a, b) => a.sequence - b.sequence),
    [statesQuery.data],
  );

  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: keys.states });

  const createMutation = useMutation({
    mutationFn: () =>
      createState(slug, identifier, {
        name: form.name.trim(),
        group: form.group,
        color: form.color,
      }),
    onSuccess: () => {
      setForm(EMPTY_FORM);
      setCreating(false);
      setError(null);
      void invalidate();
    },
    onError: (e) => setError(errMsg(e, "Failed to create state")),
  });

  const updateMutation = useMutation({
    mutationFn: () =>
      updateState(slug, identifier, editing!.id, {
        name: editForm.name.trim(),
        group: editForm.group,
        color: editForm.color,
      }),
    onSuccess: () => {
      setEditing(null);
      setError(null);
      void invalidate();
    },
    onError: (e) => setError(errMsg(e, "Failed to update state")),
  });

  const deleteMutation = useMutation({
    mutationFn: ({ id, target }: { id: string; target?: string }) =>
      deleteState(slug, identifier, id, target),
    onSuccess: () => {
      setReassignFor(null);
      setReassignTo("");
      setError(null);
      void invalidate();
    },
    onError: (e, vars) => {
      // 409 in-use → open the reassign dialog instead of a dead error.
      if (e instanceof ApiError && e.status === 409 && !vars.target) {
        const s = states.find((x) => x.id === vars.id) ?? null;
        setReassignFor(s);
        setReassignTo("");
        return;
      }
      setError(errMsg(e, "Failed to delete state"));
    },
  });

  function onDelete(s: TaxState) {
    if (
      !window.confirm(
        `Delete state "${s.name}"? Issues in this state must be moved first — you'll pick a target state next if any exist.`,
      )
    )
      return;
    deleteMutation.mutate({ id: s.id });
  }

  const grouped = useMemo(() => {
    const map = new Map<string, TaxState[]>();
    for (const g of STATE_GROUPS) map.set(g, []);
    for (const s of states) {
      const list = map.get(s.group) ?? [];
      list.push(s);
      map.set(s.group, list);
    }
    return [...map.entries()].filter(([, list]) => list.length > 0);
  }, [states]);

  const reassignTargets = useMemo(
    () => states.filter((s) => s.id !== reassignFor?.id),
    [states, reassignFor],
  );

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between">
        <CardTitle>States</CardTitle>
        {canEdit && (
          <Button size="sm" onClick={() => setCreating(true)}>
            <Plus className="mr-1 h-4 w-4" /> New state
          </Button>
        )}
      </CardHeader>
      <CardContent className="space-y-4">
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        {!canEdit && (
          <p className="text-sm text-muted-foreground">
            You are a guest here — states are read-only. Members can manage them.
          </p>
        )}
        {statesQuery.isLoading ? (
          <Skeleton className="h-24 w-full" />
        ) : (
          grouped.map(([group, list]) => (
            <div key={group} className="space-y-1">
              <h3 className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                {groupLabel(group)}
              </h3>
              <ul className="divide-y rounded-md border">
                {list.map((s) => (
                  <li key={s.id} className="flex items-center gap-3 px-3 py-2">
                    <span
                      className="h-3.5 w-3.5 shrink-0 rounded-full border"
                      style={{ backgroundColor: s.color || "#6b7280" }}
                      aria-hidden
                    />
                    <span className="flex-1 text-sm font-medium">{s.name}</span>
                    {canEdit && (
                      <div className="flex gap-1">
                        <Button
                          variant="ghost"
                          size="icon"
                          aria-label={`Edit ${s.name}`}
                          onClick={() => {
                            setEditing(s);
                            setEditForm({
                              name: s.name,
                              group: s.group,
                              color: s.color || "#6b7280",
                            });
                          }}
                        >
                          <Pencil className="h-4 w-4" />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon"
                          aria-label={`Delete ${s.name}`}
                          onClick={() => onDelete(s)}
                        >
                          <Trash2 className="h-4 w-4" />
                        </Button>
                      </div>
                    )}
                  </li>
                ))}
              </ul>
            </div>
          ))
        )}

        <StateDialog
          open={creating}
          title="New state"
          form={form}
          setForm={setForm}
          pending={createMutation.isPending}
          onClose={() => setCreating(false)}
          onSubmit={() => createMutation.mutate()}
        />
        <StateDialog
          open={editing !== null}
          title="Edit state"
          form={editForm}
          setForm={setEditForm}
          pending={updateMutation.isPending}
          onClose={() => setEditing(null)}
          onSubmit={() => updateMutation.mutate()}
        />

        {/* Reassign-on-409 dialog */}
        <Dialog
          open={reassignFor !== null}
          onOpenChange={(o) => !o && setReassignFor(null)}
        >
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Move issues first</DialogTitle>
              <DialogDescription>
                State “{reassignFor?.name}” still has issues in it. Choose a
                state to move them to — they keep everything else.
              </DialogDescription>
            </DialogHeader>
            <div className="space-y-1">
              <Label htmlFor="reassign-to">Move issues to</Label>
              <NativeSelect
                id="reassign-to"
                value={reassignTo}
                onChange={(e) => setReassignTo(e.target.value)}
              >
                <NativeSelectOption value="">Select a state…</NativeSelectOption>
                {reassignTargets.map((s) => (
                  <NativeSelectOption key={s.id} value={s.id}>
                    {s.name} ({groupLabel(s.group)})
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </div>
            <DialogFooter>
              <Button
                disabled={!reassignTo || deleteMutation.isPending}
                onClick={() =>
                  reassignFor &&
                  deleteMutation.mutate({ id: reassignFor.id, target: reassignTo })
                }
              >
                {deleteMutation.isPending ? "Moving…" : "Move issues & delete"}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      </CardContent>
    </Card>
  );
}

function StateDialog({
  open,
  title,
  form,
  setForm,
  pending,
  onClose,
  onSubmit,
}: {
  open: boolean;
  title: string;
  form: StateForm;
  setForm: (f: StateForm) => void;
  pending: boolean;
  onClose: () => void;
  onSubmit: () => void;
}) {
  const valid = form.name.trim() !== "" && isValidHexColor(form.color);
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <div className="space-y-1">
            <Label htmlFor="state-name">Name</Label>
            <Input
              id="state-name"
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
              placeholder="In review"
            />
          </div>
          <div className="space-y-1">
            <Label htmlFor="state-group">Group</Label>
            <NativeSelect
              id="state-group"
              value={form.group}
              onChange={(e) => setForm({ ...form, group: e.target.value })}
            >
              {STATE_GROUPS.map((g) => (
                <NativeSelectOption key={g} value={g}>
                  {groupLabel(g)}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </div>
          <div className="space-y-1">
            <Label htmlFor="state-color">Color</Label>
            <Input
              id="state-color"
              value={form.color}
              onChange={(e) => setForm({ ...form, color: e.target.value })}
              className="w-28 font-mono"
            />
            {!isValidHexColor(form.color) && (
              <p className="text-xs text-destructive">
                Use a hex color like #a78bfa.
              </p>
            )}
          </div>
        </div>
        <DialogFooter>
          <Button disabled={!valid || pending} onClick={onSubmit}>
            {pending ? "Saving…" : "Save"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
