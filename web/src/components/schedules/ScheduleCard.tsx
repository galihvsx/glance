import { CalendarClock, History, Pencil, Play, Trash2 } from "lucide-react";
import { Button } from "../ui/button";
import { Switch } from "../ui/switch";
import { Badge } from "../ui/badge";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "../ui/tooltip";
import {
  cadenceSummary,
  nextRunRelative,
  type RecurringSchedule,
} from "../../lib/schedules";

function absoluteTime(iso: string | null): string | null {
  if (!iso || iso.startsWith("0001-01-01")) return null;
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return null;
  return d.toLocaleString("en-US", {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
    timeZone: "UTC",
    timeZoneName: "short",
  });
}

export default function ScheduleCard({
  schedule,
  canEdit,
  toggling,
  runningNow,
  onToggle,
  onRunNow,
  onEdit,
  onDelete,
}: {
  schedule: RecurringSchedule;
  canEdit: boolean;
  toggling: boolean;
  runningNow: boolean;
  onToggle: () => void;
  onRunNow: () => void;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const s = schedule;
  const nextAbs = absoluteTime(s.next_run_at);
  const lastAbs = absoluteTime(s.last_run_at);
  const exhausted = !nextAbs;
  const paused = !s.active;

  return (
    <div className="rounded-[8px] border border-border bg-raised p-4">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <span className="truncate text-[13px] font-semibold text-ink">
              {s.name}
            </span>
            {paused && (
              <Tooltip>
                <TooltipTrigger>
                  <Badge variant="outline" className="text-[11px]">
                    Paused
                  </Badge>
                </TooltipTrigger>
                <TooltipContent className="max-w-[260px] text-[12px]">
                  Rules pause automatically when a label, assignee, state, or
                  creator reference goes stale — or when the schedule ends.
                  Edit the rule to fix it and switch it back on.
                </TooltipContent>
              </Tooltip>
            )}
            {exhausted && (
              <Badge variant="outline" className="text-[11px]">
                Ended
              </Badge>
            )}
          </div>
          <p className="mt-1 text-[12px] text-ink-muted">{cadenceSummary(s)}</p>
          <p className="mt-0.5 truncate text-[12px] text-ink-muted">
            {s.title_template}
          </p>
        </div>
        {canEdit && (
          <div className="flex shrink-0 items-center gap-2">
            <Switch
              checked={s.active}
              disabled={toggling}
              onCheckedChange={onToggle}
              aria-label={s.active ? `Pause ${s.name}` : `Resume ${s.name}`}
            />
          </div>
        )}
      </div>

      <div className="mt-3 flex flex-wrap items-center gap-x-5 gap-y-1 text-[12px] text-ink-muted">
        <span className="inline-flex items-center gap-1.5">
          <CalendarClock className="size-3.5" />
          {nextAbs ? (
            <>
              <span className="font-medium text-ink">
                {nextRunRelative(s.next_run_at)}
              </span>
              <span className="text-ink-muted">· {nextAbs}</span>
            </>
          ) : (
            <span className="text-ink-muted">
              {nextRunRelative(s.next_run_at)}
            </span>
          )}
        </span>
        <span className="inline-flex items-center gap-1.5">
          <History className="size-3.5" />
          {lastAbs ? (
            <>
              Last run <span className="text-ink">{lastAbs}</span>
            </>
          ) : (
            <span className="text-ink-muted">Never run</span>
          )}
          <span className="text-ink-muted">
            · {s.run_count} {s.run_count === 1 ? "run" : "runs"}
          </span>
        </span>
      </div>

      {canEdit && (
        <div className="mt-3 flex items-center gap-1.5 border-t border-border pt-3">
          <Button
            variant="ghost"
            size="sm"
            onClick={onRunNow}
            disabled={runningNow || paused || exhausted}
            title={
              paused
                ? "Resume the rule before running it now"
                : exhausted
                  ? "The schedule has no further runs"
                  : "Create the next occurrence now"
            }
          >
            <Play className="size-3.5" />
            {runningNow ? "Running…" : "Run now"}
          </Button>
          <Button variant="ghost" size="sm" onClick={onEdit}>
            <Pencil className="size-3.5" />
            Edit
          </Button>
          <Button
            variant="ghost"
            size="sm"
            onClick={onDelete}
            className="text-destructive hover:text-destructive"
          >
            <Trash2 className="size-3.5" />
            Delete
          </Button>
        </div>
      )}
    </div>
  );
}
