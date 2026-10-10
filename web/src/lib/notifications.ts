// Notification-center API client (C5T6). Contract mirrors
// internal/api/notify_handler.go — verified 2026-10-09 against the backend:
//
//   GET  /api/v1/notifications?unread_only=&limit=
//        → {notifications: NotificationItem[], unread_count: number}
//   POST /api/v1/notifications/read          → {ok: true}   (mark all)
//   POST /api/v1/notifications/:id/read      → {ok: true}   (mark one)
//   GET  /api/v1/notification-prefs          → {prefs: NotificationPrefItem[]}
//   PUT  /api/v1/notification-prefs/:event   → NotificationPrefItem
//
// Do NOT extend this module with backend-shape guesses: the handler is the
// source of truth (frontend-only task).

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";

/** One row from GET /api/v1/notifications. */
export interface NotificationItem {
  id: string;
  user_id: string;
  type: string;
  title: string;
  payload: NotificationPayload;
  read_at: string | null;
  created_at: string;
}

/**
 * Payload keys written by the backend call sites (notifyTx): every event
 * carries issue_id / display_id / issue_name / actor_id / project_id; some
 * carry workspace_id, comment_id, state_name or status_name. None are
 * guaranteed, so everything here is optional.
 */
export interface NotificationPayload {
  issue_id?: string;
  display_id?: string;
  issue_name?: string;
  actor_id?: string;
  project_id?: string;
  workspace_id?: string;
  comment_id?: string;
  state_name?: string;
  status_name?: string;
  [key: string]: unknown;
}

/** One row from GET /api/v1/notification-prefs. */
export interface NotificationPrefItem {
  event: string;
  in_app: boolean;
  email: boolean;
}

/**
 * Digest schedule (C11T2) from GET /api/v1/digest-schedule.
 * frequency: "daily" | "weekly"; hour: 0-23 in the glance server's local
 * time zone (server_tz names it — per-user time zones are future work).
 */
export interface DigestSchedule {
  frequency: "daily" | "weekly";
  hour: number;
  server_tz: string;
}

/** Known event types (service.AllNotifyEvents). Unknown types render raw. */
export const NOTIFY_EVENT_LABELS: Record<string, string> = {
  "issue.assigned": "Issue assigned",
  "issue.unassigned": "Issue unassigned",
  "comment.created": "Comment created",
  "issue.state_changed": "Issue state changed",
  "intake.triaged": "Intake triaged",
  "mention": "Mentioned in comment",
  // C10T2: the digest email (email-only; no in-app toggle). C11T2 added
  // per-user scheduling (frequency + send-after hour).
  "digest.daily": "Email digest",
};

export function eventLabel(event: string): string {
  return NOTIFY_EVENT_LABELS[event] ?? event;
}

/** Poll cadence for the unread badge: 30s, paused while the tab is hidden. */
export const UNREAD_POLL_MS = 30_000;

export const NOTIFICATION_KEYS = {
  list: ["notifications", "list"] as const,
  unread: ["notifications", "unread"] as const,
  prefs: ["notifications", "prefs"] as const,
  schedule: ["notifications", "digest-schedule"] as const,
};

export async function fetchNotifications(
  limit = 50,
): Promise<{ notifications: NotificationItem[]; unread_count: number }> {
  return api.get(
    `/api/v1/notifications?limit=${encodeURIComponent(String(limit))}`,
  );
}

export async function fetchUnreadCount(): Promise<number> {
  const { unread_count } = await api.get<{ unread_count: number }>(
    "/api/v1/notifications?limit=1",
  );
  return unread_count;
}

export async function markNotificationRead(id: string): Promise<void> {
  await api.post<{ ok: boolean }>(
    `/api/v1/notifications/${encodeURIComponent(id)}/read`,
  );
}

export async function markAllNotificationsRead(): Promise<void> {
  await api.post<{ ok: boolean }>("/api/v1/notifications/read");
}

export async function fetchNotificationPrefs(): Promise<NotificationPrefItem[]> {
  const { prefs } = await api.get<{ prefs: NotificationPrefItem[] }>(
    "/api/v1/notification-prefs",
  );
  return prefs;
}

export async function setNotificationPref(
  event: string,
  inApp: boolean,
  email: boolean,
): Promise<NotificationPrefItem> {
  return api.put<NotificationPrefItem>(
    `/api/v1/notification-prefs/${encodeURIComponent(event)}`,
    { in_app: inApp, email },
  );
}

// C11T2: digest schedule endpoints. Contract mirrors
// internal/api/notify_handler.go:
//
//   GET  /api/v1/digest-schedule            → DigestSchedule
//   PUT  /api/v1/digest-schedule            → DigestSchedule
//        body {frequency: "daily"|"weekly", hour: 0-23}
//
export async function fetchDigestSchedule(): Promise<DigestSchedule> {
  return api.get<DigestSchedule>("/api/v1/digest-schedule");
}

export async function setDigestSchedule(
  frequency: string,
  hour: number,
): Promise<DigestSchedule> {
  return api.put<DigestSchedule>("/api/v1/digest-schedule", {
    frequency,
    hour,
  });
}

/**
 * Shared unread-count query for the header bell. refetchIntervalInBackground
 * defaults to false in TanStack Query, so the 30s poll pauses while the tab
 * is hidden and resumes on visibility.
 */
export function useUnreadCount() {
  return useQuery({
    queryKey: NOTIFICATION_KEYS.unread,
    queryFn: fetchUnreadCount,
    refetchInterval: UNREAD_POLL_MS,
    // The badge is cosmetic: a failed poll must not error the whole page.
    retry: 1,
  });
}

/** Invalidate every notification query after a read mutation. */
export function useInvalidateNotifications() {
  const queryClient = useQueryClient();
  return () =>
    queryClient.invalidateQueries({ queryKey: ["notifications"] });
}
