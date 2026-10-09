import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Filter, X } from "lucide-react";
import { Button, buttonVariants } from "../ui/button";
import { Checkbox } from "../ui/checkbox";
import { Input } from "../ui/input";
import { Label } from "../ui/label";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";
import { Separator } from "../ui/separator";
import { cn } from "cn";
import { api } from "../../lib/api";
import {
  activeFilterCount,
  type IssueFilters,
} from "../../lib/filters";
import type { IssueState, Label as ProjectLabel, Member } from "../../lib/types";
import { priorityLabel } from "../../lib/types";

interface EstimatePoint {
  id: string;
  key: string;
  value: number;
}

interface Estimate {
  id: string;
  name: string;
  points: EstimatePoint[];
}

function SectionTitle({ children }: { children: string }) {
  return (
    <p className="px-2 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
      {children}
    </p>
  );
}

function toggle<T>(list: T[], v: T): T[] {
  return list.includes(v) ? list.filter((x) => x !== v) : [...list, v];
}

function CheckRow({
  checked,
  onChange,
  label,
  hint,
}: {
  checked: boolean;
  onChange: () => void;
  label: string;
  hint?: string;
}) {
  return (
    <label className="flex cursor-pointer items-center gap-2 rounded px-2 py-1.5 text-sm hover:bg-muted">
      <Checkbox checked={checked} onCheckedChange={onChange} />
      <span className="flex-1 truncate">{label}</span>
      {hint && (
        <span className="text-xs text-muted-foreground">{hint}</span>
      )}
    </label>
  );
}

function DateRange({
  label,
  after,
  before,
  onAfter,
  onBefore,
}: {
  label: string;
  after: string;
  before: string;
  onAfter: (v: string) => void;
  onBefore: (v: string) => void;
}) {
  return (
    <div className="px-2 py-1">
      <p className="mb-1 text-xs text-muted-foreground">{label}</p>
      <div className="flex items-center gap-1.5">
        <Input
          type="date"
          value={after}
          max={before || undefined}
          onChange={(e) => onAfter(e.target.value)}
          className="h-8 text-xs"
          aria-label={`${label} after`}
        />
        <span className="text-xs text-muted-foreground">→</span>
        <Input
          type="date"
          value={before}
          min={after || undefined}
          onChange={(e) => onBefore(e.target.value)}
          className="h-8 text-xs"
          aria-label={`${label} before`}
        />
      </div>
    </div>
  );
}

/** Rich filter panel shared by the list, board, and spreadsheet views.
 *  Every control applies live (URL-backed via useIssueFilters) — there is
 *  no Apply button. Dimensions: state, priority (multi), labels (multi),
 *  assignees (multi + unassigned), estimate (multi + none), date ranges
 *  (created / updated / due), subscribed-by-me. */
export default function FilterPanel({
  slug,
  identifier,
  filters,
  onChange,
  onClear,
}: {
  slug: string;
  identifier: string;
  filters: IssueFilters;
  onChange: (patch: Partial<IssueFilters>) => void;
  onClear: () => void;
}) {
  const [open, setOpen] = useState(false);
  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;

  const statesQuery = useQuery({
    queryKey: ["states", slug, identifier],
    queryFn: () =>
      api.get<{ states: IssueState[] }>(`${base}/states`).then((d) => d.states),
  });
  const membersQuery = useQuery({
    queryKey: ["members", slug],
    queryFn: () =>
      api
        .get<{ members: Member[] }>(
          `/api/v1/workspaces/${encodeURIComponent(slug)}/members`,
        )
        .then((d) => d.members),
  });
  const labelsQuery = useQuery({
    queryKey: ["labels", slug, identifier],
    queryFn: () =>
      api
        .get<{ labels: ProjectLabel[] }>(`${base}/labels`)
        .then((d) => d.labels),
  });
  const estimatesQuery = useQuery({
    queryKey: ["estimates", slug, identifier],
    queryFn: () =>
      api
        .get<{ estimates: Estimate[] }>(`${base}/estimates`)
        .then((d) => d.estimates),
  });

  const states = [...(statesQuery.data ?? [])].sort(
    (a, b) => a.sequence - b.sequence,
  );
  const members = membersQuery.data ?? [];
  const labels = labelsQuery.data ?? [];
  const estimatePoints = (estimatesQuery.data ?? []).flatMap((e) =>
    e.points.map((p) => ({ ...p, scale: e.name })),
  );

  const count = activeFilterCount(filters);

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        className={cn(
          buttonVariants({ variant: "outline", size: "sm" }),
          "gap-1.5",
        )}
      >
        <Filter className="h-3.5 w-3.5" />
        Filters
        {count > 0 && (
          <span className="flex h-5 min-w-5 items-center justify-center rounded-full bg-primary px-1 text-[11px] font-medium text-primary-foreground">
            {count}
          </span>
        )}
      </PopoverTrigger>
      <PopoverContent className="max-h-[70vh] w-72 overflow-y-auto p-2" align="start">
        <div className="mb-1 flex items-center justify-between px-2 py-1">
          <span className="text-sm font-medium">Filters</span>
          {count > 0 && (
            <Button
              variant="ghost"
              size="sm"
              className="h-7 gap-1 text-xs"
              onClick={() => {
                onClear();
                setOpen(false);
              }}
            >
              <X className="h-3 w-3" />
              Clear all
            </Button>
          )}
        </div>

        <SectionTitle>State</SectionTitle>
        <div className="mb-1">
          {states.map((s) => (
            <CheckRow
              key={s.id}
              checked={filters.state === s.id}
              onChange={() =>
                onChange({ state: filters.state === s.id ? "" : s.id })
              }
              label={s.name}
            />
          ))}
          {states.length === 0 && (
            <p className="px-2 py-1 text-xs text-muted-foreground">
              No states yet.
            </p>
          )}
        </div>

        <Separator className="my-1" />
        <SectionTitle>Priority</SectionTitle>
        <div className="mb-1">
          {[4, 3, 2, 1, 0].map((p) => (
            <CheckRow
              key={p}
              checked={filters.priorities.includes(p)}
              onChange={() =>
                onChange({ priorities: toggle(filters.priorities, p) })
              }
              label={priorityLabel(p)}
            />
          ))}
        </div>

        <Separator className="my-1" />
        <SectionTitle>Labels</SectionTitle>
        <div className="mb-1 max-h-40 overflow-y-auto">
          {labels.map((l) => (
            <CheckRow
              key={l.id}
              checked={filters.labels.includes(l.id)}
              onChange={() => onChange({ labels: toggle(filters.labels, l.id) })}
              label={l.name}
            />
          ))}
          {labels.length === 0 && (
            <p className="px-2 py-1 text-xs text-muted-foreground">
              No labels in this project.
            </p>
          )}
        </div>

        <Separator className="my-1" />
        <SectionTitle>Assignees</SectionTitle>
        <div className="mb-1 max-h-40 overflow-y-auto">
          <CheckRow
            checked={filters.assignees.includes("none")}
            onChange={() => onChange({ assignees: toggle(filters.assignees, "none") })}
            label="Unassigned"
          />
          {members.map((m) => (
            <CheckRow
              key={m.id}
              checked={filters.assignees.includes(m.id)}
              onChange={() =>
                onChange({ assignees: toggle(filters.assignees, m.id) })
              }
              label={m.name ?? m.email}
            />
          ))}
        </div>

        <Separator className="my-1" />
        <SectionTitle>Estimate</SectionTitle>
        <div className="mb-1 max-h-40 overflow-y-auto">
          <CheckRow
            checked={filters.estimates.includes("none")}
            onChange={() =>
              onChange({ estimates: toggle(filters.estimates, "none") })
            }
            label="No estimate"
          />
          {estimatePoints.map((p) => (
            <CheckRow
              key={p.id}
              checked={filters.estimates.includes(p.id)}
              onChange={() =>
                onChange({ estimates: toggle(filters.estimates, p.id) })
              }
              label={p.key}
              hint={p.scale}
            />
          ))}
          {estimatePoints.length === 0 && (
            <p className="px-2 py-1 text-xs text-muted-foreground">
              No estimate scales in this project.
            </p>
          )}
        </div>

        <Separator className="my-1" />
        <SectionTitle>Dates</SectionTitle>
        <div className="mb-1">
          <DateRange
            label="Created"
            after={filters.createdAfter}
            before={filters.createdBefore}
            onAfter={(v) => onChange({ createdAfter: v })}
            onBefore={(v) => onChange({ createdBefore: v })}
          />
          <DateRange
            label="Updated"
            after={filters.updatedAfter}
            before={filters.updatedBefore}
            onAfter={(v) => onChange({ updatedAfter: v })}
            onBefore={(v) => onChange({ updatedBefore: v })}
          />
          <DateRange
            label="Due"
            after={filters.dueAfter}
            before={filters.dueBefore}
            onAfter={(v) => onChange({ dueAfter: v })}
            onBefore={(v) => onChange({ dueBefore: v })}
          />
        </div>

        <Separator className="my-1" />
        <div className="px-2 py-1">
          <Label className="flex cursor-pointer items-center gap-2 text-sm font-normal">
            <Checkbox
              checked={filters.subscribed}
              onCheckedChange={(v) => onChange({ subscribed: v === true })}
            />
            Subscribed by me
          </Label>
        </div>
      </PopoverContent>
    </Popover>
  );
}
