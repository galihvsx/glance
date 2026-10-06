import { useState } from "react";
import { Tags } from "lucide-react";
import type { IssueLabel, Label as ProjectLabel } from "../../lib/types";
import { buttonVariants } from "../ui/button";
import { Checkbox } from "../ui/checkbox";
import { Label } from "../ui/label";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";
import { ScrollArea } from "../ui/scroll-area";
import { Skeleton } from "../ui/skeleton";
import { cn } from "cn";

/** Label picker: popover with label checkboxes. onToggle is called per
 *  (un)check; the caller owns the mutation + optimistic update. */
export default function LabelPicker({
  labels,
  applied,
  onToggle,
  disabled,
}: {
  labels: ProjectLabel[] | null;
  applied: IssueLabel[];
  onToggle: (labelId: string, currentlyApplied: boolean) => void;
  disabled?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const appliedIds = new Set(applied.map((l) => l.id));

  return (
    <Popover open={open} onOpenChange={setOpen}>
      {/* Base-UI Trigger renders its own button (no asChild) — style it
          with the shadcn button variants directly. */}
      <PopoverTrigger
        disabled={disabled}
        className={cn(buttonVariants({ variant: "outline", size: "sm" }), "gap-2")}
      >
        <Tags className="h-4 w-4" />
        {applied.length === 0
          ? "Labels"
          : `${applied.length} label${applied.length > 1 ? "s" : ""}`}
      </PopoverTrigger>
      <PopoverContent className="w-64 p-2" align="start">
        {labels === null ? (
          <div className="space-y-2 p-2">
            <Skeleton className="h-6 w-full" />
            <Skeleton className="h-6 w-full" />
          </div>
        ) : labels.length === 0 ? (
          <p className="p-2 text-sm text-muted-foreground">
            No labels yet. Create them in project settings.
          </p>
        ) : (
          <ScrollArea className="max-h-64">
            {labels.map((l) => {
              const checked = appliedIds.has(l.id);
              return (
                <Label
                  key={l.id}
                  className="flex cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-accent"
                >
                  <Checkbox
                    checked={checked}
                    onCheckedChange={() => onToggle(l.id, checked)}
                  />
                  <span
                    className="h-2.5 w-2.5 rounded-full"
                    style={{ backgroundColor: l.color }}
                    aria-hidden
                  />
                  <span className="truncate">{l.name}</span>
                </Label>
              );
            })}
          </ScrollArea>
        )}
      </PopoverContent>
    </Popover>
  );
}
