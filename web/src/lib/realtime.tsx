// React bindings for the realtime WS client (Task 25, cycle 41: six
// *.updated handlers + workspace subscription).
//
// useProjectRealtime subscribes to the workspace-qualified project channel
// (project:{slug}:{identifier}, R11), the workspace channel
// (workspace:{slug} — initiative.updated arrives ONLY here, initiatives are
// workspace-scoped), and, when issueUuid is set, the issue channel too.
// Events invalidate TanStack Query caches with prefix keys — targeted
// invalidation of the list/detail/board queries, never a full refetch.
// The client refcounts channel subscriptions, so mounting this in both a
// layout and a page is safe.
//
// Double-delivery: most events fan out to BOTH the project and the
// workspace channel, so the handler runs twice per event. That is benign:
// the handler's only effect is TanStack invalidation, which is idempotent
// (no setQueryData, navigation, toast, or mutation side effects).
//
// Reconnect resync (spec §6: no replay buffer): on reconnect the client
// hands us the `at` of the last received event; we probe
// ?updated_after=<at>&per_page=1 and invalidate the issue caches only when
// something actually changed (soft-deletes bump updated_at, so they're
// caught too). Cycles/intake/modules/pages/releases/templates/custom
// fields/initiatives change rarely and are cheap — unconditional
// invalidation.

import { useEffect, useRef, useState } from "react";
import { useQueryClient, type QueryClient } from "@tanstack/react-query";
import { api } from "./api";
import {
  getRealtimeClient,
  issueChannel,
  projectChannel,
  workspaceChannel,
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
    case "module.updated":
      // Modules.tsx invalidates modulesKey + module-issues on its own
      // mutations; Add/RemoveModuleIssues announce via this same event, so
      // membership caches go stale too.
      void queryClient.invalidateQueries({ queryKey: ["modules", slug, identifier] });
      void queryClient.invalidateQueries({
        queryKey: ["module-issues", slug, identifier],
      });
      break;
    case "page.updated":
      // Pages.tsx invalidates pagesKey + page-revisions on its own
      // mutations; UpdatePage snapshots a revision on title/content change.
      // PageReactions.tsx holds the per-page detail query (reactions).
      void queryClient.invalidateQueries({ queryKey: ["pages", slug, identifier] });
      void queryClient.invalidateQueries({
        queryKey: ["page-revisions", slug, identifier],
      });
      if (id) {
        void queryClient.invalidateQueries({
          queryKey: ["page-detail", slug, identifier, id],
        });
      }
      break;
    case "release.updated":
      // Releases.tsx keys: releasesKey + release-issues + release-notes.
      // Assign/UnassignReleaseIssues announce via this same event, so the
      // issue list and the derived notes go stale too.
      void queryClient.invalidateQueries({ queryKey: ["releases", slug, identifier] });
      void queryClient.invalidateQueries({
        queryKey: ["release-issues", slug, identifier],
      });
      void queryClient.invalidateQueries({
        queryKey: ["release-notes", slug, identifier],
      });
      break;
    case "template.updated":
      void queryClient.invalidateQueries({ queryKey: ["templates", slug, identifier] });
      break;
    case "custom_field.updated": {
      // The payload's `id` is ambiguous by design (custom_fields.go):
      // the field id for field CRUD, the issue id for per-issue value
      // writes (SetCustomValues / ClearCustomValue announce with the
      // issue id and emit NO issue.* event). Custom values ride inside
      // the issue-detail query (?include_custom=1), so invalidating the
      // issue detail refreshes a second tab's open issue after a value
      // write. For field-CRUD events the id is a field id, which matches
      // no ["issue", ...] key — a harmless no-op.
      void queryClient.invalidateQueries({
        queryKey: ["custom-fields", slug, identifier],
      });
      if (id) {
        void queryClient.invalidateQueries({
          queryKey: ["issue", slug, identifier, id],
        });
      }
      break;
    }
    case "initiative.updated":
      // Initiatives are workspace-scoped: list key is ["initiatives", slug]
      // (no identifier). InitiativeDetail.tsx invalidates all three on its
      // own mutations; mirror that set so an open detail view refreshes.
      void queryClient.invalidateQueries({ queryKey: ["initiatives", slug] });
      if (id) {
        void queryClient.invalidateQueries({
          queryKey: ["initiative", slug, id],
        });
        void queryClient.invalidateQueries({
          queryKey: ["initiative-rollup", slug, id],
        });
      }
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
  await queryClient.invalidateQueries({ queryKey: ["modules", slug, identifier] });
  await queryClient.invalidateQueries({
    queryKey: ["module-issues", slug, identifier],
  });
  await queryClient.invalidateQueries({ queryKey: ["pages", slug, identifier] });
  await queryClient.invalidateQueries({
    queryKey: ["page-revisions", slug, identifier],
  });
  await queryClient.invalidateQueries({
    queryKey: ["page-detail", slug, identifier],
  });
  await queryClient.invalidateQueries({ queryKey: ["releases", slug, identifier] });
  await queryClient.invalidateQueries({
    queryKey: ["release-issues", slug, identifier],
  });
  await queryClient.invalidateQueries({
    queryKey: ["release-notes", slug, identifier],
  });
  await queryClient.invalidateQueries({ queryKey: ["templates", slug, identifier] });
  await queryClient.invalidateQueries({
    queryKey: ["custom-fields", slug, identifier],
  });
  await queryClient.invalidateQueries({ queryKey: ["initiatives", slug] });
  await queryClient.invalidateQueries({ queryKey: ["initiative", slug] });
  await queryClient.invalidateQueries({
    queryKey: ["initiative-rollup", slug],
  });
}

interface UseProjectRealtimeOpts extends RealtimeContext {
  issueUuid?: string;
}

// useProjectRealtime subscribes to project (+workspace, +issue) channels
// and wires invalidation. Resync-on-reconnect is the layout's job (single
// owner); pass nothing here — this hook only subscribes.
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
      // Workspace channel: initiative.updated arrives ONLY here
      // (initiatives are workspace-scoped). Other events arrive twice
      // (project + workspace) — benign, invalidation is idempotent.
      client.subscribe(workspaceChannel(slug), (evt) =>
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
