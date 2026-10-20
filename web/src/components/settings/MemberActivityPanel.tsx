// Per-member activity panel for the WorkspaceSettings Members section
// (C80T1). An expanded member row shows their workspace activity via
// GET /api/v1/workspaces/{slug}/activity?actor=<member id> — the C80T0
// actor filter — with cursor pagination ("Load more", following the
// feed's newest-first / before= / next_cursor contract). Rows render
// with the shared WorkspaceActivityRow renderer; state names resolve
// per project exactly like the home feed does.
//
// The affordance is only offered to viewers with role >= 15 (the feed
// 403s guests and viewers); that gating lives in MemberListItem — this
// panel itself treats a 403 as a graceful error state.
import { useEffect, useRef, useState } from "react";
import { ApiError } from "../../lib/api";
import type { ActivityEntry } from "../../lib/activity";
import type { WorkspaceMember } from "../../lib/types";
import {
  WorkspaceActivityRow,
  fetchStateNamesForProjects,
  fetchWorkspaceActivityPage,
  type WorkspaceActivityRowEntry,
} from "../activity/WorkspaceActivityRow";
import { Button } from "../ui/button";
import { Skeleton } from "../ui/skeleton";

const PAGE_SIZE = 20;

export default function MemberActivityPanel({
  slug,
  member,
}: {
  slug: string;
  member: WorkspaceMember;
}) {
  const [rows, setRows] = useState<WorkspaceActivityRowEntry[]>([]);
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [stateByProject, setStateByProject] = useState<
    Map<string, Map<string, string>>
  >(new Map());
  const resolvedProjectsRef = useRef<Set<string>>(new Set());

  async function loadPage(before: string | null): Promise<void> {
    const first = before === null;
    if (first) {
      setLoading(true);
    } else {
      setLoadingMore(true);
    }
    setError(null);
    try {
      const page = await fetchWorkspaceActivityPage(slug, {
        actor: member.id,
        limit: PAGE_SIZE,
        before: before ?? undefined,
      });
      const tagged = page.activity.map((e: ActivityEntry) => ({
        ...e,
        slug,
      }));
      setRows((prev) => (first ? tagged : [...prev, ...tagged]));
      setNextCursor(page.next_cursor);
    } catch (e) {
      // 403 means the viewer's role lost feed access mid-session (the
      // affordance itself is hidden below role 15); say so plainly.
      setError(
        e instanceof ApiError && e.status === 403
          ? "Activity history isn't available at your role."
          : e instanceof Error
            ? e.message
            : "Failed to load activity",
      );
    } finally {
      setLoading(false);
      setLoadingMore(false);
    }
  }

  useEffect(() => {
    void loadPage(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [slug, member.id]);

  // Resolve state names for the projects in the loaded rows, same as the
  // home feed. Failed projects stay unresolved; rows still render.
  useEffect(() => {
    const ids = [
      ...new Set(
        rows
          .map((r) => r.project_identifier)
          .filter((id): id is string => !!id),
      ),
    ];
    const missing = ids.filter((id) => !resolvedProjectsRef.current.has(id));
    if (missing.length === 0) return;
    missing.forEach((id) => resolvedProjectsRef.current.add(id));
    let cancelled = false;
    void fetchStateNamesForProjects(slug, missing).then((maps) => {
      if (cancelled) return;
      setStateByProject((prev) => new Map([...prev, ...maps]));
    });
    return () => {
      cancelled = true;
    };
  }, [slug, rows]);

  return (
    <div
      className="mt-2 overflow-hidden rounded-md border border-border-hairline bg-surface"
      aria-label={`Activity for ${member.name ?? member.email}`}
    >
      {loading ? (
        <div className="space-y-1 px-3 py-2">
          <Skeleton className="h-10 w-full" />
          <Skeleton className="h-10 w-full" />
        </div>
      ) : error ? (
        <p className="px-3 py-4 text-center text-xs text-destructive">
          {error}
        </p>
      ) : rows.length === 0 ? (
        <p className="px-3 py-4 text-center text-xs text-muted-foreground">
          No activity yet.
        </p>
      ) : (
        <>
          <ul className="divide-y divide-border-hairline">
            {rows.map((e, i) => (
              <li key={`${e.at}-${e.issue_uuid}-${e.field}-${i}`}>
                <WorkspaceActivityRow
                  entry={e}
                  stateById={
                    stateByProject.get(e.project_identifier ?? "") ?? new Map()
                  }
                />
              </li>
            ))}
          </ul>
          {nextCursor !== null && (
            <div className="border-t border-border-hairline p-1.5 text-center">
              <Button
                variant="ghost"
                size="sm"
                className="h-7 text-xs"
                disabled={loadingMore}
                onClick={() => void loadPage(nextCursor)}
              >
                {loadingMore ? "Loading…" : "Load more"}
              </Button>
            </div>
          )}
        </>
      )}
    </div>
  );
}
