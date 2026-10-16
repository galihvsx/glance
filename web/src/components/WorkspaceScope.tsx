// WorkspaceScope (C43T0): layout for all /w/:slug/* routes — workspace
// pages AND the project routes nested under it. Owns the realtime
// connection for the workspace (single resync owner for workspace keys)
// and the connection status dot. ProjectScope (nested inside) owns the
// project channel + resyncProject; the socket client's refcounting makes
// the double workspace+user subscription on project pages benign.

import { useEffect } from "react";
import { Outlet, useParams } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { getRealtimeClient } from "../lib/ws";
import { useAuth } from "../lib/auth";
import { handleReconnect, registerWorkspaceReconnect, useWorkspaceRealtime } from "../lib/realtime";
import ConnectionDot from "./ConnectionDot";

export default function WorkspaceScope() {
  const { slug = "" } = useParams<{ slug: string }>();
  const queryClient = useQueryClient();
  // C41T2: the /me session user id feeds the user:{id} realtime
  // subscription (notification.created arrives ONLY there).
  const { user } = useAuth();

  // Own the socket lifecycle + reconnect resync for workspace keys.
  // (getRealtimeClient merges options, so mount order vs. scopes that
  // only subscribe doesn't matter. Both scopes register the SAME stable
  // handleReconnect dispatcher — see ProjectScope's comment for why
  // per-scope handlers raced. This scope's cleanup unregisters only its
  // own workspace callback.)
  useEffect(() => {
    const client = getRealtimeClient({ onReconnect: handleReconnect });
    client.connect();
    return registerWorkspaceReconnect({ queryClient, slug });
  }, [queryClient, slug]);

  useWorkspaceRealtime({ slug, userId: user?.id });

  return (
    <>
      <ConnectionDot />
      <Outlet />
    </>
  );
}
