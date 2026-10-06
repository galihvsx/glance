// ProjectScope (Task 25): layout for all /w/:slug/p/:identifier/* routes.
// Owns the realtime connection for the project (single resync owner),
// installs the global keyboard shortcuts, and mounts the command palette
// and connection status dot.

import { useEffect } from "react";
import { Outlet, useParams } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { getRealtimeClient } from "../lib/ws";
import { installGlobalShortcuts } from "../lib/shortcuts";
import { resyncProject, useProjectRealtime } from "../lib/realtime";
import CommandPalette from "./CommandPalette";
import ConnectionDot from "./ConnectionDot";

export default function ProjectScope() {
  const { slug = "", identifier = "" } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const queryClient = useQueryClient();

  // Own the socket lifecycle + reconnect resync for this project scope.
  // (getRealtimeClient merges options, so mount order vs. pages that only
  // subscribe doesn't matter.)
  useEffect(() => {
    const client = getRealtimeClient({
      onReconnect: (since) =>
        void resyncProject(queryClient, { slug, identifier }, since),
    });
    client.connect();
    const removeShortcuts = installGlobalShortcuts();
    return () => {
      removeShortcuts();
    };
  }, [queryClient, slug, identifier]);

  useProjectRealtime({ slug, identifier });

  return (
    <>
      <CommandPalette slug={slug} identifier={identifier} />
      <ConnectionDot />
      <Outlet />
    </>
  );
}
