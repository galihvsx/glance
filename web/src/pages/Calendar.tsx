import { useMemo, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { CalendarPlus, ChevronLeft, ChevronRight } from "lucide-react";
import ProjectNav from "../components/project/ProjectNav";
import PeekDrawer from "../components/issue/PeekDrawer";
import { usePeekParam } from "../components/issue/usePeek";
import { api, ApiError } from "../lib/api";
import {
  createFeedToken,
  fetchFeedTokenStatus,
  projectFeedURL,
} from "../lib/calendarFeed";
import type { Issue, IssueListResult } from "../lib/types";
import {
  bucketIssuesByDay,
  dayKey,
  firstOfMonth,
  monthGrid,
  shiftMonth,
} from "../lib/calendar";
import { cn } from "../lib/utils";

const WEEKDAYS = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];

/** Priority dot colors (subtle, readable on dark-default and light). */
const PRIORITY_DOT = [
  "bg-muted-foreground/40", // 0 None
  "bg-blue-500", // 1 Low
  "bg-yellow-500", // 2 Medium
  "bg-orange-500", // 3 High
  "bg-red-500", // 4 Urgent
];

/** Fetch every page of a bounded list query (per_page=100, cursor loop). */
async function fetchAllIssues(path: string): Promise<Issue[]> {
  const out: Issue[] = [];
  let cursor: string | undefined;
  do {
    const sep = path.includes("?") ? "&" : "?";
    const page = new URLSearchParams({ per_page: "100" });
    if (cursor) page.set("cursor", cursor);
    const res = await api.get<IssueListResult>(`${path}${sep}${page}`);
    out.push(...res.results);
    cursor = res.next_cursor;
  } while (cursor);
  return out;
}

function IssueChip({ issue, onOpen }: { issue: Issue; onOpen: () => void }) {
  return (
    <button
      type="button"
      onClick={onOpen}
      title={`${issue.display_id}: ${issue.name}`}
      className="flex w-full items-center gap-1.5 rounded px-1.5 py-0.5 text-left text-xs hover:bg-accent"
    >
      <span
        className={cn(
          "h-1.5 w-1.5 shrink-0 rounded-full",
          PRIORITY_DOT[issue.priority] ?? PRIORITY_DOT[0],
        )}
      />
      <span className="shrink-0 font-mono text-muted-foreground">
        {issue.display_id}
      </span>
      <span className="truncate">{issue.name}</span>
    </button>
  );
}

export default function Calendar() {
  const { slug = "", identifier = "" } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;
  const { peekUuid, openPeek, closePeek } = usePeekParam();

  const now = new Date();
  const [year, setYear] = useState(now.getFullYear());
  const [month, setMonth] = useState(now.getMonth());

  const go = (delta: number) => {
    const next = shiftMonth(year, month, delta);
    setYear(next.year);
    setMonth(next.month);
  };
  const goToday = () => {
    const t = new Date();
    setYear(t.getFullYear());
    setMonth(t.getMonth());
  };

  const { start, end } = useMemo(() => {
    const n = shiftMonth(year, month, 1);
    return { start: firstOfMonth(year, month), end: firstOfMonth(n.year, n.month) };
  }, [year, month]);
  const rfc = (day: string) => `${day}T00:00:00Z`;

  // Three bounded queries per month view (no per-day requests):
  // due-dated, start-dated, and undated (the "unscheduled" strip).
  const dueQuery = useQuery({
    queryKey: ["calendar", slug, identifier, "due", start],
    queryFn: () =>
      fetchAllIssues(`${base}/issues?due_after=${rfc(start)}&due_before=${rfc(end)}`),
  });
  const startQuery = useQuery({
    queryKey: ["calendar", slug, identifier, "start", start],
    queryFn: () =>
      fetchAllIssues(
        `${base}/issues?start_after=${rfc(start)}&start_before=${rfc(end)}`,
      ),
  });
  const undatedQuery = useQuery({
    queryKey: ["calendar", slug, identifier, "undated"],
    queryFn: () => fetchAllIssues(`${base}/issues?undated=1`),
  });

  const cells = useMemo(() => monthGrid(year, month), [year, month]);
  const byDay = useMemo(
    () => bucketIssuesByDay(dueQuery.data ?? [], startQuery.data ?? []),
    [dueQuery.data, startQuery.data],
  );
  const undated = useMemo(() => undatedQuery.data ?? [], [undatedQuery.data]);

  const loading = dueQuery.isLoading || startQuery.isLoading || undatedQuery.isLoading;
  const error = dueQuery.error ?? startQuery.error ?? undatedQuery.error;

  const todayKey = dayKey(new Date());
  const monthLabel = new Date(year, month, 1).toLocaleDateString(undefined, {
    month: "long",
    year: "numeric",
  });

  // iCal subscription: the first click mints the user's feed token (if
  // none is active) and copies the subscription URL. A live token's
  // plaintext can never be re-shown, so when one is already active the
  // user regenerates from Profile → Calendar feed instead.
  const [copying, setCopying] = useState(false);
  const [feedMsg, setFeedMsg] = useState<string | null>(null);

  async function copyICalURL() {
    setCopying(true);
    setFeedMsg(null);
    try {
      const status = await fetchFeedTokenStatus();
      if (status.active) {
        setFeedMsg(
          "A feed token is already active — regenerate it from Profile → Calendar feed to get a new subscription URL.",
        );
        return;
      }
      const created = await createFeedToken();
      const url = projectFeedURL(
        window.location.origin,
        slug,
        identifier,
        created.token,
      );
      await navigator.clipboard.writeText(url);
      setFeedMsg(
        "iCal URL copied to clipboard. The token is shown once — save the URL somewhere safe.",
      );
    } catch (e) {
      setFeedMsg(
        e instanceof ApiError ? e.message : "Failed to copy the iCal URL",
      );
    } finally {
      setCopying(false);
    }
  }

  return (
    <div className="flex h-full flex-col gap-4 p-4">
      <ProjectNav />
      <div className="flex items-center gap-2">
        <button
          type="button"
          onClick={() => go(-1)}
          className="rounded-md border p-1.5 hover:bg-accent"
          aria-label="Previous month"
        >
          <ChevronLeft className="h-4 w-4" />
        </button>
        <button
          type="button"
          onClick={goToday}
          className="rounded-md border px-2.5 py-1 text-xs font-medium hover:bg-accent"
        >
          Today
        </button>
        <button
          type="button"
          onClick={() => go(1)}
          className="rounded-md border p-1.5 hover:bg-accent"
          aria-label="Next month"
        >
          <ChevronRight className="h-4 w-4" />
        </button>
        <h2 className="ml-2 text-base font-semibold">{monthLabel}</h2>
        <button
          type="button"
          onClick={copyICalURL}
          disabled={copying}
          className="ml-auto flex items-center gap-1.5 rounded-md border px-2.5 py-1 text-xs font-medium hover:bg-accent disabled:opacity-50"
        >
          <CalendarPlus className="h-3.5 w-3.5" />
          {copying ? "Copying…" : "Copy iCal URL"}
        </button>
        <span className="text-xs text-muted-foreground">
          Placed by due date, start date when there is no due date
        </span>
      </div>

      {feedMsg && (
        <p className="text-xs text-muted-foreground">
          {feedMsg.includes("Profile") ? (
            <>
              A feed token is already active — regenerate it from{" "}
              <Link to="/profile" className="underline">
                Profile → Calendar feed
              </Link>{" "}
              to get a new subscription URL.
            </>
          ) : (
            feedMsg
          )}
        </p>
      )}

      {loading ? (
        <p className="text-sm text-muted-foreground">Loading calendar…</p>
      ) : error ? (
        <p className="text-sm text-destructive">
          {error instanceof ApiError ? error.message : "Failed to load calendar"}
        </p>
      ) : (
        <>
          <div className="grid grid-cols-7 overflow-hidden rounded-md border">
            {WEEKDAYS.map((d) => (
              <div
                key={d}
                className="border-b bg-muted/50 px-2 py-1.5 text-xs font-medium text-muted-foreground [&:not(:last-child)]:border-r"
              >
                {d}
              </div>
            ))}
            {cells.map((date) => {
              const key = dayKey(date);
              const issues = byDay.get(key) ?? [];
              const isToday = key === todayKey;
              const isOtherMonth = date.getMonth() !== month;
              return (
                <div
                  key={key}
                  className={cn(
                    "min-h-24 border-b p-1 [&:not(:nth-child(7n))]:border-r",
                    isOtherMonth && "opacity-40",
                  )}
                >
                  <div
                    className={cn(
                      "mb-0.5 inline-flex h-6 w-6 items-center justify-center rounded-full text-xs",
                      isToday
                        ? "bg-primary font-semibold text-primary-foreground"
                        : "text-muted-foreground",
                    )}
                  >
                    {date.getDate()}
                  </div>
                  <div className="max-h-20 space-y-0.5 overflow-y-auto">
                    {issues.map((issue) => (
                      <IssueChip
                        key={issue.id}
                        issue={issue}
                        onOpen={() => openPeek(issue.id)}
                      />
                    ))}
                  </div>
                </div>
              );
            })}
          </div>

          {byDay.size === 0 && undated.length === 0 && (
            <p className="text-sm text-muted-foreground">
              No issues scheduled for {monthLabel}. Issues appear here once they
              get a due or start date.
            </p>
          )}

          {undated.length > 0 && (
            <section>
              <h3 className="mb-2 text-sm font-medium text-muted-foreground">
                Unscheduled ({undated.length})
              </h3>
              <div className="flex flex-wrap gap-1 rounded-md border p-2">
                {undated.map((issue) => (
                  <span key={issue.id} className="max-w-64">
                    <IssueChip issue={issue} onOpen={() => openPeek(issue.id)} />
                  </span>
                ))}
              </div>
            </section>
          )}
        </>
      )}

      {peekUuid && (
        <PeekDrawer
          slug={slug}
          identifier={identifier}
          uuid={peekUuid}
          onClose={closePeek}
        />
      )}
    </div>
  );
}
