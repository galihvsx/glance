// Custom fields management (C7T3): project settings CRUD for typed issue
// fields. Guests (5+) get a read-only view; mutations are member (15)+ and
// the server is the gate.
//
// Honest notes surfaced in the UI:
// - Deleting a field deletes all its values on issues (backend
//   ON DELETE CASCADE) — the confirm dialog says so.
// - Changing a field's type deletes its existing values (backend behavior);
//   the edit modal warns when the type is changed.
// - `required` is stored but NOT enforced server-side — rendered as a
//   visual marker only.

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Pencil, Plus, Trash2, X } from "lucide-react";
import { ApiError } from "../../lib/api";
import { isValidHexColor } from "../../lib/taxonomy";
import {
  CUSTOM_FIELD_TYPES,
  createCustomField,
  customFieldKeys,
  deleteCustomField,
  draftToInput,
  fetchCustomFields,
  updateCustomField,
  validateFieldDraft,
  type CustomField,
  type CustomFieldType,
  type FieldDraft,
} from "../../lib/customFields";
import { relativeTime } from "../../lib/relativeTime";
import { Alert, AlertDescription } from "../ui/alert";
import { Badge } from "../ui/badge";
import { Button } from "../ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "../ui/card";
import { Checkbox } from "../ui/checkbox";
import {
  Dialog,
  DialogContent,
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

const typeLabel = (t: CustomFieldType) =>
  CUSTOM_FIELD_TYPES.find((x) => x.value === t)?.label ?? t;

const newDraft = (): FieldDraft => ({
  name: "",
  field_type: "text",
  options: [],
  required: false,
});

function draftFromField(f: CustomField): FieldDraft {
  return {
    name: f.name,
    field_type: f.field_type,
    options: f.options.map((o) => ({ value: o.value, color: o.color ?? "" })),
    required: f.required,
  };
}

/** Select options editor: value + optional color rows, add/remove. */
function OptionsEditor({
  options,
  setOptions,
}: {
  options: { value: string; color: string }[];
  setOptions: (o: { value: string; color: string }[]) => void;
}) {
  function update(i: number, patch: Partial<{ value: string; color: string }>) {
    setOptions(options.map((o, j) => (j === i ? { ...o, ...patch } : o)));
  }
  return (
    <div className="space-y-2">
      <Label>Options</Label>
      {options.map((o, i) => (
        <div key={i} className="flex items-center gap-2">
          <Input
            value={o.value}
            onChange={(e) => update(i, { value: e.target.value })}
            placeholder={`Option ${i + 1}`}
            aria-label={`Option ${i + 1} value`}
          />
          <Input
            value={o.color}
            onChange={(e) => update(i, { color: e.target.value })}
            placeholder="#3b82f6"
            aria-label={`Option ${i + 1} color`}
            className="w-28 font-mono"
          />
          <span
            className="h-6 w-6 shrink-0 rounded-full border"
            style={{
              backgroundColor:
                o.color && isValidHexColor(o.color)
                  ? o.color
                  : "transparent",
            }}
            aria-hidden
          />
          <Button
            type="button"
            variant="ghost"
            size="icon"
            aria-label={`Remove option ${i + 1}`}
            onClick={() => setOptions(options.filter((_, j) => j !== i))}
          >
            <X className="h-4 w-4" />
          </Button>
        </div>
      ))}
      {options.some((o) => o.color && !isValidHexColor(o.color)) && (
        <p className="text-xs text-destructive">
          Colors must be hex like #3b82f6 — invalid ones are dropped on save.
        </p>
      )}
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={() =>
          setOptions([...options, { value: "", color: "#3b82f6" }])
        }
      >
        <Plus className="mr-1 h-3.5 w-3.5" /> Add option
      </Button>
    </div>
  );
}

export default function CustomFieldsSection({
  slug,
  identifier,
  canEdit,
}: {
  slug: string;
  identifier: string;
  canEdit: boolean;
}) {
  const queryClient = useQueryClient();
  const keys = customFieldKeys(slug, identifier);
  const [draft, setDraft] = useState<FieldDraft | null>(null);
  const [editing, setEditing] = useState<CustomField | null>(null);
  const [error, setError] = useState<string | null>(null);

  const fieldsQuery = useQuery({
    queryKey: keys.fields,
    queryFn: () => fetchCustomFields(slug, identifier),
  });

  const modalOpen = draft !== null;
  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: keys.fields });

  const saveMutation = useMutation({
    mutationFn: () => {
      if (!draft) throw new Error("no draft");
      const v = validateFieldDraft(draft);
      if (!v.ok) throw new Error(v.error);
      const input = draftToInput(draft);
      return editing
        ? updateCustomField(slug, identifier, editing.id, input)
        : createCustomField(slug, identifier, input);
    },
    onSuccess: () => {
      setDraft(null);
      setEditing(null);
      setError(null);
      void invalidate();
    },
    onError: (e) => setError(errMsg(e, "Failed to save custom field")),
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => deleteCustomField(slug, identifier, id),
    onSuccess: () => {
      setError(null);
      void invalidate();
    },
    onError: (e) => setError(errMsg(e, "Failed to delete custom field")),
  });

  function onDelete(f: CustomField) {
    if (
      !window.confirm(
        `Delete field "${f.name}"? All values set on issues for this field will be deleted too. This cannot be undone.`,
      )
    )
      return;
    deleteMutation.mutate(f.id);
  }

  function startEdit(f: CustomField) {
    setEditing(f);
    setDraft(draftFromField(f));
    setError(null);
  }

  const fields = fieldsQuery.data ?? [];
  const draftError = draft ? validateFieldDraft(draft) : { ok: true as const };
  const typeChanged = editing !== null && draft !== null && draft.field_type !== editing.field_type;

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between">
        <CardTitle>Custom fields</CardTitle>
        {canEdit && (
          <Button
            size="sm"
            onClick={() => {
              setEditing(null);
              setDraft(newDraft());
              setError(null);
            }}
          >
            <Plus className="mr-1 h-4 w-4" /> New field
          </Button>
        )}
      </CardHeader>
      <CardContent className="space-y-3">
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        {!canEdit && (
          <p className="text-sm text-muted-foreground">
            You are a guest here — custom fields are read-only. Members can
            manage them.
          </p>
        )}
        {fieldsQuery.isLoading ? (
          <Skeleton className="h-10 w-full" />
        ) : fieldsQuery.isError ? (
          <Alert variant="destructive">
            <AlertDescription>
              Failed to load custom fields.
              <Button
                variant="link"
                size="sm"
                onClick={() => void fieldsQuery.refetch()}
              >
                Retry
              </Button>
            </AlertDescription>
          </Alert>
        ) : fields.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No custom fields yet. Create one to let issues carry typed extra
            data — text, numbers, dates, select options, or checkboxes.
          </p>
        ) : (
          <ul className="divide-y rounded-md border">
            {fields.map((f) => (
              <li key={f.id} className="flex items-center gap-3 px-3 py-2">
                <div className="min-w-0 flex-1">
                  <p className="flex items-center gap-2 text-sm font-medium">
                    <span className="truncate">{f.name}</span>
                    <Badge variant="outline" className="shrink-0 text-[10px]">
                      {typeLabel(f.field_type)}
                    </Badge>
                    {f.required && (
                      <Badge
                        variant="secondary"
                        className="shrink-0 text-[10px]"
                        title="Visual indicator only — the API does not enforce it."
                      >
                        Required
                      </Badge>
                    )}
                  </p>
                  {f.field_type === "select" && (
                    <p className="mt-0.5 flex flex-wrap gap-1">
                      {f.options.map((o) => (
                        <span
                          key={o.value}
                          className="inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-xs text-muted-foreground"
                        >
                          {o.color && (
                            <span
                              className="h-2 w-2 rounded-full"
                              style={{ backgroundColor: o.color }}
                              aria-hidden
                            />
                          )}
                          {o.value}
                        </span>
                      ))}
                    </p>
                  )}
                  <p className="text-xs text-muted-foreground">
                    Created {relativeTime(f.created_at)}
                  </p>
                </div>
                {canEdit && (
                  <div className="flex shrink-0 gap-1">
                    <Button
                      variant="ghost"
                      size="icon"
                      aria-label={`Edit ${f.name}`}
                      onClick={() => startEdit(f)}
                    >
                      <Pencil className="h-4 w-4" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon"
                      aria-label={`Delete ${f.name}`}
                      onClick={() => onDelete(f)}
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </div>
                )}
              </li>
            ))}
          </ul>
        )}

        {/* Create / edit dialog */}
        <Dialog
          open={modalOpen}
          onOpenChange={(o) => {
            if (!o) {
              setDraft(null);
              setEditing(null);
            }
          }}
        >
          <DialogContent className="max-w-lg">
            <DialogHeader>
              <DialogTitle>
                {editing ? "Edit custom field" : "New custom field"}
              </DialogTitle>
            </DialogHeader>
            {draft && (
              <div className="max-h-[70vh] space-y-4 overflow-y-auto pr-1">
                <div className="space-y-2">
                  <Label htmlFor="cf-name">Name</Label>
                  <Input
                    id="cf-name"
                    value={draft.name}
                    onChange={(e) =>
                      setDraft({ ...draft, name: e.target.value })
                    }
                    placeholder="e.g. Customer tier"
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="cf-type">Type</Label>
                  <NativeSelect
                    id="cf-type"
                    value={draft.field_type}
                    onChange={(e) =>
                      setDraft({
                        ...draft,
                        field_type: e.target.value as CustomFieldType,
                      })
                    }
                    className="w-full"
                  >
                    {CUSTOM_FIELD_TYPES.map((t) => (
                      <NativeSelectOption key={t.value} value={t.value}>
                        {t.label}
                      </NativeSelectOption>
                    ))}
                  </NativeSelect>
                </div>
                {typeChanged && (
                  <Alert variant="destructive">
                    <AlertDescription>
                      Changing the type deletes all existing values for this
                      field.
                    </AlertDescription>
                  </Alert>
                )}
                {draft.field_type === "select" && (
                  <OptionsEditor
                    options={draft.options}
                    setOptions={(options) => setDraft({ ...draft, options })}
                  />
                )}
                <div className="flex items-center gap-2">
                  <Checkbox
                    id="cf-required"
                    checked={draft.required}
                    onCheckedChange={(v) =>
                      setDraft({ ...draft, required: v === true })
                    }
                  />
                  <Label htmlFor="cf-required" className="font-normal">
                    Required{" "}
                    <span className="text-muted-foreground">
                      (visual marker only — the API does not enforce it)
                    </span>
                  </Label>
                </div>
                {!draftError.ok && (
                  <p className="text-sm text-destructive">{draftError.error}</p>
                )}
              </div>
            )}
            <DialogFooter>
              <Button
                disabled={
                  !draft || !draftError.ok || saveMutation.isPending
                }
                onClick={() => saveMutation.mutate()}
              >
                {saveMutation.isPending
                  ? "Saving…"
                  : editing
                    ? "Save changes"
                    : "Create field"}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      </CardContent>
    </Card>
  );
}
