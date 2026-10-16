// OrgViews (C19T8): shared building blocks for the cycles / modules /
// releases / intake pages, per the cycle-19 Plane-layout contract
// (.superpowers/sdd/2026-10-11-glance-cycle19/prototype-spec.md):
//
// - Base 13px; text ramp text-ink / text-ink-secondary / text-ink-muted.
// - Cards: bg-surface, border-border, radius 6px, hover wash (#17171a) +
//   border #3f3f46 (board-card hover language).
// - Status chips: 22px tall, border, radius 4px, bg #111113 (board-chip
//   language), accent dot + 9px secondary label.
// - Progress bars in state / accent colors; hairline separators;
//   hover washes everywhere.
// - Section headers: 35px, 12px/600, bottom hairline, mono count
//   (list group-header language).
// - Issue rows: dense 47px, hairline separators, hover wash
//   (IssueRow grid language: 76px | 1fr | 110px | 80px).
// - Empty states in token style (no oversized headings; PageHeader
//   owns the page titles).

import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { cn } from "../../lib/utils";
import type { Issue, IssueState } from "../../lib/types";
import { priorityLabel } from "../../lib/types";
import { stateDotClass } from "../../lib/stateDot";
import PriorityBars from "../issue/PriorityBars";

// ---------- Status chip ----------

/** 22px chip with an accent dot and a 9px secondary label. */
export function OrgStatusChip({
  color,
  label,
}: {
  color: string;
  label: string;
}) {
  return (
    <span className="inline-flex h-[22px] shrink-0 items-center gap-[5px] rounded-[4px] border border-border bg-[#111113] px-[6px] text-[9px] font-medium text-ink-secondary">
      <span
        className="h-2 w-2 shrink-0 rounded-full"
        style={{ backgroundColor: color }}
        aria-hidden
      />
      {label}
    </span>
  );
}

// ---------- Card ----------

/**
 * Clickable org card (keyboard + screen-reader operable). Token-styled:
 * bg-surface, hairline border, 6px radius, board-card hover language.
 */
export function OrgCard({
  label,
  onActivate,
  className,
  children,
}: {
  label: string;
  onActivate: () => void;
  className?: string;
  children: ReactNode;
}) {
  return (
    <div
      role="button"
      tabIndex={0}
      aria-label={label}
      onClick={onActivate}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onActivate();
        }
      }}
      className={cn(
        "block w-full cursor-pointer rounded-md border border-border bg-surface px-3.5 py-3 text-left transition-colors",
        "hover:border-[#3f3f46] hover:bg-[#17171a]",
        "focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-[#4f8ef7]",
        className,
      )}
    >
      {children}
    </div>
  );
}

// ---------- Stat band ----------

/** Three-stat overview band (prototype screen-1 overview-band language):
 *  3-col grid, border, radius 8px, bg-surface; label 10px muted,
 *  value 18px/620, note 10px secondary. */
export function OrgStatBand({
  stats,
}: {
  stats: { label: string; value: number | string; note: string }[];
}) {
  return (
    <div className="grid grid-cols-3 overflow-hidden rounded-lg border border-border bg-surface">
      {stats.map((s, i) => (
        <div
          key={s.label}
          className={cn("p-[15px]", i < stats.length - 1 && "border-r border-border")}
        >
          <p className="mb-[7px] text-[10px] text-ink-muted">{s.label}</p>
          <p className="text-[18px] font-semibold tabular-nums text-ink">
            {s.value}
          </p>
          <p className="mt-[3px] text-[10px] text-ink-secondary">{s.note}</p>
        </div>
      ))}
    </div>
  );
}

// ---------- Section header ----------

/** 35px section header: 12px/600, bottom hairline, mono count.
 *  `prefix` renders before the title (e.g. a state-dot for group headers). */
export function OrgSectionHead({
  title,
  count,
  prefix,
  actions,
  className,
}: {
  title: string;
  count?: number | string;
  prefix?: ReactNode;
  /** Right-aligned controls rendered inside the 35px row. */
  actions?: ReactNode;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "flex h-[35px] items-center gap-2 border-b border-border-hairline",
        className,
      )}
    >
      {prefix}
      <h2 className="text-[12px] font-semibold text-ink">{title}</h2>
      {count !== undefined && (
        <span className="font-mono text-[10px] text-ink-muted">{count}</span>
      )}
      {actions && <div className="ml-auto flex items-center">{actions}</div>}
    </div>
  );
}

// ---------- Property grid ----------

/** Peek-language property grid: label 9px muted, value 10px/500 secondary. */
export function OrgPropertyGrid({
  properties,
}: {
  properties: { label: string; value: ReactNode }[];
}) {
  if (properties.length === 0) return null;
  return (
    <div className="grid grid-cols-3 gap-x-[18px] gap-y-[7px]">
      {properties.map((p) => (
        <div key={p.label}>
          <p className="mb-[5px] block text-[9px] text-ink-muted">{p.label}</p>
          <p className="flex min-h-[23px] items-center gap-1.5 text-[10px] font-medium text-ink-secondary">
            {p.value}
          </p>
        </div>
      ))}
    </div>
  );
}

// ---------- Dense issue row ----------

/**
 * Dense 47px issue row in the IssueRow grid language:
 * ID mono 10px muted | title 12px ink | 8px state-dot + 10px label |
 * priority bars + 10px label. Hairline separator, hover wash, links to
 * the issue detail page. An optional hover-reveal `action` renders
 * absolutely at the right (e.g. remove-from-module).
 */
export function OrgIssueRow({
  issue,
  state,
  slug,
  identifier,
  action,
}: {
  issue: Issue;
  state: IssueState | undefined;
  slug: string;
  identifier: string;
  action?: {
    label: string;
    title: string;
    onClick: () => void;
    icon: ReactNode;
  };
}) {
  return (
    <div className="group/orgrow relative">
      <Link
        to={`/w/${slug}/p/${identifier}/i/${issue.id}`}
        aria-label={`Open issue ${issue.display_id}: ${issue.name}`}
        title={issue.name}
        className="flex min-h-[47px] items-center border-b border-border-hairline py-2 transition-colors hover:bg-wash-hover focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-[#4f8ef7]"
      >
        <div className="grid min-h-[27px] flex-1 grid-cols-[76px_minmax(0,1fr)_110px_80px] items-center gap-[10px] pr-2">
          <span className="truncate font-mono text-[10px] text-ink-muted">
            {issue.display_id}
          </span>
          <span className="truncate text-[12px] text-ink">{issue.name}</span>
          <span className="flex min-w-0 items-center gap-[6px]">
            {state ? (
              <>
                <span
                  className={`state-dot ${stateDotClass(state.group)}`}
                  aria-hidden
                />
                <span className="truncate text-[10px] text-ink-secondary">
                  {state.name}
                </span>
              </>
            ) : null}
          </span>
          <span className="flex min-w-0 items-center gap-[5px]">
            <PriorityBars priority={issue.priority} />
            <span className="truncate text-[10px] text-ink-secondary">
              {priorityLabel(issue.priority)}
            </span>
          </span>
        </div>
      </Link>
      {action && (
        <button
          type="button"
          onClick={action.onClick}
          title={action.title}
          aria-label={action.label}
          className="absolute right-1 top-1/2 inline-flex h-6 w-6 -translate-y-1/2 items-center justify-center rounded-[4px] text-ink-muted opacity-0 transition-all hover:bg-wash-hover hover:text-ink focus-visible:opacity-100 group-hover/orgrow:opacity-100"
        >
          {action.icon}
        </button>
      )}
    </div>
  );
}

// ---------- Detail header ----------

/**
 * Compact detail header (peek language, compact edition): 10px muted
 * back-link, then a row with the 13px/600 title, status chip, 10px
 * muted meta, and compact actions pushed right.
 */
export function OrgDetailHeader({
  backLabel,
  onBack,
  title,
  chip,
  meta,
  actions,
}: {
  backLabel: string;
  onBack: () => void;
  title: string;
  chip?: ReactNode;
  meta?: ReactNode;
  actions?: ReactNode;
}) {
  return (
    <div>
      <button
        type="button"
        onClick={onBack}
        className="text-[10px] text-ink-muted transition-colors hover:text-ink-secondary hover:underline"
      >
        ← {backLabel}
      </button>
      <div className="mt-2 flex flex-wrap items-center gap-2.5">
        <h2 className="text-[13px] font-semibold tracking-tight text-ink">
          {title}
        </h2>
        {chip}
        {meta && (
          <span className="text-[10px] text-ink-muted tabular-nums">{meta}</span>
        )}
        {actions && (
          <div className="ml-auto flex items-center gap-1.5">{actions}</div>
        )}
      </div>
    </div>
  );
}

/** Compact secondary action button for detail headers (28px, 10px). */
export function OrgHeaderButton({
  onClick,
  disabled,
  children,
  title,
  destructive,
}: {
  onClick: () => void;
  disabled?: boolean;
  children: ReactNode;
  title?: string;
  destructive?: boolean;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      title={title}
      className={cn(
        "inline-flex h-[28px] items-center gap-1.5 rounded-md border border-border bg-surface px-2.5 text-[10px] font-medium transition-colors",
        "hover:bg-raised hover:text-ink disabled:cursor-not-allowed disabled:opacity-50",
        destructive ? "text-accent-red" : "text-ink-secondary",
      )}
    >
      {children}
    </button>
  );
}

/** Compact primary action button for detail headers (28px, blue). */
export function OrgHeaderPrimaryButton({
  onClick,
  disabled,
  children,
}: {
  onClick: () => void;
  disabled?: boolean;
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      className="inline-flex h-[28px] items-center gap-1.5 rounded-md border border-accent-blue bg-accent-blue px-2.5 text-[10px] font-semibold text-white transition-colors hover:border-accent-blue-hover hover:bg-accent-blue-hover disabled:cursor-not-allowed disabled:opacity-50"
    >
      {children}
    </button>
  );
}

// ---------- Empty state ----------

/** Token-style empty state: 13px/600 ink title, 11px muted sub. */
export function OrgEmpty({
  title,
  children,
  icon,
}: {
  title: string;
  children?: ReactNode;
  icon?: ReactNode;
}) {
  return (
    <div className="rounded-md border border-border-hairline px-6 py-10 text-center">
      {icon && (
        <div className="mx-auto mb-3 flex h-9 w-9 items-center justify-center rounded-full border border-border bg-surface text-ink-muted">
          {icon}
        </div>
      )}
      <p className="text-[13px] font-semibold text-ink">{title}</p>
      {children && (
        <p className="mx-auto mt-1 max-w-sm text-[11px] leading-relaxed text-ink-muted">
          {children}
        </p>
      )}
    </div>
  );
}

// ---------- Progress bars ----------

/**
 * Folded progress counts from a six-group progress_snapshot — the same
 * fold as the client-side progressCounts (issues + states): triage +
 * backlog + unstarted → open, started → in progress, completed +
 * cancelled → done. Unknown groups are ignored; missing groups are zero.
 */
export interface SnapshotCounts {
  open: number;
  inProgress: number;
  done: number;
}

export function snapshotProgressCounts(
  snapshot: Record<string, number>,
): SnapshotCounts {
  return {
    open:
      (snapshot.triage ?? 0) +
      (snapshot.backlog ?? 0) +
      (snapshot.unstarted ?? 0),
    inProgress: snapshot.started ?? 0,
    done: (snapshot.completed ?? 0) + (snapshot.cancelled ?? 0),
  };
}

/** Counts line under a progress bar, prototype shape (C38T1):
 *  "6 done · 3 in progress · 3 open". */
export function OrgProgressCounts({ open, inProgress, done }: SnapshotCounts) {
  return (
    <p className="mt-1.5 text-[11px] text-ink-muted">
      <b className="tabular-nums text-ink">{done}</b> done
      <span className="mx-1" aria-hidden>
        ·
      </span>
      <b className="tabular-nums text-ink">{inProgress}</b> in progress
      <span className="mx-1" aria-hidden>
        ·
      </span>
      <b className="tabular-nums text-ink">{open}</b> open
    </p>
  );
}

/**
 * Stacked progress bar from a cycle progress_snapshot. Segment colors
 * come from the project's states (first state in each group) — state
 * colors, never hardcoded. Thin 6px track; optional compact legend.
 *
 * `states` only needs group + color, so overview payload states
 * (OverviewState) are assignable too. When the snapshot is empty,
 * `emptyBar` renders an all-open bar (a full-width segment in the
 * open-group color) instead of the `emptyLabel` text — releases show
 * the bar even with zero issues (C38T1 prototype); callers that want
 * the text keep the default.
 */
export function OrgProgressBar({
  snapshot,
  states,
  legend = false,
  emptyLabel = "No issues yet.",
  emptyBar = false,
}: {
  snapshot: Record<string, number>;
  states: Array<{ group: string; color: string }>;
  legend?: boolean;
  emptyLabel?: string;
  emptyBar?: boolean;
}) {
  const groups = [
    "triage",
    "backlog",
    "unstarted",
    "started",
    "completed",
    "cancelled",
  ] as const;
  const total = groups.reduce((n, g) => n + (snapshot[g] ?? 0), 0);
  if (total === 0) {
    if (emptyBar) {
      // All-open bar: no issues, so the whole track reads as open.
      // Color = first state in an open group (unstarted, then backlog,
      // then triage), same fallback as the segments below.
      const openColor =
        states.find((s) => s.group === "unstarted")?.color ??
        states.find((s) => s.group === "backlog")?.color ??
        states.find((s) => s.group === "triage")?.color ??
        "#777780";
      return (
        <div
          className="flex h-[6px] w-full overflow-hidden rounded-full bg-raised"
          role="progressbar"
          aria-valuemin={0}
          aria-valuemax={0}
          aria-valuenow={0}
          aria-label="Progress"
        >
          <div
            className="h-full"
            style={{ width: "100%", backgroundColor: openColor }}
            title="open: 0"
          />
        </div>
      );
    }
    return <p className="text-[11px] text-ink-muted">{emptyLabel}</p>;
  }
  return (
    <div>
      <div
        className="flex h-[6px] w-full overflow-hidden rounded-full bg-raised"
        role="progressbar"
        aria-valuemin={0}
        aria-valuemax={total}
        aria-valuenow={snapshot.completed ?? 0}
        aria-label="Cycle progress"
      >
        {groups.map((g) => {
          const n = snapshot[g] ?? 0;
          if (n === 0) return null;
          const color =
            states.find((s) => s.group === g)?.color ?? "#777780";
          return (
            <div
              key={g}
              className="h-full"
              style={{
                width: `${(n / total) * 100}%`,
                backgroundColor: color,
              }}
              title={`${g}: ${n}`}
            />
          );
        })}
      </div>
      {legend && (
        <div className="mt-1.5 flex flex-wrap gap-x-3 gap-y-1">
          {groups.map((g) => {
            const n = snapshot[g] ?? 0;
            if (n === 0) return null;
            const color =
              states.find((s) => s.group === g)?.color ?? "#777780";
            return (
              <span
                key={g}
                className="flex items-center gap-1.5 text-[9px] capitalize text-ink-muted"
              >
                <span
                  className="h-1.5 w-1.5 rounded-full"
                  style={{ backgroundColor: color }}
                  aria-hidden
                />
                {g} · {n}
              </span>
            );
          })}
        </div>
      )}
    </div>
  );
}

/** Simple done-% bar; the fill is the green accent (completion). */
export function OrgDoneBar({
  done,
  total,
}: {
  done: number;
  total: number;
}) {
  const pct = total === 0 ? 0 : Math.round((done / total) * 100);
  return (
    <div className="flex items-center gap-2">
      <div
        className="h-[6px] w-full overflow-hidden rounded-full bg-raised"
        role="progressbar"
        aria-valuenow={pct}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-label={`${pct}% done`}
      >
        <div
          className="h-full rounded-full bg-accent-green transition-all"
          style={{ width: `${pct}%` }}
        />
      </div>
      <span className="shrink-0 text-[10px] tabular-nums text-ink-muted">
        {pct}%
      </span>
    </div>
  );
}
