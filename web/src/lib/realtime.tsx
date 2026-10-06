// React bindings for the realtime WS client (Task 25).
//
// useProjectRealtime subscribes to the workspace-qualified project channel
// (project:{slug}:{identifier}, R11) and, when issueUuid is set, the issue
// channel too. Events invalidate TanStack Query caches with prefix keys —
// targeted invalidation of the list/detail/board queries, never a full
// refetch. The client refcounts channel subscriptions, so mounting this in
// both a layout and a page is safe.
//
// Reconnect resync (spec §6: no replay buffer): on reconnect the client
// hands us the `at` of the last received event; we probe
// ?updated_after=<at>&per_page=1 and invalidate the issue caches only when
// something actually changed (soft-deletes bump updated_at, so they're
// caught too). Cycles/intake change rarely and are cheap — unconditional
// invalidation.

import { useEffect, useRef, useState } from "react";
import { useQueryClient, type QueryClient } from "@tanstack/react-query";
import { api } from "./api";
import {
  getRealtimeClient,
  issueChannel,
  projectChannel,
  type ConnectionStatus,
  type WsEventFrame,
} from "./ws";

export function useConnectionStatus(): ConnectionStatus {
  const [status, setStatus] = useState<ConnectionStatus>("closed");
  useEffect(() => getRealtimeClient().onStatusChange(setStatus), []);
  return status;
}

interface RealtimeContext {
  slug: string;
  identifier: string;
}

type EventData = Record<string, unknown>;

function strField(data: EventData, key: string): string | undefined {
  const v = data[key];
  return typeof v === "string" ? v : undefined;
}

// handleRealtimeEvent maps one hub event onto targeted query invalidations.
// Exported for tests.
export function handleRealtimeEvent(
  queryClient: QueryClient,
  ctx: RealtimeContext,
  evt: WsEventFrame,
): void {
  const data = (evt.data ?? {}) as EventData;
  const id = strField(data, "id");
  const issueId = strField(data, "issue_id") ?? id;
  const { slug, identifier } = ctx;

  switch (evt.event) {
    case "issue.created":
    case "issue.updated":
    case "issue.deleted":
      void queryClient.invalidateQueries({ queryKey: ["issues", slug, identifier] });
      void queryClient.invalidateQueries({
        queryKey: ["board-issues", slug, identifier],
      });
      if (id) {
        void queryClient.invalidateQueries({
          queryKey: ["issue", slug, identifier, id],
        });
        void queryClient.invalidateQueries({
          queryKey: ["history", slug, identifier, id],
        });
      }
      break;
    case "comment.created":
      if (issueId) {
        void queryClient.invalidateQueries({
          queryKey: ["comments", slug, identifier, issueId],
        });
        void queryClient.invalidateQueries({
          queryKey: ["history", slug, identifier, issueId],
        });
      }
      break;
    case "cycle.updated":
      void queryClient.invalidateQueries({ queryKey: ["cycles", slug, identifier] });
      void queryClient.invalidateQueries({
        queryKey: ["cycle-issues", slug, identifier],
      });
      break;
    case "intake.updated":
      void queryClient.invalidateQueries({ queryKey: ["intake", slug, identifier] });
      break;
    default:
      // Unknown future events (e.g. notification.created): ignore.
      break;
  }
}

function projectBase(slug: string, identifier: string): string {
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;
}

// resyncProject runs on reconnect. Exported for tests.
export async function resyncProject(
  queryClient: QueryClient,
  ctx: RealtimeContext,
  since: string | null,
): Promise<void> {
  const { slug, identifier } = ctx;
  let changed = true;
  if (since) {
    try {
      const probe = await api.get<{ results: unknown[] }>(
        `${projectBase(slug, identifier)}/issues?updated_after=${encodeURIComponent(since)}&per_page=1`,
      );
      changed = probe.results.length > 0;
    } catch {
      // Probe failed — assume the worst and resync everything.
      changed = true;
    }
  }
  if (changed) {
    await queryClient.invalidateQueries({ queryKey: ["issues", slug, identifier] });
    await queryClient.invalidateQueries({
      queryKey: ["board-issues", slug, identifier],
    });
    await queryClient.invalidateQueries({ queryKey: ["issue", slug, identifier] });
    await queryClient.invalidateQueries({ queryKey: ["history", slug, identifier] });
    await queryClient.invalidateQueries({
      queryKey: ["comments", slug, identifier],
    });
  }
  await queryClient.invalidateQueries({ queryKey: ["cycles", slug, identifier] });
  await queryClient.invalidateQueries({
    queryKey: ["cycle-issues", slug, identifier],
  });
  await queryClient.invalidateQueries({ queryKey: ["intake", slug, identifier] });
}

interface UseProjectRealtimeOpts extends RealtimeContext {
  issueUuid?: string;
}

// useProjectRealtime subscribes to project (+issue) channels and wires
// invalidation. Resync-on-reconnect is the layout's job (single owner);
// pass nothing here — this hook only subscribes.
export function useProjectRealtime({
  slug,
  identifier,
  issueUuid,
}: UseProjectRealtimeOpts): void {
  const queryClient = useQueryClient();
  const ref = useRef({ queryClient, slug, identifier });
  ref.current = { queryClient, slug, identifier };

  useEffect(() => {
    const client = getRealtimeClient();
    client.connect();
    const ctx = { slug, identifier };
    const unsubs = [
      client.subscribe(projectChannel(slug, identifier), (evt) =>
        handleRealtimeEvent(ref.current.queryClient, ctx, evt),
      ),
    ];
    if (issueUuid) {
      unsubs.push(
        client.subscribe(issueChannel(issueUuid), (evt) =>
          handleRealtimeEvent(ref.current.queryClient, ctx, evt),
        ),
      );
    }
    return () => {
      unsubs.forEach((u) => u());
    };
    // Re-subscribe only when the identity changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [slug, identifier, issueUuid]);
}
