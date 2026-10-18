// Shared reaction row (C67T1): group chips + the picker trigger +
// the error surface, with the same anatomy as PageReactions. Purely
// presentational — callers own the data (query or prop) and the
// toggle/pick mutations. `groups` must already be normalized and
// sorted (lib/reactions).

import { useState } from "react";
import { SmilePlus } from "lucide-react";
import type { ReactionGroup } from "../../lib/types";
import { cn } from "../../lib/utils";
import { Alert, AlertDescription } from "../ui/alert";
import { Button } from "../ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";
import EmojiGrid from "./EmojiGrid";

export default function ReactionRow({
  groups,
  canWrite,
  pending,
  error,
  onToggle,
  onPick,
  size = "md",
  className,
}: {
  groups: ReactionGroup[];
  canWrite: boolean;
  pending: boolean;
  error: string | null;
  onToggle: (group: ReactionGroup) => void;
  onPick: (emoji: string) => void;
  /** "sm" for comment nodes, "md" for the issue-level row. */
  size?: "sm" | "md";
  className?: string;
}) {
  const [pickerOpen, setPickerOpen] = useState(false);

  function pick(emoji: string) {
    setPickerOpen(false);
    onPick(emoji);
  }

  return (
    <div className={cn(size === "md" && "mt-3", className)}>
      {error && (
        <Alert variant="destructive" className="mb-2">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      <div
        className="flex flex-wrap items-center gap-1.5"
        aria-label="Reactions"
      >
        {groups.map((g) => (
          <button
            key={g.emoji}
            type="button"
            onClick={() => onToggle(g)}
            disabled={!canWrite || pending}
            title={g.reacted ? "Remove your reaction" : "React"}
            aria-label={`${g.emoji} ${g.count}`}
            aria-pressed={g.reacted}
            className={cn(
              "flex items-center gap-1 rounded-full border px-2 py-0.5 leading-6 transition-colors",
              size === "md" ? "text-[13px]" : "text-[12px]",
              g.reacted
                ? "border-primary/50 bg-primary/10 text-foreground"
                : "border-border bg-muted/40 text-muted-foreground hover:bg-muted",
              canWrite && "cursor-pointer",
            )}
          >
            <span aria-hidden="true">{g.emoji}</span>
            <span className="tabular-nums">{g.count}</span>
          </button>
        ))}
        {canWrite && (
          <Popover open={pickerOpen} onOpenChange={setPickerOpen}>
            <PopoverTrigger
              render={
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  aria-label="Add reaction"
                  className={cn(
                    "rounded-full px-2 text-muted-foreground hover:text-foreground",
                    size === "md" ? "h-7" : "h-6",
                  )}
                />
              }
            >
              <SmilePlus
                className={size === "md" ? "size-4" : "size-3.5"}
                aria-hidden="true"
              />
            </PopoverTrigger>
            <PopoverContent align="start" className="w-80 p-0">
              <EmojiGrid onPick={pick} disabled={pending} />
            </PopoverContent>
          </Popover>
        )}
      </div>
    </div>
  );
}
