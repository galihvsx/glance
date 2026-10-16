// React bindings for the realtime WS client (Task 25, cycle 41: six
// *.updated handlers + workspace subscription; C41T2: notification.created
// handler + user-channel subscription).
//
// useProjectRealtime subscribes to the workspace-qualified project channel
// (project:{slug}:{identifier}, R11), the workspace channel
// (workspace:{slug} — initiative.updated arrives ONLY here, initiatives are
// workspace-scoped), the user channel (user:{id} — notification.created
// arrives ONLY here) when the caller passes userId, and, when issueUuid is
// set, the issue channel too.
//
// useWorkspaceRealtime (C43T0) subscribes to the workspace + user channels
// only. WorkspaceScope mounts it on every /w/:slug/* route; ProjectScope
// mounts useProjectRealtime underneath on project routes, so the workspace
// and user channels end up double-subscribed there — refcounted-benign
// (one wire subscription per channel; the handler is idempotent).
// useGlobalRealtime (C44T0) subscribes to the user channel ONLY;
// GlobalScope mounts it above every authenticated route, so slug-less
// routes (/, /notifications, …) get notification.created too and the
// user channel is triple-subscribed on project routes — same benign
// overlap. Events invalidate TanStack Query caches with prefix keys — targeted
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
  userChannel,
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
    case "objective.updated":
      // Objectives are workspace-scoped (cross-task contract, cycle 43):
      // event `objective.updated`, channel `workspace:{slug}` ONLY,
      // payload `{id: objectiveID}`. The ["kr-checkins", slug, id]
      // invalidation is a PREFIX — it covers every KR of that objective
      // (key shape ["kr-checkins", slug, objectiveId, krId]).
      void queryClient.invalidateQueries({ queryKey: ["objectives", slug] });
      if (id) {
        void queryClient.invalidateQueries({
          queryKey: ["objective", slug, id],
        });
        void queryClient.invalidateQueries({
          queryKey: ["kr-checkins", slug, id],
        });
      }
      break;
    case "notification.created":
      // notification.created arrives ONLY on the user:{id} channel
      // (notify.go announces to the recipient). Every notifications key
      // starts with "notifications" (NOTIFICATION_KEYS list/unread/prefs/
      // schedule + ["notifications", "context"] in NotificationsSheet and
      // the Notifications page), so the bare prefix covers all of them.
      void queryClient.invalidateQueries({ queryKey: ["notifications"] });
      break;
    default:
      // Unknown future events: ignore.
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

interface WorkspaceRealtimeContext {
  slug: string;
}

// resyncWorkspace runs on reconnect from WorkspaceScope (C43T0).
// Exported for tests. Unconditionally invalidates workspace-level keys
// ONLY — never project keys (["issues", ...], ["board-issues", ...],
// cycles/modules/pages/releases/templates/custom-fields stay owned by
// resyncProject). Every key below was verified against its owning page;
// no key is invented:
//   initiatives/initiative/initiative-rollup — Initiatives.tsx,
//     InitiativeDetail.tsx, AppSidebar.tsx
//   objectives/objective/kr-checkins — Goals.tsx, GoalDetail.tsx,
//     AppSidebar.tsx (objectives list)
//   notifications (bare prefix) — Notifications.tsx (NOTIFICATION_KEYS
//     list/unread/prefs/schedule + ["notifications", "context"]),
//     NotificationsSheet.tsx
//   workload — Workload.tsx (["workload", slug, selectedIds])
//   risk — Risk.tsx
//   mywork — MyWork.tsx ([...myWorkKeys(slug).all, filter])
//   projects — GoalDetail.tsx, Initiatives.tsx, Workload.tsx,
//     AppSidebar.tsx, CommandPalette.tsx, MoveIssueDialog.tsx
//   workspace (prefix; covers ["workspace", slug, "members"] too) —
//     Goals.tsx, GoalDetail.tsx, Initiatives.tsx, InitiativeDetail.tsx
//   members — Goals.tsx, GoalDetail.tsx
//   webhooks / project-templates / workspace-audit-log —
//     WorkspaceSettings.tsx child sections (WebhooksSection,
//     ProjectTemplatesSection, AuditLogSection)
// Deliberately NOT here: ["project-issues", slug, ...] (GoalDetail link
// picker — a project-scoped issue listing, not a workspace key),
// ["workspace-role", slug] (IssueCustomFields, a project-page
// component), bare ["workspaces"] (global list, not slug-scoped).
// `since` is accepted for shape parity with resyncProject; the
// invalidation is unconditional (no delta probe for workspace keys).
export async function resyncWorkspace(
  queryClient: QueryClient,
  ctx: WorkspaceRealtimeContext,
  _since: string | null,
): Promise<void> {
  const { slug } = ctx;
  await queryClient.invalidateQueries({ queryKey: ["initiatives", slug] });
  await queryClient.invalidateQueries({ queryKey: ["initiative", slug] });
  await queryClient.invalidateQueries({
    queryKey: ["initiative-rollup", slug],
  });
  await queryClient.invalidateQueries({ queryKey: ["objectives", slug] });
  await queryClient.invalidateQueries({ queryKey: ["objective", slug] });
  await queryClient.invalidateQueries({ queryKey: ["kr-checkins", slug] });
  await queryClient.invalidateQueries({ queryKey: ["notifications"] });
  await queryClient.invalidateQueries({ queryKey: ["workload", slug] });
  await queryClient.invalidateQueries({ queryKey: ["risk", slug] });
  await queryClient.invalidateQueries({ queryKey: ["mywork", slug] });
  await queryClient.invalidateQueries({ queryKey: ["projects", slug] });
  await queryClient.invalidateQueries({ queryKey: ["workspace", slug] });
  await queryClient.invalidateQueries({ queryKey: ["members", slug] });
  await queryClient.invalidateQueries({ queryKey: ["webhooks", slug] });
  await queryClient.invalidateQueries({
    queryKey: ["project-templates", slug],
  });
  await queryClient.invalidateQueries({
    queryKey: ["workspace-audit-log", slug],
  });
}

// Reconnect dispatch (C43T0 review fix): the WS client keeps only the
// last-registered onReconnect, and React runs child passive effects
// before parent ones — so two scopes registering different handlers
// race by mount order. On a fresh project-page load, WorkspaceScope's
// workspace-only handler overwrote ProjectScope's composed handler and
// resyncProject silently never ran on reconnect (a regression vs
// pre-C43, where ProjectScope alone owned onReconnect). Instead all
// scopes register the SAME stable dispatcher below; it reads the
// currently-registered resync callbacks at reconnect time, so effect
// order no longer matters. Each registration returns an unregister
// cleanup that clears only its own registration (identity guard), so a
// stale cleanup can never clear a newer registration.
//
// C44T0 adds a third registration: the global owner (GlobalScope,
// mounted above every authenticated route) resyncs the notifications
// keys on reconnect, so routes outside WorkspaceScope (/,
// /notifications, /profile, …) also resync instead of going stale
// until the next poll/navigation. On workspace routes resyncWorkspace
// already invalidates ["notifications"], so the global resync there
// is a duplicate-but-idempotent invalidation — the same benign overlap
// as the triple user-channel subscription.
interface WorkspaceReconnectRegistration {
  queryClient: QueryClient;
  slug: string;
}

interface ProjectReconnectRegistration {
  queryClient: QueryClient;
  slug: string;
  identifier: string;
}

interface GlobalReconnectRegistration {
  queryClient: QueryClient;
}

let workspaceReconnect: WorkspaceReconnectRegistration | null = null;
let projectReconnect: ProjectReconnectRegistration | null = null;
let globalReconnect: GlobalReconnectRegistration | null = null;

export function registerWorkspaceReconnect(
  reg: WorkspaceReconnectRegistration,
): () => void {
  workspaceReconnect = reg;
  return () => {
    if (workspaceReconnect === reg) workspaceReconnect = null;
  };
}

export function registerProjectReconnect(
  reg: ProjectReconnectRegistration,
): () => void {
  projectReconnect = reg;
  return () => {
    if (projectReconnect === reg) projectReconnect = null;
  };
}

export function registerGlobalReconnect(
  reg: GlobalReconnectRegistration,
): () => void {
  globalReconnect = reg;
  return () => {
    if (globalReconnect === reg) globalReconnect = null;
  };
}

// resyncNotifications runs on reconnect from GlobalScope (C44T0).
// Exported for tests. Invalidates ONLY the bare ["notifications"]
// prefix — the same key set the live notification.created handler
// invalidates (see handleRealtimeEvent).
export async function resyncNotifications(
  queryClient: QueryClient,
): Promise<void> {
  await queryClient.invalidateQueries({ queryKey: ["notifications"] });
}

// handleReconnect is the single onReconnect handler all scopes
// register (getRealtimeClient merges options; last-writer-wins is
// harmless because the function reference is identical). Runs the
// project resync then the workspace resync — the same order as the
// pre-fix composed handler — then the global notifications resync.
// Exported for tests.
export async function handleReconnect(since: string | null): Promise<void> {
  if (projectReconnect) {
    await resyncProject(
      projectReconnect.queryClient,
      {
        slug: projectReconnect.slug,
        identifier: projectReconnect.identifier,
      },
      since,
    );
  }
  if (workspaceReconnect) {
    await resyncWorkspace(
      workspaceReconnect.queryClient,
      { slug: workspaceReconnect.slug },
      since,
    );
  }
  if (globalReconnect) {
    await resyncNotifications(globalReconnect.queryClient);
  }
}

interface UseProjectRealtimeOpts extends RealtimeContext {  issueUuid?: string;
  /**
   * Current user id (from useAuth() at the call site — the /me session
   * user, no new endpoint). When set, also subscribes to the user:{id}
   * channel: notification.created arrives ONLY there. The hub authorizes
   * a subscriber to its OWN user:{id} channel and rejects foreign ones
   * (ws_handler.go authorizeChannel). Omitted while auth is loading or
   * logged out — the effect re-runs once the id arrives.
   */
  userId?: string;
}

// useProjectRealtime subscribes to project (+workspace, +issue, +user)
// channels and wires invalidation. Resync-on-reconnect is the layout's
// job (single owner); pass nothing here — this hook only subscribes.
export function useProjectRealtime({
  slug,
  identifier,
  issueUuid,
  userId,
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
    if (userId) {
      // User channel: notification.created arrives ONLY here. The hub
      // authorizes only the subscriber's OWN user:{id} channel.
      unsubs.push(
        client.subscribe(userChannel(userId), (evt) =>
          handleRealtimeEvent(ref.current.queryClient, ctx, evt),
        ),
      );
    }
    return () => {
      unsubs.forEach((u) => u());
    };
    // Re-subscribe only when the identity changes (userId joins the
    // deps so the user-channel subscription appears once /me resolves).
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [slug, identifier, issueUuid, userId]);
}

interface UseWorkspaceRealtimeOpts {
  slug: string;
  /**
   * Current user id (from useAuth() at the call site — same plumbing as
   * useProjectRealtime). When set, also subscribes to the user:{id}
   * channel. Omitted while auth is loading or logged out.
   */
  userId?: string;
}

// useWorkspaceRealtime subscribes to the workspace + user channels and
// wires invalidation. Resync-on-reconnect is the layout's job (single
// owner); pass nothing here — this hook only subscribes.
//
// The handler ctx carries identifier "" (there is no project in a
// workspace scope). Project-scoped event cases then invalidate
// ["<key>", slug, "", ...] prefixes, which match no real query — a
// no-op. That is safe by construction: no project queries mount on
// workspace pages, and on project pages ProjectScope's own
// useProjectRealtime (real identifier) handles the same events.
// Workspace-scoped cases (initiative/objective/notification) never read
// the identifier, so they invalidate exactly the right keys.
export function useWorkspaceRealtime({
  slug,
  userId,
}: UseWorkspaceRealtimeOpts): void {
  const queryClient = useQueryClient();
  const ref = useRef({ queryClient, slug });
  ref.current = { queryClient, slug };

  useEffect(() => {
    const client = getRealtimeClient();
    client.connect();
    const ctx = { slug, identifier: "" };
    const unsubs = [
      client.subscribe(workspaceChannel(slug), (evt) =>
        handleRealtimeEvent(ref.current.queryClient, ctx, evt),
      ),
    ];
    if (userId) {
      // User channel: notification.created arrives ONLY here. The hub
      // authorizes only the subscriber's OWN user:{id} channel.
      unsubs.push(
        client.subscribe(userChannel(userId), (evt) =>
          handleRealtimeEvent(ref.current.queryClient, ctx, evt),
        ),
      );
    }
    return () => {
      unsubs.forEach((u) => u());
    };
    // Re-subscribe only when the identity changes (userId joins the
    // deps so the user-channel subscription appears once /me resolves).
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [slug, userId]);
}

interface UseGlobalRealtimeOpts {
  /**
   * Current user id (from useAuth() at the call site — same plumbing as
   * useWorkspaceRealtime). When set, subscribes to the user:{id}
   * channel. Omitted while auth is loading or logged out.
   */
  userId?: string;
}

// useGlobalRealtime (C44T0) subscribes to the user channel ONLY and
// wires invalidation through the SAME handleRealtimeEvent case as the
// workspace/project hooks — no second notification.created handler.
// Mounted by GlobalScope above every authenticated route, so routes
// outside WorkspaceScope (/, /notifications, /profile,
// /settings/tokens, /w, /onboarding) get live notification.created
// invalidation too. On workspace/project routes the user channel ends
// up triple-subscribed (global + workspace + project) —
// refcounted-benign (one wire subscription; invalidation idempotent),
// the already-accepted overlap from C43.
//
// The handler ctx carries slug/identifier "" (there is no workspace
// or project in a global scope). That is safe by construction:
// notification.created — the ONLY event that arrives on the user
// channel — never reads slug/identifier, so it invalidates exactly
// ["notifications"]. Any other event on this channel would hit a
// project-scoped case with empty identifiers, invalidating
// ["<key>", "", "", ...] prefixes that match no real query — a no-op,
// same as the workspace hook's identifier-"" no-op.
export function useGlobalRealtime({ userId }: UseGlobalRealtimeOpts): void {
  const queryClient = useQueryClient();
  const ref = useRef({ queryClient });
  ref.current = { queryClient };

  useEffect(() => {
    if (!userId) return;
    const client = getRealtimeClient();
    client.connect();
    const ctx = { slug: "", identifier: "" };
    const unsub = client.subscribe(userChannel(userId), (evt) =>
      handleRealtimeEvent(ref.current.queryClient, ctx, evt),
    );
    return unsub;
    // Re-subscribe only when the identity changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [userId]);
}
