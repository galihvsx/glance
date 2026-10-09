// TopBar (T1): sticky 48px bar at the top of the main column.
//
// Left: sidebar trigger + read-only context label (workspace / project from
// route params, no new data fetching). Right: ⌘K button that fires the
// existing command palette's "toggle-palette" shortcut action (the palette
// itself is mounted by ProjectScope on project routes).

import { useMatch, useParams } from "react-router-dom";
import { PanelLeftIcon, Search } from "lucide-react";
import { emitShortcutAction } from "../../lib/shortcuts";
import { useWorkspaces } from "../../lib/useWorkspaces";
import { useShell } from "./shell-context";
import { useSidebar } from "../ui/sidebar";
import { Button } from "../ui/button";
import { Kbd } from "../ui/kbd";

function ShellTrigger() {
  const { setMobileOpen } = useShell();
  const { isMobile, toggleSidebar } = useSidebar();
  return (
    <Button
      variant="ghost"
      size="icon-sm"
      aria-label="Toggle sidebar"
      onClick={() => {
        // The shadcn sidebar renders a Sheet drawer below 768px and a
        // desktop rail/expanded sidebar at/above it; the drawer's open state
        // is driven through the shell context (auto-closes on navigation).
        if (isMobile) setMobileOpen(true);
        else toggleSidebar();
      }}
    >
      <PanelLeftIcon />
      <span className="sr-only">Toggle sidebar</span>
    </Button>
  );
}

function ContextLabel() {
  const { slug, identifier } = useParams<{ slug: string; identifier: string }>();
  const { data: workspaces } = useWorkspaces();
  const workspaceName = workspaces?.find((w) => w.slug === slug)?.name ?? slug;

  const parts: string[] = [];
  if (workspaceName) parts.push(workspaceName);
  if (identifier) parts.push(identifier.toUpperCase());

  if (parts.length === 0) return null;
  return (
    <span
      aria-label="Current context"
      className="truncate text-sm text-muted-foreground"
    >
      {parts.join(" / ")}
    </span>
  );
}

export default function TopBar() {
  // The command palette is mounted by ProjectScope, i.e. only on project
  // routes — showing the button elsewhere would be a dead control.
  const projectMatch = useMatch("/w/:slug/p/:identifier/*");
  return (
    <header className="sticky top-0 z-30 flex h-12 shrink-0 items-center gap-1 border-b bg-background px-2">
      <ShellTrigger />
      <ContextLabel />
      <div className="flex-1" />
      {projectMatch && (
        <Button
          variant="ghost"
          size="sm"
          className="text-muted-foreground"
          onClick={() => emitShortcutAction("toggle-palette")}
          aria-label="Open command palette"
        >
          <Search />
          <span className="hidden sm:inline">Search</span>
          <Kbd>⌘K</Kbd>
        </Button>
      )}
    </header>
  );
}
