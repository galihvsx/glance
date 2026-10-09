import { useEffect, useState } from "react";
import { useMatch, useNavigate } from "react-router-dom";
import {
  installGlobalShortcuts,
  useShortcutAction,
} from "../lib/shortcuts";
import {
  dispatchShellEvent,
  SHELL_CLOSE_OVERLAY,
  SHELL_OPEN_NOTIFICATIONS,
  SHELL_TOGGLE_SIDEBAR,
} from "../lib/shell-events";
import ShortcutCheatsheet from "./ShortcutCheatsheet";

// ShortcutsHost owns the global keydown listener for the whole app
// (mounted once inside the router, outside the app shell) and wires the
// app-level shortcut actions: the cheatsheet modal, overlay dismissal, and
// g-chord navigation. Page-level actions (new-issue, focus-search, j/k) are
// registered by the pages themselves.
//
// Shell-scoped actions (sidebar, notifications sheet) are forwarded to the
// AppShell via the window-event contract in ../lib/shell-events — the
// AppShell listens for them and flips its own context state. This keeps
// ShortcutsHost working on routes where the shell is not mounted.
export default function ShortcutsHost() {
  const navigate = useNavigate();
  const workspaceMatch = useMatch("/w/:slug/*");
  const slug = workspaceMatch?.params.slug;
  const [cheatsheetOpen, setCheatsheetOpen] = useState(false);

  useEffect(() => installGlobalShortcuts(), []);

  useShortcutAction("open-cheatsheet", () => setCheatsheetOpen(true));
  useShortcutAction("close-topmost", () => {
    setCheatsheetOpen(false);
    dispatchShellEvent(SHELL_CLOSE_OVERLAY);
  });
  useShortcutAction("goto-home", () => navigate("/"));
  useShortcutAction("goto-mywork", () => {
    if (slug) navigate(`/w/${slug}/my-work`);
    else navigate("/w");
  });
  useShortcutAction("toggle-sidebar", () =>
    dispatchShellEvent(SHELL_TOGGLE_SIDEBAR),
  );
  useShortcutAction("open-notifications", () =>
    dispatchShellEvent(SHELL_OPEN_NOTIFICATIONS),
  );

  return (
    <ShortcutCheatsheet
      open={cheatsheetOpen}
      onOpenChange={setCheatsheetOpen}
    />
  );
}
