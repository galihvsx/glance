// Notifications sheet for the app shell (T2).
//
// 400px right-side Sheet whose open state lives in the shell context
// (T1). Reuses the exact query keys the /notifications page uses
// (NOTIFICATION_KEYS.list + the "notifications"/"context" key), so the
// sheet and the page share one cache — no duplicate fetching.

import { useNavigate } from "react-router-dom";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Bell, CheckCheck } from "lucide-react";
import { useShell } from "./shell-context";
import {
  Sheet,
  SheetContent,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "../ui/sheet";
import { Avatar, AvatarFallback } from "../ui/avatar";
import { Badge } from "../ui/badge";
import { Button } from "../ui/button";
import { Skeleton } from "../ui/skeleton";
import {
  NOTIFICATION_KEYS,
  eventLabel,
  fetchNotifications,
  markAllNotificationsRead,
  markNotificationRead,
  useInvalidateNotifications,
  type NotificationItem,
} from "../../lib/notifications";
import {
  actorInitial,
  fetchNotificationContext,
  issueLink,
  type NotificationContext,
} from "../../lib/notificationContext";
import { relativeTime } from "../../lib/relativeTime";
import { cn } from "cn";

const EMPTY_CONTEXT: NotificationContext = {
  projects: new Map(),
  actors: new Map(),
};

/**
 * Sheet open state from the shell context, with a no-provider fallback so
 * the sheet renders (closed) outside <ShellProvider> — e.g. in tests.
 */
function useSheetState(): {
  open: boolean;
  setOpen: (v: boolean) => void;
} {
  try {
    // The provider is either present for the whole tree or absent — the
    // throw behavior never varies between renders, so this try/catch is
    // not a conditional hook call in practice.
    // oxlint-disable-next-line react-hooks/rules-of-hooks
    const { notificationsOpen, setNotificationsOpen } = useShell();
    return { open: notificationsOpen, setOpen: setNotificationsOpen };
  } catch {
    return { open: false, setOpen: () => undefined };
  }
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
  const actorId = n.payload.actor_id;
  const actor =
    typeof actorId === "string" && ctx ? (ctx.actors.get(actorId) ?? null) : null;
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
          <Badge variant="outline" className="text-[0.65rem]">
            {eventLabel(n.type)}
          </Badge>
          <span className="text-xs text-muted-foreground" title={n.created_at}>
            {relativeTime(n.created_at)}
          </span>
        </div>
      </div>
    </button>
  );
}

export default function NotificationsSheet() {
  const navigate = useNavigate();
  const { open, setOpen } = useSheetState();
  const invalidate = useInvalidateNotifications();

  // Same query keys as pages/Notifications.tsx — shared cache, one fetch.
  const listQuery = useQuery({
    queryKey: NOTIFICATION_KEYS.list,
    queryFn: () => fetchNotifications(50),
  });
  const ctxQuery = useQuery({
    queryKey: ["notifications", "context"],
    queryFn: fetchNotificationContext,
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
    setOpen(false);
    void markOne
      .mutateAsync(n.id)
      .catch(() => undefined)
      .finally(() => {
        if (link) navigate(link);
      });
  };

  const viewAll = () => {
    setOpen(false);
    navigate("/notifications");
  };

  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetContent side="right" className="sm:max-w-[400px]">
        <SheetHeader>
          <div className="flex items-center justify-between pr-8">
            <SheetTitle className="flex items-center gap-2">
              Notifications
              {unreadCount > 0 && (
                <Badge variant="secondary" aria-label={`${unreadCount} unread`}>
                  {unreadCount}
                </Badge>
              )}
            </SheetTitle>
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
        </SheetHeader>

        <div className="flex-1 space-y-2 overflow-y-auto px-4">
          {listQuery.isLoading && (
            <div className="space-y-2" aria-label="Loading notifications">
              <Skeleton className="h-16 w-full" />
              <Skeleton className="h-16 w-full" />
              <Skeleton className="h-16 w-full" />
            </div>
          )}
          {listQuery.isError && (
            <p className="py-8 text-center text-sm text-muted-foreground">
              Couldn&apos;t load notifications.
            </p>
          )}
          {!listQuery.isLoading && !listQuery.isError && notifications.length === 0 && (
            <div className="flex flex-col items-center gap-2 py-12 text-center">
              <Bell className="h-8 w-8 text-muted-foreground" />
              <p className="text-sm font-medium">You&apos;re all caught up</p>
              <p className="text-xs text-muted-foreground">
                New notifications will show up here.
              </p>
            </div>
          )}
          {notifications.map((n) => (
            <NotificationRow
              key={n.id}
              n={n}
              ctx={ctxQuery.data}
              onOpen={openNotification}
            />
          ))}
        </div>

        <SheetFooter className="border-t">
          <Button variant="ghost" size="sm" onClick={viewAll}>
            View all notifications
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  );
}
