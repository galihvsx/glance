import { useMemo, useRef, useState } from "react";
import { useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronLeft, ChevronRight, Link2 } from "lucide-react";
import PageHeader from "../components/shell/PageHeader";
import PqlEditor from "../components/issue/PqlEditor";
import SavedViewsMenu from "../components/issue/SavedViewsMenu";
import { EMPTY_FILTERS } from "../lib/filters";
import { formatPqlError } from "../lib/pql";
import PeekDrawer from "../components/issue/PeekDrawer";
import { usePeekParam } from "../components/issue/usePeek";
import { Button } from "../components/ui/button";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "../components/ui/alert-dialog";
import { Kbd } from "../components/ui/kbd";
import { toast } from "../components/ui/toast";
import { Alert, AlertDescription } from "../components/ui/alert";
import { api } from "../lib/api";
import { stateDotClass } from "../lib/stateDot";
import type {  Issue,
  IssueLink,
  IssueListResult,
  IssueState,
  Project,
  SubtaskRow,
  Workspace,
} from "../lib/types";
import { splitNestedIssues } from "../components/issue/splitNestedIssues";
import { useGanttSubWorkItems } from "../components/issue/useGanttSubWorkItems";
import {
  addDays,
  barSpan,
  daysBetween,
  dayToTs,
  dayToX,
  ganttDays,
  ganttMonths,
  xToDay,
} from "../lib/gantt";
import {
  cancelLinkDraw,
  closeLinkDelete,
  dropTargetAt,
  enterLinkMode,
  exitLinkMode,
  initialGanttLinkUiState,
  requestLinkDelete,
  resolveLinkDrop,
  startLinkDraw,
  useGanttLinkMutations,
} from "../components/gantt/ganttLinks";
import {
  computeCascade,
  type CascadeIssue,
} from "../components/gantt/ganttCascade";
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

/** 28px toolbar button in the C19 token ramp (matches PageHeader controls). */
const ganttControlClass =
  "inline-flex h-7 items-center gap-1.5 rounded-md border border-border bg-surface px-2 text-[11px] font-medium text-ink-secondary hover:bg-raised hover:text-ink";

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

/** Drag state (C109 move + C144 resize), shared by the commit path
 *  and the cascade ghost preview so both compute the prospective span
 *  from exactly the same math. C149T1: hasStart/hasTarget record
 *  which dates the issue ORIGINALLY had, captured at drag start
 *  from the Issue object — origStart/origEnd come from the filled
 *  barSpan, which fills the missing date, so presence cannot be
 *  recovered from them. The move commit uses presence to PATCH
 *  only the originally-present date(s). */
type BarDragState = {
  id: string;
  startX: number;
  origStart: string;
  origEnd: string;
  dx: number;
  mode: "move" | "resize-start" | "resize-end";
  hasStart: boolean;
  hasTarget: boolean;
};

/** C156T1: a schedule drag has no orig dates — the dragged issue
 *  is undated, so there is nothing to shift from. The drop day
 *  (the pointer's day, via xToDay) IS the new 1-day span; the
 *  commit PATCHes both dates to it. */
type ScheduleDragState = {
  id: string;
  startX: number;
  dx: number;
  mode: "schedule";
};

type DragState = BarDragState | ScheduleDragState;

/** Minimal issue shape the drag handlers read: id + stored dates.
 *  A full Issue satisfies this; a child bar passes its bridged
 *  barIssue (id + the SubtaskRow's own dates), never fabricated
 *  fields — the handlers never touch anything else on the Issue. */
type DragIssue = Pick<Issue, "id" | "start_date" | "target_date">;

/** Prospective span for a drag at the given whole-day delta — the
 *  same math `onPointerUp` commits (C144T1: resize moves only the
 *  dragged edge, min-1-day clamp in both directions). The ghost
 *  preview and the commit path both call this, so the preview never
 *  promises a cascade the commit would not make. */
function prospectiveSpan(
  drag: BarDragState,
  dayDelta: number,
): { start: string; end: string } {
  let start = addDays(drag.origStart, dayDelta);
  let end = addDays(drag.origEnd, dayDelta);
  if (drag.mode === "resize-start") {
    end = drag.origEnd;
    if (start > end) start = end;
  } else if (drag.mode === "resize-end") {
    start = drag.origStart;
    if (end < start) end = start;
  }
  return { start, end };
}

type Row =
  | { kind: "group"; group: string }
  | { kind: "issue"; issue: Issue }
  | { kind: "unscheduled-header" }
  | { kind: "issue-undated"; issue: Issue }
  // C145T3: a direct child nested under its parent row (one level,
  // sort_order ASC via splitNestedIssues). The row carries the
  // SubtaskRow, not a full Issue: its own bar comes from the row's
  // start_date/target_date (C145T1). C150T1: child bars host the
  // move drag (same handlers as parents); C150T2: they host edge
  // resize handles too; C151T1: they host the link-draw handle
  // and draw press too (same gates, same geometry as parents).
  | { kind: "issue-child"; parentId: string; row: SubtaskRow };

export default function Gantt() {
  const { slug = "", identifier = "" } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;
  // C110T2: same ["project", slug, identifier] query Cycles/EpicDetail
  // use — the detail-mode SavedViewsMenu below needs a project UUID,
  // and the shared key is cache-warm whenever the user came from
  // another project page.
  const projectQuery = useQuery({
    queryKey: ["project", slug, identifier],
    queryFn: () => api.get<Project>(base),
  });
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

  // C109T2: PQL mode on the Gantt view (Cycles detail pattern). PQL
  // mode changes ONLY which issues render: the same three window
  // queries run with &pql= appended, the window navigation below
  // keeps the applied query (windowStart is in every key), and
  // leaving PQL mode clears the query so the unfiltered sets —
  // already cached under the visual keys — come back at once.
  // Query keys carry the mode + applied query, so PQL and visual
  // caches never cross-contaminate. A LIMIT query needs no
  // special-casing: the server returns next_cursor null, so the
  // fetchAllIssues cursor loop stops after one page.
  const [pqlMode, setPqlMode] = useState(false);
  const [appliedQuery, setAppliedQuery] = useState("");
  // C110T2: a saved view whose query fails its probe has no editor
  // inline error to land in — the editor only reports failures from
  // its own Apply button, and it mounts fresh (empty draft) for a
  // menu apply. The failure is surfaced here, at the detail level,
  // instead of vanishing or rejecting unhandled (Cycles C107T1).
  const [savedViewError, setSavedViewError] = useState<string | null>(null);
  const activePql = pqlMode ? appliedQuery.trim() : "";
  const pqlSuffix = activePql ? `&pql=${encodeURIComponent(activePql)}` : "";
  const pqlKey = pqlMode ? "pql" : "all";

  // C145T3: view-local "Show sub-work items" toggle (own
  // localStorage key, default true — see useGanttSubWorkItems).
  // On: the window queries below carry include_subtasks=1 and the
  // rows nest direct children under their parent. Off: today's flat
  // fetch and flat rows, byte-identical behavior.
  const { showSubWorkItems, toggle: toggleShowSubWorkItems } =
    useGanttSubWorkItems(slug, identifier);
  const subtaskSuffix = showSubWorkItems ? "&include_subtasks=1" : "";

  // Probe-first apply (Cycles pattern): fetch one page of each
  // window query with the candidate query and commit only on
  // success. A 400 rejects, so PqlEditor shows it inline and the
  // displayed bars are untouched (appliedQuery — and therefore
  // every query key — never changes).
  async function handlePqlApply(query: string): Promise<void> {
    const q = query.trim();
    if (!q) {
      setAppliedQuery("");
      return;
    }
    const probe = (params: Record<string, string>) => {
      const p = new URLSearchParams({
        ...params,
        pql: q,
        per_page: "100",
        include_links: "1",
      });
      // C145T3: the probe is part of the PQL fetch path, so it
      // carries the flag exactly like the window queries below.
      if (showSubWorkItems) p.set("include_subtasks", "1");
      return api.get(`${base}/issues?${p.toString()}`);
    };
    await Promise.all([
      probe({ due_after: rfc(windowStart), due_before: rfc(windowEnd) }),
      probe({ start_after: rfc(windowStart), start_before: rfc(windowEnd) }),
      probe({ undated: "1" }),
    ]);
    setAppliedQuery(q);
    setSavedViewError(null);
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
  // C110T2: applying a saved PQL view from the detail-mode menu,
  // copying the Cycles handleSavedViewApplied shape verbatim.
  // Probe FIRST, then enter PQL mode: the probe commits appliedQuery
  // while still in visual mode (the bars are unaffected until
  // pqlMode flips), so the editor mounts seeded with the applied
  // query. A 400 leaves appliedQuery untouched — the bars never
  // change — and the error surfaces in the detail-level alert (the
  // editor cannot show it: it mounts fresh with an empty draft).
  // The menu ignores this handler's return value, so the catch here
  // is load-bearing: without it the rejected probe would be an
  // unhandled rejection. Null is unreachable from the detail-mode
  // menu (PQL views only) and mirrors Cycles: leave PQL mode.
  // C110 window rule: a view apply NEVER touches the year/month
  // window state below (year/month are not read or written here).
  // A saved query ANDs with the current window (C109 semantics) —
  // it never moves it. A view naming dates outside the current
  // window therefore shows an empty view until the user moves the
  // window; that is the honest AND, not a bug, and must not be
  // "fixed" into a window jump later.
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

  // Three bounded queries (same pattern as the calendar view), each with
  // ?include_links=1 so dependency edges arrive in one shot — no N+1.
  // In PQL mode each also carries &pql=, so the filtered set carries
  // its edges; an edge to an issue outside the filtered set simply
  // does not render, same as today's out-of-window endpoint behavior.
  const dueQuery = useQuery({
    queryKey: ["gantt", slug, identifier, "due", windowStart, pqlKey, activePql, showSubWorkItems],
    queryFn: () =>
      fetchAllIssues(
        `${base}/issues?due_after=${rfc(windowStart)}&due_before=${rfc(windowEnd)}${pqlSuffix}${subtaskSuffix}`,
      ),
  });
  const startQuery = useQuery({
    queryKey: ["gantt", slug, identifier, "start", windowStart, pqlKey, activePql, showSubWorkItems],
    queryFn: () =>
      fetchAllIssues(
        `${base}/issues?start_after=${rfc(windowStart)}&start_before=${rfc(windowEnd)}${pqlSuffix}${subtaskSuffix}`,
      ),
  });
  const undatedQuery = useQuery({
    queryKey: ["gantt", slug, identifier, "undated", pqlKey, activePql, showSubWorkItems],
    queryFn: () => fetchAllIssues(`${base}/issues?undated=1${pqlSuffix}${subtaskSuffix}`),
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

  // Dependency editing (C15T0): optimistic create/delete of `blocks`
  // edges over the merged server links. The peek drawer already carries
  // the cycle-8 linked-issues panel, which is the keyboard path —
  // every gutter row is a native button that opens it.
  const {
    links: visLinks,
    createMutation: createLinkMutation,
    deleteMutation: deleteLinkMutation,
  } = useGanttLinkMutations(slug, identifier, links);
  const [linkUi, setLinkUi] = useState(initialGanttLinkUiState);
  // Cursor position while drawing an edge (svg coordinates).
  const [drawPos, setDrawPos] = useState<{ x: number; y: number } | null>(
    null,
  );

  const issueById = useMemo(() => new Map(issues.map((i) => [i.id, i])), [
    issues,
  ]);

  // C150T1: cascade input = the fetched top-level set PLUS one
  // CascadeIssue per subtask row. Subtasks ride inside
  // parent.subtasks, never in `issues`, so without this flattening
  // computeCascade would miss a child drag source entirely (its
  // byId.get miss returns []) and a child dependent would be
  // skipped as outside the fetched set (rule 6) — even though the
  // child IS visible as a nested row. A child that is ALSO a
  // top-level row (its own dates in the window) is deduped: the
  // top-level entry wins, same id, same stored dates. Used by
  // BOTH the ghost preview and the commit path so they agree.
  const cascadeIssues = useMemo<CascadeIssue[]>(() => {
    const seen = new Set(issues.map((i) => i.id));
    const out: CascadeIssue[] = [...issues];
    for (const i of issues) {
      for (const s of i.subtasks ?? []) {
        if (seen.has(s.id)) continue;
        seen.add(s.id);
        out.push({
          id: s.id,
          start_date: s.start_date ?? null,
          target_date: s.target_date ?? null,
        });
      }
    }
    return out;
  }, [issues]);

  // C150T1: presence lookup for rule-7 shift PATCHes, over the
  // same flattened input: a shifted child dependent keeps its
  // single-date shape, resolved from its SubtaskRow dates — the
  // top-level-only issueById.get would miss and silently drop it.
  const cascadeIssueById = useMemo(
    () => new Map(cascadeIssues.map((i) => [i.id, i])),
    [cascadeIssues],
  );

  const statesById = useMemo(() => {
    const m = new Map<string, IssueState>();
    for (const s of statesQuery.data ?? []) m.set(s.id, s);
    return m;
  }, [statesQuery.data]);

  // C145T3: nesting split, reused verbatim from list/board
  // (splitNestedIssues) — toggle off returns today's flat layout
  // untouched; a child whose parent is NOT in the fetched set stays
  // a flat row (orphan rule: a child NEVER vanishes); direct
  // children only, one level, sort_order ASC. A child travels with
  // its parent: it nests under the parent's row in the parent's
  // group (dated) or lane (unscheduled), never in its own place.
  const { visibleIssues, nestedByParent } = useMemo(
    () => splitNestedIssues(issues, showSubWorkItems),
    [issues, showSubWorkItems],
  );

  // Rows: issues grouped by state group (dated), then the unscheduled lane.
  const rows = useMemo<Row[]>(() => {
    const dated: Issue[] = [];
    const undated: Issue[] = [];
    for (const i of visibleIssues) {
      const b = barSpan(i.start_date, i.target_date);
      (b.start ? dated : undated).push(i);
    }
    const byGroup = new Map<string, Issue[]>();
    for (const i of dated) {
      const g = statesById.get(i.state_id)?.group ?? "backlog";
      if (!byGroup.has(g)) byGroup.set(g, []);
      byGroup.get(g)!.push(i);
    }
    // Each parent row is immediately followed by its nested child
    // rows (sort_order ASC, from the split) in the same section.
    const pushWithChildren = (out: Row[], i: Issue, kind: "issue" | "issue-undated") => {
      out.push(kind === "issue" ? { kind, issue: i } : { kind, issue: i });
      for (const child of nestedByParent.get(i.id) ?? []) {
        out.push({ kind: "issue-child", parentId: i.id, row: child });
      }
    };
    const out: Row[] = [];
    for (const g of GROUP_ORDER) {
      const list = byGroup.get(g);
      if (!list?.length) continue;
      list.sort((a, b) => a.sequence_id - b.sequence_id);
      out.push({ kind: "group", group: g });
      for (const i of list) pushWithChildren(out, i, "issue");
    }
    // Any dated issues whose state group is unknown still get shown.
    for (const [g, list] of byGroup) {
      if (GROUP_ORDER.includes(g)) continue;
      list.sort((a, b) => a.sequence_id - b.sequence_id);
      out.push({ kind: "group", group: g });
      for (const i of list) pushWithChildren(out, i, "issue");
    }
    if (undated.length) {
      undated.sort((a, b) => a.sequence_id - b.sequence_id);
      out.push({ kind: "unscheduled-header" });
      for (const i of undated) pushWithChildren(out, i, "issue-undated");
    }
    return out;
  }, [visibleIssues, nestedByParent, statesById]);

  // C146T2: row geometry for cascade ghosts, covering top-level
  // bars AND nested child bars (children can be dependents even
  // though they are never drag sources). Kept as its own memo
  // because it carries state/display/name fields the link-endpoint
  // lookup does not need.
  const ghostRows = useMemo(() => {
    const m = new Map<
      string,
      { y: number; stateId: string | null; displayId: string; name: string }
    >();
    rows.forEach((r, idx) => {
      const y = HEADER_H + idx * ROW_H;
      if (r.kind === "issue" || r.kind === "issue-undated") {
        m.set(r.issue.id, {
          y,
          stateId: r.issue.state_id,
          displayId: r.issue.display_id,
          name: r.issue.name,
        });
      } else if (r.kind === "issue-child") {
        m.set(r.row.id, {
          y,
          stateId: r.row.state_id,
          displayId: r.row.display_id,
          name: r.row.title,
        });
      }
    });
    return m;
  }, [rows]);

  // C151T1: unified link-endpoint lookup — top-level issue OR child
  // row, span from stored dates, y from the row index (the same
  // geometry ghostRows already computes for children). The draw
  // preview resolves its source through this lookup; T2 reuses it
  // for the arrows memo — do not build a second lookup. First
  // occurrence wins: a parent row precedes its child rows, so a
  // duplicated id resolves to its top-level row, matching the
  // cascade dedup rule.
  const linkEndpoints = useMemo(() => {
    const m = new Map<
      string,
      { startDate: string | null; targetDate: string | null; y: number }
    >();
    rows.forEach((r, idx) => {
      const y = HEADER_H + idx * ROW_H;
      if (r.kind === "issue" || r.kind === "issue-undated") {
        if (!m.has(r.issue.id))
          m.set(r.issue.id, {
            startDate: r.issue.start_date ?? null,
            targetDate: r.issue.target_date ?? null,
            y,
          });
      } else if (r.kind === "issue-child") {
        if (!m.has(r.row.id))
          m.set(r.row.id, {
            startDate: r.row.start_date ?? null,
            targetDate: r.row.target_date ?? null,
            y,
          });
      }
    });
    return m;
  }, [rows]);

  // Dependency arrows: A blocks B (issue_id → target_issue_id) draws from
  // the blocker's bar right edge to the blocked issue's bar left edge.
  // Rendered over visLinks (server + optimistic), each carrying its link
  // so a click can open the delete confirm. Only "blocks" edges are
  // drawn — other kinds are managed in the issue detail panel.
  // C151T2: endpoints resolve through the T1 unified linkEndpoints
  // lookup (top-level issue OR child row: span from stored dates, y
  // from the row index), so parent→child, child→parent, and
  // child→child (payload permitting) edges draw with the same parent
  // geometry. A dateless endpoint has no bar and renders no arrow,
  // exactly as for dateless parents. No second lookup is built.
  const arrows = useMemo(() => {
    const out: {
      id: string;
      link: IssueLink;
      x1: number;
      y1: number;
      x2: number;
      y2: number;
    }[] = [];
    for (const l of visLinks) {
      if (l.kind !== "blocks") continue;
      const from = linkEndpoints.get(l.issue_id);
      const to = linkEndpoints.get(l.target_issue_id);
      if (!from || !to) continue;
      const fb = barSpan(from.startDate, from.targetDate);
      const tb = barSpan(to.startDate, to.targetDate);
      if (!fb.start || !fb.end || !tb.start) continue;
      const x1 = dayToX(fb.end, windowStart, DAY_W) + DAY_W;
      const x2 = dayToX(tb.start, windowStart, DAY_W);
      const y1 = from.y + ROW_H / 2;
      const y2 = to.y + ROW_H / 2;
      out.push({ id: l.id, link: l, x1, y1, x2, y2 });
    }
    return out;
  }, [visLinks, linkEndpoints, windowStart]);

  // Rows as drop targets for edge drawing (svg coordinates).
  // C151T1: issue-child rows are included (same y math) — without
  // them a draw released over a child row resolves to nothing, so
  // a self-drop on the same child bar could never reach the
  // resolveLinkDrop "self" toast the T1 spec requires. This also
  // makes drop-onto-child creates resolve; their tests belong to
  // T2, which extends the T1 suite for exactly that. Undated
  // rows are drop targets too, mirroring the parent rule.
  const dropRows = useMemo(() => {
    const out: { id: string; y: number }[] = [];
    rows.forEach((r, idx) => {
      if (r.kind === "issue" || r.kind === "issue-undated") {
        out.push({ id: r.issue.id, y: HEADER_H + idx * ROW_H });
      } else if (r.kind === "issue-child") {
        out.push({ id: r.row.id, y: HEADER_H + idx * ROW_H });
      }
    });
    return out;
  }, [rows]);

  // Drag-to-reschedule (C109T2) + drag-to-resize (C144T1).
  // mode "move" shifts both dates; "resize-start"/"resize-end"
  // move only the dragged edge, the other date passing through
  // unchanged. Move behavior is byte-identical to C109.
  const [drag, setDrag] = useState<DragState | null>(null);
  const svgRef = useRef<SVGSVGElement>(null);
  const suppressClick = useRef(false);

  // C146T2: live cascade ghost preview. During a drag, the same
  // engine the commit path uses runs over the prospective span, so
  // the ghosts show exactly what committing would shift. Pure
  // rendering: no PATCH, no cache writes. Empty whenever the drag
  // would not cascade (delta 0, move-earlier, resize-start, or no
  // violating dependent), so a no-cascade drag previews nothing.
  const previewCascade = useMemo(() => {
    if (!drag) return [];
    // C156T1: a schedule drag has no cascade ghosts — computeCascade
    // early-returns [] for a dateless dragged issue (no old end to
    // compute k from), so the ghost is the 1-day bar only.
    // Cascade-on-schedule is a carried follow-up (cycle 156 plan,
    // decision 5), not bolted on here.
    if (drag.mode === "schedule") return [];
    const span = prospectiveSpan(drag, Math.round(drag.dx / DAY_W));
    return computeCascade(
      cascadeIssues,
      links,
      drag.id,
      span.start,
      span.end,
      drag.mode,
    );
  }, [drag, cascadeIssues, links]);

  const patchMutation = useMutation({
    // C149T1: start/end are optional — the mutation sends exactly
    // the keys given. An absent key leaves that date untouched
    // server-side; an explicit null would CLEAR it, so nulls are
    // never sent. Move commits pass only the originally-present
    // date(s); resize commits pass both.
    mutationFn: ({
      id,
      start,
      end,
    }: {
      id: string;
      start?: string;
      end?: string;
    }) => {
      const body: { start_date?: string; target_date?: string } = {};
      if (start !== undefined) body.start_date = start;
      if (end !== undefined) body.target_date = end;
      return api.patch<Issue>(`${base}/issues/${id}`, body);
    },
    onSuccess: () => {
      // C109T2: the drag targets the issue, not the query, so it
      // works on visible bars in PQL mode too. The ["gantt"]
      // prefix covers BOTH the visual keys (..."all",...) and the
      // PQL keys (..."pql", query), so neither shows a stale bar.
      void queryClient.invalidateQueries({ queryKey: ["gantt"] });
    },
  });

  const svgX = (clientX: number) => {
    const rect = svgRef.current?.getBoundingClientRect();
    return rect ? clientX - rect.left : 0;
  };
  const svgY = (clientY: number) => {
    const rect = svgRef.current?.getBoundingClientRect();
    return rect ? clientY - rect.top : 0;
  };

  const onBarPointerDown = (
    e: React.PointerEvent,
    issue: DragIssue,
    b: { start: string; end: string },
  ) => {
    if (!canEdit || e.button !== 0) return;
    // Link mode: a press on a bar starts an edge draw instead of a move.
    if (linkUi.linkMode) {
      e.preventDefault();
      setLinkUi(startLinkDraw(linkUi, issue.id));
      setDrawPos({ x: svgX(e.clientX), y: svgY(e.clientY) });
      return;
    }
    e.preventDefault();
    setDrag({
      id: issue.id,
      startX: svgX(e.clientX),
      origStart: b.start,
      origEnd: b.end,
      dx: 0,
      mode: "move",
      // C149T1: presence from the Issue, NOT the filled span b
      // (barSpan fills the missing date, so b always has both).
      hasStart: Boolean(issue.start_date),
      hasTarget: Boolean(issue.target_date),
    });
  };

  // C156T1: press on a top-level unscheduled label button starts a
  // schedule drag (left button + canEdit only). No orig dates: the
  // issue is undated. Link mode never competes — the row has no bar
  // to draw from — so no linkMode gate. Child rows (T2) do not get
  // this source yet; the handler is wired only to issue-undated
  // label buttons below.
  const onUnscheduledPointerDown = (e: React.PointerEvent, issue: DragIssue) => {
    if (!canEdit || e.button !== 0) return;
    e.preventDefault();
    setDrag({
      id: issue.id,
      startX: svgX(e.clientX),
      dx: 0,
      mode: "schedule",
    });
  };

  // C144T1: edge-handle press starts a resize drag. Handles are
  // siblings of the bar rect (never ancestors), so a handle press
  // cannot reach onBarPointerDown; handles render only when
  // canEdit && !linkMode, so in link mode a press still starts an
  // edge draw exactly as today.
  const onResizePointerDown = (
    e: React.PointerEvent,
    issue: DragIssue,
    b: { start: string; end: string },
    mode: "resize-start" | "resize-end",
  ) => {
    if (!canEdit || linkUi.linkMode || e.button !== 0) return;
    e.preventDefault();
    setDrag({
      id: issue.id,
      startX: svgX(e.clientX),
      origStart: b.start,
      origEnd: b.end,
      dx: 0,
      mode,
      // C149T1: captured uniformly with move drags (resize commits
      // always send both dates — a moved edge creates the range —
      // but the state stays honest about the original shape).
      hasStart: Boolean(issue.start_date),
      hasTarget: Boolean(issue.target_date),
    });
  };

  const onPointerMove = (e: React.PointerEvent) => {
    if (linkUi.drawingFrom) {
      setDrawPos({ x: svgX(e.clientX), y: svgY(e.clientY) });
      return;
    }
    if (!drag) return;
    setDrag({ ...drag, dx: svgX(e.clientX) - drag.startX });
  };

  const onPointerUp = (e: React.PointerEvent) => {
    // Finish an edge draw: resolve the drop against the dated rows.
    if (linkUi.drawingFrom) {
      const fromId = linkUi.drawingFrom;
      const toId = dropTargetAt(dropRows, svgY(e.clientY), ROW_H);
      setLinkUi(cancelLinkDraw(linkUi));
      setDrawPos(null);
      const resolution = resolveLinkDrop(fromId, toId);
      if (resolution === "create") {
        suppressClick.current = true;
        setTimeout(() => {
          suppressClick.current = false;
        }, 0);
        createLinkMutation.mutate({ fromId, toId: toId! });
      } else if (resolution === "self") {
        toast.add({
          title: "An issue can't depend on itself",
          type: "info",
        });
      }
      return;
    }
    if (!drag) return;
    // C156T1: schedule drag — the drop day is the pointer's day
    // (xToDay clamps into the window). A release outside the
    // timeline svg (label column, toolbar, …) is a no-op: only a
    // release over the svg schedules. The commit PATCHes the single
    // issue with both dates = the drop day. Cascade-on-schedule is
    // NOT wired here: computeCascade early-returns [] for a dateless
    // dragged issue (no old end to compute k from), so the move
    // cascade path is move-only — the gap is carried (cycle 156
    // plan, decision 5), not re-implemented.
    if (drag.mode === "schedule") {
      const x = svgX(e.clientX);
      const overTimeline = svgRef.current?.contains(e.target as Node) ?? false;
      const moved = drag.dx !== 0;
      setDrag(null);
      if (moved) {
        // Suppress the click that follows a real drag (it would
        // open the peek) — same as the bar commit path below.
        suppressClick.current = true;
        setTimeout(() => {
          suppressClick.current = false;
        }, 0);
      }
      if (!overTimeline || x < 0) return;
      const day = xToDay(x, windowStart, DAY_W, days.length);
      patchMutation.mutate({ id: drag.id, start: day, end: day });
      return;
    }
    const dayDelta = Math.round(drag.dx / DAY_W);
    setDrag(null);
    if (dayDelta === 0) return;
    // C144T1: resize moves only the dragged edge; whole-day steps
    // and a min-1-day bar (Plane's rule, binding). Start dragged
    // past the target clamps to the target; target dragged before
    // the start clamps to the start. A clamped resize that lands
    // back on the original dates sends no PATCH (same as zero
    // delta: no suppressClick either). Shared with the ghost
    // preview via prospectiveSpan so preview and commit agree.
    const { start, end } = prospectiveSpan(drag, dayDelta);
    if (start === drag.origStart && end === drag.origEnd) return;
    // Suppress the click that follows a real drag (it would open peek).
    suppressClick.current = true;
    setTimeout(() => {
      suppressClick.current = false;
    }, 0);
    // C146T2: dependency cascade. The engine runs over the fetched
    // issues + links with the prospective span; resize-start yields
    // [] by construction but is still called uniformly (no
    // special-casing). Empty cascade ⇒ today's single PATCH,
    // byte-identical. Otherwise the dragged PATCH and one
    // presence-preserving PATCH per shift fire together; full
    // success toasts the count, any failure toasts an error, and
    // either way ["gantt"] invalidates to resync (rule 8).
    // C149T1: a MOVE PATCHes only the dates the dragged issue
    // originally had (drag.hasStart/hasTarget, captured at drag
    // start from the Issue). Sending the filled missing date would
    // silently convert a single-date issue into a ranged one — the
    // bar renders identically either way, so nothing would look
    // wrong. A resize that moves an edge necessarily creates a
    // range, so it keeps sending both dates. Neither-present is
    // unreachable for a bar drag (a bar needs at least one date);
    // it falls back to both rather than sending an empty PATCH.
    const draggedDates =
      drag.mode === "move" && (drag.hasStart || drag.hasTarget)
        ? {
            ...(drag.hasStart ? { start } : {}),
            ...(drag.hasTarget ? { end } : {}),
          }
        : { start, end };
    const shifts = computeCascade(
      cascadeIssues,
      links,
      drag.id,
      start,
      end,
      drag.mode,
    );
    if (shifts.length === 0) {
      patchMutation.mutate({
        id: drag.id,
        ...draggedDates,
      });
      return;
    }
    // Rule 7: a shift PATCH sends start_date only if the issue
    // originally had one, target_date only if it originally had
    // one — a single-date dependent stays single-date. The engine
    // returns ids from the flattened `cascadeIssues`, so a child
    // dependent resolves via its SubtaskRow dates (C150T1); a
    // missing original is unreachable and is skipped defensively
    // rather than patched with a guessed shape.
    const cascadePatches = shifts.flatMap((shift) => {
      const original =
        issueById.get(shift.id) ?? cascadeIssueById.get(shift.id);
      if (!original) return [];
      const body: { start_date?: string; target_date?: string } = {};
      if (original.start_date) body.start_date = shift.start_date;
      if (original.target_date) body.target_date = shift.target_date;
      return [{ id: shift.id, body }];
    });
    const draggedId = drag.id;
    void (async () => {
      const results = await Promise.allSettled([
        // C149T1: same presence rule as the single-PATCH path above
        // — a moved single-date blocker stays single-date even when
        // it cascades its dependents (computeCascade still receives
        // the filled span, unchanged).
        api.patch<Issue>(`${base}/issues/${draggedId}`, {
          ...(draggedDates.start !== undefined
            ? { start_date: draggedDates.start }
            : {}),
          ...(draggedDates.end !== undefined
            ? { target_date: draggedDates.end }
            : {}),
        }),
        ...cascadePatches.map((p) =>
          api.patch<Issue>(`${base}/issues/${p.id}`, p.body),
        ),
      ]);
      const failed = results.some((r) => r.status === "rejected");
      if (!failed) {
        toast.add({
          title: `Shifted ${cascadePatches.length} dependent ${cascadePatches.length === 1 ? "issue" : "issues"}`,
          type: "success",
        });
      } else {
        toast.add({
          title:
            "Some dependent issues failed to shift — the timeline was refreshed.",
          type: "error",
        });
      }
      void queryClient.invalidateQueries({ queryKey: ["gantt"] });
    })();
  };

  // C156T1: a schedule drag released anywhere outside the timeline
  // svg (label column, toolbar, …) is a no-op — clear it here so it
  // can never commit on a later svg pointerup. This runs on pointerup
  // bubbling to the page container; the svg's own onPointerUp commits
  // first for timeline releases, and this handler is a no-op for
  // those (target inside the svg) and for non-schedule drags. It
  // never commits — the svg handler owns the commit — so the stale
  // closure on the bubbled event cannot double-PATCH.
  const onPagePointerUp = (e: React.PointerEvent) => {
    if (drag?.mode !== "schedule") return;
    if (svgRef.current?.contains(e.target as Node)) return;
    const moved = drag.dx !== 0;
    setDrag(null);
    if (moved) {
      suppressClick.current = true;
      setTimeout(() => {
        suppressClick.current = false;
      }, 0);
    }
  };

  // C156T1: pointer-cancel aborts a schedule drag with no PATCH
  // (same handler covers the svg via bubbling; bar drags keep
  // today's behavior).
  const onPagePointerCancel = () => {
    if (drag?.mode === "schedule") setDrag(null);
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
    <>
      {/* C19T4: shared page header replaces the per-page ProjectNav. */}
      <PageHeader viewName="Timeline" showViews />
      <div
      className="flex h-full flex-col gap-3 p-4"
      onPointerUp={onPagePointerUp}
      onPointerCancel={onPagePointerCancel}
      onKeyDown={(e) => {
        if (e.key !== "Escape") return;
        if (linkUi.drawingFrom) {
          setLinkUi(cancelLinkDraw(linkUi));
          setDrawPos(null);
        } else if (linkUi.linkMode) {
          setLinkUi(exitLinkMode(linkUi));
        } else if (drag?.mode === "schedule") {
          // C156T1: Escape aborts a schedule drag with no PATCH.
          setDrag(null);
        }
      }}
    >
        <div className="flex items-center gap-1.5">
          <button
            type="button"
            onClick={() => go(-1)}
            className={cn(ganttControlClass, "px-1.5")}
            aria-label="Previous month"
          >
            <ChevronLeft className="h-3.5 w-3.5" />
          </button>
          <button
            type="button"
            onClick={() => {
              const t = new Date();
              setYear(t.getFullYear());
              setMonth(t.getMonth());
            }}
            className={ganttControlClass}
          >
            Today
          </button>
          <button
            type="button"
            onClick={() => go(1)}
            className={cn(ganttControlClass, "px-1.5")}
            aria-label="Next month"
          >
            <ChevronRight className="h-3.5 w-3.5" />
          </button>
          <span className="ml-1 text-[13px] font-semibold text-ink">
            {monthLabel(days[0])} – {monthLabel(days[days.length - 1])}
          </span>
          {/* C145T3: view-local "Show sub-work items" toggle, default
              on; off restores today's flat rows. */}
          <label
            className={cn(ganttControlClass, "ml-1 cursor-pointer")}
          >
            <input
              type="checkbox"
              checked={showSubWorkItems}
              onChange={toggleShowSubWorkItems}
              aria-label="Show sub-work items"
            />
            Show sub-work items
          </label>
          {canEdit && (
            <Button
              type="button"
              variant={linkUi.linkMode ? "secondary" : "outline"}
              size="sm"
              aria-pressed={linkUi.linkMode}
              onClick={() =>
                setLinkUi(
                  linkUi.linkMode ? exitLinkMode(linkUi) : enterLinkMode(linkUi),
                )
              }
              title="Draw dependency edges between issue bars"
              className="ml-1 h-7 gap-1.5 rounded-md border-border bg-surface text-[11px] text-ink-secondary hover:bg-raised hover:text-ink"
            >
              <Link2 className="h-3.5 w-3.5" />
              {linkUi.linkMode ? "Linking…" : "Add dependencies"}
            </Button>
          )}
          {!canEdit && (
            <span className="text-[11px] text-ink-muted">
              Guest access — timeline is read-only.
            </span>
          )}
        </div>
        {/* C109T2: All-issues / PQL toggle + editor, above the
            timeline (Cycles detail pattern). C110T2: detail
            toolbar — the All-issues / PQL toggle plus the
            detail-mode SavedViewsMenu beside it. Detail mode
            guarantees no URL writes and no default auto-apply, so
            the page settles PQL state through onViewApplied only.
            A view apply never touches the year/month window state:
            the saved query ANDs with the current window (C109
            semantics), it never moves it (see
            handleSavedViewApplied). The editor is keyed by the
            applied query: PqlEditor's draft is initial-value-only,
            so without the key a re-apply would leave a stale draft
            (C107 lesson). */}
        <div className="flex items-center gap-2">
          <div
            role="group"
            aria-label="Issue list mode"
            className="flex flex-1 rounded-md border p-0.5"
          >
            <button
              type="button"
              aria-pressed={!pqlMode}
              onClick={() => handlePqlModeChange("all")}
              className={`h-7 flex-1 rounded-sm text-xs font-medium ${
                !pqlMode ? "bg-primary text-primary-foreground" : ""
              }`}
            >
              All issues
            </button>
            <button
              type="button"
              aria-pressed={pqlMode}
              onClick={() => handlePqlModeChange("pql")}
              className={`h-7 flex-1 rounded-sm text-xs font-medium ${
                pqlMode ? "bg-primary text-primary-foreground" : ""
              }`}
            >
              PQL
            </button>
          </div>
          <SavedViewsMenu
            projectId={projectQuery.data?.id ?? ""}
            filters={EMPTY_FILTERS}
            display={null}
            onApplyDisplay={null}
            detailMode
            pqlMode={pqlMode}
            pqlQuery={appliedQuery}
            onViewApplied={handleSavedViewApplied}
          />
        </div>
        {savedViewError && (
          <Alert variant="destructive">
            <AlertDescription>{savedViewError}</AlertDescription>
          </Alert>
        )}
        {pqlMode && (
          <PqlEditor
            key={appliedQuery}
            appliedQuery={appliedQuery}
            onApply={handlePqlApply}
            onClear={() => {
              setAppliedQuery("");
              setSavedViewError(null);
            }}
            showSaveHint={true}
            saveHintText="To save this query, use Views → Save current view next to the All issues / PQL toggle; it re-applies when the view is opened."
          />
        )}
        {linkUi.linkMode && canEdit && (
          <div className="flex items-center gap-2 text-[11px] text-ink-muted">
            <span>
              Link mode: drag from one issue bar to another to create a{" "}
              <span className="font-medium text-ink">blocks</span>{" "}
              dependency. Click an existing edge to remove it.
            </span>
            <span className="flex items-center gap-1">
              <Kbd>Esc</Kbd> to exit
            </span>
          </div>
        )}

        {loading && (
          <div className="text-[13px] text-ink-muted">Loading timeline…</div>
        )}
        {error && (
          <div className="text-[13px] text-destructive">
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
          <div className="rounded-md border border-dashed border-border bg-surface p-8 text-center text-[13px] text-ink-muted">
            No issues with dates in this window. Set start or due dates on
            issues to see them on the timeline.
          </div>
        )}
        {!loading && !error && rows.length > 0 && (
          <div className="overflow-auto rounded-md border border-border">
            <div className="flex" style={{ width: GUTTER_W + width }}>
              {/* Left gutter: sticky issue labels */}
              <div
                className="sticky left-0 z-10 shrink-0 border-r border-border bg-canvas"
                style={{ width: GUTTER_W }}
              >
                <div
                  className="border-b border-border"
                  style={{ height: HEADER_H }}
                />
                {rows.map((r) => {
                  if (r.kind === "group") {
                    return (
                      <div
                        key={`g-${r.group}`}
                        className="flex items-center gap-1.5 border-b border-border-hairline bg-raised px-3 text-[10px] font-semibold uppercase tracking-[0.06em] text-ink-muted"
                        style={{ height: ROW_H }}
                      >
                        <span
                          className={cn("state-dot", stateDotClass(r.group))}
                          aria-hidden
                        />
                        {r.group}
                      </div>
                    );
                  }
                  if (r.kind === "unscheduled-header") {
                    return (
                      <div
                        key="unsched"
                        className="flex items-center border-b border-border-hairline bg-raised px-3 text-[10px] font-semibold uppercase tracking-[0.06em] text-ink-muted"
                        style={{ height: ROW_H }}
                      >
                        Unscheduled
                      </div>
                    );
                  }
                  // C145T3: nested child row, indented under its
                  // parent; click opens the same peek as a parent row.
                  if (r.kind === "issue-child") {
                    return (
                      <button
                        key={`child-${r.parentId}-${r.row.id}`}
                        type="button"
                        onClick={() => openPeek(r.row.id)}
                        className="flex w-full items-center gap-1.5 border-b border-border-hairline px-3 text-left text-[13px] text-ink-secondary hover:bg-wash-hover hover:text-ink"
                        style={{ height: ROW_H, paddingLeft: 28 }}
                        title={`${r.row.display_id}: ${r.row.title}`}
                      >
                        <span className="shrink-0 font-mono text-[10px] text-ink-muted">
                          {r.row.display_id}
                        </span>
                        <span className="truncate">{r.row.title}</span>
                      </button>
                    );
                  }
                  return (
                    <button
                      key={r.issue.id}
                      type="button"
                      onClick={() => {
                        // C156T1: a real schedule drag must not open
                        // the peek on release — same guard the bar
                        // rects carry. A plain click (no movement)
                        // still opens it.
                        if (!suppressClick.current) openPeek(r.issue.id);
                      }}
                      onPointerDown={
                        // C156T1: top-level unscheduled rows only
                        // (T2 wires the nested child rows). Dated
                        // rows keep today's press behavior.
                        r.kind === "issue-undated"
                          ? (e) => onUnscheduledPointerDown(e, r.issue)
                          : undefined
                      }
                      className="flex w-full items-center gap-1.5 border-b border-border-hairline px-3 text-left text-[13px] text-ink-secondary hover:bg-wash-hover hover:text-ink"
                      style={{ height: ROW_H }}
                      title={`${r.issue.display_id}: ${r.issue.name}`}
                    >
                      <span className="shrink-0 font-mono text-[10px] text-ink-muted">
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
                className={cn(
                  drag && "cursor-grabbing select-none",
                  linkUi.linkMode && "cursor-crosshair select-none",
                )}
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
                        className="fill-raised"
                      />
                      <text
                        x={x + 8}
                        y={19}
                        className="fill-ink text-[11px] font-medium"
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
                          className="fill-wash-hover"
                        />
                      )}
                      {isToday && (
                        <>
                          <rect
                            x={x}
                            y={0}
                            width={DAY_W}
                            height={height}
                            className="fill-accent-blue/10"
                          />
                          <line
                            x1={x}
                            y1={0}
                            x2={x}
                            y2={height}
                            className="stroke-accent-blue"
                            strokeWidth={1.5}
                            opacity={0.7}
                          />
                        </>
                      )}
                      <text
                        x={x + DAY_W / 2}
                        y={44}
                        textAnchor="middle"
                        className="fill-ink-muted text-[10px]"
                      >
                        {d.slice(8, 10)}
                      </text>
                      <line
                        x1={x}
                        y1={HEADER_H}
                        x2={x}
                        y2={height}
                        className="stroke-border-hairline"
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
                      className="fill-raised"
                    />
                  );
                })}
                {/* Dependency arrows (under the bars). Clicking an edge opens
                    the delete confirm; a fat invisible hit path makes the
                    1.5px line easy to grab. */}
                {arrows.map((a) => {
                  const midX = (a.x1 + a.x2) / 2;
                  return (
                    <g
                      key={a.id}
                      className={cn(canEdit && "cursor-pointer")}
                      onClick={() => {
                        if (!canEdit || linkUi.drawingFrom) return;
                        setLinkUi(requestLinkDelete(linkUi, a.link));
                      }}
                    >
                      <path
                        d={`M ${a.x1} ${a.y1} C ${midX} ${a.y1}, ${midX} ${a.y2}, ${a.x2 - 2} ${a.y2}`}
                        fill="none"
                        stroke="transparent"
                        strokeWidth={12}
                        pointerEvents="stroke"
                      >
                        <title>
                          {`${issueById.get(a.link.issue_id)?.display_id ?? ""} blocks ${issueById.get(a.link.target_issue_id)?.display_id ?? ""} — click to remove`}
                        </title>
                      </path>
                      <path
                        d={`M ${a.x1} ${a.y1} C ${midX} ${a.y1}, ${midX} ${a.y2}, ${a.x2 - 2} ${a.y2}`}
                        fill="none"
                        strokeWidth={1.5}
                        className={cn(
                          "stroke-muted-foreground",
                          canEdit && "transition-colors hover:stroke-destructive",
                        )}
                        markerEnd="url(#gantt-arrow)"
                        pointerEvents="none"
                      />
                    </g>
                  );
                })}
                {/* In-progress edge draw preview. C151T1: the
                    source resolves through linkEndpoints, so a draw
                    from a child bar starts at the child bar's right
                    edge, mid-row y — same dashed stroke-primary
                    line to the cursor as for parents. */}
                {linkUi.drawingFrom &&
                  drawPos &&
                  (() => {
                    const ep = linkUi.drawingFrom
                      ? linkEndpoints.get(linkUi.drawingFrom)
                      : undefined;
                    const fb =
                      ep && barSpan(ep.startDate, ep.targetDate);
                    if (!fb?.start || !fb?.end || !ep) return null;
                    const x1 = dayToX(fb.end, windowStart, DAY_W) + DAY_W;
                    const y1 = ep.y + ROW_H / 2;
                    return (
                      <path
                        d={`M ${x1} ${y1} L ${drawPos.x} ${drawPos.y}`}
                        fill="none"
                        strokeWidth={1.5}
                        strokeDasharray="4 3"
                        className="stroke-primary"
                        pointerEvents="none"
                      />
                    );
                  })()}
                {/* Bars */}
                {rows.map((r, idx) => {
                  if (r.kind !== "issue" && r.kind !== "issue-child") return null;
                  // C145T3: a child bar uses the child's own dates
                  // via the same barSpan math as a parent bar — a
                  // dateless child yields no bar, exactly as a
                  // dateless parent. C150T1: child bars move-drag
                  // exactly like parents (same handler, presence
                  // from the child's own stored dates). C150T2:
                  // child bars carry edge-resize handles exactly
                  // like parents (same gates, same geometry, same
                  // handler over the bridged barIssue). C151T1:
                  // child bars carry the link-draw handle and the
                  // link-mode press too — the barIssue bridge
                  // already carries id + stored dates, and
                  // onBarPointerDown's link-mode branch is
                  // id-generic, so no new state machine.
                  const isChild = r.kind === "issue-child";
                  const barIssue = isChild
                    ? {
                        id: r.row.id,
                        display_id: r.row.display_id,
                        name: r.row.title,
                        state_id: r.row.state_id,
                        start_date: r.row.start_date ?? null,
                        target_date: r.row.target_date ?? null,
                      }
                    : r.issue;
                  const b = barSpan(barIssue.start_date, barIssue.target_date);
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
                  // Live drag preview. Move is byte-identical to
                  // C109 (shift the window-clamped start). Resize
                  // previews from the TRUE span (b.start/b.end),
                  // never the window-clamped span, then clamps the
                  // preview span to the window for display: a
                  // resize-start shifts x and grows/shrinks from
                  // the left, a resize-end keeps x and changes
                  // the width. Both clamp to a min-1-day bar.
                  if (drag?.id === barIssue.id) {
                    const delta = Math.round(drag.dx / DAY_W);
                    if (drag.mode === "resize-start") {
                      let newStart = addDays(b.start!, delta);
                      if (newStart > b.end!) newStart = b.end!;
                      const pVisStart =
                        newStart < windowStart ? windowStart : newStart;
                      x = dayToX(pVisStart, windowStart, DAY_W);
                      w = (daysBetween(pVisStart, visEnd) + 1) * DAY_W - 4;
                    } else if (drag.mode === "resize-end") {
                      let newEnd = addDays(b.end!, delta);
                      if (newEnd < b.start!) newEnd = b.start!;
                      const pVisEnd =
                        newEnd >= windowEnd ? days[days.length - 1] : newEnd;
                      w = (daysBetween(visStart, pVisEnd) + 1) * DAY_W - 4;
                    } else {
                      x = dayToX(addDays(visStart, delta), windowStart, DAY_W);
                    }
                  }
                  const color =
                    statesById.get(barIssue.state_id ?? "")?.color ?? "#6366f1";
                  // C149T2: a 1-day bar is a filled span of one day
                  // (single-date issues fill via barSpan, and a
                  // ranged issue with start == target is 1 day too).
                  const isSingleDay = daysBetween(b.start!, b.end!) === 0;
                  return (
                    <g key={barIssue.id}>
                      <rect
                        x={x + 2}
                        y={y + 9}
                        width={Math.max(w, 8)}
                        height={16}
                        rx={4}
                        fill={color}
                        fillOpacity={0.9}
                        stroke="rgba(0,0,0,0.3)"
                        strokeWidth={1}
                        className={cn(
                          canEdit &&
                            !linkUi.linkMode &&
                            "cursor-grab",
                          canEdit &&
                            linkUi.linkMode &&
                            "cursor-crosshair",
                        )}
                        onPointerDown={
                          // C151T1: a child-bar press in link mode
                          // calls the SAME onBarPointerDown the
                          // move path uses — its link-mode branch
                          // starts the draw and returns, so no
                          // move drag starts in link mode, exactly
                          // as for parents. canEdit is re-checked
                          // inside the handler, so a read-only bar
                          // starts nothing in either mode.
                          (e) =>
                            onBarPointerDown(e, barIssue, {
                              start: b.start!,
                              end: b.end!,
                            })
                        }
                        onClick={() => {
                          // C150T1: the child path carries the
                          // parent's suppressClick check, so a real
                          // drag does not open peek on pointer-up;
                          // a zero-delta press still opens peek
                          // exactly as before.
                          if (isChild) {
                            if (
                              !drag &&
                              !suppressClick.current &&
                              !linkUi.linkMode
                            )
                              openPeek(barIssue.id);
                            return;
                          }
                          if (!drag && !suppressClick.current && !linkUi.linkMode)
                            openPeek(barIssue.id);
                        }}
                      >
                        <title>{`${barIssue.display_id}: ${barIssue.name}`}</title>
                      </rect>
                      {/* C144T1: resize handles, siblings of the bar
                          rect (so a handle press never reaches the
                          bar's move handler). Only when canEdit &&
                          !linkMode; in link mode the right edge
                          already hosts the draw handle. Window-
                          clamp rule (binding): a handle is omitted
                          when its true edge lies outside the
                          visible window — the drawn edge there is
                          not the true date, so grabbing it must
                          not move that date. C150T2: child bars
                          carry the same handles under the same
                          gates, over the bridged barIssue. */}
                      {canEdit && !linkUi.linkMode && b.start! >= windowStart && (
                        <rect
                          x={x + 2}
                          y={y + 9}
                          width={6}
                          height={16}
                          fill="#ffffff"
                          fillOpacity={0.4}
                          className="cursor-ew-resize"
                          aria-label={
                            isSingleDay ? undefined : "Drag to change start date"
                          }
                          onPointerDown={
                            isSingleDay
                              ? undefined
                              : (e) =>
                                  onResizePointerDown(e, barIssue, {
                                    start: b.start!,
                                    end: b.end!,
                                  }, "resize-start")
                          }
                        >
                          {!isSingleDay && (
                            <title>Drag to change start date</title>
                          )}
                        </rect>
                      )}
                      {canEdit && !linkUi.linkMode && b.end! < windowEnd && (
                        <rect
                          x={x + 2 + Math.max(w, 8) - 6}
                          y={y + 9}
                          width={6}
                          height={16}
                          fill="#ffffff"
                          fillOpacity={0.4}
                          className="cursor-ew-resize"
                          aria-label={
                            isSingleDay ? undefined : "Drag to change due date"
                          }
                          onPointerDown={
                            isSingleDay
                              ? undefined
                              : (e) =>
                                  onResizePointerDown(e, barIssue, {
                                    start: b.start!,
                                    end: b.end!,
                                  }, "resize-end")
                          }
                        >
                          {!isSingleDay && (
                            <title>Drag to change due date</title>
                          )}
                        </rect>
                      )}
                      {/* C149T2: enlarged invisible hit-areas on
                          1-day bars only, rendered AFTER the visible
                          handles so they paint on top in the overlap
                          region. Each hit rect covers its visible
                          6px handle plus 6px OUTWARD from the bar
                          (left: barX-6..barX+6, right:
                          barRight-6..barRight+6) and never extends
                          inward over the middle 12px move strip, so
                          move-grab is untouched. fill="transparent"
                          (not "none") so the rect still receives
                          pointer events under the default
                          visiblePainted rule. The hit rect IS the
                          one logical handle per edge on a 1-day bar:
                          it carries the handler, aria-label and
                          <title> the visible handle carries on
                          multi-day bars, while the visible 6px rect
                          above is a non-interactive decoration —
                          otherwise assistive tech and tests would
                          see two handles per edge. Same gates as
                          the visible handles (canEdit &&
                          !linkMode, C144 window-clamp per edge);
                          multi-day bars render nothing here. Rows
                          never overlap in y, so the outward
                          extension cannot steal another row's bar. */}
                      {canEdit && !linkUi.linkMode && isSingleDay && b.start! >= windowStart && (
                        <rect
                          x={x + 2 - 6}
                          y={y + 9}
                          width={12}
                          height={16}
                          fill="transparent"
                          className="cursor-ew-resize"
                          data-gantt-hit="resize-start"
                          aria-label="Drag to change start date"
                          onPointerDown={(e) =>
                            onResizePointerDown(e, barIssue, {
                              start: b.start!,
                              end: b.end!,
                            }, "resize-start")
                          }
                        >
                          <title>Drag to change start date</title>
                        </rect>
                      )}
                      {canEdit && !linkUi.linkMode && isSingleDay && b.end! < windowEnd && (
                        <rect
                          x={x + 2 + Math.max(w, 8) - 6}
                          y={y + 9}
                          width={12}
                          height={16}
                          fill="transparent"
                          className="cursor-ew-resize"
                          data-gantt-hit="resize-end"
                          aria-label="Drag to change due date"
                          onPointerDown={(e) =>
                            onResizePointerDown(e, barIssue, {
                              start: b.start!,
                              end: b.end!,
                            }, "resize-end")
                          }
                        >
                          <title>Drag to change due date</title>
                        </rect>
                      )}
                      {/* Link-mode draw handle at the bar's right
                          edge. C151T1: same geometry, same
                          fill-primary stroke-background circle for
                          child bars as for parents; gates unchanged
                          (canEdit && linkMode only). */}
                      {linkUi.linkMode && canEdit && (
                        <circle
                          cx={x + 2 + Math.max(w, 8)}
                          cy={y + ROW_H / 2}
                          r={4.5}
                          className="fill-primary stroke-background"
                          strokeWidth={1.5}
                          pointerEvents="none"
                        />
                      )}
                    </g>
                  );
                })}
                {/* C146T2: cascade ghost preview — each would-shift
                    dependent renders as a translucent, dashed ghost
                    at its shifted position, ALONGSIDE its real bar
                    (which stays at its current dates; the dragged
                    bar's own preview above is the only bar that
                    moves). Same geometry, same state color, same
                    window clamp as a real bar — the existing
                    drag-preview treatment, only translucent and
                    dashed, so no new visual language is introduced.
                    pointerEvents="none": ghosts never intercept a
                    drag. Renders for top-level and nested child
                    bars alike via ghostRows. */}
                {previewCascade.map((shift) => {
                  const row = ghostRows.get(shift.id);
                  if (!row) return null;
                  const visStart =
                    shift.start_date < windowStart
                      ? windowStart
                      : shift.start_date;
                  const visEnd =
                    shift.target_date >= windowEnd
                      ? days[days.length - 1]
                      : shift.target_date;
                  if (visStart > visEnd) return null;
                  const gx = dayToX(visStart, windowStart, DAY_W);
                  const gw =
                    (daysBetween(visStart, visEnd) + 1) * DAY_W - 4;
                  const color =
                    statesById.get(row.stateId ?? "")?.color ?? "#6366f1";
                  return (
                    <g key={`cascade-ghost-${shift.id}`}>
                      <rect
                        x={gx + 2}
                        y={row.y + 9}
                        width={Math.max(gw, 8)}
                        height={16}
                        rx={4}
                        fill={color}
                        fillOpacity={0.35}
                        stroke={color}
                        strokeWidth={1}
                        strokeDasharray="4 3"
                        pointerEvents="none"
                        data-cascade-ghost="true"
                        aria-label={`Cascade preview for ${row.displayId}`}
                      >
                        <title>{`${row.displayId}: ${row.name} (would shift to ${shift.start_date} – ${shift.target_date})`}</title>
                      </rect>
                    </g>
                  );
                })}
                {/* C156T1: schedule ghost — a 1-day bar at the
                    pointer's day on the dragged row's track, reusing
                    the C146 cascade-ghost token/opacity VERBATIM
                    (fillOpacity 0.35, dashed stroke, same geometry
                    as a 1-day bar) — no new visual language. Hidden
                    while the pointer is over the label column
                    (pointerX < 0: no day to show). pointerEvents
                    "none": the ghost never intercepts a drag. */}
                {drag?.mode === "schedule" &&
                  (() => {
                    const row = ghostRows.get(drag.id);
                    if (!row) return null;
                    const pointerX = drag.startX + drag.dx;
                    if (pointerX < 0) return null;
                    const day = xToDay(
                      pointerX,
                      windowStart,
                      DAY_W,
                      days.length,
                    );
                    const gx = dayToX(day, windowStart, DAY_W);
                    const color =
                      statesById.get(row.stateId ?? "")?.color ?? "#6366f1";
                    return (
                      <g key={`schedule-ghost-${drag.id}`}>
                        <rect
                          x={gx + 2}
                          y={row.y + 9}
                          width={Math.max(DAY_W - 4, 8)}
                          height={16}
                          rx={4}
                          fill={color}
                          fillOpacity={0.35}
                          stroke={color}
                          strokeWidth={1}
                          strokeDasharray="4 3"
                          pointerEvents="none"
                          data-schedule-ghost="true"
                          aria-label={`Schedule preview for ${row.displayId}`}
                        >
                          <title>{`${row.displayId}: ${row.name} (would schedule on ${day})`}</title>
                        </rect>
                      </g>
                    );
                  })()}
              </svg>
            </div>
          </div>
        )}
        {patchMutation.isError && (
          <div className="text-[13px] text-destructive">
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
        {/* Delete-dependency confirm (C15T0). */}
        <AlertDialog
          open={!!linkUi.confirmDelete}
          onOpenChange={(open) => {
            if (!open) setLinkUi(closeLinkDelete(linkUi));
          }}
        >
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>Remove this dependency?</AlertDialogTitle>
              <AlertDialogDescription>
                {linkUi.confirmDelete &&
                  `Remove the "blocks" dependency — ${
                  issueById.get(linkUi.confirmDelete.issue_id)?.display_id ??
                  "?"
                } → ${
                  issueById.get(linkUi.confirmDelete.target_issue_id)
                    ?.display_id ?? "?"
                }? The issues themselves are not affected.`}
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>Cancel</AlertDialogCancel>
              <AlertDialogAction
                disabled={deleteLinkMutation.isPending}
                onClick={() => {
                  const target = linkUi.confirmDelete;
                  setLinkUi(closeLinkDelete(linkUi));
                  if (target) deleteLinkMutation.mutate(target);
                }}
              >
                Remove dependency
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      </div>
    </>
  );
}
