// Notification center (C5T6): /notifications page.
//
// List: actor avatar initial, title text, relative time, issue link.
// Click a row → mark read, then navigate to the issue. "Mark all read"
// marks everything read. Prefs section toggles delivery per event type
// against the real GET/PUT /notification-prefs endpoints.

import { Link, useNavigate } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Bell, CheckCheck } from "lucide-react";
import { ApiError } from "../lib/api";
import {
  NOTIFICATION_KEYS,
  eventLabel,
  fetchNotificationPrefs,
  fetchNotifications,
  markAllNotificationsRead,
  markNotificationRead,
  setNotificationPref,
  useInvalidateNotifications,
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
              <li
                key={p.event}
                className="flex items-center justify-between gap-4 py-3"
              >
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
                </div>
              </li>
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
