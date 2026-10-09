// issue templates client (C7T1) — project settings CRUD + create-issue
// prefill.
//
// Contract (verified 2026-10-09 against the C7T0 backend,
// internal/service/template.go + internal/api/template_handler.go):
//   GET/POST  /api/v1/workspaces/:slug/projects/:identifier/templates
//             list -> {templates: [...]}, create -> 201
//   GET/PATCH/DELETE .../templates/:id
//   POST .../templates/:id/apply -> 201 created issue; optional overrides
//   Template JSON: {id, project_id, name, description, template_data,
//   created_by?, created_at, updated_at}
//   template_data: {name?, description? (tiptap doc), priority? (0-4),
//   estimate_point_id?, label_ids?[], state_id?}
// Reads: any member (guest 5+); mutations: member (15)+, server-gated.
//
// The prefill logic is deliberately pure (no React, no DOM) so vitest can
// cover it without a browser.

import { api } from "./api";
import { tiptapText } from "./tiptap";

/** Issue defaults stored in issue_templates.template_data. */
export interface TemplateData {
  name?: string;
  /** TipTap doc JSON. */
  description?: unknown;
  /** 0–4; absent = no default. */
  priority?: number;
  estimate_point_id?: string;
  label_ids?: string[];
  state_id?: string;
}

/** One project issue template (backend IssueTemplate shape). */
export interface IssueTemplate {
  id: string;
  project_id: string;
  name: string;
  description: string;
  template_data: TemplateData;
  created_by?: string | null;
  created_at: string;
  updated_at: string;
}

function base(slug: string, identifier: string): string {
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/templates`;
}

export function templateKeys(slug: string, identifier: string) {
  return {
    templates: ["templates", slug, identifier] as const,
  };
}

export function fetchTemplates(
  slug: string,
  identifier: string,
): Promise<IssueTemplate[]> {
  return api
    .get<{ templates: IssueTemplate[] }>(base(slug, identifier))
    .then((r) => r.templates ?? []);
}

export function createTemplate(
  slug: string,
  identifier: string,
  input: {
    name: string;
    description?: string;
    template_data?: TemplateData;
  },
): Promise<IssueTemplate> {
  return api.post<IssueTemplate>(base(slug, identifier), input);
}

export function updateTemplate(
  slug: string,
  identifier: string,
  id: string,
  input: {
    name?: string;
    description?: string;
    template_data?: TemplateData;
  },
): Promise<IssueTemplate> {
  return api.patch<IssueTemplate>(`${base(slug, identifier)}/${id}`, input);
}

export function deleteTemplate(
  slug: string,
  identifier: string,
  id: string,
): Promise<void> {
  return api.del<void>(`${base(slug, identifier)}/${id}`);
}

// ------------------------------------------------------------------ forms

/** Editable default issue values for a template. Null = no default (the
 *  ordinary create-form default applies). Plain text throughout — tiptap
 *  conversion happens at the template_data boundary. */
export interface TemplateDefaults {
  issueName: string;
  issueDescription: string;
  priority: number | null;
  stateId: string | null;
  estimatePointId: string | null;
  labelIds: string[];
}

/** Current create-issue form values — the prefill target. */
export interface CreateFormValues {
  name: string;
  description: string;
  priority: number;
  stateId: string;
  estimatePointId: string;
  labelIds: string[];
}

/** Blank defaults for a brand-new template. */
export function emptyDefaults(): TemplateDefaults {
  return {
    issueName: "",
    issueDescription: "",
    priority: null,
    stateId: null,
    estimatePointId: null,
    labelIds: [],
  };
}

/** Wraps plain text as a minimal TipTap doc (mirrors the create form's
 *  textToTipTapDoc). Empty text -> undefined (field omitted). */
export function plainTextToTipTapDoc(text: string): unknown | undefined {
  const t = text.trim();
  if (!t) return undefined;
  return {
    type: "doc",
    content: t.split(/\n+/).map((line) => ({
      type: "paragraph",
      content: [{ type: "text", text: line }],
    })),
  };
}

/** Serializes editable defaults to a template_data document, omitting every
 *  field that has no default — absent fields fall back to ordinary issue
 *  defaults at apply time. */
export function defaultsToTemplateData(d: TemplateDefaults): TemplateData {
  const data: TemplateData = {};
  const name = d.issueName.trim();
  if (name) data.name = name;
  const desc = plainTextToTipTapDoc(d.issueDescription);
  if (desc !== undefined) data.description = desc;
  if (d.priority !== null) data.priority = d.priority;
  if (d.stateId) data.state_id = d.stateId;
  if (d.estimatePointId) data.estimate_point_id = d.estimatePointId;
  if (d.labelIds.length > 0) data.label_ids = [...d.labelIds];
  return data;
}

/** Parses a stored template_data document back into editable defaults.
 *  Unknown/malformed fields are dropped, never thrown on. */
export function templateDataToDefaults(data: TemplateData | null | undefined): TemplateDefaults {
  const d = emptyDefaults();
  if (!data || typeof data !== "object") return d;
  if (typeof data.name === "string") d.issueName = data.name;
  if (data.description !== undefined && data.description !== null) {
    d.issueDescription = tiptapText(data.description);
  }
  if (
    typeof data.priority === "number" &&
    Number.isInteger(data.priority) &&
    data.priority >= 0 &&
    data.priority <= 4
  ) {
    d.priority = data.priority;
  }
  if (typeof data.state_id === "string" && data.state_id) d.stateId = data.state_id;
  if (
    typeof data.estimate_point_id === "string" &&
    data.estimate_point_id
  ) {
    d.estimatePointId = data.estimate_point_id;
  }
  if (Array.isArray(data.label_ids)) {
    d.labelIds = data.label_ids.filter(
      (id): id is string => typeof id === "string" && id.length > 0,
    );
  }
  return d;
}

/**
 * Applies a template to the create-issue form: every default the template
 * defines overwrites the current form value; fields the template doesn't
 * define keep the user's current values. Explicit user action (the picker)
 * triggers this — the caller shows the result for review before saving.
 */
export function applyTemplateToForm(
  template: IssueTemplate,
  form: CreateFormValues,
): CreateFormValues {
  const defaults = templateDataToDefaults(template.template_data);
  const trimmedName = defaults.issueName.trim();
  return {
    name: trimmedName ? defaults.issueName : form.name,
    description: defaults.issueDescription
      ? defaults.issueDescription
      : form.description,
    priority: defaults.priority !== null ? defaults.priority : form.priority,
    stateId: defaults.stateId ?? form.stateId,
    estimatePointId: defaults.estimatePointId ?? form.estimatePointId,
    labelIds: defaults.labelIds.length > 0 ? defaults.labelIds : form.labelIds,
  };
}
