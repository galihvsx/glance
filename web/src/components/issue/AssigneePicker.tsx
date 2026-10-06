import { useState } from "react";
import { Users } from "lucide-react";
import type { IssueAssignee, Member } from "../../lib/types";
import { buttonVariants } from "../ui/button";
import { Checkbox } from "../ui/checkbox";
import { Label } from "../ui/label";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";
import { ScrollArea } from "../ui/scroll-area";
import { Skeleton } from "../ui/skeleton";
import { cn } from "cn";

/** Assignee picker: popover with member checkboxes. onToggle is called per
 *  (un)check; the caller owns the mutation + optimistic update. */
export default function AssigneePicker({
  members,
  assigned,
  onToggle,
  disabled,
}: {
  members: Member[] | null;
  assigned: IssueAssignee[];
  onToggle: (userId: string, currentlyAssigned: boolean) => void;
  disabled?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const assignedIds = new Set(assigned.map((a) => a.id));

  return (
    <Popover open={open} onOpenChange={setOpen}>
      {/* Base-UI Trigger renders its own button (no asChild) — style it
          with the shadcn button variants directly. */}
      <PopoverTrigger
        disabled={disabled}
        className={cn(buttonVariants({ variant: "outline", size: "sm" }), "gap-2")}
      >
        <Users className="h-4 w-4" />
        {assigned.length === 0
          ? "Assign"
          : `${assigned.length} assignee${assigned.length > 1 ? "s" : ""}`}
      </PopoverTrigger>
      <PopoverContent className="w-64 p-2" align="start">
        {members === null ? (
          <div className="space-y-2 p-2">
            <Skeleton className="h-6 w-full" />
            <Skeleton className="h-6 w-full" />
          </div>
        ) : members.length === 0 ? (
          <p className="p-2 text-sm text-muted-foreground">No members.</p>
        ) : (
          <ScrollArea className="max-h-64">
            {members.map((m) => {
              const checked = assignedIds.has(m.id);
              const name = m.name ?? m.email;
              return (
                <Label
                  key={m.id}
                  className="flex cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-accent"
                >
                  <Checkbox
                    checked={checked}
                    onCheckedChange={() => onToggle(m.id, checked)}
                  />
                  <span className="truncate">{name}</span>
                </Label>
              );
            })}
          </ScrollArea>
        )}
      </PopoverContent>
    </Popover>
  );
}
