// C81T1: generic multi-select checkbox popover used by the widget
// dialog's project/assignee/priority/state/label pickers. Renders the
// same CheckRow language as the issue FilterPanel (reused, not
// duplicated).
import { useState, type ReactNode } from "react";
import { buttonVariants } from "../ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";
import { ScrollArea } from "../ui/scroll-area";
import { Skeleton } from "../ui/skeleton";
import { CheckRow } from "../issue/FilterPanel";
import { cn } from "cn";

export interface FilterPickerOption {
  id: string;
  label: string;
  /** Secondary text on the right (e.g. the project a state belongs to). */
  hint?: string;
  /** Leading node, e.g. a state color dot or label swatch. */
  badge?: ReactNode;
}

export default function FilterPicker({
  title,
  options,
  selected,
  onToggle,
  onClear,
  loading,
  emptyText,
  triggerLabel,
}: {
  title: string;
  options: FilterPickerOption[];
  selected: string[];
  onToggle: (id: string) => void;
  onClear: () => void;
  loading?: boolean;
  emptyText: string;
  triggerLabel: string;
}) {
  const [open, setOpen] = useState(false);
  const selectedSet = new Set(selected);

  return (
    <Popover open={open} onOpenChange={setOpen}>
      {/* Base-UI Trigger renders its own button — style it with the
          shadcn button variants directly, like AssigneePicker does. */}
      <PopoverTrigger
        className={cn(
          buttonVariants({ variant: "outline", size: "sm" }),
          "w-full justify-start gap-2",
        )}
      >
        <span className="truncate">{triggerLabel}</span>
        {selected.length > 0 && (
          <span className="ml-auto inline-flex size-5 shrink-0 items-center justify-center rounded-full bg-primary text-[10px] font-semibold text-primary-foreground">
            {selected.length}
          </span>
        )}
      </PopoverTrigger>
      <PopoverContent className="w-72 p-2" align="start">
        <div className="flex items-center justify-between px-2 pb-1">
          <p className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
            {title}
          </p>
          {selected.length > 0 && (
            <button
              type="button"
              onClick={onClear}
              className="text-[11px] text-muted-foreground hover:text-foreground"
            >
              Clear
            </button>
          )}
        </div>
        {loading ? (
          <div className="space-y-2 p-2">
            <Skeleton className="h-6 w-full" />
            <Skeleton className="h-6 w-full" />
          </div>
        ) : options.length === 0 ? (
          <p className="p-2 text-sm text-muted-foreground">{emptyText}</p>
        ) : (
          <ScrollArea className="max-h-64">
            {options.map((o) => (
              <CheckRow
                key={o.id}
                checked={selectedSet.has(o.id)}
                onChange={() => onToggle(o.id)}
                label={o.label}
                hint={o.hint}
                badge={o.badge}
              />
            ))}
          </ScrollArea>
        )}
      </PopoverContent>
    </Popover>
  );
}
