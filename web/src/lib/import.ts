// Issue import client (C4T8 CSV backend + C8T2 GitHub importer).
//
// The CSV endpoints speak multipart/form-data (file + JSON mapping); the
// GitHub endpoints speak JSON. Both share the preview → import → result
// shape, and the ImportSection UI renders both through the same
// preview-table / result-summary components.

import { api } from "./api";

// ---------- CSV import (C4T8 backend) ----------

export interface ImportMapping {
  title: string;
  description?: string;
  state?: string;
  priority?: string;
  labels?: string;
  assignee_email?: string;
  start_date?: string;
  target_date?: string;
}

export interface ImportPreviewRow {
  row: number;
  values: Record<string, string>;
}

export interface ImportRowError {
  row: number;
  message: string;
}

export interface ImportPreview {
  rows: ImportPreviewRow[];
  errors: ImportRowError[];
}

export interface ImportResult {
  created: number;
  failed: number;
  errors: ImportRowError[];
}

function importBase(slug: string, identifier: string): string {
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/imports`;
}

export function previewCsvImport(
  slug: string,
  identifier: string,
  file: File,
  mapping: ImportMapping,
): Promise<ImportPreview> {
  const form = new FormData();
  form.append("file", file);
  form.append("mapping", JSON.stringify(mapping));
  return api.postForm<ImportPreview>(`${importBase(slug, identifier)}/preview`, form);
}

export function runCsvImport(
  slug: string,
  identifier: string,
  file: File,
  mapping: ImportMapping,
): Promise<ImportResult> {
  const form = new FormData();
  form.append("file", file);
  form.append("mapping", JSON.stringify(mapping));
  return api.postForm<ImportResult>(importBase(slug, identifier), form);
}

export async function fetchImportTemplate(slug: string, identifier: string): Promise<Blob> {
  const res = await fetch(`${importBase(slug, identifier)}/template.csv`, {
    credentials: "include",
  });
  if (!res.ok) throw new Error(`Template download failed: ${res.status}`);
  return res.blob();
}

// ---------- GitHub import (C8T2 backend) ----------

export interface GitHubImportInput {
  owner: string;
  repo: string;
  token: string;
  state_filter?: "open" | "closed" | "all";
  max?: number;
}

export interface GitHubImportPreviewRow {
  number: number;
  title: string;
  state: string;
  labels: string[];
  new_labels: string[];
  assignees: string[];
  assignee_matched: boolean;
  comments: number;
  milestone?: string | null;
  already_imported: boolean;
}

export interface GitHubImportPreview {
  source: string;
  total: number;
  rows: GitHubImportPreviewRow[];
}

export interface GitHubImportResult {
  source: string;
  created: number;
  skipped: number;
  prs_skipped: number;
  failed: number;
  labels_created: string[];
  assignee_misses: number;
  milestones_skipped: number;
  errors: ImportRowError[];
}

export function previewGitHubImport(
  slug: string,
  identifier: string,
  input: GitHubImportInput,
): Promise<GitHubImportPreview> {
  return api.post<GitHubImportPreview>(
    `${importBase(slug, identifier)}/github/preview`,
    input,
  );
}

export function runGitHubImport(
  slug: string,
  identifier: string,
  input: GitHubImportInput,
): Promise<GitHubImportResult> {
  return api.post<GitHubImportResult>(
    `${importBase(slug, identifier)}/github`,
    input,
  );
}

// validateGitHubForm is the pure client-side gate for the GitHub import
// form: owner/repo are required (the server re-validates strictly — this
// is just for fast feedback). Returns the error message or null when OK.
export function validateGitHubForm(owner: string, repo: string): string | null {
  if (!owner.trim() || !repo.trim()) return "Owner and repo are required.";
  if (!/^[A-Za-z0-9_.-]+$/.test(owner.trim()) || !/^[A-Za-z0-9_.-]+$/.test(repo.trim())) {
    return "Owner/repo may only contain letters, digits, dot, dash and underscore.";
  }
  return null;
}

// buildCsvMapping drops empty optional fields so the backend sees a
// minimal mapping (mirrors ImportMapping's omitempty).
export function buildCsvMapping(raw: Record<string, string>): ImportMapping {
  const get = (k: string): string => (raw[k] ?? "").trim();
  const out: ImportMapping = { title: get("title") };
  const optional = [
    "description",
    "state",
    "priority",
    "labels",
    "assignee_email",
    "start_date",
    "target_date",
  ];
  for (const key of optional) {
    const v = get(key);
    if (v) Object.assign(out, { [key]: v });
  }
  return out;
}
