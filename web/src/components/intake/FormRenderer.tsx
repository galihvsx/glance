// FormRenderer (C82T1): the single shared renderer for intake-form fields.
// Used by BOTH the builder's live preview (FormBuilderDialog) and the
// public submission page (/f/:token — PublicIntakeForm), so the preview
// renders exactly what the public page will: zero divergence.
//
// Controlled: the parent owns `values` (Record<field key, value>) and
// receives edits via onChange. Field value shapes:
//
//   title/description/text/date/select -> string
//   number    -> string draft (raw input; parsed at submit)
//   priority  -> number | "" (empty = unset)
//   checkbox  -> boolean

import { PRIORITY_LABELS } from "../../lib/types";
import type { PublicIntakeFormField } from "../../lib/intakeForms";
import { Checkbox } from "../ui/checkbox";
import { Input } from "../ui/input";
import { Label } from "../ui/label";
import {
  NativeSelect,
  NativeSelectOption,
} from "../ui/native-select";
import { Textarea } from "../ui/textarea";

export type FormRendererValues = Record<string, unknown>;

export interface FormRendererProps {
  fields: PublicIntakeFormField[];
  values: FormRendererValues;
  onChange: (key: string, value: unknown) => void;
  disabled?: boolean;
  /** Compact rendering inside the builder preview. */
  compact?: boolean;
}

function RequiredMark() {
  return (
    <span className="ml-0.5 text-destructive" title="Required">
      *
    </span>
  );
}

function FieldLabel({
  field,
  htmlFor,
}: {
  field: PublicIntakeFormField;
  htmlFor?: string;
}) {
  return (
    <Label htmlFor={htmlFor}>
      {field.label}
      {field.required && <RequiredMark />}
    </Label>
  );
}

function PublicField({
  field,
  value,
  onChange,
  disabled,
}: {
  field: PublicIntakeFormField;
  value: unknown;
  onChange: (value: unknown) => void;
  disabled: boolean;
}) {
  const inputId = `intake-form-field-${field.key.replace(/[^a-zA-Z0-9]/g, "_")}`;

  switch (field.type) {
    case "title":
    case "text":
      return (
        <div className="space-y-1.5">
          <FieldLabel field={field} htmlFor={inputId} />
          <Input
            id={inputId}
            value={typeof value === "string" ? value : ""}
            onChange={(e) => onChange(e.target.value)}
            disabled={disabled}
            placeholder={field.label}
          />
        </div>
      );
    case "description":
      return (
        <div className="space-y-1.5">
          <FieldLabel field={field} htmlFor={inputId} />
          <Textarea
            id={inputId}
            value={typeof value === "string" ? value : ""}
            onChange={(e) => onChange(e.target.value)}
            disabled={disabled}
            placeholder={field.label}
            rows={4}
          />
        </div>
      );
    case "priority":
      return (
        <div className="space-y-1.5">
          <FieldLabel field={field} htmlFor={inputId} />
          <NativeSelect
            id={inputId}
            value={value === "" || value === undefined || value === null ? "" : String(value)}
            onChange={(e) =>
              onChange(e.target.value === "" ? "" : Number(e.target.value))
            }
            disabled={disabled}
          >
            <NativeSelectOption value="">Select priority…</NativeSelectOption>
            {PRIORITY_LABELS.map((label, i) => (
              <NativeSelectOption key={i} value={String(i)}>
                {label}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </div>
      );
    case "number":
      return (
        <div className="space-y-1.5">
          <FieldLabel field={field} htmlFor={inputId} />
          <Input
            id={inputId}
            type="number"
            value={typeof value === "string" || typeof value === "number" ? String(value) : ""}
            onChange={(e) => onChange(e.target.value)}
            disabled={disabled}
            placeholder={field.label}
          />
        </div>
      );
    case "date":
      return (
        <div className="space-y-1.5">
          <FieldLabel field={field} htmlFor={inputId} />
          <Input
            id={inputId}
            type="date"
            value={typeof value === "string" ? value : ""}
            onChange={(e) => onChange(e.target.value)}
            disabled={disabled}
          />
        </div>
      );
    case "select":
      return (
        <div className="space-y-1.5">
          <FieldLabel field={field} htmlFor={inputId} />
          <NativeSelect
            id={inputId}
            value={typeof value === "string" ? value : ""}
            onChange={(e) => onChange(e.target.value)}
            disabled={disabled}
          >
            <NativeSelectOption value="">Select…</NativeSelectOption>
            {field.options.map((o) => (
              <NativeSelectOption key={o.value} value={o.value}>
                {o.value}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </div>
      );
    case "checkbox":
      return (
        <div className="flex items-center gap-2 py-1">
          <Checkbox
            id={inputId}
            checked={value === true}
            onCheckedChange={(c) => onChange(c === true)}
            disabled={disabled}
          />
          <Label htmlFor={inputId}>
            {field.label}
            {field.required && <RequiredMark />}
          </Label>
        </div>
      );
    default:
      return null;
  }
}

export default function FormRenderer({
  fields,
  values,
  onChange,
  disabled = false,
  compact = false,
}: FormRendererProps) {
  return (
    <div className={compact ? "space-y-3" : "space-y-5"}>
      {fields.map((field) => (
        <PublicField
          key={field.key}
          field={field}
          value={values[field.key]}
          onChange={(v) => onChange(field.key, v)}
          disabled={disabled}
        />
      ))}
    </div>
  );
}
