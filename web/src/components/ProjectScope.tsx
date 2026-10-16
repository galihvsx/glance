// ProjectScope (Task 25): layout for all /w/:slug/p/:identifier/* routes.
// Owns the realtime connection for the project (project channel +
// resyncProject) and the issue:{uuid} subscription (via
// IssueDetailContent). Nested inside WorkspaceScope, which owns the
// socket lifecycle for workspace keys and the connection status dot —
// ProjectScope must NOT render its own <ConnectionDot /> so project
// pages show exactly one dot (C43T0). The global command palette moved
// to the app shell in C18T0 (workspace-global). The global keyboard
// shortcuts are installed once app-wide by ShortcutsHost.

import { useEffect } from "react";
import { Outlet, useParams } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { getRealtimeClient } from "../lib/ws";
import { useAuth } from "../lib/auth";
import {
  handleReconnect,
  registerProjectReconnect,
  useProjectRealtime,
} from "../lib/realtime";

export default function ProjectScope() {
  const { slug = "", identifier = "" } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const queryClient = useQueryClient();
  // C41T2: the /me session user id feeds the user:{id} realtime
  // subscription (notification.created arrives ONLY there).
  const { user } = useAuth();

  // Own the socket lifecycle + reconnect resync for this project scope.
  // (getRealtimeClient merges options, so mount order vs. scopes that
  // only subscribe doesn't matter.)
  //
  // Both scopes register the SAME stable handleReconnect dispatcher
  // (realtime.tsx) — the WS client keeps only the last-registered
  // onReconnect and React runs child effects before parent ones, so
  // registering different handlers per scope raced by mount order
  // (review fix: on a fresh project-page load the workspace-only
  // handler overwrote the composed one and resyncProject never ran).
  // The dispatcher reads the currently-registered callbacks at
  // reconnect time. This scope's cleanup unregisters only its own
  // project callback (identity guard in registerProjectReconnect).
  useEffect(() => {
    const client = getRealtimeClient({ onReconnect: handleReconnect });
    client.connect();
    return registerProjectReconnect({ queryClient, slug, identifier });
  }, [queryClient, slug, identifier]);

  useProjectRealtime({ slug, identifier, userId: user?.id });

  return <Outlet />;
}
