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
  resyncProject,
  resyncWorkspace,
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
  // (getRealtimeClient merges options, so mount order vs. pages that only
  // subscribe doesn't matter.)
  //
  // The singleton keeps only the last-registered onReconnect, so while
  // ProjectScope is mounted its handler composes BOTH resyncs: the
  // project keys (resyncProject) plus the workspace keys
  // (resyncWorkspace — the sidebar's initiatives/objectives lists live
  // on project pages too). On unmount (e.g. navigating to a workspace
  // page, where WorkspaceScope stays mounted) restore WorkspaceScope's
  // workspace-only handler so a stale project resync never fires.
  useEffect(() => {
    const client = getRealtimeClient({
      onReconnect: (since) =>
        void (async () => {
          await resyncProject(queryClient, { slug, identifier }, since);
          await resyncWorkspace(queryClient, { slug }, since);
        })(),
    });
    client.connect();
    return () => {
      getRealtimeClient({
        onReconnect: (since) =>
          void resyncWorkspace(queryClient, { slug }, since),
      });
    };
  }, [queryClient, slug, identifier]);

  useProjectRealtime({ slug, identifier, userId: user?.id });

  return <Outlet />;
}
