// GlobalScope (C44T0): layout for ALL authenticated routes — mounted
// inside ProtectedRoute, above /onboarding and AppShell, so routes
// with no workspace slug (/, /notifications, /profile,
// /settings/tokens, /w, /onboarding) get realtime too. Owns:
//   - the user:{id} subscription (notification.created arrives ONLY
//     there) via useGlobalRealtime — the SAME handleRealtimeEvent case
//     the workspace/project hooks use, no second handler;
//   - the socket lifecycle + reconnect resync for the notifications
//     keys (registerGlobalReconnect through the stable handleReconnect
//     dispatcher — the WS client keeps only the last-registered
//     onReconnect, so per-scope handlers race; see realtime.tsx);
//   - the single <ConnectionDot /> for every authenticated route.
//     WorkspaceScope/ProjectScope render none, so /w/:slug/* routes
//     show exactly one dot (C43's dot moved here, not duplicated).

import { useEffect } from "react";
import { Outlet } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { getRealtimeClient } from "../lib/ws";
import { useAuth } from "../lib/auth";
import {
  handleReconnect,
  registerGlobalReconnect,
  useGlobalRealtime,
} from "../lib/realtime";
import ConnectionDot from "./ConnectionDot";

export default function GlobalScope() {
  const queryClient = useQueryClient();
  // The /me session user id feeds the user:{id} realtime subscription
  // (notification.created arrives ONLY there).
  const { user } = useAuth();

  // Own the socket lifecycle + reconnect resync for notifications
  // keys. Registers the SAME stable handleReconnect dispatcher as the
  // workspace/project scopes; this scope's cleanup unregisters only
  // its own global callback (identity guard in
  // registerGlobalReconnect). GlobalScope never unmounts while
  // authenticated, so the global resync survives workspace navigation
  // — unlike the workspace/project registrations, which come and go
  // with their routes.
  useEffect(() => {
    const client = getRealtimeClient({ onReconnect: handleReconnect });
    client.connect();
    return registerGlobalReconnect({ queryClient });
  }, [queryClient]);

  useGlobalRealtime({ userId: user?.id });

  return (
    <>
      <ConnectionDot />
      <Outlet />
    </>
  );
}
