// profile: user settings client (C6T7).
//
// Contract (verified 2026-10-09):
//   PATCH /api/v1/auth/me {name} -> 200 User (name trimmed, 1-100 chars)
//   GET  /api/v1/auth/sessions -> {sessions: [{id, created_at, last_seen_at,
//        ip, user_agent, current}]} — current marks the caller's own session
//   DELETE /api/v1/auth/sessions/:id -> 200 {ok:true} (revoking the current
//        session also clears its cookie — a logout by another name)

import { api } from "./api";
import type { User } from "./auth";

/** One active session (backend SessionInfo + current flag). */
export interface SessionInfo {
  id: string;
  created_at: string;
  last_seen_at: string;
  ip: string | null;
  user_agent: string | null;
  current: boolean;
}

export function updateMyName(name: string): Promise<User> {
  return api.patch<User>("/api/v1/auth/me", { name });
}

export function fetchSessions(): Promise<SessionInfo[]> {
  return api
    .get<{ sessions: SessionInfo[] }>("/api/v1/auth/sessions")
    .then((r) => r.sessions ?? []);
}

export function revokeSession(id: string): Promise<void> {
  return api.del<void>(`/api/v1/auth/sessions/${id}`);
}

/**
 * Initials for the avatar fallback: first letters of the first two name
 * words, else the first two letters of the email local part.
 */
export function initials(name: string | null, email: string): string {
  const fromName = (name ?? "")
    .trim()
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((w) => w[0])
    .join("");
  if (fromName) return fromName.toUpperCase();
  const local = email.split("@")[0] ?? "";
  return local.slice(0, 2).toUpperCase() || "?";
}

/** Short human label for a user agent string (browser/OS guess). */
export function deviceLabel(userAgent: string | null): string {
  if (!userAgent) return "Unknown device";
  const ua = userAgent.toLowerCase();
  let os = "";
  if (ua.includes("android")) os = "Android";
  else if (ua.includes("iphone") || ua.includes("ipad")) os = "iOS";
  else if (ua.includes("mac os")) os = "macOS";
  else if (ua.includes("windows")) os = "Windows";
  else if (ua.includes("linux")) os = "Linux";
  let browser = "";
  if (ua.includes("edg/")) browser = "Edge";
  else if (ua.includes("chrome/")) browser = "Chrome";
  else if (ua.includes("firefox/")) browser = "Firefox";
  else if (ua.includes("safari/") && ua.includes("version/")) browser = "Safari";
  const parts = [browser, os].filter(Boolean);
  return parts.length > 0 ? parts.join(" · ") : "Unknown device";
}
