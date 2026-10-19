import { Fragment, useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import type { DragEndEvent, DragStartEvent } from "@dnd-kit/core";
import { CalendarPlus, ChevronLeft, ChevronRight } from "lucide-react";
import PageHeader from "../components/shell/PageHeader";
import PeekDrawer from "../components/issue/PeekDrawer";
import { usePeekParam } from "../components/issue/usePeek";
import { groupIssuesByEpic } from "../components/issue/groupByEpic";
import { IssueChip } from "../components/calendar/IssueChip";
import { DraggableIssueChip } from "../components/calendar/DraggableIssueChip";
import { DroppableDayCell } from "../components/calendar/DroppableDayCell";
import { DroppableWeekColumn } from "../components/calendar/DroppableWeekColumn";
import { EpicLaneHeader } from "../components/calendar/EpicLaneHeader";
import { ScheduleDnd } from "../components/calendar/ScheduleDnd";
import { UnscheduledStrip } from "../components/calendar/UnscheduledStrip";
import {
  UNSCHEDULED_STRIP_ID,
} from "../components/calendar/DroppableUnscheduledStrip";
import { useScheduleMutation } from "../components/calendar/useScheduleMutation";
import { api, ApiError } from "../lib/api";
import {
  createFeedToken,
  fetchFeedTokenStatus,
  projectFeedURL,
} from "../lib/calendarFeed";
import { resolveScheduleDrop, resolveUnscheduleDrop } from "../lib/calendar-dnd";
import type { Issue, IssueListResult, IssueState, Workspace } from "../lib/types";
import {
  bucketIssuesByDay,
  dayKey,
  firstOfMonth,
  monthGrid,
  shiftMonth,
  shiftWeek,
  startOfWeek,
  weekGrid,
} from "../lib/calendar";
import { cn } from "../lib/utils";

const WEEKDAYS = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];

type CalendarView = "month" | "week";

function viewStorageKey(slug: string, identifier: string): string {
  return `glance:calendar-view:${slug}:${identifier}`;
}

function loadView(slug: string, identifier: string): CalendarView {
  try {
    const raw = localStorage.getItem(viewStorageKey(slug, identifier));
    if (raw === "week" || raw === "month") return raw;
  } catch {
    // Private mode / storage unavailable — month default still applies.
  }
  return "month";
}

// C72T2: calendar-local group-by setting — deliberately NOT the shared
// useDisplaySettings/DisplayPanel. Sharing the list's per-project display
// key would couple two different views (changing Group By on the issue
// list would unexpectedly restructure the calendar). A calendar-local key
// keeps the swimlane grouping an independent per-view preference, like
// the calendar's own view key above.
type CalendarGroupBy = "none" | "epic";

function groupByStorageKey(slug: string, identifier: string): string {
  return `glance:calendar-groupby:${slug}:${identifier}`;
}

function loadGroupBy(slug: string, identifier: string): CalendarGroupBy {
  try {
    const raw = localStorage.getItem(groupByStorageKey(slug, identifier));
    if (raw === "epic") return "epic";
  } catch {
    // Private mode / storage unavailable — "none" default still applies.
  }
  return "none";
}

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

export default function Calendar() {
  const { slug = "", identifier = "" } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const navigate = useNavigate();
  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;
  const { peekUuid, openPeek, closePeek } = usePeekParam();

  // Month/Week view preference, per-project localStorage (same pattern as
  // the spreadsheet custom-column prefs). Adjust-during-render keeps it in
  // sync when the route params change without a remount (project switcher).
  const viewKey = `${slug}\0${identifier}`;
  const [cachedView, setCachedView] = useState(() => ({
    key: viewKey,
    view: loadView(slug, identifier),
  }));
  if (cachedView.key !== viewKey) {
    setCachedView({ key: viewKey, view: loadView(slug, identifier) });
  }
  const view = cachedView.view;
  const setView = (next: CalendarView) => {
    try {
      localStorage.setItem(viewStorageKey(slug, identifier), next);
    } catch {
      // Storage full / private mode — still applies for the session.
    }
    setCachedView((prev) => ({ ...prev, view: next }));
  };

  // C72T2: Group: None | Epic, per-project localStorage, same
  // adjust-during-render pattern as the view setting above. A pure view
  // preference — enabled for all roles (canEdit still gates DnD only).
  const [cachedGroupBy, setCachedGroupBy] = useState(() => ({
    key: viewKey,
    groupBy: loadGroupBy(slug, identifier),
  }));
  if (cachedGroupBy.key !== viewKey) {
    setCachedGroupBy({ key: viewKey, groupBy: loadGroupBy(slug, identifier) });
  }
  const groupBy = cachedGroupBy.groupBy;
  const setGroupBy = (next: CalendarGroupBy) => {
    try {
      localStorage.setItem(groupByStorageKey(slug, identifier), next);
    } catch {
      // Storage full / private mode — still applies for the session.
    }
    setCachedGroupBy((prev) => ({ ...prev, groupBy: next }));
  };

  // Anchor date drives both views: the month view uses its year/month, the
  // week view uses the week containing it. Navigation preserves the anchor
  // so switching Month <-> Week keeps the user's place.
  const [anchor, setAnchor] = useState(() => new Date());

  const go = (delta: number) => {
    setAnchor((prev) => {
      if (view === "week") return shiftWeek(prev, delta);
      const next = shiftMonth(prev.getFullYear(), prev.getMonth(), delta);
      return new Date(next.year, next.month, 1);
    });
  };
  const goToday = () => setAnchor(new Date());

  // Bounded issue queries cover the visible range only (no per-day requests):
  // due-dated, start-dated, and undated (the "unscheduled" strip).
  const { start, end } = useMemo(() => {
    if (view === "week") {
      const monday = startOfWeek(anchor);
      return { start: dayKey(monday), end: dayKey(shiftWeek(monday, 1)) };
    }
    const n = shiftMonth(anchor.getFullYear(), anchor.getMonth(), 1);
    return {
      start: firstOfMonth(anchor.getFullYear(), anchor.getMonth()),
      end: firstOfMonth(n.year, n.month),
    };
  }, [view, anchor]);
  const rfc = (day: string) => `${day}T00:00:00Z`;
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
  // C26T0: same workspace role gate as the board — drag is disabled for
  // guests (role < 15); the server 403 stays the backstop.
  const workspaceQuery = useQuery({
    queryKey: ["workspace", slug],
    queryFn: () =>
      api.get<Workspace>(`/api/v1/workspaces/${encodeURIComponent(slug)}`),
  });
  const canEdit = (workspaceQuery.data?.role ?? 0) >= 15;

  // C26T3: day-cell quick-add. Opens the EXISTING create-issue dialog on
  // the issues page with the day prefilled as the target (due) date — the
  // same location.state mechanism the board's expand-quick-add uses
  // (Issues.tsx reads it on mount). The dialog's success flow invalidates
  // the calendar caches, so the new chip appears on the day when the user
  // comes back.
  const quickAdd = (day: string) => {
    navigate(`/w/${slug}/p/${identifier}`, {
      state: { newIssue: true, newIssueTargetDate: day },
    });
  };
  const statesById = useMemo(() => {
    const m = new Map<string, IssueState>();
    for (const s of statesQuery.data ?? []) m.set(s.id, s);
    return m;
  }, [statesQuery.data]);

  const cells = useMemo(
    () =>
      view === "week"
        ? weekGrid(anchor)
        : monthGrid(anchor.getFullYear(), anchor.getMonth()),
    [view, anchor],
  );
  const byDay = useMemo(
    () => bucketIssuesByDay(dueQuery.data ?? [], startQuery.data ?? []),
    [dueQuery.data, startQuery.data],
  );
  const undated = useMemo(() => undatedQuery.data ?? [], [undatedQuery.data]);

  // C72T2: epic swimlanes. Lanes are computed over every issue visible in
  // the grid's range (first-appearance order); the shared pure
  // groupIssuesByEpic carries the cycle-72 semantics — multi-appearance
  // (an issue under EVERY live epic), archived-epic exclusion, Ungrouped
  // last and hidden when empty. Empty epic lanes are hidden
  // unconditionally (no showEmptyGroups toggle on the calendar, per the
  // spec ruling). Progress counts derive from the loaded states — no
  // extra fetches (the C70/C71 derived-data discipline).
  const gridIssues = useMemo(() => [...byDay.values()].flat(), [byDay]);
  const lanes = useMemo(
    () =>
      groupBy === "epic"
        ? groupIssuesByEpic(
            gridIssues,
            (issue) => statesById.get(issue.state_id)?.group,
          )
        : [],
    [groupBy, gridIssues, statesById],
  );
  // Lane membership as id sets, so each day cell's bands filter the
  // day's (sequence-sorted) issues without recomputing epics per chip.
  const laneMembers = useMemo(() => {
    const m = new Map<string, Set<string>>();
    for (const lane of lanes) {
      m.set(
        lane.key,
        new Set(lane.issues.map((i) => i.id)),
      );
    }
    return m;
  }, [lanes]);
  // The day's issues in a lane's band, in the day's existing
  // sequence order. Bands are render-only — the whole day cell stays
  // the drop target.
  const bandIssues = (key: string, laneKey: string): Issue[] => {
    const ids = laneMembers.get(laneKey);
    if (!ids) return [];
    return (byDay.get(key) ?? []).filter((i) => ids.has(i.id));
  };
  // C72T2: the unscheduled strip gets the same lane clusters (live epics
  // + Ungrouped last); the strip stays ONE droppable — the grouping is
  // render-only.
  const stripGroups = useMemo(
    () =>
      groupBy === "epic"
        ? groupIssuesByEpic(
            undated,
            (issue) => statesById.get(issue.state_id)?.group,
          )
        : null,
    [groupBy, undated, statesById],
  );

  // C72T2: the swimlane gutter's lane stack — the same component in
  // every month week-row gutter and the single week-view gutter, in the
  // fixed lane order (live epics first-appearance, Ungrouped last).
  const laneStack = (
    <div className="divide-y divide-border-hairline">
      {lanes.map((lane) => (
        <div key={lane.key} data-testid="epic-lane" data-lane-key={lane.key}>
          <EpicLaneHeader
            title={lane.title}
            total={lane.issues.length}
            byGroup={lane.byGroup}
            isUngrouped={lane.isUngrouped}
          />
        </div>
      ))}
    </div>
  );

  // C72T2: one day's per-lane bands in the fixed lane order. Render-only
  // — the day cell/column stays the drop target. Multi-appearance chips
  // render plainly in every lane (no link glyph, spec ruling Q4) with
  // per-lane draggable ids so the dnd registry never collides on
  // duplicate issue ids.
  const laneBands = (key: string, wrapClass: string, bandClass: string) => (
    <div className={cn("divide-y divide-border-hairline", wrapClass)}>
      {lanes.map((lane) => (
        <div key={lane.key} className={cn("space-y-0.5 p-0.5", bandClass)}>
          {bandIssues(key, lane.key).map((issue) => (
            <DraggableIssueChip
              key={issue.id}
              dragId={`${issue.id}:${lane.key}`}
              issue={issue}
              state={statesById.get(issue.state_id)}
              onOpen={() => openPeek(issue.id)}
              disabled={!canEdit}
            />
          ))}
        </div>
      ))}
    </div>
  );

  // C72T2: the month grid's 42 cells chunked into week rows so each row
  // gets its own gutter cell (the mock's per-week-row lane stack).
  const weekRows = useMemo(() => {
    const rows: Date[][] = [];
    for (let i = 0; i < cells.length; i += 7) rows.push(cells.slice(i, i + 7));
    return rows;
  }, [cells]);

  const loading = dueQuery.isLoading || startQuery.isLoading || undatedQuery.isLoading;
  const error = dueQuery.error ?? startQuery.error ?? undatedQuery.error;

  // C26T0: month-grid drag-reschedule. The mutation optimistically updates
  // the calendar caches; a failed PATCH rolls back and toasts the server
  // message. No toast on success — the chip visibly moves.
  const scheduleMutation = useScheduleMutation(slug, identifier, { start, end });
  const [activeIssue, setActiveIssue] = useState<Issue | null>(null);
  const [announcement, setAnnouncement] = useState("");

  function handleDragStart(e: DragStartEvent) {
    const issue = e.active.data.current?.issue as Issue | undefined;
    setActiveIssue(issue ?? null);
  }

  function handleDragEnd(e: DragEndEvent) {
    setActiveIssue(null);
    if (!canEdit) return;
    const issue = e.active.data.current?.issue as Issue | undefined;
    if (issue == null) return;
    const overId = e.over != null ? String(e.over.id) : null;
    // C26T2: strip drops unschedule (PATCH clears the placing date);
    // strip chips carry `fromStrip` so day drops schedule them.
    const resolved =
      overId === UNSCHEDULED_STRIP_ID
        ? resolveUnscheduleDrop(issue)
        : resolveScheduleDrop(
            issue,
            overId,
            e.active.data.current?.fromStrip === true,
          );
    if (resolved == null) return; // no-op drop → no PATCH
    scheduleMutation.mutate(
      { uuid: resolved.uuid, dates: resolved.dates },
      { onSuccess: () => setAnnouncement(resolved.announcement) },
    );
  }

  const todayKey = dayKey(new Date());
  const monthLabel = new Date(
    anchor.getFullYear(),
    anchor.getMonth(),
    1,
  ).toLocaleDateString(undefined, {
    month: "long",
    year: "numeric",
  });
  // Week label: "Oct 12 – 18, 2026" (or "Sep 28 – Oct 4, 2026" across a
  // month boundary).
  const weekLabel = useMemo(() => {
    if (view !== "week") return "";
    const days = weekGrid(anchor);
    const fmtDay = (d: Date) =>
      d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
    const fmtEnd = (d: Date) =>
      d.toLocaleDateString(undefined, {
        month: "short",
        day: "numeric",
        year: "numeric",
      });
    return `${fmtDay(days[0])} – ${fmtEnd(days[6])}`;
  }, [view, anchor]);
  const viewLabel = view === "week" ? weekLabel : monthLabel;

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

  // Shared by the month and week ScheduleDnd wrappers — the same drag
  // language in both views (C26T0 month, C26T1 week).
  const scheduleOverlay = activeIssue ? (
    <div className="w-56 rotate-2 rounded-[6px] border border-border bg-surface p-1 shadow-lg">
      <IssueChip
        issue={activeIssue}
        state={statesById.get(activeIssue.state_id)}
        onOpen={() => undefined}
      />
    </div>
  ) : undefined;

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
            aria-label={view === "week" ? "Previous week" : "Previous month"}
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
            aria-label={view === "week" ? "Next week" : "Next month"}
          >
            <ChevronRight className="h-3.5 w-3.5" />
          </button>
          {/* Month/Week switcher — PageHeader segmented language (C19T4). */}
          <div
            role="tablist"
            aria-label="Calendar view"
            className="flex h-[30px] items-center rounded-md border border-border bg-[#101012] p-[2px]"
          >
            {(["month", "week"] as const).map((v) => {
              const active = view === v;
              return (
                <button
                  key={v}
                  type="button"
                  role="tab"
                  aria-selected={active}
                  onClick={() => setView(v)}
                  className={cn(
                    "h-[24px] rounded px-2 text-[10px] font-medium transition-colors",
                    active
                      ? "bg-raised text-ink"
                      : "text-ink-muted hover:text-ink-secondary",
                  )}
                >
                  {v === "month" ? "Month" : "Week"}
                </button>
              );
            })}
          </div>
          <h2 className="ml-2 text-[13px] font-semibold text-ink">
            {viewLabel}
          </h2>
          {/* C72T2: calendar-local Group: None | Epic — same segmented
              language as the Month/Week switcher. */}
          <span className="ml-2 text-[11px] text-ink-muted">Group:</span>
          <div
            role="tablist"
            aria-label="Group by"
            className="flex h-[30px] items-center rounded-md border border-border bg-[#101012] p-[2px]"
          >
            {(["none", "epic"] as const).map((v) => {
              const active = groupBy === v;
              return (
                <button
                  key={v}
                  type="button"
                  role="tab"
                  aria-selected={active}
                  onClick={() => setGroupBy(v)}
                  className={cn(
                    "h-[24px] rounded px-2 text-[10px] font-medium transition-colors",
                    active
                      ? "bg-raised text-ink"
                      : "text-ink-muted hover:text-ink-secondary",
                  )}
                >
                  {v === "none" ? "None" : "Epic"}
                </button>
              );
            })}
          </div>
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
            {view === "week" ? (
              // C26T1: week view is drag-reschedulable like the month grid —
              // same ScheduleDnd wiring, handlers, mutation and
              // announcements. Columns are tall with no internal scroll, so
              // the whole column is the drop target.
              <ScheduleDnd
                onDragStart={handleDragStart}
                onDragEnd={handleDragEnd}
                overlay={scheduleOverlay}
              >
              {groupBy === "epic" ? (
                // C72T2: grouped week — swimlane gutter + per-lane bands.
                // Bands are render-only; the whole columns stay the drop
                // targets (DnD wiring unchanged).
                <div className="overflow-hidden rounded-md border border-border">
                  <div className="grid grid-cols-[200px_repeat(7,minmax(0,1fr))] border-b border-border-hairline bg-raised">
                    <div className="flex items-center px-2 py-1.5 text-[10px] font-semibold uppercase tracking-[0.06em] text-ink-muted">
                      Epic lane
                    </div>
                    {cells.map((date) => {
                      const key = dayKey(date);
                      const isToday = key === todayKey;
                      return (
                        <div
                          key={key}
                          className="flex items-center gap-1.5 border-l border-l-border-hairline px-2 py-1.5"
                        >
                          <span
                            className={cn(
                              "text-[10px] font-semibold uppercase tracking-[0.06em]",
                              isToday ? "text-accent-blue" : "text-ink-muted",
                            )}
                          >
                            {WEEKDAYS[(date.getDay() + 6) % 7]}
                          </span>
                          <span
                            className={cn(
                              "inline-flex h-6 w-6 items-center justify-center rounded-full text-[11px]",
                              isToday
                                ? "bg-accent-blue font-semibold text-white"
                                : "text-ink-secondary",
                            )}
                          >
                            {date.getDate()}
                          </span>
                        </div>
                      );
                    })}
                  </div>
                  <div className="grid grid-cols-[200px_repeat(7,minmax(0,1fr))]">
                    <div className="min-h-64 border-r border-r-border-hairline bg-raised">
                      {laneStack}
                    </div>
                    {cells.map((date) => {
                      const key = dayKey(date);
                      const isToday = key === todayKey;
                      return (
                        <DroppableWeekColumn
                          key={key}
                          dayKey={key}
                          isToday={isToday}
                          onQuickAdd={canEdit ? quickAdd : undefined}
                        >
                          {laneBands(key, "", "min-h-10")}
                        </DroppableWeekColumn>
                      );
                    })}
                  </div>
                </div>
              ) : (
              <div className="overflow-hidden rounded-md border border-border">
                <div className="grid grid-cols-7 border-b border-border-hairline bg-raised">
                  {cells.map((date) => {
                    const key = dayKey(date);
                    const isToday = key === todayKey;
                    return (
                      <div
                        key={key}
                        className="flex items-center gap-1.5 px-2 py-1.5 [&:not(:last-child)]:border-r [&:not(:last-child)]:border-r-border-hairline"
                      >
                        <span
                          className={cn(
                            "text-[10px] font-semibold uppercase tracking-[0.06em]",
                            isToday ? "text-accent-blue" : "text-ink-muted",
                          )}
                        >
                          {WEEKDAYS[(date.getDay() + 6) % 7]}
                        </span>
                        <span
                          className={cn(
                            "inline-flex h-6 w-6 items-center justify-center rounded-full text-[11px]",
                            isToday
                              ? "bg-accent-blue font-semibold text-white"
                              : "text-ink-secondary",
                          )}
                        >
                          {date.getDate()}
                        </span>
                      </div>
                    );
                  })}
                </div>
                <div className="grid grid-cols-7">
                  {cells.map((date) => {
                    const key = dayKey(date);
                    const issues = byDay.get(key) ?? [];
                    const isToday = key === todayKey;
                    return (
                      <DroppableWeekColumn
                        key={key}
                        dayKey={key}
                        isToday={isToday}
                        onQuickAdd={canEdit ? quickAdd : undefined}
                      >
                        <div className="space-y-0.5">
                          {issues.map((issue) => (
                            <DraggableIssueChip
                              key={issue.id}
                              issue={issue}
                              state={statesById.get(issue.state_id)}
                              onOpen={() => openPeek(issue.id)}
                              disabled={!canEdit}
                            />
                          ))}
                        </div>
                      </DroppableWeekColumn>
                    );
                  })}
                </div>
              </div>
              )}
              {/* C26T2: unscheduled strip — drop zone for unscheduling, drag
                  source for scheduling (members). */}
              <UnscheduledStrip
                issues={undated}
                statesById={statesById}
                canEdit={canEdit}
                onOpen={(issue) => openPeek(issue.id)}
                groups={stripGroups}
              />
              </ScheduleDnd>
            ) : (
              // C26T0: month grid is drag-reschedulable — day cells are
              // droppables, chips are draggables (disabled for guests).
              <ScheduleDnd
                onDragStart={handleDragStart}
                onDragEnd={handleDragEnd}
                overlay={scheduleOverlay}
              >
              {groupBy === "epic" ? (
                // C72T2: grouped month — 200px swimlane gutter + per-lane
                // bands inside each day cell. The gutter repeats the lane
                // stack once per week-row (mock tabs 1–2); bands are
                // render-only and the whole day cells stay the drop
                // targets (DnD wiring unchanged).
                <div className="grid grid-cols-[200px_repeat(7,minmax(0,1fr))] overflow-hidden rounded-md border border-border">
                  <div className="border-b border-border-hairline bg-raised px-2 py-1.5 text-[10px] font-semibold uppercase tracking-[0.06em] text-ink-muted">
                    Epic lane
                  </div>
                  {WEEKDAYS.map((d) => (
                    <div
                      key={d}
                      className="border-b border-border-hairline border-l border-l-border-hairline bg-raised px-2 py-1.5 text-[10px] font-semibold uppercase tracking-[0.06em] text-ink-muted"
                    >
                      {d}
                    </div>
                  ))}
                  {weekRows.map((days, wi) => (
                    <Fragment key={wi}>
                      <div className="border-b border-border-hairline border-r border-r-border-hairline bg-raised">
                        {laneStack}
                      </div>
                      {days.map((date) => {
                        const key = dayKey(date);
                        const isToday = key === todayKey;
                        const isOtherMonth =
                          date.getMonth() !== anchor.getMonth();
                        return (
                          <DroppableDayCell
                            key={key}
                            dayKey={key}
                            isOtherMonth={isOtherMonth}
                            onQuickAdd={canEdit ? quickAdd : undefined}
                            columns={8}
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
                            {laneBands(key, "max-h-20 overflow-y-auto", "min-h-8")}
                          </DroppableDayCell>
                        );
                      })}
                    </Fragment>
                  ))}
                </div>
              ) : (
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
                  const isOtherMonth = date.getMonth() !== anchor.getMonth();
                  return (
                    <DroppableDayCell
                      key={key}
                      dayKey={key}
                      isOtherMonth={isOtherMonth}
                      onQuickAdd={canEdit ? quickAdd : undefined}
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
                          <DraggableIssueChip
                            key={issue.id}
                            issue={issue}
                            state={statesById.get(issue.state_id)}
                            onOpen={() => openPeek(issue.id)}
                            disabled={!canEdit}
                          />
                        ))}
                      </div>
                    </DroppableDayCell>
                  );
                })}
              </div>
              )}
              {/* C26T2: unscheduled strip — drop zone for unscheduling, drag
                  source for scheduling (members). */}
              <UnscheduledStrip
                issues={undated}
                statesById={statesById}
                canEdit={canEdit}
                onOpen={(issue) => openPeek(issue.id)}
                groups={stripGroups}
              />
              </ScheduleDnd>
            )}

            {/* C26T0: polite live region announces drag-reschedule drops. */}
            <div aria-live="polite" className="sr-only">
              {announcement}
            </div>

            {byDay.size === 0 && undated.length === 0 && (
              <p className="text-[13px] text-ink-muted">
                No issues scheduled for {viewLabel}. Issues appear here once
                they get a due or start date.
              </p>
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
