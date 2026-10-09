// Webhooks management (C6T1).
//
// Backend contract — verified against internal/api/notify_handler.go and
// internal/service/webhook.go (main @ 757f76c):
//   POST /api/v1/workspaces/:slug/webhooks {url, events?, secret?, active?}
//     → 201 {id, workspace_id, url, secret, events, active, created_at,
//       updated_at} — the ONLY response that carries the secret
//       (show-on-create). Omitting secret (or null) → the server generates a
//       random 32-byte hex one.
//   GET  /api/v1/workspaces/:slug/webhooks
//     → {webhooks: Webhook[]} — secret omitted (json:"-" on the Go struct).
//   GET  /api/v1/workspaces/:slug/webhooks/:id → Webhook — secret omitted.
//   PATCH .../webhooks/:id {url?, secret?, events?, active?} → Webhook —
//     secret omitted. Only non-empty url is applied; nil fields untouched.
//   DELETE .../webhooks/:id → 204.
//   Admin-only (role 20): non-members → 404, members/guests → 403
//     (service.ErrForbidden → "forbidden").
//   URL must be http(s) → 400 otherwise (ErrBadWebhookURL).
//   Unknown event → 400 (ErrUnknownWebhookEvent).
//   Event vocabulary (service.WebhookDomainEvents):
//     "issue.created", "issue.updated", "issue.deleted", "comment.created".
//   An EMPTY events list means "every domain event" — the dispatcher matches
//   `events = '{}' OR $event = ANY(events)`, so subscribe-all is the zero
//   selection, not a special value.
//
// UI lives in components/settings/WebhooksSection.tsx.

import { api } from "./api";

export interface Webhook {
  id: string;
  workspace_id: string;
  url: string;
  // Empty = subscribed to every domain event (see module comment).
  events: string[];
  active: boolean;
  created_at: string;
  updated_at: string;
}

// POST 201 response shape — the only response that reveals the signing
// secret. Never stored, never refetched: shown once, then discarded.
export interface WebhookCreateResponse extends Webhook {
  secret: string;
}

export interface WebhookEventOption {
  value: string;
  label: string;
  hint: string;
}

// Closed vocabulary, pinned to service.WebhookDomainEvents. webhooks.test.ts
// asserts the exact values so backend drift fails loudly.
export const WEBHOOK_EVENTS: WebhookEventOption[] = [
  { value: "issue.created", label: "Issue created", hint: "Issues opened in any project" },
  { value: "issue.updated", label: "Issue updated", hint: "Title, state, assignee, priority…" },
  { value: "issue.deleted", label: "Issue deleted", hint: "Issues permanently removed" },
  { value: "comment.created", label: "Comment created", hint: "New comments on issues" },
];

export function eventLabel(value: string): string {
  return WEBHOOK_EVENTS.find((e) => e.value === value)?.label ?? value;
}

// toggleEvent adds or removes one event from a selection, keeping the
// canonical WEBHOOK_EVENTS order (stable chips in the list regardless of
// click order). Unknown values are dropped.
export function toggleEvent(selected: string[], event: string): string[] {
  const set = new Set(selected);
  if (set.has(event)) set.delete(event);
  else set.add(event);
  return WEBHOOK_EVENTS.map((e) => e.value).filter((v) => set.has(v));
}

// validateWebhookUrl mirrors the backend's validateWebhookURL
// (internal/service/webhook.go): scheme must be http or https and a host
// must be present. Returns an error message or null when valid.
export function validateWebhookUrl(raw: string): string | null {
  const trimmed = raw.trim();
  if (!trimmed) return "URL is required.";
  let u: URL;
  try {
    u = new URL(trimmed);
  } catch {
    return "Enter a valid URL (e.g. https://example.com/hook).";
  }
  if (u.protocol !== "http:" && u.protocol !== "https:") {
    return "The URL must use http or https.";
  }
  if (!u.hostname) return "The URL must include a host.";
  return null;
}

export interface WebhookCreateBody {
  url: string;
  events: string[];
  active: boolean;
  // Omitted when empty so the server auto-generates a secret.
  secret?: string;
}

// buildCreateBody shapes the POST payload: an empty secret field is
// dropped entirely so the backend generates one (explicit "" would mean
// "store empty" on the current backend).
export function buildCreateBody(
  url: string,
  events: string[],
  secret: string,
  active: boolean,
): WebhookCreateBody {
  const s = secret.trim();
  return {
    url: url.trim(),
    events: [...events],
    active,
    ...(s ? { secret: s } : {}),
  };
}

// generateWebhookSecret creates a client-side signing secret for the
// "Regenerate secret" flow: 32 random bytes, hex-encoded — the same shape
// the server generates on create (service.randomWebhookSecret). Generated
// here so regenerate has deterministic behavior regardless of whether the
// deferred-minors item (empty-secret PATCH) has landed yet.
export function generateWebhookSecret(): string {
  const bytes = new Uint8Array(32);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
}

// --- Show-once secret state -----------------------------------------------
// The secret from a create/regenerate is sensitive and never refetched:
// the reveal state holds it only until the dialog closes. A tiny reducer
// pins the "exactly once" contract for tests.

export interface SecretReveal {
  // "create" | "regenerate" — copy shown to the user.
  kind: "create" | "regenerate";
  secret: string;
}

export type RevealAction =
  | { type: "revealed"; kind: "create" | "regenerate"; secret: string }
  | { type: "dismissed" };

export function revealReducer(
  _state: SecretReveal | null,
  action: RevealAction,
): SecretReveal | null {
  switch (action.type) {
    case "revealed":
      return { kind: action.kind, secret: action.secret };
    case "dismissed":
      return null;
  }
}

// --- API -------------------------------------------------------------------

const webhooksPath = (slug: string, id?: string) =>
  `/api/v1/workspaces/${encodeURIComponent(slug)}/webhooks${
    id ? `/${encodeURIComponent(id)}` : ""
  }`;

export function listWebhooks(slug: string): Promise<Webhook[]> {
  return api
    .get<{ webhooks: Webhook[] }>(webhooksPath(slug))
    .then((r) => r.webhooks ?? []);
}

export function createWebhook(
  slug: string,
  body: WebhookCreateBody,
): Promise<WebhookCreateResponse> {
  return api.post<WebhookCreateResponse>(webhooksPath(slug), body);
}

export interface WebhookUpdateBody {
  url?: string;
  secret?: string;
  events?: string[];
  active?: boolean;
}

export function updateWebhook(
  slug: string,
  id: string,
  body: WebhookUpdateBody,
): Promise<Webhook> {
  return api.patch<Webhook>(webhooksPath(slug, id), body);
}

export function deleteWebhook(slug: string, id: string): Promise<void> {
  return api.del<void>(webhooksPath(slug, id));
}
