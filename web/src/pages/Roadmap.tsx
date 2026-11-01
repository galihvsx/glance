import { useMemo, useState } from "react";
import { useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import PageHeader, { pageHeaderControlClass } from "../components/shell/PageHeader";
import PeekDrawer from "../components/issue/PeekDrawer";
import PqlEditor from "../components/issue/PqlEditor";
import SavedViewsMenu from "../components/issue/SavedViewsMenu";
import { usePeekParam } from "../components/issue/usePeek";
import PriorityBars from "../components/issue/PriorityBars";
import { api } from "../lib/api";
import { EMPTY_FILTERS } from "../lib/filters";
import { formatPqlError } from "../lib/pql";
import type { Issue, IssueListResult, IssueState, Project } from "../lib/types";
import { Alert, AlertDescription } from "../components/ui/alert";
import { priorityLabel } from "../lib/types";
import { stateDotClass } from "../lib/stateDot";
import {
  bucketIssues,
  currentMonthStart,
  roadmapMonths,
} from "../lib/roadmap";
import { cn } from "../lib/utils";

/**
 * Project roadmap: issues grouped by the month of their `target_date`,
 * with issues lacking a target date in an "Unscheduled" lane.
 *
 * Rendering choice: a single lane per month with state chips (colored dot
 * + state name), NOT per-state swimlanes. A swimlane layout multiplies the
 * vertical scan cost and duplicates the Gantt view's grouping; the single
 * lane keeps the month columns scannable while the chip still carries the
 * state signal (same chip vocabulary as the board cards).
 */
const RANGES = [3, 6, 12] as const;
type Range = (typeof RANGES)[number];

/** 22px meta chip — the same chip vocabulary as the board cards
 *  (C19T2 board card anatomy). */
const roadmapChipClass =
  "inline-flex h-[22px] items-center gap-[5px] rounded-[4px] border border-border bg-[#111113] px-[6px] text-[9px] text-ink-secondary";

async function fetchAllIssues(path: string): Promise<Issue[]> {
  const issues: Issue[] = [];
  let cursor: string | undefined;
  do {
    const sep = path.includes("?") ? "&" : "?";
    const page = new URLSearchParams({ per_page: "100" });
    if (cursor) page.set("cursor", cursor);
    const res = await api.get<IssueListResult>(`${path}${sep}${page}`);
    issues.push(...res.results);
    cursor = res.next_cursor;
  } while (cursor);
  return issues;
}

function formatDay(day: string): string {
  const [y, m, d] = day.split("-").map(Number);
  return new Date(Date.UTC(y, m - 1, d)).toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
    timeZone: "UTC",
  });
}

export default function Roadmap() {
  const { slug = "", identifier = "" } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;
  const { peekUuid, openPeek, closePeek } = usePeekParam();

  const [range, setRange] = useState<Range>(6);
  const [anchor, setAnchor] = useState<string>(() => currentMonthStart());

  // One paginated list query composed from the existing endpoint — no new
  // backend work needed (start_date/target_date are already on the list
  // response, C9T0 contract check).
  const issuesQuery = useQuery({
    queryKey: ["roadmap", slug, identifier],
    queryFn: () => fetchAllIssues(`${base}/issues`),
  });

  // C109T3: PQL mode on the roadmap, copying the Cycles detail pattern
  // verbatim (C95T1/C107T1): pqlMode local state, appliedQuery,
  // probe-first handlePqlApply through the plain list (cursor loop
  // via fetchAllIssues, as today), PQL editor keyed by appliedQuery
  // (the C107 re-apply lesson — PqlEditor's draft is
  // initial-value-only). The PQL query key includes the applied
  // query, so PQL and visual caches never cross-contaminate.
  // PQL mode changes ONLY which issues render: month buckets and
  // the Unscheduled lane are computed from the filtered set below.
  // Leaving PQL mode clears the applied query and restores the
  // unfiltered list. A 400 rejects in the probe, so PqlEditor
  // shows it inline and the displayed set is untouched
  // (appliedQuery — and therefore displayedIssues — never changes).
  // Empty query = no filter: stays in PQL mode, shows the full list.
  // C110T3: the detail-mode SavedViewsMenu below (C107T1 shape) —
  // the shared ["project", slug, identifier] query supplies the
  // project UUID the menu needs, cache-warm whenever the user came
  // from another project page. Roadmap has no window, so the C109
  // design question is moot here: a view apply sets the query, and
  // month buckets and the Unscheduled lane recompute from the
  // filtered set. The wiring is the same detail-mode shape as the
  // other date views for consistency, not because a window is
  // involved — a saved query never moves anything, it only filters.
  const projectQuery = useQuery({
    queryKey: ["project", slug, identifier],
    queryFn: () => api.get<Project>(base),
  });
  const [pqlMode, setPqlMode] = useState(false);
  const [appliedQuery, setAppliedQuery] = useState("");
  // C110T3: a saved view whose query fails its probe has no editor
  // inline error to land in — the editor only reports failures from
  // its own Apply button, and it mounts fresh (empty draft) for a
  // menu apply. The failure is surfaced here, at the detail level,
  // instead of vanishing or rejecting unhandled.
  const [savedViewError, setSavedViewError] = useState<string | null>(null);
  const pqlIssuesQuery = useQuery({
    queryKey: ["roadmap-pql", slug, identifier, appliedQuery],
    enabled: pqlMode && appliedQuery.trim() !== "",
    queryFn: () =>
      fetchAllIssues(
        `${base}/issues?pql=${encodeURIComponent(appliedQuery.trim())}`,
      ),
  });
  async function handlePqlApply(query: string): Promise<void> {
    const q = query.trim();
    if (!q) {
      setAppliedQuery("");
      return;
    }
    await fetchAllIssues(`${base}/issues?pql=${encodeURIComponent(q)}`);
    setAppliedQuery(q);
    setSavedViewError(null);
  }
  function handlePqlModeChange(mode: "all" | "pql") {
    if (mode === "pql") {
      setPqlMode(true);
      return;
    }
    // Leaving PQL mode clears the applied query, so the unfiltered
    // list (already cached) is restored immediately.
    setPqlMode(false);
    setAppliedQuery("");
    setSavedViewError(null);
  }
  // C110T3: applying a saved PQL view from the detail-mode menu,
  // copying the Cycles handleSavedViewApplied shape verbatim.
  // Probe FIRST, then enter PQL mode: the probe commits appliedQuery
  // while still in all-issues mode (displayedIssues is unaffected
  // until pqlMode flips), so the editor mounts seeded with the
  // applied query. A 400 leaves appliedQuery untouched — the list
  // never changes — and the error surfaces in the detail-level alert
  // (the editor cannot show it: it mounts fresh with an empty draft).
  // The menu ignores this handler's return value, so the catch here
  // is load-bearing: without it the rejected probe would be an
  // unhandled rejection. Null is unreachable from the detail-mode
  // menu (PQL views only) and mirrors Cycles: leave PQL mode.
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
  // unfiltered list rather than blanking it — the filtered set
  // replaces it as soon as it arrives.
  const displayedIssues =
    pqlMode && appliedQuery.trim() !== ""
      ? (pqlIssuesQuery.data ?? issuesQuery.data ?? [])
      : (issuesQuery.data ?? []);
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

  const buckets = useMemo(() => roadmapMonths(anchor, range), [anchor, range]);
  const { columns, unscheduled } = useMemo(
    () => bucketIssues(displayedIssues, buckets),
    [displayedIssues, buckets],
  );

  const loading = issuesQuery.isLoading || statesQuery.isLoading;
  const error = issuesQuery.error ?? statesQuery.error;

  const windowLabel =
    buckets.length > 0
      ? `${buckets[0].label} – ${buckets[buckets.length - 1].label}`
      : "";

  const renderIssue = (issue: Issue) => {
    const state = statesById.get(issue.state_id);
    const target = issue.target_date?.slice(0, 10);
    return (
      <button
        key={issue.id}
        type="button"
        onClick={() => openPeek(issue.id)}
        className="w-full rounded-[6px] border border-border bg-surface p-[10px] text-left transition-[border-color,background-color] duration-150 hover:border-[#3f3f46] hover:bg-[#17171a]"
        title={`${issue.display_id}: ${issue.name}`}
      >
        <div className="flex items-center">
          <span className="font-mono text-[10px] text-ink-muted">
            {issue.display_id}
          </span>
          {target && (
            <span className="ml-auto font-mono text-[10px] text-ink-muted">
              Due {formatDay(target)}
            </span>
          )}
        </div>
        <p className="mb-[10px] mt-[5px] text-[13px] font-[570] leading-[18px] tracking-[-0.008em] text-ink">
          {issue.name}
        </p>
        <div className="flex flex-wrap gap-[4px]">
          {state && (
            <span className={roadmapChipClass}>
              <span
                className={cn("state-dot", stateDotClass(state.group))}
                aria-hidden
              />
              {state.name}
            </span>
          )}
          <span className={roadmapChipClass}>
            <PriorityBars priority={issue.priority} />
            {priorityLabel(issue.priority)}
          </span>
        </div>
      </button>
    );
  };

  return (
    <>
      {/* C19T4: shared page header replaces the per-page ProjectNav. */}
      <PageHeader viewName="Roadmap" />
      <div className="flex h-full flex-col gap-3 p-4">
        <div className="flex flex-wrap items-center gap-1.5">
          {/* C109T3: All issues / PQL toggle, Cycles pattern verbatim,
              above the roadmap next to the existing controls. */}
          <div
            className="inline-flex h-[30px] items-center rounded-[6px] border border-border bg-[#101012] p-[2px]"
            role="group"
            aria-label="Issue list mode"
          >
            <button
              type="button"
              aria-pressed={!pqlMode}
              onClick={() => handlePqlModeChange("all")}
              className={cn(
                "h-[24px] rounded-[4px] px-2 text-[10px]",
                !pqlMode
                  ? "bg-raised font-medium text-ink"
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
                "h-[24px] rounded-[4px] px-2 text-[10px]",
                pqlMode
                  ? "bg-raised font-medium text-ink"
                  : "text-ink-muted hover:text-ink-secondary",
              )}
            >
              PQL
            </button>
          </div>
          {/* C110T3: detail-mode SavedViewsMenu beside the toggle,
              copying the CycleDetail/EpicDetail detail toolbar
              verbatim. Detail mode guarantees no URL writes and no
              default auto-apply, so the page settles PQL state
              through onViewApplied only. */}
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
          <div
            className="inline-flex h-[30px] items-center rounded-[6px] border border-border bg-[#101012] p-[2px]"
            role="group"
            aria-label="Range"
          >
            {RANGES.map((r) => (
              <button
                key={r}
                type="button"
                onClick={() => setRange(r)}
                className={cn(
                  "h-[24px] rounded-[4px] px-2 text-[10px]",
                  range === r
                    ? "bg-raised font-medium text-ink"
                    : "text-ink-muted hover:text-ink-secondary",
                )}
              >
                {r} mo
              </button>
            ))}
          </div>
          <button
            type="button"
            onClick={() => setAnchor(currentMonthStart())}
            className={pageHeaderControlClass}
          >
            Today
          </button>
          <span className="ml-1 text-[13px] font-semibold text-ink">
            {windowLabel}
          </span>
        </div>

        {savedViewError && (
          <Alert variant="destructive">
            <AlertDescription>{savedViewError}</AlertDescription>
          </Alert>
        )}

        {pqlMode && (
          // Keyed by the applied query: PqlEditor's draft is
          // initial-value-only, so without this a re-apply would
          // leave the editor showing the previous draft instead of
          // the applied query.
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

        {loading && (
          <div className="text-[13px] text-ink-muted">Loading roadmap…</div>
        )}
        {error && (
          <div className="text-[13px] text-destructive">
            Failed to load issues.{" "}
            <button
              type="button"
              className="underline"
              onClick={() => {
                void issuesQuery.refetch();
                void statesQuery.refetch();
              }}
            >
              Retry
            </button>
          </div>
        )}
        {!loading && !error && (
          <div className="flex min-h-0 flex-1 gap-[28px] overflow-x-auto px-[18px] pb-[18px] pt-[14px]">
            {columns.map(({ bucket, issues }) => (
              <section
                key={bucket.month}
                aria-label={bucket.label}
                className="flex w-[325px] shrink-0 flex-col rounded-[7px] border border-border bg-[#0f0f11] px-[8px] pb-[8px]"
              >
                <header className="flex h-[42px] items-center gap-[7px] border-b border-border-hairline px-[4px]">
                  <h3 className="text-[11px] font-semibold text-ink">
                    {bucket.label}
                  </h3>
                  <span className="font-mono text-[10px] text-ink-muted">
                    {issues.length}
                  </span>
                </header>
                <div className="flex min-h-0 flex-1 flex-col gap-[7px] overflow-y-auto pt-[8px]">
                  {issues.map(renderIssue)}
                  {issues.length === 0 && (
                    <p className="px-1 py-2 text-[11px] text-ink-muted">
                      No issues due this month.
                    </p>
                  )}
                </div>
              </section>
            ))}
            {/* Unscheduled lane: issues with no target_date (target_date is
                the bucket key — start_date alone does not schedule an
                issue; see roadmapBucket). */}
            <section
              aria-label="Unscheduled"
              className="flex w-[325px] shrink-0 flex-col rounded-[7px] border border-dashed border-border bg-transparent px-[8px] pb-[8px]"
            >
              <header className="flex h-[42px] items-center gap-[7px] border-b border-dashed border-border-hairline px-[4px]">
                <h3 className="text-[11px] font-semibold text-ink-muted">
                  Unscheduled
                </h3>
                <span className="font-mono text-[10px] text-ink-muted">
                  {unscheduled.length}
                </span>
              </header>
              <div className="flex min-h-0 flex-1 flex-col gap-[7px] overflow-y-auto pt-[8px]">
                {unscheduled.map(renderIssue)}
                {unscheduled.length === 0 && (
                  <p className="px-1 py-2 text-[11px] text-ink-muted">
                    Every issue has a target date.
                  </p>
                )}
              </div>
            </section>
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
    </>
  );
}
