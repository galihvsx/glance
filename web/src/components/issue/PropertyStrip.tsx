import { useState } from "react";
import { Calendar, Check, User } from "lucide-react";
import { priorityLabel, type Issue, type IssueState, type Member } from "../../lib/types";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";
import { cn } from "cn";
import StateDot from "./StateDot";
import AssigneePicker from "./AssigneePicker";

/** Priority bars per the cycle-19 spec (Screen 2): 11x11px flex, gap 1px,
 *  items-end; three 2px-wide bars, heights 3/6/9px, radius 1px, amber for
 *  the filled ones. */
export function PriorityBars({ value, className }: { value: number; className?: string }) {
  const filled = Math.max(0, Math.min(3, value));
  return (
    <span className={cn("flex h-[11px] w-[11px] items-end gap-[1px]", className)} aria-hidden>
      {[3, 6, 9].map((h, i) => (
        <span
          key={h}
          className="w-[2px] rounded-[1px]"
          style={{
            height: `${h}px`,
            backgroundColor: i < filled ? "#e7ad42" : "rgba(255,255,255,.15)",
          }}
        />
      ))}
    </span>
  );
}

function fmtShortDate(iso: string): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})/.exec(iso);
  if (!m) return iso;
  const d = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]));
  return d.toLocaleDateString("en-US", { month: "short", day: "numeric" });
}

const itemCls =
  "flex h-[27px] shrink-0 items-center gap-[7px] border-r border-border px-3 text-[10px] text-ink-secondary first:pl-0 last:border-r-0";
const triggerCls =
  "flex h-[27px] items-center gap-[7px] rounded-[4px] px-1 text-[10px] text-ink-secondary transition-colors hover:bg-wash-hover hover:text-ink disabled:opacity-50";

function DateItem({
  label,
  value,
  onPatch,
  disabled,
}: {
  label: string;
  value?: string | null;
  onPatch: (v: string | null) => void;
  disabled?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState(value ?? "");
  return (
    <span className={itemCls}>
      <Popover
        open={open}
        onOpenChange={(o) => {
          setOpen(o);
          if (o) setDraft(value ?? "");
        }}
      >
        <PopoverTrigger disabled={disabled} className={cn(triggerCls, value && "text-ink")}>
          <Calendar className="h-[13px] w-[13px]" strokeWidth={1.8} />
          {value ? fmtShortDate(value) : label}
        </PopoverTrigger>
        <PopoverContent className="w-auto p-3" align="start">
          <div className="flex items-center gap-2">
            <input
              type="date"
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              className="h-8 rounded-md border border-border bg-surface px-2 text-xs text-ink [color-scheme:dark]"
            />
            <button
              type="button"
              className="h-8 rounded-md bg-accent-blue px-3 text-xs font-medium text-white hover:bg-accent-blue-hover"
              onClick={() => {
                onPatch(draft || null);
                setOpen(false);
              }}
            >
              Set
            </button>
            {value && (
              <button
                type="button"
                className="h-8 rounded-md px-2 text-xs text-ink-muted hover:bg-wash-hover hover:text-ink"
                onClick={() => {
                  onPatch(null);
                  setOpen(false);
                }}
              >
                Clear
              </button>
            )}
          </div>
        </PopoverContent>
      </Popover>
    </span>
  );
}

/**
 * The inline property strip (C19T3), spec-exact: margin-top 18px, 1px
 * top+bottom hairlines, min-height 49px; items 27px, padding 0 12px
 * (first 0), 1px right border (last none), 10px secondary, gap 7px:
 * State | Priority | Assignees | Start date | Due date — all editable
 * inline (PATCH state/priority/start_date/target_date all exist).
 */
export default function PropertyStrip({
  issue,
  states,
  members,
  onPatch,
  onToggleAssignee,
  disabled,
}: {
  issue: Issue;
  states: IssueState[];
  members: Member[] | null;
  onPatch: (body: Record<string, unknown>) => void;
  onToggleAssignee: (id: string, currently: boolean) => void;
  disabled?: boolean;
}) {
  const state = states.find((s) => s.id === issue.state_id);
  const [stateOpen, setStateOpen] = useState(false);
  const [priorityOpen, setPriorityOpen] = useState(false);

  return (
    <div className="mt-[18px] flex min-h-[49px] flex-wrap items-center border-y border-border-hairline">
      <span className={itemCls}>
        <Popover open={stateOpen} onOpenChange={setStateOpen}>
          <PopoverTrigger disabled={disabled} className={triggerCls}>
            {state ? (
              <>
                <StateDot state={state} />
                <span>{state.name}</span>
              </>
            ) : (
              <span className="text-ink-muted">Select state</span>
            )}
          </PopoverTrigger>
          <PopoverContent className="w-52 p-1" align="start">
            {states.map((s) => (
              <button
                key={s.id}
                type="button"
                onClick={() => {
                  onPatch({ state_id: s.id });
                  setStateOpen(false);
                }}
                className="flex w-full items-center gap-2 rounded-[4px] px-2 py-1.5 text-left text-xs text-ink-secondary hover:bg-wash-hover hover:text-ink"
              >
                <StateDot state={s} />
                <span className="flex-1">{s.name}</span>
                {s.id === issue.state_id && <Check className="h-3.5 w-3.5 text-ink-muted" />}
              </button>
            ))}
          </PopoverContent>
        </Popover>
      </span>

      <span className={itemCls}>
        <Popover open={priorityOpen} onOpenChange={setPriorityOpen}>
          <PopoverTrigger disabled={disabled} className={triggerCls}>
            <PriorityBars value={issue.priority} />
            <span>{priorityLabel(issue.priority)}</span>
          </PopoverTrigger>
          <PopoverContent className="w-44 p-1" align="start">
            {[0, 1, 2, 3, 4].map((p) => (
              <button
                key={p}
                type="button"
                onClick={() => {
                  onPatch({ priority: p });
                  setPriorityOpen(false);
                }}
                className="flex w-full items-center gap-2 rounded-[4px] px-2 py-1.5 text-left text-xs text-ink-secondary hover:bg-wash-hover hover:text-ink"
              >
                <PriorityBars value={p} />
                <span className="flex-1">{priorityLabel(p)}</span>
                {p === issue.priority && <Check className="h-3.5 w-3.5 text-ink-muted" />}
              </button>
            ))}
          </PopoverContent>
        </Popover>
      </span>

      <span className={itemCls}>
        <AssigneePicker
          members={members}
          assigned={issue.assignees}
          onToggle={onToggleAssignee}
          disabled={disabled}
          triggerClassName="h-[27px] gap-[7px] rounded-[4px] border-0 bg-transparent px-1 text-[10px] font-normal text-ink-secondary shadow-none hover:bg-wash-hover hover:text-ink"
          triggerContent={
            <>
              <User className="h-[13px] w-[13px]" strokeWidth={1.8} />
              {issue.assignees.length === 0 ? (
                <span>Add assignees</span>
              ) : (
                <span>
                  {issue.assignees.length} assignee{issue.assignees.length > 1 ? "s" : ""}
                </span>
              )}
            </>
          }
        />
      </span>

      <DateItem
        label="Start date"
        value={issue.start_date}
        disabled={disabled}
        onPatch={(v) => onPatch({ start_date: v })}
      />
      <DateItem
        label="Due date"
        value={issue.target_date}
        disabled={disabled}
        onPatch={(v) => onPatch({ target_date: v })}
      />
    </div>
  );
}
