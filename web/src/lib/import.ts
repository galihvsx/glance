// Issue import client (C4T8 CSV backend + C8T2 GitHub importer + C9T1
// Jira Cloud importer).
//
// The CSV endpoints speak multipart/form-data (file + JSON mapping);
// the GitHub and Jira endpoints speak JSON. All share the preview →
// import → result shape, and the ImportSection UI renders them through
// the same preview-table / result-summary components.

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

// ---------- Trello import (C10T0 backend) ----------

export interface TrelloImportInput {
  api_key: string;
  token: string;
  board_id: string;
  max?: number;
}

export interface TrelloImportPreviewRow {
  id: string;
  short_link: string;
  short_url: string;
  name: string;
  list_name: string;
  state: string;
  state_is_new: boolean;
  labels: string[];
  new_labels: string[];
  due_date?: string | null;
  assignees: string[];
  assignee_matched: boolean;
  comments: number;
  checklists: number;
  already_imported: boolean;
}

export interface TrelloImportPreview {
  source: string;
  board_id: string;
  total: number;
  rows: TrelloImportPreviewRow[];
}

export interface TrelloImportResult {
  source: string;
  board_id: string;
  created: number;
  skipped: number;
  archived_skipped: number;
  failed: number;
  labels_created: string[];
  states_created: string[];
  assignee_misses: number;
  checklists_skipped: number;
  errors: ImportRowError[];
}

export function previewTrelloImport(
  slug: string,
  identifier: string,
  input: TrelloImportInput,
): Promise<TrelloImportPreview> {
  return api.post<TrelloImportPreview>(
    `${importBase(slug, identifier)}/trello/preview`,
    input,
  );
}

export function runTrelloImport(
  slug: string,
  identifier: string,
  input: TrelloImportInput,
): Promise<TrelloImportResult> {
  return api.post<TrelloImportResult>(
    `${importBase(slug, identifier)}/trello`,
    input,
  );
}

// validateTrelloForm is the pure client-side gate for the Trello import
// form: the API key is required (Trello has no anonymous API access) and
// the board is the 24-char board id, the 8-char short link, or an https
// trello.com board URL. The server re-validates strictly; this is just
// fast feedback. Returns the error message or null when OK.
export function validateTrelloForm(apiKey: string, boardId: string): string | null {
  if (!apiKey.trim()) return "API key is required.";
  const b = boardId.trim();
  if (!b) return "Board is required.";
  if (/^[0-9a-fA-F]{24}$/.test(b) || /^[A-Za-z0-9]{8}$/.test(b)) return null;
  if (/^https:\/\/(www\.)?trello\.com\/b\/[A-Za-z0-9]{8}(\/|$)/.test(b)) return null;
  return "Board must be the 24-char board id, the 8-char short link, or an https trello.com board URL.";
}

// ---------- Jira import (C9T1 backend) ----------

export interface JiraImportInput {
  site: string;
  email: string;
  api_token: string;
  project_key: string;
  max?: number;
}

export interface JiraImportPreviewRow {
  key: string;
  title: string;
  status: string;
  state: string;
  state_is_new: boolean;
  labels: string[];
  new_labels: string[];
  priority: number;
  assignee?: string | null;
  assignee_matched: boolean;
  comments: number;
  issue_type: string;
  already_imported: boolean;
}

export interface JiraImportPreview {
  source: string;
  total: number;
  rows: JiraImportPreviewRow[];
}

export interface JiraImportResult {
  source: string;
  created: number;
  skipped: number;
  failed: number;
  labels_created: string[];
  states_created: string[];
  assignee_misses: number;
  errors: ImportRowError[];
}

export function previewJiraImport(
  slug: string,
  identifier: string,
  input: JiraImportInput,
): Promise<JiraImportPreview> {
  return api.post<JiraImportPreview>(
    `${importBase(slug, identifier)}/jira/preview`,
    input,
  );
}

export function runJiraImport(
  slug: string,
  identifier: string,
  input: JiraImportInput,
): Promise<JiraImportResult> {
  return api.post<JiraImportResult>(
    `${importBase(slug, identifier)}/jira`,
    input,
  );
}

// validateJiraForm is the pure client-side gate for the Jira import
// form: site is the Atlassian subdomain only (the server pins
// <site>.atlassian.net — Server/DC hosts are rejected), project_key is
// Jira's key charset. The server re-validates strictly; this is just
// fast feedback. Returns the error message or null when OK.
export function validateJiraForm(
  site: string,
  email: string,
  projectKey: string,
): string | null {
  const s = site.trim().toLowerCase();
  if (!s) return "Site is required (your Atlassian subdomain, e.g. “acme”).";
  if (!/^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/.test(s)) {
    return "Site must be the subdomain only — letters, digits and hyphens (e.g. “acme”, not “acme.atlassian.net”).";
  }
  if (!email.trim()) return "Email is required.";
  if (!/^[A-Za-z][A-Za-z0-9]{1,29}$/.test(projectKey.trim())) {
    return "Project key must start with a letter and contain only letters and digits (e.g. “PROJ”).";
  }
  return null;
}

// ---------- Workspace archive import (C11T0 backend) ----------
//
// Restores a glance-export/1 archive (as produced by the workspace Data
// section's export) into a workspace. Single shot: no preview — the
// server streams the upload and returns an honest report. Re-import is
// safe: rows whose UUID already exists are skipped, never duplicated.

export interface ArchiveImportReport {
  format: string;
  source_workspace: string;
  target_workspace: string;
  imported: Record<string, number>;
  skipped: {
    count: number;
    reasons: string[];
  };
}

export function runArchiveImport(
  slug: string,
  file: File,
): Promise<ArchiveImportReport> {
  const form = new FormData();
  form.append("file", file);
  return api.postForm<ArchiveImportReport>(
    `/api/v1/workspaces/${encodeURIComponent(slug)}/import`,
    form,
  );
}
