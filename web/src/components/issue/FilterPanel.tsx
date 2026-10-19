import { useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { Filter, X } from "lucide-react";
import { Badge } from "../ui/badge";
import { Button, buttonVariants } from "../ui/button";
import { Checkbox } from "../ui/checkbox";
import { Input } from "../ui/input";
import { Label } from "../ui/label";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";
import { Separator } from "../ui/separator";
import { Skeleton } from "../ui/skeleton";
import { cn } from "cn";
import { api } from "../../lib/api";
import {
  activeFilterCount,
  type IssueFilters,
} from "../../lib/filters";
import type {
  Epic,
  Issue,
  IssueListResult,
  IssueState,
  Label as ProjectLabel,
  Member,
} from "../../lib/types";
import { priorityLabel } from "../../lib/types";
import { useDebouncedValue } from "../../lib/useDebouncedValue";

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

/** Section heading with an optional per-section clear (C75T1: every
 *  multi-value dimension can be reset on its own; the global "Clear all"
 *  in the header still resets everything). */
function SectionHeader({
  title,
  active,
  onClear,
}: {
  title: string;
  active: boolean;
  onClear: () => void;
}) {
  return (
    <div className="flex items-center justify-between px-2">
      <p className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
        {title}
      </p>
      {active && (
        <button
          type="button"
          onClick={onClear}
          aria-label={`Clear ${title} filter`}
          className="text-[11px] text-muted-foreground hover:text-foreground"
        >
          Clear
        </button>
      )}
    </div>
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
  badge,
}: {
  checked: boolean;
  onChange: () => void;
  label: string;
  hint?: string;
  /** Optional leading node, e.g. the issue display_id badge (C75T1). */
  badge?: ReactNode;
}) {
  return (
    <label className="flex cursor-pointer items-center gap-2 rounded px-2 py-1.5 text-sm hover:bg-muted">
      <Checkbox checked={checked} onCheckedChange={onChange} />
      {badge}
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
 *  assignees (multi + unassigned), estimate (multi + none), parent
 *  (multi + top-level, C75T1), epic (multi + none, C75T1), date ranges
 *  (created / updated / due), subscribed-by-me. */
export default function FilterPanel({
  slug,
  identifier,
  filters,
  onChange,
  onClear,
  triggerClassName,
}: {
  slug: string;
  identifier: string;
  filters: IssueFilters;
  onChange: (patch: Partial<IssueFilters>) => void;
  onClear: () => void;
  /** Extra classes for the popover trigger (C19T4: PageHeader restyles it). */
  triggerClassName?: string;
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

  // C75T1: epics list (active + archived, archived last) for the Epic
  // filter section. Repeatable status= is the same form the Epics page
  // uses (epicFilters.ts).
  const epicStatusParams = "status=active&status=archived";
  const epicsQuery = useQuery({
    queryKey: ["epics", slug, identifier, epicStatusParams],
    queryFn: () =>
      api
        .get<{ epics: Epic[] }>(`${base}/epics?${epicStatusParams}`)
        .then((d) => d.epics),
  });
  const epics = [...(epicsQuery.data ?? [])].sort(
    (a, b) =>
      Number(!!a.archived_at) - Number(!!b.archived_at) ||
      a.name.localeCompare(b.name),
  );

  // C75T1: searchable parent-issue picker. Mirrors ParentPicker's search
  // (250ms debounce, per_page 10) but renders as toggle rows; the query
  // only runs while the popover is open.
  const [parentSearch, setParentSearch] = useState("");
  const debouncedParentSearch = useDebouncedValue(parentSearch.trim(), 250);
  const parentSearchQuery = useQuery({
    queryKey: ["parent-filter-search", slug, identifier, debouncedParentSearch],
    queryFn: () => {
      const p = new URLSearchParams();
      p.set("per_page", "10");
      if (debouncedParentSearch) p.set("q", debouncedParentSearch);
      return api
        .get<IssueListResult>(`${base}/issues?${p}`)
        .then((d) => d.results);
    },
    enabled: open,
    staleTime: 30_000,
  });
  const searchResults = parentSearchQuery.data ?? [];

  // Selected parents that fell outside the current search page still need
  // their names rendered (e.g. reopening the panel with a pasted URL).
  // Resolve them individually; unresolvable ids fall back to a raw-id row
  // so the stale selection stays clearable.
  const selectedParentIds = filters.parents.filter((id) => id !== "none");
  const missingParentIds = selectedParentIds.filter(
    (id) => !searchResults.some((r) => r.id === id),
  );
  const missingParentsQuery = useQuery({
    queryKey: [
      "parent-filter-missing",
      slug,
      identifier,
      ...[...missingParentIds].sort(),
    ],
    queryFn: async () => {
      const settled = await Promise.allSettled(
        missingParentIds.map((id) =>
          api.get<Issue>(`${base}/issues/${encodeURIComponent(id)}`),
        ),
      );
      return settled.flatMap((r) =>
        r.status === "fulfilled" ? [r.value] : [],
      );
    },
    enabled: open && missingParentIds.length > 0,
    staleTime: 30_000,
    retry: false,
  });
  const resolvedMissingParents = missingParentsQuery.data ?? [];
  const parentOptions: Issue[] = [
    ...searchResults,
    ...resolvedMissingParents.filter(
      (m) => !searchResults.some((r) => r.id === m.id),
    ),
  ];
  const unresolvableParentIds = missingParentIds.filter(
    (id) =>
      !resolvedMissingParents.some((m) => m.id === id) &&
      !missingParentsQuery.isPending,
  );

  // Selected epics that are no longer in the project (deleted) also get a
  // raw-id fallback row so the stale selection stays clearable.
  const missingEpicIds = filters.epics.filter(
    (id) => id !== "none" && !epics.some((e) => e.id === id),
  );

  const count = activeFilterCount(filters);

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        className={cn(
          buttonVariants({ variant: "outline", size: "sm" }),
          "gap-1.5",
          triggerClassName,
        )}
      >
        <Filter className="h-3.5 w-3.5" />
        Filter
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

        <SectionHeader
          title="State"
          active={filters.state !== ""}
          onClear={() => onChange({ state: "" })}
        />
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
        <SectionHeader
          title="Priority"
          active={filters.priorities.length > 0}
          onClear={() => onChange({ priorities: [] })}
        />
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
        <SectionHeader
          title="Labels"
          active={filters.labels.length > 0}
          onClear={() => onChange({ labels: [] })}
        />
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
        <SectionHeader
          title="Assignees"
          active={filters.assignees.length > 0}
          onClear={() => onChange({ assignees: [] })}
        />
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
        <SectionHeader
          title="Estimate"
          active={filters.estimates.length > 0}
          onClear={() => onChange({ estimates: [] })}
        />
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
        <SectionHeader
          title="Parent"
          active={filters.parents.length > 0}
          onClear={() => onChange({ parents: [] })}
        />
        <div className="mb-1">
          <div className="px-2 py-1">
            <Input
              value={parentSearch}
              onChange={(e) => setParentSearch(e.target.value)}
              placeholder="Search issues…"
              className="h-8 text-xs"
              aria-label="Search parent issues"
            />
          </div>
          <CheckRow
            checked={filters.parents.includes("none")}
            onChange={() => onChange({ parents: toggle(filters.parents, "none") })}
            label="No parent (top-level)"
          />
          <div className="max-h-40 overflow-y-auto">
            {parentSearchQuery.isPending ? (
              <div className="space-y-1 px-2 py-1">
                <Skeleton className="h-8 w-full" />
                <Skeleton className="h-8 w-full" />
              </div>
            ) : (
              <>
                {parentOptions.map((issue) => (
                  <CheckRow
                    key={issue.id}
                    checked={filters.parents.includes(issue.id)}
                    onChange={() =>
                      onChange({ parents: toggle(filters.parents, issue.id) })
                    }
                    label={issue.name}
                    badge={
                      <Badge
                        variant="outline"
                        className="font-mono text-[10px] shrink-0"
                      >
                        {issue.display_id}
                      </Badge>
                    }
                  />
                ))}
                {unresolvableParentIds.map((id) => (
                  <CheckRow
                    key={id}
                    checked
                    onChange={() =>
                      onChange({ parents: toggle(filters.parents, id) })
                    }
                    label={`${id.slice(0, 8)}…`}
                    hint="unknown"
                  />
                ))}
                {parentOptions.length === 0 &&
                  unresolvableParentIds.length === 0 && (
                    <p className="px-2 py-1 text-xs text-muted-foreground">
                      {debouncedParentSearch
                        ? "No issues match."
                        : "No issues in this project."}
                    </p>
                  )}
              </>
            )}
          </div>
        </div>

        <Separator className="my-1" />
        <SectionHeader
          title="Epic"
          active={filters.epics.length > 0}
          onClear={() => onChange({ epics: [] })}
        />
        <div className="mb-1 max-h-40 overflow-y-auto">
          <CheckRow
            checked={filters.epics.includes("none")}
            onChange={() => onChange({ epics: toggle(filters.epics, "none") })}
            label="No epic"
          />
          {epics.map((e) => (
            <CheckRow
              key={e.id}
              checked={filters.epics.includes(e.id)}
              onChange={() => onChange({ epics: toggle(filters.epics, e.id) })}
              label={e.name}
              hint={e.archived_at ? "archived" : `${e.issue_count} issues`}
            />
          ))}
          {missingEpicIds.map((id) => (
            <CheckRow
              key={id}
              checked
              onChange={() => onChange({ epics: toggle(filters.epics, id) })}
              label={`${id.slice(0, 8)}…`}
              hint="unknown"
            />
          ))}
          {epicsQuery.isPending && (
            <div className="space-y-1 px-2 py-1">
              <Skeleton className="h-8 w-full" />
              <Skeleton className="h-8 w-full" />
            </div>
          )}
          {!epicsQuery.isPending &&
            epics.length === 0 &&
            missingEpicIds.length === 0 && (
              <p className="px-2 py-1 text-xs text-muted-foreground">
                No epics in this project.
              </p>
            )}
        </div>

        <Separator className="my-1" />
        <SectionHeader
          title="Dates"
          active={!!(
            filters.createdAfter ||
            filters.createdBefore ||
            filters.updatedAfter ||
            filters.updatedBefore ||
            filters.dueAfter ||
            filters.dueBefore
          )}
          onClear={() =>
            onChange({
              createdAfter: "",
              createdBefore: "",
              updatedAfter: "",
              updatedBefore: "",
              dueAfter: "",
              dueBefore: "",
            })
          }
        />
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
        <div className="px-2 py-1">
          <Label className="flex cursor-pointer items-center gap-2 text-sm font-normal">
            <Checkbox
              checked={filters.archived}
              onCheckedChange={(v) => onChange({ archived: v === true })}
            />
            Show archived
          </Label>
        </div>
      </PopoverContent>
    </Popover>
  );
}
