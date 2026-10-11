// Notification center (C5T6, C19T9 polish): /notifications page.
//
// Dense 47px notification rows (C19T5 row language): unread dot in the
// blue accent, 24px actor avatar, title 12px (medium when unread), meta
// 10px muted (display-id, time, event). Rows grouped under day headers
// in the prototype "Recently updated" bar language (35px, hairline,
// 12px/600). Header is the shell 49px bar — no oversized h1.
//
// List: actor avatar initial, title text, relative time, issue link.
// Click a row → mark read, then navigate to the issue. "Mark all read"
// marks everything read. Prefs section toggles delivery per event type
// against the real GET/PUT /notification-prefs endpoints.

import { Link, useNavigate } from "react-router-dom";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Bell, CheckCheck } from "lucide-react";
import { ApiError } from "../lib/api";
import {
  NOTIFICATION_KEYS,
  DIGEST_TZ_SHORTLIST,
  eventLabel,
  fetchDigestSchedule,
  fetchNotificationPrefs,
  fetchNotifications,
  markAllNotificationsRead,
  markNotificationRead,
  setDigestSchedule,
  setNotificationPref,
  useInvalidateNotifications,
  type DigestSchedule,
  type NotificationItem,
  type NotificationPrefItem,
} from "../lib/notifications";
import {
  actorInitial,
  fetchNotificationContext,
  issueLink,
  type ActorInfo,
  type NotificationContext,
} from "../lib/notificationContext";
import { relativeTime } from "../lib/relativeTime";
import { Avatar, AvatarFallback } from "../components/ui/avatar";
import { Button } from "../components/ui/button";
import {
  NativeSelect,
  NativeSelectOption,
} from "../components/ui/native-select";
import { Input } from "../components/ui/input";
import { Skeleton } from "../components/ui/skeleton";
import { Switch } from "../components/ui/switch";
import { Alert, AlertDescription } from "../components/ui/alert";
import EmptyState from "../components/personal/EmptyState";
import { SectionBar } from "../components/personal/SectionBar";

const EMPTY_CONTEXT: NotificationContext = {
  projects: new Map(),
  actors: new Map(),
};

function actorFor(
  ctx: NotificationContext | undefined,
  n: NotificationItem,
): ActorInfo | null {
  const actorId = n.payload.actor_id;
  if (typeof actorId !== "string" || !ctx) return null;
  return ctx.actors.get(actorId) ?? null;
}

function NotificationRow({
  n,
  ctx,
  onOpen,
}: {
  n: NotificationItem;
  ctx: NotificationContext | undefined;
  onOpen: (n: NotificationItem, link: string | null) => void;
}) {
  const actor = actorFor(ctx, n);
  const link = issueLink(ctx ?? EMPTY_CONTEXT, n.payload);
  const unread = n.read_at === null;

  return (
    <li>
      <button
        type="button"
        onClick={() => onOpen(n, link)}
        className="grid min-h-[47px] w-full grid-cols-[16px_24px_minmax(0,1fr)] items-center gap-[10px] px-3 py-2 text-left transition-colors hover:bg-wash-hover"
      >
        <span className="flex justify-center">
          {unread && (
            <span
              aria-label="unread"
              className="h-2 w-2 rounded-full bg-accent-blue"
            />
          )}
        </span>
        <Avatar className="h-6 w-6 shrink-0">
          <AvatarFallback className="text-[10px] font-semibold">
            {actor ? actorInitial(actor) : "?"}
          </AvatarFallback>
        </Avatar>
        <span className="min-w-0">
          <span
            className={`block truncate text-[12px] ${
              unread ? "font-medium text-ink" : "text-ink-secondary"
            }`}
          >
            {n.title}
          </span>
          <span className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[10px] text-ink-muted">
            {n.payload.display_id &&
              (link ? (
                <Link
                  to={link}
                  onClick={(e) => e.stopPropagation()}
                  className="font-medium text-accent-blue hover:underline"
                >
                  {n.payload.display_id}
                </Link>
              ) : (
                <span className="font-mono">{n.payload.display_id}</span>
              ))}
            <span title={n.created_at}>{relativeTime(n.created_at)}</span>
            <span>{eventLabel(n.type)}</span>
          </span>
        </span>
      </button>
    </li>
  );
}

/** Day bucket label for grouping headers: Today / Yesterday / date. */
function dayLabel(iso: string): string {
  const d = new Date(iso);
  const now = new Date();
  const startOf = (x: Date) =>
    new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const diffDays = Math.round((startOf(now) - startOf(d)) / 86_400_000);
  if (diffDays <= 0) return "Today";
  if (diffDays === 1) return "Yesterday";
  return d.toLocaleDateString("en-US", {
    month: "short",
    day: "numeric",
    year: "numeric",
  });
}

function groupByDay(
  notifications: NotificationItem[],
): { label: string; items: NotificationItem[] }[] {
  const groups = new Map<string, NotificationItem[]>();
  for (const n of notifications) {
    const label = dayLabel(n.created_at);
    const list = groups.get(label);
    if (list) list.push(n);
    else groups.set(label, [n]);
  }
  return [...groups.entries()].map(([label, items]) => ({ label, items }));
}

// C11T2/C13T1: digest schedule controls (frequency + send-after hour +
// timezone), rendered inside the digest pref row next to the email
// toggle. Disabled while the digest email itself is off. The hour is
// evaluated in the selected timezone (C13T1) and the copy states that.
// The timezone field is a shortlist datalist plus free-text IANA entry —
// the server validates the name and rejects unknowns (400), which rolls
// back with the server's message. Exported for the vitest below.
export function DigestScheduleControls({ enabled }: { enabled: boolean }) {
  const queryClient = useQueryClient();
  const scheduleQuery = useQuery({
    queryKey: NOTIFICATION_KEYS.schedule,
    queryFn: fetchDigestSchedule,
  });

  // Local draft of the tz field while the user is editing; null means
  // "show the server value". Reset on rollback and on successful save.
  const [tzDraft, setTzDraft] = useState<string | null>(null);

  const setSchedule = useMutation({
    mutationFn: ({
      frequency,
      hour,
      tz,
    }: {
      frequency: string;
      hour: number;
      tz: string;
    }) => setDigestSchedule(frequency, hour, tz),
    // Optimistic update: apply locally, roll back on failure.
    onMutate: async (vars) => {
      await queryClient.cancelQueries({
        queryKey: NOTIFICATION_KEYS.schedule,
      });
      const previous = queryClient.getQueryData<DigestSchedule>(
        NOTIFICATION_KEYS.schedule,
      );
      queryClient.setQueryData<DigestSchedule>(
        NOTIFICATION_KEYS.schedule,
        (old) =>
          old
            ? {
                ...old,
                frequency: vars.frequency as DigestSchedule["frequency"],
                hour: vars.hour,
                tz: vars.tz,
              }
            : old,
      );
      return { previous };
    },
    onError: (_err, _vars, context) => {
      if (context?.previous) {
        queryClient.setQueryData(
          NOTIFICATION_KEYS.schedule,
          context.previous,
        );
      }
      // Drop the rejected draft so the input shows the rolled-back value.
      setTzDraft(null);
    },
    onSuccess: () => {
      setTzDraft(null);
    },
    onSettled: () => {
      void queryClient.invalidateQueries({
        queryKey: NOTIFICATION_KEYS.schedule,
      });
    },
  });

  const sched = scheduleQuery.data;
  const frequency = sched?.frequency ?? "daily";
  const hour = sched?.hour ?? 8;
  // GET always reports the effective zone, so tz is never blank.
  const tz = sched?.tz ?? "";
  const shownTz = tzDraft ?? tz;
  const disabled =
    !enabled || setSchedule.isPending || scheduleQuery.isLoading;

  // Commit the draft on blur/Enter. An emptied field resets to the
  // server-local default (the API treats "" as the default).
  const commitTz = () => {
    const next = (tzDraft ?? "").trim();
    if (tzDraft === null || next === tz || setSchedule.isPending) return;
    setSchedule.mutate({ frequency, hour, tz: next });
  };

  return (
    <div className="flex flex-wrap items-center gap-x-5 gap-y-2">
      <label className="flex items-center gap-2 text-xs text-ink-muted">
        Frequency
        <NativeSelect
          size="sm"
          value={frequency}
          disabled={disabled}
          onChange={(e) =>
            setSchedule.mutate({ frequency: e.target.value, hour, tz })
          }
          aria-label="Digest frequency"
        >
          <NativeSelectOption value="daily">Daily</NativeSelectOption>
          <NativeSelectOption value="weekly">Weekly</NativeSelectOption>
        </NativeSelect>
      </label>
      <label className="flex items-center gap-2 text-xs text-ink-muted">
        Send after
        <NativeSelect
          size="sm"
          value={String(hour)}
          disabled={disabled}
          onChange={(e) =>
            setSchedule.mutate({ frequency, hour: Number(e.target.value), tz })
          }
          aria-label="Digest send-after hour"
        >
          {Array.from({ length: 24 }, (_, h) => (
            <NativeSelectOption key={h} value={String(h)}>
              {String(h).padStart(2, "0")}:00
            </NativeSelectOption>
          ))}
        </NativeSelect>
      </label>
      <label className="flex items-center gap-2 text-xs text-ink-muted">
        Time zone
        <Input
          list="digest-tz-list"
          value={shownTz}
          disabled={disabled}
          onChange={(e) => setTzDraft(e.target.value)}
          onBlur={commitTz}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              commitTz();
              e.currentTarget.blur();
            }
          }}
          aria-label="Digest timezone"
          placeholder="Asia/Jakarta"
          className="h-7 w-36 rounded-[min(var(--radius-md),10px)] text-xs"
        />
        <datalist id="digest-tz-list">
          {DIGEST_TZ_SHORTLIST.map((z) => (
            <option key={z} value={z} />
          ))}
        </datalist>
      </label>
      <span className="text-xs text-ink-muted">
        Digest sends after {String(hour).padStart(2, "0")}:00 in {shownTz}.
      </span>
      {setSchedule.isError && (
        <span className="text-xs text-destructive">
          {setSchedule.error instanceof Error
            ? setSchedule.error.message
            : "Failed to save schedule"}
        </span>
      )}
    </div>
  );
}

// One preference row. The digest (C10T2/C11T2) is email-only, so its row
// renders a single Email toggle — no inert In-app switch — plus the C11T2
// schedule controls beneath it.
function PrefRow({
  p,
  setPref,
}: {
  p: NotificationPrefItem;
  setPref: {
    mutate: (vars: { event: string; inApp: boolean; email: boolean }) => void;
    isPending: boolean;
  };
}) {
  const isDigest = p.event === "digest.daily";
  const emailToggle = (
    <label className="flex items-center gap-2 text-xs text-ink-muted">
      Email
      <Switch
        size="sm"
        checked={p.email}
        disabled={setPref.isPending}
        onCheckedChange={(email) =>
          setPref.mutate({
            event: p.event,
            inApp: p.in_app,
            email,
          })
        }
        aria-label={`Email notifications for ${eventLabel(p.event)}`}
      />
    </label>
  );
  if (isDigest) {
    return (
      <li className="flex flex-col gap-3 py-3">
        <div className="flex items-center justify-between gap-4">
          <span className="text-[12px] font-medium text-ink">
            {eventLabel(p.event)}
            <span className="ml-2 text-[11px] font-normal text-ink-muted">
              One email summarizing assignments, mentions, and state changes
            </span>
          </span>
          {emailToggle}
        </div>
        <DigestScheduleControls enabled={p.email} />
      </li>
    );
  }
  return (
    <li className="flex min-h-[47px] items-center justify-between gap-4 py-2">
      <span className="text-[12px] font-medium text-ink">
        {eventLabel(p.event)}
      </span>
      <div className="flex items-center gap-4">
        <label className="flex items-center gap-2 text-xs text-ink-muted">
          In-app
          <Switch
            size="sm"
            checked={p.in_app}
            disabled={setPref.isPending}
            onCheckedChange={(inApp) =>
              setPref.mutate({
                event: p.event,
                inApp,
                email: p.email,
              })
            }
            aria-label={`In-app notifications for ${eventLabel(p.event)}`}
          />
        </label>
        {emailToggle}
      </div>
    </li>
  );
}

function PrefsSection() {
  const queryClient = useQueryClient();
  const prefsQuery = useQuery({
    queryKey: NOTIFICATION_KEYS.prefs,
    queryFn: fetchNotificationPrefs,
  });

  const setPref = useMutation({
    mutationFn: ({
      event,
      inApp,
      email,
    }: {
      event: string;
      inApp: boolean;
      email: boolean;
    }) => setNotificationPref(event, inApp, email),
    // Optimistic toggle: apply locally, roll back on failure.
    onMutate: async (vars) => {
      await queryClient.cancelQueries({ queryKey: NOTIFICATION_KEYS.prefs });
      const previous = queryClient.getQueryData<NotificationPrefItem[]>(
        NOTIFICATION_KEYS.prefs,
      );
      queryClient.setQueryData<NotificationPrefItem[]>(
        NOTIFICATION_KEYS.prefs,
        (old) =>
          old?.map((p) =>
            p.event === vars.event
              ? { ...p, in_app: vars.inApp, email: vars.email }
              : p,
          ),
      );
      return { previous };
    },
    onError: (_err, _vars, context) => {
      if (context?.previous) {
        queryClient.setQueryData(NOTIFICATION_KEYS.prefs, context.previous);
      }
    },
    onSettled: () => {
      void queryClient.invalidateQueries({
        queryKey: NOTIFICATION_KEYS.prefs,
      });
    },
  });

  const prefs = prefsQuery.data;

  return (
    <div className="mt-8">
      <SectionBar label="Notification preferences" />
      {prefsQuery.isLoading && (
        <div className="space-y-2 pt-2">
          <Skeleton className="h-[47px] w-full" />
          <Skeleton className="h-[47px] w-full" />
        </div>
      )}
      {prefsQuery.isError && (
        <Alert variant="destructive" className="mt-2">
          <AlertDescription>
            {prefsQuery.error instanceof ApiError
              ? prefsQuery.error.message
              : "Failed to load notification preferences"}
          </AlertDescription>
        </Alert>
      )}
      {setPref.isError && (
        <Alert variant="destructive" className="mt-2">
          <AlertDescription>
            {setPref.error instanceof ApiError
              ? setPref.error.message
              : "Failed to save preference"}
          </AlertDescription>
        </Alert>
      )}
      {prefs && (
        <ul className="divide-y divide-border-hairline">
          {prefs.map((p: NotificationPrefItem) => (
            <PrefRow key={p.event} p={p} setPref={setPref} />
          ))}
        </ul>
      )}
    </div>
  );
}

export default function Notifications() {
  const navigate = useNavigate();
  const invalidate = useInvalidateNotifications();

  const listQuery = useQuery({
    queryKey: NOTIFICATION_KEYS.list,
    queryFn: () => fetchNotifications(50),
  });
  const ctxQuery = useQuery({
    queryKey: ["notifications", "context"],
    queryFn: fetchNotificationContext,
    // Context changes rarely; the list refreshes on read mutations.
    staleTime: 60_000,
  });

  const markOne = useMutation({
    mutationFn: (id: string) => markNotificationRead(id),
    onSettled: () => void invalidate(),
  });
  const markAll = useMutation({
    mutationFn: markAllNotificationsRead,
    onSettled: () => void invalidate(),
  });

  const notifications = listQuery.data?.notifications ?? [];
  const unreadCount = listQuery.data?.unread_count ?? 0;
  const dayGroups = groupByDay(notifications);

  const openNotification = (n: NotificationItem, link: string | null) => {
    // Mark read first so the badge clears, then navigate. Navigation
    // happens even if the mark fails — the click's intent was to open it.
    void markOne
      .mutateAsync(n.id)
      .catch(() => undefined)
      .finally(() => {
        if (link) navigate(link);
      });
  };

  return (
    <div className="w-full">
      {/* Shell 49px header bar — the inbox has no workspace scope, so no
          PageHeader breadcrumb. */}
      <div className="flex h-[49px] shrink-0 items-center gap-2.5 border-b border-border bg-[#0c0c0e] px-3 pl-4">
        <span className="text-xs font-semibold text-ink">Notifications</span>
        {unreadCount > 0 && (
          <span className="text-xs text-ink-muted">
            {unreadCount} unread
          </span>
        )}
        <div className="ml-auto">
          <Button
            variant="ghost"
            size="sm"
            disabled={markAll.isPending || unreadCount === 0}
            onClick={() => void markAll.mutateAsync().catch(() => undefined)}
          >
            <CheckCheck className="mr-1.5 h-4 w-4" />
            Mark all read
          </Button>
        </div>
      </div>

      <div className="mx-auto w-full max-w-3xl space-y-4 p-6">
        {(listQuery.isError || markOne.isError || markAll.isError) && (
          <Alert variant="destructive">
            <AlertDescription>
              {(() => {
                const err = listQuery.error ?? markOne.error ?? markAll.error;
                return err instanceof ApiError
                  ? err.message
                  : "Something went wrong";
              })()}
            </AlertDescription>
          </Alert>
        )}

        {listQuery.isLoading && (
          <div className="space-y-2">
            <Skeleton className="h-[47px] w-full" />
            <Skeleton className="h-[47px] w-full" />
            <Skeleton className="h-[47px] w-full" />
          </div>
        )}

        {!listQuery.isLoading && notifications.length === 0 && (
          <EmptyState icon={Bell} title="No notifications yet">
            <p>
              You&apos;ll see assignments, comments and state changes here
              when they happen.
            </p>
          </EmptyState>
        )}

        {dayGroups.map((g) => (
          <section key={g.label}>
            <SectionBar
              label={g.label}
              count={g.items.filter((n) => n.read_at === null).length || undefined}
            />
            <ul className="mt-2 divide-y divide-border-hairline overflow-hidden rounded-md border border-border">
              {g.items.map((n) => (
                <NotificationRow
                  key={n.id}
                  n={n}
                  ctx={ctxQuery.data}
                  onOpen={openNotification}
                />
              ))}
            </ul>
          </section>
        ))}

        <PrefsSection />
      </div>
    </div>
  );
}
