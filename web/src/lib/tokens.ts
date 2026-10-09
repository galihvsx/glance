// tokens: API-token (personal access token) management client.
// Mirrors the backend contract in internal/auth/token.go + internal/api/token_handler.go.
//
// Contract (verified 2026-10-09):
//   POST /api/v1/tokens  {name, scopes, expires_at?} -> 201 {id,name,scopes,
//                          expires_at,last_used_at,created_at,token}
//                          `token` is the plaintext, returned exactly once.
//   GET  /api/v1/tokens  -> {tokens: [{id,name,scopes,expires_at,
//                          last_used_at,created_at}]} — no hash, no plaintext.
//   DELETE /api/v1/tokens/:id -> 204; 404 for unknown/already-revoked/foreign.
// Scopes are a closed vocabulary: "read", "write". At least one scope is
// required. expires_at is optional (RFC 3339, must be in the future); absent
// means the token never expires.

import { api } from "./api";

/** Safe-to-serialize token view: no hash, no plaintext. */
export interface ApiToken {
  id: string;
  name: string;
  scopes: string[];
  expires_at: string | null;
  last_used_at: string | null;
  created_at: string;
}

/** 201 response of POST /api/v1/tokens: the Token view plus the plaintext,
 *  which the server shows exactly once and cannot be re-fetched. */
export interface CreatedToken extends ApiToken {
  token: string;
}

/** Closed scope vocabulary from internal/auth (ScopeRead, ScopeWrite). */
export const TOKEN_SCOPES = ["read", "write"] as const;
export type TokenScope = (typeof TOKEN_SCOPES)[number];

export function scopeLabel(scope: string): string {
  switch (scope) {
    case "read":
      return "Read — view issues, projects and data";
    case "write":
      return "Write — read plus create, update and delete";
    default:
      return scope;
  }
}

export interface ExpiryChoice {
  id: string;
  label: string;
  /** Duration in days, or null for "never expires". */
  days: number | null;
}

export const EXPIRY_CHOICES: ExpiryChoice[] = [
  { id: "never", label: "Never", days: null },
  { id: "7d", label: "7 days", days: 7 },
  { id: "30d", label: "30 days", days: 30 },
  { id: "90d", label: "90 days", days: 90 },
  { id: "1y", label: "1 year", days: 365 },
];

/** RFC 3339 expires_at for an expiry choice; undefined means "no expiry". */
export function expiresAtForChoice(
  choice: ExpiryChoice,
  now: Date = new Date(),
): string | undefined {
  if (choice.days === null) return undefined;
  return new Date(now.getTime() + choice.days * 86_400_000).toISOString();
}

export interface CreateTokenInput {
  name: string;
  scopes: string[];
  expires_at?: string;
}

/** Client-side validation mirroring the backend rules (name required,
 *  closed scope vocabulary, at least one scope). Returns an error message
 *  or null when the form is valid. */
export function validateCreateTokenForm(
  name: string,
  scopes: string[],
): string | null {
  if (!name.trim()) return "Name is required.";
  if (scopes.length === 0) return "Select at least one scope.";
  for (const s of scopes) {
    if (!TOKEN_SCOPES.includes(s as TokenScope)) {
      return `Unknown scope: ${s}.`;
    }
  }
  return null;
}

// ---------------------------------------------------------------------------
// Secret reveal state machine.
//
// The plaintext secret is handed to the client once in the create response
// and lives only in this state. The list endpoint cannot resurrect it:
// the ApiToken type carries no secret field, so refetching can never
// re-render it. DISMISS clears it for good.
export type SecretRevealState = {
  /** A token has been created and its secret is being shown once. */
  showing: boolean;
  /** The plaintext secret. Null after dismissal. */
  secret: string | null;
};

export type SecretRevealAction =
  | { type: "created"; token: string }
  | { type: "dismissed" };

export function secretRevealReducer(
  _state: SecretRevealState,
  action: SecretRevealAction,
): SecretRevealState {
  switch (action.type) {
    case "created":
      return { showing: true, secret: action.token };
    case "dismissed":
      // The secret is gone from state; nothing on the server can bring it
      // back — only a new token mint produces a fresh secret.
      return { showing: false, secret: null };
  }
}

export const initialSecretRevealState: SecretRevealState = {
  showing: false,
  secret: null,
};

// ---------------------------------------------------------------------------
// Revoke flow (house pattern: window.confirm before the destructive call).

/** Asks for confirmation; returns true only when the user confirmed. */
export function confirmRevoke(
  tokenName: string,
  confirm: (message: string) => boolean,
): boolean {
  return confirm(
    `Revoke token "${tokenName}"? It will stop working immediately and this cannot be undone.`,
  );
}

// ---------------------------------------------------------------------------
// API calls (TanStack Query is used in the page).

export const TOKEN_QUERY_KEY = ["api-tokens"] as const;

export async function fetchTokens(): Promise<ApiToken[]> {
  const res = await api.get<{ tokens: ApiToken[] }>("/api/v1/tokens");
  return res.tokens;
}

export async function createToken(
  input: CreateTokenInput,
): Promise<CreatedToken> {
  return api.post<CreatedToken>("/api/v1/tokens", input);
}

export async function revokeToken(id: string): Promise<void> {
  await api.del<void>(`/api/v1/tokens/${encodeURIComponent(id)}`);
}
