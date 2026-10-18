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
//
// Presentation is the shared ReactionRow (C67T1); only the page
// endpoint contract (POST takes ?emoji=, not a JSON body) stays here.

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../../lib/api";
import { useAuth } from "../../lib/auth";
import {
  normalizeGroups,
  sortGroups,
  toggleGroup,
} from "../../lib/reactions";
import type { Member, Page, ReactionGroup } from "../../lib/types";
import ReactionRow from "../reactions/ReactionRow";

// Re-exported for the C24T3 unit tests (and any other importers);
// the implementation lives in lib/reactions, shared with the
// issue/comment reaction surfaces (C67T1).
export { sortGroups, toggleGroup };

const ROLE_MEMBER = 15;

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

  const [error, setError] = useState<string | null>(null);

  const groups = sortGroups(normalizeGroups(detailQuery.data?.reactions));

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
    <ReactionRow
      groups={groups}
      canWrite={canWrite}
      pending={toggleMutation.isPending}
      error={error}
      onToggle={toggle}
      onPick={pick}
    />
  );
}
