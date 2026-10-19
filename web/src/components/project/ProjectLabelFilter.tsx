// ProjectLabelFilter (C76T1): multi-select label filter for the
// Projects page header — OR semantics across the selected ids, driving
// ?label= via useProjectLabelFilters (URL-backed). Selected ids that no
// longer exist (pasted URL, deleted label) get a raw-id fallback row so
// the stale selection stays clearable — the same discipline as the
// C75 parent/epic filter rows.

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Tags, X } from "lucide-react";
import { Button, buttonVariants } from "../ui/button";
import { Checkbox } from "../ui/checkbox";
import { Label } from "../ui/label";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";
import { Skeleton } from "../ui/skeleton";
import { cn } from "cn";
import { fetchProjectLabels, projectLabelKeys } from "../../lib/projectLabels";

function toggle(list: string[], v: string): string[] {
  return list.includes(v) ? list.filter((x) => x !== v) : [...list, v];
}

export default function ProjectLabelFilter({
  slug,
  selected,
  onChange,
  onClear,
  triggerClassName,
}: {
  slug: string;
  /** Selected project-label ids (URL-backed). */
  selected: string[];
  onChange: (ids: string[]) => void;
  /** Global clear (resets the whole filter). */
  onClear: () => void;
  /** Extra classes for the popover trigger (PageHeader restyles it). */
  triggerClassName?: string;
}) {
  const [open, setOpen] = useState(false);

  const labelsQuery = useQuery({
    queryKey: projectLabelKeys(slug).labels,
    queryFn: () => fetchProjectLabels(slug),
  });
  const labels = [...(labelsQuery.data ?? [])].sort((a, b) =>
    a.name.localeCompare(b.name),
  );

  const missingIds = selected.filter(
    (id) => !labels.some((l) => l.id === id),
  );

  const count = selected.length;

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        aria-label={`Filter projects by label${count > 0 ? ` (${count} active)` : ""}`}
        className={cn(
          buttonVariants({ variant: "outline", size: "sm" }),
          "gap-1.5",
          triggerClassName,
        )}
      >
        <Tags className="h-3.5 w-3.5" aria-hidden />
        Labels
        {count > 0 && (
          <span
            data-testid="project-label-filter-count"
            className="flex h-5 min-w-5 items-center justify-center rounded-full bg-primary px-1 text-[11px] font-medium text-primary-foreground"
          >
            {count}
          </span>
        )}
      </PopoverTrigger>
      <PopoverContent className="max-h-[70vh] w-72 overflow-y-auto p-2" align="start">
        <div className="mb-1 flex items-center justify-between px-2 py-1">
          <span className="text-sm font-medium">Labels</span>
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
              <X className="h-3 w-3" aria-hidden />
              Clear all
            </Button>
          )}
        </div>

        <div className="flex items-center justify-between px-2">
          <p className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
            Project labels
          </p>
          {count > 0 && (
            <button
              type="button"
              onClick={() => onChange([])}
              aria-label="Clear labels filter"
              className="text-[11px] text-muted-foreground hover:text-foreground"
            >
              Clear
            </button>
          )}
        </div>
        <div className="mb-1 max-h-64 overflow-y-auto">
          {labelsQuery.isPending ? (
            <div className="space-y-1 px-2 py-1">
              <Skeleton className="h-8 w-full" />
              <Skeleton className="h-8 w-full" />
            </div>
          ) : (
            <>
              {labels.map((l) => (
                <Label
                  key={l.id}
                  className="flex cursor-pointer items-center gap-2 rounded px-2 py-1.5 text-sm hover:bg-muted"
                >
                  <Checkbox
                    checked={selected.includes(l.id)}
                    onCheckedChange={() => onChange(toggle(selected, l.id))}
                  />
                  <span
                    className="h-2.5 w-2.5 shrink-0 rounded-full"
                    style={{ backgroundColor: l.color }}
                    aria-hidden
                  />
                  <span className="flex-1 truncate">{l.name}</span>
                </Label>
              ))}
              {missingIds.map((id) => (
                <Label
                  key={id}
                  className="flex cursor-pointer items-center gap-2 rounded px-2 py-1.5 text-sm hover:bg-muted"
                >
                  <Checkbox
                    checked
                    onCheckedChange={() => onChange(toggle(selected, id))}
                  />
                  <span className="flex-1 truncate font-mono text-xs">
                    {id.slice(0, 8)}…
                  </span>
                  <span className="text-xs text-muted-foreground">unknown</span>
                </Label>
              ))}
              {labels.length === 0 && missingIds.length === 0 && (
                <p className="px-2 py-1 text-xs text-muted-foreground">
                  No project labels in this workspace yet.
                </p>
              )}
            </>
          )}
        </div>
        <p className="px-2 py-1 text-[11px] text-muted-foreground">
          Projects carrying any of the selected labels.
        </p>
      </PopoverContent>
    </Popover>
  );
}
