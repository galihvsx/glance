// Intake forms (C82T1): per-project shareable forms that feed the intake
// inbox. Types mirror the backend JSON shapes (snake_case):
//
//   GET|POST /api/v1/workspaces/:slug/projects/:identifier/intake/forms
//   GET|PATCH|DELETE .../intake/forms/:id
//   POST .../intake/forms/:id/regenerate
//   GET  /api/v1/forms/:token          (public, no auth)
//   POST /api/v1/forms/:token/submit   (public, no auth)
//
// Field keys: "title" (always present, always required), "description",
// "priority", and "custom:<custom_field_uuid>" for project custom fields.

import { api } from "./api";
import type { CustomField } from "./customFields";

export const INTAKE_FORM_FIELD_TITLE = "title";
export const INTAKE_FORM_FIELD_DESCRIPTION = "description";
export const INTAKE_FORM_FIELD_PRIORITY = "priority";
export const INTAKE_FORM_FIELD_CUSTOM_PREFIX = "custom:";

export type PublicIntakeFormFieldType =
  | "title"
  | "description"
  | "priority"
  | "text"
  | "number"
  | "date"
  | "select"
  | "checkbox";

/** One ordered field descriptor (manage view). */
export interface IntakeFormField {
  key: string;
  label: string;
  required: boolean;
}

/** One intake form (management view — never served publicly). */
export interface IntakeForm {
  id: string;
  project_id: string;
  intake_id: string;
  name: string;
  description?: string | null;
  is_enabled: boolean;
  share_token: string;
  fields: IntakeFormField[];
  created_by: string;
  created_at: string;
  updated_at: string;
}

/** Sanitized field descriptor served to anonymous submitters. */
export interface PublicIntakeFormField {
  key: string;
  label: string;
  required: boolean;
  type: PublicIntakeFormFieldType;
  options: { value: string; color?: string | null }[];
}

/** Sanitized envelope for GET /api/v1/forms/:token. */
export interface PublicIntakeForm {
  name: string;
  description?: string | null;
  project_name: string;
  fields: PublicIntakeFormField[];
}

export interface IntakeFormInput {
  name: string;
  description?: string | null;
  is_enabled?: boolean;
  fields: IntakeFormField[];
}

export interface IntakeFormPatch {
  name?: string;
  /** null/"" clears the description (backend PATCH semantics). */
  description?: string | null;
  is_enabled?: boolean;
  fields?: IntakeFormField[];
}

export function intakeFormsKey(slug: string, identifier: string) {
  return ["intake-forms", slug, identifier] as const;
}

export function publicIntakeFormKey(token: string) {
  return ["public-intake-form", token] as const;
}

function base(slug: string, identifier: string): string {
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/intake/forms`;
}

/** The public shareable URL for a form's share token. */
export function intakeFormPublicUrl(shareToken: string): string {
  return `${window.location.origin}/f/${shareToken}`;
}

export function fetchIntakeForms(
  slug: string,
  identifier: string,
): Promise<IntakeForm[]> {
  return api
    .get<{ forms: IntakeForm[] }>(base(slug, identifier))
    .then((r) => r.forms ?? []);
}

export function createIntakeForm(
  slug: string,
  identifier: string,
  input: IntakeFormInput,
): Promise<IntakeForm> {
  return api.post<IntakeForm>(base(slug, identifier), input);
}

export function updateIntakeForm(
  slug: string,
  identifier: string,
  id: string,
  patch: IntakeFormPatch,
): Promise<IntakeForm> {
  return api.patch<IntakeForm>(`${base(slug, identifier)}/${id}`, patch);
}

export function deleteIntakeForm(
  slug: string,
  identifier: string,
  id: string,
): Promise<void> {
  return api.del(`${base(slug, identifier)}/${id}`);
}

export function regenerateIntakeFormToken(
  slug: string,
  identifier: string,
  id: string,
): Promise<IntakeForm> {
  return api.post<IntakeForm>(`${base(slug, identifier)}/${id}/regenerate`);
}

export function fetchPublicIntakeForm(token: string): Promise<PublicIntakeForm> {
  return api.get<PublicIntakeForm>(
    `/api/v1/forms/${encodeURIComponent(token)}`,
  );
}

export function submitPublicIntakeForm(
  token: string,
  values: Record<string, unknown>,
): Promise<{ issue_id: string }> {
  return api.post<{ issue_id: string }>(
    `/api/v1/forms/${encodeURIComponent(token)}/submit`,
    { values },
  );
}

/** A fresh form always starts with exactly one required title field. */
export function defaultFormFields(): IntakeFormField[] {
  return [{ key: INTAKE_FORM_FIELD_TITLE, label: "Title", required: true }];
}

/** True when the key addresses a project custom field. */
export function isCustomFieldKey(key: string): boolean {
  return key.startsWith(INTAKE_FORM_FIELD_CUSTOM_PREFIX);
}

/** The custom-field id carried by a "custom:<uuid>" key. */
export function customFieldIdFromKey(key: string): string {
  return key.slice(INTAKE_FORM_FIELD_CUSTOM_PREFIX.length);
}

/** Canonical display label for a field key (picker + builder defaults). */
export function canonicalFieldLabel(
  key: string,
  customFieldsById: Map<string, CustomField>,
): string {
  switch (key) {
    case INTAKE_FORM_FIELD_TITLE:
      return "Title";
    case INTAKE_FORM_FIELD_DESCRIPTION:
      return "Description";
    case INTAKE_FORM_FIELD_PRIORITY:
      return "Priority";
    default:
      if (isCustomFieldKey(key)) {
        return customFieldsById.get(customFieldIdFromKey(key))?.name ?? key;
      }
      return key;
  }
}

/** Resolve the public render type for a manage-view field key. */
export function resolvePublicFieldType(
  key: string,
  customFieldsById: Map<string, CustomField>,
): PublicIntakeFormFieldType {
  switch (key) {
    case INTAKE_FORM_FIELD_TITLE:
      return "title";
    case INTAKE_FORM_FIELD_DESCRIPTION:
      return "description";
    case INTAKE_FORM_FIELD_PRIORITY:
      return "priority";
    default: {
      const cf = customFieldsById.get(customFieldIdFromKey(key));
      return (cf?.field_type ?? "text") as PublicIntakeFormFieldType;
    }
  }
}

/**
 * Map the builder's ordered manage-view fields to the public field shape
 * the shared FormRenderer consumes — the same shape GET /api/v1/forms/:token
 * serves, so the builder preview renders exactly what the public page will.
 * Deleted custom fields keep rendering with a warning (submit drops them
 * warning-free, mirroring the backend).
 */
export function toPublicFields(
  fields: IntakeFormField[],
  customFieldsById: Map<string, CustomField>,
): PublicIntakeFormField[] {
  return fields.map((f) => {
    const cf = isCustomFieldKey(f.key)
      ? customFieldsById.get(customFieldIdFromKey(f.key))
      : undefined;
    return {
      key: f.key,
      label: f.label,
      required: f.required,
      type: resolvePublicFieldType(f.key, customFieldsById),
      options:
        cf?.field_type === "select"
          ? (cf.options ?? []).map((o) => ({
              value: o.value,
              color: o.color ?? null,
            }))
          : [],
    };
  });
}

/** Fields offered in the builder's available-fields column, minus `title`. */
export interface AvailableField {
  key: string;
  label: string;
  /** Present for custom fields; undefined for core fields. */
  fieldType?: PublicIntakeFormFieldType;
}

export function availableFields(
  customFields: CustomField[],
): AvailableField[] {
  const out: AvailableField[] = [
    { key: INTAKE_FORM_FIELD_DESCRIPTION, label: "Description", fieldType: "description" },
    { key: INTAKE_FORM_FIELD_PRIORITY, label: "Priority", fieldType: "priority" },
  ];
  for (const cf of customFields) {
    out.push({
      key: `${INTAKE_FORM_FIELD_CUSTOM_PREFIX}${cf.id}`,
      label: cf.name,
      fieldType: cf.field_type,
    });
  }
  return out;
}

/**
 * Build the submit payload from renderer values: empty optional values are
 * omitted (the backend skips blank values); checkboxes always send a
 * boolean (false is an explicit choice and counts as present); priority
 * sends a JSON number 0-4; numbers send JSON numbers.
 */
export function buildSubmitValues(
  publicFields: PublicIntakeFormField[],
  values: Record<string, unknown>,
): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const f of publicFields) {
    const v = values[f.key];
    switch (f.type) {
      case "checkbox":
        out[f.key] = v === true;
        break;
      case "number": {
        const s = String(v ?? "").trim();
        if (s !== "") out[f.key] = Number(s);
        break;
      }
      case "priority": {
        if (v !== undefined && v !== null && String(v) !== "") {
          out[f.key] = Number(v);
        }
        break;
      }
      default: {
        const s = typeof v === "string" ? v : String(v ?? "");
        if (s.trim() !== "") out[f.key] = s;
        break;
      }
    }
  }
  return out;
}

/** Client-side required check before POSTing a submission; returns the
 *  labels of required fields whose current value is empty. */
export function missingRequiredLabels(
  publicFields: PublicIntakeFormField[],
  values: Record<string, unknown>,
): string[] {
  const missing: string[] = [];
  for (const f of publicFields) {
    if (!f.required) continue;
    const v = values[f.key];
    if (f.type === "checkbox") continue; // false counts as present
    const s = typeof v === "string" ? v : String(v ?? "");
    if (s.trim() === "") missing.push(f.label);
  }
  return missing;
}
