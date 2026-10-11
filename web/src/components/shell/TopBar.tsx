// TopBar (C19T1): 44px bar across the top of the main column, rebuilt
// for the Plane layout (prototype Screen 1, literal).
//
// 3-column grid: minmax(180px,1fr) | minmax(240px,480px) |
// minmax(250px,1fr). LEFT: workspace switcher (30px, 20px avatar, name,
// chevron — the reopen-sidebar control lives inside it when collapsed).
// CENTER: global search (30px, ⌘K kbd, opens the command palette on
// every route). RIGHT: Get started pill, Inbox icon (notifications
// sheet), Help icon (shortcut cheatsheet), AI button (command palette —
// glance has no standalone AI surface yet), 25px user avatar (account
// menu).
//
// Responsive: ≤900px hides Get started + AI; ≤560px compresses further
// (search kbd hidden).

import { Link } from "react-router-dom";
import { HelpCircle, Inbox, Search, Sparkles } from "lucide-react";
import { useAuth } from "../../lib/auth";
import { initials } from "../../lib/profile";
import { emitShortcutAction } from "../../lib/shortcuts";
import { useShell } from "./shell-context";
import { AccountMenu } from "./ShellFooter";
import WorkspaceSwitcher from "./WorkspaceSwitcher";

function IconButton({
  label,
  title,
  onClick,
  children,
}: {
  label: string;
  title?: string;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      aria-label={label}
      title={title ?? label}
      onClick={onClick}
      className="flex size-7 shrink-0 items-center justify-center rounded-[5px] text-ink-secondary transition-colors hover:bg-wash-hover hover:text-ink"
    >
      {children}
    </button>
  );
}

function SearchTrigger() {
  return (
    <button
      type="button"
      onClick={() => emitShortcutAction("toggle-palette")}
      aria-label="Open command palette"
      className="relative h-[30px] w-full rounded-md border border-border bg-[#111113] text-left transition-colors hover:border-[#3f3f46]"
    >
      <Search
        className="absolute top-2 left-[9px] size-[14px] text-ink-muted"
        aria-hidden="true"
      />
      <span className="block truncate pr-[58px] pl-[31px] text-[11px] leading-[30px] text-ink-muted">
        Search or jump to…
      </span>
      <kbd className="absolute top-[5px] right-[5px] hidden rounded-[3px] border border-border px-1 font-mono text-[9px] leading-[18px] text-ink-muted min-[561px]:inline-block">
        ⌘ K
      </kbd>
    </button>
  );
}

function UserAvatar() {
  const { user, loading } = useAuth();
  if (loading || !user) {
    return (
      <span
        aria-hidden="true"
        className="size-[25px] shrink-0 animate-pulse rounded-full bg-wash-hover"
      />
    );
  }
  const label = user.name ?? user.email;
  return (
    <AccountMenu
      trigger={
        <button
          type="button"
          aria-label={`Account: ${label}`}
          title={label}
          className="flex size-[25px] shrink-0 items-center justify-center rounded-full border border-[rgba(255,255,255,0.12)] bg-[#3e3e46] text-[8px] font-bold text-white transition-opacity hover:opacity-90"
        >
          {initials(user.name, user.email)}
        </button>
      }
    />
  );
}

export default function TopBar() {
  const { setNotificationsOpen } = useShell();

  return (
    <header
      data-slot="topbar"
      className="grid h-11 shrink-0 grid-cols-[minmax(180px,1fr)_minmax(240px,480px)_minmax(250px,1fr)] items-center gap-[10px] border-b border-border bg-rail px-[10px]"
    >
      <div className="flex min-w-0 items-center">
        <WorkspaceSwitcher />
      </div>
      <div className="flex min-w-0 items-center justify-center">
        <SearchTrigger />
      </div>
      <div className="flex items-center justify-end gap-[3px]">
        <Link
          to="/onboarding"
          className="mr-1 hidden h-[27px] shrink-0 items-center rounded-full border border-border bg-surface px-[10px] text-[10px] text-ink-secondary transition-colors hover:bg-raised hover:text-ink min-[901px]:inline-flex"
        >
          Get started
        </Link>
        <IconButton
          label="Inbox"
          onClick={() => setNotificationsOpen(true)}
        >
          <Inbox className="size-[15px]" strokeWidth={1.6} aria-hidden="true" />
        </IconButton>
        <IconButton
          label="Help"
          title="Keyboard shortcuts"
          onClick={() => emitShortcutAction("open-cheatsheet")}
        >
          <HelpCircle className="size-[15px]" strokeWidth={1.6} aria-hidden="true" />
        </IconButton>
        <button
          type="button"
          onClick={() => emitShortcutAction("toggle-palette")}
          aria-label="AI assistant"
          title="AI assistant"
          className="mr-1 hidden h-7 shrink-0 items-center gap-[5px] rounded-[5px] border border-border bg-surface px-2 text-[10px] text-ink-secondary transition-colors hover:bg-raised hover:text-ink min-[901px]:inline-flex"
        >
          <Sparkles className="size-[13px]" aria-hidden="true" />
          AI
        </button>
        <UserAvatar />
      </div>
    </header>
  );
}
