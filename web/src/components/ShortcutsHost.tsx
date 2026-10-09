import { useEffect, useState } from "react";
import { useMatch, useNavigate } from "react-router-dom";
import {
  installGlobalShortcuts,
  useShortcutAction,
} from "../lib/shortcuts";
import ShortcutCheatsheet from "./ShortcutCheatsheet";

// ShortcutsHost owns the global keydown listener for the whole app
// (mounted once inside the router) and wires the app-level shortcut
// actions: the cheatsheet modal, overlay dismissal, and g-chord
// navigation. Page-level actions (new-issue, focus-search, j/k) are
// registered by the pages themselves.
export default function ShortcutsHost() {
  const navigate = useNavigate();
  const workspaceMatch = useMatch("/w/:slug/*");
  const slug = workspaceMatch?.params.slug;
  const [cheatsheetOpen, setCheatsheetOpen] = useState(false);

  useEffect(() => installGlobalShortcuts(), []);

  useShortcutAction("open-cheatsheet", () => setCheatsheetOpen(true));
  useShortcutAction("close-topmost", () => setCheatsheetOpen(false));
  useShortcutAction("goto-home", () => navigate("/"));
  useShortcutAction("goto-mywork", () => {
    if (slug) navigate(`/w/${slug}/my-work`);
    else navigate("/w");
  });

  return (
    <ShortcutCheatsheet
      open={cheatsheetOpen}
      onOpenChange={setCheatsheetOpen}
    />
  );
}
