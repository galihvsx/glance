import { Fragment, useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import type { DragEndEvent, DragStartEvent } from "@dnd-kit/core";
import { CalendarPlus, ChevronLeft, ChevronRight } from "lucide-react";
import PageHeader from "../components/shell/PageHeader";
import PeekDrawer from "../components/issue/PeekDrawer";
import PqlEditor from "../components/issue/PqlEditor";
import SavedViewsMenu from "../components/issue/SavedViewsMenu";
import { Alert, AlertDescription } from "../components/ui/alert";
import { usePeekParam } from "../components/issue/usePeek";
import { groupIssuesByEpic } from "../components/issue/groupByEpic";
import { splitNestedIssues } from "../components/issue/splitNestedIssues";
import { IssueChip } from "../components/calendar/IssueChip";
import { SubtaskChip } from "../components/calendar/SubtaskChip";
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
import { computeCascade, type CascadeShift } from "../components/gantt/ganttCascade";
import { barSpan } from "../lib/gantt";
import { toast } from "../components/ui/toast";
import { api, ApiError } from "../lib/api";
import { EMPTY_FILTERS } from "../lib/filters";
import { formatPqlError } from "../lib/pql";
import {
  createFeedToken,
  fetchFeedTokenStatus,
  projectFeedURL,
} from "../lib/calendarFeed";
import {
  resolveScheduleDrop,
  resolveUnscheduleDrop,
  type ResolvedScheduleDrop,
} from "../lib/calendar-dnd";
import type {
  Issue,
  IssueLink,
  IssueListResult,
  IssueState,
  Project,
  Workspace,
} from "../lib/types";
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

// C145T2: calendar-local "Show sub-work items" storage — see the
// in-component comment where the setting lives.
function subWorkItemsStorageKey(slug: string, identifier: string): string {
  return `glance:calendar-sub-work-items:${slug}:${identifier}`;
}

function loadSubWorkItems(slug: string, identifier: string): boolean {
  try {
    const raw = localStorage.getItem(subWorkItemsStorageKey(slug, identifier));
    if (!raw) return true;
    const parsed: unknown = JSON.parse(raw);
    return typeof parsed === "boolean" ? parsed : true;
  } catch {
    return true;
  }
}

/** 28px toolbar button in the C19 token ramp (matches PageHeader controls). */
const calControlClass =
  "inline-flex h-7 items-center gap-1.5 rounded-md border border-border bg-surface px-2 text-[11px] font-medium text-ink-secondary hover:bg-raised hover:text-ink disabled:opacity-50";

interface ListResultWithLinks extends IssueListResult {
  links?: IssueLink[];
}

/** Fetch every page of a bounded list query (per_page=100, cursor loop).
 *  C158T1: ?include_links=1 per page (Gantt pattern verbatim) so
 *  dependency edges arrive in one shot — no N+1. */
async function fetchAllIssues(path: string): Promise<{
  issues: Issue[];
  links: IssueLink[];
}> {
  const issues: Issue[] = [];
  const links: IssueLink[] = [];
  let cursor: string | undefined;
  do {
    const sep = path.includes("?") ? "&" : "?";
    const page = new URLSearchParams({ per_page: "100", include_links: "1" });
    if (cursor) page.set("cursor", cursor);
    const res = await api.get<ListResultWithLinks>(`${path}${sep}${page}`);
    issues.push(...res.results);
    if (res.links) links.push(...res.links);
    cursor = res.next_cursor;
  } while (cursor);
  return { issues, links };
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

  // C145T2: calendar-local "Show sub-work items" — its OWN storage
  // key, following the calendar-local group-by precedent above (NOT
  // the list's display key: flipping it here must never restructure
  // the list/board). Default true, matching
  // DEFAULT_DISPLAY_SETTINGS.showSubWorkItems.
  const [cachedSubWorkItems, setCachedSubWorkItems] = useState(() => ({
    key: viewKey,
    show: loadSubWorkItems(slug, identifier),
  }));
  if (cachedSubWorkItems.key !== viewKey) {
    setCachedSubWorkItems({
      key: viewKey,
      show: loadSubWorkItems(slug, identifier),
    });
  }
  const showSubWorkItems = cachedSubWorkItems.show;
  const setShowSubWorkItems = (next: boolean) => {
    try {
      localStorage.setItem(
        subWorkItemsStorageKey(slug, identifier),
        JSON.stringify(next),
      );
    } catch {
      // Storage full / private mode — still applies for the session.
    }
    setCachedSubWorkItems((prev) => ({ ...prev, show: next }));
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
  // C145T2: include_subtasks=1 rides the toggle on every visual
  // query; the toggle sits in each query key so on/off refetches
  // instead of reusing stale rows.
  const subFlag = showSubWorkItems ? "&include_subtasks=1" : "";
  const dueQuery = useQuery({
    queryKey: ["calendar", slug, identifier, "due", start, showSubWorkItems],
    queryFn: () =>
      fetchAllIssues(
        `${base}/issues?due_after=${rfc(start)}&due_before=${rfc(end)}${subFlag}`,
      ),
  });
  const startQuery = useQuery({
    queryKey: ["calendar", slug, identifier, "start", start, showSubWorkItems],
    queryFn: () =>
      fetchAllIssues(
        `${base}/issues?start_after=${rfc(start)}&start_before=${rfc(end)}${subFlag}`,
      ),
  });
  const undatedQuery = useQuery({
    queryKey: ["calendar", slug, identifier, "undated", showSubWorkItems],
    queryFn: () => fetchAllIssues(`${base}/issues?undated=1${subFlag}`),
  });

  // C109T1: PQL mode on the calendar. Copies the Cycles detail pattern
  // verbatim (C95T1/C107T1): pqlMode local state, appliedQuery,
  // probe-first handlePqlApply through the three window queries, and
  // a PQL editor keyed by appliedQuery (the C107 re-apply lesson —
  // PqlEditor's draft is initial-value-only, so a re-apply without
  // the key shows a stale draft). PQL mode changes ONLY which issues
  // render: the window queries keep their window params and AND with
  // the predicate (C109T0 backend), window navigation keeps the
  // applied query (the PQL query keys include the window start), and
  // group-by-epic stays display-only over the filtered set. The PQL
  // query keys include the applied query so PQL and visual caches
  // never cross-contaminate; leaving PQL mode clears the applied
  // query and the already-cached unfiltered window sets return.
  const [pqlMode, setPqlMode] = useState(false);
  const [appliedQuery, setAppliedQuery] = useState("");
  // C110T1: same ["project", slug, identifier] query Issues/Board use
  // — the detail-mode SavedViewsMenu needs a project UUID, and the
  // shared key is cache-warm whenever the user came from another
  // project page.
  const projectQuery = useQuery({
    queryKey: ["project", slug, identifier],
    queryFn: () => api.get<Project>(base),
  });
  // C110T1: a saved view whose query fails its probe has no editor
  // inline error to land in — the editor only reports failures from
  // its own Apply button, and it mounts fresh (empty draft) for a
  // menu apply. The failure is surfaced here, at the detail level,
  // instead of vanishing or rejecting unhandled.
  const [savedViewError, setSavedViewError] = useState<string | null>(null);
  const pqlActive = pqlMode && appliedQuery.trim() !== "";
  const pqlDueQuery = useQuery({
    queryKey: ["calendar", slug, identifier, "due-pql", start, appliedQuery, showSubWorkItems],
    enabled: pqlActive,
    queryFn: () =>
      fetchAllIssues(
        `${base}/issues?due_after=${rfc(start)}&due_before=${rfc(end)}&pql=${encodeURIComponent(appliedQuery.trim())}${subFlag}`,
      ),
  });
  const pqlStartQuery = useQuery({
    queryKey: ["calendar", slug, identifier, "start-pql", start, appliedQuery, showSubWorkItems],
    enabled: pqlActive,
    queryFn: () =>
      fetchAllIssues(
        `${base}/issues?start_after=${rfc(start)}&start_before=${rfc(end)}&pql=${encodeURIComponent(appliedQuery.trim())}${subFlag}`,
      ),
  });
  const pqlUndatedQuery = useQuery({
    queryKey: ["calendar", slug, identifier, "undated-pql", appliedQuery, showSubWorkItems],
    enabled: pqlActive,
    queryFn: () =>
      fetchAllIssues(
        `${base}/issues?undated=1&pql=${encodeURIComponent(appliedQuery.trim())}${subFlag}`,
      ),
  });
  // Probe-first apply (Cycles pattern): fetch one page of each window
  // query with the candidate query and commit only on success. A 400
  // rejects, so PqlEditor shows it inline and the displayed sets are
  // untouched (appliedQuery — and therefore the displayed sets —
  // never changes). Empty query = no filter: stays in PQL mode,
  // shows the full window sets.
  async function handlePqlApply(query: string): Promise<void> {
    const q = query.trim();
    if (!q) {
      setAppliedQuery("");
      return;
    }
    const probe = (params: Record<string, string>) => {
      const p = new URLSearchParams({ ...params, pql: q, per_page: "100" });
      // C145T2: the probe validates the same query the PQL display
      // queries will run, flag included.
      if (showSubWorkItems) p.set("include_subtasks", "1");
      return api.get<IssueListResult>(`${base}/issues?${p.toString()}`);
    };
    await Promise.all([
      probe({ due_after: rfc(start), due_before: rfc(end) }),
      probe({ start_after: rfc(start), start_before: rfc(end) }),
      probe({ undated: "1" }),
    ]);
    setAppliedQuery(q);
  }
  function handlePqlModeChange(mode: "all" | "pql") {
    if (mode === "pql") {
      setPqlMode(true);
      return;
    }
    // Leaving PQL mode clears the applied query, so the unfiltered
    // window sets (already cached) are restored immediately.
    setPqlMode(false);
    setAppliedQuery("");
    setSavedViewError(null);
  }
  // C110T1: applying a saved PQL view from the detail-mode menu.
  // Probe FIRST, then enter PQL mode: the probe commits appliedQuery
  // while still in unfiltered mode, so the editor mounts seeded with
  // the view's query (C107 key lesson). The window anchor
  // (month/week navigation state) is NEVER touched by a view apply:
  // a saved query ANDs with the current window (C109 semantics), it
  // never moves it. A saved view whose PQL names dates outside the
  // current window therefore shows an empty view until the user
  // moves the window — that is the honest AND, not a bug, so do
  // not "fix" this into a window jump.
  async function handleSavedViewApplied(pqlQuery: string | null) {
    if (pqlQuery === null) {
      setPqlMode(false);
      setAppliedQuery("");
      setSavedViewError(null);
      return;
    }
    try {
      await handlePqlApply(pqlQuery);
    } catch (e) {
      setSavedViewError(formatPqlError(e));
    }
    setPqlMode(true);
  }
  // While a committed PQL query is still loading, keep showing the
  // unfiltered window sets rather than blanking — the filtered sets
  // replace them as soon as they arrive (Cycles precedent).
  const displayedDue = pqlActive
    ? (pqlDueQuery.data?.issues ?? dueQuery.data?.issues ?? [])
    : (dueQuery.data?.issues ?? []);
  const displayedStart = pqlActive
    ? (pqlStartQuery.data?.issues ?? startQuery.data?.issues ?? [])
    : (startQuery.data?.issues ?? []);
  const displayedUndated = pqlActive
    ? (pqlUndatedQuery.data?.issues ?? undatedQuery.data?.issues ?? [])
    : (undatedQuery.data?.issues ?? []);

  // C158T1: cascade input — merged visible issues (due ∪ start ∪
  // undated, dedup by id; the displayed sets above already carry
  // the PQL variants when PQL is active) + merged links, deduped
  // by link id (the same edge can arrive via both the due and the
  // start window — dedup is required, not optional). Dependents
  // outside the fetched set are skipped per rule 6 — the cascade
  // covers the visible set only, same as Gantt. PQL mode reads
  // the PQL queries' links (falling back to the base queries'
  // while they load, matching the displayed-set fallback above);
  // the probe-first apply stays link-free (it only validates).
  const { cascadeIssues, cascadeLinks } = useMemo(() => {
    const seen = new Set<string>();
    const issues: Issue[] = [];
    for (const issue of [
      ...displayedDue,
      ...displayedStart,
      ...displayedUndated,
    ]) {
      if (seen.has(issue.id)) continue;
      seen.add(issue.id);
      issues.push(issue);
    }
    const linkSeen = new Set<string>();
    const links: IssueLink[] = [];
    const linkSources =
      pqlActive &&
      (pqlDueQuery.data ?? pqlStartQuery.data ?? pqlUndatedQuery.data)
        ? [pqlDueQuery.data, pqlStartQuery.data, pqlUndatedQuery.data]
        : [dueQuery.data, startQuery.data, undatedQuery.data];
    for (const q of linkSources) {
      if (!q) continue;
      for (const l of q.links) {
        if (linkSeen.has(l.id)) continue;
        linkSeen.add(l.id);
        links.push(l);
      }
    }
    return { cascadeIssues: issues, cascadeLinks: links };
  }, [
    displayedDue,
    displayedStart,
    displayedUndated,
    pqlActive,
    pqlDueQuery.data,
    pqlStartQuery.data,
    pqlUndatedQuery.data,
    dueQuery.data,
    startQuery.data,
    undatedQuery.data,
  ]);
  // C158T1: presence lookup for rule-7 shift PATCHes, over the
  // same merged set the engine ran on.
  const cascadeIssueById = useMemo(
    () => new Map(cascadeIssues.map((i) => [i.id, i])),
    [cascadeIssues],
  );

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

  // C145T2: one parent chip plus its nested child chips, indented
  // under it in the SAME cell. Child chips are display +
  // click-to-peek only — never draggable (no nested drag on the
  // calendar this cycle), so they carry no dragId and never collide
  // in the dnd registry, including multi-appearance lane bands.
  const parentStack = (issue: Issue, dragId?: string) => (
    <>
      <DraggableIssueChip
        issue={issue}
        state={statesById.get(issue.state_id)}
        onOpen={() => openPeek(issue.id)}
        disabled={!canEdit}
        dragId={dragId}
      />
      {(nestedByParent.get(issue.id) ?? []).map((row) => (
        <SubtaskChip
          key={row.id}
          row={row}
          state={row.state_id ? statesById.get(row.state_id) : undefined}
          onOpen={() => openPeek(row.id)}
        />
      ))}
    </>
  );

  const cells = useMemo(
    () =>
      view === "week"
        ? weekGrid(anchor)
        : monthGrid(anchor.getFullYear(), anchor.getMonth()),
    [view, anchor],
  );
  // C145T2: split the fetched sets BEFORE bucketing, so a child
  // nested under a fetched parent never ALSO renders as a flat chip
  // in its own day cell or the strip (child travels with parent).
  // Partitioning back into the due/start/undated lanes preserves
  // bucketIssuesByDay's exact placement semantics (a start-window
  // issue carrying a target date stays unplaced, as today). An
  // orphan child — parent outside the fetched set — is absent from
  // every parent's `subtasks`, so it stays flat in its own placement
  // and never vanishes.
  const { visibleIssues, nestedByParent } = useMemo(() => {
    const byId = new Map<string, Issue>();
    for (const issue of [
      ...displayedDue,
      ...displayedStart,
      ...displayedUndated,
    ]) {
      const prev = byId.get(issue.id);
      if (prev) {
        if ((prev.subtasks?.length ?? 0) === 0 && issue.subtasks?.length) {
          byId.set(issue.id, issue);
        }
      } else {
        byId.set(issue.id, issue);
      }
    }
    return splitNestedIssues([...byId.values()], showSubWorkItems);
  }, [displayedDue, displayedStart, displayedUndated, showSubWorkItems]);
  const visibleById = useMemo(
    () => new Set(visibleIssues.map((i) => i.id)),
    [visibleIssues],
  );
  const byDay = useMemo(
    () =>
      bucketIssuesByDay(
        displayedDue.filter((i) => visibleById.has(i.id)),
        displayedStart.filter((i) => visibleById.has(i.id)),
      ),
    [displayedDue, displayedStart, visibleById],
  );
  const undated = useMemo(
    () => displayedUndated.filter((i) => visibleById.has(i.id)),
    [displayedUndated, visibleById],
  );

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
            <Fragment key={issue.id}>
              {parentStack(issue, `${issue.id}:${lane.key}`)}
            </Fragment>
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
  const scheduleMutation = useScheduleMutation(slug, identifier, { start, end, showSubWorkItems });
  const queryClient = useQueryClient();
  const [activeIssue, setActiveIssue] = useState<Issue | null>(null);
  const [announcement, setAnnouncement] = useState("");

  // C158T1: shared cascade commit (cycle 158 plan, decision 6) —
  // built for the strip-schedule path; the dated-chip move path
  // (T2) reuses it unchanged. The dragged PATCH goes through the
  // existing scheduleMutation (keeping its optimistic update); one
  // presence-preserving PATCH per shift fires alongside it
  // (Promise.allSettled), with the Gantt cascade success/failure
  // toasts and calendar-prefix invalidation (rule 8 semantics,
  // calendar cache keys). A dragged-PATCH failure is already
  // toasted by the mutation's onError, so this block only toasts
  // for shift failures — never a double toast.
  const commitWithCascade = (
    resolved: ResolvedScheduleDrop,
    shifts: CascadeShift[],
  ) => {
    // Rule 7: a shift PATCH sends start_date only if the issue
    // originally had one, target_date only if it originally had
    // one — a single-date dependent stays single-date. A missing
    // original is unreachable (shifts come from cascadeIssues)
    // and is skipped defensively rather than patched with a
    // guessed shape.
    const cascadePatches = shifts.flatMap((shift) => {
      const original = cascadeIssueById.get(shift.id);
      if (!original) return [];
      const body: { start_date?: string; target_date?: string } = {};
      if (original.start_date) body.start_date = shift.start_date;
      if (original.target_date) body.target_date = shift.target_date;
      return [{ id: shift.id, body }];
    });
    void (async () => {
      const results = await Promise.allSettled([
        scheduleMutation.mutateAsync(
          { uuid: resolved.uuid, dates: resolved.dates },
          { onSuccess: () => setAnnouncement(resolved.announcement) },
        ),
        ...cascadePatches.map((p) =>
          api.patch<Issue>(`${base}/issues/${p.id}`, p.body),
        ),
      ]);
      const draggedFailed = results[0].status === "rejected";
      const shiftFailed = results
        .slice(1)
        .some((r) => r.status === "rejected");
      if (!draggedFailed && !shiftFailed) {
        toast.add({
          title: `Shifted ${cascadePatches.length} dependent ${cascadePatches.length === 1 ? "issue" : "issues"}`,
          type: "success",
        });
      } else if (!draggedFailed && shiftFailed) {
        toast.add({
          title:
            "Some dependent issues failed to shift — the calendar was refreshed.",
          type: "error",
        });
      }
      void queryClient.invalidateQueries({
        queryKey: ["calendar", slug, identifier],
      });
    })();
  };

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
    const fromStrip = e.active.data.current?.fromStrip === true;
    const resolved =
      overId === UNSCHEDULED_STRIP_ID
        ? resolveUnscheduleDrop(issue)
        : resolveScheduleDrop(
            issue,
            overId,
            fromStrip,
          );
    if (resolved == null) return; // no-op drop → no PATCH
    // C158T1: strip-schedule cascade (rule 9) — the dragged chip
    // is dateless, so its final span is the 1-day span [d, d]
    // (d = the drop day, the same day the dragged PATCH targets).
    // Empty cascade ⇒ today's single PATCH, byte-identical.
    // Otherwise the dragged PATCH + one presence-preserving PATCH
    // per shift fire through the shared cascade-commit block.
    // The unschedule path above never cascades (clearing a date
    // can only grow gaps).
    if (fromStrip && overId !== UNSCHEDULED_STRIP_ID) {
      const dropDay =
        typeof resolved.dates.target_date === "string"
          ? resolved.dates.target_date
          : null;
      if (dropDay) {
        const shifts = computeCascade(
          cascadeIssues,
          cascadeLinks,
          issue.id,
          dropDay,
          dropDay,
          "schedule",
        );
        if (shifts.length > 0) {
          commitWithCascade(resolved, shifts);
          return;
        }
      }
    }
    // C158T2: dated-chip move cascade ("move" mode, rules 3–4) —
    // k is the drop delta and the new span is the delta-shifted
    // dates resolveDropDates produced (barSpan resolves a
    // single-date chip to its 1-day span, the engine's contract).
    // Moving earlier (k ≤ 0) ⇒ the engine returns an empty
    // cascade, so the commit stays today's single PATCH,
    // byte-identical. Otherwise the dragged PATCH + one
    // presence-preserving PATCH per shift fire through the SAME
    // shared cascade-commit block as the strip-schedule path
    // (decision 6), with the same toasts and invalidation.
    if (!fromStrip && overId !== UNSCHEDULED_STRIP_ID) {
      const newSpan = barSpan(
        resolved.dates.start_date,
        resolved.dates.target_date,
      );
      if (newSpan.start !== null && newSpan.end !== null) {
        const shifts = computeCascade(
          cascadeIssues,
          cascadeLinks,
          issue.id,
          newSpan.start,
          newSpan.end,
          "move",
        );
        if (shifts.length > 0) {
          commitWithCascade(resolved, shifts);
          return;
        }
      }
    }
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
          {/* C145T2: calendar-local Show sub-work items, next to
              Group — same toolbar language, own storage key (see the
              setting above), default on. */}
          <label className="ml-2 flex cursor-pointer items-center gap-1.5 text-[11px] text-ink-muted">
            <input
              type="checkbox"
              checked={showSubWorkItems}
              onChange={(e) => setShowSubWorkItems(e.target.checked)}
              aria-label="Show sub-work items"
            />
            Show sub-work items
          </label>
          {/* C109T1: All-issues / PQL toggle (Cycles pattern), above
              the calendar next to the existing view controls. */}
          <div
            role="group"
            aria-label="Issue list mode"
            className="ml-2 flex h-[30px] items-center rounded-md border border-border bg-[#101012] p-[2px]"
          >
            <button
              type="button"
              aria-pressed={!pqlMode}
              onClick={() => handlePqlModeChange("all")}
              className={cn(
                "h-[24px] rounded px-2 text-[10px] font-medium transition-colors",
                !pqlMode
                  ? "bg-raised text-ink"
                  : "text-ink-muted hover:text-ink-secondary",
              )}
            >
              All issues
            </button>
            <button
              type="button"
              aria-pressed={pqlMode}
              onClick={() => handlePqlModeChange("pql")}
              className={cn(
                "h-[24px] rounded px-2 text-[10px] font-medium transition-colors",
                pqlMode
                  ? "bg-raised text-ink"
                  : "text-ink-muted hover:text-ink-secondary",
              )}
            >
              PQL
            </button>
          </div>
          {/* C110T1: detail-mode SavedViewsMenu beside the toggle
              (CycleDetail/EpicDetail detail toolbar verbatim).
              Detail mode guarantees no URL writes and no default
              auto-apply, so the page settles PQL state through
              onViewApplied only — and a view apply never moves the
              window anchor (see handleSavedViewApplied). */}
          <SavedViewsMenu
            projectId={projectQuery.data?.id ?? ""}
            filters={EMPTY_FILTERS}
            display={null}
            onApplyDisplay={null}
            pqlMode={pqlMode}
            pqlQuery={appliedQuery}
            onViewApplied={handleSavedViewApplied}
            detailMode
          />
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

        {savedViewError && (
          <Alert variant="destructive" className="mb-2">
            <AlertDescription>{savedViewError}</AlertDescription>
          </Alert>
        )}

        {pqlMode && (
          // Keyed by the applied query: PqlEditor's draft is
          // initial-value-only, so without this a re-apply while
          // already in PQL mode would leave the editor showing the
          // previous draft instead of the applied query (C107
          // lesson). showSaveHint={true}: the detail-mode Views
          // menu beside the toggle is now the save path (C108T1
          // corrected hint text).
          <PqlEditor
            key={appliedQuery}
            appliedQuery={appliedQuery}
            onApply={handlePqlApply}
            onClear={() => setAppliedQuery("")}
            showSaveHint={true}
            saveHintText="To save this query, use Views → Save current view next to the All issues / PQL toggle; it re-applies when the view is opened."
          />
        )}

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
                            <Fragment key={issue.id}>
                              {parentStack(issue)}
                            </Fragment>
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
                nestedByParent={nestedByParent}
                onOpenSubtask={(id) => openPeek(id)}
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
                          <Fragment key={issue.id}>
                            {parentStack(issue)}
                          </Fragment>
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
                nestedByParent={nestedByParent}
                onOpenSubtask={(id) => openPeek(id)}
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
