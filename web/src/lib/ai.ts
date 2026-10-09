// AI assist client (C5T3). Backend contract: POST /api/v1/ai/draft and
// POST /api/v1/ai/triage (internal/api/ai_handler.go), plus the cheap
// availability probe GET /api/v1/ai/status → {configured}.
//
// Error taxonomy (from the backend §5 envelope):
// - 503 ai_not_configured → the UI renders AI buttons DISABLED with a
//   tooltip ("AI not configured by administrator"), never a fake error.
// - 502 → provider failure → inline error on the calling widget.
// - 404 → caller is not a member of the project (tenancy boundary).

import { useQuery } from "@tanstack/react-query";
import { api, ApiError } from "./api";

export interface AIDraftResponse {
  description: string;
}

export interface AITriageResult {
  priority: number;
  label_names: string[];
  state_name?: string | null;
}

export interface AIStatusResponse {
  configured: boolean;
}

/** True iff err is the backend's honest "AI not configured" (503). */
export function isAINotConfigured(err: unknown): boolean {
  return err instanceof ApiError && err.status === 503;
}

/** True iff err is an AI provider failure (502) — shown inline. */
export function isAIProviderFailure(err: unknown): boolean {
  return err instanceof ApiError && err.status === 502;
}

/** POST /api/v1/ai/draft → the provider's drafted description. */
export async function draftDescription(
  slug: string,
  identifier: string,
  title: string,
  context?: string,
): Promise<string> {
  const res = await api.post<AIDraftResponse>("/api/v1/ai/draft", {
    workspace_slug: slug,
    project_identifier: identifier,
    title,
    ...(context && context.trim() ? { context } : {}),
  });
  return res.description;
}

/** POST /api/v1/ai/triage → priority/labels/state mapped to real taxonomy. */
export async function triageIssue(
  slug: string,
  identifier: string,
  title: string,
  description?: string,
): Promise<AITriageResult> {
  return api.post<AITriageResult>("/api/v1/ai/triage", {
    workspace_slug: slug,
    project_identifier: identifier,
    title,
    ...(description && description.trim() ? { description } : {}),
  });
}

/**
 * Cheap, side-effect-free availability probe: GET /api/v1/ai/status
 * never touches the provider (it only reports whether an API key is
 * configured). A missing route (older server) or any other ambiguous
 * failure resolves to "unknown → enabled", so the real call can surface
 * the real error instead of the UI guessing wrong.
 */
export async function probeAIAvailable(): Promise<boolean> {
  try {
    const res = await api.get<AIStatusResponse>("/api/v1/ai/status");
    return res.configured;
  } catch {
    return true;
  }
}

/**
 * Shared availability state (one probe per session, 5-minute cache).
 * `data === false` means the server answered "not configured": callers
 * render AI controls disabled with the "AI not configured by
 * administrator" tooltip. `undefined` (loading/error) means unknown —
 * controls stay enabled and the real call surfaces the real error.
 */
export function useAIAvailable() {
  return useQuery({
    queryKey: ["ai-available"],
    queryFn: probeAIAvailable,
    staleTime: 5 * 60 * 1000,
    retry: false,
    refetchOnWindowFocus: false,
  });
}
