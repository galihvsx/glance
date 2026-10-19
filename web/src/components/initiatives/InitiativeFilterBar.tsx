// InitiativeFilterBar (C73T1): rich filter bar for the Initiatives page,
// mirroring the FilterPanel component family (components/issue/FilterPanel.tsx):
// a Filters popover with a count badge and Clear all, plus an always-visible
// search input and sort dropdown. Every control writes to URL search params
// via useInitiativeFilters, so filter state is shareable.

import { useState } from "react";
import { Filter, Search, X } from "lucide-react";
import { Button, buttonVariants } from "../ui/button";
import { Checkbox } from "../ui/checkbox";
import { Input } from "../ui/input";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";
import { Separator } from "../ui/separator";
import { NativeSelect, NativeSelectOption } from "../ui/native-select";
import { cn } from "cn";
import {
  INITIATIVE_SORT_LABELS,
  INITIATIVE_SORT_OPTIONS,
  INITIATIVE_STATUSES,
  initiativeActiveFilterCount,
  type InitiativeFilters,
  type InitiativeSortOption,
} from "../../lib/initiativeFilters";
import type { Member, Project } from "../../lib/types";

/** Status chip dot colors: state/accent colors, never hardcoded grays
 *  (same mapping as the initiative status chips on the cards). */
function initiativeStatusColor(status: string): string {
  switch (status) {
    case "active":
      return "#4f8ef7"; // accent blue
    case "completed":
      return "#42c88a"; // accent green
    default:
      return "#777780"; // muted (archived)
  }
}

function capitalize(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1);
}

function SectionTitle({ children }: { children: string }) {
  return (
    <p className="px-2 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
      {children}
    </p>
  );
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
      {hint && <span className="text-xs text-muted-foreground">{hint}</span>}
    </label>
  );
}

function StatusChip({
  status,
  active,
  onToggle,
}: {
  status: string;
  active: boolean;
  onToggle: () => void;
}) {
  return (
    <button
      type="button"
      aria-pressed={active}
      onClick={onToggle}
      className={cn(
        "inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs transition-colors",
        active
          ? "border-primary bg-primary/10 font-medium text-ink"
          : "border-border text-ink-muted hover:border-primary/50 hover:text-ink",
      )}
    >
      <span
        className="h-2 w-2 rounded-full"
        style={{ backgroundColor: initiativeStatusColor(status) }}
      />
      {capitalize(status)}
    </button>
  );
}

function toggleStatus(
  list: InitiativeFilters["statuses"],
  v: InitiativeFilters["statuses"][number],
): InitiativeFilters["statuses"] {
  return list.includes(v) ? list.filter((x) => x !== v) : [...list, v];
}

export default function InitiativeFilterBar({
  filters,
  onChange,
  onClear,
  members,
  projects,
  searchInput,
  onSearchInput,
}: {
  filters: InitiativeFilters;
  onChange: (patch: Partial<InitiativeFilters>) => void;
  onClear: () => void;
  members: Member[];
  projects: Project[];
  /** Local (debounced upstream) search box value. */
  searchInput: string;
  onSearchInput: (v: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const count = initiativeActiveFilterCount(filters);

  return (
    <div className="flex flex-wrap items-center gap-2">
      <div className="relative min-w-0 flex-1 basis-48">
        <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
        <Input
          value={searchInput}
          onChange={(e) => onSearchInput(e.target.value)}
          placeholder="Search initiatives…"
          aria-label="Search initiatives"
          className="h-8 pl-8 text-sm"
        />
      </div>

      <NativeSelect
        size="sm"
        aria-label="Sort initiatives"
        value={filters.sort}
        onChange={(e) =>
          onChange({ sort: e.target.value as InitiativeSortOption })
        }
      >
        {INITIATIVE_SORT_OPTIONS.map((o) => (
          <NativeSelectOption key={o} value={o}>
            {INITIATIVE_SORT_LABELS[o]}
          </NativeSelectOption>
        ))}
      </NativeSelect>

      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger
          className={cn(buttonVariants({ variant: "outline", size: "sm" }), "gap-1.5")}
        >
          <Filter className="h-3.5 w-3.5" />
          Filters
          {count > 0 && (
            <span className="flex h-5 min-w-5 items-center justify-center rounded-full bg-primary px-1 text-[11px] font-medium text-primary-foreground">
              {count}
            </span>
          )}
        </PopoverTrigger>
        <PopoverContent
          className="max-h-[70vh] w-72 overflow-y-auto p-2"
          align="start"
        >
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

          <SectionTitle>Status</SectionTitle>
          <div className="mb-1 flex flex-wrap gap-1.5 px-2 py-1.5">
            {INITIATIVE_STATUSES.map((s) => (
              <StatusChip
                key={s}
                status={s}
                active={filters.statuses.includes(s)}
                onToggle={() => onChange({ statuses: toggleStatus(filters.statuses, s) })}
              />
            ))}
          </div>

          <Separator className="my-1" />
          <SectionTitle>Created by</SectionTitle>
          <div className="mb-1 max-h-40 overflow-y-auto">
            {members.map((m) => (
              <CheckRow
                key={m.id}
                checked={filters.createdBy === m.id}
                onChange={() =>
                  onChange({
                    createdBy: filters.createdBy === m.id ? "" : m.id,
                  })
                }
                label={m.name ?? m.email}
                hint={m.name ? m.email : undefined}
              />
            ))}
            {members.length === 0 && (
              <p className="px-2 py-1 text-xs text-muted-foreground">
                No members in this workspace.
              </p>
            )}
          </div>

          <Separator className="my-1" />
          <SectionTitle>Project</SectionTitle>
          <div className="mb-1 max-h-40 overflow-y-auto">
            {projects.map((p) => (
              <CheckRow
                key={p.id}
                checked={filters.projectId === p.id}
                onChange={() =>
                  onChange({
                    projectId: filters.projectId === p.id ? "" : p.id,
                  })
                }
                label={p.name}
                hint={p.identifier}
              />
            ))}
            {projects.length === 0 && (
              <p className="px-2 py-1 text-xs text-muted-foreground">
                No projects in this workspace.
              </p>
            )}
          </div>

          <Separator className="my-1" />
          <SectionTitle>Created</SectionTitle>
          <div className="mb-1 px-2 py-1">
            <div className="flex items-center gap-1.5">
              <Input
                type="date"
                value={filters.createdAfter}
                max={filters.createdBefore || undefined}
                onChange={(e) => onChange({ createdAfter: e.target.value })}
                className="h-8 text-xs"
                aria-label="Created from"
              />
              <span className="text-xs text-muted-foreground">→</span>
              <Input
                type="date"
                value={filters.createdBefore}
                min={filters.createdAfter || undefined}
                onChange={(e) => onChange({ createdBefore: e.target.value })}
                className="h-8 text-xs"
                aria-label="Created to"
              />
            </div>
          </div>
        </PopoverContent>
      </Popover>

      {count > 0 && (
        <Button
          variant="ghost"
          size="sm"
          className="h-7 gap-1 text-xs"
          onClick={onClear}
        >
          <X className="h-3 w-3" />
          Clear all
        </Button>
      )}
    </div>
  );
}
