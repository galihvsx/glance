// custom fields client (C7T3) — project settings CRUD + per-issue values.
//
// Contract (verified 2026-10-09 against the C7T2 backend,
// internal/service/custom_fields.go + internal/api/customfield_handler.go):
//   GET/POST  /api/v1/workspaces/:slug/projects/:identifier/custom-fields
//             list -> {custom_fields: [...]}, create -> 201.
//             create body: {name, field_type, options?, required?, position?}
//   GET/PATCH/DELETE .../custom-fields/:fieldID (PATCH {} -> 400).
//   PUT .../issues/:uuid/custom-values {values: {field_id: value}} -> 200
//             with {custom_values: {...}}; a JSON null clears that field.
//   DELETE .../issues/:uuid/custom-values/:fieldID -> 204.
//   GET .../issues/:uuid?include_custom=1 -> detail + `custom_values`
//             keyed by field_id: {field_id: {field_id, name, field_type, value}}.
//   Value shapes: text -> string, number -> JSON number, date -> "YYYY-MM-DD"
//             string, select -> option string, checkbox -> boolean.
//   `required` is stored and exposed so clients can render it, but it is
//   NOT enforced server-side on value writes — render it as a visual
//   indicator only.
// Reads: any member (guest 5+); mutations: member (15)+, server-gated.
//
// The pure helpers at the bottom (validateCustomValueInput, valueToDraft,
// formatCustomValue, validateFieldDraft) mirror the server's type checks so
// the UI can validate locally first; the server's 400 message is still
// surfaced verbatim. They carry no React/DOM so vitest can cover them.

import { api } from "./api";
import { isValidHexColor } from "./taxonomy";

export type CustomFieldType = "text" | "number" | "date" | "select" | "checkbox";

export const CUSTOM_FIELD_TYPES: { value: CustomFieldType; label: string }[] = [
  { value: "text", label: "Text" },
  { value: "number", label: "Number" },
  { value: "date", label: "Date" },
  { value: "select", label: "Select" },
  { value: "checkbox", label: "Checkbox" },
];

/** One option of a select field. */
export interface CustomFieldOption {
  value: string;
  color?: string | null;
}

/** One project custom field (backend CustomField shape). */
export interface CustomField {
  id: string;
  project_id: string;
  name: string;
  field_type: CustomFieldType;
  options: CustomFieldOption[];
  required: boolean;
  position: number;
  created_at: string;
  updated_at: string;
}

/** One set custom value on an issue (backend CustomValue shape). */
export interface CustomValue {
  field_id: string;
  name: string;
  field_type: CustomFieldType;
  value: string | number | boolean;
}

function base(slug: string, identifier: string): string {
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/custom-fields`;
}

export function customFieldKeys(slug: string, identifier: string) {
  return {
    fields: ["custom-fields", slug, identifier] as const,
  };
}

export function fetchCustomFields(
  slug: string,
  identifier: string,
): Promise<CustomField[]> {
  return api
    .get<{ custom_fields: CustomField[] }>(base(slug, identifier))
    .then((r) => r.custom_fields ?? []);
}

export interface CustomFieldInput {
  name: string;
  field_type: CustomFieldType;
  options?: CustomFieldOption[];
  required?: boolean;
  position?: number;
}

export function createCustomField(
  slug: string,
  identifier: string,
  input: CustomFieldInput,
): Promise<CustomField> {
  return api.post<CustomField>(base(slug, identifier), input);
}

export function updateCustomField(
  slug: string,
  identifier: string,
  id: string,
  input: Partial<CustomFieldInput>,
): Promise<CustomField> {
  return api.patch<CustomField>(`${base(slug, identifier)}/${id}`, input);
}

export function deleteCustomField(
  slug: string,
  identifier: string,
  id: string,
): Promise<void> {
  return api.del<void>(`${base(slug, identifier)}/${id}`);
}

function valuesBase(slug: string, identifier: string, uuid: string): string {
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/issues/${encodeURIComponent(uuid)}/custom-values`;
}

/** One value in the PUT body: string | number | boolean, or null to clear. */
export type CustomValueInput = string | number | boolean | null;

export function setCustomValues(
  slug: string,
  identifier: string,
  uuid: string,
  values: Record<string, CustomValueInput>,
): Promise<Record<string, CustomValue>> {
  return api
    .put<{ custom_values: Record<string, CustomValue> }>(
      valuesBase(slug, identifier, uuid),
      { values },
    )
    .then((r) => r.custom_values ?? {});
}

// ---------- pure helpers (vitest-covered) ----------

/**
 * Editable draft string for a field from its stored value. Checkbox is
 * handled as a boolean separately; this covers the text-family inputs.
 */
export function valueToDraft(field: CustomField, value?: CustomValue): string {
  if (!value) return "";
  switch (field.field_type) {
    case "number":
      return typeof value.value === "number" ? String(value.value) : "";
    case "checkbox":
      return "";
    default:
      return typeof value.value === "string" ? value.value : "";
  }
}

export type ValidatedValue =
  | { ok: true; value: CustomValueInput }
  | { ok: false; error: string };

function isValidYMD(s: string): boolean {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(s);
  if (!m) return false;
  const dt = new Date(Date.UTC(Number(m[1]), Number(m[2]) - 1, Number(m[3])));
  return (
    dt.getUTCFullYear() === Number(m[1]) &&
    dt.getUTCMonth() === Number(m[2]) - 1 &&
    dt.getUTCDate() === Number(m[3])
  );
}

/**
 * Client-side mirror of the server's checkCustomValue. Returns the value to
 * send (or null to clear the field), or a validation error to show instead
 * of calling the API. An empty draft always means "clear" — the server
 * deletes the row on ""/null the same way.
 */
export function validateCustomValueInput(
  field: CustomField,
  raw: string,
): ValidatedValue {
  const trimmed = raw.trim();
  switch (field.field_type) {
    case "text":
      return { ok: true, value: trimmed === "" ? null : raw };
    case "number": {
      if (trimmed === "") return { ok: true, value: null };
      const n = Number(trimmed);
      if (!Number.isFinite(n))
        return { ok: false, error: `"${field.name}" must be a number.` };
      return { ok: true, value: n };
    }
    case "date": {
      if (trimmed === "") return { ok: true, value: null };
      if (!isValidYMD(trimmed))
        return {
          ok: false,
          error: `"${field.name}" must be a date in YYYY-MM-DD format.`,
        };
      return { ok: true, value: trimmed };
    }
    case "select": {
      if (trimmed === "") return { ok: true, value: null };
      const hit = field.options.find((o) => o.value === trimmed);
      if (!hit)
        return {
          ok: false,
          error: `"${field.name}" must be one of its options.`,
        };
      return { ok: true, value: hit.value };
    }
    case "checkbox":
      // Checkbox drafts are booleans handled by the caller, never strings.
      return { ok: false, error: `"${field.name}" must be a boolean.` };
  }
}

/** Render a YYYY-MM-DD string as "9 Oct 2026"; passes anything else through. */
export function formatYMD(s: string): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(s);
  if (!m) return s;
  // Construct as a local date (not UTC midnight) so the day never shifts
  // with the viewer's timezone.
  const dt = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]));
  if (Number.isNaN(dt.getTime())) return s;
  return dt.toLocaleDateString("en-GB", {
    day: "numeric",
    month: "short",
    year: "numeric",
  });
}

/**
 * C9T4: spreadsheet custom-field columns. Returns the fields whose column
 * is toggled on, ordered by position (name breaks ties for stability).
 * Default OFF: a missing id or explicit false means hidden; ids not in
 * `fields` (e.g. deleted fields) are ignored.
 */
export function resolveVisibleCustomFields(
  fields: CustomField[],
  visible: Record<string, boolean>,
): CustomField[] {
  return fields
    .filter((f) => visible[f.id] === true)
    .sort(
      (a, b) => a.position - b.position || a.name.localeCompare(b.name),
    );
}

/** Human display for a stored value; "—" when unset. */
export function formatCustomValue(field: CustomField, value?: CustomValue): string {
  if (!value) return "—";
  switch (field.field_type) {
    case "checkbox":
      return value.value === true ? "Yes" : "No";
    case "date":
      return formatYMD(String(value.value));
    default:
      return String(value.value);
  }
}

/** The create/edit modal's draft shape. */
export interface FieldDraft {
  name: string;
  field_type: CustomFieldType;
  options: { value: string; color: string }[];
  required: boolean;
}

export type FieldDraftError =
  | { ok: true }
  | { ok: false; error: string };

/**
 * Validate a settings draft before POST/PATCH: mirrors the server's
 * normalizeSelectOptions (select needs >=1 non-empty unique option; options
 * are only valid on select fields).
 */
export function validateFieldDraft(draft: FieldDraft): FieldDraftError {
  if (draft.name.trim() === "") return { ok: false, error: "Name is required." };
  if (draft.field_type === "select") {
    const values = draft.options.map((o) => o.value.trim()).filter((v) => v !== "");
    if (values.length === 0)
      return { ok: false, error: "Select fields need at least one option." };
    if (new Set(values).size !== values.length)
      return { ok: false, error: "Option values must be unique." };
  }
  return { ok: true };
}

/** Build the create body from a validated draft (trims option values,
 *  drops empty values and non-hex colors). */
export function draftToInput(draft: FieldDraft): CustomFieldInput {
  return {
    name: draft.name.trim(),
    field_type: draft.field_type,
    options:
      draft.field_type === "select"
        ? draft.options
            .map((o) => ({
              value: o.value.trim(),
              ...(isValidHexColor(o.color) ? { color: o.color } : {}),
            }))
            .filter((o) => o.value !== "")
        : [],
    required: draft.required,
  };
}
