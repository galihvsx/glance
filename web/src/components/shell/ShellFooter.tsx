// Shell sidebar footer (T2): profile row + theme toggle.
//
// Expanded: avatar + name/email button opening the account dropdown.
// Rail (collapsed): avatar-only icon button with a tooltip.

import { useNavigate } from "react-router-dom";
import type { ReactElement } from "react";
import { KeyRound, LogOut, ShieldCheck, User } from "lucide-react";
import { useAuth } from "../../lib/auth";
import { initials } from "../../lib/profile";
import ThemeToggle from "../ThemeToggle";
import { Avatar, AvatarFallback } from "../ui/avatar";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "../ui/dropdown-menu";
import { Skeleton } from "../ui/skeleton";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "../ui/tooltip";
import { cn } from "cn";

function AccountMenu({ trigger }: { trigger: ReactElement }) {
  const navigate = useNavigate();
  const { user, logout } = useAuth();

  return (
    <DropdownMenu>
      <DropdownMenuTrigger render={trigger} />
      <DropdownMenuContent align="start" side="top" className="w-56">
        <DropdownMenuItem onClick={() => navigate("/profile")}>
          <User className="h-4 w-4" />
          Profile
        </DropdownMenuItem>
        <DropdownMenuItem onClick={() => navigate("/settings/tokens")}>
          <KeyRound className="h-4 w-4" />
          API tokens
        </DropdownMenuItem>
        {user?.is_admin && (
          <DropdownMenuItem onClick={() => navigate("/admin")}>
            <ShieldCheck className="h-4 w-4" />
            Administration
          </DropdownMenuItem>
        )}
        <DropdownMenuSeparator />
        <DropdownMenuItem onClick={() => void logout()}>
          <LogOut className="h-4 w-4" />
          Log out
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

export default function ShellFooter({
  collapsed = false,
}: {
  /** Rail mode: avatar-only trigger with tooltip. */
  collapsed?: boolean;
}) {
  const { user, loading } = useAuth();

  if (loading || !user) {
    return (
      <div className="flex items-center gap-2 px-2 py-2">
        <Skeleton className="h-8 w-8 rounded-full" />
        {!collapsed && <Skeleton className="h-4 flex-1" />}
      </div>
    );
  }

  const label = user.name ?? user.email;
  const avatar = (
    <Avatar className="h-8 w-8 shrink-0">
      <AvatarFallback className="text-xs font-semibold">
        {initials(user.name, user.email)}
      </AvatarFallback>
    </Avatar>
  );

  return (
    <div
      className={cn(
        "flex items-center gap-1 px-2 py-2",
        collapsed && "flex-col justify-center",
      )}
    >
      {collapsed ? (
        <Tooltip>
          <AccountMenu
            trigger={
              <TooltipTrigger
                render={
                  <button
                    type="button"
                    className="rounded-full transition-opacity hover:opacity-80"
                    aria-label={`Account: ${label}`}
                  >
                    {avatar}
                  </button>
                }
              />
            }
          />
          <TooltipContent side="right">{label}</TooltipContent>
        </Tooltip>
      ) : (
        <AccountMenu
          trigger={
            <button
              type="button"
              className="flex min-w-0 flex-1 items-center gap-2 rounded-md px-1 py-1 text-left transition-colors hover:bg-accent"
              aria-label={`Account: ${label}`}
            >
              {avatar}
              <span className="min-w-0 flex-1">
                <span className="block truncate text-sm font-medium">
                  {user.name ?? "Account"}
                </span>
                <span className="block truncate text-xs text-muted-foreground">
                  {user.email}
                </span>
              </span>
            </button>
          }
        />
      )}
      <ThemeToggle />
    </div>
  );
}
