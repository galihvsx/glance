// Header bell icon with the unread-count badge (C5T6).
//
// Mounted next to <ThemeToggle /> in each page's top bar. The count polls
// every 30s via useUnreadCount; TanStack Query pauses the interval while the
// tab is hidden (refetchIntervalInBackground defaults to false), so there
// is no polling cost for background tabs.

import { Link } from "react-router-dom";
import { Bell } from "lucide-react";
import { buttonVariants } from "../ui/button";
import { useUnreadCount } from "../../lib/notifications";
import { cn } from "cn";

function badgeText(count: number): string {
  return count > 99 ? "99+" : String(count);
}

export default function NotificationBell() {
  const { data: unread } = useUnreadCount();
  const count = unread ?? 0;
  const label =
    count > 0 ? `Notifications, ${count} unread` : "Notifications";

  return (
    <Link
      to="/notifications"
      className={cn(buttonVariants({ variant: "ghost", size: "icon" }), "relative")}
      aria-label={label}
      title={label}
    >
      <Bell className="h-4 w-4" />
      {count > 0 && (
        <span
          aria-hidden="true"
          className="absolute -right-0.5 -top-0.5 flex min-h-[1.1rem] min-w-[1.1rem] items-center justify-center rounded-full bg-destructive px-1 text-[0.65rem] font-semibold leading-none text-destructive-foreground"
        >
          {badgeText(count)}
        </span>
      )}
    </Link>
  );
}
