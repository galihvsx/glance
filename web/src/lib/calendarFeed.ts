// calendarFeed: iCal subscription feed token client (C15T3).
// Mirrors the backend contract in internal/auth/feedtoken.go +
// internal/api/calendar_feed_handler.go.
//
// Contract:
//   GET    /api/v1/me/calendar-token -> {active, created_at?, last_used_at?}
//   POST   /api/v1/me/calendar-token -> 201 {token, created_at}
//            `token` is the plaintext (glcal_…), returned exactly once.
//            Regenerate semantics: any previous live token is revoked.
//   DELETE /api/v1/me/calendar-token -> 204; 404 when no live token.
//   GET /api/v1/workspaces/{slug}/projects/{identifier}/calendar.ics?token=…
//   GET /api/v1/workspaces/{slug}/projects/{identifier}/cycles/{cycleID}/calendar.ics?token=…
// All management endpoints are session-cookie only. The feed endpoints
// take ?token= (Authorization: Bearer <feed-token> is a fallback).

import { api } from "./api";

/** Safe-to-serialize feed-token view: timestamps only, never the secret. */
export interface FeedTokenStatus {
  active: boolean;
  created_at: string | null;
  last_used_at: string | null;
}

/** 201 response of POST /api/v1/me/calendar-token: the plaintext feed
 *  token, shown exactly once — there is no endpoint that reveals it again. */
export interface CreatedFeedToken {
  token: string;
  created_at: string;
}

export function fetchFeedTokenStatus(): Promise<FeedTokenStatus> {
  return api.get<FeedTokenStatus>("/api/v1/me/calendar-token");
}

export function createFeedToken(): Promise<CreatedFeedToken> {
  return api.post<CreatedFeedToken>("/api/v1/me/calendar-token");
}

export function revokeFeedToken(): Promise<void> {
  return api.del<void>("/api/v1/me/calendar-token");
}

/** Absolute project feed subscription URL for an external calendar app. */
export function projectFeedURL(
  origin: string,
  slug: string,
  identifier: string,
  token: string,
): string {
  return (
    `${origin}/api/v1/workspaces/${encodeURIComponent(slug)}` +
    `/projects/${encodeURIComponent(identifier)}/calendar.ics` +
    `?token=${encodeURIComponent(token)}`
  );
}

/** Absolute cycle feed subscription URL for an external calendar app. */
export function cycleFeedURL(
  origin: string,
  slug: string,
  identifier: string,
  cycleId: string,
  token: string,
): string {
  return (
    `${origin}/api/v1/workspaces/${encodeURIComponent(slug)}` +
    `/projects/${encodeURIComponent(identifier)}/cycles/${encodeURIComponent(cycleId)}` +
    `/calendar.ics?token=${encodeURIComponent(token)}`
  );
}

/** URL pattern hint shown next to a freshly minted token (no secret in it). */
export function feedURLPattern(origin: string): string {
  return `${origin}/api/v1/workspaces/{slug}/projects/{identifier}/calendar.ics?token=<token>`;
}
