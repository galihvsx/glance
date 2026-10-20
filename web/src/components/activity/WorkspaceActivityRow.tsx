// Shared workspace activity-feed row renderer (C80T1). Extracted from
// RecentActivitySection's row so the home feed and the per-member
// activity panel in WorkspaceSettings render rows with one renderer —
// do not duplicate this. Rows read "actor → change → issue link" with
// the owning project badge and relative time; entry.slug is the
// workspace slug the row belongs to (used to build the issue link).
import { Link } from "react-router-dom";
import { api } from "../../lib/api";
import { describeChange, type ActivityEntry } from "../../lib/activity";
import { relativeTime } from "../../lib/relativeTime";
import type { IssueState } from "../../lib/types";
import { Avatar, AvatarFallback } from "../ui/avatar";

/** A workspace-feed ActivityEntry tagged with its workspace slug. */
export interface WorkspaceActivityRowEntry extends ActivityEntry {
  slug: string;
}

function actorInitial(name: string): string {
  return (name.trim()[0] ?? "?").toUpperCase();
}

function ChangeText({
  entry,
  stateById,
}: {
  entry: WorkspaceActivityRowEntry;
  stateById: Map<string, string>;
}) {
  const d = describeChange(entry, stateById);
  switch (d.kind) {
    case "created":
      return <>created the issue</>;
    case "deleted":
      return <>deleted the issue</>;
    case "edited":
      return <>edited the description</>;
    case "set":
      return (
        <>
          set <span className="font-medium">{d.label}</span> to{" "}
          <code className="rounded bg-raised px-1 py-0.5 font-mono text-[11px]">
            {d.newText}
          </code>
        </>
      );
    case "cleared":
      return (
        <>
          cleared <span className="font-medium">{d.label}</span>
        </>
      );
    default:
      return (
        <>
          changed <span className="font-medium">{d.label}</span> from{" "}
          <code className="rounded bg-raised px-1 py-0.5 font-mono text-[11px]">
            {d.oldText}
          </code>{" "}
          to{" "}
          <code className="rounded bg-raised px-1 py-0.5 font-mono text-[11px]">
            {d.newText}
          </code>
        </>
      );
  }
}

export function WorkspaceActivityRow({
  entry,
  stateById,
}: {
  entry: WorkspaceActivityRowEntry;
  stateById: Map<string, string>;
}) {
  return (
    <div className="flex items-center gap-[10px] px-3 py-2">
      <Avatar className="h-6 w-6 shrink-0">
        <AvatarFallback className="text-[10px] font-semibold">
          {actorInitial(entry.actor)}
        </AvatarFallback>
      </Avatar>
      <div className="min-w-0 flex-1">
        <p className="truncate text-[12px] leading-snug text-ink">
          <span className="font-medium">{entry.actor}</span>{" "}
          <span className="text-ink-secondary">
            <ChangeText entry={entry} stateById={stateById} />
          </span>
        </p>
        <div className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-0.5">
          <Link
            to={`/w/${entry.slug}/p/${entry.project_identifier}/i/${entry.issue_uuid}`}
            className="font-mono text-[10px] text-ink-muted hover:text-ink hover:underline"
          >
            {entry.issue_identifier}
          </Link>
          {entry.project_name && (
            <span className="rounded border border-border px-1.5 py-0.5 text-[10px] leading-none text-ink-secondary">
              {entry.project_name}
            </span>
          )}
          <span className="text-[10px] text-ink-muted">
            {relativeTime(entry.at)}
          </span>
        </div>
      </div>
    </div>
  );
}

/** Workspace activity page fetcher: one page of the newest-first feed,
 *  optionally filtered to a member's rows (?actor=, C80T0). Returns the
 *  rows plus the opaque next cursor (null when exhausted). */
export interface WorkspaceActivityPage {
  activity: ActivityEntry[];
  next_cursor: string | null;
}

export async function fetchWorkspaceActivityPage(
  slug: string,
  params: { actor?: string; limit: number; before?: string },
): Promise<WorkspaceActivityPage> {
  const p = new URLSearchParams();
  p.set("limit", String(params.limit));
  if (params.actor) p.set("actor", params.actor);
  if (params.before) p.set("before", params.before);
  const d = await api.get<WorkspaceActivityPage>(
    `/api/v1/workspaces/${encodeURIComponent(slug)}/activity?${p}`,
  );
  return { activity: d.activity, next_cursor: d.next_cursor ?? null };
}

/** Resolve state names for the projects in a set of feed rows (the same
 *  per-project states fan-out RecentActivitySection does). Rows for
 *  projects whose states fail to load still render — state_id cells fall
 *  back to the truncated id via formatValue. */
export async function fetchStateNamesForProjects(
  slug: string,
  projectIdentifiers: string[],
): Promise<Map<string, Map<string, string>>> {
  const out = new Map<string, Map<string, string>>();
  await Promise.all(
    projectIdentifiers.map(async (identifier) => {
      try {
        const d = await api.get<{ states: IssueState[] }>(
          `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/states`,
        );
        const m = new Map<string, string>();
        for (const s of d.states) m.set(s.id, s.name);
        out.set(identifier, m);
      } catch {
        // Leave the project unresolved; rows still render.
      }
    }),
  );
  return out;
}
