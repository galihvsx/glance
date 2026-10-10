// Shell sidebar footer (T2): profile row + theme toggle.
//
// Expanded: avatar + name/email button opening the account dropdown.
// Rail (collapsed): avatar-only icon button with a tooltip.

import { useNavigate } from "react-router-dom";
import type { ReactElement } from "react";
import { Download, KeyRound, LogOut, ShieldCheck, User, X } from "lucide-react";
import { useAuth } from "../../lib/auth";
import { initials } from "../../lib/profile";
import { useInstallPrompt } from "../../hooks/use-install-prompt";
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

// Subtle PWA install affordance (C15T4): appears only while the
// beforeinstallprompt event is available; dismissal persists.
function InstallButton() {
  const { canInstall, install, dismiss } = useInstallPrompt();
  if (!canInstall) return null;
  return (
    <span className="flex items-center">
      <Tooltip>
        <TooltipTrigger
          render={
            <button
              type="button"
              onClick={() => void install()}
              aria-label="Install glance app"
              className="inline-flex h-8 w-8 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
            >
              <Download className="h-4 w-4" />
            </button>
          }
        />
        <TooltipContent side="top">Install app</TooltipContent>
      </Tooltip>
      <button
        type="button"
        onClick={dismiss}
        aria-label="Dismiss install prompt"
        className="inline-flex h-6 w-6 items-center justify-center rounded-md text-muted-foreground/70 transition-colors hover:bg-accent hover:text-accent-foreground"
      >
        <X className="h-3 w-3" />
      </button>
    </span>
  );
}

export default function ShellFooter({  collapsed = false,
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
      <InstallButton />
    </div>
  );
}
