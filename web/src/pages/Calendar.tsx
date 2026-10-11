import { useMemo, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { CalendarPlus, ChevronLeft, ChevronRight } from "lucide-react";
import PageHeader from "../components/shell/PageHeader";
import PeekDrawer from "../components/issue/PeekDrawer";
import { usePeekParam } from "../components/issue/usePeek";
import { api, ApiError } from "../lib/api";
import { stateDotClass } from "../lib/stateDot";
import {
  createFeedToken,
  fetchFeedTokenStatus,
  projectFeedURL,
} from "../lib/calendarFeed";
import type { Issue, IssueListResult, IssueState } from "../lib/types";
import {
  bucketIssuesByDay,
  dayKey,
  firstOfMonth,
  monthGrid,
  shiftMonth,
} from "../lib/calendar";
import { cn } from "../lib/utils";

const WEEKDAYS = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];

/** 28px toolbar button in the C19 token ramp (matches PageHeader controls). */
const calControlClass =
  "inline-flex h-7 items-center gap-1.5 rounded-md border border-border bg-surface px-2 text-[11px] font-medium text-ink-secondary hover:bg-raised hover:text-ink disabled:opacity-50";

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

function IssueChip({
  issue,
  state,
  onOpen,
}: {
  issue: Issue;
  state?: IssueState;
  onOpen: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onOpen}
      title={`${issue.display_id}: ${issue.name}`}
      className="flex w-full items-center gap-1.5 rounded-[4px] px-1.5 py-[3px] text-left text-[11px] text-ink-secondary hover:bg-wash-hover hover:text-ink"
    >
      <span
        className={cn("state-dot", stateDotClass(state?.group))}
        aria-hidden
      />
      <span className="shrink-0 font-mono text-[10px] text-ink-muted">
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

  // Workflow states drive the state-dot colors on the event chips
  // (C19 state-dot system — same vocabulary as the board cards).
  const statesQuery = useQuery({
    queryKey: ["states", slug, identifier],
    queryFn: () =>
      api.get<{ states: IssueState[] }>(`${base}/states`).then((d) => d.states),
  });
  const statesById = useMemo(() => {
    const m = new Map<string, IssueState>();
    for (const s of statesQuery.data ?? []) m.set(s.id, s);
    return m;
  }, [statesQuery.data]);

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
    <>
      {/* C19T4: shared page header replaces the per-page ProjectNav. */}
      <PageHeader viewName="Calendar" showViews />
      <div className="flex h-full flex-col gap-3 p-4">
        <div className="flex items-center gap-1.5">
          <button
            type="button"
            onClick={() => go(-1)}
            className={cn(calControlClass, "px-1.5")}
            aria-label="Previous month"
          >
            <ChevronLeft className="h-3.5 w-3.5" />
          </button>
          <button type="button" onClick={goToday} className={calControlClass}>
            Today
          </button>
          <button
            type="button"
            onClick={() => go(1)}
            className={cn(calControlClass, "px-1.5")}
            aria-label="Next month"
          >
            <ChevronRight className="h-3.5 w-3.5" />
          </button>
          <h2 className="ml-2 text-[13px] font-semibold text-ink">
            {monthLabel}
          </h2>
          <button
            type="button"
            onClick={copyICalURL}
            disabled={copying}
            className={cn(calControlClass, "ml-auto")}
          >
            <CalendarPlus className="h-3.5 w-3.5" />
            {copying ? "Copying…" : "Copy iCal URL"}
          </button>
          <span className="text-[11px] text-ink-muted">
            Placed by due date, start date when there is no due date
          </span>
        </div>

        {feedMsg && (
          <p className="text-[11px] text-ink-muted">
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
          <p className="text-[13px] text-ink-muted">Loading calendar…</p>
        ) : error ? (
          <p className="text-[13px] text-destructive">
            {error instanceof ApiError ? error.message : "Failed to load calendar"}
          </p>
        ) : (
          <>
            <div className="grid grid-cols-7 overflow-hidden rounded-md border border-border">
              {WEEKDAYS.map((d) => (
                <div
                  key={d}
                  className="border-b border-border-hairline bg-raised px-2 py-1.5 text-[10px] font-semibold uppercase tracking-[0.06em] text-ink-muted [&:not(:nth-child(7n))]:border-r [&:not(:nth-child(7n))]:border-r-border-hairline"
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
                      "min-h-24 border-b border-border-hairline p-1 [&:not(:nth-child(7n))]:border-r [&:not(:nth-child(7n))]:border-r-border-hairline",
                      isOtherMonth && "opacity-40",
                    )}
                  >
                    <div
                      className={cn(
                        "mb-0.5 inline-flex h-6 w-6 items-center justify-center rounded-full text-[11px]",
                        isToday
                          ? "bg-accent-blue font-semibold text-white"
                          : "text-ink-muted",
                      )}
                    >
                      {date.getDate()}
                    </div>
                    <div className="max-h-20 space-y-0.5 overflow-y-auto">
                      {issues.map((issue) => (
                        <IssueChip
                          key={issue.id}
                          issue={issue}
                          state={statesById.get(issue.state_id)}
                          onOpen={() => openPeek(issue.id)}
                        />
                      ))}
                    </div>
                  </div>
                );
              })}
            </div>

            {byDay.size === 0 && undated.length === 0 && (
              <p className="text-[13px] text-ink-muted">
                No issues scheduled for {monthLabel}. Issues appear here once they
                get a due or start date.
              </p>
            )}

            {undated.length > 0 && (
              <section>
                <h3 className="mb-2 text-[12px] font-medium text-ink-secondary">
                  Unscheduled ({undated.length})
                </h3>
                <div className="flex flex-wrap gap-1 rounded-md border border-border bg-surface p-2">
                  {undated.map((issue) => (
                    <span key={issue.id} className="max-w-64">
                      <IssueChip
                        issue={issue}
                        state={statesById.get(issue.state_id)}
                        onOpen={() => openPeek(issue.id)}
                      />
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
    </>
  );
}
