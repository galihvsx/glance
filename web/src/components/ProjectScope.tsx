// ProjectScope (Task 25): layout for all /w/:slug/p/:identifier/* routes.
// Owns the realtime connection for the project (single resync owner) and
// the connection status dot. The global command palette moved to the app
// shell in C18T0 (workspace-global). The global keyboard shortcuts are
// installed once app-wide by ShortcutsHost.

import { useEffect } from "react";
import { Outlet, useParams } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { getRealtimeClient } from "../lib/ws";
import { useAuth } from "../lib/auth";
import { resyncProject, useProjectRealtime } from "../lib/realtime";
import ConnectionDot from "./ConnectionDot";

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
  // (getRealtimeClient merges options, so mount order vs. pages that only
  // subscribe doesn't matter.)
  useEffect(() => {
    const client = getRealtimeClient({
      onReconnect: (since) =>
        void resyncProject(queryClient, { slug, identifier }, since),
    });
    client.connect();
  }, [queryClient, slug, identifier]);

  useProjectRealtime({ slug, identifier, userId: user?.id });

  return (
    <>
      <ConnectionDot />
      <Outlet />
    </>
  );
}
