// FormBuilderDialog (C82T1): create/edit dialog for an intake form.
// Field picker: left column = available fields (title auto-included and
// locked; description; priority; project custom fields with type badges),
// right column = the form's ordered fields with drag-reorder via the
// existing dnd-kit sortable helpers (same pattern as Board.tsx — NOT
// duplicated), per-field required toggle (title locked on), editable
// display label, and remove button. A live preview toggle renders the
// form through the shared FormRenderer — the same component the public
// /f/:token page uses, so the preview is what submitters will see.

import { useEffect, useMemo, useState } from "react";
import {
  DndContext,
  KeyboardSensor,
  PointerSensor,
  closestCenter,
  useSensor,
  useSensors,
  type DragEndEvent,
} from "@dnd-kit/core";
import {
  SortableContext,
  arrayMove,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { Eye, EyeOff, GripVertical, Plus, X } from "lucide-react";
import { ApiError } from "../../lib/api";
import { CUSTOM_FIELD_TYPES, type CustomField } from "../../lib/customFields";
import {
  INTAKE_FORM_FIELD_TITLE,
  availableFields,
  canonicalFieldLabel,
  createIntakeForm,
  customFieldIdFromKey,
  defaultFormFields,
  isCustomFieldKey,
  toPublicFields,
  updateIntakeForm,
  type IntakeForm,
  type IntakeFormField,
} from "../../lib/intakeForms";
import { Badge } from "../ui/badge";
import { Button } from "../ui/button";
import { Alert, AlertDescription } from "../ui/alert";
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
import { Switch } from "../ui/switch";
import { Textarea } from "../ui/textarea";
import { cn } from "../../lib/utils";
import FormRenderer, { type FormRendererValues } from "./FormRenderer";

export interface FormBuilderDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  slug: string;
  identifier: string;
  /** null = create mode; a form = edit mode. */
  form?: IntakeForm | null;
  customFields: CustomField[];
  onSaved: (form: IntakeForm) => void;
}

const typeLabel = (t: string) =>
  CUSTOM_FIELD_TYPES.find((x) => x.value === t)?.label ?? t;

// ---------- Sortable form-field row ----------

function SortableFieldRow({
  field,
  onLabelChange,
  onRequiredChange,
  onRemove,
}: {
  field: IntakeFormField;
  onLabelChange: (label: string) => void;
  onRequiredChange: (required: boolean) => void;
  onRemove: () => void;
}) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } =
    useSortable({ id: field.key });
  const locked = field.key === INTAKE_FORM_FIELD_TITLE;

  return (
    <div
      ref={setNodeRef}
      data-field-row={field.key}
      style={{
        transform: CSS.Transform.toString(transform),
        transition,
        opacity: isDragging ? 0.5 : 1,
      }}
      className={cn(
        "flex items-center gap-2 rounded-md border border-border bg-surface p-2",
        isDragging && "shadow-lg",
      )}
    >
      {/* Drag handle only — inputs stay fully interactive. */}
      <button
        type="button"
        className={cn(
          "flex h-7 w-6 shrink-0 cursor-grab items-center justify-center text-ink-muted hover:text-ink",
          locked && "cursor-not-allowed opacity-50",
        )}
        aria-label={locked ? "Title is fixed at the top" : `Drag to reorder ${field.label}`}
        disabled={locked}
        {...attributes}
        {...listeners}
      >
        <GripVertical className="h-4 w-4" aria-hidden />
      </button>
      <div className="min-w-0 flex-1">
        <Input
          value={field.label}
          onChange={(e) => onLabelChange(e.target.value)}
          aria-label="Display label"
          className="h-8"
        />
      </div>
      <div className="flex shrink-0 items-center gap-1.5">
        <Label className="flex items-center gap-1.5 text-xs text-ink-secondary">
          <Switch
            checked={field.required}
            onCheckedChange={onRequiredChange}
            disabled={locked}
            aria-label={`Required: ${field.label}`}
          />
          Required
        </Label>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="h-7 w-7"
          onClick={onRemove}
          disabled={locked}
          title={locked ? "Title is always included" : "Remove field"}
          aria-label={`Remove ${field.label}`}
        >
          <X className="h-4 w-4" aria-hidden />
        </Button>
      </div>
    </div>
  );
}

// ---------- Dialog ----------

export default function FormBuilderDialog({
  open,
  onOpenChange,
  slug,
  identifier,
  form = null,
  customFields,
  onSaved,
}: FormBuilderDialogProps) {
  const customFieldsById = useMemo(
    () => new Map(customFields.map((cf) => [cf.id, cf])),
    [customFields],
  );

  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [isEnabled, setIsEnabled] = useState(true);
  const [fields, setFields] = useState<IntakeFormField[]>(defaultFormFields());
  const [preview, setPreview] = useState(false);
  const [previewValues, setPreviewValues] = useState<FormRendererValues>({});
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Reset the draft whenever the dialog opens or the edited form changes.
  useEffect(() => {
    if (!open) return;
    setName(form?.name ?? "");
    setDescription(form?.description ?? "");
    setIsEnabled(form?.is_enabled ?? true);
    setFields(
      form ? form.fields.map((f) => ({ ...f })) : defaultFormFields(),
    );
    setPreview(false);
    setPreviewValues({});
    setError(null);
    setSaving(false);
  }, [open, form]);

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );

  const includedKeys = useMemo(
    () => new Set(fields.map((f) => f.key)),
    [fields],
  );
  const offered = useMemo(() => availableFields(customFields), [customFields]);

  function addField(key: string) {
    if (includedKeys.has(key)) return;
    setFields((prev) => [
      ...prev,
      {
        key,
        label: canonicalFieldLabel(key, customFieldsById),
        required: false,
      },
    ]);
  }

  function onDragEnd(e: DragEndEvent) {
    const { active, over } = e;
    if (!over || active.id === over.id) return;
    setFields((prev) => {
      const from = prev.findIndex((f) => f.key === String(active.id));
      const to = prev.findIndex((f) => f.key === String(over.id));
      if (from < 0 || to < 0) return prev;
      return arrayMove(prev, from, to);
    });
  }

  // Public-page fidelity: deleted custom fields are dropped from the
  // public view (backend GetPublicIntakeForm skips them), so the preview
  // drops them too — and the banner below tells the admin.
  const deletedKeys = useMemo(
    () =>
      fields.filter(
        (f) => isCustomFieldKey(f.key) && !customFieldsById.has(customFieldIdFromKey(f.key)),
      ),
    [fields, customFieldsById],
  );
  const previewFields = useMemo(
    () =>
      toPublicFields(fields, customFieldsById).filter(
        (f) =>
          !(
            isCustomFieldKey(f.key) &&
            !customFieldsById.has(customFieldIdFromKey(f.key))
          ),
      ),
    [fields, customFieldsById],
  );

  async function handleSave() {
    if (!name.trim()) {
      setError("Give the form a name.");
      return;
    }
    setSaving(true);
    setError(null);
    try {
      const payload = {
        name: name.trim(),
        description: description.trim() === "" ? null : description.trim(),
        is_enabled: isEnabled,
        fields: fields.map((f) => ({ ...f, label: f.label.trim() || canonicalFieldLabel(f.key, customFieldsById) })),
      };
      const saved = form
        ? await updateIntakeForm(slug, identifier, form.id, payload)
        : await createIntakeForm(slug, identifier, payload);
      onSaved(saved);
      onOpenChange(false);
    } catch (e) {
      setError(
        e instanceof ApiError ? e.message : "Saving the form failed.",
      );
    } finally {
      setSaving(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90vh] max-w-3xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{form ? "Edit form" : "New intake form"}</DialogTitle>
          <DialogDescription>
            Choose the fields submitters fill in. The public link accepts
            submissions from anyone — no account needed.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-5">
          {/* Name / description / enabled */}
          <div className="grid gap-4">
            <div className="space-y-2">
              <Label htmlFor="form-name">Name</Label>
              <Input
                id="form-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="Bug report"
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="form-description">Description</Label>
              <Textarea
                id="form-description"
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                placeholder="What this form is for — shown to submitters."
                rows={2}
              />
            </div>
            <div className="flex items-center gap-2">
              <Switch
                id="form-enabled"
                checked={isEnabled}
                onCheckedChange={setIsEnabled}
              />
              <Label htmlFor="form-enabled">
                Enabled
                <span className="ml-1 font-normal text-ink-muted">
                  (disabled forms reject submissions with an unavailable-link page)
                </span>
              </Label>
            </div>
          </div>

          {/* Field picker */}
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-2">
              <h3 className="text-sm font-medium">Available fields</h3>
              <div className="space-y-1.5">
                {/* Title is auto-included: always present, always required. */}
                <div className="flex items-center justify-between rounded-md border border-border bg-surface px-2.5 py-1.5">
                  <span className="flex items-center gap-2 text-sm">
                    Title
                    <Badge variant="secondary" className="text-[10px]">
                      required · locked
                    </Badge>
                  </span>
                </div>
                {offered.map((f) => (
                  <div
                    key={f.key}
                    className="flex items-center justify-between rounded-md border border-border bg-surface px-2.5 py-1.5"
                  >
                    <span className="flex min-w-0 items-center gap-2 text-sm">
                      <span className="truncate">{f.label}</span>
                      {f.fieldType && (
                        <Badge variant="outline" className="shrink-0 text-[10px]">
                          {typeLabel(f.fieldType)}
                        </Badge>
                      )}
                    </span>
                    {includedKeys.has(f.key) ? (
                      <span className="shrink-0 text-xs text-ink-muted">Added</span>
                    ) : (
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        className="h-7 w-7"
                        onClick={() => addField(f.key)}
                        aria-label={`Add ${f.label} to the form`}
                      >
                        <Plus className="h-4 w-4" aria-hidden />
                      </Button>
                    )}
                  </div>
                ))}
              </div>
            </div>

            <div className="space-y-2">
              <h3 className="text-sm font-medium">
                Form fields{" "}
                <span className="font-normal text-ink-muted">
                  ({fields.length})
                </span>
              </h3>
              <DndContext
                sensors={sensors}
                collisionDetection={closestCenter}
                onDragEnd={onDragEnd}
              >
                <SortableContext
                  items={fields.map((f) => f.key)}
                  strategy={verticalListSortingStrategy}
                >
                  <div className="space-y-1.5">
                    {fields.map((f) => (
                      <SortableFieldRow
                        key={f.key}
                        field={f}
                        onLabelChange={(label) =>
                          setFields((prev) =>
                            prev.map((x) =>
                              x.key === f.key ? { ...x, label } : x,
                            ),
                          )
                        }
                        onRequiredChange={(required) =>
                          setFields((prev) =>
                            prev.map((x) =>
                              x.key === f.key ? { ...x, required } : x,
                            ),
                          )
                        }
                        onRemove={() =>
                          setFields((prev) =>
                            prev.filter((x) => x.key !== f.key),
                          )
                        }
                      />
                    ))}
                  </div>
                </SortableContext>
              </DndContext>
              <p className="text-xs text-ink-muted">
                Drag to reorder. Titles stay first and required.
              </p>
            </div>
          </div>

          {/* Live preview — the same FormRenderer the public page uses. */}
          <div className="space-y-3 border-t border-border-hairline pt-4">
            <div className="flex items-center gap-2">
              <Switch
                id="form-preview"
                checked={preview}
                onCheckedChange={setPreview}
              />
              <Label
                htmlFor="form-preview"
                className="flex items-center gap-1.5"
              >
                {preview ? (
                  <Eye className="h-4 w-4" aria-hidden />
                ) : (
                  <EyeOff className="h-4 w-4" aria-hidden />
                )}
                Live preview
              </Label>
            </div>
            {preview && (
              <div className="rounded-md border border-border bg-wash p-4">
                {deletedKeys.length > 0 && (
                  <Alert variant="destructive" className="mb-3">
                    <AlertDescription>
                      {deletedKeys.length === 1 ? "This field" : "These fields"}{" "}
                      no longer {deletedKeys.length === 1 ? "exists" : "exist"} on the
                      project and won&apos;t be shown to submitters:{" "}
                      {deletedKeys.map((f) => f.label).join(", ")}.
                    </AlertDescription>
                  </Alert>
                )}
                <FormRenderer
                  compact
                  fields={previewFields}
                  values={previewValues}
                  onChange={(key, value) =>
                    setPreviewValues((prev) => ({ ...prev, [key]: value }))
                  }
                />
              </div>
            )}
          </div>

          {error && (
            <Alert variant="destructive">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
        </div>

        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={saving}
          >
            Cancel
          </Button>
          <Button type="button" onClick={handleSave} disabled={saving}>
            {saving ? "Saving…" : form ? "Save changes" : "Create form"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
