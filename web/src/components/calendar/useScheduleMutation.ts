import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../../lib/api";
import { issueDay } from "../../lib/calendar";
import type { ScheduleDates } from "../../lib/calendar-dnd";
import type { Issue } from "../../lib/types";
import { toast } from "../ui/toast";

/** Visible range as day keys ("YYYY-MM-DD"), matching the calendar queries. */
export interface ScheduleRange {
  start: string;
  end: string;
}

function issuePath(slug: string, identifier: string, uuid: string): string {
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/issues/${encodeURIComponent(uuid)}`;
}

/**
 * PATCH an issue's start/target dates with optimistic updates of the
 * calendar's `due` / `start` / `undated` caches. On failure the caches roll
 * back and an error toast carries the server message; on success the
 * caches are invalidated so the server stays the source of truth.
 */
export function useScheduleMutation(
  slug: string,
  identifier: string,
  range: ScheduleRange,
) {
  const queryClient = useQueryClient();
  const dueKey = ["calendar", slug, identifier, "due", range.start];
  const startKey = ["calendar", slug, identifier, "start", range.start];
  const undatedKey = ["calendar", slug, identifier, "undated"];
  const keys = [dueKey, startKey, undatedKey];

  return useMutation({
    mutationFn: ({ uuid, dates }: { uuid: string; dates: ScheduleDates }) =>
      api.patch<Issue>(issuePath(slug, identifier, uuid), dates),
    onMutate: async ({ uuid, dates }) => {
      await Promise.all(
        keys.map((key) => queryClient.cancelQueries({ queryKey: key })),
      );
      const prev = keys.map(
        (key) => [key, queryClient.getQueryData<Issue[]>(key)] as const,
      );
      const applyDates = (issue: Issue): Issue => ({
        ...issue,
        // The `in` check distinguishes "field omitted" from "explicitly
        // cleared": a null date (C26T2 unschedule) becomes a null column.
        ...("start_date" in dates
          ? {
              start_date: dates.start_date
                ? `${dates.start_date}T00:00:00Z`
                : null,
            }
          : {}),
        ...("target_date" in dates
          ? {
              target_date: dates.target_date
                ? `${dates.target_date}T00:00:00Z`
                : null,
            }
          : {}),
      });
      // A moved chip leaves caches it no longer belongs to: a due/start
      // cache only holds issues placed inside the visible range, and the
      // undated cache only holds dateless issues.
      const keep = (key: readonly unknown[], issue: Issue): boolean => {
        if (issue.id !== uuid) return true;
        const day = issueDay(applyDates(issue));
        if (key === undatedKey) return day === null;
        return day !== null && day >= range.start && day < range.end;
      };
      for (const key of keys) {
        queryClient.setQueryData<Issue[]>(key, (old) =>
          old
            ?.map((i) => (i.id === uuid ? applyDates(i) : i))
            .filter((i) => keep(key, i)),
        );
      }
      // C26T2: an unscheduled chip optimistically joins the undated cache
      // so it lands in the strip immediately — it was never fetched there,
      // so the map/filter loop above can't move it. `prev` still holds the
      // snapshots for rollback.
      const source = prev
        .flatMap(([, data]) => data ?? [])
        .find((i) => i.id === uuid);
      if (source && issueDay(applyDates(source)) === null) {
        const applied = applyDates(source);
        queryClient.setQueryData<Issue[]>(undatedKey, (old) => {
          if (!old) return [applied];
          return old.some((i) => i.id === uuid)
            ? old.map((i) => (i.id === uuid ? applied : i))
            : [...old, applied];
        });
      }
      return { prev };
    },
    onError: (err, _vars, context) => {
      for (const [key, data] of context?.prev ?? []) {
        queryClient.setQueryData(key, data);
      }
      toast.add({
        title: "Couldn't reschedule issue",
        description:
          err instanceof ApiError
            ? err.message
            : "The date change wasn't saved.",
        type: "error",
      });
    },
    onSettled: () => {
      for (const key of keys) {
        void queryClient.invalidateQueries({ queryKey: key });
      }
      // C109T1: the PQL window sets use "due-pql" / "start-pql" /
      // "undated-pql" as element 4, so they are NOT a prefix match for the
      // base keys above. Invalidating the shared ["calendar", slug,
      // identifier] prefix covers both the base keys and the *-pql keys,
      // so a drag while in PQL mode refetches the filtered sets instead
      // of leaving the dragged chip stale.
      void queryClient.invalidateQueries({
        queryKey: ["calendar", slug, identifier],
      });
    },
  });
}
