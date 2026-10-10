// Notification center (C5T6): /notifications page.
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
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "../components/ui/card";
import {
  NativeSelect,
  NativeSelectOption,
} from "../components/ui/native-select";
import { Input } from "../components/ui/input";
import { Skeleton } from "../components/ui/skeleton";
import { Switch } from "../components/ui/switch";
import { Alert, AlertDescription } from "../components/ui/alert";
import { cn } from "cn";

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
    <button
      type="button"
      onClick={() => onOpen(n, link)}
      className={cn(
        "flex w-full items-start gap-3 rounded-lg border p-3 text-left transition-colors",
        unread
          ? "border-primary/30 bg-primary/5 hover:bg-primary/10"
          : "border-border hover:bg-muted/50",
      )}
    >
      <Avatar className="h-8 w-8 shrink-0">
        <AvatarFallback className="text-xs font-semibold">
          {actor ? actorInitial(actor) : "?"}
        </AvatarFallback>
      </Avatar>
      <div className="min-w-0 flex-1">
        <p className="text-sm leading-snug">
          {unread && (
            <span
              aria-label="unread"
              className="mr-1.5 inline-block h-2 w-2 rounded-full bg-primary align-middle"
            />
          )}
          <span className={unread ? "font-medium" : ""}>{n.title}</span>
        </p>
        <div className="mt-1.5 flex flex-wrap items-center gap-2">
          {n.payload.display_id &&
            (link ? (
              <Link
                to={link}
                onClick={(e) => e.stopPropagation()}
                className="text-xs font-medium text-primary hover:underline"
              >
                {n.payload.display_id}
              </Link>
            ) : (
              <span className="text-xs text-muted-foreground">
                {n.payload.display_id}
              </span>
            ))}
          <span className="text-xs text-muted-foreground" title={n.created_at}>
            {relativeTime(n.created_at)}
          </span>
          <Badge variant="outline" className="text-[0.65rem]">
            {eventLabel(n.type)}
          </Badge>
        </div>
      </div>
    </button>
  );
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
      <label className="flex items-center gap-2 text-xs text-muted-foreground">
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
      <label className="flex items-center gap-2 text-xs text-muted-foreground">
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
      <label className="flex items-center gap-2 text-xs text-muted-foreground">
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
      <span className="text-xs text-muted-foreground">
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
    <label className="flex items-center gap-2 text-xs text-muted-foreground">
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
          <span className="text-sm font-medium">
            {eventLabel(p.event)}
            <span className="ml-2 text-xs font-normal text-muted-foreground">
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
    <li className="flex items-center justify-between gap-4 py-3">
      <span className="text-sm font-medium">{eventLabel(p.event)}</span>
      <div className="flex items-center gap-4">
        <label className="flex items-center gap-2 text-xs text-muted-foreground">
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
    <Card className="mt-8">
      <CardHeader>
        <CardTitle className="text-base">Notification preferences</CardTitle>
      </CardHeader>
      <CardContent>
        {prefsQuery.isLoading && (
          <div className="space-y-2">
            <Skeleton className="h-8 w-full" />
            <Skeleton className="h-8 w-full" />
          </div>
        )}
        {prefsQuery.isError && (
          <Alert variant="destructive">
            <AlertDescription>
              {prefsQuery.error instanceof ApiError
                ? prefsQuery.error.message
                : "Failed to load notification preferences"}
            </AlertDescription>
          </Alert>
        )}
        {setPref.isError && (
          <Alert variant="destructive" className="mb-3">
            <AlertDescription>
              {setPref.error instanceof ApiError
                ? setPref.error.message
                : "Failed to save preference"}
            </AlertDescription>
          </Alert>
        )}
        {prefs && (
          <ul className="divide-y divide-border">
            {prefs.map((p: NotificationPrefItem) => (
              <PrefRow key={p.event} p={p} setPref={setPref} />
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
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
    <div className="mx-auto w-full max-w-3xl p-6">

      <div className="mb-6 flex items-center justify-between">
        <h1 className="text-2xl font-semibold tracking-tight">Notifications</h1>
        <Button
          variant="outline"
          size="sm"
          disabled={markAll.isPending || unreadCount === 0}
          onClick={() => void markAll.mutateAsync().catch(() => undefined)}
        >
          <CheckCheck className="mr-1.5 h-4 w-4" />
          Mark all read
        </Button>
      </div>

      {(listQuery.isError || markOne.isError || markAll.isError) && (
        <Alert variant="destructive" className="mb-4">
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
          <Skeleton className="h-16 w-full" />
          <Skeleton className="h-16 w-full" />
          <Skeleton className="h-16 w-full" />
        </div>
      )}

      {!listQuery.isLoading && notifications.length === 0 && (
        <div className="flex flex-col items-center rounded-lg border border-dashed py-16 text-center">
          <Bell className="mb-3 h-8 w-8 text-muted-foreground" />
          <p className="text-sm font-medium">No notifications yet</p>
          <p className="mt-1 max-w-sm text-sm text-muted-foreground">
            You&apos;ll see assignments, comments and state changes here when
            they happen.
          </p>
        </div>
      )}

      <div className="space-y-2">
        {notifications.map((n) => (
          <NotificationRow
            key={n.id}
            n={n}
            ctx={ctxQuery.data}
            onOpen={openNotification}
          />
        ))}
      </div>

      <PrefsSection />
    </div>
  );
}
