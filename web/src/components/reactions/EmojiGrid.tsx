// Shared emoji picker grid (C67T1): the curated popover grid from
// PageReactions, extracted so issue/comment reactions reuse it instead
// of forking a second copy. Search filters by name, keywords, or the
// emoji itself; only single-emoji entries (per the server contract) are
// shown.

import { useMemo, useState } from "react";
import { EMOJI_LIST, isSingleEmoji, type EmojiEntry } from "../../lib/emoji";
import { Input } from "../ui/input";
import { ScrollArea } from "../ui/scroll-area";

export default function EmojiGrid({
  onPick,
  disabled,
}: {
  onPick: (emoji: string) => void;
  disabled?: boolean;
}) {
  const [query, setQuery] = useState("");
  const entries = useMemo(() => {
    const valid = EMOJI_LIST.filter((e: EmojiEntry) => isSingleEmoji(e.emoji));
    const q = query.trim().toLowerCase();
    if (!q) return valid;
    return valid.filter(
      (e) =>
        e.name.includes(q) ||
        e.keywords.some((k) => k.includes(q)) ||
        e.emoji === q,
    );
  }, [query]);

  return (
    <>
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
                aria-label={e.name}
                title={e.name}
                disabled={disabled}
                onClick={() => onPick(e.emoji)}
                className="flex aspect-square items-center justify-center rounded-md text-xl transition-colors hover:bg-accent"
              >
                {e.emoji}
              </button>
            ))}
          </div>
        )}
      </ScrollArea>
    </>
  );
}
