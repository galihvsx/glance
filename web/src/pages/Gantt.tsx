import { useMemo, useRef, useState } from "react";
import { useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronLeft, ChevronRight } from "lucide-react";
import ProjectNav from "../components/project/ProjectNav";
import PeekDrawer from "../components/issue/PeekDrawer";
import { usePeekParam } from "../components/issue/usePeek";
import { api } from "../lib/api";
import type {
  Issue,
  IssueLink,
  IssueListResult,
  IssueState,
  Workspace,
} from "../lib/types";
import {
  addDays,
  barSpan,
  daysBetween,
  dayToTs,
  dayToX,
  ganttDays,
  ganttMonths,
} from "../lib/gantt";
import { cn } from "../lib/utils";

/** State-group order for the row grouping (same vocabulary as cycles). */
const GROUP_ORDER = [
  "triage",
  "backlog",
  "unstarted",
  "started",
  "completed",
  "cancelled",
];

const DAY_W = 28;
const ROW_H = 34;
const HEADER_H = 56;
const GUTTER_W = 260;

interface ListResultWithLinks extends IssueListResult {
  links?: IssueLink[];
}

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

type Row =
  | { kind: "group"; group: string }
  | { kind: "issue"; issue: Issue }
  | { kind: "unscheduled-header" }
  | { kind: "issue-undated"; issue: Issue };

export default function Gantt() {
  const { slug = "", identifier = "" } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;
  const { peekUuid, openPeek, closePeek } = usePeekParam();
  const queryClient = useQueryClient();

  const now = new Date();
  const [year, setYear] = useState(now.getFullYear());
  const [month, setMonth] = useState(now.getMonth()); // 0-indexed, center

  const go = (delta: number) => {
    const d = new Date(year, month + delta, 1);
    setYear(d.getFullYear());
    setMonth(d.getMonth());
  };

  const days = useMemo(() => ganttDays(year, month), [year, month]);
  const windowStart = days[0];
  const windowEnd = addDays(days[days.length - 1], 1); // exclusive
  const months = useMemo(() => ganttMonths(days), [days]);
  const rfc = (day: string) => `${day}T00:00:00Z`;

  // Three bounded queries (same pattern as the calendar view), each with
  // ?include_links=1 so dependency edges arrive in one shot — no N+1.
  const dueQuery = useQuery({
    queryKey: ["gantt", slug, identifier, "due", windowStart],
    queryFn: () =>
      fetchAllIssues(
        `${base}/issues?due_after=${rfc(windowStart)}&due_before=${rfc(windowEnd)}`,
      ),
  });
  const startQuery = useQuery({
    queryKey: ["gantt", slug, identifier, "start", windowStart],
    queryFn: () =>
      fetchAllIssues(
        `${base}/issues?start_after=${rfc(windowStart)}&start_before=${rfc(windowEnd)}`,
      ),
  });
  const undatedQuery = useQuery({
    queryKey: ["gantt", slug, identifier, "undated"],
    queryFn: () => fetchAllIssues(`${base}/issues?undated=1`),
  });

  const statesQuery = useQuery({
    queryKey: ["states", slug, identifier],
    queryFn: () =>
      api.get<{ states: IssueState[] }>(`${base}/states`).then((d) => d.states),
  });
  const workspaceQuery = useQuery({
    queryKey: ["workspace", slug],
    queryFn: () =>
      api.get<Workspace>(`/api/v1/workspaces/${encodeURIComponent(slug)}`),
  });
  const canEdit = (workspaceQuery.data?.role ?? 0) >= 15;

  // Merge + dedupe the three queries (an issue can appear in due+start).
  const { issues, links } = useMemo(() => {
    const seen = new Set<string>();
    const issues: Issue[] = [];
    const links: IssueLink[] = [];
    const linkSeen = new Set<string>();
    for (const q of [dueQuery.data, startQuery.data, undatedQuery.data]) {
      if (!q) continue;
      for (const i of q.issues) {
        if (!seen.has(i.id)) {
          seen.add(i.id);
          issues.push(i);
        }
      }
      for (const l of q.links) {
        if (!linkSeen.has(l.id)) {
          linkSeen.add(l.id);
          links.push(l);
        }
      }
    }
    return { issues, links };
  }, [dueQuery.data, startQuery.data, undatedQuery.data]);

  const statesById = useMemo(() => {
    const m = new Map<string, IssueState>();
    for (const s of statesQuery.data ?? []) m.set(s.id, s);
    return m;
  }, [statesQuery.data]);

  // Rows: issues grouped by state group (dated), then the unscheduled lane.
  const rows = useMemo<Row[]>(() => {
    const dated: Issue[] = [];
    const undated: Issue[] = [];
    for (const i of issues) {
      const b = barSpan(i.start_date, i.target_date);
      (b.start ? dated : undated).push(i);
    }
    const byGroup = new Map<string, Issue[]>();
    for (const i of dated) {
      const g = statesById.get(i.state_id)?.group ?? "backlog";
      if (!byGroup.has(g)) byGroup.set(g, []);
      byGroup.get(g)!.push(i);
    }
    const out: Row[] = [];
    for (const g of GROUP_ORDER) {
      const list = byGroup.get(g);
      if (!list?.length) continue;
      list.sort((a, b) => a.sequence_id - b.sequence_id);
      out.push({ kind: "group", group: g });
      for (const i of list) out.push({ kind: "issue", issue: i });
    }
    // Any dated issues whose state group is unknown still get shown.
    for (const [g, list] of byGroup) {
      if (GROUP_ORDER.includes(g)) continue;
      list.sort((a, b) => a.sequence_id - b.sequence_id);
      out.push({ kind: "group", group: g });
      for (const i of list) out.push({ kind: "issue", issue: i });
    }
    if (undated.length) {
      undated.sort((a, b) => a.sequence_id - b.sequence_id);
      out.push({ kind: "unscheduled-header" });
      for (const i of undated) out.push({ kind: "issue-undated", issue: i });
    }
    return out;
  }, [issues, statesById]);

  // Row Y positions for bars + arrows.
  const rowY = useMemo(() => {
    const m = new Map<string, number>();
    rows.forEach((r, idx) => {
      if (r.kind === "issue" || r.kind === "issue-undated") {
        m.set(r.issue.id, HEADER_H + idx * ROW_H);
      }
    });
    return m;
  }, [rows]);

  // Dependency arrows: A blocks B (issue_id → target_issue_id) draws from
  // the blocker's bar right edge to the blocked issue's bar left edge.
  const arrows = useMemo(() => {
    const out: { x1: number; y1: number; x2: number; y2: number }[] = [];
    for (const l of links) {
      if (l.kind !== "blocks") continue;
      const fromY = rowY.get(l.issue_id);
      const toY = rowY.get(l.target_issue_id);
      if (fromY === undefined || toY === undefined) continue;
      const from = issues.find((i) => i.id === l.issue_id);
      const to = issues.find((i) => i.id === l.target_issue_id);
      if (!from || !to) continue;
      const fb = barSpan(from.start_date, from.target_date);
      const tb = barSpan(to.start_date, to.target_date);
      if (!fb.start || !fb.end || !tb.start) continue;
      const x1 = dayToX(fb.end, windowStart, DAY_W) + DAY_W;
      const x2 = dayToX(tb.start, windowStart, DAY_W);
      const y1 = fromY + ROW_H / 2;
      const y2 = toY + ROW_H / 2;
      out.push({ x1, y1, x2, y2 });
    }
    return out;
  }, [links, rowY, issues, windowStart]);

  // Drag-to-reschedule.
  const [drag, setDrag] = useState<{
    id: string;
    startX: number;
    origStart: string;
    origEnd: string;
    dx: number;
  } | null>(null);
  const svgRef = useRef<SVGSVGElement>(null);
  const suppressClick = useRef(false);

  const patchMutation = useMutation({
    mutationFn: ({ id, start, end }: { id: string; start: string; end: string }) =>
      api.patch<Issue>(`${base}/issues/${id}`, {
        start_date: start,
        target_date: end,
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["gantt"] });
    },
  });

  const svgX = (clientX: number) => {
    const rect = svgRef.current?.getBoundingClientRect();
    return rect ? clientX - rect.left : 0;
  };

  const onBarPointerDown = (
    e: React.PointerEvent,
    issue: Issue,
    b: { start: string; end: string },
  ) => {
    if (!canEdit || e.button !== 0) return;
    e.preventDefault();
    setDrag({
      id: issue.id,
      startX: svgX(e.clientX),
      origStart: b.start,
      origEnd: b.end,
      dx: 0,
    });
  };

  const onPointerMove = (e: React.PointerEvent) => {
    if (!drag) return;
    setDrag({ ...drag, dx: svgX(e.clientX) - drag.startX });
  };

  const onPointerUp = () => {
    if (!drag) return;
    const dayDelta = Math.round(drag.dx / DAY_W);
    setDrag(null);
    if (dayDelta !== 0) {
      // Suppress the click that follows a real drag (it would open peek).
      suppressClick.current = true;
      setTimeout(() => {
        suppressClick.current = false;
      }, 0);
      patchMutation.mutate({
        id: drag.id,
        start: addDays(drag.origStart, dayDelta),
        end: addDays(drag.origEnd, dayDelta),
      });
    }
  };

  const loading =
    dueQuery.isLoading || startQuery.isLoading || undatedQuery.isLoading;
  const error = dueQuery.error ?? startQuery.error ?? undatedQuery.error;

  const todayKey = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, "0")}-${String(now.getDate()).padStart(2, "0")}`;
  const width = days.length * DAY_W;
  const height = HEADER_H + rows.length * ROW_H;

  const monthLabel = (day: string) => {
    const [y, m] = day.split("-").map(Number);
    return new Date(Date.UTC(y, m - 1, 1)).toLocaleDateString(undefined, {
      month: "long",
      year: "numeric",
    });
  };

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
          onClick={() => {
            const t = new Date();
            setYear(t.getFullYear());
            setMonth(t.getMonth());
          }}
          className="rounded-md border px-2 py-1 text-xs hover:bg-accent"
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
        <span className="text-sm font-medium">
          {monthLabel(days[0])} – {monthLabel(days[days.length - 1])}
        </span>
        {!canEdit && (
          <span className="text-xs text-muted-foreground">
            Guest access — timeline is read-only.
          </span>
        )}
      </div>

      {loading && (
        <div className="text-sm text-muted-foreground">Loading timeline…</div>
      )}
      {error && (
        <div className="text-sm text-destructive">
          Failed to load issues.{" "}
          <button
            type="button"
            className="underline"
            onClick={() => {
              void dueQuery.refetch();
              void startQuery.refetch();
              void undatedQuery.refetch();
            }}
          >
            Retry
          </button>
        </div>
      )}
      {!loading && !error && rows.length === 0 && (
        <div className="rounded-md border border-dashed p-8 text-center text-sm text-muted-foreground">
          No issues with dates in this window. Set start or due dates on
          issues to see them on the timeline.
        </div>
      )}
      {!loading && !error && rows.length > 0 && (
        <div className="overflow-auto rounded-md border">
          <div className="flex" style={{ width: GUTTER_W + width }}>
            {/* Left gutter: sticky issue labels */}
            <div
              className="sticky left-0 z-10 shrink-0 border-r bg-background"
              style={{ width: GUTTER_W }}
            >
              <div
                className="border-b font-medium"
                style={{ height: HEADER_H }}
              />
              {rows.map((r) => {
                if (r.kind === "group") {
                  return (
                    <div
                      key={`g-${r.group}`}
                      className="flex items-center border-b bg-muted/40 px-3 text-xs font-semibold uppercase tracking-wide text-muted-foreground"
                      style={{ height: ROW_H }}
                    >
                      {r.group}
                    </div>
                  );
                }
                if (r.kind === "unscheduled-header") {
                  return (
                    <div
                      key="unsched"
                      className="flex items-center border-b bg-muted/40 px-3 text-xs font-semibold uppercase tracking-wide text-muted-foreground"
                      style={{ height: ROW_H }}
                    >
                      Unscheduled
                    </div>
                  );
                }
                return (
                  <button
                    key={r.issue.id}
                    type="button"
                    onClick={() => openPeek(r.issue.id)}
                    className="flex w-full items-center gap-1.5 border-b px-3 text-left text-xs hover:bg-accent"
                    style={{ height: ROW_H }}
                    title={`${r.issue.display_id}: ${r.issue.name}`}
                  >
                    <span className="shrink-0 font-mono text-muted-foreground">
                      {r.issue.display_id}
                    </span>
                    <span className="truncate">{r.issue.name}</span>
                  </button>
                );
              })}
            </div>
            {/* Timeline */}
            <svg
              ref={svgRef}
              width={width}
              height={height}
              className={cn(drag && "cursor-grabbing select-none")}
              onPointerMove={onPointerMove}
              onPointerUp={onPointerUp}
              onPointerLeave={onPointerUp}
            >
              <defs>
                <marker
                  id="gantt-arrow"
                  markerWidth="8"
                  markerHeight="8"
                  refX="7"
                  refY="4"
                  orient="auto"
                >
                  <path
                    d="M0,0 L8,4 L0,8"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="1.5"
                    className="text-muted-foreground"
                  />
                </marker>
              </defs>
              {/* Month headers */}
              {months.map((m) => {
                const x = dayToX(m.day, windowStart, DAY_W);
                return (
                  <g key={m.day}>
                    <rect
                      x={x}
                      y={0}
                      width={m.span * DAY_W}
                      height={28}
                      className="fill-muted/40"
                    />
                    <text
                      x={x + 8}
                      y={19}
                      className="fill-foreground text-xs font-medium"
                    >
                      {monthLabel(m.day)}
                    </text>
                  </g>
                );
              })}
              {/* Day headers + grid */}
              {days.map((d) => {
                const x = dayToX(d, windowStart, DAY_W);
                const isToday = d === todayKey;
                const dt = new Date(dayToTs(d) + 12 * 3600 * 1000);
                const weekend = dt.getUTCDay() === 0 || dt.getUTCDay() === 6;
                return (
                  <g key={d}>
                    {weekend && (
                      <rect
                        x={x}
                        y={HEADER_H}
                        width={DAY_W}
                        height={height - HEADER_H}
                        className="fill-muted/20"
                      />
                    )}
                    {isToday && (
                      <rect
                        x={x}
                        y={0}
                        width={DAY_W}
                        height={height}
                        className="fill-primary/10"
                      />
                    )}
                    <text
                      x={x + DAY_W / 2}
                      y={44}
                      textAnchor="middle"
                      className="fill-muted-foreground text-[10px]"
                    >
                      {d.slice(8, 10)}
                    </text>
                    <line
                      x1={x}
                      y1={HEADER_H}
                      x2={x}
                      y2={height}
                      className="stroke-border"
                      strokeWidth={1}
                    />
                  </g>
                );
              })}
              {/* Row backgrounds for group headers */}
              {rows.map((r, idx) => {
                if (r.kind !== "group" && r.kind !== "unscheduled-header")
                  return null;
                const y = HEADER_H + idx * ROW_H;
                return (
                  <rect
                    key={`bg-${idx}`}
                    x={0}
                    y={y}
                    width={width}
                    height={ROW_H}
                    className="fill-muted/40"
                  />
                );
              })}
              {/* Dependency arrows (under the bars) */}
              {arrows.map((a, i) => {
                const midX = (a.x1 + a.x2) / 2;
                return (
                  <path
                    key={i}
                    d={`M ${a.x1} ${a.y1} C ${midX} ${a.y1}, ${midX} ${a.y2}, ${a.x2 - 2} ${a.y2}`}
                    fill="none"
                    strokeWidth={1.5}
                    className="stroke-muted-foreground"
                    markerEnd="url(#gantt-arrow)"
                  />
                );
              })}
              {/* Bars */}
              {rows.map((r, idx) => {
                if (r.kind !== "issue") return null;
                const b = barSpan(r.issue.start_date, r.issue.target_date);
                if (!b.start || !b.end) return null;
                const y = HEADER_H + idx * ROW_H;
                // Clamp the bar to the visible window.
                const visStart = b.start < windowStart ? windowStart : b.start;
                const visEnd =
                  b.end >= addDays(days[days.length - 1], 1)
                    ? days[days.length - 1]
                    : b.end;
                if (visStart > visEnd) return null;
                let x = dayToX(visStart, windowStart, DAY_W);
                let w = (daysBetween(visStart, visEnd) + 1) * DAY_W - 4;
                // Live drag offset.
                if (drag?.id === r.issue.id) {
                  const delta = Math.round(drag.dx / DAY_W);
                  x = dayToX(addDays(visStart, delta), windowStart, DAY_W);
                }
                const color =
                  statesById.get(r.issue.state_id)?.color ?? "#6366f1";
                return (
                  <g key={r.issue.id}>
                    <rect
                      x={x + 2}
                      y={y + 9}
                      width={Math.max(w, 8)}
                      height={16}
                      rx={4}
                      fill={color}
                      fillOpacity={0.85}
                      className={cn(canEdit && "cursor-grab")}
                      onPointerDown={(e) =>
                        onBarPointerDown(e, r.issue, {
                          start: b.start!,
                          end: b.end!,
                        })
                      }
                      onClick={() => {
                        if (!drag && !suppressClick.current)
                          openPeek(r.issue.id);
                      }}
                    >
                      <title>{`${r.issue.display_id}: ${r.issue.name}`}</title>
                    </rect>
                  </g>
                );
              })}
            </svg>
          </div>
        </div>
      )}
      {patchMutation.isError && (
        <div className="text-sm text-destructive">
          Failed to reschedule — the timeline was refreshed.
        </div>
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
