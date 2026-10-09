// Notification display context (C5T6).
//
// Notification payloads carry project_id / actor_id, but the SPA routes on
// workspace slug + project identifier and renders actor names — neither of
// which the backend includes in the payload (verified in notify_handler.go;
// this task may not change the backend). This module resolves those IDs
// through the existing list APIs, following the Home page's fan-out pattern
// (workspaces → per-workspace projects/members).
//
// Everything is optional: a notification whose project or actor can no
// longer be resolved still renders its title text, just without a link or
// resolved name.

import { api } from "./api";
import type { Project, Workspace, WorkspaceMember } from "./types";

const enc = encodeURIComponent;

export interface ProjectRoute {
  slug: string;
  identifier: string;
  name: string;
}

export interface ActorInfo {
  name: string | null;
  email: string;
}

/** Display name: name when set, else the email local part, else "Someone". */
export function actorDisplayName(actor: ActorInfo): string {
  if (actor.name && actor.name.trim()) return actor.name.trim();
  const local = actor.email.split("@")[0];
  return local || "Someone";
}

/** First letter of the display name, uppercased, for avatar initials. */
export function actorInitial(actor: ActorInfo): string {
  const name = actorDisplayName(actor);
  return name.charAt(0).toUpperCase() || "?";
}

export interface NotificationContext {
  /** project_id → route parts for issue deep-links. */
  projects: Map<string, ProjectRoute>;
  /** actor_id → resolved identity. */
  actors: Map<string, ActorInfo>;
}

export async function fetchNotificationContext(): Promise<NotificationContext> {
  const { workspaces } = await api.get<{ workspaces: Workspace[] }>(
    "/api/v1/workspaces",
  );

  const perWorkspace = await Promise.all(
    workspaces.map(async (ws) => {
      const [projectsRes, membersRes] = await Promise.all([
        api.get<{ projects: Project[] }>(
          `/api/v1/workspaces/${enc(ws.slug)}/projects`,
        ),
        api.get<{ members: WorkspaceMember[] }>(
          `/api/v1/workspaces/${enc(ws.slug)}/members`,
        ),
      ]);
      return { ws, projects: projectsRes.projects, members: membersRes.members };
    }),
  );

  const projects = new Map<string, ProjectRoute>();
  const actors = new Map<string, ActorInfo>();
  for (const { ws, projects: ps, members } of perWorkspace) {
    for (const p of ps) {
      // First workspace wins for a project id (ids are globally unique
      // anyway); this is only a routing hint.
      if (!projects.has(p.id)) {
        projects.set(p.id, {
          slug: ws.slug,
          identifier: p.identifier,
          name: p.name,
        });
      }
    }
    for (const m of members) {
      if (!actors.has(m.id)) {
        actors.set(m.id, { name: m.name ?? null, email: m.email });
      }
    }
  }
  return { projects, actors };
}

/** Deep-link for a notification's issue, or null when unresolvable. */
export function issueLink(
  ctx: NotificationContext,
  payload: { project_id?: string; issue_id?: string },
): string | null {
  if (!payload.project_id || !payload.issue_id) return null;
  const route = ctx.projects.get(payload.project_id);
  if (!route) return null;
  return `/w/${enc(route.slug)}/p/${enc(route.identifier)}/i/${enc(payload.issue_id)}`;
}
