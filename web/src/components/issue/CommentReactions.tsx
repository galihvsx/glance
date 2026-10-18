// Comment-level reactions (C67T1): chips + a small picker trigger on
// a comment node (top-level and replies). Contract (verified against
// the backend this cycle):
//   POST   .../comments/{commentID}/reactions  JSON body {emoji}
//   DELETE .../comments/{commentID}/reactions?emoji=
// The `reactions` groups ride on the Comment JSON (always `[]`, never
// null — C67T0); the comments query is invalidated on toggle so the
// feed settles to the server truth. The member gate (member 15+) is
// decided by the parent (ActivitySection) and passed as `canWrite`.
// Optimistic updates reuse the shared lib/reactions helpers; the row
// is the shared ReactionRow.

import { useEffect, useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../../lib/api";
import { normalizeGroups, sortGroups, toggleGroup } from "../../lib/reactions";
import type { CommentNode, ReactionGroup } from "../../lib/types";
import ReactionRow from "../reactions/ReactionRow";

export default function CommentReactions({
  slug,
  identifier,
  uuid,
  comment,
  canWrite,
}: {
  slug: string;
  identifier: string;
  uuid: string;
  comment: CommentNode;
  canWrite: boolean;
}) {
  const queryClient = useQueryClient();
  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/issues/${encodeURIComponent(uuid)}`;
  const reactionsPath = `${base}/comments/${encodeURIComponent(comment.id)}/reactions`;

  const [error, setError] = useState<string | null>(null);
  const [groups, setGroups] = useState<ReactionGroup[]>(() =>
    sortGroups(normalizeGroups(comment.reactions)),
  );

  const toggleMutation = useMutation({
    mutationFn: ({ emoji, add }: { emoji: string; add: boolean }) =>
      add
        ? api.post(reactionsPath, { emoji })
        : api.del(`${reactionsPath}?emoji=${encodeURIComponent(emoji)}`),
    onMutate: ({ emoji, add }) => {
      setError(null);
      setGroups((prev) => toggleGroup(prev, emoji, add));
    },
    onError: (e) => {
      setError(e instanceof ApiError ? e.message : "Failed to update reaction");
      // Revert to the last server truth; the invalidate below refetches it.
      setGroups(sortGroups(normalizeGroups(comment.reactions)));
      void queryClient.invalidateQueries({
        queryKey: ["comments", slug, identifier, uuid],
      });
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["comments", slug, identifier, uuid],
      });
    },
  });

  // Settle to the server truth when the parent hands us a new
  // comment object (e.g. after the comments query refetch). Only the
  // prop changing triggers a resync — never clobber an in-flight
  // optimistic update when the mutation settles.
  const propReactionsRef = useRef(comment.reactions);
  useEffect(() => {
    if (comment.reactions !== propReactionsRef.current) {
      propReactionsRef.current = comment.reactions;
      setGroups(sortGroups(normalizeGroups(comment.reactions)));
    }
  }, [comment.reactions]);

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

  if (groups.length === 0 && !canWrite) return null;

  return (
    <ReactionRow
      groups={groups}
      canWrite={canWrite}
      pending={toggleMutation.isPending}
      error={error}
      onToggle={toggle}
      onPick={pick}
      size="sm"
    />
  );
}
