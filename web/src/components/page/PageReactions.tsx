// Page reactions (C24T3): the reaction row rendered under the wiki
// page view, next to the page comments. Each pill shows an emoji plus
// its count (13px base), highlighted when the caller reacted; the +
// button opens the curated emoji popover (same grid as EmojiPicker).
//
// The read side is the reactions[] embedded on GET page detail (same
// ReactionGroup shape as issue reactions); toggles POST/DELETE
// .../reactions?emoji= with an optimistic update and a refetch on
// error. The server enforces everything; the UI only hides what would
// 403 (write controls show for member (15)+, mirroring PageComments).

import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { SmilePlus } from "lucide-react";
import { api, ApiError } from "../../lib/api";
import { useAuth } from "../../lib/auth";
import { EMOJI_LIST, isSingleEmoji, type EmojiEntry } from "../../lib/emoji";
import type { Member, Page, ReactionGroup } from "../../lib/types";
import { Alert, AlertDescription } from "../ui/alert";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";
import { ScrollArea } from "../ui/scroll-area";
import { cn } from "../../lib/utils";

const ROLE_MEMBER = 15;

export function sortGroups(groups: ReactionGroup[]): ReactionGroup[] {
  // Server order: count DESC, emoji ASC. The emoji tiebreak here uses
  // code-unit order (deterministic; the DB collation may differ in
  // exotic cases, but ties among equal counts are cosmetic and the
  // next refetch settles them to the server truth).
  return [...groups].sort(
    (a, b) =>
      b.count - a.count ||
      (a.emoji < b.emoji ? -1 : a.emoji > b.emoji ? 1 : 0),
  );
}

/** Apply one toggle optimistically, mirroring the server's grouping. */
export function toggleGroup(
  groups: ReactionGroup[],
  emoji: string,
  add: boolean,
): ReactionGroup[] {
  const existing = groups.find((g) => g.emoji === emoji);
  if (add) {
    if (existing?.reacted) return groups;
    if (existing) {
      return sortGroups(
        groups.map((g) =>
          g.emoji === emoji
            ? { ...g, count: g.count + 1, reacted: true }
            : g,
        ),
      );
    }
    return sortGroups([...groups, { emoji, count: 1, reacted: true }]);
  }
  if (!existing?.reacted) return groups;
  const next = groups
    .map((g) =>
      g.emoji === emoji
        ? { ...g, count: g.count - 1, reacted: false }
        : g,
    )
    .filter((g) => g.count > 0);
  return sortGroups(next);
}

function EmojiGrid({
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

export default function PageReactions({
  slug,
  identifier,
  pageId,
}: {
  slug: string;
  identifier: string;
  pageId: string;
}) {
  const { user } = useAuth();
  const queryClient = useQueryClient();
  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/pages/${encodeURIComponent(pageId)}`;
  const detailKey = ["page-detail", slug, identifier, pageId];

  const detailQuery = useQuery({
    queryKey: detailKey,
    queryFn: () => api.get<Page>(base).then((d) => d),
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

  const myRole =
    membersQuery.data?.find((m) => m.id === user?.id)?.role ?? 0;
  const canWrite = myRole >= ROLE_MEMBER;

  const [pickerOpen, setPickerOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const groups = detailQuery.data?.reactions ?? [];

  function optimisticToggle(emoji: string, add: boolean) {
    queryClient.setQueryData<Page>(detailKey, (old) => {
      if (!old) return old;
      return { ...old, reactions: toggleGroup(old.reactions ?? [], emoji, add) };
    });
  }

  const toggleMutation = useMutation({
    mutationFn: ({ emoji, add }: { emoji: string; add: boolean }) =>
      add
        ? api.post(`${base}/reactions?emoji=${encodeURIComponent(emoji)}`, null)
        : api.del(`${base}/reactions?emoji=${encodeURIComponent(emoji)}`),
    onMutate: ({ emoji, add }) => {
      setError(null);
      optimisticToggle(emoji, add);
    },
    onError: (e) => {
      setError(
        e instanceof ApiError ? e.message : "Failed to update reaction",
      );
      void queryClient.invalidateQueries({ queryKey: detailKey });
    },
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: detailKey });
    },
  });

  function toggle(g: ReactionGroup) {
    if (!canWrite || toggleMutation.isPending) return;
    toggleMutation.mutate({ emoji: g.emoji, add: !g.reacted });
  }

  function pick(emoji: string) {
    setPickerOpen(false);
    if (!canWrite || toggleMutation.isPending) return;
    // Picking an already-reacted emoji is a no-op on the server too;
    // only add here so the picker never silently removes a reaction.
    const existing = groups.find((g) => g.emoji === emoji);
    if (existing?.reacted) return;
    toggleMutation.mutate({ emoji, add: true });
  }

  if (!detailQuery.data && detailQuery.isLoading) return null;
  if (groups.length === 0 && !canWrite) return null;

  return (
    <div className="mt-3">
      {error && (
        <Alert variant="destructive" className="mb-2">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      <div className="flex flex-wrap items-center gap-1.5" aria-label="Reactions">
        {groups.map((g) => (
          <button
            key={g.emoji}
            type="button"
            onClick={() => toggle(g)}
            disabled={!canWrite || toggleMutation.isPending}
            title={g.reacted ? "Remove your reaction" : "React"}
            aria-pressed={g.reacted}
            className={cn(
              "flex items-center gap-1 rounded-full border px-2 py-0.5 text-[13px] leading-6 transition-colors",
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
                  className="h-7 rounded-full px-2 text-muted-foreground hover:text-foreground"
                />
              }
            >
              <SmilePlus className="size-4" aria-hidden="true" />
            </PopoverTrigger>
            <PopoverContent align="start" className="w-80 p-0">
              <EmojiGrid
                onPick={pick}
                disabled={toggleMutation.isPending}
              />
            </PopoverContent>
          </Popover>
        )}
      </div>
    </div>
  );
}
