// Project icon emoji picker (C18T4): a popover grid over the curated
// EMOJI_LIST with search. value is the current icon (null/undefined =
// unset); onChange receives the picked emoji or null when cleared. Only
// single emoji can be chosen — the grid only offers valid ones and the
// server re-validates on write.

import { useMemo, useState } from "react";
import { SmilePlus } from "lucide-react";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";
import { ScrollArea } from "../ui/scroll-area";
import { EMOJI_LIST, isSingleEmoji } from "../../lib/emoji";
import { cn } from "cn";

interface EmojiPickerProps {
  value?: string | null;
  onChange: (icon: string | null) => void;
  disabled?: boolean;
  /** Accessible label for the trigger button. */
  label?: string;
}

export default function EmojiPicker({
  value,
  onChange,
  disabled,
  label = "Choose project icon",
}: EmojiPickerProps) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");

  const entries = useMemo(() => {
    // Defensive: never offer an entry the server would reject, even if
    // the curated list gains a bad one later.
    const valid = EMOJI_LIST.filter((e) => isSingleEmoji(e.emoji));
    const q = query.trim().toLowerCase();
    if (!q) return valid;
    return valid.filter(
      (e) =>
        e.name.includes(q) ||
        e.keywords.some((k) => k.includes(q)) ||
        e.emoji === q,
    );
  }, [query]);

  function pick(emoji: string) {
    onChange(emoji);
    setOpen(false);
    setQuery("");
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        render={
          <Button
            type="button"
            variant="outline"
            disabled={disabled}
            aria-label={label}
            className={cn(
              "size-11 shrink-0 p-0 text-2xl leading-none",
              !value && "border-dashed text-muted-foreground",
            )}
          />
        }
      >
        {value ? (
          <span aria-hidden="true">{value}</span>
        ) : (
          <SmilePlus className="size-5" aria-hidden="true" />
        )}
      </PopoverTrigger>
      <PopoverContent align="start" className="w-80 p-0">
        <div className="border-b p-2">
          <Input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search emoji…"
            aria-label="Search emoji"
            className="h-8"
          />
        </div>
        <ScrollArea className="h-56">
          {entries.length === 0 ? (
            <p className="p-4 text-center text-sm text-muted-foreground">
              No emoji match.
            </p>
          ) : (
            <div
              className="grid grid-cols-8 gap-0.5 p-2"
              role="listbox"
              aria-label="Emoji choices"
            >
              {entries.map((e) => (
                <button
                  key={e.emoji}
                  type="button"
                  role="option"
                  aria-selected={value === e.emoji}
                  aria-label={e.name}
                  title={e.name}
                  onClick={() => pick(e.emoji)}
                  className={cn(
                    "flex aspect-square items-center justify-center rounded-md text-xl transition-colors hover:bg-accent",
                    value === e.emoji && "bg-accent ring-1 ring-ring",
                  )}
                >
                  {e.emoji}
                </button>
              ))}
            </div>
          )}
        </ScrollArea>
        {value && (
          <div className="border-t p-2">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="w-full"
              onClick={() => {
                onChange(null);
                setOpen(false);
                setQuery("");
              }}
            >
              Remove icon
            </Button>
          </div>
        )}
      </PopoverContent>
    </Popover>
  );
}
